package web

import (
	"io/fs"
	"strings"
	"testing"
)

func TestEmbeddedStaticAssets(t *testing.T) {
	required := []string{
		"static/index.html",
		"static/manifest.json",
		"static/sw.js",
	}

	for _, path := range required {
		data, err := fs.ReadFile(StaticFS, path)
		if err != nil {
			t.Fatalf("expected embedded file %s to exist, got error: %v", path, err)
		}
		if len(data) == 0 {
			t.Fatalf("expected embedded file %s to be non-empty", path)
		}
	}

	indexHTML, _ := fs.ReadFile(StaticFS, "static/index.html")
	if !strings.Contains(string(indexHTML), "Homebox AI Inventory") {
		t.Errorf("index.html missing expected title 'Homebox AI Inventory'")
	}
	if !strings.Contains(string(indexHTML), "/api/analyze") || !strings.Contains(string(indexHTML), "/api/commit") {
		t.Errorf("index.html missing expected API endpoints")
	}
}
