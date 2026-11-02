package web

import "embed"

// TemplateFS embeds all HTML templates and web assets.
//
//go:embed templates/*
var TemplateFS embed.FS
