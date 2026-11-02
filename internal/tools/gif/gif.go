package gif

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/TheInternetUse7/tools/internal/limiter"
)

// Config holds configuration parameters for the GIF tool.
type Config struct {
	ScratchDir     string
	MaxUploadBytes int64
	MaxDuration    float64
	Timeout        time.Duration
	Limiter        *limiter.ConcurrencyLimiter
	IPLimiter      *limiter.IPRateLimiter
	Tmpl           *template.Template
	GetClientIP    func(r *http.Request) string
}

// Tool implements tools.Tool for Video to GIF conversions.
type Tool struct {
	cfg Config
}

// NewTool creates a new GIF tool instance.
func NewTool(cfg Config) *Tool {
	if cfg.MaxUploadBytes <= 0 {
		cfg.MaxUploadBytes = 100 * 1024 * 1024 // 100MB
	}
	if cfg.MaxDuration <= 0 {
		cfg.MaxDuration = 20.0
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 90 * time.Second
	}
	return &Tool{cfg: cfg}
}

func (t *Tool) ID() string          { return "gif" }
func (t *Tool) Title() string       { return "Video to GIF Converter" }
func (t *Tool) Description() string { return "Convert short video clips into GIFs." }
func (t *Tool) Category() string    { return "Media" }
func (t *Tool) Path() string        { return "/gif" }

func (t *Tool) Icon() template.HTML {
	return template.HTML(`<svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
		<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 10l4.553-2.276A1 1 0 0121 8.618v6.764a1 1 0 01-1.447.894L15 14M5 18h8a2 2 0 002-2V8a2 2 0 00-2-2H5a2 2 0 00-2 2v8a2 2 0 002 2z"/>
	</svg>`)
}

func (t *Tool) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /gif", t.handlePage)
	mux.HandleFunc("POST /gif/convert", t.handleConvert)
}

