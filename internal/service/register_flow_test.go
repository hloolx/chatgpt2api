package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func registerJSONResponse(req *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

func TestRegisterFNV1A32MatchesPythonImplementation(t *testing.T) {
	cases := map[string]string{
		"":            "ab3e7c0b",
		"abc":         "1cc93dbc",
		"seedpayload": "769860aa",
		"OpenAI":      "ce220710",
	}
	for input, want := range cases {
		if got := registerFNV1A32(input); got != want {
			t.Fatalf("registerFNV1A32(%q) = %s, want %s", input, got, want)
		}
	}
}

func TestBuildSentinelTokenUsesSentinelChallenge(t *testing.T) {
	worker := &registerWorker{
		deviceID: "device-1",
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.String() != registerSentinelBase+"/backend-api/sentinel/req" {
				t.Fatalf("unexpected request URL: %s", req.URL.String())
			}
			if got := req.Header.Get("Content-Type"); got != "text/plain;charset=UTF-8" {
				t.Fatalf("Content-Type = %q", got)
			}
			return registerJSONResponse(req, http.StatusOK, `{"token":"challenge-token","proofofwork":{"required":false}}`), nil
		})},
	}

	token, err := worker.buildSentinelToken(context.Background(), "username_password_create")
	if err != nil {
		t.Fatalf("buildSentinelToken() error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(token), &payload); err != nil {
		t.Fatalf("sentinel token is not JSON: %v", err)
	}
	if payload["c"] != "challenge-token" || payload["id"] != "device-1" || payload["flow"] != "username_password_create" {
		t.Fatalf("sentinel payload = %#v", payload)
	}
	p, _ := payload["p"].(string)
	if !strings.HasPrefix(p, "gAAAAAC") {
		t.Fatalf("sentinel proof token = %q", p)
	}
}

func TestValidateOTPCodeRetriesWithSentinelToken(t *testing.T) {
	validateCalls := 0
	worker := &registerWorker{
		deviceID: "device-1",
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/api/accounts/email-otp/validate":
				validateCalls++
				if validateCalls == 1 {
					if req.Header.Get("openai-sentinel-token") != "" {
						t.Fatal("first OTP validate unexpectedly had sentinel token")
					}
					return registerJSONResponse(req, http.StatusForbidden, `{"error":"sentinel_required"}`), nil
				}
				if req.Header.Get("openai-sentinel-token") == "" {
					t.Fatal("second OTP validate did not carry sentinel token")
				}
				return registerJSONResponse(req, http.StatusOK, `{"continue_url":"/continue"}`), nil
			case "/backend-api/sentinel/req":
				return registerJSONResponse(req, http.StatusOK, `{"token":"challenge-token","proofofwork":{"required":false}}`), nil
			default:
				t.Fatalf("unexpected request path: %s", req.URL.Path)
				return nil, nil
			}
		})},
	}

	payload, err := worker.validateOTPCode(context.Background(), "123456")
	if err != nil {
		t.Fatalf("validateOTPCode() error = %v", err)
	}
	if validateCalls != 2 {
		t.Fatalf("validate calls = %d, want 2", validateCalls)
	}
	if payload["continue_url"] != "/continue" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestSelectWorkspaceForConsentCodeUsesCookieFallback(t *testing.T) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New() error = %v", err)
	}
	authURL, _ := url.Parse(registerAuthBase)
	cookiePayload, _ := json.Marshal(map[string]any{"workspaces": []map[string]any{{"id": "workspace-1"}}})
	jar.SetCookies(authURL, []*http.Cookie{
		{Name: "oai-client-auth-session", Value: base64.RawURLEncoding.EncodeToString(cookiePayload) + ".rest", Path: "/"},
	})
	worker := &registerWorker{
		deviceID: "device-1",
		client: &http.Client{Jar: jar, Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/api/accounts/workspace/select":
				return registerJSONResponse(req, http.StatusOK, `{"data":{"orgs":[{"id":"org-1","projects":[{"id":"project-1"}]}]},"continue_url":"https://auth.openai.com/continue"}`), nil
			case "/api/accounts/organization/select":
				resp := registerJSONResponse(req, http.StatusFound, `{}`)
				resp.Header.Set("Location", registerPlatformOAuthRedirectURI+"?code=callback-code&state=state")
				return resp, nil
			default:
				t.Fatalf("unexpected request path: %s", req.URL.Path)
				return nil, nil
			}
		})},
	}

	code, err := worker.selectWorkspaceForConsentCode(context.Background(), registerAuthBase+"/sign-in-with-chatgpt/codex/consent")
	if err != nil {
		t.Fatalf("selectWorkspaceForConsentCode() error = %v", err)
	}
	if code != "callback-code" {
		t.Fatalf("code = %q", code)
	}
}

