package homebox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"sync"
	"time"
)

// Client is an HTTP REST client for the Homebox v1 API.
type Client struct {
	baseURL    string
	publicURL  string
	apiKey     string
	username   string
	password   string
	httpClient *http.Client

	mu          sync.RWMutex
	cachedToken string
	tokenExpiry time.Time
}

// NewClient creates a new Homebox API client.
func NewClient(baseURL, publicURL, apiKey, username, password string) *Client {
	baseURL = strings.TrimRight(baseURL, "/")
	if publicURL == "" {
		publicURL = baseURL
	} else {
		publicURL = strings.TrimRight(publicURL, "/")
	}
	return &Client{
		baseURL:   baseURL,
		publicURL: publicURL,
		apiKey:    strings.TrimPrefix(strings.TrimSpace(apiKey), "Bearer "),
		username:  username,
		password:  password,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// PublicItemURL returns the user-facing browser URL for viewing a Homebox entity.
func (c *Client) PublicItemURL(entityID string) string {
	return fmt.Sprintf("%s/item/%s", c.publicURL, entityID)
}

// EntityType represents a Homebox entity type (category or location type).
type EntityType struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	IsLocation bool   `json:"isLocation"`
}

// EntityTypesResult groups entity types into all, locations, and items.
type EntityTypesResult struct {
	All       []EntityType `json:"all"`
	Locations []EntityType `json:"locations"`
	Items     []EntityType `json:"items"`
}

// LocationNode represents a flattened node from the Homebox location hierarchy tree.
type LocationNode struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Type        string `json:"type"`
}

type rawTreeNode struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Type     string        `json:"type"`
	Children []rawTreeNode `json:"children"`
}

// ContainerItem represents an existing child item inside a container.
type ContainerItem struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Quantity       int    `json:"quantity"`
	Description    string `json:"description"`
	EntityTypeID   string `json:"entityTypeId"`
	EntityTypeName string `json:"entityTypeName"`
}

// AttachmentInfo represents an existing photo attachment on a container.
type AttachmentInfo struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Primary   bool   `json:"primary"`
	CreatedAt string `json:"createdAt"`
	URL       string `json:"url"`
	FullURL   string `json:"fullUrl"`
}

// ContainerDetails holds both child items and photo attachments for a container.
type ContainerDetails struct {
	Items       []ContainerItem  `json:"items"`
	Attachments []AttachmentInfo `json:"attachments"`
}

// EntityInput represents the payload for creating or updating an entity in Homebox.
type EntityInput struct {
	Name         string  `json:"name"`
	ParentID     *string `json:"parentId,omitempty"`
	EntityTypeID string  `json:"entityTypeId,omitempty"`
	Description  string  `json:"description,omitempty"`
	Quantity     int     `json:"quantity,omitempty"`
}

func (c *Client) getAuthToken(ctx context.Context) (string, error) {
	if c.apiKey != "" {
		return c.apiKey, nil
	}
	c.mu.RLock()
	if c.cachedToken != "" && time.Now().Before(c.tokenExpiry) {
		tok := c.cachedToken
		c.mu.RUnlock()
		return tok, nil
	}
	c.mu.RUnlock()

	if c.username == "" || c.password == "" {
		return "", fmt.Errorf("neither HOMEBOX_API_KEY nor HOMEBOX_USERNAME/HOMEBOX_PASSWORD configured")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cachedToken != "" && time.Now().Before(c.tokenExpiry) {
		return c.cachedToken, nil
	}

	loginPayload := map[string]string{
		"username": c.username,
		"password": c.password,
	}
	body, _ := json.Marshal(loginPayload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/users/login", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("homebox login failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("homebox login returned %d: %s", resp.StatusCode, string(respBytes))
	}

	var loginResp struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
		return "", fmt.Errorf("failed to decode login response: %w", err)
	}
	token := strings.TrimPrefix(strings.TrimSpace(loginResp.Token), "Bearer ")
	c.cachedToken = token
	c.tokenExpiry = time.Now().Add(12 * time.Hour)
	return token, nil
}

func (c *Client) doRequest(ctx context.Context, method, path string, body io.Reader, contentType string) (*http.Response, error) {
	token, err := c.getAuthToken(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return c.httpClient.Do(req)
}

// FetchEntityTypes returns all entity types split by location vs item types.
func (c *Client) FetchEntityTypes(ctx context.Context) (*EntityTypesResult, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/entity-types", nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to fetch entity types (status %d): %s", resp.StatusCode, string(b))
	}

	var all []EntityType
	if err := json.NewDecoder(resp.Body).Decode(&all); err != nil {
		return nil, err
	}
	res := &EntityTypesResult{
		All:       all,
		Locations: []EntityType{},
		Items:     []EntityType{},
	}
	for _, t := range all {
		if t.IsLocation {
			res.Locations = append(res.Locations, t)
		} else {
			res.Items = append(res.Items, t)
		}
	}
	return res, nil
}

