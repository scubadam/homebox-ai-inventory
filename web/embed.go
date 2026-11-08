package web

import "embed"

// StaticFS embeds the Progressive Web App static assets (index.html, manifest.json, sw.js).
//
//go:embed static/*
var StaticFS embed.FS
