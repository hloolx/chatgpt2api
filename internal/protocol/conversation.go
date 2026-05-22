package protocol

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"chatgpt2api/internal/backend"
	"chatgpt2api/internal/service"
	"chatgpt2api/internal/storage"
	"chatgpt2api/internal/util"

	"github.com/HugoSmits86/nativewebp"
)

type ImageConfig interface {
	ImagesDir() string
	ImageMetadataDir() string
	BaseURL() string
}

type Engine struct {
	Accounts     *service.AccountService
	Config       ImageConfig
	Storage      storage.JSONDocumentBackend
	Proxy        *service.ProxyService
	Logger       *service.Logger
	CloudStorage *service.CloudStorageService

	ListModelsFunc         func(context.Context) (map[string]any, error)
	StreamImageOutputsFunc func(context.Context, *backend.Client, ConversationRequest, int, int) (<-chan ImageOutput, <-chan error)
	ImageTokenProvider     func(context.Context) (string, error)
	ImageClientFactory     func(string) *backend.Client

	responseContextMu sync.Mutex
	ResponseContexts  *ResponseContextStore
}

type ImageOutputSlotAcquirer func(context.Context, int) (func(), error)

// ImageOutputCharger atomically reserves/deducts billing for a single image
// output before it is persisted. It returns a non-nil error to deny saving
// this image. Denials backed by service.BillingLimitError are surfaced to the
// caller as the request-level error.
type ImageOutputCharger func(index int) error

type ConversationRequest struct {
	Model                  string
	Prompt                 string
	Messages               []map[string]any
	Images                 []string
	InputImageMask         string
	N                      int
	Size                   string
	Quality                string
	Background             string
	Moderation             string
	Style                  string
	OutputFormat           string
	OutputCompression      *int
	PartialImages          *int
	ResponseFormat         string
	BaseURL                string
	OwnerID                string
	OwnerName              string
	MessageAsError         bool
	AcquireImageOutputSlot ImageOutputSlotAcquirer
	ChargeImageOutput      ImageOutputCharger
}

func (r ConversationRequest) Normalized() ConversationRequest {
	r.Size = NormalizeImageGenerationSize(r.Size)
	r.Quality = ImageQualityForModel(r.Model, r.Quality)
	r.OutputFormat = NormalizeImageOutputFormat(r.OutputFormat)
	if !SupportsImageOutputCompression(r.OutputFormat) {
		r.OutputCompression = nil
	} else if r.OutputCompression != nil {
		compression := *r.OutputCompression
		if compression < 0 {
			compression = 0
		} else if compression > 100 {
			compression = 100
		}
		r.OutputCompression = &compression
	}
	return r
}

func ImageQualityForModel(model, quality string) string {
	if strings.TrimSpace(model) == util.ImageModelCodex {
		return ""
	}
	return strings.TrimSpace(quality)
}

func NormalizeImageOutputFormat(format string) string {
	return service.NormalizeImageOutputFormat(format)
}

func SupportsImageOutputCompression(format string) bool {
	return NormalizeImageOutputFormat(format) == "jpeg"
}

type ImageOutputOptions struct {
	Format              string
	Compression         *int
	TrustUpstreamFormat bool
}

type ImageToolOptions struct {
	Background     string
	Moderation     string
	Style          string
	PartialImages  *int
	InputImageMask string
}

func ImageOutputOptionsFromPayload(payload map[string]any) ImageOutputOptions {
	format := NormalizeImageOutputFormat(util.Clean(payload["output_format"]))
	options := ImageOutputOptions{Format: format}
	if !SupportsImageOutputCompression(format) {
		return options
	}
	if compression, ok := normalizedImageOutputCompression(payload["output_compression"]); ok {
		options.Compression = &compression
	}
	return options
}

func ImageToolOptionsFromPayload(payload map[string]any) ImageToolOptions {
	options := ImageToolOptions{
		Background:     util.Clean(payload["background"]),
		Moderation:     util.Clean(payload["moderation"]),
		Style:          util.Clean(payload["style"]),
		InputImageMask: responseImageMask(payload["input_image_mask"]),
	}
	if partialImages, ok := normalizedPositiveInt(payload["partial_images"]); ok {
		options.PartialImages = &partialImages
	}
	return options
}

func normalizedImageOutputCompression(value any) (int, bool) {
	if value == nil || strings.TrimSpace(util.Clean(value)) == "" {
		return 0, false
	}
	compression := util.ToInt(value, -1)
	if compression < 0 {
		return 0, false
	}
	if compression > 100 {
		compression = 100
	}
	return compression, true
}

func (r ConversationRequest) SupportsImageGenerationModel() bool {
	return util.IsImageGenerationModel(r.Model)
}

func (r ConversationRequest) UsesResponsesImageRoute() bool {
	model := strings.TrimSpace(r.Model)
	return model == "" || model == util.ImageModelAuto || model == util.ImageModelGPT || model == util.ImageModelCodex
}

type ConversationState struct {
	Text           string
	ConversationID string
	FileIDs        []string
	SedimentIDs    []string
	Blocked        bool
	ToolInvoked    *bool
	TurnUseCase    string
}

type ConversationEvent map[string]any

type ImageOutput struct {
	Kind              string
	Model             string
	Index             int
	Total             int
	Created           int64
	Text              string
	UpstreamEventType string
	Data              []map[string]any
	ChargeHandled     bool
}

type ImageOutputProgressCallback func([]map[string]any)

type indexedImageOutputData struct {
	index int
	data  []map[string]any
}

type ImageGenerationError struct {
	Message    string
	StatusCode int
	Type       string
	Code       string
	Param      any
}

type imageRunResult struct {
	emitted         bool
	returnedMessage bool
	lastError       string
	err             error
}

func (e *ImageGenerationError) Error() string { return e.Message }

func (e *ImageGenerationError) OpenAIError() map[string]any {
	return map[string]any{"error": map[string]any{"message": e.Message, "type": e.Type, "param": e.Param, "code": e.Code}}
}

func NewImageGenerationError(message string) *ImageGenerationError {
	return &ImageGenerationError{Message: message, StatusCode: 502, Type: "server_error", Code: "upstream_error"}
}

