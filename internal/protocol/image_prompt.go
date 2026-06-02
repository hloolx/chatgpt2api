package protocol

import (
	"strings"
	"unicode"

	"chatgpt2api/internal/util"
)

func imageGenerationPromptWithDirective(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" || imagePromptHasGenerationDirective(prompt) {
		return prompt
	}
	if imagePromptLooksChinese(prompt) {
		return "生成图片：" + prompt
	}
	return "Generate an image: " + prompt
}

func imagePromptHasGenerationDirective(prompt string) bool {
	normalized := strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(prompt)), " "))
	if normalized == "" {
		return false
	}
	for _, phrase := range []string{
		"生成图片",
		"图片生成",
		"生成一张图",
		"生成一张图片",
		"生成一幅图",
		"生成一幅图片",
		"生成一张照片",
		"生成海报",
		"绘制",
		"画一",
		"画个",
		"画出",
		"画成",
		"帮我画",
		"做一张图",
		"出一张图",
		"出图",
	} {
		if strings.Contains(normalized, phrase) {
			return true
		}
	}
	return englishPromptHasGenerationDirective(englishPromptWords(normalized))
}

func englishPromptHasGenerationDirective(words []string) bool {
	for i, word := range words {
		switch word {
		case "draw", "paint", "render", "illustrate":
			return true
		case "generate", "create", "make", "produce":
			if englishPromptHasImageNounAfter(words, i+1) {
				return true
			}
		}
	}
	return false
}

func englishPromptHasImageNounAfter(words []string, start int) bool {
	for i := start; i < len(words) && i <= start+3; i++ {
		switch words[i] {
		case "a", "an", "the", "one":
			continue
		case "image", "images", "picture", "pictures", "photo", "photos", "artwork", "illustration", "illustrations":
			return true
		default:
			return false
		}
	}
	return false
}

func englishPromptWords(text string) []string {
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte(' ')
	}
	return strings.Fields(b.String())
}

func imagePromptLooksChinese(prompt string) bool {
	han := 0
	for _, r := range prompt {
		if unicode.Is(unicode.Han, r) {
			han++
		}
	}
	return han > 0
}

func imageGenerationFailureError(message string) *ImageGenerationError {
	category := imageFailureCategory(message)
	err := &ImageGenerationError{Message: chineseImageFailureMessage(message), StatusCode: 502, Type: "server_error", Code: "upstream_error"}
	switch category {
	case "safety":
		err.StatusCode = 400
		err.Type = "invalid_request_error"
		err.Code = "content_policy_violation"
	case "quota":
		err.StatusCode = 429
		err.Type = "insufficient_quota"
		err.Code = "insufficient_quota"
	case "challenge":
		err.StatusCode = 403
		err.Type = "invalid_request_error"
		err.Code = "upstream_challenge_required"
	case "network":
		err.StatusCode = 502
		err.Type = "server_error"
		err.Code = "upstream_connection_error"
	case "permission":
		err.StatusCode = 403
		err.Type = "invalid_request_error"
		err.Code = "account_not_authorized"
	case "text":
		err.StatusCode = 400
		err.Type = "invalid_request_error"
		err.Code = "image_generation_text_response"
	}
	return err
}

func imageTextResponseError(message string) *ImageGenerationError {
	return &ImageGenerationError{Message: chineseImageFailureMessage(message), StatusCode: 400, Type: "invalid_request_error", Code: "image_generation_text_response"}
}

func imageFailureCategory(message string) string {
	text := strings.TrimSpace(message)
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "content policy") ||
		strings.Contains(lower, "safety") ||
		strings.Contains(lower, "moderation") ||
		strings.Contains(lower, "blocked") ||
		strings.Contains(lower, "refused") ||
		strings.Contains(text, "违反") ||
		strings.Contains(text, "防护限制") ||
		strings.Contains(text, "安全") ||
		strings.Contains(text, "审核"):
		return "safety"
	case strings.Contains(lower, "image generation limit") ||
		strings.Contains(lower, "insufficient_quota") ||
		strings.Contains(lower, "usage limit") ||
		strings.Contains(lower, "limit reached") ||
		strings.Contains(lower, "no available image quota") ||
		strings.Contains(text, "检测到限流") ||
		strings.Contains(text, "限流") ||
		strings.Contains(text, "额度"):
		return "quota"
	case strings.Contains(lower, "cloudflare challenge") ||
		strings.Contains(lower, "challenge page") ||
		strings.Contains(lower, "challenge_required") ||
		strings.Contains(lower, "cf_chl"):
		return "challenge"
	case strings.Contains(lower, "connection") ||
		strings.Contains(lower, "proxy") ||
		strings.Contains(lower, "tls") ||
		strings.Contains(lower, "timeout") ||
		strings.Contains(lower, "eof") ||
		strings.Contains(lower, "stream") ||
		strings.Contains(lower, "flow control"):
		return "network"
	case strings.Contains(lower, "unauthorized") ||
		strings.Contains(lower, "status=401") ||
		strings.Contains(lower, "status 401") ||
		strings.Contains(lower, "requires plus") ||
		strings.Contains(lower, "plus / team / pro") ||
		strings.Contains(text, "无权访问") ||
		strings.Contains(text, "需要 Plus"):
		return "permission"
	case strings.Contains(lower, "text response") ||
		strings.Contains(lower, "returned a text") ||
		strings.Contains(text, "返回了文字") ||
		strings.Contains(text, "没有返回图片"):
		return "text"
	default:
		return ""
	}
}

