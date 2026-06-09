package backend

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"chatgpt2api/internal/service"
	"chatgpt2api/internal/util"
)

type nParamProxyConfig struct {
	proxy string
}

func (c nParamProxyConfig) Proxy() string {
	return c.proxy
}

// TestNParamSequential 测试顺序调用模式：
// 使用同一个 token，串行调用 n 次，每次等前一次完成后才开始下一次。
func TestNParamSequential(t *testing.T) {
	token := strings.TrimSpace(os.Getenv("CHATGPT2API_DIAG_ACCESS_TOKEN"))
	if token == "" {
		t.Skip("CHATGPT2API_DIAG_ACCESS_TOKEN is not set")
	}
	proxy := strings.TrimSpace(os.Getenv("CHATGPT2API_DIAG_PROXY"))
	n := intFromEnv("CHATGPT2API_DIAG_N", 2)
	model := firstNonEmpty(strings.TrimSpace(os.Getenv("CHATGPT2API_DIAG_MODEL")), util.ImageModelGPT)
	prompt := firstNonEmpty(strings.TrimSpace(os.Getenv("CHATGPT2API_DIAG_PROMPT")), "一只可爱的猫咪在草地上晒太阳，油画风格")

	client := NewClient(token, nil, service.NewProxyService(nParamProxyConfig{proxy: proxy}))

	t.Logf("=== 顺序调用测试 ===")
	t.Logf("token=%s, model=%s, n=%d, prompt=%s", maskToken(token), model, n, prompt)

	start := time.Now()
	successCount := 0
	var totalImages int
	var totalDuration time.Duration

	for i := 1; i <= n; i++ {
		iterStart := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)

		events, errCh := client.StreamResponsesImage(ctx, ResponsesImageRequest{
			Prompt: prompt,
			Model:  model,
		})

		imagesForCall := 0
		conversationID := ""
		hasError := false

		for event := range events {
			if event.HasImageResult() {
				imagesForCall++
				totalImages++
			}
			if strings.TrimSpace(event.ConversationID) != "" {
				conversationID = strings.TrimSpace(event.ConversationID)
			}
		}

		err := <-errCh
		cancel()
		elapsed := time.Since(iterStart)

		if err != nil {
			hasError = true
			t.Logf("[顺序 #%d/%d] 失败: %v (耗时 %v)", i, n, err, elapsed.Round(time.Millisecond))
		} else {
			successCount++
			totalDuration += elapsed
			t.Logf("[顺序 #%d/%d] 成功: %d 张图片, conversation=%s (耗时 %v)", i, n, imagesForCall, conversationID, elapsed.Round(time.Millisecond))
		}

		if hasError && i < n {
			// 失败后短暂等待再试下一次
			select {
			case <-time.After(2 * time.Second):
			case <-ctx.Done():
			}
		}
	}

	totalTime := time.Since(start)
	t.Logf("=== 顺序调用结果: 成功=%d/%d, 总图片=%d, 总耗时=%v, 平均成功耗时=%v ===",
		successCount, n, totalImages, totalTime.Round(time.Millisecond),
		func() time.Duration {
			if successCount == 0 {
				return 0
			}
			return totalDuration / time.Duration(successCount)
		}())
}