func (t *Tool) handlePage(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if sh, _, err := net.SplitHostPort(host); err == nil {
		host = sh
	}
	if host == "localhost" || host == "127.0.0.1" || host == "0.0.0.0" || host == "" {
		host = "tools"
	}

	data := struct {
		Host string
	}{
		Host: host,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.cfg.Tmpl.ExecuteTemplate(w, "gif.html", data); err != nil {
		log.Printf("Template render error: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

func (t *Tool) handleConvert(w http.ResponseWriter, r *http.Request) {
	// 1. IP Rate Limiting check
	clientIP := "unknown"
	if t.cfg.GetClientIP != nil {
		clientIP = t.cfg.GetClientIP(r)
	}
	if t.cfg.IPLimiter != nil {
		allowed, _, resetIn := t.cfg.IPLimiter.Allow(clientIP)
		if !allowed {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]string{
				"error": fmt.Sprintf("Rate limit reached. You can convert another video in %d seconds.", int(resetIn.Seconds())),
			})
			return
		}
	}

	// 2. Concurrency Semaphore check
	if t.cfg.Limiter != nil {
		if !t.cfg.Limiter.TryAcquire() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "The server is currently busy converting other videos. Please try again in a few moments.",
			})
			return
		}
		defer t.cfg.Limiter.Release()
	}

	// 3. Limit total request body to protect RAM and disk
	r.Body = http.MaxBytesReader(w, r.Body, t.cfg.MaxUploadBytes)

	mr, err := r.MultipartReader()
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid multipart request")
		return
	}

	// Defaults
	start := 0.0
	duration := 5.0
	width := 480
	fps := 15
	colors := 128
	dither := "bayer"
	lossy := 0

	nonce := randomString(8)
	tempInPath := filepath.Join(t.cfg.ScratchDir, fmt.Sprintf("in-%d-%s.tmp", time.Now().UnixNano(), nonce))
	tempOutPath := filepath.Join(t.cfg.ScratchDir, fmt.Sprintf("out-%d-%s.gif", time.Now().UnixNano(), nonce))
	tempLossyPath := filepath.Join(t.cfg.ScratchDir, fmt.Sprintf("lossy-%d-%s.gif", time.Now().UnixNano(), nonce))

	// Ensure cleanup in all execution paths
	var inputCreated, outputCreated, lossyCreated bool
	defer func() {
		if inputCreated {
			_ = os.Remove(tempInPath)
		}
		if outputCreated {
			_ = os.Remove(tempOutPath)
		}
		if lossyCreated {
			_ = os.Remove(tempLossyPath)
		}
	}()

	var videoFound bool

	// Stream multipart parts directly to disk / parse form parameters
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			if strings.Contains(err.Error(), "request body too large") {
				writeJSONError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("Uploaded video exceeds maximum allowed size (%d MB)", t.cfg.MaxUploadBytes/(1024*1024)))
				return
			}
			writeJSONError(w, http.StatusBadRequest, "Failed reading upload form")
			return
		}

		formName := part.FormName()
		if formName == "video" && !videoFound {
			// Retain valid video container extension so FFmpeg accurately probes demuxer
			ext := strings.ToLower(filepath.Ext(part.FileName()))
			if ext == "" || len(ext) > 6 {
				ext = ".mp4"
			}
			tempInPath = filepath.Join(t.cfg.ScratchDir, fmt.Sprintf("in-%d-%s%s", time.Now().UnixNano(), nonce, ext))

			// Stream uploaded video directly to temp file on disk (ZERO memory buffering)
			dst, err := os.OpenFile(tempInPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
			if err != nil {
				log.Printf("Failed to create temporary video file: %v", err)
				writeJSONError(w, http.StatusInternalServerError, "Failed to create scratch file on server")
				return
			}
			inputCreated = true

			copied, copyErr := io.Copy(dst, part)
			dst.Close()

			if copyErr != nil {
				if strings.Contains(copyErr.Error(), "request body too large") {
					writeJSONError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("Uploaded video exceeds maximum allowed size (%d MB)", t.cfg.MaxUploadBytes/(1024*1024)))
					return
				}
				log.Printf("Streaming upload failed: %v", copyErr)
				writeJSONError(w, http.StatusInternalServerError, "Failed saving uploaded video to disk")
				return
			}

			if copied == 0 {
				writeJSONError(w, http.StatusBadRequest, "Uploaded video file is empty (0 bytes). Please select a valid video.")
				return
			}
			videoFound = true
		} else {
			// Form values (start, duration, width, fps)
			valBytes, err := io.ReadAll(io.LimitReader(part, 1024))
			if err == nil {
				val := strings.TrimSpace(string(valBytes))
				switch formName {
				case "start":
					if s, err := strconv.ParseFloat(val, 64); err == nil && s >= 0 {
						start = s
					}
				case "duration":
					if d, err := strconv.ParseFloat(val, 64); err == nil && d > 0 {
						if d > t.cfg.MaxDuration {
							d = t.cfg.MaxDuration
						}
						duration = d
					}
				case "width":
					if w, err := strconv.Atoi(val); err == nil && (w == 0 || (w >= 100 && w <= 1920)) {
						width = w
					}
				case "fps":
					if f, err := strconv.Atoi(val); err == nil && f >= 5 && f <= 30 {
						fps = f
					}
				case "colors":
					if c, err := strconv.Atoi(val); err == nil && (c == 64 || c == 128 || c == 256) {
						colors = c
					}
				case "dither":
					valLower := strings.ToLower(val)
					if valLower == "bayer" || valLower == "floyd_steinberg" || valLower == "none" {
						dither = valLower
					}
				case "lossy":
					if l, err := strconv.Atoi(val); err == nil && l >= 0 && l <= 100 {
						lossy = l
					}
				}
			}
		}
	}

	if !videoFound {
		writeJSONError(w, http.StatusBadRequest, "No video file provided in form field 'video'")
		return
	}

	// 4. Run FFmpeg pipeline
	ctx, cancel := context.WithTimeout(r.Context(), t.cfg.Timeout)
	defer cancel()

	ditherFilter := "bayer:bayer_scale=5"
	switch dither {
	case "floyd_steinberg":
		ditherFilter = "floyd_steinberg"
	case "none":
		ditherFilter = "none"
	case "bayer":
		ditherFilter = "bayer:bayer_scale=5"
	}

	scaleFilter := ""
	if width > 0 {
		scaleFilter = fmt.Sprintf("scale=%d:-1:flags=lanczos,", width)
	}

	filterComplex := fmt.Sprintf(
		"fps=%d,%ssplit[s0][s1];[s0]palettegen=max_colors=%d:stats_mode=diff[p];[s1][p]paletteuse=dither=%s",
		fps, scaleFilter, colors, ditherFilter,
	)

	args := []string{
		"-y",
		"-ss", fmt.Sprintf("%.3f", start),
		"-t", fmt.Sprintf("%.3f", duration),
		"-i", tempInPath,
		"-vf", filterComplex,
		"-f", "gif",
		tempOutPath,
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	startConvertTime := time.Now()
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			log.Printf("FFmpeg timed out after %v", t.cfg.Timeout)
			writeJSONError(w, http.StatusGatewayTimeout, "Conversion timed out. Try a shorter duration or lower resolution.")
			return
		}
		log.Printf("FFmpeg failed: %v | stderr: %s", err, stderr.String())
		writeJSONError(w, http.StatusInternalServerError, "FFmpeg failed to process video clip. Please check file format and timestamps.")
		return
	}
	outputCreated = true

	finalPath := tempOutPath

	// 5. Apply Gifsicle lossy compression if requested
	if lossy > 0 {
		if gifsicleBin, err := exec.LookPath("gifsicle"); err == nil {
			lossyCmd := exec.CommandContext(ctx, gifsicleBin, "-O3", fmt.Sprintf("--lossy=%d", lossy), "-o", tempLossyPath, tempOutPath)
			if err := lossyCmd.Run(); err == nil {
				lossyCreated = true
				finalPath = tempLossyPath
			} else {
				log.Printf("Gifsicle lossy compression failed, using original: %v", err)
			}
		} else {
			log.Printf("Gifsicle not installed in PATH, skipping lossy compression")
		}
	}

	// 6. Stream generated GIF back to client
	outFile, err := os.Open(finalPath)
	if err != nil {
		log.Printf("Failed to open generated GIF: %v", err)
		writeJSONError(w, http.StatusInternalServerError, "Failed to read generated GIF file")
		return
	}
	defer outFile.Close()

	stat, err := outFile.Stat()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to stat generated GIF")
		return
	}

	elapsed := time.Since(startConvertTime)
	log.Printf("[GIF] Converted for %s in %v (output: %d bytes, w=%d, fps=%d, colors=%d, dither=%s, lossy=%d)", clientIP, elapsed, stat.Size(), width, fps, colors, dither, lossy)

	w.Header().Set("Content-Type", "image/gif")
	w.Header().Set("Content-Disposition", "attachment; filename=\"clip.gif\"")
	w.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.WriteHeader(http.StatusOK)

	if _, err := io.Copy(w, outFile); err != nil {
		log.Printf("Client disconnected or error streaming GIF response: %v", err)
	}
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// Fallback
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b)
}

// CheckFFmpeg verifies that ffmpeg binary is accessible in system PATH.
func CheckFFmpeg() bool {
	cmd := exec.Command("ffmpeg", "-version")
	return cmd.Run() == nil
}

// SweepOrphanedFiles cleans up lingering temporary files from past runs/crashes.
func SweepOrphanedFiles(scratchDir string) int {
	entries, err := os.ReadDir(scratchDir)
	if err != nil {
		return 0
	}
	cleaned := 0
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "in-") || strings.HasPrefix(name, "out-") || strings.HasPrefix(name, "lossy-") {
			fullPath := filepath.Join(scratchDir, name)
			if err := os.Remove(fullPath); err == nil {
				cleaned++
			}
		}
	}
	return cleaned
}
