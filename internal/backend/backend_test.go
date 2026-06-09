package backend

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func ptrInt(value int) *int {
	return &value
}

func imageEventBytesEqual(event ResponsesImageEvent, want []byte) bool {
	if len(event.ImageData) > 0 {
		return bytes.Equal(event.ImageData, want)
	}
	if event.Result == "" {
		return false
	}
	got, err := base64.StdEncoding.DecodeString(event.Result)
	return err == nil && bytes.Equal(got, want)
}

func newTestBackendClient(server *httptest.Server) *Client {
	client := &Client{
		BaseURL:     server.URL,
		AccessToken: "token-1",
		httpClient:  server.Client(),
		lookup: testAccountLookup{
			"token-1": {"chatgpt_account_id": "acct-1"},
		},
	}
	client.fp = client.buildFingerprint()
	client.applyBrowserFingerprint()
	client.userAgent = client.fp["user-agent"]
	client.deviceID = client.fp["oai-device-id"]
	client.sessionID = client.fp["oai-session-id"]
	return client
}

func setOfficialImageDownloadRetryDelayForTest(delay time.Duration) func() {
	previous := officialImageDownloadRetryDelay
	officialImageDownloadRetryDelay = delay
	return func() {
		officialImageDownloadRetryDelay = previous
	}
}

func TestUpstreamHTTPErrorSummarizesCloudflareChallenge(t *testing.T) {
	err := upstreamHTTPError("bootstrap", 403, []byte(`<html><body><script>window._cf_chl_opt={}</script>Enable JavaScript and cookies to continue</body></html>`))
	got := err.Error()
	if !strings.Contains(got, "bootstrap failed: status=403") {
		t.Fatalf("error missing context/status: %q", got)
	}
	if !strings.Contains(got, "upstream returned Cloudflare challenge page") {
		t.Fatalf("error missing challenge summary: %q", got)
	}
	if strings.Contains(got, "<html>") || strings.Contains(got, "window._cf_chl_opt") {
		t.Fatalf("error leaked challenge HTML: %q", got)
	}
}

func TestUpstreamHTTPErrorSummarizesGenericHTML(t *testing.T) {
	err := upstreamHTTPError("auth_models", 502, []byte(`<!doctype html><html><body>bad gateway</body></html>`))
	got := err.Error()
	if got != "auth_models failed: status=502, upstream returned HTML error page" {
		t.Fatalf("upstreamHTTPError() = %q", got)
	}
}

func TestUpstreamHTTPErrorKeepsPlainBodyDetail(t *testing.T) {
	err := upstreamHTTPError("auth_models", 400, []byte(`{"error":"bad request"}`))
	got := err.Error()
	if got != `auth_models failed: status=400, body={"error":"bad request"}` {
		t.Fatalf("upstreamHTTPError() = %q", got)
	}
}

func TestUpstreamTransportErrorSummarizesSurfHandshakeFailure(t *testing.T) {
	err := upstreamTransportError("bootstrap", errString(`Get "https://chatgpt.com/": surf: HTTP/2 request failed: uTLS.HandshakeContext() error: EOF; HTTP/1.1 fallback failed: uTLS.HandshakeContext() error: EOF`))
	got := err.Error()
	want := "bootstrap failed: upstream connection failed before TLS handshake completed; check proxy reachability to chatgpt.com or change proxy"
	if got != want {
		t.Fatalf("upstreamTransportError() = %q, want %q", got, want)
	}
}

func TestApplyBrowserFingerprintPreservesAccountProfile(t *testing.T) {
	client := &Client{fp: map[string]string{
		"user-agent":     "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36 Edg/143.0.0.0",
		"impersonate":    "edge101",
		"oai-device-id":  "device-1",
		"oai-session-id": "session-1",
	}}
	client.applyBrowserFingerprint()
	if client.fp["user-agent"] != "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36 Edg/143.0.0.0" {
		t.Fatalf("user-agent = %q", client.fp["user-agent"])
	}
	if client.fp["sec-ch-ua"] != `"Microsoft Edge";v="143", "Chromium";v="143", "Not A(Brand";v="24"` {
		t.Fatalf("sec-ch-ua = %q", client.fp["sec-ch-ua"])
	}
	if client.fp["sec-ch-ua-full-version"] != `"143.0.0.0"` {
		t.Fatalf("sec-ch-ua-full-version = %q", client.fp["sec-ch-ua-full-version"])
	}
	if client.fp["impersonate"] != "edge101" {
		t.Fatalf("impersonate = %q", client.fp["impersonate"])
	}
	if client.fp["oai-device-id"] != "device-1" || client.fp["oai-session-id"] != "session-1" {
		t.Fatalf("device/session should be preserved: %#v", client.fp)
	}
}

func TestOfficialImageHeadersIncludeSentinelAndConduitTokens(t *testing.T) {
	client := &Client{
		BaseURL:     "https://chatgpt.com",
		AccessToken: "token-1",
		userAgent:   browserUserAgent,
		deviceID:    "device-1",
		sessionID:   "session-1",
		fp: map[string]string{
			"user-agent":                  browserUserAgent,
			"sec-ch-ua":                   browserSecCHUA,
			"sec-ch-ua-arch":              browserSecCHUAArch,
			"sec-ch-ua-bitness":           browserSecCHUABitness,
			"sec-ch-ua-full-version":      browserSecCHUAFullVersion,
			"sec-ch-ua-full-version-list": browserSecCHUAFullVersionList,
			"sec-ch-ua-mobile":            browserSecCHUAMobile,
			"sec-ch-ua-platform":          browserSecCHUAPlatform,
			"sec-ch-ua-platform-version":  browserSecCHUAPlatformVersion,
		},
	}
	headers := client.officialHeaders(officialStreamPath, ChatRequirements{
		Token:          "req-token",
		ProofToken:     "proof-token",
		TurnstileToken: "turn-token",
		SOToken:        "so-token",
	}, "conduit-token", "text/event-stream")
	for key, want := range map[string]string{
		"Authorization": "Bearer token-1",
		"OpenAI-Sentinel-Chat-Requirements-Token": "req-token",
		"OpenAI-Sentinel-Proof-Token":             "proof-token",
		"OpenAI-Sentinel-Turnstile-Token":         "turn-token",
		"OpenAI-Sentinel-SO-Token":                "so-token",
		"X-Conduit-Token":                         "conduit-token",
		"Accept":                                  "text/event-stream",
		"Content-Type":                            "application/json",
		"X-OpenAI-Target-Path":                    officialStreamPath,
	} {
		if got := headers[key]; got != want {
			t.Fatalf("headers[%s] = %q, want %q", key, got, want)
		}
	}
	if headers["X-Oai-Turn-Trace-Id"] == "" {
		t.Fatalf("X-Oai-Turn-Trace-Id missing in %#v", headers)
	}
}