const maxTransientImageStreamAttempts = 3

func isTransientImageStreamErrorMessage(message string) bool {
	lower := strings.ToLower(strings.TrimSpace(message))
	if lower == "" {
		return false
	}
	if strings.Contains(lower, strings.ToLower(util.UpstreamConnectionFailureMessage)) {
		return true
	}
	if _, ok := util.SummarizeUpstreamConnectionError(lower); ok {
		return true
	}
	for _, token := range []string{
		"sse read error",
		"responses sse read error",
		"stream error",
		"flow_control_error",
		"internal_error",
		"received from peer",
		"unexpected eof",
		"http2: client connection lost",
		"connection reset by peer",
		"stream closed",
	} {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

func imageStreamErrorMessage(message string) string {
	text := strings.TrimSpace(message)
	lower := strings.ToLower(text)
	if strings.Contains(lower, "cf_chl") ||
		strings.Contains(lower, "challenge-platform") ||
		strings.Contains(lower, "enable javascript and cookies to continue") ||
		strings.Contains(lower, "cloudflare challenge") {
		return "upstream returned Cloudflare challenge page; refresh browser fingerprint/session or change proxy"
	}
	if detail, ok := util.SummarizeUpstreamConnectionError(text); ok {
		return detail
	}
	if strings.Contains(lower, "flow_control_error") {
		return "upstream image stream interrupted by HTTP/2 flow control; retry the request or change proxy if it repeats"
	}
	if isCodexResponsesUnauthorizedErrorMessage(lower) {
		return "codex-gpt-image-2 需要 Plus / Team / Pro 账号；Free 账号无权访问 Codex 图片接口"
	}
	if text == "" {
		return "upstream image request failed without error detail"
	}
	return text
}

func isCodexResponsesUnauthorizedErrorMessage(message string) bool {
	return strings.Contains(message, "/backend-api/codex/responses failed: status=401") &&
		strings.Contains(message, "unauthorized")
}

func (o ImageOutput) Chunk() map[string]any {
	chunk := map[string]any{
		"object":              "image.generation.chunk",
		"created":             o.Created,
		"model":               o.Model,
		"index":               o.Index,
		"total":               o.Total,
		"progress_text":       o.Text,
		"upstream_event_type": o.UpstreamEventType,
		"data":                []map[string]any{},
	}
	switch o.Kind {
	case "message":
		chunk["object"] = "image.generation.message"
		chunk["message"] = o.Text
		delete(chunk, "progress_text")
		delete(chunk, "upstream_event_type")
	case "result":
		chunk["object"] = "image.generation.result"
		chunk["data"] = o.Data
		delete(chunk, "progress_text")
		delete(chunk, "upstream_event_type")
	}
	return chunk
}

func (e *Engine) TextBackend(accessToken string) *backend.Client {
	return backend.NewClient(accessToken, e.Accounts, e.Proxy)
}

func (e *Engine) ListModels(ctx context.Context) (map[string]any, error) {
	result, err := e.listModels(ctx)
	if err != nil {
		return nil, err
	}
	data := util.AsMapSlice(result["data"])
	seen := map[string]struct{}{}
	for _, item := range data {
		if id := util.Clean(item["id"]); id != "" {
			seen[id] = struct{}{}
		}
	}
	for _, model := range util.ModelList() {
		if _, ok := seen[model]; !ok {
			data = append(data, map[string]any{"id": model, "object": "model", "created": 0, "owned_by": "chatgpt2api", "permission": []any{}, "root": model, "parent": nil})
		}
	}
	result["data"] = data
	return result, nil
}

func (e *Engine) listModels(ctx context.Context) (map[string]any, error) {
	if e != nil && e.ListModelsFunc != nil {
		return e.ListModelsFunc(ctx)
	}
	return backend.NewClient("", e.Accounts, e.Proxy).ListModels(ctx)
}

func (e *Engine) StreamTextDeltas(ctx context.Context, client *backend.Client, request ConversationRequest) (<-chan string, <-chan error) {
	out := make(chan string)
	errCh := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errCh)
		events, convErr := e.ConversationEvents(ctx, client, request.Messages, request.Model, request.Prompt)
		for event := range events {
			if event["type"] != "conversation.delta" {
				continue
			}
			delta := util.Clean(event["delta"])
			if delta == "" {
				continue
			}
			select {
			case out <- delta:
			case <-ctx.Done():
				errCh <- ctx.Err()
				return
			}
		}
		if err := <-convErr; err != nil {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	return out, errCh
}

func (e *Engine) CollectText(ctx context.Context, client *backend.Client, request ConversationRequest) (string, error) {
	deltas, errCh := e.StreamTextDeltas(ctx, client, request)
	var parts []string
	for delta := range deltas {
		parts = append(parts, delta)
	}
	return strings.Join(parts, ""), <-errCh
}

func (e *Engine) CollectVisionText(ctx context.Context, client *backend.Client, messages []map[string]any, model string, images []backend.VisionImage) (string, error) {
	deltas, errCh := client.StreamMultimodalConversation(ctx, messages, model, images)
	var parts []string
	for delta := range deltas {
		parts = append(parts, delta)
	}
	return strings.Join(parts, ""), <-errCh
}

func (e *Engine) ConversationEvents(ctx context.Context, client *backend.Client, messages []map[string]any, model, prompt string) (<-chan ConversationEvent, <-chan error) {
	out := make(chan ConversationEvent)
	errCh := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errCh)
		normalized := NormalizeMessages(messages, nil)
		if len(normalized) == 0 && prompt != "" {
			normalized = []map[string]any{{"role": "user", "content": prompt}}
		}
		historyText := AssistantHistoryText(normalized)
		historyMessages := AssistantHistoryMessages(normalized)
		payloads, upstreamErr := client.StreamConversation(ctx, normalized, model, prompt)
		iterErr := IterConversationPayloads(ctx, payloads, historyText, historyMessages, out)
		upErr := <-upstreamErr
		if iterErr != nil {
			errCh <- iterErr
			return
		}
		errCh <- upErr
	}()
	return out, errCh
}

