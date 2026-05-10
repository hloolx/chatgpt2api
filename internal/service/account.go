package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"chatgpt2api/internal/storage"
	"chatgpt2api/internal/util"
)

type AccountConfig interface {
	AutoRemoveInvalidAccounts() bool
	AutoRemoveRateLimitedAccounts() bool
}

type AccountService struct {
	mu                sync.Mutex
	storage           storage.Backend
	config            AccountConfig
	proxy             *ProxyService
	logs              *LogService
	index             int
	items             []map[string]any
	imageReservations map[string]int
	remoteBaseURL     string
	browserHTTPClient func(profile string, timeout time.Duration) *http.Client
}

const (
	defaultRemoteUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/145.0.0.0 Safari/537.36"
	defaultRemoteSecCHUA   = `"Not:A-Brand";v="99", "Google Chrome";v="145", "Chromium";v="145"`
	defaultRemoteProfile   = "chrome145"
)

func NewAccountService(backend storage.Backend, config AccountConfig, proxy *ProxyService, logs *LogService) *AccountService {
	browserHTTPClient := func(profile string, timeout time.Duration) *http.Client {
		if proxy == nil {
			return &http.Client{Timeout: timeout}
		}
		return proxy.BrowserHTTPClientWithProfile(profile, timeout)
	}
	s := &AccountService{
		storage:           backend,
		config:            config,
		proxy:             proxy,
		logs:              logs,
		imageReservations: map[string]int{},
		remoteBaseURL:     "https://chatgpt.com",
		browserHTTPClient: browserHTTPClient,
	}
	s.items = s.loadAccounts()
	return s
}

func (s *AccountService) ListTokens() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.items))
	for _, item := range s.items {
		if token := util.Clean(item["access_token"]); token != "" {
			out = append(out, token)
		}
	}
	return out
}

func (s *AccountService) ListTokensByIDs(ids []string) []string {
	targets := cleanAccountIDs(ids)
	if len(targets) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(targets))
	for _, item := range s.items {
		token := util.Clean(item["access_token"])
		if token == "" {
			continue
		}
		if _, ok := targets[accountIDFromToken(token)]; ok {
			out = append(out, token)
		}
	}
	return out
}

func (s *AccountService) GetTokenByID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.items {
		token := util.Clean(item["access_token"])
		if token != "" && accountIDFromToken(token) == id {
			return token
		}
	}
	return ""
}

func (s *AccountService) ListAccounts() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return publicAccounts(s.items)
}

func (s *AccountService) ListLimitedTokens() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, item := range s.items {
		if item["status"] == "限流" {
			if token := util.Clean(item["access_token"]); token != "" {
				out = append(out, token)
			}
		}
	}
	return out
}

func (s *AccountService) AddAccounts(tokens []string) map[string]any {
	cleaned := cleanTokens(tokens)
	if len(cleaned) == 0 {
		return map[string]any{"added": 0, "skipped": 0, "items": s.ListAccounts()}
	}
	records := make([]map[string]any, 0, len(cleaned))
	for _, token := range cleaned {
		records = append(records, map[string]any{"access_token": token})
	}
	return s.AddAccountRecords(records)
}

func (s *AccountService) AddAccountRecords(records []map[string]any) map[string]any {
	cleaned := cleanAccountRecords(records)
	if len(cleaned) == 0 {
		return map[string]any{"added": 0, "skipped": 0, "items": s.ListAccounts()}
	}
	s.mu.Lock()
	indexed := map[string]map[string]any{}
	order := make([]string, 0, len(s.items)+len(cleaned))
	for _, item := range s.items {
		token := util.Clean(item["access_token"])
		if token == "" {
			continue
		}
		indexed[token] = util.CopyMap(item)
		order = append(order, token)
	}
	added, skipped := 0, 0
	for _, record := range cleaned {
		token := util.Clean(record["access_token"])
		if token == "" {
			continue
		}
		current, ok := indexed[token]
		if ok {
			skipped++
		} else {
			added++
			current = map[string]any{}
			order = append(order, token)
		}
		normalized := normalizeAccount(mergeMaps(current, record, map[string]any{"access_token": token}))
		if normalized != nil {
			indexed[token] = normalized
		}
	}
	next := make([]map[string]any, 0, len(order))
	seen := map[string]struct{}{}
	for _, token := range order {
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		next = append(next, indexed[token])
	}
	s.items = next
	_ = s.saveLocked()
	items := publicAccounts(s.items)
	s.mu.Unlock()
	s.logs.Add(fmt.Sprintf("新增 %d 个账号，跳过 %d 个", added, skipped), map[string]any{
		"module":         "accounts",
		"operation_type": "新增",
		"added":          added,
		"skipped":        skipped,
	})
	return map[string]any{"added": added, "skipped": skipped, "items": items}
}

func (s *AccountService) ListCPAAuthFilesByIDs(ids []string, includeIncomplete bool) ([]map[string]any, []map[string]any) {
	targets := cleanAccountIDs(ids)
	s.mu.Lock()
	defer s.mu.Unlock()

	files := []map[string]any{}
	skipped := []map[string]any{}
	for _, account := range s.items {
		token := util.Clean(account["access_token"])
		if token == "" {
			continue
		}
		localID := accountIDFromToken(token)
		if len(targets) > 0 {
			if _, ok := targets[localID]; !ok {
				continue
			}
		}
		payload, missing := cpaAuthPayloadForAccount(account)
		if payload == nil {
			continue
		}
		name := cpaAuthFileName(account, localID)
		if missing && !includeIncomplete {
			skipped = append(skipped, map[string]any{
				"name":       name,
				"account_id": localID,
				"email":      account["email"],
				"error":      "missing refresh_token, id_token, or account_id",
			})
			continue
		}
		files = append(files, map[string]any{
			"name":                   name,
			"account_id":             localID,
			"email":                  account["email"],
			"payload":                payload,
			"missing_oauth_metadata": missing,
		})
	}
	return files, skipped
}

