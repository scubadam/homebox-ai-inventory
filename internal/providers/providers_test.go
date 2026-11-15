package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/scubadam/homebox-ai-inventory/internal/config"
	"github.com/scubadam/homebox-ai-inventory/internal/homebox"
)

func TestBuildPromptModesAndMultiAngle(t *testing.T) {
	// Update mode with 2 angles
	req := AnalyzeRequest{
		Mode: "update",
		Images: []ImageInput{
			{Filename: "angle1.jpg", Bytes: []byte("img1")},
			{Filename: "angle2.jpg", Bytes: []byte("img2")},
		},
		ExistingTypeNames: []string{"Cables", "Tools"},
		ExistingItems: []homebox.ContainerItem{
			{ID: "id-1", Name: "HDMI Cable", Quantity: 2, EntityTypeName: "Cables"},
		},
	}
	prompt := BuildPrompt(req)
	if !strings.Contains(prompt, "TASK FOR UPDATE MODE") {
		t.Errorf("expected update mode instructions in prompt")
	}
	if !strings.Contains(prompt, "HDMI Cable") {
		t.Errorf("expected existing item HDMI Cable in prompt")
	}
	if !strings.Contains(prompt, "SAME container/box/shelf from DIFFERENT ANGLES") {
		t.Errorf("expected multi-angle deduplication note in prompt")
	}
	if !strings.Contains(prompt, "EXTERIOR COVER DETECTION") {
		t.Errorf("expected exterior cover detection instructions for multi-photo update/new mode")
	}
	if strings.Contains(prompt, "box_2d") {
		t.Errorf("did not expect box_2d instructions when IsolateItemPhotos is false")
	}

	// IsolateItemPhotos enabled
	reqCrop := req
	reqCrop.IsolateItemPhotos = true
	promptCrop := BuildPrompt(reqCrop)
	if !strings.Contains(promptCrop, "box_2d") || !strings.Contains(promptCrop, "crop_photo_index") {
		t.Errorf("expected box_2d and crop_photo_index instructions when IsolateItemPhotos is true")
	}

	// Add mode
	reqAdd := AnalyzeRequest{
		Mode:   "add",
		Images: []ImageInput{{Filename: "a.jpg"}, {Filename: "b.jpg"}},
	}
	promptAdd := BuildPrompt(reqAdd)
	if !strings.Contains(promptAdd, "TASK FOR 'ADD ITEMS' MODE") {
		t.Errorf("expected add items instructions in prompt")
	}
}

func TestParseCleanJSONHandlesCodeFencesAndPreamble(t *testing.T) {
	rawWithFence := "Sure! Here is the JSON:\n```json\n{\n  \"container_name\": \"Tool Box\",\n  \"recommended_new_categories\": [\"Hand Tools\"],\n  \"items\": [{\"name\": \"Wrench\", \"quantity\": 1, \"description\": \"10mm\", \"category\": \"Hand Tools\", \"fallback_existing_category\": \"Misc\", \"existing_id\": 42}]\n}\n```"
	res, err := ParseCleanJSON(rawWithFence)
	if err != nil {
		t.Fatalf("ParseCleanJSON failed: %v", err)
	}
	if res.ContainerName != "Tool Box" || len(res.Items) != 1 || res.Items[0].Name != "Wrench" {
		t.Errorf("unexpected parsed result: %+v", res)
	}
	if res.Items[0].ExistingID == nil || *res.Items[0].ExistingID != "42" {
		t.Errorf("expected numeric existing_id 42 to parse as string '42', got %+v", res.Items[0].ExistingID)
	}
}

