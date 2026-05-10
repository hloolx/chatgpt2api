package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"chatgpt2api/internal/storage"
	"chatgpt2api/internal/util"
)

type CPAConfig struct {
	mu      sync.Mutex
	path    string
	store   storage.JSONDocumentBackend
	pools   []map[string]any
	docName string
}

type CPAImportService struct {
	config   *CPAConfig
	accounts *AccountService
	proxy    *ProxyService
}

func NewCPAConfig(dataDir string, backend ...storage.Backend) *CPAConfig {
	c := &CPAConfig{path: filepath.Join(dataDir, "cpa_config.json"), store: firstJSONDocumentStore(backend), docName: "cpa_config.json"}
	c.pools = c.load()
	return c
}

func NewCPAImportService(config *CPAConfig, accounts *AccountService, proxy *ProxyService) *CPAImportService {
	return &CPAImportService{config: config, accounts: accounts, proxy: proxy}
}

func (c *CPAConfig) ListPools() []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return copyMaps(c.pools)
}

func (c *CPAConfig) GetPool(id string) map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, pool := range c.pools {
		if pool["id"] == id {
			return util.CopyMap(pool)
		}
	}
	return nil
}

func (c *CPAConfig) AddPool(name, baseURL, secretKey string) map[string]any {
	pool := normalizeCPAPool(map[string]any{"id": util.NewHex(12), "name": name, "base_url": baseURL, "secret_key": secretKey})
	c.mu.Lock()
	c.pools = append(c.pools, pool)
	_ = c.saveLocked()
	c.mu.Unlock()
	return util.CopyMap(pool)
}

func (c *CPAConfig) UpdatePool(id string, updates map[string]any) map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	for index, pool := range c.pools {
		if pool["id"] != id {
			continue
		}
		merged := mergeMaps(pool, updates, map[string]any{"id": id})
		c.pools[index] = normalizeCPAPool(merged)
		_ = c.saveLocked()
		return util.CopyMap(c.pools[index])
	}
	return nil
}

func (c *CPAConfig) DeletePool(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	next := c.pools[:0]
	removed := false
	for _, pool := range c.pools {
		if pool["id"] == id {
			removed = true
			continue
		}
		next = append(next, pool)
	}
	if removed {
		c.pools = next
		_ = c.saveLocked()
	}
	return removed
}

func (c *CPAConfig) SetImportJob(id string, job map[string]any) map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	for index, pool := range c.pools {
		if pool["id"] != id {
			continue
		}
		next := util.CopyMap(pool)
		next["import_job"] = normalizeImportJob(job, false)
		c.pools[index] = next
		_ = c.saveLocked()
		return util.CopyMap(next)
	}
	return nil
}

func (c *CPAConfig) GetImportJob(id string) map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, pool := range c.pools {
		if pool["id"] == id {
			if job, ok := pool["import_job"].(map[string]any); ok {
				return util.CopyMap(job)
			}
		}
	}
	return nil
}

func (c *CPAConfig) SetExportJob(id string, job map[string]any) map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	for index, pool := range c.pools {
		if pool["id"] != id {
			continue
		}
		next := util.CopyMap(pool)
		next["export_job"] = normalizeExportJob(job, false)
		c.pools[index] = next
		_ = c.saveLocked()
		return util.CopyMap(next)
	}
	return nil
}

func (c *CPAConfig) GetExportJob(id string) map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, pool := range c.pools {
		if pool["id"] == id {
			if job, ok := pool["export_job"].(map[string]any); ok {
				return util.CopyMap(job)
			}
		}
	}
	return nil
}

func (c *CPAConfig) load() []map[string]any {
	raw := loadStoredJSON(c.store, c.docName, c.path)
	if obj, ok := raw.(map[string]any); ok && obj["base_url"] != nil {
		pool := normalizeCPAPool(obj)
		if util.Clean(pool["base_url"]) != "" {
			return []map[string]any{pool}
		}
		return nil
	}
	var pools []map[string]any
	for _, item := range anyList(raw) {
		if pool, ok := item.(map[string]any); ok {
			pools = append(pools, normalizeCPAPool(pool))
		}
	}
	return pools
}