// TestNParamParallel 测试并行调用模式：
// 使用多个不同 token（从环境变量中逗号分隔），并发调用 n 次。
func TestNParamParallel(t *testing.T) {
	tokenList := strings.TrimSpace(os.Getenv("CHATGPT2API_DIAG_ACCESS_TOKENS"))
	if tokenList == "" {
		// 回退到单 token 环境变量
		single := strings.TrimSpace(os.Getenv("CHATGPT2API_DIAG_ACCESS_TOKEN"))
		if single == "" {
			t.Skip("CHATGPT2API_DIAG_ACCESS_TOKENS or CHATGPT2API_DIAG_ACCESS_TOKEN is not set")
		}
		tokenList = single
	}
	tokens := splitAndClean(tokenList)
	if len(tokens) == 0 {
		t.Skip("no tokens available")
	}
	proxy := strings.TrimSpace(os.Getenv("CHATGPT2API_DIAG_PROXY"))
	n := intFromEnv("CHATGPT2API_DIAG_N", 2)
	model := firstNonEmpty(strings.TrimSpace(os.Getenv("CHATGPT2API_DIAG_MODEL")), util.ImageModelGPT)
	prompt := firstNonEmpty(strings.TrimSpace(os.Getenv("CHATGPT2API_DIAG_PROMPT")), "一只可爱的猫咪在草地上晒太阳，油画风格")

	t.Logf("=== 并行调用测试 ===")
	t.Logf("tokens=%d, model=%s, n=%d, prompt=%s", len(tokens), model, n, prompt)

	start := time.Now()
	var wg sync.WaitGroup

	type callResult struct {
		Index          int
		Success        bool
		Images         int
		ConversationID string
		Duration       time.Duration
		Error          string
		Token          string
	}

	results := make([]callResult, n)
	var totalImages int32

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			iterStart := time.Now()

			// 轮询使用 token 池中的 token
			token := tokens[idx%len(tokens)]
			client := NewClient(token, nil, service.NewProxyService(nParamProxyConfig{proxy: proxy}))

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			events, errCh := client.StreamResponsesImage(ctx, ResponsesImageRequest{
				Prompt: prompt,
				Model:  model,
			})

			var imagesForCall int32
			var conversationID string
			for event := range events {
				if event.HasImageResult() {
					imagesForCall++
				}
				if strings.TrimSpace(event.ConversationID) != "" {
					conversationID = strings.TrimSpace(event.ConversationID)
				}
			}

			err := <-errCh
			elapsed := time.Since(iterStart)

			results[idx] = callResult{
				Index:          idx + 1,
				Success:        err == nil,
				Images:         int(imagesForCall),
				ConversationID: conversationID,
				Duration:       elapsed,
				Error: func() string {
					if err != nil {
						return err.Error()
					}
					return ""
				}(),
				Token: maskToken(token),
			}
			atomic.AddInt32(&totalImages, imagesForCall)
		}(i)
	}

	wg.Wait()
	totalTime := time.Since(start)

	successCount := 0
	var totalSuccessDuration time.Duration
	for _, r := range results {
		if r.Success {
			successCount++
			totalSuccessDuration += r.Duration
			t.Logf("[并行 #%d] 成功: %d 张图片, token=%s, conversation=%s (耗时 %v)",
				r.Index, r.Images, r.Token, r.ConversationID, r.Duration.Round(time.Millisecond))
		} else {
			t.Logf("[并行 #%d] 失败: token=%s, err=%s (耗时 %v)",
				r.Index, r.Token, r.Error, r.Duration.Round(time.Millisecond))
		}
	}

	t.Logf("=== 并行调用结果: 成功=%d/%d, 总图片=%d, 总耗时=%v, 平均成功耗时=%v ===",
		successCount, n, atomic.LoadInt32(&totalImages), totalTime.Round(time.Millisecond),
		func() time.Duration {
			if successCount == 0 {
				return 0
			}
			return totalSuccessDuration / time.Duration(successCount)
		}())
}