func TestGeminiProviderAnalyze(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "gemini-2.5-flash:generateContent") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("x-goog-api-key") != "test-gemini-key" && r.URL.Query().Get("key") != "test-gemini-key" {
			t.Errorf("missing or wrong API key: header=%q, query=%q", r.Header.Get("x-goog-api-key"), r.URL.Query().Get("key"))
		}
		resp := map[string]any{
			"candidates": []map[string]any{
				{
					"content": map[string]any{
						"parts": []map[string]any{
							{
								"text": `{"container_name":"Cable Bin","recommended_new_categories":[],"items":[{"name":"USB-C Cable","quantity":3,"description":"Braided","category":"Cables","fallback_existing_category":"Cables","status":"new","photo_indices":[0]}]}`,
							},
						},
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-2.5-flash", "test-gemini-key", 5*time.Second)
	res, err := p.Analyze(context.Background(), AnalyzeRequest{
		Mode:   "new",
		Images: []ImageInput{{Filename: "1.jpg", Bytes: []byte("img")}},
	})
	if err != nil {
		t.Fatalf("GeminiProvider.Analyze failed: %v", err)
	}
	if res.ContainerName != "Cable Bin" || len(res.Items) != 1 || res.Items[0].Quantity != 3 {
		t.Errorf("unexpected Gemini result: %+v", res)
	}
}

func TestOllamaAndOpenAIProvidersAnalyze(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chat", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message": map[string]any{
				"content": "```json\n{\"container_name\":\"Ollama Box\",\"items\":[{\"name\":\"Raspberry Pi\",\"quantity\":1,\"description\":\"Pi 5\",\"category\":\"SBC\",\"fallback_existing_category\":\"Electronics\"}]}\n```",
			},
		})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"content": `{"container_name":"OpenAI Box","items":[{"name":"Multimeter","quantity":1,"description":"Fluke","category":"Tools","fallback_existing_category":"Tools"}]}`,
					},
				},
			},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ollamaP := NewOllamaOpenAIProvider("ollama", srv.URL, "moondream", "", 5*time.Second)
	resOllama, err := ollamaP.Analyze(context.Background(), AnalyzeRequest{
		Mode:   "new",
		Images: []ImageInput{{Filename: "1.jpg", Bytes: []byte("img")}},
	})
	if err != nil {
		t.Fatalf("Ollama Analyze failed: %v", err)
	}
	if resOllama.ContainerName != "Ollama Box" || resOllama.Items[0].Name != "Raspberry Pi" {
		t.Errorf("unexpected Ollama result: %+v", resOllama)
	}

	openaiP := NewOllamaOpenAIProvider("openai", srv.URL, "gpt-4o-mini", "sk-test", 5*time.Second)
	resOAI, err := openaiP.Analyze(context.Background(), AnalyzeRequest{
		Mode:   "new",
		Images: []ImageInput{{Filename: "1.jpg", Bytes: []byte("img")}},
	})
	if err != nil {
		t.Fatalf("OpenAI Analyze failed: %v", err)
	}
	if resOAI.ContainerName != "OpenAI Box" || resOAI.Items[0].Name != "Multimeter" {
		t.Errorf("unexpected OpenAI result: %+v", resOAI)
	}
}

func TestAnthropicStubAndFactory(t *testing.T) {
	cfg := &config.Config{AIProvider: "anthropic", AIModel: "claude-3-5-sonnet"}
	p, err := NewFromConfig(cfg)
	if err != nil {
		t.Fatalf("NewFromConfig failed for anthropic: %v", err)
	}
	_, err = p.Analyze(context.Background(), AnalyzeRequest{})
	if err == nil || !strings.Contains(err.Error(), "stub") {
		t.Errorf("expected anthropic stub error message, got: %v", err)
	}
}

func TestGeminiProviderAnalyzeWithCropsAndExteriorCover(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("failed to decode request body: %v", err)
		}
		genCfg, _ := body["generationConfig"].(map[string]any)
		schema, _ := genCfg["responseSchema"].(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		if _, ok := props["cover_photo_index"]; !ok {
			t.Errorf("expected cover_photo_index in Gemini responseSchema")
		}
		if _, ok := props["exterior_photo_indices"]; !ok {
			t.Errorf("expected exterior_photo_indices in Gemini responseSchema")
		}
		itemsSchema, _ := props["items"].(map[string]any)
		itemObj, _ := itemsSchema["items"].(map[string]any)
		itemProps, _ := itemObj["properties"].(map[string]any)
		if _, ok := itemProps["box_2d"]; !ok {
			t.Errorf("expected box_2d in Gemini item schema when IsolateItemPhotos is true")
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"candidates": []map[string]any{
				{
					"content": map[string]any{
						"parts": []map[string]any{
							{
								"text": `{"container_name":"Kitchen Drawer","cover_photo_index":0,"exterior_photo_indices":[0],"recommended_new_categories":[],"items":[{"name":"Peeler","quantity":1,"description":"Green peeler","category":"Kitchenware","fallback_existing_category":"Misc","status":"new","photo_indices":[1],"crop_photo_index":1,"box_2d":[100,200,500,600]}]}`,
							},
						},
					},
				},
			},
		})
	}))
	defer srv.Close()

	p := NewGeminiProvider(srv.URL, "gemini-2.5-flash", "test-gemini-key", 5*time.Second)
	res, err := p.Analyze(context.Background(), AnalyzeRequest{
		Mode:              "new",
		IsolateItemPhotos: true,
		Images: []ImageInput{
			{Filename: "exterior.jpg", Bytes: []byte("ext")},
			{Filename: "interior.jpg", Bytes: []byte("int")},
		},
	})
	if err != nil {
		t.Fatalf("GeminiProvider.Analyze failed: %v", err)
	}
	if res.CoverPhotoIndex == nil || *res.CoverPhotoIndex != 0 {
		t.Errorf("expected CoverPhotoIndex=0, got %+v", res.CoverPhotoIndex)
	}
	if len(res.ExteriorPhotoIndices) != 1 || res.ExteriorPhotoIndices[0] != 0 {
		t.Errorf("expected ExteriorPhotoIndices=[0], got %v", res.ExteriorPhotoIndices)
	}
	if len(res.Items) != 1 || len(res.Items[0].Box2D) != 4 || res.Items[0].CropPhotoIndex == nil || *res.Items[0].CropPhotoIndex != 1 {
		t.Errorf("unexpected crop metadata on parsed item: %+v", res.Items)
	}
}
