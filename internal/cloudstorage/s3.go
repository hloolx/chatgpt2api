package cloudstorage

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"
)

// S3Config holds configuration for S3-compatible object storage.
type S3Config struct {
	Endpoint       string // e.g. https://<account_id>.r2.cloudflarestorage.com
	Region         string // e.g. "auto" for R2, "us-west-002" for B2
	AccessKeyID    string
	SecretAccessKey string
	Bucket         string
	PublicURL      string // optional custom domain for public access
	PathPrefix     string // optional object key prefix
	ForcePathStyle bool   // true for MinIO (endpoint/bucket/key), false for R2/B2/AWS (bucket.endpoint/key)
}

// S3Uploader implements Uploader for S3-compatible object storage services
// (Cloudflare R2, Backblaze B2, MinIO, AWS S3, etc.).
type S3Uploader struct {
	config S3Config
}

// NewS3Uploader creates a new S3-compatible uploader.
func NewS3Uploader(cfg S3Config) *S3Uploader {
	return &S3Uploader{config: cfg}
}

func (u *S3Uploader) Name() string {
	return "S3"
}

func (u *S3Uploader) Referer() string {
	return u.config.Endpoint
}

func (u *S3Uploader) DoUpload(ctx context.Context, client *http.Client, fileData []byte, headSize int) (string, error) {
	key := u.objectKey()
	putURL := u.buildURL(key)

	payloadHash := sha256Hex(fileData)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, putURL, bytes.NewReader(fileData))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("x-amz-content-sha256", payloadHash)

	signS3Request(req, u.config.AccessKeyID, u.config.SecretAccessKey, u.config.Region, "s3", payloadHash)

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("s3 put: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("s3 upload failed: %s %s", resp.Status, string(body))
	}

	return u.publicURL(key), nil
}

// objectKey generates a unique object key for the uploaded file.
func (u *S3Uploader) objectKey() string {
	prefix := u.config.PathPrefix
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return prefix + randomString(8) + "_" + fmt.Sprint(time.Now().Unix()) + ".bin"
}

// buildURL constructs the full URL for an S3 object key.
func (u *S3Uploader) buildURL(key string) string {
	endpoint := strings.TrimRight(u.config.Endpoint, "/")
	if u.config.ForcePathStyle {
		return endpoint + "/" + u.config.Bucket + "/" + key
	}
	return strings.Replace(endpoint, "://", "://"+u.config.Bucket+".", 1) + "/" + key
}

// publicURL returns the URL used to access the object publicly.
func (u *S3Uploader) publicURL(key string) string {
	if u.config.PublicURL != "" {
		return strings.TrimRight(u.config.PublicURL, "/") + "/" + key
	}
	return u.buildURL(key)
}

// CheckS3Connection verifies S3 credentials by performing a HEAD request on the bucket.
func CheckS3Connection(ctx context.Context, client *http.Client, cfg S3Config) error {
	endpoint := strings.TrimRight(cfg.Endpoint, "/")
	var headURL string
	if cfg.ForcePathStyle {
		headURL = endpoint + "/" + cfg.Bucket
	} else {
		headURL = strings.Replace(endpoint, "://", "://"+cfg.Bucket+".", 1)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, headURL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	payloadHash := "UNSIGNED-PAYLOAD"
	req.Header.Set("x-amz-content-sha256", payloadHash)
	req.Header.Set("x-amz-date", time.Now().UTC().Format("20060102T150405Z"))

	signS3Request(req, cfg.AccessKeyID, cfg.SecretAccessKey, cfg.Region, "s3", payloadHash)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("s3 head bucket: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("s3 head bucket failed: %s %s", resp.Status, string(body))
	}
	return nil
}

// --- AWS Signature Version 4 ---

func signS3Request(req *http.Request, accessKeyID, secretAccessKey, region, service, payloadHash string) {
	now := time.Now().UTC()
	dateStamp := now.Format("20060102")
	amzDate := now.Format("20060102T150405Z")

	if req.Host == "" {
		req.Host = req.URL.Host
	}
	req.Header.Set("Host", req.Host)
	req.Header.Set("x-amz-date", amzDate)

	canonicalURI := req.URL.Path
	if canonicalURI == "" {
		canonicalURI = "/"
	} else {
		canonicalURI = path.Clean(canonicalURI)
		if !strings.HasPrefix(canonicalURI, "/") {
			canonicalURI = "/" + canonicalURI
		}
	}

	canonicalQueryString := req.URL.RawQuery

	signedHeaders, canonicalHeaders := canonicalizeHeaders(req)

	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI,
		canonicalQueryString,
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	credentialScope := dateStamp + "/" + region + "/" + service + "/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + credentialScope + "\n" + sha256Hex([]byte(canonicalRequest))

	signingKey := getSignatureKey(secretAccessKey, dateStamp, region, service)
	signature := hmacSHA256Hex(signingKey, stringToSign)

	authorization := "AWS4-HMAC-SHA256 Credential=" + accessKeyID + "/" + credentialScope + ", SignedHeaders=" + signedHeaders + ", Signature=" + signature
	req.Header.Set("Authorization", authorization)
}

func canonicalizeHeaders(req *http.Request) (string, string) {
	headers := make(map[string][]string)
	for k, v := range req.Header {
		headers[strings.ToLower(k)] = v
	}
	if _, ok := headers["host"]; !ok {
		headers["host"] = []string{req.Host}
	}

	keys := make([]string, 0, len(headers))
	for k := range headers {
		keys = append(keys, k)
	}
	// insertion sort for small slice
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}

	var signed []string
	var buf strings.Builder
	for _, k := range keys {
		signed = append(signed, k)
		buf.WriteString(k)
		buf.WriteString(":")
		buf.WriteString(strings.TrimSpace(headers[k][0]))
		buf.WriteString("\n")
	}

	return strings.Join(signed, ";"), buf.String()
}

var (
	signatureKeyCache     []byte
	signatureKeyCacheDate string
	signatureKeyCacheMu   sync.RWMutex
)

func getSignatureKey(secretKey, dateStamp, region, service string) []byte {
	cacheKey := dateStamp + "|" + region + "|" + service

	signatureKeyCacheMu.RLock()
	if signatureKeyCache != nil && signatureKeyCacheDate == cacheKey {
		result := signatureKeyCache
		signatureKeyCacheMu.RUnlock()
		return result
	}
	signatureKeyCacheMu.RUnlock()

	kDate := hmacSHA256([]byte("AWS4"+secretKey), dateStamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	kSigning := hmacSHA256(kService, "aws4_request")

	signatureKeyCacheMu.Lock()
	signatureKeyCache = kSigning
	signatureKeyCacheDate = cacheKey
	signatureKeyCacheMu.Unlock()

	return kSigning
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func hmacSHA256Hex(key []byte, data string) string {
	return hex.EncodeToString(hmacSHA256(key, data))
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