func IterConversationPayloads(ctx context.Context, payloads <-chan string, historyText string, historyMessages []string, out chan<- ConversationEvent) error {
	state := &ConversationState{}
	historyIndex := 0
	for payload := range payloads {
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			event := conversationBaseEvent("conversation.done", state)
			event["done"] = true
			select {
			case out <- event:
			case <-ctx.Done():
				return ctx.Err()
			}
			break
		}
		var raw any
		if err := json.Unmarshal([]byte(payload), &raw); err != nil {
			UpdateConversationState(state, payload, nil)
			event := conversationBaseEvent("conversation.raw", state)
			event["payload"] = payload
			out <- event
			continue
		}
		eventMap, ok := raw.(map[string]any)
		if !ok {
			event := conversationBaseEvent("conversation.event", state)
			event["raw"] = raw
			out <- event
			continue
		}
		UpdateConversationState(state, payload, eventMap)
		if historyIndex < len(historyMessages) && EventAssistantText(eventMap, historyText) == historyMessages[historyIndex] {
			historyIndex++
			state.Text = ""
			continue
		}
		nextText := AssistantText(eventMap, state.Text, historyText)
		if nextText != state.Text {
			delta := nextText
			if strings.HasPrefix(nextText, state.Text) {
				delta = nextText[len(state.Text):]
			}
			state.Text = nextText
			event := conversationBaseEvent("conversation.delta", state)
			event["raw"] = eventMap
			event["delta"] = delta
			out <- event
			continue
		}
		event := conversationBaseEvent("conversation.event", state)
		event["raw"] = eventMap
		out <- event
	}
	return nil
}

func (e *Engine) StreamImageOutputsWithPool(ctx context.Context, request ConversationRequest) (<-chan ImageOutput, <-chan error) {
	request = request.Normalized()
	out := make(chan ImageOutput)
	errCh := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errCh)
		if !request.SupportsImageGenerationModel() {
			errCh <- &ImageGenerationError{Message: "unsupported image model,supported models: " + util.ImageGenerationModelNames(), StatusCode: 502, Type: "server_error", Code: "upstream_error"}
			return
		}
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		resultCh := make(chan imageRunResult, request.N)
		var wg sync.WaitGroup
		for index := 1; index <= request.N; index++ {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				releaseSlot, err := request.acquireImageOutputSlot(ctx, index)
				if err != nil {
					cancel()
					resultCh <- imageRunResult{lastError: err.Error(), err: err}
					return
				}
				defer releaseSlot()
				result := e.runSingleImageOutput(ctx, out, request, index)
				if result.err != nil {
					cancel()
				}
				resultCh <- result
			}(index)
		}
		go func() {
			wg.Wait()
			close(resultCh)
		}()

		emittedAny := false
		messageOnly := false
		lastError := ""
		for result := range resultCh {
			emittedAny = emittedAny || result.emitted
			messageOnly = messageOnly || result.returnedMessage
			if result.lastError != "" {
				lastError = result.lastError
			}
			if result.err != nil {
				errCh <- result.err
				return
			}
		}
		if messageOnly {
			errCh <- nil
			return
		}
		if !emittedAny {
			errCh <- NewImageGenerationError(imageStreamErrorMessage(lastError))
			return
		}
		errCh <- nil
	}()
	return out, errCh
}

func (r ConversationRequest) acquireImageOutputSlot(ctx context.Context, index int) (func(), error) {
	if r.AcquireImageOutputSlot == nil {
		return noopImageOutputSlotRelease, nil
	}
	release, err := r.AcquireImageOutputSlot(ctx, index)
	if err != nil {
		return nil, err
	}
	if release == nil {
		return noopImageOutputSlotRelease, nil
	}
	return release, nil
}

func noopImageOutputSlotRelease() {}

func (e *Engine) runSingleImageOutput(ctx context.Context, out chan<- ImageOutput, request ConversationRequest, index int) imageRunResult {
	result := imageRunResult{}
	transientAttempts := 0
	for {
		token, err := e.nextImageAccessToken(ctx)
		if err != nil {
			result.lastError = err.Error()
			result.err = NewImageGenerationError(err.Error())
			return result
		}
		emittedForToken := false
		returnedMessage := false
		returnedResult := false
		rateLimitedForToken := false
		rateLimitMessage := ""
		client := e.newImageClient(token)
		outputs, imageErr := e.StreamImageOutputs(ctx, client, request, index, request.N)
		for output := range outputs {
			if output.Kind == "message" && service.IsAccountRateLimitedErrorMessage(output.Text) {
				rateLimitedForToken = true
				rateLimitMessage = output.Text
				result.lastError = output.Text
				continue
			}
			if output.Kind == "message" && request.MessageAsError {
				if e.Accounts != nil {
					e.Accounts.MarkImageResult(token, false)
				}
				result.err = &ImageGenerationError{Message: firstNonEmpty(output.Text, "Image generation returned a text response instead of image data."), StatusCode: 400, Type: "invalid_request_error", Code: "image_generation_text_response"}
				result.lastError = result.err.Error()
				return result
			}
			if output.Kind == "result" && request.ChargeImageOutput != nil && !output.ChargeHandled {
				if err := request.ChargeImageOutput(index); err != nil {
					var billingErr service.BillingLimitError
					if errors.As(err, &billingErr) {
						result.err = billingErr
						result.lastError = billingErr.Error()
					} else {
						result.err = NewImageGenerationError(err.Error())
						result.lastError = err.Error()
					}
					return result
				}
			}
			result.emitted = true
			emittedForToken = true
			returnedMessage = output.Kind == "message"
			returnedResult = returnedResult || output.Kind == "result"
			out <- output
		}
		err = <-imageErr
		if err == nil {
			if rateLimitedForToken {
				if e.Accounts != nil {
					e.Accounts.MarkImageResult(token, false)
					e.Accounts.ApplyAccountErrorMessage(token, "image_stream", rateLimitMessage)
				}
				continue
			}
			if returnedMessage || !returnedResult {
				if e.Accounts != nil {
					e.Accounts.MarkImageResult(token, false)
				}
				result.returnedMessage = returnedMessage || !returnedResult
				return result
			}
			if e.Accounts != nil {
				e.Accounts.MarkImageResult(token, true)
			}
			return result
		}
		var billingErr service.BillingLimitError
		if errors.As(err, &billingErr) {
			result.err = billingErr
			result.lastError = billingErr.Error()
			return result
		}
		if e.Accounts != nil {
			e.Accounts.MarkImageResult(token, false)
		}
		result.lastError = err.Error()
		if e.Accounts != nil {
			if normalized, handled := e.Accounts.ApplyAccountErrorMessage(token, "image_stream", result.lastError); handled {
				result.lastError = normalized
				if service.IsAccountRateLimitedErrorMessage(err.Error()) || !emittedForToken {
					continue
				}
			}
		}
		if !emittedForToken && IsTokenInvalidError(result.lastError) {
			continue
		}
		if !returnedResult && isTransientImageStreamErrorMessage(result.lastError) && transientAttempts < maxTransientImageStreamAttempts {
			transientAttempts++
			continue
		}
		result.err = NewImageGenerationError(imageStreamErrorMessage(result.lastError))
		return result
	}
}

