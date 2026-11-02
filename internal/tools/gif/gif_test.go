package gif_test

import (
	"bytes"
	"encoding/json"
	"html/template"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/TheInternetUse7/tools/internal/limiter"
	"github.com/TheInternetUse7/tools/internal/tools/gif"
	"github.com/TheInternetUse7/tools/web"
)

func TestGIFToolRoutes(t *testing.T) {
	tmplFuncs := template.FuncMap{
		"safe": func(s string) template.HTML {
			return template.HTML(s)
		},
	}
	tmpl, err := template.New("").Funcs(tmplFuncs).ParseFS(web.TemplateFS, "templates/*.html")
	if err != nil {
		t.Fatalf("failed to parse templates: %v", err)
	}

	scratchDir := filepath.Join(os.TempDir(), "tools-test-scratch")
	_ = os.MkdirAll(scratchDir, 0700)
	defer os.RemoveAll(scratchDir)

	concLimiter := limiter.NewConcurrencyLimiter(1)
	ipLimiter := limiter.NewIPRateLimiter(2, 5*time.Minute)
	defer ipLimiter.Stop()

	tool := gif.NewTool(gif.Config{
		ScratchDir:     scratchDir,
		MaxUploadBytes: 10 * 1024 * 1024,
		MaxDuration:    15.0,
		Timeout:        30 * time.Second,
		Limiter:        concLimiter,
		IPLimiter:      ipLimiter,
		Tmpl:           tmpl,
		GetClientIP: func(r *http.Request) string {
			return "127.0.0.1"
		},
	})

	mux := http.NewServeMux()
	tool.RegisterRoutes(mux)

	// 1. Test GET /gif
	reqPage := httptest.NewRequest("GET", "/gif", nil)
	recPage := httptest.NewRecorder()
	mux.ServeHTTP(recPage, reqPage)

	if recPage.Code != http.StatusOK {
		t.Fatalf("expected /gif to return 200, got %d", recPage.Code)
	}

	// 2. Test POST /gif/convert with empty form (no video field)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("start", "0")
	_ = writer.WriteField("duration", "3.0")
	_ = writer.WriteField("width", "0")
	_ = writer.WriteField("fps", "10")
	_ = writer.WriteField("colors", "64")
	_ = writer.WriteField("dither", "floyd_steinberg")
	_ = writer.WriteField("lossy", "30")
	_ = writer.Close()

	reqNoVideo := httptest.NewRequest("POST", "/gif/convert", &body)
	reqNoVideo.Header.Set("Content-Type", writer.FormDataContentType())
	recNoVideo := httptest.NewRecorder()
	mux.ServeHTTP(recNoVideo, reqNoVideo)

	if recNoVideo.Code != http.StatusBadRequest {
		t.Fatalf("expected missing video to return 400, got %d", recNoVideo.Code)
	}

	// 3. Test Rate Limiter rejection (IP limiter maxRequests is 2)
	// We made 1 convert request above (which was allowed through rate limiter before failing validation).
	// Let's make a 2nd request:
	req2 := httptest.NewRequest("POST", "/gif/convert", bytes.NewReader(body.Bytes()))
	req2.Header.Set("Content-Type", writer.FormDataContentType())
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	// 2nd request should be allowed into handler (and fail validation with 400)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("expected request 2 to fail validation with 400, got %d", rec2.Code)
	}

	// 3rd request should now be blocked by IP rate limiter (429)
	req3 := httptest.NewRequest("POST", "/gif/convert", bytes.NewReader(body.Bytes()))
	req3.Header.Set("Content-Type", writer.FormDataContentType())
	rec3 := httptest.NewRecorder()
	mux.ServeHTTP(rec3, req3)

	if rec3.Code != http.StatusTooManyRequests {
		t.Fatalf("expected request 3 to be rate limited with 429, got %d", rec3.Code)
	}

	var jsonResp map[string]string
	if err := json.Unmarshal(rec3.Body.Bytes(), &jsonResp); err != nil {
		t.Fatalf("failed parsing rate limit json response: %v", err)
	}
	if jsonResp["error"] == "" {
		t.Fatal("expected error message in rate limit response")
	}
}

func TestGIFSuccessfulConversion(t *testing.T) {
	clipPath := filepath.Join("..", "..", "..", "test_clip.mp4")
	videoData, err := os.ReadFile(clipPath)
	if err != nil {
		t.Skip("skipping real conversion test: test_clip.mp4 not found")
	}

	tmplFuncs := template.FuncMap{
		"safe": func(s string) template.HTML {
			return template.HTML(s)
		},
	}
	tmpl, err := template.New("").Funcs(tmplFuncs).ParseFS(web.TemplateFS, "templates/*.html")
	if err != nil {
		t.Fatalf("failed to parse templates: %v", err)
	}

	scratchDir := filepath.Join(os.TempDir(), "tools-test-scratch-real")
	_ = os.MkdirAll(scratchDir, 0700)
	defer os.RemoveAll(scratchDir)

	concLimiter := limiter.NewConcurrencyLimiter(2)
	ipLimiter := limiter.NewIPRateLimiter(10, 5*time.Minute)
	defer ipLimiter.Stop()

	tool := gif.NewTool(gif.Config{
		ScratchDir:     scratchDir,
		MaxUploadBytes: 10 * 1024 * 1024,
		MaxDuration:    15.0,
		Timeout:        30 * time.Second,
		Limiter:        concLimiter,
		IPLimiter:      ipLimiter,
		Tmpl:           tmpl,
		GetClientIP: func(r *http.Request) string {
			return "127.0.0.1"
		},
	})

	mux := http.NewServeMux()
	tool.RegisterRoutes(mux)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("start", "0")
	_ = writer.WriteField("duration", "1.0")
	_ = writer.WriteField("width", "320")
	_ = writer.WriteField("fps", "10")
	_ = writer.WriteField("colors", "128")
	_ = writer.WriteField("dither", "bayer")

	part, err := writer.CreateFormFile("video", "sample.mp4")
	if err != nil {
		t.Fatalf("failed to create form file: %v", err)
	}
	if _, err := part.Write(videoData); err != nil {
		t.Fatalf("failed to write video data: %v", err)
	}
	_ = writer.Close()

	req := httptest.NewRequest("POST", "/gif/convert", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected conversion to succeed with 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "image/gif" {
		t.Fatalf("expected Content-Type image/gif, got %s", rec.Header().Get("Content-Type"))
	}
	if !bytes.HasPrefix(rec.Body.Bytes(), []byte("GIF89a")) && !bytes.HasPrefix(rec.Body.Bytes(), []byte("GIF87a")) {
		t.Fatalf("response body does not have valid GIF magic header")
	}
}