func (s *AccountService) DeleteAccounts(tokens []string) map[string]any {
	targets := map[string]struct{}{}
	for _, token := range cleanTokens(tokens) {
		targets[token] = struct{}{}
	}
	if len(targets) == 0 {
		return map[string]any{"removed": 0, "items": s.ListAccounts()}
	}
	s.mu.Lock()
	next := s.items[:0]
	removed := 0
	for _, item := range s.items {
		token := util.Clean(item["access_token"])
		if _, ok := targets[token]; ok {
			removed++
			delete(s.imageReservations, token)
			continue
		}
		next = append(next, item)
	}
	s.items = next
	if len(s.items) > 0 {
		s.index %= len(s.items)
	} else {
		s.index = 0
	}
	if removed > 0 {
		_ = s.saveLocked()
	}
	items := publicAccounts(s.items)
	s.mu.Unlock()
	if removed > 0 {
		s.logs.Add(fmt.Sprintf("删除 %d 个账号", removed), map[string]any{
			"module":         "accounts",
			"operation_type": "删除",
			"removed":        removed,
		})
	}
	return map[string]any{"removed": removed, "items": items}
}

func (s *AccountService) RemoveToken(token string) bool {
	return util.ToInt(s.DeleteAccounts([]string{token})["removed"], 0) > 0
}

func (s *AccountService) UpdateAccount(accessToken string, updates map[string]any) map[string]any {
	accessToken = util.Clean(accessToken)
	if accessToken == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.findIndexLocked(accessToken)
	if idx < 0 {
		return nil
	}
	account := normalizeAccount(mergeMaps(s.items[idx], updates, map[string]any{"access_token": accessToken}))
	if account == nil {
		return nil
	}
	if account["status"] == "限流" && s.config.AutoRemoveRateLimitedAccounts() {
		delete(s.imageReservations, accessToken)
		s.items = append(s.items[:idx], s.items[idx+1:]...)
		_ = s.saveLocked()
		s.logs.Add("自动移除限流账号", map[string]any{
			"module":         "accounts",
			"operation_type": "自动移除",
			"token":          util.AnonymizeToken(accessToken),
		})
		return nil
	}
	s.items[idx] = account
	_ = s.saveLocked()
	s.logs.Add("更新账号", map[string]any{
		"module":         "accounts",
		"operation_type": "更新",
		"token":          util.AnonymizeToken(accessToken),
		"status":         account["status"],
	})
	return util.CopyMap(account)
}

func (s *AccountService) GetAccount(accessToken string) map[string]any {
	accessToken = util.Clean(accessToken)
	if accessToken == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.findIndexLocked(accessToken)
	if idx < 0 {
		return nil
	}
	return util.CopyMap(s.items[idx])
}

func (s *AccountService) GetTextAccessToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.items {
		status := util.Clean(item["status"])
		if status != "禁用" && status != "异常" {
			return util.Clean(item["access_token"])
		}
	}
	return ""
}

func (s *AccountService) GetAvailableAccessToken(ctx context.Context) (string, error) {
	return s.GetAvailableAccessTokenFor(ctx, nil)
}

func (s *AccountService) GetAvailableAccessTokenFor(ctx context.Context, allow func(map[string]any) bool) (string, error) {
	attempted := map[string]struct{}{}
	var lastRefreshErr error
	for {
		reservation, err := s.reserveNextCandidateToken(attempted, allow)
		if err != nil {
			if lastRefreshErr != nil {
				return "", lastRefreshErr
			}
			return "", err
		}
		attempted[reservation.token] = struct{}{}
		account, refreshErr := s.RefreshAccountState(ctx, reservation.token)
		if refreshErr != nil {
			lastRefreshErr = refreshErr
		}
		if account != nil && (allow == nil || allow(account)) && s.reservedImageSlotAvailable(reservation) {
			return reservation.token, nil
		}
		s.releaseImageReservation(reservation.token)
	}
}

func (s *AccountService) HasAvailableAccount() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range s.items {
		if s.availableImageSlotsLocked(item) > 0 {
			return true
		}
	}
	return false
}

func (s *AccountService) RefreshAccountState(ctx context.Context, accessToken string) (map[string]any, error) {
	remote, err := s.FetchRemoteInfo(ctx, accessToken)
	if err != nil {
		if _, handled := s.ApplyAccountError(accessToken, "refresh_account_state", err); handled {
			return s.GetAccount(accessToken), nil
		}
		return nil, err
	}
	return s.UpdateAccount(accessToken, remote), nil
}

