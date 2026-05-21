package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExtractUnseenRegisterMailCodeSkipsSeenMessage(t *testing.T) {
	mailbox := map[string]any{"address": "user@example.test"}
	message := map[string]any{
		"provider":     "moemail",
		"mailbox":      "user@example.test",
		"message_id":   "message-1",
		"subject":      "Verify",
		"text_content": "Verification code: 123456",
	}

	if got := extractUnseenRegisterMailCode(mailbox, message); got != "123456" {
		t.Fatalf("first code = %q, want 123456", got)
	}
	if got := extractUnseenRegisterMailCode(mailbox, message); got != "" {
		t.Fatalf("second code = %q, want empty for already seen message", got)
	}
	seen := registerSeenMailRefList(mailbox["_seen_code_message_refs"])
	if len(seen) != 1 || !strings.Contains(seen[0], "message-1") {
		t.Fatalf("seen refs = %#v", seen)
	}
}

func TestRegisterMoEmailProviderCreatesAndReadsMailbox(t *testing.T) {
	var generatedPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "secret-key" {
			t.Errorf("X-API-Key = %q", r.Header.Get("X-API-Key"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/emails/generate":
			if err := json.NewDecoder(r.Body).Decode(&generatedPayload); err != nil {
				t.Errorf("decode generate payload: %v", err)
			}
			_, _ = w.Write([]byte(`{"email":"user@example.test","id":"email-1"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/emails/email-1":
			_, _ = w.Write([]byte(`{"messages":[{"id":"old","subject":"Old","text":"Verification code: 111111","timestamp":100},{"id":"message-2","subject":"Verify","text":"Verification code: 222222","timestamp":200}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/emails/email-1/message-2":
			_, _ = w.Write([]byte(`{"message":{"id":"message-2","subject":"Verify","text":"Verification code: 222222","timestamp":200,"from":{"email":"noreply@example.test"}}}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider, err := createRegisterMailProvider(map[string]any{
		"request_timeout": 1,
		"providers": []map[string]any{{
			"type":        "moemail",
			"enable":      true,
			"api_base":    server.URL,
			"api_key":     "secret-key",
			"domain":      []string{"example.test"},
			"expiry_time": 15,
		}},
	}, "", "", "")
	if err != nil {
		t.Fatalf("createRegisterMailProvider() error = %v", err)
	}
	defer provider.Close()

	mailbox, err := provider.CreateMailbox("user")
	if err != nil {
		t.Fatalf("CreateMailbox() error = %v", err)
	}
	if mailbox["provider"] != "moemail" || mailbox["address"] != "user@example.test" || mailbox["email_id"] != "email-1" {
		t.Fatalf("mailbox = %#v", mailbox)
	}
	if generatedPayload["name"] != "user" || generatedPayload["domain"] != "example.test" || int(generatedPayload["expiryTime"].(float64)) != 15 {
		t.Fatalf("generated payload = %#v", generatedPayload)
	}

	message, err := provider.FetchLatestMessage(mailbox)
	if err != nil {
		t.Fatalf("FetchLatestMessage() error = %v", err)
	}
	if got := extractRegisterMailCode(message); got != "222222" {
		t.Fatalf("extractRegisterMailCode() = %q, want 222222; message=%#v", got, message)
	}
	if message["message_id"] != "message-2" || message["sender"] != "noreply@example.test" {
		t.Fatalf("message metadata = %#v", message)
	}
}

func TestRegisterInbucketProviderCreatesAndReadsMailbox(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/mailbox/user/"):
			_, _ = w.Write([]byte(`{"id":"message-2","subject":"Verify","from":"OpenAI","date":"2026-01-01T00:00:00Z","header":{"To":["user@random.example.test"]},"body":{"text":"Verification code: 333444","html":""}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/mailbox/user":
			_, _ = w.Write([]byte(`[{"id":"old","subject":"Old","date":"2025-01-01T00:00:00Z"},{"id":"message-2","subject":"Verify","date":"2026-01-01T00:00:00Z"}]`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider, err := createRegisterMailProvider(map[string]any{
		"request_timeout": 1,
		"providers": []map[string]any{{
			"type":             "inbucket",
			"enable":           true,
			"api_base":         server.URL,
			"domain":           []string{"example.test"},
			"random_subdomain": false,
		}},
	}, "", "", "")
	if err != nil {
		t.Fatalf("createRegisterMailProvider() error = %v", err)
	}
	defer provider.Close()

	mailbox, err := provider.CreateMailbox("user")
	if err != nil {
		t.Fatalf("CreateMailbox() error = %v", err)
	}
	if mailbox["provider"] != "inbucket" || mailbox["address"] != "user@example.test" || mailbox["mailbox_name"] != "user" {
		t.Fatalf("mailbox = %#v", mailbox)
	}
	mailbox["address"] = "user@random.example.test"

	message, err := provider.FetchLatestMessage(mailbox)
	if err != nil {
		t.Fatalf("FetchLatestMessage() error = %v", err)
	}
	if got := extractRegisterMailCode(message); got != "333444" {
		t.Fatalf("extractRegisterMailCode() = %q, want 333444; message=%#v", got, message)
	}
	if message["message_id"] != "message-2" || message["sender"] != "OpenAI" {
		t.Fatalf("message metadata = %#v", message)
	}
}

func TestRegisterYYDSMailProviderCreatesAndReadsMailbox(t *testing.T) {
	var createPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/accounts/wildcard":
			if r.Header.Get("X-API-Key") != "secret-key" {
				t.Errorf("X-API-Key = %q", r.Header.Get("X-API-Key"))
			}
			if err := json.NewDecoder(r.Body).Decode(&createPayload); err != nil {
				t.Errorf("decode create payload: %v", err)
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"address":"user@example.test","token":"mail-token","id":"account-1"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/messages/message-2":
			if r.Header.Get("Authorization") != "Bearer mail-token" {
				t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"id":"message-2","subject":"Verify","text":"Verification code: 555666","timestamp":200,"from":{"email":"noreply@example.test"}}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/messages":
			if r.URL.Query().Get("address") != "user@example.test" {
				t.Errorf("address query = %q", r.URL.Query().Get("address"))
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"items":[{"id":"old","subject":"Old","timestamp":100},{"id":"message-2","subject":"Verify","timestamp":200}]}}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider, err := createRegisterMailProvider(map[string]any{
		"request_timeout": 1,
		"providers": []map[string]any{{
			"type":      "yyds_mail",
			"enable":    true,
			"api_base":  server.URL,
			"api_key":   "secret-key",
			"domain":    []string{"example.test"},
			"subdomain": "sub",
			"wildcard":  true,
		}},
	}, "", "", "")
	if err != nil {
		t.Fatalf("createRegisterMailProvider() error = %v", err)
	}
	defer provider.Close()

	mailbox, err := provider.CreateMailbox("user")
	if err != nil {
		t.Fatalf("CreateMailbox() error = %v", err)
	}
	if mailbox["provider"] != "yyds_mail" || mailbox["address"] != "user@example.test" || mailbox["token"] != "mail-token" {
		t.Fatalf("mailbox = %#v", mailbox)
	}
	if createPayload["localPart"] != "user" || createPayload["domain"] != "example.test" || createPayload["subdomain"] != "sub" {
		t.Fatalf("create payload = %#v", createPayload)
	}

	message, err := provider.FetchLatestMessage(mailbox)
	if err != nil {
		t.Fatalf("FetchLatestMessage() error = %v", err)
	}
	if got := extractRegisterMailCode(message); got != "555666" {
		t.Fatalf("extractRegisterMailCode() = %q, want 555666; message=%#v", got, message)
	}
	if message["message_id"] != "message-2" || message["sender"] != "noreply@example.test" {
		t.Fatalf("message metadata = %#v", message)
	}
}
