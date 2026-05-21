package cloudstorage

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"image"
	"image/color"
	"image/gif"
	"net/url"
	"strings"
)

// GenerateDefaultGIF generates a random-color 8x8 pixel GIF image.
func GenerateDefaultGIF() ([]byte, error) {
	randomColor := []byte{0x33, 0x89, 0x9a}
	_, _ = rand.Read(randomColor)

	img := image.NewPaletted(image.Rect(0, 0, 8, 8), color.Palette{
		color.RGBA{R: randomColor[0], G: randomColor[1], B: randomColor[2], A: 255},
	})
	var buf bytes.Buffer
	if err := gif.Encode(&buf, img, nil); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// WithHashFragment appends a SHA-256 hash fragment (base62 encoded) to the URL.
func WithHashFragment(rawURL string, data []byte) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	hash := sha256.Sum256(data)
	u.Fragment = Encode(hash[:])
	return u.String()
}

// IsStoredFileURL validates that a URL looks like a real stored file URL
// (http/https with host, or tg scheme).
func IsStoredFileURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	switch u.Scheme {
	case "http", "https":
		return u.Host != ""
	case "tg":
		return u.Host == "file" && strings.TrimPrefix(u.Path, "/") != ""
	default:
		return false
	}
}
