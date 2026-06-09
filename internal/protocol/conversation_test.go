package protocol

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"chatgpt2api/internal/backend"
	"chatgpt2api/internal/service"
	"chatgpt2api/internal/util"
)

type testProtocolImageConfig struct {
	root string
}

type testProtocolProxyConfig struct{}

func (testProtocolProxyConfig) Proxy() string { return "" }

type testProtocolAccountLookup map[string]map[string]any

func (l testProtocolAccountLookup) GetAccount(accessToken string) map[string]any {
	return l[accessToken]
}

func (c testProtocolImageConfig) ImagesDir() string {
	path := filepath.Join(c.root, "images")
	_ = os.MkdirAll(path, 0o755)
	return path
}

func (c testProtocolImageConfig) ImageMetadataDir() string {
	path := filepath.Join(c.root, "image_metadata")
	_ = os.MkdirAll(path, 0o755)
	return path
}

func (c testProtocolImageConfig) BaseURL() string {
	return "https://example.test"
}

func TestFormatImageResultStoresOwnerName(t *testing.T) {
	config := testProtocolImageConfig{root: t.TempDir()}
	engine := &Engine{Config: config}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewNRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatalf("png.Encode() error = %v", err)
	}
	imageData := base64.StdEncoding.EncodeToString(encoded.Bytes())

	result := engine.FormatImageResult(
		[]map[string]any{{"b64_json": imageData}},
		"draw",
		"url",
		"https://example.test",
		"linuxdo:41499",
		"Cassianvale",
		123,
		"",
	)
	items, _ := result["data"].([]map[string]any)
	if len(items) != 1 {
		t.Fatalf("FormatImageResult() data = %#v", result["data"])
	}
	imageURL, _ := items[0]["url"].(string)
	rel := strings.TrimPrefix(imageURL, "https://example.test/images/")
	if rel == imageURL || rel == "" {
		t.Fatalf("image url = %q", imageURL)
	}

	data, err := os.ReadFile(filepath.Join(config.ImageMetadataDir(), filepath.FromSlash(rel)+".json"))
	if err != nil {
		t.Fatalf("ReadFile(metadata) error = %v", err)
	}
	var meta map[string]any
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("Unmarshal(metadata) error = %v", err)
	}
	if meta["owner_id"] != "linuxdo:41499" || meta["owner_name"] != "Cassianvale" {
		t.Fatalf("metadata = %#v", meta)
	}
}

func TestFormatImageResultSavesRawBytesAsRequestedFormat(t *testing.T) {
	config := testProtocolImageConfig{root: t.TempDir()}
	engine := &Engine{Config: config}
	src := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	src.Set(0, 0, color.NRGBA{R: 255, A: 255})
	src.Set(1, 0, color.NRGBA{G: 255, A: 255})
	src.Set(0, 1, color.NRGBA{B: 255, A: 255})
	src.Set(1, 1, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, src); err != nil {
		t.Fatalf("png.Encode() error = %v", err)
	}
	compression := 20

	result := engine.FormatImageResultWithOptions(
		[]map[string]any{{imageResultBytesKey: encoded.Bytes(), "revised_prompt": "raw bytes"}},
		"draw",
		"url",
		"https://example.test",
		"owner-1",
		"Alice",
		123,
		"",
		ImageOutputOptions{Format: "jpeg", Compression: &compression},
	)
	items, _ := result["data"].([]map[string]any)
	if len(items) != 1 {
		t.Fatalf("FormatImageResultWithOptions() data = %#v", result["data"])
	}
	if _, ok := items[0]["b64_json"]; ok {
		t.Fatalf("url response unexpectedly includes b64_json: %#v", items[0])
	}
	if items[0]["output_format"] != "jpeg" {
		t.Fatalf("output_format = %#v, want jpeg", items[0]["output_format"])
	}
	imageURL, _ := items[0]["url"].(string)
	if !strings.HasSuffix(imageURL, ".jpg") {
		t.Fatalf("image url = %q, want .jpg suffix", imageURL)
	}
	rel := strings.TrimPrefix(imageURL, "https://example.test/images/")
	stored, err := os.ReadFile(filepath.Join(config.ImagesDir(), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("ReadFile(stored image) error = %v", err)
	}
	_, format, err := image.DecodeConfig(bytes.NewReader(stored))
	if err != nil {
		t.Fatalf("DecodeConfig(stored image) error = %v", err)
	}
	if format != "jpeg" {
		t.Fatalf("stored format = %q, want jpeg", format)
	}
}

func TestFormatImageResultEncodesRequestedOutputFormat(t *testing.T) {
	config := testProtocolImageConfig{root: t.TempDir()}
	engine := &Engine{Config: config}
	src := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	src.Set(0, 0, color.NRGBA{R: 255, A: 255})
	src.Set(1, 0, color.NRGBA{G: 255, A: 255})
	src.Set(0, 1, color.NRGBA{B: 255, A: 255})
	src.Set(1, 1, color.NRGBA{R: 255, G: 255, B: 255, A: 128})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, src); err != nil {
		t.Fatalf("png.Encode() error = %v", err)
	}
	compression := 25

	result := engine.FormatImageResultWithOptions(
		[]map[string]any{{"b64_json": base64.StdEncoding.EncodeToString(encoded.Bytes())}},
		"draw",
		"b64_json",
		"https://example.test",
		"owner-1",
		"Alice",
		123,
		"",
		ImageOutputOptions{Format: "jpeg", Compression: &compression},
	)
	items, _ := result["data"].([]map[string]any)
	if len(items) != 1 {
		t.Fatalf("FormatImageResultWithOptions() data = %#v", result["data"])
	}
	if items[0]["output_format"] != "jpeg" {
		t.Fatalf("output_format = %#v, want jpeg", items[0]["output_format"])
	}
	imageURL, _ := items[0]["url"].(string)
	if !strings.HasSuffix(imageURL, ".jpg") {
		t.Fatalf("image url = %q, want .jpg suffix", imageURL)
	}
	b64, _ := items[0]["b64_json"].(string)
	converted, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("DecodeString() error = %v", err)
	}
	_, format, err := image.DecodeConfig(bytes.NewReader(converted))
	if err != nil {
		t.Fatalf("DecodeConfig() error = %v", err)
	}
	if format != "jpeg" {
		t.Fatalf("decoded format = %q, want jpeg", format)
	}
	rel := strings.TrimPrefix(imageURL, "https://example.test/images/")
	if _, err := os.Stat(filepath.Join(config.ImagesDir(), filepath.FromSlash(rel))); err != nil {
		t.Fatalf("stored jpeg missing: %v", err)
	}
}

