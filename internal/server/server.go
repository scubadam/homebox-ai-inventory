package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/scubadam/homebox-ai-inventory/internal/config"
	"github.com/scubadam/homebox-ai-inventory/internal/homebox"
	"github.com/scubadam/homebox-ai-inventory/internal/imaging"
	"github.com/scubadam/homebox-ai-inventory/internal/providers"
	"github.com/scubadam/homebox-ai-inventory/web"
)

// ImageSession holds optimized photos in memory between /api/analyze and /api/commit.
type ImageSession struct {
	CreatedAt time.Time
	Images    []providers.ImageInput
}

// Server implements all HTTP endpoints for homebox-ai-inventory.
type Server struct {
	cfg      *config.Config
	hb       *homebox.Client
	provider providers.VisionProvider

	mu       sync.RWMutex
	sessions map[string]ImageSession
}

// New creates a new Server instance wired with the Homebox client and VisionProvider.
func New(cfg *config.Config, hb *homebox.Client, provider providers.VisionProvider) *Server {
	return &Server{
		cfg:      cfg,
		hb:       hb,
		provider: provider,
		sessions: make(map[string]ImageSession),
	}
}

// Handler returns the root http.Handler with CORS middleware and all routes registered.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Static PWA routes
	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("GET /manifest.json", s.handleManifest)
	mux.HandleFunc("GET /sw.js", s.handleServiceWorker)

	// Health & status routes
	mux.HandleFunc("GET /api/health", s.handleHealth)

	// Core Inventory API routes
	mux.HandleFunc("GET /api/bootstrap", s.handleBootstrap)
	mux.HandleFunc("POST /api/entity-types", s.handleCreateEntityType)
	mux.HandleFunc("GET /api/attachment-proxy/{entity_id}/{attachment_id}", s.handleAttachmentProxy)
	mux.HandleFunc("GET /api/container-items/{container_id}", s.handleContainerItems)
	mux.HandleFunc("POST /api/analyze", s.handleAnalyze)
	mux.HandleFunc("POST /api/commit", s.handleCommit)

	return corsMiddleware(s.basicAuthMiddleware(mux))
}

func (s *Server) basicAuthMiddleware(next http.Handler) http.Handler {
	if !s.cfg.AuthEnabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Health check is always unauthenticated so probes and monitoring succeed
		if r.URL.Path == "/api/health" {
			next.ServeHTTP(w, r)
			return
		}

		user, pass, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(user), []byte(s.cfg.AuthUsername)) != 1 ||
			subtle.ConstantTimeCompare([]byte(pass), []byte(s.cfg.AuthPassword)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="Homebox AI Inventory"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		} else {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, map[string]string{"detail": detail})
}

func generateSessionID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]),
		hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]),
	)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(web.StaticFS, "static/index.html")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "index.html not found")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleManifest(w http.ResponseWriter, _ *http.Request) {
	data, err := fs.ReadFile(web.StaticFS, "static/manifest.json")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "manifest.json not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleServiceWorker(w http.ResponseWriter, _ *http.Request) {
	data, err := fs.ReadFile(web.StaticFS, "static/sw.js")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "sw.js not found")
		return
	}
	w.Header().Set("Content-Type", "application/javascript")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	provName := ""
	if s.provider != nil {
		provName = s.provider.Name()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "ok",
		"ai_provider": provName,
		"ai_model":    s.cfg.AIModel,
		"homebox_url": s.cfg.HomeboxPublicURL,
	})
}

func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	typesData, err := s.hb.FetchEntityTypes(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	locations, err := s.hb.FetchLocationTree(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"locations":      locations,
		"location_types": typesData.Locations,
		"item_types":     typesData.Items,
	})
}