func (s *AccountService) RefreshAccounts(ctx context.Context, accessTokens []string) map[string]any {
	tokens := cleanTokens(accessTokens)
	if len(tokens) == 0 {
		return map[string]any{
			"refreshed":   0,
			"errors":      []map[string]string{},
			"results":     []map[string]any{},
			"total":       0,
			"failed":      0,
			"duration_ms": 0,
			"items":       s.ListAccounts(),
		}
	}
	startedAt := time.Now()
	type result struct {
		token    string
		info     map[string]any
		err      error
		duration time.Duration
	}
	workers := len(tokens)
	if workers > 10 {
		workers = 10
	}
	jobs := make(chan string)
	results := make(chan result, len(tokens))
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for token := range jobs {
				started := time.Now()
				info, err := s.FetchRemoteInfo(ctx, token)
				results <- result{token: token, info: info, err: err, duration: time.Since(started)}
			}
		}()
	}
	go func() {
		for _, token := range tokens {
			jobs <- token
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()
	resultsByToken := make(map[string]result, len(tokens))
	for res := range results {
		resultsByToken[res.token] = res
	}

	refreshed := 0
	errors := []map[string]string{}
	details := make([]map[string]any, 0, len(tokens))
	for _, token := range tokens {
		res := resultsByToken[token]
		detail := map[string]any{
			"account_id":    accountIDFromToken(token),
			"access_token":  token,
			"token_preview": util.AnonymizeToken(token),
			"success":       false,
			"status":        "error",
			"duration_ms":   res.duration.Milliseconds(),
		}
		if res.err == nil {
			updated := s.UpdateAccount(res.token, res.info)
			if updated != nil {
				refreshed++
				detail["account_status"] = updated["status"]
				detail["email"] = updated["email"]
				detail["type"] = updated["type"]
				detail["quota"] = updated["quota"]
				detail["image_quota_unknown"] = updated["image_quota_unknown"]
				detail["restore_at"] = updated["restore_at"]
				detail["message"] = "刷新成功"
			} else {
				detail["message"] = "刷新完成，账号状态已自动处理"
			}
			detail["success"] = true
			detail["status"] = "success"
			details = append(details, detail)
			continue
		}
		message := res.err.Error()
		if normalized, handled := s.ApplyAccountError(res.token, "refresh_accounts", res.err); handled {
			message = normalized
		}
		if current := s.GetAccount(res.token); current != nil {
			detail["account_status"] = current["status"]
			detail["email"] = current["email"]
			detail["type"] = current["type"]
			detail["quota"] = current["quota"]
			detail["image_quota_unknown"] = current["image_quota_unknown"]
			detail["restore_at"] = current["restore_at"]
		}
		errorItem := map[string]string{
			"account_id":   accountIDFromToken(res.token),
			"access_token": res.token,
			"error":        message,
		}
		errors = append(errors, errorItem)
		detail["message"] = message
		detail["error"] = message
		details = append(details, detail)
	}
	return map[string]any{
		"refreshed":   refreshed,
		"errors":      errors,
		"results":     details,
		"total":       len(tokens),
		"failed":      len(errors),
		"duration_ms": time.Since(startedAt).Milliseconds(),
		"items":       s.ListAccounts(),
	}
}

func (s *AccountService) MarkImageResult(accessToken string, success bool) map[string]any {
	accessToken = util.Clean(accessToken)
	if accessToken == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releaseImageReservationLocked(accessToken)
	idx := s.findIndexLocked(accessToken)
	if idx < 0 {
		return nil
	}
	next := util.CopyMap(s.items[idx])
	next["last_used_at"] = util.NowLocal()
	unknown := util.ToBool(next["image_quota_unknown"])
	if success {
		next["success"] = util.ToInt(next["success"], 0) + 1
		if !unknown {
			quota := util.ToInt(next["quota"], 0) - 1
			if quota < 0 {
				quota = 0
			}
			next["quota"] = quota
			if quota == 0 {
				next["status"] = "限流"
				if _, ok := next["restore_at"]; !ok {
					next["restore_at"] = nil
				}
			} else if next["status"] == "限流" {
				next["status"] = "正常"
			}
		}
	} else {
		next["fail"] = util.ToInt(next["fail"], 0) + 1
	}
	account := normalizeAccount(next)
	if account == nil {
		return nil
	}
	if account["status"] == "限流" && s.config.AutoRemoveRateLimitedAccounts() {
		delete(s.imageReservations, accessToken)
		s.items = append(s.items[:idx], s.items[idx+1:]...)
		_ = s.saveLocked()
		s.logs.Add("自动移除限流账号", map[string]any{
			"module":         "accounts",
			"operation_type": "自动移除",
			"token":          util.AnonymizeToken(accessToken),
		})
		return nil
	}
	s.items[idx] = account
	_ = s.saveLocked()
	return util.CopyMap(account)
}

func (s *AccountService) RemoveInvalidToken(accessToken, event string) bool {
	if !s.config.AutoRemoveInvalidAccounts() {
		return false
	}
	removed := s.RemoveToken(accessToken)
	if removed {
		s.logs.Add("自动移除异常账号", map[string]any{
			"module":         "accounts",
			"operation_type": "自动移除",
			"source":         event,
			"token":          util.AnonymizeToken(accessToken),
		})
	}
	return removed
}

func (s *AccountService) ApplyAccountError(accessToken, event string, err error) (string, bool) {
	if err == nil {
		return "", false
	}
	return s.ApplyAccountErrorMessage(accessToken, event, err.Error())
}

func (s *AccountService) ApplyAccountErrorMessage(accessToken, event, message string) (string, bool) {
	if IsAccountInvalidErrorMessage(message) {
		if !s.RemoveInvalidToken(accessToken, event) {
			s.UpdateAccount(accessToken, map[string]any{"status": "异常", "quota": 0, "image_quota_unknown": false})
		}
		return "检测到封号", true
	}
	if IsAccountRateLimitedErrorMessage(message) {
		s.UpdateAccount(accessToken, map[string]any{"status": "限流", "quota": 0, "image_quota_unknown": false})
		return "检测到限流", true
	}
	return message, false
}

func (s *AccountService) FetchRemoteInfo(ctx context.Context, accessToken string) (map[string]any, error) {
	accessToken = util.Clean(accessToken)
	if accessToken == "" {
		return nil, fmt.Errorf("access_token is required")
	}
	baseURL := strings.TrimRight(firstNonEmpty(s.remoteBaseURL, "https://chatgpt.com"), "/")
	headers := s.remoteHeaders(accessToken)
	client := s.browserHTTPClient(s.remoteImpersonation(accessToken), 30*time.Second)
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	if err := s.bootstrapRemote(ctx, client, baseURL, accessToken); err != nil {
		return nil, err
	}
	type response struct {
		payload map[string]any
		err     error
	}
	fetch := func(method, urlPath string, body any, extra map[string]string) response {
		var reader io.Reader
		if body != nil {
			data, _ := json.Marshal(body)
			reader = bytes.NewReader(data)
		}
		req, _ := http.NewRequestWithContext(ctx, method, baseURL+urlPath, reader)
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		req.Header.Set("x-openai-target-path", urlPath)
		req.Header.Set("x-openai-target-route", urlPath)
		for key, value := range extra {
			req.Header.Set(key, value)
		}
		resp, err := client.Do(req)
		if err != nil {
			return response{err: err}
		}
		defer resp.Body.Close()
		data, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return response{err: readErr}
		}
		if resp.StatusCode != http.StatusOK {
			return response{err: refreshHTTPError(urlPath, resp.StatusCode, data)}
		}
		var payload map[string]any
		if err := json.Unmarshal(data, &payload); err != nil {
			return response{err: err}
		}
		return response{payload: payload}
	}
	me := fetch(http.MethodGet, "/backend-api/me", nil, nil)
	if me.err != nil {
		return nil, me.err
	}
	init := fetch(http.MethodPost, "/backend-api/conversation/init", map[string]any{
		"gizmo_id": nil, "requested_default_model": nil, "conversation_id": nil, "timezone_offset_min": -480,
	}, nil)
	if init.err != nil {
		return nil, init.err
	}
	limits := anyList(init.payload["limits_progress"])
	accountType := s.detectAccountType(accessToken, me.payload, init.payload)
	quota, restoreAt, unknown := extractQuotaAndRestoreAt(limits)
	chatGPTAccountID := firstNonEmpty(
		chatGPTAccountIDFromPayload(decodeAccessTokenPayload(accessToken)),
		util.Clean(me.payload["chatgpt_account_id"]),
		util.Clean(me.payload["account_id"]),
		util.Clean(me.payload["id"]),
	)
	status := "正常"
	if !unknown && quota == 0 {
		status = "限流"
	}
	return map[string]any{
		"email":               me.payload["email"],
		"user_id":             me.payload["id"],
		"chatgpt_account_id":  chatGPTAccountID,
		"type":                accountType,
		"quota":               quota,
		"image_quota_unknown": unknown,
		"limits_progress":     limits,
		"default_model_slug":  init.payload["default_model_slug"],
		"restore_at":          restoreAt,
		"status":              status,
	}, nil
}