func (c *CPAConfig) saveLocked() error {
	return saveStoredJSON(c.store, c.docName, c.path, c.pools)
}

func (s *CPAImportService) ListRemoteFiles(ctx context.Context, pool map[string]any) ([]map[string]any, error) {
	baseURL := util.Clean(pool["base_url"])
	secret := util.Clean(pool["secret_key"])
	if baseURL == "" || secret == "" {
		return []map[string]any{}, nil
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/v0/management/auth-files", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Accept", "application/json")
	resp, err := s.proxy.HTTPClient(30 * time.Second).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("remote list failed: HTTP %d", resp.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	var items []map[string]any
	for _, raw := range anyList(payload["files"]) {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name := util.Clean(item["name"])
		if name == "" {
			continue
		}
		items = append(items, map[string]any{"name": name, "email": firstNonEmpty(util.Clean(item["email"]), util.Clean(item["account"]))})
	}
	return items, nil
}

func (s *CPAImportService) StartImport(pool map[string]any, selected []string) (map[string]any, error) {
	var names []string
	for _, name := range selected {
		if value := strings.TrimSpace(name); value != "" {
			names = append(names, value)
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("selected files is required")
	}
	poolID := util.Clean(pool["id"])
	job := newImportJob(len(names))
	saved := s.config.SetImportJob(poolID, job)
	if saved == nil {
		return nil, fmt.Errorf("pool not found")
	}
	go s.runImport(poolID, pool, names)
	return util.CopyMap(saved["import_job"].(map[string]any)), nil
}

func (s *CPAImportService) StartExport(pool map[string]any, accountIDs []string, includeIncomplete bool) (map[string]any, error) {
	files, skipped := s.accounts.ListCPAAuthFilesByIDs(accountIDs, includeIncomplete)
	if len(files) == 0 {
		if len(skipped) > 0 {
			return nil, fmt.Errorf("no exportable accounts; selected accounts are missing refresh_token, id_token, or account_id")
		}
		return nil, fmt.Errorf("no accounts to export")
	}
	poolID := util.Clean(pool["id"])
	job := newExportJob(len(files) + len(skipped))
	if len(skipped) > 0 {
		errors := anyList(job["errors"])
		for _, item := range skipped {
			errors = append(errors, map[string]any{
				"name":  item["name"],
				"error": item["error"],
			})
		}
		job["errors"] = errors
		job["failed"] = len(errors)
		job["completed"] = len(skipped)
	}
	saved := s.config.SetExportJob(poolID, job)
	if saved == nil {
		return nil, fmt.Errorf("pool not found")
	}
	go s.runExport(poolID, pool, files)
	return util.CopyMap(saved["export_job"].(map[string]any)), nil
}

func (s *CPAImportService) runExport(poolID string, pool map[string]any, files []map[string]any) {
	s.updateExportJob(poolID, map[string]any{"status": "running"})
	type result struct {
		name  string
		err   string
		warn  bool
		email string
	}
	results := make(chan result, len(files))
	workers := minInt(8, maxInt(1, len(files)))
	jobs := make(chan map[string]any)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for file := range jobs {
				name := util.Clean(file["name"])
				payload, _ := file["payload"].(map[string]any)
				err := s.uploadAuthFile(context.Background(), pool, name, payload)
				if err != nil {
					results <- result{name: name, err: err.Error()}
					continue
				}
				results <- result{name: name, warn: util.ToBool(file["missing_oauth_metadata"]), email: util.Clean(file["email"])}
			}
		}()
	}
	go func() {
		for _, file := range files {
			jobs <- file
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()
	exported := 0
	missingOAuth := 0
	for res := range results {
		if res.err != "" {
			s.appendExportJobError(poolID, res.name, res.err)
		} else {
			exported++
			if res.warn {
				missingOAuth++
			}
		}
		current := s.config.GetExportJob(poolID)
		s.updateExportJob(poolID, map[string]any{
			"completed":     util.ToInt(current["completed"], 0) + 1,
			"exported":      exported,
			"missing_oauth": missingOAuth,
			"failed":        len(anyList(current["errors"])),
		})
	}
	current := s.config.GetExportJob(poolID)
	status := "completed"
	if exported == 0 && len(files) > 0 {
		status = "failed"
	}
	s.updateExportJob(poolID, map[string]any{"status": status, "completed": util.ToInt(current["total"], 0), "exported": exported, "missing_oauth": missingOAuth, "failed": len(anyList(current["errors"]))})
}

func (s *CPAImportService) runImport(poolID string, pool map[string]any, names []string) {
	s.updateJob(poolID, map[string]any{"status": "running"})
	type result struct {
		name   string
		record map[string]any
		err    string
	}
	results := make(chan result, len(names))
	workers := minInt(16, maxInt(1, len(names)))
	jobs := make(chan string)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for name := range jobs {
				record, err := s.fetchRemoteAuthFile(context.Background(), pool, name)
				if err != nil {
					results <- result{name: name, err: err.Error()}
				} else {
					results <- result{name: name, record: record}
				}
			}
		}()
	}
	go func() {
		for _, name := range names {
			jobs <- name
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()
	var records []map[string]any
	var tokens []string
	for res := range results {
		if res.record != nil {
			records = append(records, res.record)
			if token := util.Clean(res.record["access_token"]); token != "" {
				tokens = append(tokens, token)
			}
		} else {
			s.appendJobError(poolID, res.name, res.err)
		}
		current := s.config.GetImportJob(poolID)
		s.updateJob(poolID, map[string]any{"completed": util.ToInt(current["completed"], 0) + 1, "failed": len(anyList(current["errors"]))})
	}
	if len(records) == 0 {
		current := s.config.GetImportJob(poolID)
		s.updateJob(poolID, map[string]any{"status": "failed", "completed": util.ToInt(current["total"], 0), "failed": len(anyList(current["errors"]))})
		return
	}
	add := s.accounts.AddAccountRecords(records)
	refresh := s.accounts.RefreshAccounts(context.Background(), tokens)
	current := s.config.GetImportJob(poolID)
	s.updateJob(poolID, map[string]any{"status": "completed", "completed": len(names), "added": util.ToInt(add["added"], 0), "skipped": util.ToInt(add["skipped"], 0), "refreshed": util.ToInt(refresh["refreshed"], 0), "failed": len(anyList(current["errors"]))})
}

func (s *CPAImportService) fetchRemoteAuthFile(ctx context.Context, pool map[string]any, name string) (map[string]any, error) {
	baseURL := util.Clean(pool["base_url"])
	secret := util.Clean(pool["secret_key"])
	if baseURL == "" || secret == "" || strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("invalid request")
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/v0/management/auth-files/download?name="+urlQuery(name), nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Accept", "application/json")
	resp, err := s.proxy.HTTPClient(30 * time.Second).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	token := util.Clean(payload["access_token"])
	if token == "" {
		return nil, fmt.Errorf("missing access_token")
	}
	payload["access_token"] = token
	payload["cpa_file_name"] = name
	if providerType := authProviderType(payload["type"]); providerType != "" {
		payload["auth_type"] = providerType
	}
	return payload, nil
}

func (s *CPAImportService) uploadAuthFile(ctx context.Context, pool map[string]any, name string, payload map[string]any) error {
	baseURL := util.Clean(pool["base_url"])
	secret := util.Clean(pool["secret_key"])
	name = strings.TrimSpace(name)
	if baseURL == "" || secret == "" || name == "" || payload == nil {
		return fmt.Errorf("invalid request")
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/v0/management/auth-files?name="+urlQuery(name), bytes.NewReader(data))
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.proxy.HTTPClient(30 * time.Second).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		if detail := strings.TrimSpace(string(body)); detail != "" {
			return fmt.Errorf("HTTP %d: %s", resp.StatusCode, detail)
		}
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

func (s *CPAImportService) updateJob(poolID string, updates map[string]any) {
	current := s.config.GetImportJob(poolID)
	if current == nil {
		return
	}
	for key, value := range updates {
		current[key] = value
	}
	current["updated_at"] = util.NowISO()
	s.config.SetImportJob(poolID, current)
}

func (s *CPAImportService) appendJobError(poolID, name, message string) {
	current := s.config.GetImportJob(poolID)
	if current == nil {
		return
	}
	errors := anyList(current["errors"])
	errors = append(errors, map[string]any{"name": name, "error": message})
	s.updateJob(poolID, map[string]any{"errors": errors, "failed": len(errors)})
}

func (s *CPAImportService) updateExportJob(poolID string, updates map[string]any) {
	current := s.config.GetExportJob(poolID)
	if current == nil {
		return
	}
	for key, value := range updates {
		current[key] = value
	}
	current["updated_at"] = util.NowISO()
	s.config.SetExportJob(poolID, current)
}

func (s *CPAImportService) appendExportJobError(poolID, name, message string) {
	current := s.config.GetExportJob(poolID)
	if current == nil {
		return
	}
	errors := anyList(current["errors"])
	errors = append(errors, map[string]any{"name": name, "error": message})
	s.updateExportJob(poolID, map[string]any{"errors": errors, "failed": len(errors)})
}

func normalizeCPAPool(raw map[string]any) map[string]any {
	return map[string]any{"id": firstNonEmpty(util.Clean(raw["id"]), util.NewHex(12)), "name": util.Clean(raw["name"]), "base_url": util.Clean(raw["base_url"]), "secret_key": util.Clean(raw["secret_key"]), "import_job": normalizeImportJob(raw["import_job"], true), "export_job": normalizeExportJob(raw["export_job"], true)}
}

func normalizeImportJob(raw any, failUnfinished bool) map[string]any {
	item, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	status := firstNonEmpty(util.Clean(item["status"]), "failed")
	if failUnfinished && (status == "pending" || status == "running") {
		status = "failed"
	}
	return map[string]any{"job_id": firstNonEmpty(util.Clean(item["job_id"]), util.NewHex(32)), "status": status, "created_at": firstNonEmpty(util.Clean(item["created_at"]), util.NowISO()), "updated_at": firstNonEmpty(util.Clean(item["updated_at"]), util.Clean(item["created_at"]), util.NowISO()), "total": util.ToInt(item["total"], 0), "completed": util.ToInt(item["completed"], 0), "added": util.ToInt(item["added"], 0), "skipped": util.ToInt(item["skipped"], 0), "refreshed": util.ToInt(item["refreshed"], 0), "failed": util.ToInt(item["failed"], 0), "errors": anyList(item["errors"])}
}

func newImportJob(total int) map[string]any {
	return map[string]any{"job_id": util.NewHex(32), "status": "pending", "created_at": util.NowISO(), "updated_at": util.NowISO(), "total": total, "completed": 0, "added": 0, "skipped": 0, "refreshed": 0, "failed": 0, "errors": []any{}}
}

func normalizeExportJob(raw any, failUnfinished bool) map[string]any {
	item, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	status := firstNonEmpty(util.Clean(item["status"]), "failed")
	if failUnfinished && (status == "pending" || status == "running") {
		status = "failed"
	}
	return map[string]any{"job_id": firstNonEmpty(util.Clean(item["job_id"]), util.NewHex(32)), "status": status, "created_at": firstNonEmpty(util.Clean(item["created_at"]), util.NowISO()), "updated_at": firstNonEmpty(util.Clean(item["updated_at"]), util.Clean(item["created_at"]), util.NowISO()), "total": util.ToInt(item["total"], 0), "completed": util.ToInt(item["completed"], 0), "exported": util.ToInt(item["exported"], 0), "missing_oauth": util.ToInt(item["missing_oauth"], 0), "failed": util.ToInt(item["failed"], 0), "errors": anyList(item["errors"])}
}

func newExportJob(total int) map[string]any {
	return map[string]any{"job_id": util.NewHex(32), "status": "pending", "created_at": util.NowISO(), "updated_at": util.NowISO(), "total": total, "completed": 0, "exported": 0, "missing_oauth": 0, "failed": 0, "errors": []any{}}
}

func copyMaps(items []map[string]any) []map[string]any {
	out := make([]map[string]any, len(items))
	for i, item := range items {
		out[i] = util.CopyMap(item)
	}
	return out
}

func urlQuery(value string) string {
	return url.QueryEscape(value)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