func (s *Server) handleCreateEntityType(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name       string `json:"name"`
		IsLocation bool   `json:"isLocation"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeError(w, http.StatusBadRequest, "Valid entity type name is required")
		return
	}
	created, err := s.hb.CreateEntityType(r.Context(), req.Name, req.IsLocation)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, created)
}

func (s *Server) handleAttachmentProxy(w http.ResponseWriter, r *http.Request) {
	entityID := r.PathValue("entity_id")
	attachmentID := r.PathValue("attachment_id")
	if entityID == "" || attachmentID == "" {
		writeError(w, http.StatusBadRequest, "Missing entity_id or attachment_id")
		return
	}
	data, contentType, err := s.hb.FetchAttachment(r.Context(), entityID, attachmentID)
	if err != nil {
		writeError(w, http.StatusNotFound, "Attachment not found")
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleContainerItems(w http.ResponseWriter, r *http.Request) {
	containerID := r.PathValue("container_id")
	if containerID == "" {
		writeError(w, http.StatusBadRequest, "Missing container_id")
		return
	}
	details, err := s.hb.GetContainerDetails(r.Context(), containerID)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, details)
}

// ProcessedItem is the normalized item structure returned by /api/analyze to the PWA UI.
type ProcessedItem struct {
	Name          string  `json:"name"`
	Quantity      int     `json:"quantity"`
	Description   string  `json:"description"`
	CategoryName  string  `json:"categoryName"`
	EntityTypeID  string  `json:"entityTypeId"`
	Status        string  `json:"status"`
	ExistingID    *string `json:"existingId"`
	PhotoIndices  []int   `json:"photo_indices,omitempty"`
}

func (s *Server) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	// Limit request body to 64MB to prevent denial-of-service / memory exhaustion
	r.Body = http.MaxBytesReader(w, r.Body, 64<<20)
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid multipart form data: "+err.Error())
		return
	}

	mode := strings.TrimSpace(r.FormValue("mode"))
	if mode == "" {
		mode = "new"
	}
	containerID := strings.TrimSpace(r.FormValue("container_id"))

	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		writeError(w, http.StatusBadRequest, "No files provided")
		return
	}
	if len(files) > 20 {
		writeError(w, http.StatusBadRequest, "Too many files uploaded (maximum 20 photos per analysis)")
		return
	}

	optImages := make([]providers.ImageInput, 0, len(files))
	for _, fh := range files {
		f, err := fh.Open()
		if err != nil {
			continue
		}
		rawBytes, err := io.ReadAll(f)
		_ = f.Close()
		if err != nil || len(rawBytes) == 0 {
			continue
		}

		cType := fh.Header.Get("Content-Type")
		if cType == "" {
			cType = "image/jpeg"
		}

		optBytes, err := imaging.OptimizeImage(rawBytes, s.cfg.MaxImageDimension, s.cfg.JPEGQuality)
		if err != nil {
			log.Printf("image optimization fallback for %s: %v", fh.Filename, err)
		} else {
			cType = "image/jpeg"
		}

		fname := fh.Filename
		if fname == "" {
			fname = "photo.jpg"
		}
		optImages = append(optImages, providers.ImageInput{
			Filename:    fname,
			Bytes:       optBytes,
			ContentType: cType,
		})
	}

	if len(optImages) == 0 {
		writeError(w, http.StatusBadRequest, "No valid images uploaded")
		return
	}

	sessionID := generateSessionID()
	s.saveSession(sessionID, optImages)

	ctx := r.Context()
	typesData, err := s.hb.FetchEntityTypes(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	existingTypeNames := make([]string, 0, len(typesData.Items))
	typeNameToID := make(map[string]string, len(typesData.Items))
	fallbackTypeID := ""
	if len(typesData.Items) > 0 {
		fallbackTypeID = typesData.Items[0].ID
	}
	for _, t := range typesData.Items {
		existingTypeNames = append(existingTypeNames, t.Name)
		typeNameToID[strings.ToLower(strings.TrimSpace(t.Name))] = t.ID
	}

	var existingItemsList []homebox.ContainerItem
	if mode == "update" && containerID != "" {
		if details, err := s.hb.GetContainerDetails(ctx, containerID); err == nil {
			existingItemsList = details.Items
		} else {
			log.Printf("warning: could not load existing items for container %s: %v", containerID, err)
		}
	}

	parsed, err := s.provider.Analyze(ctx, providers.AnalyzeRequest{
		Mode:              mode,
		Images:            optImages,
		ExistingTypeNames: existingTypeNames,
		ExistingItems:     existingItemsList,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	// Filter recommended new categories that do not exist yet
	recCats := []string{}
	seenRec := make(map[string]bool)
	for _, c := range parsed.RecommendedNewCategories {
		cClean := strings.TrimSpace(c)
		cLower := strings.ToLower(cClean)
		if cClean != "" && typeNameToID[cLower] == "" && !seenRec[cLower] {
			recCats = append(recCats, cClean)
			seenRec[cLower] = true
		}
	}

	processedItems := make([]ProcessedItem, 0, len(parsed.Items))
	matchedExistingIDs := make(map[string]bool)

	for _, item := range parsed.Items {
		catName := strings.TrimSpace(item.Category)
		fallbackName := strings.ToLower(strings.TrimSpace(item.FallbackExistingCategory))

		entityTypeID := typeNameToID[strings.ToLower(catName)]
		if entityTypeID == "" {
			if fbID, ok := typeNameToID[fallbackName]; ok {
				entityTypeID = fbID
			} else {
				entityTypeID = fallbackTypeID
			}
		}

		var exID *string
		if mode != "add" && item.ExistingID != nil && strings.TrimSpace(*item.ExistingID) != "" {
			val := strings.TrimSpace(*item.ExistingID)
			exID = &val
			matchedExistingIDs[val] = true
		}

		validPIndices := []int{}
		for _, idx := range item.PhotoIndices {
			if idx >= 0 && idx < len(optImages) {
				validPIndices = append(validPIndices, idx)
			}
		}
		if len(validPIndices) == 0 && len(optImages) > 0 {
			validPIndices = []int{0}
		}

		name := strings.TrimSpace(item.Name)
		if name == "" {
			name = "Untitled Item"
		}
		qty := item.Quantity
		if qty < 1 {
			qty = 1
		}
		status := item.Status
		if mode == "add" || (status != "matched" && status != "new") {
			status = "new"
		}

		processedItems = append(processedItems, ProcessedItem{
			Name:         name,
			Quantity:     qty,
			Description:  item.Description,
			CategoryName: catName,
			EntityTypeID: entityTypeID,
			Status:       status,
			ExistingID:   exID,
			PhotoIndices: validPIndices,
		})
	}

	missingItems := []ProcessedItem{}
	if mode == "update" && len(existingItemsList) > 0 {
		for _, ex := range existingItemsList {
			if !matchedExistingIDs[ex.ID] {
				idCopy := ex.ID
				missingItems = append(missingItems, ProcessedItem{
					Name:         ex.Name,
					Quantity:     ex.Quantity,
					Description:  ex.Description,
					CategoryName: ex.EntityTypeName,
					EntityTypeID: ex.EntityTypeID,
					Status:       "missing",
					ExistingID:   &idCopy,
				})
			}
		}
	}

	containerName := strings.TrimSpace(parsed.ContainerName)
	if containerName == "" {
		containerName = "New Container"
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"session_id":             sessionID,
		"container_name":         containerName,
		"recommended_categories": recCats,
		"items":                  processedItems,
		"missing_items":          missingItems,
		"photo_count":            len(optImages),
	})
}

// CommitItem represents a single reviewed item submitted to /api/commit.
type CommitItem struct {
	Name         string  `json:"name"`
	Quantity     int     `json:"quantity"`
	Description  string  `json:"description"`
	EntityTypeID string  `json:"entityTypeId"`
	Status       string  `json:"status"`
	ExistingID   *string `json:"existingId"`
	Action       string  `json:"action"` // "save", "delete", "ignore"
	PhotoIndices []int   `json:"photo_indices"`
}

// CommitPayload represents the JSON body sent by the PWA to /api/commit.
type CommitPayload struct {
	SessionID            string       `json:"session_id"`
	Mode                 string       `json:"mode"` // "new", "create", "update", "add"
	ContainerID          *string      `json:"container_id"`
	ParentID             *string      `json:"parent_id"`
	ContainerName        string       `json:"container_name"`
	ContainerTypeID      *string      `json:"container_type_id"`
	Items                []CommitItem `json:"items"`
	PrimaryPhotoRef      *string      `json:"primary_photo_ref"`
	DeletedAttachmentIDs []string     `json:"deleted_attachment_ids"`
}

func (s *Server) handleCommit(w http.ResponseWriter, r *http.Request) {
	var payload CommitPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON payload: "+err.Error())
		return
	}
	if payload.Mode == "" {
		payload.Mode = "new"
	}

	session, ok := s.getSession(payload.SessionID)
	if !ok || len(session.Images) == 0 {
		writeError(w, http.StatusBadRequest, "Image session expired or no photos found")
		return
	}

	ctx := r.Context()
	containerID := ""
	if payload.ContainerID != nil {
		containerID = strings.TrimSpace(*payload.ContainerID)
	}
	containerTypeID := ""
	if payload.ContainerTypeID != nil {
		containerTypeID = strings.TrimSpace(*payload.ContainerTypeID)
	}

	// Mode 0: "add" mode - attach photos directly to newly created items inside an existing container
	if payload.Mode == "add" {
		if containerID == "" {
			writeError(w, http.StatusBadRequest, "Target container ID is required in Add Items mode")
			return
		}
		createdCount := 0
		uploadedPhotosCount := 0

		for _, item := range payload.Items {
			if item.Action == "ignore" {
				continue
			}
			qty := item.Quantity
			if qty < 1 {
				qty = 1
			}
			parentID := containerID
			itemID, err := s.hb.CreateEntity(ctx, homebox.EntityInput{
				Name:         strings.TrimSpace(item.Name),
				ParentID:     &parentID,
				EntityTypeID: item.EntityTypeID,
				Description:  item.Description,
				Quantity:     qty,
			})
			if err != nil {
				log.Printf("error creating item '%s': %v", item.Name, err)
				continue
			}
			createdCount++

			targetIndices := item.PhotoIndices
			if len(targetIndices) == 0 && len(session.Images) == 1 {
				targetIndices = []int{0}
			}
			for order, pIdx := range targetIndices {
				if pIdx >= 0 && pIdx < len(session.Images) {
					img := session.Images[pIdx]
					isPrimary := (order == 0)
					if err := s.hb.UploadAttachment(ctx, itemID, img.Filename, img.ContentType, img.Bytes, isPrimary); err == nil {
						uploadedPhotosCount++
					} else {
						log.Printf("warning: failed to upload photo #%d to item %s: %v", pIdx+1, itemID, err)
					}
				}
			}
		}

		s.deleteSession(payload.SessionID)
		writeJSON(w, http.StatusOK, map[string]any{
			"success":               true,
			"container_id":          containerID,
			"container_name":        payload.ContainerName,
			"created_count":         createdCount,
			"updated_count":         0,
			"deleted_count":         0,
			"uploaded_photos_count": uploadedPhotosCount,
			"deleted_photos_count":  0,
			"view_url":              s.hb.PublicItemURL(containerID),
		})
		return
	}

	// Mode 1: "new" or "create" vs "update"
	if payload.Mode == "new" || payload.Mode == "create" {
		var parentID *string
		if payload.ParentID != nil && strings.TrimSpace(*payload.ParentID) != "" {
			val := strings.TrimSpace(*payload.ParentID)
			parentID = &val
		}
		createdID, err := s.hb.CreateEntity(ctx, homebox.EntityInput{
			Name:         strings.TrimSpace(payload.ContainerName),
			ParentID:     parentID,
			EntityTypeID: containerTypeID,
			Description:  fmt.Sprintf("Created via Homebox AI Inventory Assistant with %d items.", len(payload.Items)),
			Quantity:     1,
		})
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		containerID = createdID
	} else if payload.ContainerName != "" && containerID != "" {
		if err := s.hb.UpdateEntity(ctx, containerID, homebox.EntityInput{
			Name:         strings.TrimSpace(payload.ContainerName),
			EntityTypeID: containerTypeID,
		}); err != nil {
			log.Printf("warning: failed to update container name for %s: %v", containerID, err)
		}
	}

	// Step 2: Delete requested existing attachments in update mode
	deletedPhotosCount := 0
	if containerID != "" && len(payload.DeletedAttachmentIDs) > 0 {
		for _, attID := range payload.DeletedAttachmentIDs {
			if err := s.hb.DeleteAttachment(ctx, containerID, attID); err == nil {
				deletedPhotosCount++
			} else {
				log.Printf("warning: failed to delete attachment %s: %v", attID, err)
			}
		}
	}

	// Step 3: Determine Primary Cover photo
	primaryNewIdx := -1
	primaryRef := ""
	if payload.PrimaryPhotoRef != nil {
		primaryRef = strings.TrimSpace(*payload.PrimaryPhotoRef)
	}
	if strings.HasPrefix(primaryRef, "new:") {
		if idx, err := strconv.Atoi(strings.TrimPrefix(primaryRef, "new:")); err == nil {
			primaryNewIdx = idx
		} else {
			primaryNewIdx = 0
		}
	} else if primaryRef == "" && (payload.Mode == "new" || payload.Mode == "create") {
		primaryNewIdx = 0
	}

	uploadedPhotosCount := 0
	for idx, img := range session.Images {
		isPrimary := (idx == primaryNewIdx)
		if err := s.hb.UploadAttachment(ctx, containerID, img.Filename, img.ContentType, img.Bytes, isPrimary); err == nil {
			uploadedPhotosCount++
		} else {
			log.Printf("warning: failed to upload photo #%d to container %s: %v", idx+1, containerID, err)
		}
	}

	// Step 4: Update existing attachment primary flag if needed
	if strings.HasPrefix(primaryRef, "existing:") && containerID != "" {
		chosenAttID := strings.TrimPrefix(primaryRef, "existing:")
		if err := s.hb.SetAttachmentPrimary(ctx, containerID, chosenAttID, true); err != nil {
			log.Printf("warning: failed to set existing attachment %s as primary: %v", chosenAttID, err)
		}
	} else if primaryNewIdx >= 0 && containerID != "" && payload.Mode == "update" {
		if currAtts, err := s.hb.GetEntityAttachments(ctx, containerID); err == nil {
			for _, att := range currAtts {
				if att.Primary && primaryRef != "existing:"+att.ID {
					_ = s.hb.SetAttachmentPrimary(ctx, containerID, att.ID, false)
				}
			}
		}
	}

	// Step 5: Synchronize items (Create, Update, Delete)
	createdCount := 0
	updatedCount := 0
	deletedCount := 0

	for _, item := range payload.Items {
		exID := ""
		if item.ExistingID != nil {
			exID = strings.TrimSpace(*item.ExistingID)
		}
		if item.Action == "delete" && exID != "" {
			if err := s.hb.DeleteEntity(ctx, exID); err == nil {
				deletedCount++
			} else {
				log.Printf("error deleting item %s: %v", exID, err)
			}
			continue
		}
		if item.Action == "ignore" {
			continue
		}
		qty := item.Quantity
		if qty < 1 {
			qty = 1
		}
		if exID != "" {
			if err := s.hb.UpdateEntity(ctx, exID, homebox.EntityInput{
				Name:         strings.TrimSpace(item.Name),
				Quantity:     qty,
				EntityTypeID: item.EntityTypeID,
				Description:  item.Description,
			}); err == nil {
				updatedCount++
			} else {
				log.Printf("error updating item %s: %v", exID, err)
			}
			continue
		}

		parentID := containerID
		if _, err := s.hb.CreateEntity(ctx, homebox.EntityInput{
			Name:         strings.TrimSpace(item.Name),
			ParentID:     &parentID,
			EntityTypeID: item.EntityTypeID,
			Description:  item.Description,
			Quantity:     qty,
		}); err == nil {
			createdCount++
		} else {
			log.Printf("error creating item '%s': %v", item.Name, err)
		}
	}

	s.deleteSession(payload.SessionID)
	writeJSON(w, http.StatusOK, map[string]any{
		"success":               true,
		"container_id":          containerID,
		"container_name":        payload.ContainerName,
		"created_count":         createdCount,
		"updated_count":         updatedCount,
		"deleted_count":         deletedCount,
		"uploaded_photos_count": uploadedPhotosCount,
		"deleted_photos_count":  deletedPhotosCount,
		"view_url":              s.hb.PublicItemURL(containerID),
	})
}

func (s *Server) saveSession(id string, images []providers.ImageInput) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Evict sessions older than 30 minutes
	cutoff := time.Now().Add(-30 * time.Minute)
	for k, v := range s.sessions {
		if v.CreatedAt.Before(cutoff) {
			delete(s.sessions, k)
		}
	}

	// Bound in-memory session growth: if 50 or more sessions exist, prune oldest
	for len(s.sessions) >= 50 {
		var oldestKey string
		var oldestTime time.Time
		first := true
		for k, v := range s.sessions {
			if first || v.CreatedAt.Before(oldestTime) {
				oldestKey = k
				oldestTime = v.CreatedAt
				first = false
			}
		}
		if oldestKey != "" {
			delete(s.sessions, oldestKey)
		} else {
			break
		}
	}

	s.sessions[id] = ImageSession{
		CreatedAt: time.Now(),
		Images:    images,
	}
}

func (s *Server) getSession(id string) (ImageSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[id]
	return sess, ok
}

func (s *Server) deleteSession(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
}
