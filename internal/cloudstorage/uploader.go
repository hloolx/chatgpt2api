package cloudstorage

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"time"
)

// Uploader is the interface for cloud storage upload providers.
type Uploader interface {
	Name() string
	Referer() string
	DoUpload(ctx context.Context, client *http.Client, fileData []byte, headSize int) (string, error)
}

// A1Uploader — anonymous upload to kf.flash.cn
type A1Uploader struct{}

// A4Uploader — cookie-authenticated upload to docs.qq.com (Tencent Docs)
type A4Uploader struct {
	Cookie string
}

func NewA1Uploader() *A1Uploader {
	return &A1Uploader{}
}

func NewA4Uploader(cookie string) *A4Uploader {
	return &A4Uploader{Cookie: strings.TrimSpace(cookie)}
}

func (u *A1Uploader) Name() string {
	return "线路A1"
}

func (u *A1Uploader) Referer() string {
	return "https://kf.flash.cn/"
}

func (u *A1Uploader) DoUpload(ctx context.Context, client *http.Client, fileData []byte, headSize int) (string, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("flag", ""); err != nil {
		return "", err
	}

	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="FileUploadForm[file]"; filename="%s.gif"`, randomString(5)))
	header.Set("Content-Type", "image/gif")
	part, err := writer.CreatePart(header)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(fileData); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.Referer()+"service/upload", body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Referer", u.Referer())
	req.Header.Set("User-Agent", defaultUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("上传失败: %s %s", resp.Status, string(data))
	}

	var result []struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("上传响应解析失败: %w: %s", err, string(data))
	}
	if len(result) == 0 || result[0].URL == "" {
		return "", fmt.Errorf("上传失败: %s", string(data))
	}
	if result[0].URL[0] == '/' {
		return "https:" + result[0].URL, nil
	}
	return result[0].URL, nil
}

func (u *A4Uploader) Name() string {
	return "线路A4"
}

func (u *A4Uploader) Referer() string {
	return "https://docs.qq.com/"
}

func (u *A4Uploader) DoUpload(ctx context.Context, client *http.Client, fileData []byte, headSize int) (string, error) {
	if u.Cookie == "" {
		return "", fmt.Errorf("线路A4需要设置腾讯文档 Cookie")
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="1.gif"`)
	header.Set("Content-Type", "image/gif")
	part, err := writer.CreatePart(header)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(fileData); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://docs.qq.com/api/docsdata/image/upload", body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Referer", u.Referer())
	req.Header.Set("Cookie", u.Cookie)
	req.Header.Set("User-Agent", defaultUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("上传失败: %s %s", resp.Status, string(data))
	}

	var result struct {
		RetCode int    `json:"retcode"`
		RetMsg  string `json:"retmsg"`
		URL     string `json:"url"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("上传响应解析失败: %w: %s", err, string(data))
	}
	if result.RetCode != 0 || result.URL == "" {
		return "", fmt.Errorf("上传失败: %s", string(data))
	}
	return strings.Split(result.URL, "?")[0], nil
}

// CheckA4Cookie verifies a Tencent Docs cookie by attempting a probe upload
// of a 1x1 transparent GIF.
func CheckA4Cookie(ctx context.Context, client *http.Client, cookie string) (string, error) {
	probeGIF, err := base64.StdEncoding.DecodeString("R0lGODlhAQABAIAAAAAAAP///ywAAAAAAQABAAACAUwAOw==")
	if err != nil {
		return "", err
	}
	return NewA4Uploader(cookie).DoUpload(ctx, client, probeGIF, 0)
}

const defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

func randomString(length int) string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		for i := range buf {
			buf[i] = chars[time.Now().UnixNano()%int64(len(chars))]
		}
		return string(buf)
	}
	for i, b := range buf {
		buf[i] = chars[int(b)%len(chars)]
	}
	return string(buf)
}
