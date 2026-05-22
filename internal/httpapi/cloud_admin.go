package httpapi

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"chatgpt2api/internal/cloudstorage"
	"chatgpt2api/internal/service"
	"chatgpt2api/internal/util"
)

// handleCloudCookies - GET returns list, PUT saves a cookie
func (a *App) handleCloudCookies(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	if identity.Role != service.AuthRoleAdmin {
		util.WriteError(w, http.StatusForbidden, "admin permission required")
		return
	}

	store := a.cloudStorage.GetCookieStore()
	if store == nil {
		util.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "cloud storage not available"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		cookies, err := store.ListCookies()
		if err != nil {
			util.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		// Mask cookie values for security
		masked := make([]map[string]any, 0, len(cookies))
		for _, c := range cookies {
			masked = append(masked, map[string]any{
				"id":           c.ID,
				"name":         c.Name,
				"cookie":       maskCookieValue(c.Cookie),
				"alive":        c.Alive,
				"error":        c.Error,
				"last_checked": c.LastChecked,
			})
		}
		util.WriteJSON(w, http.StatusOK, map[string]any{"cookies": masked})

	case http.MethodPut:
		var cookie service.A4Cookie
		if err := util.DecodeJSON(r.Body, &cookie); err != nil {
			util.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if err := store.SaveCookie(cookie); err != nil {
			util.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		util.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleCloudCookieCheck - POST triggers aliveness check for all cookies
func (a *App) handleCloudCookieCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	if identity.Role != service.AuthRoleAdmin {
		util.WriteError(w, http.StatusForbidden, "admin permission required")
		return
	}

	store := a.cloudStorage.GetCookieStore()
	if store == nil {
		util.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "not available"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	if err := store.CheckAllCookies(ctx, a.cloudStorage.GetHTTPClient()); err != nil {
		util.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	cookies, _ := store.ListCookies()
	util.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "cookies": cookies})
}

// handleCloudCookieDelete - DELETE removes a cookie by ID
func (a *App) handleCloudCookieDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	if identity.Role != service.AuthRoleAdmin {
		util.WriteError(w, http.StatusForbidden, "admin permission required")
		return
	}

	id := r.URL.Query().Get("id")
	if id == "" {
		util.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "id required"})
		return
	}

	store := a.cloudStorage.GetCookieStore()
	if store == nil {
		util.WriteJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "not available"})
		return
	}

	if err := store.DeleteCookie(id); err != nil {
		util.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	util.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleCloudStorageStatus - GET returns cloud storage health
func (a *App) handleCloudStorageStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	if identity.Role != service.AuthRoleAdmin {
		util.WriteError(w, http.StatusForbidden, "admin permission required")
		return
	}

	if a.cloudStorage == nil || !a.cloudStorage.Enabled() {
		util.WriteJSON(w, http.StatusOK, map[string]any{
			"enabled":      false,
			"storage_mode": a.config.StorageMode(),
		})
		return
	}

	store := a.cloudStorage.GetCookieStore()
	cookies, _ := store.ListCookies()
	aliveCount := 0
	hasAliveCookie := false
	for _, c := range cookies {
		if c.Alive != nil && *c.Alive {
			aliveCount++
			hasAliveCookie = true
		}
	}

	// Determine the currently active uploader
	activeUploader := "A1 (fallback)"
	if hasAliveCookie && a.config.CloudStorageUploader() != "a1" {
		activeUploader = "A4"
	}

	util.WriteJSON(w, http.StatusOK, map[string]any{
		"enabled":             true,
		"storage_mode":        a.config.StorageMode(),
		"uploader_preference": a.config.CloudStorageUploader(),
		"active_uploader":     activeUploader,
		"a4_cookies_total":    len(cookies),
		"a4_cookies_alive":    aliveCount,
	})
}

// handleCloudTestUpload - POST uploads a user-provided image to verify cloud storage works
func (a *App) handleCloudTestUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	if identity.Role != service.AuthRoleAdmin {
		util.WriteError(w, http.StatusForbidden, "admin permission required")
		return
	}

	if a.cloudStorage == nil || !a.cloudStorage.Enabled() {
		util.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "cloud storage is not enabled"})
		return
	}

	// Parse multipart form (max 20MB)
	if err := r.ParseMultipartForm(20 << 20); err != nil {
		util.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "failed to parse upload: " + err.Error()})
		return
	}

	file, header, err := r.FormFile("image")
	if err != nil {
		util.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "missing 'image' file field: " + err.Error()})
		return
	}
	defer file.Close()

	// Read file data
	imageData, err := io.ReadAll(io.LimitReader(file, 20<<20))
	if err != nil {
		util.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to read file: " + err.Error()})
		return
	}

	if len(imageData) == 0 {
		util.WriteJSON(w, http.StatusBadRequest, map[string]any{"error": "empty file"})
		return
	}

	// Determine filename and extension
	filename := header.Filename
	if filename == "" {
		filename = "test-upload.png"
	}

	// Generate relative path: YYYY/MM/DD/hash.ext
	sum := md5.Sum(imageData)
	now := time.Now()
	relativeDir := filepath.Join(now.Format("2006"), now.Format("01"), now.Format("02"))
	ext := filepath.Ext(filename)
	if ext == "" {
		ext = ".png"
	}
	rel := filepath.Join(relativeDir, fmt.Sprintf("%d_%s%s", now.Unix(), hex.EncodeToString(sum[:]), ext))
	localPath := filepath.Join(a.config.ImagesDir(), rel)

	// Save to local disk
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		util.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to create directory: " + err.Error()})
		return
	}
	if err := os.WriteFile(localPath, imageData, 0o644); err != nil {
		util.WriteJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to save file: " + err.Error()})
		return
	}

	// Upload to cloud storage
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	record, err := a.cloudStorage.UploadImage(ctx, imageData, filename)
	if err != nil {
		util.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}

	// Save cloud record
	if err := a.cloudStorage.SaveRecord(ctx, rel, record); err != nil {
		util.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"ok":    false,
			"error": "failed to save cloud record: " + err.Error(),
		})
		return
	}

	// Build local access URL
	localURL := "/images/" + filepath.ToSlash(rel)

	// Verify cloud download+decrypt
	fetchCtx, fetchCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer fetchCancel()

	req, _ := http.NewRequestWithContext(fetchCtx, http.MethodGet, record.CloudURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := a.cloudStorage.GetHTTPClient().Do(req)
	verifyOk := false
	if err == nil && resp != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
		resp.Body.Close()
		if len(body) > record.HeadSize {
			aesKey := cloudstorage.Decode(record.EncryptKey)
			if aesKey != nil {
				if _, decErr := cloudstorage.DecryptAES(body[record.HeadSize:], aesKey); decErr == nil {
					verifyOk = true
				}
			}
		}
	}

	response := map[string]any{
		"ok":           true,
		"uploader":     record.Uploader,
		"cloud_url":    record.CloudURL,
		"local_url":    localURL,
		"local_path":   localPath,
		"content_type": record.ContentType,
		"verify_ok":    verifyOk,
	}
	if record.DirectURL != "" {
		response["direct_url"] = record.DirectURL
		response["verify_ok"] = true // Direct URL means no decrypt needed
	}
	util.WriteJSON(w, http.StatusOK, response)
}

func maskCookieValue(cookie string) string {
	if len(cookie) <= 12 {
		return "***"
	}
	return cookie[:8] + "..." + cookie[len(cookie)-4:]
}