func TestLoginAndExchangeTokensSubmitsEmailBeforePassword(t *testing.T) {
	var sequence []string
	worker := &registerWorker{
		service:  &RegisterService{},
		deviceID: "device-1",
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/api/accounts/authorize":
				sequence = append(sequence, "authorize")
				return registerJSONResponse(req, http.StatusOK, `{}`), nil
			case "/backend-api/sentinel/req":
				return registerJSONResponse(req, http.StatusOK, `{"token":"challenge-token","proofofwork":{"required":false}}`), nil
			case "/api/accounts/authorize/continue":
				sequence = append(sequence, "email")
				var body map[string]any
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Fatalf("decode authorize/continue body: %v", err)
				}
				username := body["username"].(map[string]any)
				if username["kind"] != "email" || username["value"] != "user@example.test" {
					t.Fatalf("authorize/continue body = %#v", body)
				}
				return registerJSONResponse(req, http.StatusOK, `{}`), nil
			case "/api/accounts/password/verify":
				sequence = append(sequence, "password")
				return registerJSONResponse(req, http.StatusOK, `{"continue_url":"`+registerPlatformOAuthRedirectURI+`?code=callback-code&state=state"}`), nil
			case "/auth/callback":
				sequence = append(sequence, "callback")
				return registerJSONResponse(req, http.StatusOK, `{}`), nil
			case "/oauth/token":
				sequence = append(sequence, "token")
				return registerJSONResponse(req, http.StatusOK, `{"access_token":"access","refresh_token":"refresh","id_token":"id"}`), nil
			default:
				t.Fatalf("unexpected request path: %s", req.URL.Path)
				return nil, nil
			}
		})},
	}

	tokens, err := worker.loginAndExchangeTokens(context.Background(), "user@example.test", "Password123!", map[string]any{"address": "user@example.test"})
	if err != nil {
		t.Fatalf("loginAndExchangeTokens() error = %v", err)
	}
	if tokens["access_token"] != "access" || tokens["refresh_token"] != "refresh" || tokens["id_token"] != "id" {
		t.Fatalf("tokens = %#v", tokens)
	}
	want := []string{"authorize", "email", "password", "callback", "token"}
	if strings.Join(sequence, ",") != strings.Join(want, ",") {
		t.Fatalf("request sequence = %#v, want %#v", sequence, want)
	}
}

func TestRegisterHTTPClientUsesSOCKSTransport(t *testing.T) {
	client, err := registerHTTPClient("socks5h://127.0.0.1:1", time.Second, "device-1")
	if err != nil {
		t.Fatalf("registerHTTPClient() error = %v", err)
	}
	if client.Jar == nil {
		t.Fatal("SOCKS register client missing cookie jar")
	}
	authURL, _ := url.Parse(registerAuthBase)
	cookies := client.Jar.Cookies(authURL)
	var hasDeviceID bool
	for _, c := range cookies {
		if c.Name == "oai-did" && c.Value == "device-1" {
			hasDeviceID = true
			break
		}
	}
	if !hasDeviceID {
		t.Fatal("SOCKS register client missing oai-did cookie")
	}
}
func TestExtractRegisterMailCodeFromRawMIME(t *testing.T) {
	raw := strings.Join([]string{
		"From: OpenAI <noreply@example.test>",
		"To: user@example.test",
		"Subject: Verify",
		"Content-Type: multipart/alternative; boundary=abc",
		"",
		"--abc",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"Your verification code is 654321",
		"--abc--",
	}, "\r\n")
	if got := extractRegisterMailCode(map[string]any{"raw": raw}); got != "654321" {
		t.Fatalf("extractRegisterMailCode(raw) = %q", got)
	}
}

func TestRegisterMessageMatchesEmail(t *testing.T) {
	message := map[string]any{"to": []any{map[string]any{"address": "target@example.test"}}}
	if !registerMessageMatchesEmail(message, "target@example.test") {
		t.Fatal("matching recipient was rejected")
	}
	if registerMessageMatchesEmail(message, "other@example.test") {
		t.Fatal("non-matching recipient was accepted")
	}
}

func TestLatestRegisterMailMessageByTimestamp(t *testing.T) {
	items := []map[string]any{
		{"id": "old", "timestamp": float64(100), "subject": "old"},
		{"id": "new", "timestamp": float64(200), "subject": "new"},
	}
	if got := latestRegisterMailMessage(items); got["id"] != "new" {
		t.Fatalf("latestRegisterMailMessage() = %#v", got)
	}
}