func TestBuildOfficialImagePromptOnlyAddsSizeHint(t *testing.T) {
	prompt := buildOfficialImagePrompt("画一张城市封面", "16:9")
	if !strings.Contains(prompt, "16:9 横屏构图") {
		t.Fatalf("buildOfficialImagePrompt() missing size hint: %s", prompt)
	}
	for _, unwanted := range []string{"High 档", "透明背景", "anime", "阶段性预览", "WebP"} {
		if strings.Contains(prompt, unwanted) {
			t.Fatalf("buildOfficialImagePrompt() unexpectedly contains %q: %s", unwanted, prompt)
		}
	}
}

func TestOfficialImageModelSlug(t *testing.T) {
	for _, tt := range []struct {
		model string
		want  string
	}{
		{model: "", want: "auto"},
		{model: "auto", want: "auto"},
		{model: "gpt-image-2", want: "gpt-5-5"},
		{model: "codex-gpt-image-2", want: "codex-gpt-image-2"},
		{model: "gpt-5.5", want: "auto"},
	} {
		if got := officialImageModelSlug(tt.model); got != tt.want {
			t.Fatalf("officialImageModelSlug(%q) = %q, want %q", tt.model, got, tt.want)
		}
	}
}

func TestStreamResponsesImageUsesOfficialPrepareAndConversationRoutes(t *testing.T) {
	const png1x1 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+X2ioAAAAASUVORK5CYII="
	imageBytes, err := base64.StdEncoding.DecodeString(png1x1)
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	var seenPaths []string
	var prepareBody map[string]any
	var streamBody map[string]any
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPaths = append(seenPaths, r.URL.Path)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<html data-build="build-1"><script src="/backend-api/sentinel/sdk.js"></script></html>`))
		case r.Method == http.MethodPost && r.URL.Path == "/backend-api/sentinel/chat-requirements":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":"req-token","proofofwork":{"required":false},"turnstile":{"required":false},"arkose":{"required":false}}`))
		case r.Method == http.MethodPost && r.URL.Path == officialPreparePath:
			if err := json.NewDecoder(r.Body).Decode(&prepareBody); err != nil {
				t.Fatalf("decode prepare body: %v", err)
			}
			if got := r.Header.Get("OpenAI-Sentinel-Chat-Requirements-Token"); got != "req-token" {
				t.Fatalf("prepare requirements token = %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"conduit_token":"conduit-token"}`))
		case r.Method == http.MethodPost && r.URL.Path == officialStreamPath:
			if err := json.NewDecoder(r.Body).Decode(&streamBody); err != nil {
				t.Fatalf("decode stream body: %v", err)
			}
			if got := r.Header.Get("X-Conduit-Token"); got != "conduit-token" {
				t.Fatalf("stream conduit token = %q", got)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"type\":\"server_ste_metadata\",\"metadata\":{\"tool_invoked\":true,\"turn_use_case\":\"image gen\"},\"conversation_id\":\"conv-1\"}\n\n"))
			_, _ = w.Write([]byte("data: {\"type\":\"message_stream_complete\",\"conversation_id\":\"conv-1\"}\n\n"))
		case r.Method == http.MethodGet && r.URL.Path == "/backend-api/conversation/conv-1":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"mapping":{"node-1":{"message":{"author":{"role":"tool"},"metadata":{},"content":{"content_type":"multimodal_text","parts":[{"content_type":"image_asset_pointer","asset_pointer":"file-service://file_abc"}]}}}}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/backend-api/files/download/file_abc":
			if got := r.URL.Query().Get("conversation_id"); got != "conv-1" {
				t.Fatalf("conversation_id = %q", got)
			}
			if got := r.URL.Query().Get("inline"); got != "false" {
				t.Fatalf("inline = %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"download_url":"` + server.URL + `/download/file_abc.png"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/download/file_abc.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(imageBytes)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := &Client{
		BaseURL:     server.URL,
		AccessToken: "token-1",
		httpClient:  server.Client(),
		lookup: testAccountLookup{
			"token-1": {"chatgpt_account_id": "acct-1"},
		},
	}
	client.fp = client.buildFingerprint()
	client.applyBrowserFingerprint()
	client.userAgent = client.fp["user-agent"]
	client.deviceID = client.fp["oai-device-id"]
	client.sessionID = client.fp["oai-session-id"]

	events, errCh := client.StreamResponsesImage(context.Background(), ResponsesImageRequest{
		Prompt:        "生成封面",
		Model:         "gpt-image-2",
		Size:          "16:9",
		Quality:       "high",
		Background:    "transparent",
		Style:         "anime",
		Moderation:    "low",
		PartialImages: ptrInt(2),
		OutputFormat:  "webp",
	})
	var results []ResponsesImageEvent
	for event := range events {
		if event.HasImageResult() {
			results = append(results, event)
		}
	}
	if err := <-errCh; err != nil {
		t.Fatalf("StreamResponsesImage() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("result events = %#v", results)
	}
	if results[0].Result != "" {
		t.Fatalf("official image result used base64 field, want raw image bytes")
	}
	if results[0].ConversationID != "conv-1" {
		t.Fatalf("conversation id = %q, want conv-1", results[0].ConversationID)
	}
	if !imageEventBytesEqual(results[0], imageBytes) {
		t.Fatalf("result bytes = %#v, want PNG bytes", results[0])
	}
	if prepareBody["model"] != "gpt-5-5" {
		t.Fatalf("prepare model = %#v, want gpt-5-5", prepareBody["model"])
	}
	if streamBody["model"] != "gpt-5-5" {
		t.Fatalf("stream model = %#v, want gpt-5-5", streamBody["model"])
	}
	messages := streamBody["messages"].([]any)
	message := messages[0].(map[string]any)
	content := message["content"].(map[string]any)
	if content["content_type"] != "text" {
		t.Fatalf("content_type = %#v, want text", content["content_type"])
	}
	parts := content["parts"].([]any)
	if len(parts) != 1 || !strings.Contains(parts[0].(string), "16:9 横屏构图") {
		t.Fatalf("parts = %#v, want prompt with size hint", parts)
	}
	for _, unwanted := range []string{"High 档", "透明背景", "anime", "阶段性预览", "WebP"} {
		if strings.Contains(parts[0].(string), unwanted) {
			t.Fatalf("prompt unexpectedly contains %q: %s", unwanted, parts[0].(string))
		}
	}
	if len(seenPaths) < 6 {
		t.Fatalf("seen paths too short: %#v", seenPaths)
	}
}

func TestStreamResponsesImageUsesDirectSSEImageAssetPointer(t *testing.T) {
	const png1x1 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+X2ioAAAAASUVORK5CYII="
	imageBytes, err := base64.StdEncoding.DecodeString(png1x1)
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	var server *httptest.Server
	pollCount := 0
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<html data-build="build-1"><script src="/backend-api/sentinel/sdk.js"></script></html>`))
		case r.Method == http.MethodPost && r.URL.Path == "/backend-api/sentinel/chat-requirements":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":"req-token","proofofwork":{"required":false},"turnstile":{"required":false},"arkose":{"required":false}}`))
		case r.Method == http.MethodPost && r.URL.Path == officialPreparePath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"conduit_token":"conduit-token"}`))
		case r.Method == http.MethodPost && r.URL.Path == officialStreamPath:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"p\":\"\",\"o\":\"add\",\"v\":{\"message\":{\"author\":{\"role\":\"tool\",\"metadata\":{}},\"content\":{\"content_type\":\"multimodal_text\",\"parts\":[{\"content_type\":\"image_asset_pointer\",\"asset_pointer\":\"sediment://file_direct\"}]}},\"conversation_id\":\"conv-direct\"}}\n\n"))
			_, _ = w.Write([]byte("data: {\"type\":\"message_stream_complete\",\"conversation_id\":\"conv-direct\"}\n\n"))
		case r.Method == http.MethodGet && r.URL.Path == "/backend-api/conversation/conv-direct":
			pollCount++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"mapping":{}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/backend-api/files/download/file_direct":
			if got := r.URL.Query().Get("conversation_id"); got != "conv-direct" {
				t.Fatalf("conversation_id = %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"download_url":"` + server.URL + `/download/file_direct.png"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/download/file_direct.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(imageBytes)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := newTestBackendClient(server)
	events, errCh := client.StreamResponsesImage(context.Background(), ResponsesImageRequest{
		Prompt: "生成封面",
		Model:  "gpt-image-2",
	})
	var results []ResponsesImageEvent
	for event := range events {
		if event.HasImageResult() {
			results = append(results, event)
		}
	}
	if err := <-errCh; err != nil {
		t.Fatalf("StreamResponsesImage() error = %v", err)
	}
	if pollCount != 0 {
		t.Fatalf("conversation poll count = %d, want direct SSE asset to avoid polling", pollCount)
	}
	if len(results) != 1 || !imageEventBytesEqual(results[0], imageBytes) {
		t.Fatalf("results = %#v, want one direct image result", results)
	}
}

func TestStreamResponsesImageIgnoresFalseToolInvokedForImageGenResult(t *testing.T) {
	const png1x1 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+X2ioAAAAASUVORK5CYII="
	imageBytes, err := base64.StdEncoding.DecodeString(png1x1)
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<html data-build="build-1"><script src="/backend-api/sentinel/sdk.js"></script></html>`))
		case r.Method == http.MethodPost && r.URL.Path == "/backend-api/sentinel/chat-requirements":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":"req-token","proofofwork":{"required":false},"turnstile":{"required":false},"arkose":{"required":false}}`))
		case r.Method == http.MethodPost && r.URL.Path == officialPreparePath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"conduit_token":"conduit-token"}`))
		case r.Method == http.MethodPost && r.URL.Path == officialStreamPath:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"p\":\"\",\"o\":\"add\",\"v\":{\"message\":{\"author\":{\"role\":\"tool\",\"metadata\":{}},\"content\":{\"content_type\":\"multimodal_text\",\"parts\":[{\"content_type\":\"image_asset_pointer\",\"asset_pointer\":\"sediment://file_image\"}]}},\"conversation_id\":\"conv-image\"}}\n\n"))
			_, _ = w.Write([]byte("data: {\"v\":{\"message\":{\"author\":{\"role\":\"assistant\"},\"content\":{\"content_type\":\"text\",\"parts\":[\"Here is the generated image.\"]}}},\"conversation_id\":\"conv-image\"}\n\n"))
			_, _ = w.Write([]byte("data: {\"type\":\"server_ste_metadata\",\"metadata\":{\"tool_invoked\":false,\"turn_use_case\":\"image gen\"},\"conversation_id\":\"conv-image\"}\n\n"))
			_, _ = w.Write([]byte("data: {\"type\":\"message_stream_complete\",\"conversation_id\":\"conv-image\"}\n\n"))
		case r.Method == http.MethodGet && r.URL.Path == "/backend-api/files/download/file_image":
			if got := r.URL.Query().Get("conversation_id"); got != "conv-image" {
				t.Fatalf("conversation_id = %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"download_url":"` + server.URL + `/download/file_image.png"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/download/file_image.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(imageBytes)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := newTestBackendClient(server)
	events, errCh := client.StreamResponsesImage(context.Background(), ResponsesImageRequest{
		Prompt: "生成封面",
		Model:  "gpt-image-2",
	})
	var results []ResponsesImageEvent
	var textResponses []ResponsesImageEvent
	for event := range events {
		if event.HasImageResult() {
			results = append(results, event)
		}
		if event.Type == "image_text_response" {
			textResponses = append(textResponses, event)
		}
	}
	if err := <-errCh; err != nil {
		t.Fatalf("StreamResponsesImage() error = %v", err)
	}
	if len(textResponses) != 0 {
		t.Fatalf("text responses = %#v, want none for successful image gen", textResponses)
	}
	if len(results) != 1 || !imageEventBytesEqual(results[0], imageBytes) {
		t.Fatalf("results = %#v, want one image result", results)
	}
}

func TestOfficialImageEditNoResultWaitsForCallerContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/backend-api/conversation/conv-empty" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"mapping":{}}`))
			return
		}
		t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	client := newTestBackendClient(server)
	out := make(chan ResponsesImageEvent, 8)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := iterOfficialImageSSE(
		ctx,
		client,
		strings.NewReader("data: {\"type\":\"resume_conversation_token\",\"conversation_id\":\"conv-empty\"}\n\ndata: {\"type\":\"message_stream_complete\",\"conversation_id\":\"conv-empty\"}\n\n"),
		ResponsesImageRequest{
			Prompt:      "修改参考图",
			Model:       "gpt-image-2",
			InputImages: []ResponsesInputImage{{Data: []byte("image"), ContentType: "image/png"}},
		},
		out,
	)
	close(out)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("iterOfficialImageSSE() error = %v, want context deadline", err)
	}
}

