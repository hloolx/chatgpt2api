package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"chatgpt2api/internal/cloudstorage"
	"chatgpt2api/internal/storage"
)

// A4Cookie represents a stored A4 cookie for cloud storage upload.
type A4Cookie struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Cookie      string `json:"cookie"`
	Alive       *bool  `json:"alive"`        // nil=unchecked, true=alive, false=dead
	Error       string `json:"error"`
	LastChecked string `json:"last_checked"`
}

// CloudCookieStore manages A4 cookies with persistent storage.
type CloudCookieStore struct {
	mu    sync.RWMutex
	store storage.JSONDocumentBackend
}

const cloudCookieDocumentName = "cloud_cookies/a4_cookies"

// NewCloudCookieStore creates a new cookie store backed by the given storage backend.
func NewCloudCookieStore(store storage.Backend) *CloudCookieStore {
	return &CloudCookieStore{
		store: jsonDocumentStoreFromBackend(store),
	}
}

// ListCookies returns all stored A4 cookies.
func (s *CloudCookieStore) ListCookies() ([]A4Cookie, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.listCookiesLocked()
}

func (s *CloudCookieStore) listCookiesLocked() ([]A4Cookie, error) {
	raw := loadStoredJSON(s.store, cloudCookieDocumentName)
	if raw == nil {
		return nil, nil
	}
	switch v := raw.(type) {
	case []any:
		cookies := make([]A4Cookie, 0, len(v))
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				cookies = append(cookies, a4CookieFromMap(m))
			}
		}
		return cookies, nil
	case []map[string]any:
		cookies := make([]A4Cookie, 0, len(v))
		for _, item := range v {
			cookies = append(cookies, a4CookieFromMap(item))
		}
		return cookies, nil
	case map[string]any:
		// Single cookie stored as object — wrap in slice
		return []A4Cookie{a4CookieFromMap(v)}, nil
	default:
		return nil, nil
	}
}

// SaveCookie adds or updates a cookie in the store.
func (s *CloudCookieStore) SaveCookie(cookie A4Cookie) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cookies, err := s.listCookiesLocked()
	if err != nil {
		cookies = nil
	}

	found := false
	for i, c := range cookies {
		if c.ID == cookie.ID {
			cookies[i] = cookie
			found = true
			break
		}
	}
	if !found {
		if cookie.ID == "" {
			cookie.ID = fmt.Sprintf("a4-%d", time.Now().UnixNano())
		}
		cookies = append(cookies, cookie)
	}

	return saveStoredJSON(s.store, cloudCookieDocumentName, cookies)
}

// DeleteCookie removes a cookie by ID.
func (s *CloudCookieStore) DeleteCookie(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cookies, err := s.listCookiesLocked()
	if err != nil {
		return err
	}

	filtered := make([]A4Cookie, 0, len(cookies))
	for _, c := range cookies {
		if c.ID != id {
			filtered = append(filtered, c)
		}
	}

	return saveStoredJSON(s.store, cloudCookieDocumentName, filtered)
}

// GetAliveCookie returns the first cookie with Alive == true, or nil if none alive.
func (s *CloudCookieStore) GetAliveCookie() (*A4Cookie, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cookies, err := s.listCookiesLocked()
	if err != nil {
		return nil, err
	}

	for _, c := range cookies {
		if c.Alive != nil && *c.Alive {
			copy := c
			return &copy, nil
		}
	}
	return nil, nil
}

// MarkCookieAlive updates the aliveness status of a cookie.
func (s *CloudCookieStore) MarkCookieAlive(id string, alive bool, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cookies, err := s.listCookiesLocked()
	if err != nil {
		cookies = nil
	}

	for i, c := range cookies {
		if c.ID == id {
			cookies[i].Alive = &alive
			cookies[i].Error = errMsg
			cookies[i].LastChecked = time.Now().UTC().Format(time.RFC3339Nano)
			return saveStoredJSON(s.store, cloudCookieDocumentName, cookies)
		}
	}
	return fmt.Errorf("cookie %s not found", id)
}

// CheckAllCookies iterates all cookies, checks each via cloudstorage.CheckA4Cookie, and updates aliveness.
func (s *CloudCookieStore) CheckAllCookies(ctx context.Context, client *http.Client) error {
	cookies, err := s.ListCookies()
	if err != nil {
		return err
	}

	for _, c := range cookies {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		_, checkErr := cloudstorage.CheckA4Cookie(ctx, client, c.Cookie)
		if checkErr != nil {
			_ = s.MarkCookieAlive(c.ID, false, checkErr.Error())
		} else {
			_ = s.MarkCookieAlive(c.ID, true, "")
		}
	}
	return nil
}

func a4CookieFromMap(m map[string]any) A4Cookie {
	cookie := A4Cookie{
		ID:          stringValue(m, "id"),
		Name:        stringValue(m, "name"),
		Cookie:      stringValue(m, "cookie"),
		Error:       stringValue(m, "error"),
		LastChecked: stringValue(m, "last_checked"),
	}
	if aliveRaw, ok := m["alive"]; ok {
		switch v := aliveRaw.(type) {
		case bool:
			alive := v
			cookie.Alive = &alive
		case string:
			lower := strings.ToLower(strings.TrimSpace(v))
			if lower == "true" || lower == "1" {
				alive := true
				cookie.Alive = &alive
			} else if lower == "false" || lower == "0" {
				alive := false
				cookie.Alive = &alive
			}
		case json.Number:
			if n, err := v.Int64(); err == nil && n == 1 {
				alive := true
				cookie.Alive = &alive
			} else if err == nil && n == 0 {
				alive := false
				cookie.Alive = &alive
			}
		case float64:
			if v == 1 {
				alive := true
				cookie.Alive = &alive
			} else if v == 0 {
				alive := false
				cookie.Alive = &alive
			}
		}
	}
	return cookie
}

func stringValue(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
		return fmt.Sprint(v)
	}
	return ""
}