func TestFormatImageResultRequestedFormatOverridesUpstreamFormat(t *testing.T) {
	config := testProtocolImageConfig{root: t.TempDir()}
	engine := &Engine{Config: config}
	src := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	src.Set(0, 0, color.NRGBA{R: 255, A: 255})
	src.Set(1, 0, color.NRGBA{G: 255, A: 255})
	src.Set(0, 1, color.NRGBA{B: 255, A: 255})
	src.Set(1, 1, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, src); err != nil {
		t.Fatalf("png.Encode() error = %v", err)
	}
	compression := 30

	result := engine.FormatImageResultWithOptions(
		[]map[string]any{{
			"b64_json":      base64.StdEncoding.EncodeToString(encoded.Bytes()),
			"output_format": "png",
		}},
		"draw",
		"b64_json",
		"https://example.test",
		"owner-1",
		"Alice",
		123,
		"",
		ImageOutputOptions{Format: "jpeg", Compression: &compression},
	)
	items, _ := result["data"].([]map[string]any)
	if len(items) != 1 {
		t.Fatalf("FormatImageResultWithOptions() data = %#v", result["data"])
	}
	if items[0]["output_format"] != "jpeg" {
		t.Fatalf("output_format = %#v, want requested jpeg", items[0]["output_format"])
	}
	imageURL, _ := items[0]["url"].(string)
	if !strings.HasSuffix(imageURL, ".jpg") {
		t.Fatalf("image url = %q, want .jpg suffix", imageURL)
	}
	b64, _ := items[0]["b64_json"].(string)
	converted, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("DecodeString() error = %v", err)
	}
	_, format, err := image.DecodeConfig(bytes.NewReader(converted))
	if err != nil {
		t.Fatalf("DecodeConfig() error = %v", err)
	}
	if format != "jpeg" {
		t.Fatalf("decoded format = %q, want jpeg", format)
	}
}

func TestFormatImageResultTrustsCodexUpstreamOutputFormat(t *testing.T) {
	config := testProtocolImageConfig{root: t.TempDir()}
	engine := &Engine{Config: config}
	upstreamBytes := []byte("RIFF\x10\x00\x00\x00WEBPcodex-upstream-bytes")
	compression := 40
	options := imageResultOutputOptions(
		ConversationRequest{Model: "codex-gpt-image-2", OutputFormat: "jpeg", OutputCompression: &compression},
		backend.ResponsesImageEvent{OutputFormat: "webp"},
	)
	if !options.TrustUpstreamFormat {
		t.Fatal("Codex result options should trust upstream format")
	}
	if options.Format != "webp" {
		t.Fatalf("Codex result format = %q, want upstream webp", options.Format)
	}
	if options.Compression != nil {
		t.Fatalf("Codex result compression = %#v, want nil", *options.Compression)
	}

	result := engine.FormatImageResultWithOptions(
		[]map[string]any{{
			"b64_json":      base64.StdEncoding.EncodeToString(upstreamBytes),
			"output_format": "webp",
		}},
		"draw",
		"b64_json",
		"https://example.test",
		"owner-1",
		"Alice",
		123,
		"",
		options,
	)
	items, _ := result["data"].([]map[string]any)
	if len(items) != 1 {
		t.Fatalf("FormatImageResultWithOptions() data = %#v", result["data"])
	}
	if items[0]["output_format"] != "webp" {
		t.Fatalf("output_format = %#v, want upstream webp", items[0]["output_format"])
	}
	imageURL, _ := items[0]["url"].(string)
	if !strings.HasSuffix(imageURL, ".webp") {
		t.Fatalf("image url = %q, want .webp suffix", imageURL)
	}
	b64, _ := items[0]["b64_json"].(string)
	returnedBytes, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("DecodeString() error = %v", err)
	}
	if !bytes.Equal(returnedBytes, upstreamBytes) {
		t.Fatalf("returned bytes = %q, want upstream bytes %q", returnedBytes, upstreamBytes)
	}
	rel := strings.TrimPrefix(imageURL, "https://example.test/images/")
	storedBytes, err := os.ReadFile(filepath.Join(config.ImagesDir(), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("ReadFile(stored image) error = %v", err)
	}
	if !bytes.Equal(storedBytes, upstreamBytes) {
		t.Fatalf("stored bytes = %q, want upstream bytes %q", storedBytes, upstreamBytes)
	}
}