func TestOfficialImageFinalTextBypassesImageResultPolling(t *testing.T) {
	const upstreamText = "上游返回的任何非排队文本都应该原样返回。"
	client := &Client{}
	out := make(chan ResponsesImageEvent, 8)
	err := iterOfficialImageSSE(
		context.Background(),
		client,
		strings.NewReader(
			"data: {\"v\":{\"message\":{\"author\":{\"role\":\"assistant\"},\"content\":{\"content_type\":\"text\",\"parts\":[\""+upstreamText+"\"]}}},\"conversation_id\":\"conv-text\"}\n\n"+
				"data: {\"type\":\"server_ste_metadata\",\"metadata\":{\"turn_use_case\":\"image gen\"},\"conversation_id\":\"conv-text\"}\n\n"+
				"data: {\"type\":\"message_stream_complete\",\"conversation_id\":\"conv-text\"}\n\n",
		),
		ResponsesImageRequest{Prompt: "修改参考图", Model: "gpt-image-2"},
		out,
	)
	close(out)

	if err != nil {
		t.Fatalf("iterOfficialImageSSE() error = %v", err)
	}
	var got ResponsesImageEvent
	for event := range out {
		if event.Type == "image_text_response" {
			got = event
		}
	}
	if got.Text != upstreamText {
		t.Fatalf("image_text_response = %#v, want upstream text", got)
	}
}

