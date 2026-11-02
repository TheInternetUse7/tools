package tools

import (
	"html/template"
	"net/http"
)

// Tool defines the interface that all modular tools must implement.
type Tool interface {
	// ID returns the unique identifier for the tool (e.g. "gif").
	ID() string
	// Title returns the human-readable display name (e.g. "Video to GIF").
	Title() string
	// Description returns a concise summary of what the tool does.
	Description() string
	// Icon returns an SVG markup string or emoji to render on dashboard cards.
	Icon() template.HTML
	// Category returns the functional category (e.g. "Media", "Utilities", "Text").
	Category() string
	// Path returns the URL path where the tool is mounted (e.g. "/gif").
	Path() string
	// RegisterRoutes hooks the tool's HTTP endpoints into the central router.
	RegisterRoutes(mux *http.ServeMux)
}

// Registry manages the set of available tools across the suite.
type Registry struct {
	tools []Tool
}

// NewRegistry creates a new empty Tool Registry.
func NewRegistry() *Registry {
	return &Registry{
		tools: make([]Tool, 0),
	}
}

// Register adds a tool to the registry.
func (r *Registry) Register(t Tool) {
	r.tools = append(r.tools, t)
}

// All returns a slice of all registered tools.
func (r *Registry) All() []Tool {
	return r.tools
}

// Count returns the number of active tools.
func (r *Registry) Count() int {
	return len(r.tools)
}