func TestFormatImageResultIgnoresWebPOutputCompression(t *testing.T) {
	config := testProtocolImageConfig{root: t.TempDir()}
	engine := &Engine{Config: config}
	src := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	src.Set(0, 0, color.NRGBA{R: 255, A: 255})
	src.Set(1, 0, color.NRGBA{G: 255, A: 255})
	src.Set(0, 1, color.NRGBA{B: 255, A: 255})
	src.Set(1, 1, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, src); err != nil {
		t.Fatalf("png.Encode() error = %v", err)
	}
	compression := 90

	result := engine.FormatImageResultWithOptions(
		[]map[string]any{{
			"b64_json":           base64.StdEncoding.EncodeToString(encoded.Bytes()),
			"output_format":      "webp",
			"output_compression": 10,
		}},
		"draw",
		"b64_json",
		"https://example.test",
		"owner-1",
		"Alice",
		123,
		"",
		ImageOutputOptions{Format: "webp", Compression: &compression},
	)
	items, _ := result["data"].([]map[string]any)
	if len(items) != 1 {
		t.Fatalf("FormatImageResultWithOptions() data = %#v", result["data"])
	}
	if items[0]["output_format"] != "webp" {
		t.Fatalf("output_format = %#v, want webp", items[0]["output_format"])
	}
	imageURL, _ := items[0]["url"].(string)
	if !strings.HasSuffix(imageURL, ".webp") {
		t.Fatalf("image url = %q, want .webp suffix", imageURL)
	}
	b64, _ := items[0]["b64_json"].(string)
	converted, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("DecodeString() error = %v", err)
	}
	headerLen := min(len(converted), 12)
	header := converted[:headerLen]
	if !bytes.HasPrefix(header, []byte("RIFF")) || !bytes.Contains(header, []byte("WEBP")) {
		t.Fatalf("converted bytes are not webp: %x", header)
	}
}

func TestImageStreamErrorMessage(t *testing.T) {
	cloudflare := `bootstrap failed: status=403, body=<html><script>window._cf_chl_opt={}</script>Enable JavaScript and cookies to continue</html>`
	if got := imageStreamErrorMessage(cloudflare); got != "upstream returned Cloudflare challenge page; refresh browser fingerprint/session or change proxy" {
		t.Fatalf("cloudflare challenge error = %q", got)
	}

	cases := []string{
		"curl: (35) OpenSSL SSL_connect: SSL_ERROR_SYSCALL",
		"TLS connect error: connection reset by peer",
		"error: OPENSSL_INTERNAL:WRONG_VERSION_NUMBER",
		`Get "https://chatgpt.com/": surf: HTTP/2 request failed: uTLS.HandshakeContext() error: EOF; HTTP/1.1 fallback failed: uTLS.HandshakeContext() error: EOF`,
	}
	for _, input := range cases {
		if got := imageStreamErrorMessage(input); got != "upstream connection failed before TLS handshake completed; check proxy reachability to chatgpt.com or change proxy" {
			t.Fatalf("imageStreamErrorMessage(%q) = %q", input, got)
		}
	}
	if got := imageStreamErrorMessage("upstream returned 500"); got != "upstream returned 500" {
		t.Fatalf("non-connection error = %q", got)
	}
	flowControl := "connection error: FLOW_CONTROL_ERROR"
	if got := imageStreamErrorMessage(flowControl); got != "upstream image stream interrupted by HTTP/2 flow control; retry the request or change proxy if it repeats" {
		t.Fatalf("flow control error = %q", got)
	}
	if got := imageStreamErrorMessage(""); got != "upstream image request failed without error detail" {
		t.Fatalf("empty error = %q", got)
	}
}