func TestStreamResponsesImageDoesNotTreatQueuedAssistantNoticeAsFinalText(t *testing.T) {
	const png1x1 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+X2ioAAAAASUVORK5CYII="
	imageBytes, err := base64.StdEncoding.DecodeString(png1x1)
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	var server *httptest.Server
	pollCount := 0
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<html data-build="build-1"><script src="/backend-api/sentinel/sdk.js"></script></html>`))
		case r.Method == http.MethodPost && r.URL.Path == "/backend-api/sentinel/chat-requirements":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":"req-token","proofofwork":{"required":false},"turnstile":{"required":false},"arkose":{"required":false}}`))
		case r.Method == http.MethodPost && r.URL.Path == officialPreparePath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"conduit_token":"conduit-token"}`))
		case r.Method == http.MethodPost && r.URL.Path == officialStreamPath:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"type\":\"title_generation\",\"title\":\"正在处理图片\",\"conversation_id\":\"conv-queued\"}\n\n"))
			_, _ = w.Write([]byte("data: {\"v\":{\"message\":{\"author\":{\"role\":\"assistant\"},\"content\":{\"content_type\":\"text\",\"parts\":[\"正在处理图片 目前有很多人在创建图片，因此可能需要一点时间。图片准备好后我们会通知你。\"]}}},\"conversation_id\":\"conv-queued\"}\n\n"))
			_, _ = w.Write([]byte("data: {\"type\":\"message_stream_complete\",\"conversation_id\":\"conv-queued\"}\n\n"))
		case r.Method == http.MethodGet && r.URL.Path == "/backend-api/conversation/conv-queued":
			pollCount++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"mapping":{"node-1":{"message":{"author":{"role":"tool"},"metadata":{},"content":{"content_type":"multimodal_text","parts":[{"content_type":"image_asset_pointer","asset_pointer":"file-service://file_ready"}]}}}}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/backend-api/files/download/file_ready":
			if got := r.URL.Query().Get("conversation_id"); got != "conv-queued" {
				t.Fatalf("conversation_id = %q", got)
			}
			if got := r.URL.Query().Get("inline"); got != "false" {
				t.Fatalf("inline = %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"download_url":"` + server.URL + `/download/file_ready.png"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/download/file_ready.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(imageBytes)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := &Client{
		BaseURL:     server.URL,
		AccessToken: "token-1",
		httpClient:  server.Client(),
		lookup: testAccountLookup{
			"token-1": {"chatgpt_account_id": "acct-1"},
		},
	}
	client.fp = client.buildFingerprint()
	client.applyBrowserFingerprint()
	client.userAgent = client.fp["user-agent"]
	client.deviceID = client.fp["oai-device-id"]
	client.sessionID = client.fp["oai-session-id"]

	events, errCh := client.StreamResponsesImage(context.Background(), ResponsesImageRequest{
		Prompt: "生成封面",
		Model:  "gpt-image-2",
	})
	var results []ResponsesImageEvent
	var texts []string
	for event := range events {
		if strings.TrimSpace(event.Text) != "" {
			texts = append(texts, event.Text)
		}
		if event.HasImageResult() {
			results = append(results, event)
		}
	}
	if err := <-errCh; err != nil {
		t.Fatalf("StreamResponsesImage() error = %v", err)
	}
	if pollCount == 0 {
		t.Fatal("expected conversation polling after queued assistant notice")
	}
	if len(results) != 1 || !imageEventBytesEqual(results[0], imageBytes) {
		t.Fatalf("results = %#v, want one final image result", results)
	}
}

func TestStreamResponsesImageRetriesConversationPollRateLimit(t *testing.T) {
	const png1x1 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+X2ioAAAAASUVORK5CYII="
	imageBytes, err := base64.StdEncoding.DecodeString(png1x1)
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	var server *httptest.Server
	pollCount := 0
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<html data-build="build-1"><script src="/backend-api/sentinel/sdk.js"></script></html>`))
		case r.Method == http.MethodPost && r.URL.Path == "/backend-api/sentinel/chat-requirements":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":"req-token","proofofwork":{"required":false},"turnstile":{"required":false},"arkose":{"required":false}}`))
		case r.Method == http.MethodPost && r.URL.Path == officialPreparePath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"conduit_token":"conduit-token"}`))
		case r.Method == http.MethodPost && r.URL.Path == officialStreamPath:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"type\":\"resume_conversation_token\",\"conversation_id\":\"conv-rate-limited\"}\n\n"))
			_, _ = w.Write([]byte("data: {\"type\":\"message_stream_complete\",\"conversation_id\":\"conv-rate-limited\"}\n\n"))
		case r.Method == http.MethodGet && r.URL.Path == "/backend-api/conversation/conv-rate-limited":
			pollCount++
			w.Header().Set("Content-Type", "application/json")
			if pollCount == 1 {
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"detail":"Too many requests"}`))
				return
			}
			_, _ = w.Write([]byte(`{"mapping":{"node-1":{"message":{"author":{"role":"tool"},"metadata":{"async_task_type":"image_gen"},"content":{"content_type":"multimodal_text","parts":[{"content_type":"image_asset_pointer","asset_pointer":"file-service://file_ready"}]}}}}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/backend-api/files/download/file_ready":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"download_url":"` + server.URL + `/download/file_ready.png"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/download/file_ready.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(imageBytes)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := newTestBackendClient(server)
	events, errCh := client.StreamResponsesImage(context.Background(), ResponsesImageRequest{
		Prompt: "生成封面",
		Model:  "gpt-image-2",
	})
	var results []ResponsesImageEvent
	for event := range events {
		if event.HasImageResult() {
			results = append(results, event)
		}
	}
	if err := <-errCh; err != nil {
		t.Fatalf("StreamResponsesImage() error = %v", err)
	}
	if pollCount != 2 {
		t.Fatalf("conversation poll count = %d, want retry after rate limit", pollCount)
	}
	if len(results) != 1 || !imageEventBytesEqual(results[0], imageBytes) {
		t.Fatalf("results = %#v, want one final image result", results)
	}
}

func TestStreamResponsesImageReturnsPolledConversationText(t *testing.T) {
	const finalText = "非常抱歉，生成的图片可能违反了关于裸露、色情或情色内容的防护限制。如果你认为此判断有误，请重试或修改提示语。"
	pollCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<html data-build="build-1"><script src="/backend-api/sentinel/sdk.js"></script></html>`))
		case r.Method == http.MethodPost && r.URL.Path == "/backend-api/sentinel/chat-requirements":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":"req-token","proofofwork":{"required":false},"turnstile":{"required":false},"arkose":{"required":false}}`))
		case r.Method == http.MethodPost && r.URL.Path == officialPreparePath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"conduit_token":"conduit-token"}`))
		case r.Method == http.MethodPost && r.URL.Path == officialStreamPath:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"type\":\"title_generation\",\"title\":\"正在处理图片\",\"conversation_id\":\"conv-refused\"}\n\n"))
			_, _ = w.Write([]byte("data: {\"type\":\"message_stream_complete\",\"conversation_id\":\"conv-refused\"}\n\n"))
		case r.Method == http.MethodGet && r.URL.Path == "/backend-api/conversation/conv-refused":
			pollCount++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"mapping":{
				"assistant-text":{"message":{"author":{"role":"assistant"},"create_time":3,"content":{"content_type":"text","parts":["` + finalText + `"]},"status":"finished_successfully","recipient":"all","metadata":{"model_slug":"gpt-5-5"}}}
			}}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := newTestBackendClient(server)
	events, errCh := client.StreamResponsesImage(context.Background(), ResponsesImageRequest{
		Prompt: "修改参考图",
		Model:  "gpt-image-2",
	})
	var textEvents []ResponsesImageEvent
	var results []ResponsesImageEvent
	for event := range events {
		if event.Type == "image_text_response" {
			textEvents = append(textEvents, event)
		}
		if event.HasImageResult() {
			results = append(results, event)
		}
	}
	if err := <-errCh; err != nil {
		t.Fatalf("StreamResponsesImage() error = %v", err)
	}
	if pollCount != 1 {
		t.Fatalf("conversation poll count = %d, want one poll", pollCount)
	}
	if len(results) != 0 {
		t.Fatalf("results = %#v, want no image results", results)
	}
	if len(textEvents) != 1 || textEvents[0].Text != finalText {
		t.Fatalf("text events = %#v, want upstream conversation text", textEvents)
	}
}

func TestStreamResponsesImageEmitsFinalTextWhenNoImageResult(t *testing.T) {
	const finalText = "你好！我是 ChatGPT。"
	var server *httptest.Server
	pollCount := 0
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<html data-build="build-1"><script src="/backend-api/sentinel/sdk.js"></script></html>`))
		case r.Method == http.MethodPost && r.URL.Path == "/backend-api/sentinel/chat-requirements":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":"req-token","proofofwork":{"required":false},"turnstile":{"required":false},"arkose":{"required":false}}`))
		case r.Method == http.MethodPost && r.URL.Path == officialPreparePath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"conduit_token":"conduit-token"}`))
		case r.Method == http.MethodPost && r.URL.Path == officialStreamPath:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"v\":{\"message\":{\"author\":{\"role\":\"assistant\"},\"content\":{\"content_type\":\"text\",\"parts\":[\"" + finalText + "\"]}}},\"conversation_id\":\"conv-text\"}\n\n"))
			_, _ = w.Write([]byte("data: {\"type\":\"message_stream_complete\",\"conversation_id\":\"conv-text\"}\n\n"))
		case r.Method == http.MethodGet && r.URL.Path == "/backend-api/conversation/conv-text":
			pollCount++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"mapping":{}}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := newTestBackendClient(server)
	events, errCh := client.StreamResponsesImage(context.Background(), ResponsesImageRequest{
		Prompt: "你好，你是什么模型？",
		Model:  "gpt-image-2",
	})
	var texts []string
	var results []ResponsesImageEvent
	for event := range events {
		if strings.TrimSpace(event.Text) != "" {
			texts = append(texts, event.Text)
		}
		if event.HasImageResult() {
			results = append(results, event)
		}
	}
	if err := <-errCh; err != nil {
		t.Fatalf("StreamResponsesImage() error = %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("results = %#v, want no image results", results)
	}
	if len(texts) == 0 || texts[len(texts)-1] != finalText {
		t.Fatalf("texts = %#v, want final text %q", texts, finalText)
	}
	if pollCount != 0 {
		t.Fatalf("conversation poll count = %d, want no polling for final text", pollCount)
	}
}

func TestStreamResponsesImageFetchesHistoryTextForTextTurn(t *testing.T) {
	const finalText = "你好！我是 GPT-5 mini。"
	historyCount := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`<html data-build="build-1"><script src="/backend-api/sentinel/sdk.js"></script></html>`))
		case r.Method == http.MethodPost && r.URL.Path == "/backend-api/sentinel/chat-requirements":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":"req-token","proofofwork":{"required":false},"turnstile":{"required":false},"arkose":{"required":false}}`))
		case r.Method == http.MethodPost && r.URL.Path == officialPreparePath:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"conduit_token":"conduit-token"}`))
		case r.Method == http.MethodPost && r.URL.Path == officialStreamPath:
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"type\":\"resume_conversation_token\",\"conversation_id\":\"conv-history\"}\n\n"))
			_, _ = w.Write([]byte("data: {\"type\":\"server_ste_metadata\",\"conversation_id\":\"conv-history\",\"metadata\":{\"tool_invoked\":false,\"turn_use_case\":\"text\"}}\n\n"))
			_, _ = w.Write([]byte("data: {\"type\":\"message_stream_complete\",\"conversation_id\":\"conv-history\"}\n\n"))
		case r.Method == http.MethodGet && r.URL.Path == "/backend-api/conversation/conv-history":
			historyCount++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"mapping":{
				"user-node":{"message":{"author":{"role":"user"},"create_time":1,"content":{"content_type":"text","parts":["你好，你是什么模型？"]},"status":"finished_successfully","recipient":"all","metadata":{}}},
				"assistant-context":{"message":{"author":{"role":"assistant"},"create_time":2,"content":{"content_type":"model_editable_context"},"status":"finished_successfully","recipient":"all","metadata":{"is_visually_hidden_from_conversation":true}}},
				"assistant-text":{"message":{"author":{"role":"assistant"},"create_time":3,"content":{"content_type":"text","parts":["` + finalText + `"]},"status":"finished_successfully","recipient":"all","metadata":{"model_slug":"gpt-5-5"}}}
			}}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := newTestBackendClient(server)
	events, errCh := client.StreamResponsesImage(context.Background(), ResponsesImageRequest{
		Prompt: "你好，你是什么模型？",
		Model:  "gpt-image-2",
	})
	var textEvents []ResponsesImageEvent
	for event := range events {
		if event.Type == "image_text_response" {
			textEvents = append(textEvents, event)
		}
	}
	if err := <-errCh; err != nil {
		t.Fatalf("StreamResponsesImage() error = %v", err)
	}
	if len(textEvents) != 1 {
		t.Fatalf("text events = %#v, want one text response", textEvents)
	}
	if textEvents[0].Text != finalText {
		t.Fatalf("text response = %q, want %q", textEvents[0].Text, finalText)
	}
	if historyCount != 1 {
		t.Fatalf("history count = %d, want 1", historyCount)
	}
}

func TestResolveOfficialImageResultsRetriesTransientDownloadURL404(t *testing.T) {
	const png1x1 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+X2ioAAAAASUVORK5CYII="
	imageBytes, err := base64.StdEncoding.DecodeString(png1x1)
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}
	resetOfficialImageRetryDelay := setOfficialImageDownloadRetryDelayForTest(0)
	defer resetOfficialImageRetryDelay()

	downloadAttempts := 0
	urlAttempts := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/backend-api/files/download/file_img":
			urlAttempts++
			if got := r.URL.Query().Get("conversation_id"); got != "conv-1" {
				t.Fatalf("conversation_id = %q", got)
			}
			if got := r.URL.Query().Get("inline"); got != "false" {
				t.Fatalf("inline = %q", got)
			}
			if got := r.Header.Get("X-OpenAI-Target-Path"); got != "/backend-api/files/download/file_img" {
				t.Fatalf("target path = %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"download_url":"` + server.URL + `/download/image.png"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/download/image.png":
			downloadAttempts++
			if downloadAttempts < officialImageDownloadAttempts {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"detail":"File link not found."}`))
				return
			}
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(imageBytes)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := newTestBackendClient(server)
	results, err := client.resolveOfficialImageResults(context.Background(), ResponsesImageRequest{
		Prompt: "生成封面",
	}, ResponsesImageEvent{
		ConversationID: "conv-1",
		SedimentIDs:    []string{"file_img"},
	})
	if err != nil {
		t.Fatalf("resolveOfficialImageResults() error = %v", err)
	}
	if urlAttempts != officialImageDownloadAttempts {
		t.Fatalf("download URL attempts = %d, want %d", urlAttempts, officialImageDownloadAttempts)
	}
	if downloadAttempts != officialImageDownloadAttempts {
		t.Fatalf("download attempts = %d, want %d", downloadAttempts, officialImageDownloadAttempts)
	}
	if len(results) != 1 || !imageEventBytesEqual(results[0], imageBytes) {
		t.Fatalf("results = %#v, want one final image result", results)
	}
}