// CreateEntityType registers a new entity type in Homebox.
func (c *Client) CreateEntityType(ctx context.Context, name string, isLocation bool) (*EntityType, error) {
	payload := map[string]any{
		"name":       strings.TrimSpace(name),
		"isLocation": isLocation,
	}
	b, _ := json.Marshal(payload)
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/entity-types", bytes.NewReader(b), "application/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		rb, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to create entity type (status %d): %s", resp.StatusCode, string(rb))
	}
	var created EntityType
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		return nil, err
	}
	return &created, nil
}

// FetchLocationTree fetches and flattens the hierarchical location tree.
func (c *Client) FetchLocationTree(ctx context.Context) ([]LocationNode, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/entities/tree", nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		rb, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to fetch entities tree (status %d): %s", resp.StatusCode, string(rb))
	}

	var tree []rawTreeNode
	if err := json.NewDecoder(resp.Body).Decode(&tree); err != nil {
		return nil, err
	}

	locs := []LocationNode{}
	var walk func(nodes []rawTreeNode, prefix string)
	walk = func(nodes []rawTreeNode, prefix string) {
		for _, n := range nodes {
			if n.Type == "location" {
				display := prefix + n.Name
				locs = append(locs, LocationNode{
					ID:          n.ID,
					Name:        n.Name,
					DisplayName: display,
					Type:        n.Type,
				})
				walk(n.Children, display+" / ")
			}
		}
	}
	walk(tree, "")
	return locs, nil
}

// FetchAttachment proxies an attachment image binary and its MIME type.
func (c *Client) FetchAttachment(ctx context.Context, entityID, attachmentID string) ([]byte, string, error) {
	path := fmt.Sprintf("/api/v1/entities/%s/attachments/%s", entityID, attachmentID)
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("attachment not found (status %d)", resp.StatusCode)
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "image/jpeg"
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	return data, contentType, nil
}

// RawAttachment represents an attachment entry on an entity JSON object.
type RawAttachment struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Type      string `json:"type"`
	MimeType  string `json:"mimeType"`
	Primary   bool   `json:"primary"`
	CreatedAt string `json:"createdAt"`
	Thumbnail *struct {
		ID string `json:"id"`
	} `json:"thumbnail"`
}

// GetEntityAttachments fetches the raw attachment list for an entity.
func (c *Client) GetEntityAttachments(ctx context.Context, entityID string) ([]RawAttachment, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/entities/"+entityID, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("entity %s returned status %d", entityID, resp.StatusCode)
	}
	var entData struct {
		Attachments []RawAttachment `json:"attachments"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&entData); err != nil {
		return nil, err
	}
	return entData.Attachments, nil
}

// GetContainerDetails fetches both photo attachments and child items for a container.
func (c *Client) GetContainerDetails(ctx context.Context, containerID string) (*ContainerDetails, error) {
	details := &ContainerDetails{
		Items:       []ContainerItem{},
		Attachments: []AttachmentInfo{},
	}

	if rawAtts, err := c.GetEntityAttachments(ctx, containerID); err == nil {
		for _, att := range rawAtts {
			if att.Type == "photo" || att.Type == "image" || att.Type == "" || strings.HasPrefix(att.MimeType, "image/") {
				thumbID := att.ID
				if att.Thumbnail != nil && att.Thumbnail.ID != "" {
					thumbID = att.Thumbnail.ID
				}
				title := att.Title
				if title == "" {
					title = "Photo"
				}
				details.Attachments = append(details.Attachments, AttachmentInfo{
					ID:        att.ID,
					Title:     title,
					Primary:   att.Primary,
					CreatedAt: att.CreatedAt,
					URL:       fmt.Sprintf("/api/attachment-proxy/%s/%s", containerID, thumbID),
					FullURL:   fmt.Sprintf("/api/attachment-proxy/%s/%s", containerID, att.ID),
				})
			}
		}
	}

	path := fmt.Sprintf("/api/v1/entities?parentIds=%s&pageSize=200", containerID)
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		rb, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to fetch container items (status %d): %s", resp.StatusCode, string(rb))
	}

	var page struct {
		Items []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Quantity    int    `json:"quantity"`
			Description string `json:"description"`
			EntityType  struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"entityType"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return nil, err
	}

	for _, it := range page.Items {
		qty := it.Quantity
		if qty < 1 {
			qty = 1
		}
		typeName := it.EntityType.Name
		if typeName == "" {
			typeName = "Item"
		}
		details.Items = append(details.Items, ContainerItem{
			ID:             it.ID,
			Name:           it.Name,
			Quantity:       qty,
			Description:    it.Description,
			EntityTypeID:   it.EntityType.ID,
			EntityTypeName: typeName,
		})
	}
	return details, nil
}

// CreateEntity creates a new container or item entity and returns its ID.
func (c *Client) CreateEntity(ctx context.Context, input EntityInput) (string, error) {
	if input.Quantity < 1 {
		input.Quantity = 1
	}
	b, _ := json.Marshal(input)
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/entities", bytes.NewReader(b), "application/json")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		rb, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to create entity (status %d): %s", resp.StatusCode, string(rb))
	}

	var raw struct {
		ID   string `json:"id"`
		Item *struct {
			ID string `json:"id"`
		} `json:"item"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return "", err
	}
	if raw.Item != nil && raw.Item.ID != "" {
		return raw.Item.ID, nil
	}
	return raw.ID, nil
}