func (s *AccountService) bootstrapRemote(ctx context.Context, client *http.Client, baseURL, accessToken string) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/", nil)
	for key, value := range s.remoteBootstrapHeaders(accessToken) {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return refreshHTTPError("bootstrap", resp.StatusCode, data)
	}
	return nil
}

func (s *AccountService) StartLimitedWatcher(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	go func() {
		timer := time.NewTimer(0)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				tokens := s.ListLimitedTokens()
				if len(tokens) > 0 {
					s.RefreshAccounts(ctx, tokens)
				}
				timer.Reset(interval)
			}
		}
	}()
}

type imageTokenReservation struct {
	token string
	slot  int
}

func (s *AccountService) reserveNextCandidateToken(excluded map[string]struct{}, allow func(map[string]any) bool) (imageTokenReservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var tokens []string
	for _, item := range s.items {
		token := util.Clean(item["access_token"])
		if token == "" {
			continue
		}
		if _, ok := excluded[token]; ok {
			continue
		}
		if allow != nil && !allow(item) {
			continue
		}
		if s.availableImageSlotsLocked(item) > 0 {
			tokens = append(tokens, token)
		}
	}
	if len(tokens) == 0 {
		return imageTokenReservation{}, fmt.Errorf("no available image quota")
	}
	token := tokens[s.index%len(tokens)]
	s.index++
	s.ensureImageReservationsLocked()
	s.imageReservations[token]++
	return imageTokenReservation{token: token, slot: s.imageReservations[token]}, nil
}

func (s *AccountService) reservedImageSlotAvailable(reservation imageTokenReservation) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.findIndexLocked(reservation.token)
	if idx < 0 {
		return false
	}
	return reservation.slot > 0 && reservation.slot <= imageAccountCapacity(s.items[idx])
}

func (s *AccountService) availableImageSlotsLocked(account map[string]any) int {
	capacity := imageAccountCapacity(account)
	if capacity <= 0 {
		return 0
	}
	token := util.Clean(account["access_token"])
	if token == "" {
		return 0
	}
	inFlight := s.imageReservations[token]
	if inFlight >= capacity {
		return 0
	}
	return capacity - inFlight
}

func (s *AccountService) releaseImageReservation(accessToken string) {
	accessToken = util.Clean(accessToken)
	if accessToken == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releaseImageReservationLocked(accessToken)
}

func (s *AccountService) releaseImageReservationLocked(accessToken string) {
	s.ensureImageReservationsLocked()
	count := s.imageReservations[accessToken]
	if count <= 1 {
		delete(s.imageReservations, accessToken)
		return
	}
	s.imageReservations[accessToken] = count - 1
}

func (s *AccountService) ensureImageReservationsLocked() {
	if s.imageReservations == nil {
		s.imageReservations = map[string]int{}
	}
}

func imageAccountCapacity(account map[string]any) int {
	if !IsImageAccountAvailable(account) {
		return 0
	}
	if util.ToBool(account["image_quota_unknown"]) {
		return 1
	}
	return util.ToInt(account["quota"], 0)
}

