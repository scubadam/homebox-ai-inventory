package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/scubadam/homebox-ai-inventory/internal/homebox"
)

// ImageInput represents an optimized image ready for multimodal analysis.
type ImageInput struct {
	Filename    string
	Bytes       []byte
	ContentType string
}

// AnalyzeRequest contains all context needed by a VisionProvider to analyze photos.
type AnalyzeRequest struct {
	Mode              string // "new", "update", "add"
	Images            []ImageInput
	ExistingTypeNames []string
	ExistingItems     []homebox.ContainerItem
}

// RawDetectedItem represents an item returned by the AI model's structured JSON output.
type RawDetectedItem struct {
	Name                     string  `json:"name"`
	Quantity                 int     `json:"quantity"`
	Description              string  `json:"description"`
	Category                 string  `json:"category"`
	FallbackExistingCategory string  `json:"fallback_existing_category"`
	Status                   string  `json:"status"`
	ExistingID               *string `json:"existing_id"`
	PhotoIndices             []int   `json:"photo_indices"`
}

type rawDetectedItemAux struct {
	Name                     string          `json:"name"`
	Quantity                 int             `json:"quantity"`
	Description              string          `json:"description"`
	Category                 string          `json:"category"`
	FallbackExistingCategory string          `json:"fallback_existing_category"`
	Status                   string          `json:"status"`
	ExistingID               json.RawMessage `json:"existing_id"`
	PhotoIndices             []int           `json:"photo_indices"`
}

// UnmarshalJSON tolerates both string and numeric existing_id representations from local VLMs.
func (r *RawDetectedItem) UnmarshalJSON(data []byte) error {
	var aux rawDetectedItemAux
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	r.Name = aux.Name
	r.Quantity = aux.Quantity
	r.Description = aux.Description
	r.Category = aux.Category
	r.FallbackExistingCategory = aux.FallbackExistingCategory
	r.Status = aux.Status
	r.PhotoIndices = aux.PhotoIndices

	rawID := strings.TrimSpace(string(aux.ExistingID))
	if rawID != "" && rawID != "null" {
		var s string
		if err := json.Unmarshal(aux.ExistingID, &s); err == nil {
			r.ExistingID = &s
		} else {
			trimmed := strings.Trim(rawID, `"`)
			r.ExistingID = &trimmed
		}
	}
	return nil
}

// RawVisionResult matches the JSON schema returned by all vision providers.
type RawVisionResult struct {
	ContainerName            string            `json:"container_name"`
	RecommendedNewCategories []string          `json:"recommended_new_categories"`
	Items                    []RawDetectedItem `json:"items"`
}

// VisionProvider is the pluggable interface implemented by all AI vision backends.
type VisionProvider interface {
	Name() string
	Analyze(ctx context.Context, req AnalyzeRequest) (*RawVisionResult, error)
}