func TestResolveOfficialImageResultsUsesConversationScopedFileDownloadForSedimentID(t *testing.T) {
	const png1x1 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+X2ioAAAAASUVORK5CYII="
	imageBytes, err := base64.StdEncoding.DecodeString(png1x1)
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}

	fileURLAttempts := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/backend-api/files/download/file_img":
			fileURLAttempts++
			if got := r.URL.Query().Get("conversation_id"); got != "conv-1" {
				t.Fatalf("conversation_id = %q", got)
			}
			if got := r.URL.Query().Get("inline"); got != "false" {
				t.Fatalf("inline = %q", got)
			}
			if got := r.Header.Get("X-OpenAI-Target-Path"); got != "/backend-api/files/download/file_img" {
				t.Fatalf("target path = %q", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"download_url":"` + server.URL + `/download/file.png"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/download/file.png":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(imageBytes)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := newTestBackendClient(server)
	results, err := client.resolveOfficialImageResults(context.Background(), ResponsesImageRequest{
		Prompt: "生成封面",
	}, ResponsesImageEvent{
		ConversationID: "conv-1",
		SedimentIDs:    []string{"file_img"},
	})
	if err != nil {
		t.Fatalf("resolveOfficialImageResults() error = %v", err)
	}
	if fileURLAttempts != 1 {
		t.Fatalf("file URL attempts = %d, want 1", fileURLAttempts)
	}
	if len(results) != 1 || !imageEventBytesEqual(results[0], imageBytes) {
		t.Fatalf("results = %#v, want one final image result", results)
	}
}

func TestResolveOfficialImageResultsAuthenticatesBackendDownloadURL(t *testing.T) {
	const png1x1 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+X2ioAAAAASUVORK5CYII="
	imageBytes, err := base64.StdEncoding.DecodeString(png1x1)
	if err != nil {
		t.Fatalf("decode png: %v", err)
	}

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/backend-api/files/download/file_img":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"download_url":"` + server.URL + `/backend-api/estuary/content?id=file_img&sig=test"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/backend-api/estuary/content":
			if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
				t.Fatalf("Authorization = %q", got)
			}
			if got := r.Header.Get("X-OpenAI-Target-Path"); got != "/backend-api/estuary/content" {
				t.Fatalf("target path = %q", got)
			}
			if got := r.Header.Get("Accept"); got != "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8" {
				t.Fatalf("Accept = %q", got)
			}
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(imageBytes)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := newTestBackendClient(server)
	results, err := client.resolveOfficialImageResults(context.Background(), ResponsesImageRequest{
		Prompt: "生成封面",
	}, ResponsesImageEvent{
		ConversationID: "conv-1",
		FileIDs:        []string{"file_img"},
	})
	if err != nil {
		t.Fatalf("resolveOfficialImageResults() error = %v", err)
	}
	if len(results) != 1 || !imageEventBytesEqual(results[0], imageBytes) {
		t.Fatalf("results = %#v, want one final image result", results)
	}
}