func (s *AccountService) findIndexLocked(accessToken string) int {
	for index, item := range s.items {
		if util.Clean(item["access_token"]) == accessToken {
			return index
		}
	}
	return -1
}

func (s *AccountService) loadAccounts() []map[string]any {
	items, err := s.storage.LoadAccounts()
	if err != nil {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if normalized := normalizeAccount(item); normalized != nil {
			out = append(out, normalized)
		}
	}
	return out
}

func (s *AccountService) saveLocked() error {
	return s.storage.SaveAccounts(s.items)
}

func (s *AccountService) remoteHeaders(accessToken string) map[string]string {
	account := s.GetAccount(accessToken)
	clean := func(keys ...string) string {
		for _, key := range keys {
			if raw, ok := account["fp"].(map[string]any); ok {
				if value := util.Clean(raw[key]); value != "" {
					return value
				}
			}
			if value := util.Clean(account[key]); value != "" {
				return value
			}
		}
		return ""
	}
	headers := map[string]string{
		"authorization":      "Bearer " + accessToken,
		"accept":             "*/*",
		"accept-language":    "zh-CN,zh;q=0.9,en;q=0.8",
		"content-type":       "application/json",
		"oai-language":       "zh-CN",
		"origin":             "https://chatgpt.com",
		"referer":            "https://chatgpt.com/",
		"sec-fetch-dest":     "empty",
		"sec-fetch-mode":     "cors",
		"sec-fetch-site":     "same-origin",
		"user-agent":         firstNonEmpty(clean("user-agent", "user_agent"), defaultRemoteUserAgent),
		"sec-ch-ua":          firstNonEmpty(clean("sec-ch-ua"), defaultRemoteSecCHUA),
		"sec-ch-ua-mobile":   firstNonEmpty(clean("sec-ch-ua-mobile"), "?0"),
		"sec-ch-ua-platform": firstNonEmpty(clean("sec-ch-ua-platform"), `"Windows"`),
	}
	if deviceID := clean("oai-device-id", "oai_device_id"); deviceID != "" {
		headers["oai-device-id"] = deviceID
	}
	if sessionID := clean("oai-session-id", "oai_session_id"); sessionID != "" {
		headers["oai-session-id"] = sessionID
	}
	return headers
}

func (s *AccountService) remoteBootstrapHeaders(accessToken string) map[string]string {
	account := s.GetAccount(accessToken)
	clean := func(keys ...string) string {
		for _, key := range keys {
			if raw, ok := account["fp"].(map[string]any); ok {
				if value := util.Clean(raw[key]); value != "" {
					return value
				}
			}
			if value := util.Clean(account[key]); value != "" {
				return value
			}
		}
		return ""
	}
	return map[string]string{
		"user-agent":                firstNonEmpty(clean("user-agent", "user_agent"), defaultRemoteUserAgent),
		"accept":                    "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
		"accept-language":           "zh-CN,zh;q=0.9,en;q=0.8",
		"sec-ch-ua":                 firstNonEmpty(clean("sec-ch-ua"), defaultRemoteSecCHUA),
		"sec-ch-ua-mobile":          firstNonEmpty(clean("sec-ch-ua-mobile"), "?0"),
		"sec-ch-ua-platform":        firstNonEmpty(clean("sec-ch-ua-platform"), `"Windows"`),
		"sec-fetch-dest":            "document",
		"sec-fetch-mode":            "navigate",
		"sec-fetch-site":            "none",
		"sec-fetch-user":            "?1",
		"upgrade-insecure-requests": "1",
	}
}

func (s *AccountService) remoteImpersonation(accessToken string) string {
	account := s.GetAccount(accessToken)
	if raw, ok := account["fp"].(map[string]any); ok {
		if value := util.Clean(raw["impersonate"]); value != "" {
			return value
		}
	}
	return firstNonEmpty(util.Clean(account["impersonate"]), defaultRemoteProfile)
}

func (s *AccountService) detectAccountType(accessToken string, mePayload, initPayload map[string]any) string {
	tokenPayload := decodeAccessTokenPayload(accessToken)
	if authPayload, ok := tokenPayload["https://api.openai.com/auth"].(map[string]any); ok {
		if matched := normalizeAccountType(authPayload["chatgpt_plan_type"]); matched != "" {
			return matched
		}
	}
	for _, payload := range []any{mePayload, initPayload, tokenPayload} {
		if matched := searchAccountType(payload); matched != "" {
			return matched
		}
	}
	return "Free"
}

func IsImageAccountAvailable(account map[string]any) bool {
	if account == nil {
		return false
	}
	status := util.Clean(account["status"])
	if status == "禁用" || status == "限流" || status == "异常" {
		return false
	}
	if util.ToBool(account["image_quota_unknown"]) {
		return true
	}
	return util.ToInt(account["quota"], 0) > 0
}

func IsPaidImageAccount(account map[string]any) bool {
	switch util.Clean(account["type"]) {
	case "Plus", "ProLite", "Pro", "Team":
		return true
	default:
		return false
	}
}

func IsAccountInvalidErrorMessage(message string) bool {
	text := strings.ToLower(strings.TrimSpace(message))
	if text == "" || isBootstrapErrorMessage(text) {
		return false
	}
	return strings.Contains(text, "token_invalidated") ||
		strings.Contains(text, "token_revoked") ||
		strings.Contains(text, "authentication token has been invalidated") ||
		strings.Contains(text, "invalidated oauth token")
}

