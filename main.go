package main

import (
	"context"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/TheInternetUse7/tools/internal/hub"
	"github.com/TheInternetUse7/tools/internal/limiter"
	"github.com/TheInternetUse7/tools/internal/tools"
	"github.com/TheInternetUse7/tools/internal/tools/gif"
	"github.com/TheInternetUse7/tools/web"
)

func main() {
	log.Println("Starting tools suite...")

	// 1. Environment & configuration setup
	port := getEnv("PORT", "8080")
	maxConcurrent := getEnvInt("MAX_CONCURRENT_JOBS", 2)
	rateLimitReqs := getEnvInt("RATE_LIMIT_REQUESTS", 5)
	rateLimitWindow := getEnvDuration("RATE_LIMIT_WINDOW", 10*time.Minute)
	maxUploadMB := getEnvInt64("MAX_UPLOAD_SIZE_MB", 100)
	maxDurationSec := getEnvFloat("MAX_DURATION_SECONDS", 20.0)

	scratchDir := os.Getenv("SCRATCH_DIR")
	if scratchDir == "" {
		scratchDir = filepath.Join(os.TempDir(), "tools-scratch")
	}

	if err := os.MkdirAll(scratchDir, 0700); err != nil {
		log.Fatalf("Failed to create temporary scratch directory %s: %v", scratchDir, err)
	}

	// 2. Sweep orphaned temporary files from previous unexpected shutdowns
	cleaned := gif.SweepOrphanedFiles(scratchDir)
	if cleaned > 0 {
		log.Printf("Cleaned %d orphaned temp files from %s", cleaned, scratchDir)
	}

	// 3. Verify FFmpeg availability
	if gif.CheckFFmpeg() {
		log.Println("FFmpeg binary detected and ready")
	} else {
		log.Println("WARNING: ffmpeg not found in PATH! Video conversions will fail until installed.")
	}

	// 4. Parse embedded web templates
	tmplFuncs := template.FuncMap{
		"safe": func(s string) template.HTML {
			return template.HTML(s)
		},
	}
	tmpl, err := template.New("").Funcs(tmplFuncs).ParseFS(web.TemplateFS, "templates/*.html")
	if err != nil {
		log.Fatalf("Failed to parse embedded HTML templates: %v", err)
	}

	// 5. Initialize shared resource limiters
	concLimiter := limiter.NewConcurrencyLimiter(maxConcurrent)
	ipLimiter := limiter.NewIPRateLimiter(rateLimitReqs, rateLimitWindow)
	defer ipLimiter.Stop()

	// 6. Tool Registry & modular tools registration
	registry := tools.NewRegistry()

	gifTool := gif.NewTool(gif.Config{
		ScratchDir:     scratchDir,
		MaxUploadBytes: maxUploadMB * 1024 * 1024,
		MaxDuration:    maxDurationSec,
		Timeout:        90 * time.Second,
		Limiter:        concLimiter,
		IPLimiter:      ipLimiter,
		Tmpl:           tmpl,
		GetClientIP:    hub.GetClientIP,
	})
	registry.Register(gifTool)

	// 7. Router setup
	mux := http.NewServeMux()
	h := hub.New(registry, concLimiter, tmpl)
	h.RegisterRoutes(mux)

	// Wrap root router with request logging
	handler := loggingMiddleware(mux)

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// 8. Server startup & graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("Server listening on http://0.0.0.0:%s (%d concurrent slots, %d req/%v rate limit)",
			port, maxConcurrent, rateLimitReqs, rateLimitWindow)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server ListenAndServe failed: %v", err)
		}
	}()

	<-stop
	log.Println("Shutdown signal received, shutting down gracefully...")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Printf("Graceful shutdown error: %v", err)
	}

	log.Println("Server stopped.")
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		if r.URL.Path != "/healthz" {
			log.Printf("%s %s %s (%v)", r.Method, r.URL.Path, hub.GetClientIP(r), time.Since(start))
		}
	})
}

func getEnv(key, def string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return def
}

func getEnvInt(key string, def int) int {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.Atoi(val); err == nil && i > 0 {
			return i
		}
	}
	return def
}

func getEnvInt64(key string, def int64) int64 {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.ParseInt(val, 10, 64); err == nil && i > 0 {
			return i
		}
	}
	return def
}

func getEnvFloat(key string, def float64) float64 {
	if val := os.Getenv(key); val != "" {
		if f, err := strconv.ParseFloat(val, 64); err == nil && f > 0 {
			return f
		}
	}
	return def
}

func getEnvDuration(key string, def time.Duration) time.Duration {
	if val := os.Getenv(key); val != "" {
		if d, err := time.ParseDuration(val); err == nil && d > 0 {
			return d
		}
	}
	return def
}