func TestBuildResponsesImagePayloadSendsCompressionOnlyForJPEG(t *testing.T) {
	compression := 37
	jpegPayload, err := buildResponsesImagePayload(ResponsesImageRequest{
		Prompt:            "生成封面",
		Model:             "codex-gpt-image-2",
		OutputFormat:      "jpeg",
		OutputCompression: &compression,
	})
	if err != nil {
		t.Fatalf("buildResponsesImagePayload(jpeg) error = %v", err)
	}
	var jpegBody map[string]any
	if err := json.Unmarshal(jpegPayload, &jpegBody); err != nil {
		t.Fatalf("Unmarshal(jpeg) error = %v", err)
	}
	jpegTools := jpegBody["tools"].([]any)
	jpegTool := jpegTools[0].(map[string]any)
	if jpegTool["output_compression"] != float64(37) {
		t.Fatalf("jpeg output_compression = %#v, want 37", jpegTool["output_compression"])
	}

	webpPayload, err := buildResponsesImagePayload(ResponsesImageRequest{
		Prompt:            "生成封面",
		Model:             "codex-gpt-image-2",
		OutputFormat:      "webp",
		OutputCompression: &compression,
	})
	if err != nil {
		t.Fatalf("buildResponsesImagePayload(webp) error = %v", err)
	}
	var webpBody map[string]any
	if err := json.Unmarshal(webpPayload, &webpBody); err != nil {
		t.Fatalf("Unmarshal(webp) error = %v", err)
	}
	webpTools := webpBody["tools"].([]any)
	webpTool := webpTools[0].(map[string]any)
	if _, ok := webpTool["output_compression"]; ok {
		t.Fatalf("webp tool should not include output_compression: %#v", webpTool)
	}
}