func IsAccountRateLimitedErrorMessage(message string) bool {
	text := strings.ToLower(strings.TrimSpace(message))
	if text == "" || isBootstrapErrorMessage(text) {
		return false
	}
	if strings.Contains(text, "insufficient_quota") ||
		strings.Contains(text, "limit reached") ||
		strings.Contains(text, "usage limit") ||
		strings.Contains(text, "image generation limit") ||
		strings.Contains(text, "you've reached") ||
		strings.Contains(text, "you have reached") ||
		strings.Contains(text, "限流") ||
		strings.Contains(text, "额度已用尽") ||
		strings.Contains(text, "生成上限") ||
		strings.Contains(text, "已达上限") {
		return true
	}
	return false
}

func normalizeAccount(item map[string]any) map[string]any {
	if item == nil {
		return nil
	}
	accessToken := util.Clean(item["access_token"])
	if accessToken == "" {
		return nil
	}
	normalized := util.CopyMap(item)
	normalized["access_token"] = accessToken
	if authType := util.Clean(normalized["auth_type"]); authType != "" {
		normalized["auth_type"] = authType
	} else if providerType := authProviderType(normalized["type"]); providerType != "" {
		normalized["auth_type"] = providerType
	}
	planType := normalizeAccountType(normalized["type"])
	if planType == "" {
		planType = firstNonEmpty(
			normalizeAccountType(normalized["plan_type"]),
			normalizeAccountType(normalized["account_type"]),
			normalizeAccountType(normalized["chatgpt_plan_type"]),
		)
	}
	if planType == "" {
		planType = "Free"
	}
	normalized["type"] = planType
	normalized["status"] = firstNonEmpty(util.Clean(normalized["status"]), "正常")
	quota := util.ToInt(normalized["quota"], 0)
	if quota < 0 {
		quota = 0
	}
	normalized["quota"] = quota
	normalized["image_quota_unknown"] = util.ToBool(normalized["image_quota_unknown"])
	if email := util.Clean(normalized["email"]); email != "" {
		normalized["email"] = email
	} else {
		normalized["email"] = nil
	}
	if userID := util.Clean(normalized["user_id"]); userID != "" {
		normalized["user_id"] = userID
	} else {
		normalized["user_id"] = nil
	}
	if accountID := util.Clean(normalized["chatgpt_account_id"]); accountID != "" {
		normalized["chatgpt_account_id"] = accountID
	} else if accountID := util.Clean(normalized["account_id"]); accountID != "" {
		normalized["chatgpt_account_id"] = accountID
	} else if accountID := chatGPTAccountIDFromPayload(decodeAccessTokenPayload(accessToken)); accountID != "" {
		normalized["chatgpt_account_id"] = accountID
	} else {
		normalized["chatgpt_account_id"] = nil
	}
	if accountID := firstNonEmpty(util.Clean(normalized["account_id"]), util.Clean(normalized["chatgpt_account_id"])); accountID != "" {
		normalized["account_id"] = accountID
	} else {
		normalized["account_id"] = nil
	}
	for _, key := range []string{"refresh_token", "id_token", "last_refresh", "cpa_file_name"} {
		if value := util.Clean(normalized[key]); value != "" {
			normalized[key] = value
		} else {
			delete(normalized, key)
		}
	}
	if _, ok := normalized["expired"]; ok {
		normalized["expired"] = util.ToBool(normalized["expired"])
	}
	if _, ok := normalized["disabled"]; ok {
		normalized["disabled"] = util.ToBool(normalized["disabled"])
	}
	limits := anyList(normalized["limits_progress"])
	normalized["limits_progress"] = limits
	if model := util.Clean(normalized["default_model_slug"]); model != "" {
		normalized["default_model_slug"] = model
	} else {
		normalized["default_model_slug"] = nil
	}
	if restore := util.Clean(normalized["restore_at"]); restore != "" {
		normalized["restore_at"] = restore
	} else {
		normalized["restore_at"] = nil
	}
	normalized["success"] = util.ToInt(normalized["success"], 0)
	normalized["fail"] = util.ToInt(normalized["fail"], 0)
	return normalized
}

func publicAccounts(accounts []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(accounts))
	for _, account := range accounts {
		token := util.Clean(account["access_token"])
		if token == "" {
			continue
		}
		out = append(out, map[string]any{
			"id":                 accountIDFromToken(token),
			"token_preview":      util.AnonymizeToken(token),
			"access_token":       token,
			"type":               util.ValueOr(account["type"], "Free"),
			"status":             util.ValueOr(account["status"], "正常"),
			"quota":              util.ValueOr(account["quota"], 0),
			"imageQuotaUnknown":  util.ToBool(account["image_quota_unknown"]),
			"email":              account["email"],
			"user_id":            account["user_id"],
			"chatgpt_account_id": account["chatgpt_account_id"],
			"limits_progress":    util.ValueOr(account["limits_progress"], []any{}),
			"default_model_slug": account["default_model_slug"],
			"restoreAt":          account["restore_at"],
			"success":            util.ToInt(account["success"], 0),
			"fail":               util.ToInt(account["fail"], 0),
			"lastUsedAt":         account["last_used_at"],
			"hasRefreshToken":    util.Clean(account["refresh_token"]) != "",
			"hasIdToken":         util.Clean(account["id_token"]) != "",
			"cpaExportReady":     cpaOAuthMetadataComplete(account),
		})
	}
	return out
}

func accountIDFromToken(token string) string {
	return util.SHA1Short(token, 16)
}

func cleanAccountIDs(ids []string) map[string]struct{} {
	out := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			out[id] = struct{}{}
		}
	}
	return out
}

func cleanTokens(tokens []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(tokens))
	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		out = append(out, token)
	}
	return out
}

