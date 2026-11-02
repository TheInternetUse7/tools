package hub

import (
	"encoding/json"
	"html/template"
	"log"
	"net"
	"net/http"
	"strings"

	"github.com/TheInternetUse7/tools/internal/limiter"
	"github.com/TheInternetUse7/tools/internal/tools"
	"github.com/TheInternetUse7/tools/internal/tools/gif"
)

// Hub coordinates top-level routing, shared dashboard, and health monitoring.
type Hub struct {
	registry *tools.Registry
	limiter  *limiter.ConcurrencyLimiter
	tmpl     *template.Template
}

// New creates a new Hub instance.
func New(registry *tools.Registry, lim *limiter.ConcurrencyLimiter, tmpl *template.Template) *Hub {
	return &Hub{
		registry: registry,
		limiter:  lim,
		tmpl:     tmpl,
	}
}

// RegisterRoutes registers top-level and tool-specific routes into the router.
func (h *Hub) RegisterRoutes(mux *http.ServeMux) {
	// Top-level hub routes
	mux.HandleFunc("GET /{$}", h.handleDashboard)
	mux.HandleFunc("GET /healthz", h.handleHealthz)

	// Register each modular tool
	for _, t := range h.registry.All() {
		t.RegisterRoutes(mux)
	}
}

func (h *Hub) handleDashboard(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if sh, _, err := net.SplitHostPort(host); err == nil {
		host = sh
	}
	if host == "localhost" || host == "127.0.0.1" || host == "0.0.0.0" || host == "" {
		host = "tools"
	}

	data := struct {
		Host  string
		Tools []tools.Tool
	}{
		Host:  host,
		Tools: h.registry.All(),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, "index.html", data); err != nil {
		log.Printf("Failed rendering dashboard template: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

func (h *Hub) handleHealthz(w http.ResponseWriter, r *http.Request) {
	activeJobs := 0
	maxJobs := 1
	if h.limiter != nil {
		activeJobs = h.limiter.Active()
		maxJobs = h.limiter.Max()
	}

	ffmpegAvailable := gif.CheckFFmpeg()

	w.Header().Set("Content-Type", "application/json")
	if !ffmpegAvailable {
		w.WriteHeader(http.StatusServiceUnavailable)
	} else {
		w.WriteHeader(http.StatusOK)
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":              "ok",
		"ffmpeg":              ffmpegAvailable,
		"active_jobs":         activeJobs,
		"max_concurrent_jobs": maxJobs,
		"tools_count":         h.registry.Count(),
	})
}

// GetClientIP extracts the real client IP address prioritizing Cloudflare headers.
func GetClientIP(r *http.Request) string {
	// 1. Cloudflare Connecting IP (from Cloudflare Tunnel / Proxy)
	if cfIP := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); cfIP != "" {
		return cfIP
	}

	// 2. Standard X-Forwarded-For header
	if xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); xff != "" {
		parts := strings.Split(xff, ",")
		if len(parts) > 0 {
			ip := strings.TrimSpace(parts[0])
			if ip != "" {
				return ip
			}
		}
	}

	// 3. Fallback to direct TCP connection address
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}

	return r.RemoteAddr
}