func TestShouldTreatOfficialImageEventAsFinalText(t *testing.T) {
	toolFalse := false
	toolTrue := true
	tests := []struct {
		name  string
		event ResponsesImageEvent
		want  bool
	}{
		{name: "blocked text", event: ResponsesImageEvent{Text: "blocked", Blocked: true}, want: true},
		{name: "explicit no tool", event: ResponsesImageEvent{Text: "denied", ToolInvoked: &toolFalse, TurnUseCase: "multimodal"}, want: true},
		{name: "text use case", event: ResponsesImageEvent{Text: "plain text", ToolInvoked: &toolTrue, TurnUseCase: "text"}, want: true},
		{name: "queued notice still pending", event: ResponsesImageEvent{Text: "正在处理图片", ToolInvoked: nil, TurnUseCase: ""}, want: false},
		{name: "image generation queued notice still pending", event: ResponsesImageEvent{Text: "正在处理图片，图片准备好后我们会通知你。", ToolInvoked: nil, TurnUseCase: "image gen"}, want: false},
		{name: "image generation upstream text waits for explicit text marker", event: ResponsesImageEvent{Text: "上游返回的任何非排队文本都应该原样返回。", ToolInvoked: nil, TurnUseCase: "image gen"}, want: false},
		{name: "image result present", event: ResponsesImageEvent{Text: "ignored", Result: "b64"}, want: false},
		{name: "empty text", event: ResponsesImageEvent{Text: "", ToolInvoked: &toolFalse}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldTreatOfficialImageEventAsFinalText(tt.event); got != tt.want {
				t.Fatalf("shouldTreatOfficialImageEventAsFinalText(%#v) = %v, want %v", tt.event, got, tt.want)
			}
		})
	}
}