func cleanAccountRecords(records []map[string]any) []map[string]any {
	indexed := map[string]map[string]any{}
	order := make([]string, 0, len(records))
	for _, record := range records {
		normalized := normalizeAccountImportRecord(record)
		if normalized == nil {
			continue
		}
		token := util.Clean(normalized["access_token"])
		if token == "" {
			continue
		}
		if _, ok := indexed[token]; !ok {
			order = append(order, token)
		}
		indexed[token] = normalizeAccount(mergeMaps(indexed[token], normalized))
	}
	out := make([]map[string]any, 0, len(order))
	for _, token := range order {
		if record := indexed[token]; record != nil {
			out = append(out, record)
		}
	}
	return out
}

func normalizeAccountImportRecord(record map[string]any) map[string]any {
	if record == nil {
		return nil
	}
	next := util.CopyMap(record)
	delete(next, "password")
	if accessToken := firstNonEmpty(util.Clean(next["access_token"]), util.Clean(next["accessToken"])); accessToken != "" {
		next["access_token"] = accessToken
	}
	if refreshToken := firstNonEmpty(util.Clean(next["refresh_token"]), util.Clean(next["refreshToken"])); refreshToken != "" {
		next["refresh_token"] = refreshToken
	}
	idToken := firstNonEmpty(util.Clean(next["id_token"]), util.Clean(next["idToken"]))
	if idToken != "" {
		next["id_token"] = idToken
	}
	if accountID := firstNonEmpty(
		util.Clean(next["account_id"]),
		util.Clean(next["accountId"]),
		util.Clean(next["chatgpt_account_id"]),
		chatGPTAccountIDFromPayload(decodeAccessTokenPayload(idToken)),
	); accountID != "" {
		next["account_id"] = accountID
		next["chatgpt_account_id"] = accountID
	}
	if lastRefresh := firstNonEmpty(util.Clean(next["last_refresh"]), util.Clean(next["lastRefresh"]), util.Clean(next["lastRefreshedAt"])); lastRefresh != "" {
		next["last_refresh"] = lastRefresh
	}
	if providerType := authProviderType(next["type"]); providerType != "" {
		next["auth_type"] = providerType
		if plan := firstNonEmpty(normalizeAccountType(next["plan_type"]), normalizeAccountType(next["account_type"]), normalizeAccountType(next["chatgpt_plan_type"])); plan != "" {
			next["type"] = plan
		} else {
			delete(next, "type")
		}
	}
	return normalizeAccount(next)
}

func decodeAccessTokenPayload(accessToken string) map[string]any {
	parts := strings.Split(util.Clean(accessToken), ".")
	if len(parts) < 2 {
		return map[string]any{}
	}
	payload := parts[1]
	data, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		data, err = base64.URLEncoding.DecodeString(payload)
	}
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if json.Unmarshal(data, &out) != nil {
		return map[string]any{}
	}
	return out
}

func chatGPTAccountIDFromPayload(payload map[string]any) string {
	if len(payload) == 0 {
		return ""
	}
	if accountID := util.Clean(payload["chatgpt_account_id"]); accountID != "" {
		return accountID
	}
	if accountID := util.Clean(payload["account_id"]); accountID != "" {
		return accountID
	}
	if authPayload := util.StringMap(payload["https://api.openai.com/auth"]); authPayload != nil {
		if accountID := util.Clean(authPayload["chatgpt_account_id"]); accountID != "" {
			return accountID
		}
	}
	return ""
}

func authProviderType(value any) string {
	switch strings.ToLower(util.Clean(value)) {
	case "codex", "openai", "anthropic", "claude", "gemini", "qwen", "iflow", "antigravity":
		return strings.ToLower(util.Clean(value))
	default:
		return ""
	}
}

func cpaAuthPayloadForAccount(account map[string]any) (map[string]any, bool) {
	if account == nil {
		return nil, false
	}
	accessToken := util.Clean(account["access_token"])
	if accessToken == "" {
		return nil, false
	}
	idToken := util.Clean(account["id_token"])
	accountID := firstNonEmpty(
		util.Clean(account["account_id"]),
		util.Clean(account["chatgpt_account_id"]),
		chatGPTAccountIDFromPayload(decodeAccessTokenPayload(idToken)),
		chatGPTAccountIDFromPayload(decodeAccessTokenPayload(accessToken)),
	)
	payload := map[string]any{
		"type":         firstNonEmpty(util.Clean(account["auth_type"]), "codex"),
		"access_token": accessToken,
		"disabled":     util.ToBool(account["disabled"]) || util.Clean(account["status"]) == "禁用",
		"expired":      util.ToBool(account["expired"]),
	}
	if refreshToken := util.Clean(account["refresh_token"]); refreshToken != "" {
		payload["refresh_token"] = refreshToken
	}
	if idToken != "" {
		payload["id_token"] = idToken
	}
	if accountID != "" {
		payload["account_id"] = accountID
		payload["chatgpt_account_id"] = accountID
	}
	if email := util.Clean(account["email"]); email != "" {
		payload["email"] = email
	}
	if lastRefresh := util.Clean(account["last_refresh"]); lastRefresh != "" {
		payload["last_refresh"] = lastRefresh
	}
	if planType := normalizeAccountType(account["type"]); planType != "" {
		payload["plan_type"] = planType
	}
	metadata := map[string]any{
		"source":           "chatgpt2api",
		"local_account_id": accountIDFromToken(accessToken),
		"exported_at":      util.NowISO(),
	}
	if accountID != "" {
		metadata["account_id"] = accountID
		metadata["chatgpt_account_id"] = accountID
	}
	payload["metadata"] = metadata
	return payload, !cpaOAuthMetadataComplete(account)
}