func (e *Engine) StreamImageOutputs(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
	if e.StreamImageOutputsFunc != nil {
		return e.StreamImageOutputsFunc(ctx, client, request, index, total)
	}
	return e.StreamResponsesImageOutputs(ctx, client, request, index, total)
}

func (e *Engine) nextImageAccessToken(ctx context.Context) (string, error) {
	if e.ImageTokenProvider != nil {
		return e.ImageTokenProvider(ctx)
	}
	return e.Accounts.GetAvailableAccessTokenFor(ctx, nil)
}

func (e *Engine) newImageClient(token string) *backend.Client {
	if e.ImageClientFactory != nil {
		return e.ImageClientFactory(token)
	}
	return backend.NewClient(token, e.Accounts, e.Proxy)
}

func (e *Engine) StreamResponsesImageOutputs(ctx context.Context, client *backend.Client, request ConversationRequest, index, total int) (<-chan ImageOutput, <-chan error) {
	out := make(chan ImageOutput)
	errCh := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errCh)
		prompt := buildResponsesImagePrompt(request.Prompt, request.Size, request.Model)
		if strings.TrimSpace(prompt) == "" {
			prompt = request.Prompt
		}
		events, upstreamErr := client.StreamResponsesImage(ctx, backend.ResponsesImageRequest{
			Prompt:            prompt,
			Model:             request.Model,
			Size:              request.Size,
			Quality:           request.Quality,
			Background:        request.Background,
			Moderation:        request.Moderation,
			Style:             request.Style,
			OutputFormat:      request.OutputFormat,
			OutputCompression: request.OutputCompression,
			PartialImages:     request.PartialImages,
			InputImages:       responsesInputImages(request.Images),
			InputImageMask:    responsesInputImagePtr(request.InputImageMask),
		})
		emitted := false
		seen := map[string]struct{}{}
		for event := range events {
			if event.PartialImage != "" {
				out <- ImageOutput{Kind: "progress", Model: request.Model, Index: index, Total: total, Created: firstNonZeroInt64(event.Created, time.Now().Unix()), Text: event.Text, UpstreamEventType: event.Type}
				continue
			}
			if isFinalImageTextEvent(event) {
				emitted = true
				out <- ImageOutput{Kind: "message", Model: request.Model, Index: index, Total: total, Created: firstNonZeroInt64(event.Created, time.Now().Unix()), Text: strings.TrimSpace(event.Text), UpstreamEventType: event.Type}
				continue
			}
			if event.Result == "" {
				continue
			}
			key := firstNonEmpty(event.ItemID, event.Result)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			item := map[string]any{
				"b64_json":       event.Result,
				"revised_prompt": firstNonEmpty(event.RevisedPrompt, prompt),
				"output_format":  firstNonEmpty(event.OutputFormat, request.OutputFormat),
			}
			if event.Background != "" {
				item["background"] = event.Background
			}
			created := firstNonZeroInt64(event.Created, time.Now().Unix())
			chargeHandled := false
			result, err := e.FormatImageResultWithCharge([]map[string]any{item}, prompt, request.ResponseFormat, request.BaseURL, request.OwnerID, request.OwnerName, created, "", imageResultOutputOptions(request, event), func() error {
				if request.ChargeImageOutput == nil {
					return nil
				}
				if err := request.ChargeImageOutput(index); err != nil {
					return err
				}
				chargeHandled = true
				return nil
			})
			if err != nil {
				errCh <- err
				return
			}
			data := util.AsMapSlice(result["data"])
			if len(data) > 0 {
				emitted = true
				out <- ImageOutput{Kind: "result", Model: request.Model, Index: index, Total: total, Created: created, Data: data, ChargeHandled: chargeHandled}
			}
		}
		if err := <-upstreamErr; err != nil {
			errCh <- err
			return
		}
		if !emitted {
			errCh <- fmt.Errorf("upstream image stream completed without image output")
			return
		}
		errCh <- nil
	}()
	return out, errCh
}

func imageResultOutputOptions(request ConversationRequest, event backend.ResponsesImageEvent) ImageOutputOptions {
	if strings.TrimSpace(request.Model) == util.ImageModelCodex {
		return ImageOutputOptions{Format: firstNonEmpty(event.OutputFormat, request.OutputFormat), TrustUpstreamFormat: true}
	}
	return ImageOutputOptions{Format: request.OutputFormat, Compression: request.OutputCompression}
}

func responsesInputImages(values []string) []backend.ResponsesInputImage {
	out := make([]backend.ResponsesInputImage, 0, len(values))
	for _, value := range values {
		image := responsesInputImage(value)
		if len(image.Data) > 0 {
			out = append(out, image)
		}
	}
	return out
}

func responsesInputImagePtr(value string) *backend.ResponsesInputImage {
	image := responsesInputImage(value)
	if len(image.Data) == 0 {
		return nil
	}
	return &image
}

func responsesInputImage(value string) backend.ResponsesInputImage {
	value = strings.TrimSpace(value)
	if value == "" {
		return backend.ResponsesInputImage{}
	}
	contentType := "image/png"
	dataPart := value
	if strings.HasPrefix(value, "data:") {
		header, data, ok := strings.Cut(value, ",")
		if ok {
			dataPart = data
			if mimeType := strings.TrimPrefix(strings.Split(header, ";")[0], "data:"); mimeType != "" {
				contentType = mimeType
			}
		}
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(dataPart))
	if err != nil {
		return backend.ResponsesInputImage{}
	}
	return backend.ResponsesInputImage{Data: data, ContentType: contentType}
}

