package service

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"chatgpt2api/internal/cloudstorage"
	"chatgpt2api/internal/config"
	"chatgpt2api/internal/storage"
)

// CloudImageRecord holds the result of a cloud storage upload.
type CloudImageRecord struct {
	CloudURL    string `json:"cloud_url"`
	EncryptKey  string `json:"encrypt_key"`  // base62-encoded AES key
	HeadSize    int    `json:"head_size"`    // GIF header size to strip during download
	Uploader    string `json:"uploader"`     // "线路A1" or "线路A4"
	UploadedAt  int64  `json:"uploaded_at"`  // unix timestamp
	ContentType string `json:"content_type"` // original image MIME type
}

// CloudStorageService manages uploading images to free cloud storage.
type CloudStorageService struct {
	config      *config.Store
	httpClient  *http.Client
	cookieStore *CloudCookieStore
	mu          sync.RWMutex
	jsonStore   storage.JSONDocumentBackend
}

// NewCloudStorageService creates a new cloud storage service.
// If httpClient is nil, a default one will be created lazily.
func NewCloudStorageService(cfg *config.Store, httpClient *http.Client, backend storage.Backend) *CloudStorageService {
	return &CloudStorageService{
		config:     cfg,
		httpClient: httpClient,
		jsonStore:  jsonDocumentStoreFromBackend(backend),
		// cookieStore is lazily initialized on first use via config.StorageBackend()
	}
}

// GetHTTPClient returns the HTTP client, creating a default one if nil.
func (s *CloudStorageService) GetHTTPClient() *http.Client {
	s.mu.RLock()
	client := s.httpClient
	s.mu.RUnlock()
	if client != nil {
		return client
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.httpClient != nil {
		return s.httpClient
	}
	// Get the proxy URL from config
	proxyURL := s.config.Proxy()
	var proxyFunc func(*http.Request) (*url.URL, error)
	if proxyURL != "" {
		if parsed, err := url.Parse(proxyURL); err == nil {
			proxyFunc = http.ProxyURL(parsed)
		}
	}
	if proxyFunc == nil {
		proxyFunc = http.ProxyFromEnvironment
	}

	s.httpClient = &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			Proxy: proxyFunc,
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
			},
		},
	}
	return s.httpClient
}

// GetCookieStore returns the CloudCookieStore, initializing it lazily.
func (s *CloudStorageService) GetCookieStore() *CloudCookieStore {
	s.mu.RLock()
	store := s.cookieStore
	s.mu.RUnlock()
	if store != nil {
		return store
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cookieStore != nil {
		return s.cookieStore
	}
	backend, err := s.config.StorageBackend()
	if err != nil {
		return nil
	}
	s.cookieStore = NewCloudCookieStore(backend)
	return s.cookieStore
}

// UploadImage uploads an image to cloud storage and returns the result record.
// It encrypts the image data with AES-256, prepends a GIF header, and uploads
// via A4 (docs.qq.com) if a live cookie is available, falling back to A1 (flash.cn).
func (s *CloudStorageService) UploadImage(ctx context.Context, imageData []byte, filename string) (*CloudImageRecord, error) {
	if !s.config.CloudStorageEnabled() {
		return nil, fmt.Errorf("cloud storage is disabled")
	}

	client := s.GetHTTPClient()

	// Generate GIF head for camouflage
	gifHead, err := cloudstorage.GenerateDefaultGIF()
	if err != nil {
		return nil, fmt.Errorf("generate gif head: %w", err)
	}
	headSize := len(gifHead)

	// Generate random AES-256 key (32 bytes)
	aesKey, err := cloudstorage.GenerateRandomByteArray(32)
	if err != nil {
		return nil, fmt.Errorf("generate aes key: %w", err)
	}

	// Encrypt image data with AES-256-CBC
	encrypted, err := cloudstorage.EncryptAES(imageData, aesKey)
	if err != nil {
		return nil, fmt.Errorf("encrypt image: %w", err)
	}

	// Prepend GIF head to encrypted data
	uploadData := make([]byte, 0, headSize+len(encrypted))
	uploadData = append(uploadData, gifHead...)
	uploadData = append(uploadData, encrypted...)

	// Encode key for storage
	encryptKey := cloudstorage.Encode(aesKey)

	// Select uploader and upload with retries
	uploader := s.selectUploader()
	if uploader == nil {
		return nil, fmt.Errorf("no available uploader")
	}

	var lastErr error
	var rawURL string
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(100 * time.Millisecond)
		}

		rawURL, err = uploader.DoUpload(ctx, client, uploadData, headSize)
		if err == nil && rawURL != "" {
			break
		}
		lastErr = err
		if attempt < 2 {
			// Log masked error for debugging
			log.Printf("[cloudstorage] upload attempt %d/%d failed: %v", attempt+1, 3, err)
		}
	}
	if rawURL == "" {
		if lastErr != nil {
			return nil, fmt.Errorf("upload failed after 3 attempts: %w", lastErr)
		}
		return nil, fmt.Errorf("upload failed after 3 attempts")
	}

	// Append hash fragment for integrity verification
	cloudURL := cloudstorage.WithHashFragment(rawURL, imageData)

	// Determine content type from filename
	contentType := contentTypeFromFilename(filename)

	return &CloudImageRecord{
		CloudURL:    cloudURL,
		EncryptKey:  encryptKey,
		HeadSize:    headSize,
		Uploader:    uploader.Name(),
		UploadedAt:  time.Now().Unix(),
		ContentType: contentType,
	}, nil
}