func cpaOAuthMetadataComplete(account map[string]any) bool {
	if account == nil {
		return false
	}
	accessToken := util.Clean(account["access_token"])
	if accessToken == "" {
		return false
	}
	accountID := firstNonEmpty(
		util.Clean(account["account_id"]),
		util.Clean(account["chatgpt_account_id"]),
		chatGPTAccountIDFromPayload(decodeAccessTokenPayload(util.Clean(account["id_token"]))),
		chatGPTAccountIDFromPayload(decodeAccessTokenPayload(accessToken)),
	)
	return util.Clean(account["refresh_token"]) != "" && util.Clean(account["id_token"]) != "" && accountID != ""
}

func cpaAuthFileName(account map[string]any, localID string) string {
	base := firstNonEmpty(util.Clean(account["email"]), util.Clean(account["account_id"]), util.Clean(account["chatgpt_account_id"]), localID)
	base = strings.TrimSuffix(base, ".json")
	base = strings.TrimSpace(base)
	if base == "" {
		base = "account-" + util.NewHex(8)
	}
	base = sanitizeAuthFileNamePart(base)
	if !strings.HasPrefix(strings.ToLower(base), "codex-") {
		base = "codex-" + base
	}
	if len(base) > 120 {
		base = base[:120]
	}
	return base + ".json"
}

func sanitizeAuthFileNamePart(value string) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		allowed := r >= 'a' && r <= 'z' ||
			r >= 'A' && r <= 'Z' ||
			r >= '0' && r <= '9' ||
			r == '@' || r == '.' || r == '_' || r == '-'
		if allowed {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "._- ")
	if out == "" {
		return "account"
	}
	return out
}

func normalizeAccountType(value any) string {
	switch strings.ToLower(util.Clean(value)) {
	case "free":
		return "Free"
	case "plus", "personal":
		return "Plus"
	case "prolite", "pro_lite":
		return "ProLite"
	case "team", "business", "enterprise":
		return "Team"
	case "pro":
		return "Pro"
	default:
		return ""
	}
}

func searchAccountType(value any) string {
	switch x := value.(type) {
	case map[string]any:
		for key, item := range x {
			keyText := strings.ToLower(util.Clean(key))
			if strings.Contains(keyText, "plan") || strings.Contains(keyText, "type") || strings.Contains(keyText, "subscription") || strings.Contains(keyText, "workspace") || strings.Contains(keyText, "tier") {
				if matched := normalizeAccountType(item); matched != "" {
					return matched
				}
				if matched := searchAccountType(item); matched != "" {
					return matched
				}
			}
		}
	case []any:
		for _, item := range x {
			if matched := searchAccountType(item); matched != "" {
				return matched
			}
		}
	}
	return ""
}

func extractQuotaAndRestoreAt(limits []any) (int, any, bool) {
	for _, raw := range limits {
		item, ok := raw.(map[string]any)
		if !ok || item["feature_name"] != "image_gen" {
			continue
		}
		restore := any(nil)
		if value := util.Clean(item["reset_after"]); value != "" {
			restore = value
		}
		return util.ToInt(item["remaining"], 0), restore, false
	}
	return 0, nil, true
}

func anyList(value any) []any {
	if list, ok := value.([]any); ok {
		return list
	}
	if list, ok := value.([]map[string]any); ok {
		out := make([]any, len(list))
		for i, item := range list {
			out[i] = item
		}
		return out
	}
	return []any{}
}

func mergeMaps(items ...map[string]any) map[string]any {
	out := map[string]any{}
	for _, item := range items {
		for key, value := range item {
			out[key] = value
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func isBootstrapErrorMessage(message string) bool {
	return strings.HasPrefix(strings.TrimSpace(message), "bootstrap failed")
}

func refreshHTTPError(context string, status int, body []byte) error {
	detail := summarizeRefreshErrorBody(body)
	if detail == "" {
		return fmt.Errorf("%s failed: HTTP %d", context, status)
	}
	return fmt.Errorf("%s failed: HTTP %d, %s", context, status, detail)
}

func summarizeRefreshErrorBody(body []byte) string {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return ""
	}
	var payload any
	if json.Unmarshal(body, &payload) == nil {
		if detail := summarizeRefreshErrorValue(payload); detail != "" {
			return detail
		}
	}
	lower := strings.ToLower(text)
	if strings.Contains(lower, "cf_chl") ||
		strings.Contains(lower, "challenge-platform") ||
		strings.Contains(lower, "enable javascript and cookies to continue") ||
		strings.Contains(lower, "cloudflare") {
		return "upstream returned Cloudflare challenge page; refresh browser fingerprint/session or change proxy"
	}
	if strings.Contains(lower, "<html") || strings.Contains(lower, "<!doctype html") || strings.Contains(lower, "<body") {
		return "upstream returned HTML error page"
	}
	const maxBodyDetail = 2048
	if len(text) > maxBodyDetail {
		return "body=" + text[:maxBodyDetail] + "...(truncated)"
	}
	return "body=" + text
}

func summarizeRefreshErrorValue(value any) string {
	switch item := value.(type) {
	case map[string]any:
		for _, key := range []string{"detail", "message", "error_description"} {
			if detail := summarizeRefreshErrorValue(item[key]); detail != "" {
				return detail
			}
		}
		if detail := summarizeRefreshErrorValue(item["error"]); detail != "" {
			return detail
		}
		if data, err := json.Marshal(item); err == nil && len(data) > 0 {
			return "body=" + string(data)
		}
	case []any:
		if len(item) == 0 {
			return ""
		}
		if detail := summarizeRefreshErrorValue(item[0]); detail != "" {
			return detail
		}
	case string:
		text := strings.TrimSpace(item)
		if text != "" {
			return "body=" + text
		}
	}
	return ""
}