// TestNParamComparison 同时运行顺序和并行两种模式并对比结果
func TestNParamComparison(t *testing.T) {
	tokenList := strings.TrimSpace(os.Getenv("CHATGPT2API_DIAG_ACCESS_TOKENS"))
	singleToken := strings.TrimSpace(os.Getenv("CHATGPT2API_DIAG_ACCESS_TOKEN"))
	if tokenList == "" && singleToken == "" {
		t.Skip("CHATGPT2API_DIAG_ACCESS_TOKENS or CHATGPT2API_DIAG_ACCESS_TOKEN is not set")
	}
	if tokenList == "" {
		tokenList = singleToken
	}
	tokens := splitAndClean(tokenList)
	if len(tokens) == 0 {
		t.Skip("no tokens available")
	}

	proxy := strings.TrimSpace(os.Getenv("CHATGPT2API_DIAG_PROXY"))
	n := intFromEnv("CHATGPT2API_DIAG_N", 2)
	model := firstNonEmpty(strings.TrimSpace(os.Getenv("CHATGPT2API_DIAG_MODEL")), util.ImageModelGPT)
	prompt := firstNonEmpty(strings.TrimSpace(os.Getenv("CHATGPT2API_DIAG_PROMPT")), "一只可爱的猫咪在草地上晒太阳，油画风格")

	t.Logf("=== n=%d 顺序 vs 并行 对比测试 ===", n)
	t.Logf("可用 token 数=%d, model=%s", len(tokens), model)

	// ---- 顺序模式 ----
	t.Log(">> 开始顺序调用...")
	seqStart := time.Now()
	seqSuccess := 0
	seqImages := 0
	var seqDurations []time.Duration

	for i := 1; i <= n; i++ {
		iterStart := time.Now()
		client := NewClient(tokens[0], nil, service.NewProxyService(nParamProxyConfig{proxy: proxy}))
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)

		events, errCh := client.StreamResponsesImage(ctx, ResponsesImageRequest{
			Prompt: prompt,
			Model:  model,
		})

		imagesForCall := 0
		for range events {
			imagesForCall++
		}
		err := <-errCh
		cancel()
		elapsed := time.Since(iterStart)

		if err != nil {
			t.Logf("  [顺序 #%d] 失败: %v (耗时 %v)", i, err, elapsed.Round(time.Millisecond))
		} else {
			seqSuccess++
			seqImages += imagesForCall
			seqDurations = append(seqDurations, elapsed)
			t.Logf("  [顺序 #%d] 成功: %d events (耗时 %v)", i, imagesForCall, elapsed.Round(time.Millisecond))
		}
	}
	seqTotal := time.Since(seqStart)

	// ---- 并行模式 ----
	t.Log(">> 开始并行调用...")
	parStart := time.Now()
	var parWg sync.WaitGroup

	type parResult struct {
		Index    int
		Success  bool
		Events   int
		Duration time.Duration
		Error    string
	}

	parResults := make([]parResult, n)

	for i := 0; i < n; i++ {
		parWg.Add(1)
		go func(idx int) {
			defer parWg.Done()
			iterStart := time.Now()
			token := tokens[idx%len(tokens)]
			client := NewClient(token, nil, service.NewProxyService(nParamProxyConfig{proxy: proxy}))
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			events, errCh := client.StreamResponsesImage(ctx, ResponsesImageRequest{
				Prompt: prompt,
				Model:  model,
			})

			count := 0
			for range events {
				count++
			}
			err := <-errCh
			elapsed := time.Since(iterStart)

			parResults[idx] = parResult{
				Index:    idx + 1,
				Success:  err == nil,
				Events:   count,
				Duration: elapsed,
				Error: func() string {
					if err != nil {
						return err.Error()
					}
					return ""
				}(),
			}
		}(i)
	}
	parWg.Wait()
	parTotal := time.Since(parStart)

	parSuccess := 0
	parEvents := 0
	for _, r := range parResults {
		if r.Success {
			parSuccess++
			parEvents += r.Events
			t.Logf("  [并行 #%d] 成功: %d events (耗时 %v)", r.Index, r.Events, r.Duration.Round(time.Millisecond))
		} else {
			t.Logf("  [并行 #%d] 失败: %s (耗时 %v)", r.Index, r.Error, r.Duration.Round(time.Millisecond))
		}
	}

	// ---- 对比总结 ----
	t.Log("")
	t.Log("========== 对比总结 ==========")
	t.Logf("顺序模式: 成功=%d/%d, 总事件=%d, 总耗时=%v",
		seqSuccess, n, seqImages, seqTotal.Round(time.Millisecond))
	t.Logf("并行模式: 成功=%d/%d, 总事件=%d, 总耗时=%v",
		parSuccess, n, parEvents, parTotal.Round(time.Millisecond))

	if seqSuccess > 0 && parSuccess > 0 {
		speedup := float64(seqTotal) / float64(parTotal)
		t.Logf("加速比: %.2fx (顺序耗时/并行耗时)", speedup)
	}
	t.Log("==============================")
}

