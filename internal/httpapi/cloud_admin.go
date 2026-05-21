package httpapi

import (
	"context"
	"io"
	"net/http"
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
		util.WriteJSON(w, http.StatusOK, map[string]any{"enabled": false})
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
		"uploader_preference": a.config.CloudStorageUploader(),
		"active_uploader":     activeUploader,
		"a4_cookies_total":    len(cookies),
		"a4_cookies_alive":    aliveCount,
	})
}

// handleCloudTestUpload - POST uploads a small test image to verify cloud storage works
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

	// Generate a small test image (1x1 PNG pixel)
	testImage := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xDE, 0x00, 0x00, 0x00, 0x0C, 0x49, 0x44, 0x41, 0x54, 0x08, 0xD7, 0x63, 0xF8, 0xCF, 0xC0, 0x00, 0x00, 0x00, 0x03, 0x00, 0x01, 0x89, 0xE3, 0x85, 0x41, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	record, err := a.cloudStorage.UploadImage(ctx, testImage, "test-upload.png")
	if err != nil {
		util.WriteJSON(w, http.StatusInternalServerError, map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}

	// Also test that we can retrieve the image from cloud (download + decrypt)
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

	util.WriteJSON(w, http.StatusOK, map[string]any{
		"ok":           true,
		"uploader":     record.Uploader,
		"cloud_url":    record.CloudURL,
		"content_type": record.ContentType,
		"verify_ok":    verifyOk,
	})
}

func maskCookieValue(cookie string) string {
	if len(cookie) <= 12 {
		return "***"
	}
	return cookie[:8] + "..." + cookie[len(cookie)-4:]
}