type testAccountLookup map[string]map[string]any

func (l testAccountLookup) GetAccount(accessToken string) map[string]any {
	return l[accessToken]
}

func TestConversationPayloadEmbedsOpenAIMessageHistoryInSingleUserMessage(t *testing.T) {
	client := &Client{}
	payload := client.conversationPayload([]map[string]any{
		{"role": "user", "content": "你好，你是什么模型？"},
		{"role": "assistant", "content": "你好！我是一个由OpenAI开发的语言模型，叫做GPT-4。"},
		{"role": "user", "content": "我之前说了什么？"},
	}, "auto", "Asia/Shanghai")

	if payload["parent_message_id"] != "client-created-root" {
		t.Fatalf("parent_message_id = %q, want client-created-root", payload["parent_message_id"])
	}
	messages, ok := payload["messages"].([]map[string]any)
	if !ok {
		t.Fatalf("messages = %T, want []map[string]any", payload["messages"])
	}
	if len(messages) != 1 {
		t.Fatalf("messages length = %d, want 1", len(messages))
	}
	author := messages[0]["author"].(map[string]any)
	if author["role"] != "user" {
		t.Fatalf("message role = %q, want user", author["role"])
	}
	content := messages[0]["content"].(map[string]any)
	parts := content["parts"].([]any)
	if len(parts) != 1 {
		t.Fatalf("parts length = %d, want 1", len(parts))
	}
	prompt, ok := parts[0].(string)
	if !ok {
		t.Fatalf("prompt = %T, want string", parts[0])
	}
	for _, want := range []string{
		"Conversation history:",
		"User: 你好，你是什么模型？",
		"Assistant: 你好！我是一个由OpenAI开发的语言模型，叫做GPT-4。",
		"Current user message:\n我之前说了什么？",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %s", want, prompt)
		}
	}
}

func TestConversationPayloadKeepsSingleUserMessagePrompt(t *testing.T) {
	client := &Client{}
	payload := client.conversationPayload([]map[string]any{
		{"role": "user", "content": "hello"},
	}, "auto", "Asia/Shanghai")

	messages := payload["messages"].([]map[string]any)
	content := messages[0]["content"].(map[string]any)
	parts := content["parts"].([]any)
	if parts[0] != "hello" {
		t.Fatalf("prompt = %q, want hello", parts[0])
	}
}

func TestConversationPayloadKeepsSystemHintsEmpty(t *testing.T) {
	client := &Client{}
	payload := client.conversationPayload([]map[string]any{
		{"role": "user", "content": "draw\n\n输出为 16:9 横屏构图"},
	}, "gpt-5.5", "Asia/Shanghai")

	if payload["model"] != "gpt-5.5" {
		t.Fatalf("model = %q, want gpt-5.5", payload["model"])
	}
	hints, ok := payload["system_hints"].([]any)
	if !ok {
		t.Fatalf("system_hints = %T, want []any", payload["system_hints"])
	}
	if len(hints) != 0 {
		t.Fatalf("system_hints = %#v, want empty", hints)
	}
	messages := payload["messages"].([]map[string]any)
	content := messages[0]["content"].(map[string]any)
	parts := content["parts"].([]any)
	if !strings.Contains(parts[0].(string), "输出为 16:9") {
		t.Fatalf("prompt = %q, want image generation hint", parts[0])
	}
}

func TestSolveTurnstileTokenInterpretsEncodedProgram(t *testing.T) {
	program := `[[3,"ok"]]`
	key := "secret"
	dx := base64.StdEncoding.EncodeToString([]byte(xorTurnstileString(program, key)))
	if got := solveTurnstileToken(dx, key); got != "b2s=" {
		t.Fatalf("solveTurnstileToken() = %q", got)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