func firstNonZeroInt64(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func isFinalImageTextEvent(event backend.ResponsesImageEvent) bool {
	if strings.TrimSpace(event.Text) == "" || event.Result != "" {
		return false
	}
	if event.Type == "image_text_response" {
		return true
	}
	if event.Blocked {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(event.TurnUseCase), "text") {
		return true
	}
	if responsesImageEventHasResultPointers(event) || isResponsesImageGenerationUseCase(event.TurnUseCase) {
		return false
	}
	return event.ToolInvoked != nil && !*event.ToolInvoked
}

func responsesImageEventHasResultPointers(event backend.ResponsesImageEvent) bool {
	return len(filterResponsesImageIDs(event.FileIDs)) > 0 || len(filterResponsesImageIDs(event.SedimentIDs)) > 0
}

func filterResponsesImageIDs(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || value == "file_upload" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func isResponsesImageGenerationUseCase(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "_", " ")
	normalized = strings.ReplaceAll(normalized, "-", " ")
	normalized = strings.Join(strings.Fields(normalized), " ")
	return normalized == "image gen" || normalized == "image generation"
}

func (e *Engine) CollectImageOutputs(outputs <-chan ImageOutput, errCh <-chan error) (map[string]any, error) {
	return e.CollectImageOutputsWithProgress(outputs, errCh, nil)
}

func (e *Engine) CollectImageOutputsWithProgress(outputs <-chan ImageOutput, errCh <-chan error, onProgress ImageOutputProgressCallback) (map[string]any, error) {
	var created int64
	var results []indexedImageOutputData
	message := ""
	var progress []string
	for output := range outputs {
		if created == 0 {
			created = output.Created
		}
		switch output.Kind {
		case "progress":
			if output.Text != "" {
				progress = append(progress, output.Text)
			}
		case "message":
			message = output.Text
		case "result":
			results = append(results, indexedImageOutputData{index: output.Index, data: output.Data})
			if onProgress != nil {
				onProgress(indexedImageDataWithPlaceholders(results))
			}
		}
	}
	streamErr := <-errCh
	if created == 0 {
		created = time.Now().Unix()
	}
	data := denseIndexedImageData(results)
	if streamErr != nil && onProgress != nil {
		data = indexedImageDataWithPlaceholders(results)
	}
	result := map[string]any{"created": created, "data": data}
	if len(data) == 0 {
		if text := strings.TrimSpace(message); text != "" {
			result["message"] = text
			result["output_type"] = "text"
		} else if text := strings.TrimSpace(strings.Join(progress, "")); text != "" {
			result["message"] = text
		}
	}
	if streamErr != nil {
		if imageErr, ok := streamErr.(*ImageGenerationError); ok && imageErr.Code == "image_generation_text_response" {
			result["output_type"] = "text"
		}
		if result["message"] == nil {
			result["message"] = streamErr.Error()
		}
		return result, streamErr
	}
	return result, nil
}

func denseIndexedImageData(results []indexedImageOutputData) []map[string]any {
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].index == results[j].index {
			return i < j
		}
		return results[i].index < results[j].index
	})
	data := make([]map[string]any, 0)
	for _, item := range results {
		data = append(data, cloneImageOutputData(item.data)...)
	}
	return data
}

func indexedImageDataWithPlaceholders(results []indexedImageOutputData) []map[string]any {
	maxIndex := 0
	for _, item := range results {
		if item.index > maxIndex {
			maxIndex = item.index
		}
	}
	if maxIndex < 1 {
		return nil
	}
	data := make([]map[string]any, maxIndex)
	for i := range data {
		data[i] = map[string]any{}
	}
	for _, item := range results {
		if item.index < 1 || len(item.data) == 0 {
			continue
		}
		cloned := cloneImageOutputData(item.data)
		data[item.index-1] = cloned[0]
		data = append(data, cloned[1:]...)
	}
	return data
}

func cloneImageOutputData(items []map[string]any) []map[string]any {
	if len(items) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if item == nil {
			out = append(out, map[string]any{})
			continue
		}
		out = append(out, util.CopyMap(item))
	}
	return out
}

func (e *Engine) FormatImageResult(items []map[string]any, prompt, responseFormat, baseURL, ownerID, ownerName string, created int64, message string) map[string]any {
	return e.FormatImageResultWithOptions(items, prompt, responseFormat, baseURL, ownerID, ownerName, created, message, ImageOutputOptions{})
}

func (e *Engine) FormatImageResultWithOptions(items []map[string]any, prompt, responseFormat, baseURL, ownerID, ownerName string, created int64, message string, options ImageOutputOptions) map[string]any {
	result, _ := e.formatImageResultWithOptions(items, prompt, responseFormat, baseURL, ownerID, ownerName, created, message, options, nil)
	return result
}

func (e *Engine) FormatImageResultWithCharge(items []map[string]any, prompt, responseFormat, baseURL, ownerID, ownerName string, created int64, message string, options ImageOutputOptions, charge func() error) (map[string]any, error) {
	return e.formatImageResultWithOptions(items, prompt, responseFormat, baseURL, ownerID, ownerName, created, message, options, charge)
}