func TestHandleImageGenerationsReturnsChineseErrorAfterRepeatedTextResponse(t *testing.T) {
	engine := &Engine{
		ImageTokenProvider: func(context.Context) (string, error) { return "test-token", nil },
		ImageClientFactory: func(string) *backend.Client { return nil },
	}
	calls := 0
	engine.StreamImageOutputsFunc = func(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
		calls++
		out := make(chan ImageOutput, 1)
		errCh := make(chan error, 1)
		out <- ImageOutput{Kind: "message", Model: request.Model, Index: index, Total: total, Created: time.Now().Unix(), Text: "你好！我是 ChatGPT。", UpstreamEventType: "image_text_response"}
		close(out)
		errCh <- nil
		close(errCh)
		return out, errCh
	}

	result, _, err := engine.HandleImageGenerations(context.Background(), map[string]any{
		"prompt": "你好，你是什么模型？",
		"model":  "gpt-image-2",
	})
	if err == nil {
		t.Fatal("HandleImageGenerations() error = nil, want text-response image error")
	}
	var imageErr *ImageGenerationError
	if !errors.As(err, &imageErr) {
		t.Fatalf("HandleImageGenerations() error = %T %v, want ImageGenerationError", err, err)
	}
	if imageErr.Code != "image_generation_text_response" || !strings.Contains(imageErr.Message, "模型返回了文字，没有返回图片") {
		t.Fatalf("image error = %#v", imageErr)
	}
	if calls != 2 {
		t.Fatalf("stream calls = %d, want initial call plus one retry", calls)
	}
	if result["output_type"] != "text" {
		t.Fatalf("output_type = %#v, want text in %#v", result["output_type"], result)
	}
	if !strings.Contains(util.Clean(result["message"]), "已自动重试一次仍未成功") {
		t.Fatalf("message = %#v, want Chinese retry failure", result["message"])
	}
}

func TestHandleImageGenerationsTranslatesArbitraryUpstreamImageText(t *testing.T) {
	const upstreamText = "上游返回的任何非排队文本都应该触发重试。"
	engine := &Engine{
		ImageTokenProvider: func(context.Context) (string, error) { return "test-token", nil },
		ImageClientFactory: func(string) *backend.Client { return nil },
	}
	engine.StreamImageOutputsFunc = func(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
		out := make(chan ImageOutput, 1)
		errCh := make(chan error, 1)
		out <- ImageOutput{Kind: "message", Model: request.Model, Index: index, Total: total, Created: time.Now().Unix(), Text: upstreamText, UpstreamEventType: "image_text_response"}
		close(out)
		errCh <- nil
		close(errCh)
		return out, errCh
	}

	result, _, err := engine.HandleImageGenerations(context.Background(), map[string]any{
		"prompt": "draw",
		"model":  "gpt-image-2",
	})
	if err == nil {
		t.Fatal("HandleImageGenerations() error = nil, want text-response image error")
	}
	if result["output_type"] != "text" || !strings.Contains(util.Clean(result["message"]), "模型返回了文字，没有返回图片") {
		t.Fatalf("result = %#v, want Chinese text-response failure", result)
	}
}

func TestHandleImageGenerationsRetriesTextResponseOnce(t *testing.T) {
	engine := &Engine{
		ImageTokenProvider: func(context.Context) (string, error) { return "test-token", nil },
		ImageClientFactory: func(string) *backend.Client { return nil },
	}
	var prompts []string
	engine.StreamImageOutputsFunc = func(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
		prompts = append(prompts, request.Prompt)
		out := make(chan ImageOutput, 1)
		errCh := make(chan error, 1)
		if len(prompts) == 1 {
			out <- ImageOutput{Kind: "message", Model: request.Model, Index: index, Total: total, Created: time.Now().Unix(), Text: "我是一个文字模型。", UpstreamEventType: "image_text_response"}
		} else {
			out <- ImageOutput{Kind: "result", Model: request.Model, Index: index, Total: total, Created: time.Now().Unix(), Data: []map[string]any{{"url": "https://example.test/retry.png"}}}
		}
		close(out)
		errCh <- nil
		close(errCh)
		return out, errCh
	}

	result, _, err := engine.HandleImageGenerations(context.Background(), map[string]any{
		"prompt":          "一只小猪喝酒",
		"model":           "gpt-image-2",
		"response_format": "url",
	})
	if err != nil {
		t.Fatalf("HandleImageGenerations() error = %v", err)
	}
	data := util.AsMapSlice(result["data"])
	if len(data) != 1 || data[0]["url"] != "https://example.test/retry.png" {
		t.Fatalf("result data = %#v", result["data"])
	}
	if len(prompts) != 2 {
		t.Fatalf("stream calls = %d, want retry once", len(prompts))
	}
	if prompts[0] != "生成图片：一只小猪喝酒" {
		t.Fatalf("first prompt = %q", prompts[0])
	}
	if !strings.Contains(prompts[1], "请重新生成图片") || !strings.Contains(prompts[1], "我是一个文字模型") {
		t.Fatalf("retry prompt = %q, want Chinese retry instruction with prior reason", prompts[1])
	}
}