// TestNParamDiagnostic 单次诊断测试（和现有 image_diag_test.go 相同模式），
// 但额外输出 token 使用情况和详细的 timing。
func TestNParamDiagnostic(t *testing.T) {
	token := strings.TrimSpace(os.Getenv("CHATGPT2API_DIAG_ACCESS_TOKEN"))
	if token == "" {
		t.Skip("CHATGPT2API_DIAG_ACCESS_TOKEN is not set")
	}
	proxy := strings.TrimSpace(os.Getenv("CHATGPT2API_DIAG_PROXY"))
	prompt := firstNonEmpty(strings.TrimSpace(os.Getenv("CHATGPT2API_DIAG_PROMPT")), "你好，你是什么模型？")
	model := firstNonEmpty(strings.TrimSpace(os.Getenv("CHATGPT2API_DIAG_MODEL")), util.ImageModelGPT)

	client := NewClient(token, nil, service.NewProxyService(nParamProxyConfig{proxy: proxy}))
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	t.Logf("token=%s, model=%s, prompt=%s", maskToken(token), model, prompt)

	events, errCh := client.StreamResponsesImage(ctx, ResponsesImageRequest{
		Prompt: prompt,
		Model:  model,
	})

	count := 0
	imageCount := 0
	conversationID := ""
	start := time.Now()

	for event := range events {
		count++
		if event.HasImageResult() {
			imageCount++
		}
		if strings.TrimSpace(event.ConversationID) != "" {
			conversationID = strings.TrimSpace(event.ConversationID)
		}
		raw, _ := json.Marshal(event.Raw)
		rawText := string(raw)
		text := strings.TrimSpace(event.Text)
		if len([]rune(text)) > 120 {
			text = string([]rune(text)[:120])
		}
		summary := map[string]any{
			"type":                event.Type,
			"has_conversation_id": strings.TrimSpace(event.ConversationID) != "",
			"has_result":          event.HasImageResult(),
			"text":                text,
			"blocked":             event.Blocked,
			"tool_invoked": func() any {
				if event.ToolInvoked != nil {
					return *event.ToolInvoked
				}
				return nil
			}(),
			"turn_use_case": event.TurnUseCase,
		}
		encoded, _ := json.Marshal(summary)
		t.Logf("event[%d]=%s", count, encoded)

		// 检测 raw 中的关键信号
		if strings.Contains(rawText, "asset_pointer") {
			t.Logf("  -> raw 包含 asset_pointer")
		}
		if strings.Contains(rawText, "image_asset_pointer") {
			t.Logf("  -> raw 包含 image_asset_pointer")
		}
		if strings.Contains(rawText, "image_gen") {
			t.Logf("  -> raw 包含 async_image_gen")
		}
	}

	err := <-errCh
	elapsed := time.Since(start)

	if err != nil {
		t.Logf("stream_error=%v", err)
	}
	t.Logf("total_events=%d, images=%d, conversation=%s, duration=%v",
		count, imageCount, conversationID, elapsed.Round(time.Millisecond))
}

func maskToken(token string) string {
	if len(token) <= 8 {
		return token[:min(len(token), 4)] + "***"
	}
	return token[:4] + "***" + token[len(token)-4:]
}

func intFromEnv(key string, defaultVal int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return defaultVal
	}
	return n
}

func splitAndClean(s string) []string {
	parts := strings.Split(s, ",")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