func (e *Engine) formatImageResultWithOptions(items []map[string]any, prompt, responseFormat, baseURL, ownerID, ownerName string, created int64, message string, options ImageOutputOptions, charge func() error) (map[string]any, error) {
	defaultFormat := NormalizeImageOutputFormat(options.Format)
	hasRequestedFormat := strings.TrimSpace(options.Format) != ""
	var data []map[string]any
	for _, item := range items {
		b64 := util.Clean(item["b64_json"])
		if b64 == "" {
			continue
		}
		revised := firstNonEmpty(util.Clean(item["revised_prompt"]), prompt)
		imageBytes, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			continue
		}
		itemOptions := options
		if hasRequestedFormat {
			itemOptions.Format = defaultFormat
		} else if itemFormat := strings.TrimSpace(util.Clean(item["output_format"])); itemFormat != "" {
			itemOptions.Format = NormalizeImageOutputFormat(itemFormat)
		}
		if itemOptions.Format == "" {
			itemOptions.Format = defaultFormat
		}
		if !SupportsImageOutputCompression(itemOptions.Format) {
			itemOptions.Compression = nil
		}
		if itemOptions.Compression == nil {
			if SupportsImageOutputCompression(itemOptions.Format) {
				if compression, ok := normalizedImageOutputCompression(item["output_compression"]); ok {
					itemOptions.Compression = &compression
				}
			}
		}
		if !itemOptions.TrustUpstreamFormat {
			imageBytes, err = encodeImageBytes(imageBytes, itemOptions)
			if err != nil {
				continue
			}
		}
		if charge != nil {
			if err := charge(); err != nil {
				if created == 0 {
					created = time.Now().Unix()
				}
				result := map[string]any{"created": created, "data": data}
				if message != "" && len(data) == 0 {
					result["message"] = message
				}
				return result, err
			}
		}
		outputFormat := NormalizeImageOutputFormat(itemOptions.Format)
		urlValue := e.SaveImageBytesForOwnerWithFormat(imageBytes, baseURL, ownerID, ownerName, outputFormat)
		responseItem := map[string]any{"url": urlValue, "revised_prompt": revised, "output_format": outputFormat}
		if responseFormat == "b64_json" {
			responseItem["b64_json"] = base64.StdEncoding.EncodeToString(imageBytes)
		}
		data = append(data, responseItem)
	}
	if created == 0 {
		created = time.Now().Unix()
	}
	result := map[string]any{"created": created, "data": data}
	if message != "" && len(data) == 0 {
		result["message"] = message
	}
	return result, nil
}

func (e *Engine) SaveImageBytes(imageData []byte, baseURL string) string {
	return e.SaveImageBytesForOwner(imageData, baseURL, "", "")
}

func (e *Engine) SaveImageBytesForOwner(imageData []byte, baseURL, ownerID, ownerName string) string {
	return e.SaveImageBytesForOwnerWithFormat(imageData, baseURL, ownerID, ownerName, "png")
}

func (e *Engine) SaveImageBytesForOwnerWithFormat(imageData []byte, baseURL, ownerID, ownerName, outputFormat string) string {
	outputFormat = NormalizeImageOutputFormat(outputFormat)
	sum := md5.Sum(imageData)
	filename := fmt.Sprintf("%d_%s.%s", time.Now().Unix(), hex.EncodeToString(sum[:]), imageFileExtension(outputFormat))
	relativeDir := filepath.Join(time.Now().Format("2006"), time.Now().Format("01"), time.Now().Format("02"))
	rel := filepath.Join(relativeDir, filename)
	filePath := filepath.Join(e.Config.ImagesDir(), rel)
	_ = os.MkdirAll(filepath.Dir(filePath), 0o755)

	// Always write to local disk first as a safety net.
	_ = os.WriteFile(filePath, imageData, 0o644)
	e.writeImageOwnerMetadata(rel, ownerID, ownerName)

	// Cloud storage: upload immediately (sync). On success, delete local file.
	if e.CloudStorage != nil && e.CloudStorage.Enabled() {
		go func(data []byte, fname string, localPath string) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			record, err := e.CloudStorage.UploadImage(ctx, data, fname)
			if err != nil {
				if e.Logger != nil {
					e.Logger.Warning("cloud upload failed, keeping local file", "filename", fname, "error", err)
				}
				return
			}
			if err := e.CloudStorage.SaveRecord(ctx, rel, record); err != nil {
				if e.Logger != nil {
					e.Logger.Warning("cloud record save failed", "filename", fname, "error", err)
				}
				return
			}
			// Upload succeeded and record saved. Remove local file to save disk space.
			if removeErr := os.Remove(localPath); removeErr != nil && !os.IsNotExist(removeErr) {
				if e.Logger != nil {
					e.Logger.Warning("failed to remove local file after cloud upload", "path", localPath, "error", removeErr)
				}
			}
		}(imageData, filename, filePath)
	}

	if baseURL == "" {
		baseURL = e.Config.BaseURL()
	}
	return strings.TrimRight(baseURL, "/") + "/images/" + filepath.ToSlash(rel)
}

func imageFileExtension(outputFormat string) string {
	if NormalizeImageOutputFormat(outputFormat) == "jpeg" {
		return "jpg"
	}
	return NormalizeImageOutputFormat(outputFormat)
}

func encodeImageBytes(data []byte, options ImageOutputOptions) ([]byte, error) {
	format := NormalizeImageOutputFormat(options.Format)
	if format == "png" {
		return data, nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	switch format {
	case "jpeg":
		quality := 90
		if options.Compression != nil {
			quality = 100 - *options.Compression
			if quality < 1 {
				quality = 1
			} else if quality > 100 {
				quality = 100
			}
		}
		if err := jpeg.Encode(&buf, flattenAlpha(img), &jpeg.Options{Quality: quality}); err != nil {
			return nil, err
		}
	case "webp":
		if err := nativewebp.Encode(&buf, img, nil); err != nil {
			return nil, err
		}
	default:
		if err := png.Encode(&buf, img); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func flattenAlpha(img image.Image) image.Image {
	bounds := img.Bounds()
	out := image.NewRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			alpha := int(a)
			out.Set(x, y, color.RGBA{
				R: blendOverWhite(int(r), alpha),
				G: blendOverWhite(int(g), alpha),
				B: blendOverWhite(int(b), alpha),
				A: 255,
			})
		}
	}
	return out
}

func blendOverWhite(channel, alpha int) uint8 {
	value := (channel*alpha + 0xffff*(0xffff-alpha)) / 0xffff
	return uint8(value >> 8)
}

func (e *Engine) writeImageOwnerMetadata(rel, ownerID, ownerName string) {
	ownerID = strings.TrimSpace(ownerID)
	ownerName = strings.TrimSpace(ownerName)
	if e == nil || e.Config == nil || ownerID == "" {
		return
	}
	value := map[string]any{"owner_id": ownerID, "updated_at": time.Now().UTC().Format(time.RFC3339Nano)}
	if ownerName != "" {
		value["owner_name"] = ownerName
	}
	if e.Storage != nil {
		_ = e.Storage.SaveJSONDocument(imageOwnerDocumentName(rel), value)
		return
	}
	metaPath := filepath.Join(e.Config.ImageMetadataDir(), filepath.FromSlash(filepath.ToSlash(rel))+".json")
	_ = os.MkdirAll(filepath.Dir(metaPath), 0o755)
	data, err := json.Marshal(value)
	if err == nil {
		_ = os.WriteFile(metaPath, data, 0o644)
	}
}

func imageOwnerDocumentName(rel string) string {
	return "image_metadata/" + filepath.ToSlash(rel) + ".json"
}

func IsTokenInvalidError(message string) bool {
	return service.IsAccountInvalidErrorMessage(message)
}

func MessageText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, item := range v {
			switch x := item.(type) {
			case string:
				parts = append(parts, x)
			case map[string]any:
				t := util.Clean(x["type"])
				if t == "text" || t == "input_text" || t == "output_text" {
					parts = append(parts, util.Clean(x["text"]))
				}
			}
		}
		return strings.Join(parts, "")
	default:
		return ""
	}
}