func TestHandleImageGenerationsReturnsChineseFailureWithoutPromptRetry(t *testing.T) {
	engine := &Engine{
		ImageTokenProvider: func(context.Context) (string, error) { return "test-token", nil },
		ImageClientFactory: func(string) *backend.Client { return nil },
	}
	calls := 0
	engine.StreamImageOutputsFunc = func(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
		calls++
		out := make(chan ImageOutput, 1)
		errCh := make(chan error, 1)
		close(out)
		errCh <- fmt.Errorf("content policy blocked this image")
		close(errCh)
		return out, errCh
	}

	result, _, err := engine.HandleImageGenerations(context.Background(), map[string]any{
		"prompt": "a wine poster",
		"model":  "gpt-image-2",
	})
	if err == nil {
		t.Fatal("HandleImageGenerations() error = nil, want Chinese failure")
	}
	if calls != 1 {
		t.Fatalf("stream calls = %d, want no prompt retry for moderation error", calls)
	}
	var imageErr *ImageGenerationError
	if !errors.As(err, &imageErr) {
		t.Fatalf("error = %T %v, want ImageGenerationError", err, err)
	}
	if imageErr.StatusCode != 400 || imageErr.Code != "content_policy_violation" {
		t.Fatalf("image error = %#v, want moderation status/code", imageErr)
	}
	if !strings.Contains(err.Error(), "触发安全审核或内容政策限制") {
		t.Fatalf("error = %q, want Chinese moderation reason", err.Error())
	}
	if result["message"] != err.Error() {
		t.Fatalf("result message = %#v, want error text", result["message"])
	}
}

func TestStreamResponsesImageOutputsCompletesWithUpstreamRefusalText(t *testing.T) {
	const upstreamText = "非常抱歉，生成的图片可能违反了关于裸露、色情或情色内容的防护限制。如果你认为此判断有误，请重试或修改提示语。"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<html data-build="build-1"><script src="/backend-api/sentinel/sdk.js"></script></html>`))
		case r.Method == http.MethodPost && r.URL.Path == "/backend-api/sentinel/chat-requirements":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":"req-token","proofofwork":{"required":false},"turnstile":{"required":false},"arkose":{"required":false}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/backend-api/f/conversation/prepare":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"conduit_token":"conduit-token"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/backend-api/f/conversation":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"type\":\"title_generation\",\"title\":\"正在处理图片\",\"conversation_id\":\"conv-refused\"}\n\n"))
			_, _ = w.Write([]byte("data: {\"type\":\"message_stream_complete\",\"conversation_id\":\"conv-refused\"}\n\n"))
		case r.Method == http.MethodGet && r.URL.Path == "/backend-api/conversation/conv-refused":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"mapping":{
				"assistant-text":{"message":{"author":{"role":"assistant"},"create_time":3,"content":{"content_type":"text","parts":["` + upstreamText + `"]},"status":"finished_successfully","recipient":"all","metadata":{"model_slug":"gpt-5-5"}}}
			}}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	engine := &Engine{
		ImageTokenProvider: func(context.Context) (string, error) { return "test-token", nil },
		ImageClientFactory: func(token string) *backend.Client {
			client := backend.NewClient(token, nil, service.NewProxyService(testProtocolProxyConfig{}))
			client.BaseURL = server.URL
			return client
		},
	}

	outputs, imageErr := engine.StreamImageOutputsWithPool(context.Background(), ConversationRequest{
		Prompt: "edit",
		Model:  "gpt-image-2",
		N:      1,
	})
	result, err := engine.CollectImageOutputs(outputs, imageErr)
	if err != nil {
		t.Fatalf("CollectImageOutputs() err = %v", err)
	}
	if result["output_type"] != "text" || result["message"] != upstreamText {
		t.Fatalf("result = %#v, want upstream refusal text as text output", result)
	}
}

func TestIsFinalImageTextEventIgnoresImageGenMetadataWithResultIDs(t *testing.T) {
	toolFalse := false
	event := backend.ResponsesImageEvent{
		Type:           "server_ste_metadata",
		Text:           "Here is the generated image.",
		ToolInvoked:    &toolFalse,
		TurnUseCase:    "image gen",
		SedimentIDs:    []string{"file_image"},
		ConversationID: "conv-image",
	}

	if isFinalImageTextEvent(event) {
		t.Fatalf("isFinalImageTextEvent(%#v) = true, want false for image generation metadata", event)
	}
}

func TestIsFinalImageTextEventWaitsForBackendTextMarkerOnImageGenRefusal(t *testing.T) {
	event := backend.ResponsesImageEvent{
		Type:        "message_stream_complete",
		Text:        "非常抱歉，生成的图片可能违反了关于裸露、色情或情色内容的防护限制。如果你认为此判断有误，请重试或修改提示语。",
		TurnUseCase: "image gen",
	}

	if isFinalImageTextEvent(event) {
		t.Fatalf("isFinalImageTextEvent(%#v) = true, want false before backend marks final text", event)
	}

	event.Type = "image_text_response"
	if !isFinalImageTextEvent(event) {
		t.Fatalf("isFinalImageTextEvent(%#v) = false, want true after backend marks final text", event)
	}
}

func TestIsFinalImageTextEventKeepsQueuedImageNoticePending(t *testing.T) {
	event := backend.ResponsesImageEvent{
		Type:        "message_stream_complete",
		Text:        "正在处理图片，目前有很多人在创建图片，因此可能需要一点时间。图片准备好后我们会通知你。",
		TurnUseCase: "image gen",
	}

	if isFinalImageTextEvent(event) {
		t.Fatalf("isFinalImageTextEvent(%#v) = true, want false for queued image notice", event)
	}
}

func TestStreamResponsesImageOutputsReturnsAfterFinalImageResult(t *testing.T) {
	const png1x1 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+X2ioAAAAASUVORK5CYII="
	requestCancelled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/backend-api/codex/responses" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"type":"response.output_item.done","item":{"id":"img_1","type":"image_generation_call","result":"` + png1x1 + `","revised_prompt":"draw","output_format":"png"}}` + "\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		select {
		case <-r.Context().Done():
		case <-time.After(1500 * time.Millisecond):
		}
		close(requestCancelled)
	}))
	defer server.Close()

	client := backend.NewClient("token-1", testProtocolAccountLookup{
		"token-1": {"chatgpt_account_id": "acct-1"},
	}, service.NewProxyService(testProtocolProxyConfig{}))
	client.BaseURL = server.URL
	engine := &Engine{Config: testProtocolImageConfig{root: t.TempDir()}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	started := time.Now()
	outputs, errCh := engine.StreamResponsesImageOutputs(ctx, client, ConversationRequest{
		Prompt:         "draw",
		Model:          util.ImageModelCodex,
		N:              1,
		ResponseFormat: "url",
		BaseURL:        "https://example.test",
	}, 1, 1)
	result, err := engine.CollectImageOutputs(outputs, errCh)
	if err != nil {
		t.Fatalf("CollectImageOutputs() err = %v", err)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("CollectImageOutputs() took %s, want return before upstream stream closes", elapsed)
	}
	data := util.AsMapSlice(result["data"])
	if len(data) != 1 || util.Clean(data[0]["url"]) == "" {
		t.Fatalf("result data = %#v, want one saved image URL", result["data"])
	}
	select {
	case <-requestCancelled:
	case <-time.After(time.Second):
		t.Fatal("upstream stream was not cancelled after final image result")
	}
}

