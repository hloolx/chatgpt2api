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
	CloudURL       string `json:"cloud_url"`
	DirectURL      string `json:"direct_url,omitempty"` // direct public URL (S3 with PublicURL)
	EncryptKey     string `json:"encrypt_key,omitempty"` // base62-encoded AES key (empty for direct mode)
	HeadSize       int    `json:"head_size"`             // GIF header size to strip during download
	Uploader       string `json:"uploader"`              // "线路A1", "线路A4", or "S3"
	UploadedAt     int64  `json:"uploaded_at"`           // unix timestamp
	ContentType    string `json:"content_type"`          // original image MIME type
	StorageLocation string `json:"storage_location"`     // "local" or "cloud"
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

// GetHTTPClient returns the HTTP client for cloud storage operations.
// Uses the dedicated cloud proxy (CHATGPT2API_CLOUD_PROXY) if configured and enabled.
// When cloud_proxy_enabled is false, cloud storage operations use direct connection (no proxy).
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

	var proxyFunc func(*http.Request) (*url.URL, error)

	// Check if cloud proxy is enabled
	if s.config.CloudProxyEnabled() {
		// Use dedicated cloud proxy if configured
		proxyURL := s.config.CloudProxy()
		if proxyURL != "" {
			if parsed, err := url.Parse(proxyURL); err == nil {
				proxyFunc = http.ProxyURL(parsed)
			}
		}
	}
	// When cloud_proxy_enabled is false, proxyFunc remains nil (direct connection)

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
// For S3 with PublicURL configured, uploads the raw image (no encryption) and sets DirectURL.
// For A1/A4 or S3 without PublicURL, encrypts with AES-256 and optionally prepends a GIF header.
func (s *CloudStorageService) UploadImage(ctx context.Context, imageData []byte, filename string) (*CloudImageRecord, error) {
	if !s.config.CloudStorageEnabled() {
		return nil, fmt.Errorf("cloud storage is disabled")
	}

	client := s.GetHTTPClient()

	uploader := s.selectUploader()
	if uploader == nil {
		return nil, fmt.Errorf("no available uploader")
	}

	isS3 := uploader.Name() == "S3"
	s3Direct := isS3 && s.config.S3PublicURL() != ""

	var headSize int
	var uploadData []byte
	var encryptKey string

	if s3Direct {
		// S3 direct mode: upload raw image, no encryption
		headSize = 0
		uploadData = imageData
		encryptKey = ""
	} else {
		// Encrypted mode
		aesKey, err := cloudstorage.GenerateRandomByteArray(32)
		if err != nil {
			return nil, fmt.Errorf("generate aes key: %w", err)
		}
		encrypted, err := cloudstorage.EncryptAES(imageData, aesKey)
		if err != nil {
			return nil, fmt.Errorf("encrypt image: %w", err)
		}
		encryptKey = cloudstorage.Encode(aesKey)

		if isS3 {
			headSize = 0
			uploadData = encrypted
		} else {
			gifHead, gifErr := cloudstorage.GenerateDefaultGIF()
			if gifErr != nil {
				return nil, fmt.Errorf("generate gif head: %w", gifErr)
			}
			headSize = len(gifHead)
			uploadData = make([]byte, 0, headSize+len(encrypted))
			uploadData = append(uploadData, gifHead...)
			uploadData = append(uploadData, encrypted...)
		}
	}

	var lastErr error
	var rawURL string
	var err error
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
			log.Printf("[cloudstorage] upload attempt %d/%d failed: %v", attempt+1, 3, err)
		}
	}
	if rawURL == "" {
		if lastErr != nil {
			return nil, fmt.Errorf("upload failed after 3 attempts: %w", lastErr)
		}
		return nil, fmt.Errorf("upload failed after 3 attempts")
	}

	cloudURL := cloudstorage.WithHashFragment(rawURL, imageData)
	contentType := contentTypeFromFilename(filename)

	record := &CloudImageRecord{
		CloudURL:       cloudURL,
		EncryptKey:     encryptKey,
		HeadSize:       headSize,
		Uploader:       uploader.Name(),
		UploadedAt:     time.Now().Unix(),
		ContentType:    contentType,
		StorageLocation: "cloud",
	}
	if s3Direct {
		record.DirectURL = rawURL
	}
	return record, nil
}

// selectUploader selects the best available uploader based on config and cookie aliveness.
func (s *CloudStorageService) selectUploader() cloudstorage.Uploader {
	preference := s.config.CloudStorageUploader()

	// If explicitly set to s3, use it
	if preference == "s3" {
		return cloudstorage.NewS3Uploader(cloudstorage.S3Config{
			Endpoint:       s.config.S3Endpoint(),
			Region:         s.config.S3Region(),
			AccessKeyID:    s.config.S3AccessKeyID(),
			SecretAccessKey: s.config.S3SecretAccessKey(),
			Bucket:         s.config.S3Bucket(),
			PublicURL:      s.config.S3PublicURL(),
			PathPrefix:     s.config.S3PathPrefix(),
			ForcePathStyle: s.config.S3ForcePathStyle(),
		})
	}

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

// DeleteRecord removes a cloud image record from the database.
func (s *CloudStorageService) DeleteRecord(ctx context.Context, imageRel string) error {
	if s.jsonStore == nil {
		return fmt.Errorf("no storage backend available for cloud records")
	}
	return s.jsonStore.DeleteJSONDocument("cloud_image/" + imageRel + ".json")
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
		CloudURL:       stringValue(m, "cloud_url"),
		DirectURL:      stringValue(m, "direct_url"),
		EncryptKey:     stringValue(m, "encrypt_key"),
		HeadSize:       int(int64Value(m, "head_size")),
		Uploader:       stringValue(m, "uploader"),
		UploadedAt:     int64Value(m, "uploaded_at"),
		ContentType:    stringValue(m, "content_type"),
		StorageLocation: stringValue(m, "storage_location"),
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