func NormalizeMessages(messages any, system any) []map[string]any {
	var normalized []map[string]any
	if text := MessageText(system); text != "" {
		normalized = append(normalized, map[string]any{"role": "system", "content": text})
	}
	if list, ok := messages.([]map[string]any); ok {
		for _, message := range list {
			normalized = append(normalized, map[string]any{"role": firstNonEmpty(util.Clean(message["role"]), "user"), "content": MessageText(message["content"])})
		}
		return normalized
	}
	if list, ok := messages.([]any); ok {
		for _, raw := range list {
			if message, ok := raw.(map[string]any); ok {
				normalized = append(normalized, map[string]any{"role": firstNonEmpty(util.Clean(message["role"]), "user"), "content": MessageText(message["content"])})
			}
		}
	}
	return normalized
}

func AssistantHistoryText(messages []map[string]any) string {
	var parts []string
	for _, item := range messages {
		if item["role"] == "assistant" {
			parts = append(parts, util.Clean(item["content"]))
		}
	}
	return strings.Join(parts, "")
}

func AssistantHistoryMessages(messages []map[string]any) []string {
	var out []string
	for _, item := range messages {
		if item["role"] == "assistant" && util.Clean(item["content"]) != "" {
			out = append(out, util.Clean(item["content"]))
		}
	}
	return out
}

func NormalizeImageGenerationSize(size string) string {
	switch strings.ToLower(strings.TrimSpace(size)) {
	case "1080p":
		return "1080x1080"
	case "2k":
		return "2048x2048"
	case "4k":
		return "2880x2880"
	default:
		return strings.TrimSpace(size)
	}
}

func imageSizeDimensions(size string) (int, int, bool) {
	matches := regexp.MustCompile(`^(\d+)x(\d+)$`).FindStringSubmatch(strings.ToLower(strings.TrimSpace(size)))
	if len(matches) != 3 {
		return 0, 0, false
	}
	width := util.ToInt(matches[1], 0)
	height := util.ToInt(matches[2], 0)
	if width <= 0 || height <= 0 {
		return 0, 0, false
	}
	return width, height, true
}

func BuildImagePrompt(prompt, size, quality string) string {
	prompt = strings.TrimSpace(prompt)
	size = NormalizeImageGenerationSize(size)
	if strings.EqualFold(size, "auto") {
		size = ""
	}
	var hintsList []string
	hints := map[string]string{
		"1:1":  "输出为 1:1 正方形构图，主体居中，适合正方形画幅。",
		"3:2":  "输出为 3:2 横版构图，适合摄影、产品展示和横向叙事画幅。",
		"2:3":  "输出为 2:3 竖版构图，适合海报、人物和纵向叙事画幅。",
		"16:9": "输出为 16:9 横屏构图，适合宽画幅展示。",
		"21:9": "输出为 21:9 超宽横版构图，适合电影感全景和宽银幕画幅。",
		"9:16": "输出为 9:16 竖屏构图，适合竖版画幅展示。",
		"4:3":  "输出为 4:3 比例，兼顾宽度与高度，适合展示画面细节。",
		"3:4":  "输出为 3:4 比例，纵向构图，适合人物肖像或竖向场景。",
	}
	if size != "" {
		if width, height, ok := imageSizeDimensions(size); ok {
			hintsList = append(hintsList, fmt.Sprintf("以 %d x %d 像素对应的宽高比作为构图偏好，实际像素以上游返回为准。", width, height))
		} else if hint, ok := hints[size]; ok {
			hintsList = append(hintsList, hint)
		} else {
			hintsList = append(hintsList, "输出图片，目标尺寸或宽高比为 "+size+"。")
		}
	}
	qualityHints := map[string]string{
		"low":    "画质使用 Low 档，优先更快出图，细节可以适度简化。",
		"medium": "画质使用 Medium 档，在速度、细节和整体完成度之间保持平衡。",
		"high":   "画质使用 High 档，提升细节、纹理、光影和整体完成度。",
	}
	if hint, ok := qualityHints[strings.ToLower(strings.TrimSpace(quality))]; ok {
		hintsList = append(hintsList, hint)
	}
	if len(hintsList) == 0 {
		return prompt
	}
	return prompt + "\n\n" + strings.Join(hintsList, "\n")
}

func buildResponsesImagePrompt(prompt, size, model string) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return ""
	}
	if strings.TrimSpace(model) == util.ImageModelCodex {
		return prompt
	}
	return BuildImagePrompt(prompt, size, "")
}

func CountMessageTokens(messages []map[string]any, model string) int {
	total := 3
	for _, message := range messages {
		total += 3
		for key, value := range message {
			if text, ok := value.(string); ok {
				total += CountTextTokens(text, model)
				if key == "name" {
					total++
				}
			}
		}
	}
	return total
}

func CountTextTokens(text, model string) int {
	runes := []rune(text)
	if len(runes) == 0 {
		return 0
	}
	return (len(runes) + 3) / 4
}