func TestIsTransientImageStreamErrorMessage(t *testing.T) {
	transient := []string{
		"responses SSE read error: stream error: stream ID 1; INTERNAL_ERROR; received from peer",
		"connection error: FLOW_CONTROL_ERROR",
		"http2: client connection lost",
		"unexpected EOF",
		"connection reset by peer",
		"stream closed",
		"bootstrap failed: upstream connection failed before TLS handshake completed; check proxy reachability to chatgpt.com or change proxy",
		`bootstrap failed: Get "https://chatgpt.com/": surf: HTTP/2 request failed: uTLS.HandshakeContext() error: EOF; HTTP/1.1 fallback failed: uTLS.HandshakeContext() error: EOF`,
	}
	for _, input := range transient {
		if !isTransientImageStreamErrorMessage(input) {
			t.Fatalf("isTransientImageStreamErrorMessage(%q) = false, want true", input)
		}
	}

	stable := []string{
		"upstream returned Cloudflare challenge page",
		"You've reached the image generation limit for now.",
		"invalid size: expected WIDTHxHEIGHT",
		"auth_chat_requirements failed: status=401",
	}
	for _, input := range stable {
		if isTransientImageStreamErrorMessage(input) {
			t.Fatalf("isTransientImageStreamErrorMessage(%q) = true, want false", input)
		}
	}
}

func TestStreamImageOutputsWithPoolRunsRequestedImagesConcurrently(t *testing.T) {
	engine := &Engine{
		ImageTokenProvider: func(context.Context) (string, error) { return "test-token", nil },
		ImageClientFactory: func(string) *backend.Client { return nil },
	}

	var mu sync.Mutex
	started := 0
	maxActive := 0
	release := make(chan struct{})
	engine.StreamImageOutputsFunc = func(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
		out := make(chan ImageOutput)
		errCh := make(chan error, 1)
		go func() {
			defer close(out)
			defer close(errCh)
			mu.Lock()
			started++
			if started > maxActive {
				maxActive = started
			}
			mu.Unlock()
			<-release
			out <- ImageOutput{
				Kind:    "result",
				Model:   request.Model,
				Index:   index,
				Total:   total,
				Created: int64(index),
				Data:    []map[string]any{{"url": imageURLForIndex(index)}},
			}
			mu.Lock()
			started--
			mu.Unlock()
			errCh <- nil
		}()
		return out, errCh
	}

	outputs, errCh := engine.StreamImageOutputsWithPool(context.Background(), ConversationRequest{
		Model: "gpt-image-2",
		N:     4,
	})

	time.Sleep(120 * time.Millisecond)
	mu.Lock()
	gotActive := maxActive
	mu.Unlock()
	if gotActive != 4 {
		t.Fatalf("max concurrent image workers = %d, want 4", gotActive)
	}

	close(release)
	for range outputs {
	}
	if err := <-errCh; err != nil {
		t.Fatalf("StreamImageOutputsWithPool() err = %v", err)
	}
}

