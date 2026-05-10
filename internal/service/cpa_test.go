package service

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"chatgpt2api/internal/storage"
	"chatgpt2api/internal/util"
)

func TestAddAccountRecordsPreservesCPAOAuthMetadata(t *testing.T) {
	accounts := newTestAccountService(t)

	result := accounts.AddAccountRecords([]map[string]any{{
		"type":          "codex",
		"access_token":  "access-token",
		"refresh_token": "refresh-token",
		"id_token":      "id-token",
		"account_id":    "acct-123",
		"email":         "user@example.com",
		"account_type":  "plus",
		"last_refresh":  "2026-05-10T06:00:00Z",
		"password":      "should-not-be-stored",
	}})
	if result["added"] != 1 || result["skipped"] != 0 {
		t.Fatalf("AddAccountRecords() = %#v, want added 1 skipped 0", result)
	}

	account := accounts.GetAccount("access-token")
	if account["auth_type"] != "codex" || account["type"] != "Plus" {
		t.Fatalf("account type fields = %#v", account)
	}
	for _, key := range []string{"refresh_token", "id_token", "account_id", "email", "last_refresh"} {
		if util.Clean(account[key]) == "" {
			t.Fatalf("account missing %s: %#v", key, account)
		}
	}
	if _, ok := account["password"]; ok {
		t.Fatalf("account stored password: %#v", account)
	}

	public := accounts.ListAccounts()
	if len(public) != 1 || public[0]["hasRefreshToken"] != true || public[0]["hasIdToken"] != true || public[0]["cpaExportReady"] != true {
		t.Fatalf("public account metadata = %#v", public)
	}
	if _, ok := public[0]["refresh_token"]; ok {
		t.Fatalf("public account leaked refresh token: %#v", public[0])
	}

	files, skipped := accounts.ListCPAAuthFilesByIDs(nil, false)
	if len(skipped) != 0 || len(files) != 1 {
		t.Fatalf("CPA auth files = %#v skipped %#v, want one file and no skipped", files, skipped)
	}
	payload := files[0]["payload"].(map[string]any)
	metadata := payload["metadata"].(map[string]any)
	if payload["type"] != "codex" ||
		payload["refresh_token"] != "refresh-token" ||
		payload["id_token"] != "id-token" ||
		payload["account_id"] != "acct-123" ||
		payload["chatgpt_account_id"] != "acct-123" ||
		metadata["account_id"] != "acct-123" {
		t.Fatalf("CPA payload = %#v, want full codex auth metadata", payload)
	}
}

func TestAddAccountRecordsDerivesCPAAccountIDFromIDToken(t *testing.T) {
	accounts := newTestAccountService(t)
	idToken := testUnsignedJWT(t, map[string]any{
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acct-from-id-token",
		},
	})

	result := accounts.AddAccountRecords([]map[string]any{{
		"type":          "codex",
		"access_token":  "access-from-id-token",
		"refresh_token": "refresh-from-id-token",
		"id_token":      idToken,
		"email":         "id-token@example.com",
	}})
	if result["added"] != 1 || result["skipped"] != 0 {
		t.Fatalf("AddAccountRecords() = %#v, want added 1 skipped 0", result)
	}

	account := accounts.GetAccount("access-from-id-token")
	if account["account_id"] != "acct-from-id-token" || account["chatgpt_account_id"] != "acct-from-id-token" {
		t.Fatalf("account ids = %#v, want account id from id_token", account)
	}

	public := accounts.ListAccounts()
	if len(public) != 1 || public[0]["cpaExportReady"] != true {
		t.Fatalf("public account metadata = %#v, want CPA export ready", public)
	}

	files, skipped := accounts.ListCPAAuthFilesByIDs(nil, false)
	if len(skipped) != 0 || len(files) != 1 {
		t.Fatalf("CPA auth files = %#v skipped %#v, want one file and no skipped", files, skipped)
	}
	payload := files[0]["payload"].(map[string]any)
	metadata := payload["metadata"].(map[string]any)
	if payload["account_id"] != "acct-from-id-token" ||
		payload["chatgpt_account_id"] != "acct-from-id-token" ||
		metadata["account_id"] != "acct-from-id-token" {
		t.Fatalf("CPA payload = %#v, want account id derived from id_token", payload)
	}
}

func TestCPAImportPreservesRemoteAuthFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/management/auth-files/download" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		writeJSON(t, w, map[string]any{
			"type":          "codex",
			"access_token":  "remote-access",
			"refresh_token": "remote-refresh",
			"id_token":      "remote-id",
			"account_id":    "remote-account",
			"email":         "remote@example.com",
		})
	}))
	defer server.Close()

	dir := t.TempDir()
	backend := storage.NewJSONBackend(filepath.Join(dir, "accounts.json"), filepath.Join(dir, "auth_keys.json"))
	accounts := NewAccountService(backend, testAccountConfig{}, NewProxyService(testAccountConfig{}), NewLogService(dir))
	quotaServer := newAccountQuotaServer(t, map[string]any{"email": "remote@example.com", "id": "remote-user"}, []map[string]any{{
		"feature_name": "image_gen",
		"remaining":    1,
	}})
	defer quotaServer.Close()
	accounts.remoteBaseURL = quotaServer.URL
	accounts.browserHTTPClient = func(string, time.Duration) *http.Client {
		return quotaServer.Client()
	}
	config := NewCPAConfig(dir, backend)
	pool := config.AddPool("remote", server.URL, "secret")
	cpa := NewCPAImportService(config, accounts, NewProxyService(testAccountConfig{}))

	if _, err := cpa.StartImport(pool, []string{"remote.json"}); err != nil {
		t.Fatalf("StartImport() error = %v", err)
	}
	waitForCPAJob(t, func() map[string]any { return config.GetImportJob(util.Clean(pool["id"])) })

	account := accounts.GetAccount("remote-access")
	if account == nil {
		t.Fatal("imported account not found")
	}
	if account["auth_type"] != "codex" || account["refresh_token"] != "remote-refresh" || account["id_token"] != "remote-id" || account["account_id"] != "remote-account" {
		t.Fatalf("imported account = %#v, want preserved CPA metadata", account)
	}
}

func TestCPAExportUploadsAuthFiles(t *testing.T) {
	var uploadedName string
	var uploadedPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v0/management/auth-files" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		uploadedName = r.URL.Query().Get("name")
		if err := json.NewDecoder(r.Body).Decode(&uploadedPayload); err != nil {
			t.Fatalf("decode uploaded payload: %v", err)
		}
		writeJSON(t, w, map[string]any{"status": "ok"})
	}))
	defer server.Close()

	dir := t.TempDir()
	backend := storage.NewJSONBackend(filepath.Join(dir, "accounts.json"), filepath.Join(dir, "auth_keys.json"))
	accounts := NewAccountService(backend, testAccountConfig{}, NewProxyService(testAccountConfig{}), NewLogService(dir))
	accounts.AddAccountRecords([]map[string]any{{
		"type":          "codex",
		"access_token":  "export-access",
		"refresh_token": "export-refresh",
		"id_token":      "export-id",
		"account_id":    "export-account",
		"email":         "export@example.com",
	}})
	config := NewCPAConfig(dir, backend)
	pool := config.AddPool("remote", server.URL, "secret")
	cpa := NewCPAImportService(config, accounts, NewProxyService(testAccountConfig{}))

	if _, err := cpa.StartExport(pool, nil, true); err != nil {
		t.Fatalf("StartExport() error = %v", err)
	}
	job := waitForCPAJob(t, func() map[string]any { return config.GetExportJob(util.Clean(pool["id"])) })
	if job["status"] != "completed" || job["exported"] != 1 || job["failed"] != 0 {
		t.Fatalf("export job = %#v, want completed exported 1", job)
	}
	if uploadedName == "" ||
		uploadedPayload["type"] != "codex" ||
		uploadedPayload["refresh_token"] != "export-refresh" ||
		uploadedPayload["id_token"] != "export-id" ||
		uploadedPayload["account_id"] != "export-account" ||
		uploadedPayload["chatgpt_account_id"] != "export-account" {
		t.Fatalf("uploaded name=%q payload=%#v, want CPA codex auth file", uploadedName, uploadedPayload)
	}
}

func testUnsignedJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal jwt claims: %v", err)
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	body := base64.RawURLEncoding.EncodeToString(payload)
	return header + "." + body + "."
}

func waitForCPAJob(t *testing.T, get func() map[string]any) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job := get()
		status := util.Clean(job["status"])
		if status == "completed" || status == "failed" {
			return job
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for CPA job: %#v", get())
	return nil
}