// selectUploader selects the best available uploader based on config and cookie aliveness.
func (s *CloudStorageService) selectUploader() cloudstorage.Uploader {
	preference := s.config.CloudStorageUploader()

	// If explicitly set to a1, use it
	if preference == "a1" {
		return cloudstorage.NewA1Uploader()
	}

	// Try A4 if preference is "a4" or "auto"
	if preference == "a4" || preference == "auto" {
		// Check for alive A4 cookie from stored cookies
		var a4Cookie string
		if store := s.GetCookieStore(); store != nil {
			if alive, err := store.GetAliveCookie(); err == nil && alive != nil {
				a4Cookie = alive.Cookie
			}
		}

		// Fall back to env-configured A4 cookie if no stored cookie is alive
		if a4Cookie == "" {
			a4Cookie = s.config.A4Cookie()
		}

		if a4Cookie != "" {
			return cloudstorage.NewA4Uploader(a4Cookie)
		}

		// If preference is a4 but no cookie available, return nil
		if preference == "a4" {
			return nil
		}
	}

	// Default fallback: A1
	return cloudstorage.NewA1Uploader()
}

// Close performs cleanup for the cloud storage service.
func (s *CloudStorageService) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.httpClient != nil {
		s.httpClient.CloseIdleConnections()
	}
	return nil
}

// Enabled returns whether cloud storage is enabled via config.
func (s *CloudStorageService) Enabled() bool {
	return s.config.CloudStorageEnabled()
}

// SaveRecord persists a cloud image record to the database keyed by the
// relative image path.
func (s *CloudStorageService) SaveRecord(ctx context.Context, imageRel string, record *CloudImageRecord) error {
	if s.jsonStore == nil {
		return fmt.Errorf("no storage backend available for cloud records")
	}
	return saveStoredJSON(s.jsonStore, "cloud_image/"+imageRel+".json", record)
}

// GetRecord retrieves a cloud image record for the given relative image path.
func (s *CloudStorageService) GetRecord(ctx context.Context, imageRel string) (*CloudImageRecord, error) {
	if s.jsonStore == nil {
		return nil, fmt.Errorf("no storage backend available for cloud records")
	}
	raw := loadStoredJSON(s.jsonStore, "cloud_image/"+imageRel+".json")
	if raw == nil {
		return nil, fmt.Errorf("no cloud record found for %s", imageRel)
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid cloud record format for %s", imageRel)
	}
	return &CloudImageRecord{
		CloudURL:    stringValue(m, "cloud_url"),
		EncryptKey:  stringValue(m, "encrypt_key"),
		HeadSize:    int(int64Value(m, "head_size")),
		Uploader:    stringValue(m, "uploader"),
		UploadedAt:  int64Value(m, "uploaded_at"),
		ContentType: stringValue(m, "content_type"),
	}, nil
}

// maskCookie returns a masked version of a cookie string for safe logging.
func maskCookie(cookie string) string {
	if len(cookie) <= 12 {
		return "***"
	}
	return cookie[:8] + "..." + cookie[len(cookie)-4:]
}

// int64Value extracts an int64 value from a map, returning 0 if the key is missing
// or the value is not a number.
func int64Value(m map[string]any, key string) int64 {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	default:
		return 0
	}
}

func contentTypeFromFilename(filename string) string {
	filename = strings.ToLower(filename)
	switch {
	case strings.HasSuffix(filename, ".png"):
		return "image/png"
	case strings.HasSuffix(filename, ".jpg"), strings.HasSuffix(filename, ".jpeg"):
		return "image/jpeg"
	case strings.HasSuffix(filename, ".gif"):
		return "image/gif"
	case strings.HasSuffix(filename, ".webp"):
		return "image/webp"
	case strings.HasSuffix(filename, ".bmp"):
		return "image/bmp"
	case strings.HasSuffix(filename, ".svg"):
		return "image/svg+xml"
	default:
		return "image/png"
	}
}