func TestStreamImageOutputsWithPoolKeepsSiblingWorkerAfterPartialFailure(t *testing.T) {
	engine := &Engine{
		ImageTokenProvider: func(context.Context) (string, error) { return "test-token", nil },
		ImageClientFactory: func(string) *backend.Client { return nil },
	}

	secondStarted := make(chan struct{})
	firstFailed := make(chan struct{})
	releaseSecond := make(chan struct{})
	engine.StreamImageOutputsFunc = func(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
		out := make(chan ImageOutput, 1)
		errCh := make(chan error, 1)
		if index == 1 {
			go func() {
				defer close(out)
				defer close(errCh)
				select {
				case <-secondStarted:
				case <-ctx.Done():
				}
				close(firstFailed)
				errCh <- fmt.Errorf("content policy blocked this image")
			}()
			return out, errCh
		}
		go func() {
			defer close(out)
			defer close(errCh)
			select {
			case <-secondStarted:
			default:
				close(secondStarted)
			}
			<-releaseSecond
			if err := ctx.Err(); err != nil {
				errCh <- err
				return
			}
			out <- ImageOutput{
				Kind:    "result",
				Model:   request.Model,
				Index:   index,
				Total:   total,
				Created: int64(index),
				Data:    []map[string]any{{"url": imageURLForIndex(index)}},
			}
			errCh <- nil
		}()
		return out, errCh
	}

	outputs, errCh := engine.StreamImageOutputsWithPool(context.Background(), ConversationRequest{
		Model: "gpt-image-2",
		N:     2,
	})

	select {
	case <-secondStarted:
	case <-time.After(time.Second):
		t.Fatal("second image worker did not start")
	}
	select {
	case <-firstFailed:
	case <-time.After(time.Second):
		t.Fatal("first image worker did not fail")
	}
	close(releaseSecond)
	var data []map[string]any
	for output := range outputs {
		data = append(data, output.Data...)
	}
	err := <-errCh
	if err == nil {
		t.Fatal("StreamImageOutputsWithPool() err = nil, want first worker error")
	}
	if errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "触发安全审核或内容政策限制") {
		t.Fatalf("err = %v, want original Chinese moderation failure", err)
	}
	if len(data) != 1 || data[0]["url"] != imageURLForIndex(2) {
		t.Fatalf("partial data = %#v, want sibling result", data)
	}
}