func EncodeImages(images []UploadedImage) []string {
	out := make([]string, 0, len(images))
	for _, image := range images {
		if len(image.Data) > 0 {
			out = append(out, base64.StdEncoding.EncodeToString(image.Data))
		}
	}
	return out
}

type UploadedImage struct {
	Data        []byte
	Filename    string
	ContentType string
}

func AssistantText(event map[string]any, currentText, historyText string) string {
	for _, candidate := range []any{event, event["v"]} {
		m := util.StringMap(candidate)
		message := util.StringMap(m["message"])
		if len(message) == 0 {
			continue
		}
		author := util.StringMap(message["author"])
		if strings.ToLower(util.Clean(author["role"])) != "assistant" {
			continue
		}
		text := AssistantMessageText(message)
		if text != "" {
			return StripHistory(text, historyText)
		}
	}
	return ApplyTextPatch(event, currentText, historyText)
}

func EventAssistantText(event map[string]any, historyText string) string {
	for _, candidate := range []any{event, event["v"]} {
		m := util.StringMap(candidate)
		message := util.StringMap(m["message"])
		author := util.StringMap(message["author"])
		if author["role"] == "assistant" {
			return StripHistory(AssistantMessageText(message), historyText)
		}
	}
	return ""
}

func AssistantMessageText(message map[string]any) string {
	content := util.StringMap(message["content"])
	parts, _ := content["parts"].([]any)
	var out []string
	for _, part := range parts {
		if text, ok := part.(string); ok {
			out = append(out, text)
		}
	}
	return strings.Join(out, "")
}

func StripHistory(text, historyText string) string {
	for historyText != "" && strings.HasPrefix(text, historyText) {
		text = text[len(historyText):]
	}
	return text
}

func ApplyTextPatch(event map[string]any, currentText, historyText string) string {
	if event["p"] == "/message/content/parts/0" {
		return ApplyPatchOp(event, currentText, historyText)
	}
	if value, ok := event["v"].(string); ok && currentText != "" && event["p"] == nil && event["o"] == nil {
		return currentText + value
	}
	if event["o"] == "patch" {
		text := currentText
		for _, raw := range anyList(event["v"]) {
			if op, ok := raw.(map[string]any); ok {
				text = ApplyTextPatch(op, text, historyText)
			}
		}
		return text
	}
	text := currentText
	for _, raw := range anyList(event["v"]) {
		if op, ok := raw.(map[string]any); ok {
			text = ApplyTextPatch(op, text, historyText)
		}
	}
	return text
}

func ApplyPatchOp(operation map[string]any, currentText, historyText string) string {
	value := util.Clean(operation["v"])
	switch operation["o"] {
	case "append":
		return currentText + value
	case "replace":
		return StripHistory(value, historyText)
	default:
		return currentText
	}
}

func UpdateConversationState(state *ConversationState, payload string, event map[string]any) {
	conversationID, fileIDs, sedimentIDs := ExtractConversationIDs(payload)
	if conversationID != "" && state.ConversationID == "" {
		state.ConversationID = conversationID
	}
	if event != nil && IsImageToolEvent(event) {
		state.FileIDs = appendUnique(state.FileIDs, fileIDs...)
		state.SedimentIDs = appendUnique(state.SedimentIDs, sedimentIDs...)
	}
	if event == nil {
		return
	}
	if id := util.Clean(event["conversation_id"]); id != "" {
		state.ConversationID = id
	}
	value := util.StringMap(event["v"])
	if id := util.Clean(value["conversation_id"]); id != "" {
		state.ConversationID = id
	}
	if event["type"] == "moderation" {
		moderation := util.StringMap(event["moderation_response"])
		if moderation["blocked"] == true {
			state.Blocked = true
		}
	}
	if event["type"] == "server_ste_metadata" {
		metadata := util.StringMap(event["metadata"])
		if toolInvoked, ok := metadata["tool_invoked"].(bool); ok {
			state.ToolInvoked = &toolInvoked
		}
		if value := util.Clean(metadata["turn_use_case"]); value != "" {
			state.TurnUseCase = value
		}
	}
}

func ExtractConversationIDs(payload string) (string, []string, []string) {
	conversation := ""
	if match := regexp.MustCompile(`"conversation_id"\s*:\s*"([^"]+)"`).FindStringSubmatch(payload); len(match) > 1 {
		conversation = match[1]
	}
	fileIDs := regexp.MustCompile(`(file[-_][A-Za-z0-9]+)`).FindAllString(payload, -1)
	sedimentMatches := regexp.MustCompile(`sediment://([A-Za-z0-9_-]+)`).FindAllStringSubmatch(payload, -1)
	var sediments []string
	for _, match := range sedimentMatches {
		if len(match) > 1 {
			sediments = append(sediments, match[1])
		}
	}
	return conversation, fileIDs, sediments
}

func IsImageToolEvent(event map[string]any) bool {
	value := util.StringMap(event["v"])
	message := util.StringMap(event["message"])
	if len(message) == 0 {
		message = util.StringMap(value["message"])
	}
	metadata := util.StringMap(message["metadata"])
	author := util.StringMap(message["author"])
	return author["role"] == "tool" && metadata["async_task_type"] == "image_gen"
}

func conversationBaseEvent(eventType string, state *ConversationState) ConversationEvent {
	var tool any
	if state.ToolInvoked != nil {
		tool = *state.ToolInvoked
	}
	return ConversationEvent{
		"type":            eventType,
		"text":            state.Text,
		"conversation_id": state.ConversationID,
		"file_ids":        state.FileIDs,
		"sediment_ids":    state.SedimentIDs,
		"blocked":         state.Blocked,
		"tool_invoked":    tool,
		"turn_use_case":   state.TurnUseCase,
	}
}

func anyList(v any) []any {
	switch list := v.(type) {
	case []any:
		return list
	case []map[string]any:
		out := make([]any, 0, len(list))
		for _, item := range list {
			out = append(out, item)
		}
		return out
	}
	return nil
}

func appendUnique(base []string, values ...string) []string {
	seen := map[string]struct{}{}
	for _, item := range base {
		seen[item] = struct{}{}
	}
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		base = append(base, value)
	}
	return base
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
