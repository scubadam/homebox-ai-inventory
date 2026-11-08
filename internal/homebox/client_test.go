package homebox

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHomeboxClientEntityTypesAndTree(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/entity-types", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Method == http.MethodPost {
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(EntityType{
				ID:         "type-new",
				Name:       req["name"].(string),
				IsLocation: req["isLocation"].(bool),
			})
			return
		}
		_ = json.NewEncoder(w).Encode([]EntityType{
			{ID: "loc-1", Name: "Plastic Box", IsLocation: true},
			{ID: "item-1", Name: "Cables", IsLocation: false},
			{ID: "item-2", Name: "Tools", IsLocation: false},
		})
	})

	mux.HandleFunc("/api/v1/entities/tree", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{
				"id":   "garage-id",
				"name": "Garage",
				"type": "location",
				"children": []map[string]any{
					{
						"id":       "shelf-id",
						"name":     "Shelf A",
						"type":     "location",
						"children": []map[string]any{},
					},
					{
						"id":   "hammer-id",
						"name": "Hammer",
						"type": "item",
					},
				},
			},
		})
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := NewClient(srv.URL, "https://homebox.example.com", "test-token", "", "")
	ctx := context.Background()

	types, err := client.FetchEntityTypes(ctx)
	if err != nil {
		t.Fatalf("FetchEntityTypes failed: %v", err)
	}
	if len(types.Locations) != 1 || len(types.Items) != 2 {
		t.Errorf("unexpected split: %d locations, %d items", len(types.Locations), len(types.Items))
	}

	created, err := client.CreateEntityType(ctx, "Electronics", false)
	if err != nil {
		t.Fatalf("CreateEntityType failed: %v", err)
	}
	if created.ID != "type-new" || created.Name != "Electronics" {
		t.Errorf("unexpected created entity type: %+v", created)
	}

	tree, err := client.FetchLocationTree(ctx)
	if err != nil {
		t.Fatalf("FetchLocationTree failed: %v", err)
	}
	if len(tree) != 2 {
		t.Fatalf("expected 2 locations in flattened tree, got %d", len(tree))
	}
	if tree[1].DisplayName != "Garage / Shelf A" {
		t.Errorf("expected hierarchical displayName 'Garage / Shelf A', got '%s'", tree[1].DisplayName)
	}
	if client.PublicItemURL("box-123") != "https://homebox.example.com/item/box-123" {
		t.Errorf("unexpected PublicItemURL: %s", client.PublicItemURL("box-123"))
	}
}

func TestHomeboxClientAutoLoginAndMultipartUpload(t *testing.T) {
	loginCount := 0
	uploadCalled := false

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/users/login", func(w http.ResponseWriter, r *http.Request) {
		loginCount++
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "Bearer dynamic-jwt-token"})
	})
	mux.HandleFunc("/api/v1/entities/ent-1/attachments", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer dynamic-jwt-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if r.FormValue("primary") != "true" || r.FormValue("type") != "photo" {
			t.Errorf("unexpected form values: primary=%s, type=%s", r.FormValue("primary"), r.FormValue("type"))
		}
		f, hdr, err := r.FormFile("file")
		if err != nil {
			t.Fatalf("missing file part: %v", err)
		}
		defer f.Close()
		data, _ := io.ReadAll(f)
		if hdr.Filename != "box.jpg" || !strings.EqualFold(string(data), "fake-jpeg-bytes") {
			t.Errorf("unexpected uploaded file: name=%s, data=%s", hdr.Filename, string(data))
		}
		uploadCalled = true
		w.WriteHeader(http.StatusCreated)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := NewClient(srv.URL, "", "", "admin@example.com", "secret")
	err := client.UploadAttachment(context.Background(), "ent-1", "box.jpg", "image/jpeg", []byte("fake-jpeg-bytes"), true)
	if err != nil {
		t.Fatalf("UploadAttachment failed: %v", err)
	}
	if loginCount != 1 || !uploadCalled {
		t.Errorf("expected loginCount=1 (got %d) and uploadCalled=true (got %v)", loginCount, uploadCalled)
	}
}