func TestStreamImageOutputsWithPoolHonorsImageOutputSlotAcquirer(t *testing.T) {
	engine := &Engine{
		ImageTokenProvider: func(context.Context) (string, error) { return "test-token", nil },
		ImageClientFactory: func(string) *backend.Client { return nil },
	}

	var mu sync.Mutex
	active := 0
	maxActive := 0
	release := make(chan struct{})
	slots := make(chan struct{}, 2)
	engine.StreamImageOutputsFunc = func(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
		out := make(chan ImageOutput)
		errCh := make(chan error, 1)
		go func() {
			defer close(out)
			defer close(errCh)
			mu.Lock()
			active++
			if active > maxActive {
				maxActive = active
			}
			mu.Unlock()
			<-release
			out <- ImageOutput{
				Kind:    "result",
				Model:   request.Model,
				Index:   index,
				Total:   total,
				Created: int64(index),
				Data:    []map[string]any{{"url": imageURLForIndex(index)}},
			}
			mu.Lock()
			active--
			mu.Unlock()
			errCh <- nil
		}()
		return out, errCh
	}

	outputs, errCh := engine.StreamImageOutputsWithPool(context.Background(), ConversationRequest{
		Model: "gpt-image-2",
		N:     3,
		AcquireImageOutputSlot: func(ctx context.Context, index int) (func(), error) {
			select {
			case slots <- struct{}{}:
				return func() { <-slots }, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	})

	time.Sleep(120 * time.Millisecond)
	mu.Lock()
	gotActive := maxActive
	mu.Unlock()
	if gotActive != 2 {
		t.Fatalf("max concurrent image workers = %d, want 2", gotActive)
	}

	close(release)
	for range outputs {
	}
	if err := <-errCh; err != nil {
		t.Fatalf("StreamImageOutputsWithPool() err = %v", err)
	}
}

func TestStreamImageOutputsWithPoolClassifiesGenericUnauthorizedWithoutPromptRetry(t *testing.T) {
	usedTokens := []string(nil)
	engine := &Engine{
		ImageTokenProvider: func(context.Context) (string, error) {
			token := fmt.Sprintf("token-%d", len(usedTokens)+1)
			usedTokens = append(usedTokens, token)
			return token, nil
		},
		ImageClientFactory: func(string) *backend.Client { return nil },
	}
	engine.StreamImageOutputsFunc = func(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
		out := make(chan ImageOutput)
		errCh := make(chan error, 1)
		close(out)
		errCh <- fmt.Errorf("auth_chat_requirements failed: status=401, body={\"detail\":\"challenge_required\"}")
		close(errCh)
		return out, errCh
	}

	outputs, errCh := engine.StreamImageOutputsWithPool(context.Background(), ConversationRequest{
		Model: "gpt-image-2",
		N:     1,
	})
	for range outputs {
	}
	err := <-errCh
	if err == nil {
		t.Fatal("StreamImageOutputsWithPool() err = nil, want upstream error")
	}
	if len(usedTokens) != 1 {
		t.Fatalf("used tokens = %#v, want no prompt retry for challenge error", usedTokens)
	}
	var imageErr *ImageGenerationError
	if !errors.As(err, &imageErr) {
		t.Fatalf("err = %T %v, want ImageGenerationError", err, err)
	}
	if imageErr.StatusCode != 403 || imageErr.Code != "upstream_challenge_required" {
		t.Fatalf("image error = %#v, want challenge status/code", imageErr)
	}
	if !strings.Contains(err.Error(), "风控验证页面") || !strings.Contains(err.Error(), "challenge_required") {
		t.Fatalf("error = %q, want Chinese challenge guidance with original detail", err.Error())
	}
}

func TestStreamImageOutputsWithPoolClassifiesCodexUnauthorizedPermissionWithoutPromptRetry(t *testing.T) {
	usedTokens := []string(nil)
	engine := &Engine{
		ImageTokenProvider: func(context.Context) (string, error) {
			token := fmt.Sprintf("token-%d", len(usedTokens)+1)
			usedTokens = append(usedTokens, token)
			return token, nil
		},
		ImageClientFactory: func(string) *backend.Client { return nil },
	}
	engine.StreamImageOutputsFunc = func(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
		out := make(chan ImageOutput)
		errCh := make(chan error, 1)
		close(out)
		errCh <- fmt.Errorf("/backend-api/codex/responses failed: status=401, body={\"detail\":\"Unauthorized\"}")
		close(errCh)
		return out, errCh
	}

	outputs, errCh := engine.StreamImageOutputsWithPool(context.Background(), ConversationRequest{
		Model: "codex-gpt-image-2",
		N:     1,
	})
	for range outputs {
	}
	err := <-errCh
	if err == nil {
		t.Fatal("StreamImageOutputsWithPool() err = nil, want permission error")
	}
	if len(usedTokens) != 1 {
		t.Fatalf("used tokens = %#v, want no prompt retry for permission error", usedTokens)
	}
	var imageErr *ImageGenerationError
	if !errors.As(err, &imageErr) {
		t.Fatalf("err = %T %v, want ImageGenerationError", err, err)
	}
	if imageErr.StatusCode != 403 || imageErr.Code != "account_not_authorized" {
		t.Fatalf("image error = %#v, want permission status/code", imageErr)
	}
	if !strings.Contains(err.Error(), "上游账号无权限或登录凭证失效") {
		t.Fatalf("error = %q, want Chinese permission guidance", err.Error())
	}
}

func TestCollectImageOutputsKeepsImageOrderByIndex(t *testing.T) {
	outputs := make(chan ImageOutput, 2)
	errCh := make(chan error, 1)
	outputs <- ImageOutput{
		Kind:    "result",
		Index:   2,
		Total:   2,
		Created: 2,
		Data:    []map[string]any{{"url": "https://example.test/second.png"}},
	}
	outputs <- ImageOutput{
		Kind:    "result",
		Index:   1,
		Total:   2,
		Created: 1,
		Data:    []map[string]any{{"url": "https://example.test/first.png"}},
	}
	close(outputs)
	errCh <- nil
	close(errCh)

	result, err := (&Engine{}).CollectImageOutputs(outputs, errCh)
	if err != nil {
		t.Fatalf("CollectImageOutputs() err = %v", err)
	}
	data := result["data"].([]map[string]any)
	if len(data) != 2 {
		t.Fatalf("data len = %d, want 2", len(data))
	}
	if data[0]["url"] != "https://example.test/first.png" || data[1]["url"] != "https://example.test/second.png" {
		t.Fatalf("data order = %#v, want first then second", data)
	}
}

func imageURLForIndex(index int) string {
	switch index {
	case 1:
		return "https://example.test/image-1.png"
	case 2:
		return "https://example.test/image-2.png"
	case 3:
		return "https://example.test/image-3.png"
	case 4:
		return "https://example.test/image-4.png"
	default:
		return "https://example.test/image.png"
	}
}