// UpdateEntity updates an existing entity in Homebox.
func (c *Client) UpdateEntity(ctx context.Context, entityID string, input EntityInput) error {
	payload := map[string]any{
		"name": strings.TrimSpace(input.Name),
	}
	if input.EntityTypeID != "" {
		payload["entityTypeId"] = input.EntityTypeID
	}
	if input.Quantity > 0 {
		payload["quantity"] = input.Quantity
	}
	if input.Description != "" || input.Quantity > 0 {
		payload["description"] = input.Description
	}
	b, _ := json.Marshal(payload)
	resp, err := c.doRequest(ctx, http.MethodPut, "/api/v1/entities/"+entityID, bytes.NewReader(b), "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		rb, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to update entity %s (status %d): %s", entityID, resp.StatusCode, string(rb))
	}
	return nil
}

// DeleteEntity deletes an entity by ID.
func (c *Client) DeleteEntity(ctx context.Context, entityID string) error {
	resp, err := c.doRequest(ctx, http.MethodDelete, "/api/v1/entities/"+entityID, nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		rb, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to delete entity %s (status %d): %s", entityID, resp.StatusCode, string(rb))
	}
	return nil
}

// UploadAttachment uploads a photo attachment to an entity.
func (c *Client) UploadAttachment(ctx context.Context, entityID, filename, contentType string, fileBytes []byte, isPrimary bool) error {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	_ = writer.WriteField("name", filename)
	_ = writer.WriteField("type", "photo")
	if isPrimary {
		_ = writer.WriteField("primary", "true")
	} else {
		_ = writer.WriteField("primary", "false")
	}

	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, strings.ReplaceAll(filename, `"`, "")))
	if contentType == "" {
		contentType = "image/jpeg"
	}
	h.Set("Content-Type", contentType)

	part, err := writer.CreatePart(h)
	if err != nil {
		return err
	}
	if _, err := part.Write(fileBytes); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}

	path := fmt.Sprintf("/api/v1/entities/%s/attachments", entityID)
	resp, err := c.doRequest(ctx, http.MethodPost, path, &body, writer.FormDataContentType())
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		rb, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to upload attachment (status %d): %s", resp.StatusCode, string(rb))
	}
	return nil
}

// SetAttachmentPrimary sets or unsets the primary flag on an existing attachment.
func (c *Client) SetAttachmentPrimary(ctx context.Context, entityID, attachmentID string, isPrimary bool) error {
	payload := map[string]any{
		"primary": isPrimary,
		"type":    "photo",
	}
	b, _ := json.Marshal(payload)
	path := fmt.Sprintf("/api/v1/entities/%s/attachments/%s", entityID, attachmentID)
	resp, err := c.doRequest(ctx, http.MethodPut, path, bytes.NewReader(b), "application/json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		rb, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to update attachment %s (status %d): %s", attachmentID, resp.StatusCode, string(rb))
	}
	return nil
}

// DeleteAttachment deletes an attachment from an entity.
func (c *Client) DeleteAttachment(ctx context.Context, entityID, attachmentID string) error {
	path := fmt.Sprintf("/api/v1/entities/%s/attachments/%s", entityID, attachmentID)
	resp, err := c.doRequest(ctx, http.MethodDelete, path, nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		rb, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to delete attachment %s (status %d): %s", attachmentID, resp.StatusCode, string(rb))
	}
	return nil
}