// BuildPrompt generates the unified multimodal inventory cataloging prompt used across providers.
func BuildPrompt(req AnalyzeRequest) string {
	var existingContext string
	switch req.Mode {
	case "update":
		if len(req.ExistingItems) > 0 {
			lines := make([]string, 0, len(req.ExistingItems))
			for _, it := range req.ExistingItems {
				lines = append(lines, fmt.Sprintf("- ID: %s, Name: '%s', Qty: %d, Type: '%s', Notes: '%s'",
					it.ID, it.Name, it.Quantity, it.EntityTypeName, it.Description))
			}
			existingContext = fmt.Sprintf(`
Existing items currently recorded in this container:
%s

TASK FOR UPDATE MODE:
- Compare the photo(s) against the existing items recorded above.
- If an existing item is still present in the container:
  - Match its existing_id.
  - Set status = "matched".
  - Update the quantity if you see more or fewer.
  - Suggest a more accurate category if the current one is too generic (e.g. Misc item -> Tools).
- If you see items in the photo(s) that are NOT in the existing list:
  - Leave existing_id = null.
  - Set status = "new".
`, strings.Join(lines, "\n"))
		}
	case "add":
		existingContext = `
TASK FOR 'ADD ITEMS' MODE:
- The user is adding new items into an existing location or container.
- These photos show ONLY the new items being added into the container.
- Do NOT match or compare against previous contents. Every item detected is a new addition (status = "new", existing_id = null).
- IMPORTANT: For each item, indicate in 'photo_indices' which 0-based photo index/indices that item is visible in (e.g. [0] if in photo 1, [1] if in photo 2, [0, 1] if in multiple).
`
	}

	var multiPhotoNote string
	if len(req.Images) > 1 {
		if req.Mode == "add" {
			multiPhotoNote = fmt.Sprintf(`
NOTE: There are %d photos provided showing the items being added (distinct items or multiple angles of items).
Deduplicate if the same physical item is visible across multiple angles, and record all photos where it appears in photo_indices!
`, len(req.Images))
		} else {
			multiPhotoNote = fmt.Sprintf(`
NOTE: There are %d photos provided. These photos show the SAME container/box/shelf from DIFFERENT ANGLES and depths.
Synthesize all photos together. CRITICAL: Deduplicate items across angles so each real physical object is counted only once!
`, len(req.Images))
		}
	}

	catsJoined := strings.Join(req.ExistingTypeNames, ", ")
	if existingContext == "" {
		existingContext = "Suggest a concise title for the container or box describing its primary contents."
	}

	return fmt.Sprintf(`You are an intelligent home inventory cataloger.
%s
Current available item categories in Homebox: [%s].

%s

INSTRUCTIONS:
1. Identify all distinct items or groups of items visible across the photos.
2. Provide accurate counts/quantities.
3. For each item, decide its best category/type:
   - If an existing category fits (e.g. Cables, Screws/hardware, Pc parts, etc.), use it.
   - If NONE of the existing categories fit well (for example: tools, food cans, pharmaceuticals, craft supplies, clothing, books), RECOMMEND a clean, concise NEW category name (e.g. "Tools", "Canned Food", "Beverages", "Stationery").
4. Return a list of all recommended new category names in 'recommended_new_categories'.
5. For each item, provide:
   - name (concise item title)
   - quantity (integer >= 1)
   - description (useful details, size, specs)
   - category (the ideal category name: either an existing one or one of your recommended new ones)
   - fallback_existing_category (must be one from: [%s])
   - status ("new" or "matched")
   - existing_id (if matched to an existing item, otherwise null)
   - photo_indices (array of 0-based integers matching the photos where this item appears, e.g. [0] or [1])
`, multiPhotoNote, catsJoined, existingContext, catsJoined)
}

// ParseCleanJSON strips optional markdown code fences (```json ... ```) before unmarshaling
// into RawVisionResult so local VLMs and OpenAI-compatible models parse reliably.
func ParseCleanJSON(rawText string) (*RawVisionResult, error) {
	cleaned := strings.TrimSpace(rawText)
	if strings.HasPrefix(cleaned, "```") {
		if idx := strings.Index(cleaned, "\n"); idx != -1 {
			cleaned = cleaned[idx+1:]
		}
		cleaned = strings.TrimSuffix(strings.TrimSpace(cleaned), "```")
		cleaned = strings.TrimSpace(cleaned)
	}
	// Find outer JSON braces if model included conversational preamble
	start := strings.Index(cleaned, "{")
	end := strings.LastIndex(cleaned, "}")
	if start != -1 && end != -1 && end > start {
		cleaned = cleaned[start : end+1]
	}

	var res RawVisionResult
	if err := json.Unmarshal([]byte(cleaned), &res); err != nil {
		return nil, fmt.Errorf("failed to parse vision JSON output: %w (raw: %s)", err, rawText)
	}
	if res.Items == nil {
		res.Items = []RawDetectedItem{}
	}
	return &res, nil
}
