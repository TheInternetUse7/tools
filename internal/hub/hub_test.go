package hub_test

import (
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TheInternetUse7/tools/internal/hub"
	"github.com/TheInternetUse7/tools/internal/limiter"
	"github.com/TheInternetUse7/tools/internal/tools"
	"github.com/TheInternetUse7/tools/web"
)

func TestGetClientIP(t *testing.T) {
	// 1. CF-Connecting-IP should take priority
	req1 := httptest.NewRequest("GET", "/", nil)
	req1.Header.Set("CF-Connecting-IP", "203.0.113.195")
	req1.Header.Set("X-Forwarded-For", "198.51.100.1")
	req1.RemoteAddr = "127.0.0.1:54321"

	if ip := hub.GetClientIP(req1); ip != "203.0.113.195" {
		t.Fatalf("expected CF-Connecting-IP '203.0.113.195', got %q", ip)
	}

	// 2. X-Forwarded-For fallback
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.Header.Set("X-Forwarded-For", "198.51.100.1, 10.0.0.1")
	req2.RemoteAddr = "127.0.0.1:54321"

	if ip := hub.GetClientIP(req2); ip != "198.51.100.1" {
		t.Fatalf("expected first X-Forwarded-For '198.51.100.1', got %q", ip)
	}

	// 3. RemoteAddr fallback
	req3 := httptest.NewRequest("GET", "/", nil)
	req3.RemoteAddr = "192.168.1.50:12345"

	if ip := hub.GetClientIP(req3); ip != "192.168.1.50" {
		t.Fatalf("expected RemoteAddr host '192.168.1.50', got %q", ip)
	}
}

func TestHubEndpoints(t *testing.T) {
	tmplFuncs := template.FuncMap{
		"safe": func(s string) template.HTML {
			return template.HTML(s)
		},
	}
	tmpl, err := template.New("").Funcs(tmplFuncs).ParseFS(web.TemplateFS, "templates/*.html")
	if err != nil {
		t.Fatalf("failed to parse templates: %v", err)
	}

	registry := tools.NewRegistry()
	concLimiter := limiter.NewConcurrencyLimiter(2)
	h := hub.New(registry, concLimiter, tmpl)

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	// Test GET /
	reqDash := httptest.NewRequest("GET", "/", nil)
	recDash := httptest.NewRecorder()
	mux.ServeHTTP(recDash, reqDash)

	if recDash.Code != http.StatusOK {
		t.Fatalf("expected / to return 200, got %d", recDash.Code)
	}
	if recDash.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("expected text/html content type, got %s", recDash.Header().Get("Content-Type"))
	}

	// Test GET /healthz
	reqHealth := httptest.NewRequest("GET", "/healthz", nil)
	recHealth := httptest.NewRecorder()
	mux.ServeHTTP(recHealth, reqHealth)

	if recHealth.Code != http.StatusOK {
		t.Fatalf("expected /healthz to return 200, got %d", recHealth.Code)
	}

	var healthRes map[string]any
	if err := json.Unmarshal(recHealth.Body.Bytes(), &healthRes); err != nil {
		t.Fatalf("failed to parse healthz json: %v", err)
	}
	if healthRes["status"] != "ok" {
		t.Fatalf("expected status 'ok', got %v", healthRes["status"])
	}
}
