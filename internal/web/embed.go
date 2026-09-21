package web

import "embed"

// StaticFS holds the embedded web assets (HTML, CSS, JS).
//
//go:embed static/*
var StaticFS embed.FS