func imageGenerationRetryRequest(request ConversationRequest, reason string) ConversationRequest {
	next := request
	next.Prompt = imageGenerationRetryPrompt(request.Prompt, reason)
	next.Messages = replaceLatestUserPrompt(next.Messages, next.Prompt)
	return next.Normalized()
}

func imageGenerationRetryPrompt(prompt, reason string) string {
	prompt = strings.TrimSpace(prompt)
	reason = strings.TrimSpace(reason)
	if imagePromptLooksChinese(prompt) {
		if reason != "" {
			return "请重新生成图片，不要只回复文字。必须调用图片生成工具并返回图片结果。\n上一次失败原因：" + reason + "\n原始需求：" + prompt
		}
		return "请重新生成图片，不要只回复文字。必须调用图片生成工具并返回图片结果。\n原始需求：" + prompt
	}
	if reason != "" {
		return "Generate an image now. Do not answer with text only. You must use the image generation tool and return image data.\nPrevious failure reason: " + reason + "\nOriginal request: " + prompt
	}
	return "Generate an image now. Do not answer with text only. You must use the image generation tool and return image data.\nOriginal request: " + prompt
}

func chineseImageFailureMessage(message string) string {
	text := strings.TrimSpace(message)
	lower := strings.ToLower(text)
	switch {
	case text == "":
		return "图片生成失败：上游没有返回具体错误。"
	case strings.Contains(lower, "content policy") ||
		strings.Contains(lower, "safety") ||
		strings.Contains(lower, "moderation") ||
		strings.Contains(lower, "blocked") ||
		strings.Contains(lower, "refused") ||
		strings.Contains(text, "违反") ||
		strings.Contains(text, "防护限制") ||
		strings.Contains(text, "安全") ||
		strings.Contains(text, "审核"):
		return "图片生成失败：触发安全审核或内容政策限制。" + appendOriginalFailure(text)
	case strings.Contains(lower, "image generation limit") ||
		strings.Contains(lower, "insufficient_quota") ||
		strings.Contains(lower, "usage limit") ||
		strings.Contains(lower, "limit reached") ||
		strings.Contains(lower, "no available image quota") ||
		strings.Contains(text, "限流") ||
		strings.Contains(text, "额度"):
		return "图片生成失败：上游账号限流或图片额度不足。" + appendOriginalFailure(text)
	case strings.Contains(lower, "cloudflare challenge") ||
		strings.Contains(lower, "challenge page") ||
		strings.Contains(lower, "challenge_required") ||
		strings.Contains(lower, "cf_chl"):
		return "图片生成失败：上游返回了风控验证页面，请刷新账号会话或更换代理。" + appendOriginalFailure(text)
	case strings.Contains(lower, "connection") ||
		strings.Contains(lower, "proxy") ||
		strings.Contains(lower, "tls") ||
		strings.Contains(lower, "timeout") ||
		strings.Contains(lower, "eof") ||
		strings.Contains(lower, "stream") ||
		strings.Contains(lower, "flow control"):
		return "图片生成失败：连接上游时出现网络、代理或流式传输错误。" + appendOriginalFailure(text)
	case strings.Contains(lower, "unauthorized") ||
		strings.Contains(lower, "status=401") ||
		strings.Contains(lower, "status 401") ||
		strings.Contains(lower, "requires plus") ||
		strings.Contains(lower, "plus / team / pro") ||
		strings.Contains(text, "无权访问") ||
		strings.Contains(text, "需要 Plus"):
		return "图片生成失败：上游账号无权限或登录凭证失效。" + appendOriginalFailure(text)
	case strings.Contains(lower, "text response") ||
		strings.Contains(lower, "returned a text") ||
		strings.Contains(text, "返回了文字") ||
		strings.Contains(text, "没有返回图片"):
		return "图片生成失败：模型返回了文字，没有返回图片；已自动重试一次仍未成功。" + appendOriginalFailure(text)
	default:
		return "图片生成失败：上游返回错误。" + appendOriginalFailure(text)
	}
}

func imageTextResponseFailureMessage(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "模型返回了文字，没有返回图片。"
	}
	return "模型返回了文字，没有返回图片。返回内容：" + text
}

func appendOriginalFailure(text string) string {
	if text == "" {
		return ""
	}
	return " 原始原因：" + text
}

func replaceLatestUserPrompt(messages []map[string]any, prompt string) []map[string]any {
	if len(messages) == 0 || strings.TrimSpace(prompt) == "" {
		return messages
	}
	out := cloneMessages(messages)
	for index := len(out) - 1; index >= 0; index-- {
		if strings.ToLower(strings.TrimSpace(util.Clean(out[index]["role"]))) != "user" {
			continue
		}
		out[index]["content"] = prompt
		return out
	}
	return out
}
