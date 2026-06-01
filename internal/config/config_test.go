package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreUpdatePersistsRuntimeSettings(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CHATGPT2API_ROOT", root)
	unsetEnv(t, "CHATGPT2API_BASE_URL")
	unsetEnv(t, "CHATGPT2API_PROXY")
	unsetEnv(t, "CHATGPT2API_REFRESH_ACCOUNT_INTERVAL_MINUTE")
	unsetEnv(t, "CHATGPT2API_IMAGE_TASK_TIMEOUT_SECONDS")
	unsetEnv(t, "CHATGPT2API_USER_DEFAULT_CONCURRENT_LIMIT")
	unsetEnv(t, "CHATGPT2API_USER_DEFAULT_RPM_LIMIT")
	unsetEnv(t, "CHATGPT2API_IMAGE_RETENTION_DAYS")
	unsetEnv(t, "CHATGPT2API_IMAGE_STORAGE_LIMIT_MB")
	unsetEnv(t, "CHATGPT2API_FORCE_IMAGE_URL_RESPONSE")
	unsetEnv(t, "CHATGPT2API_DEFAULT_IMAGE_VISIBILITY")
	unsetEnv(t, "CHATGPT2API_LOG_RETENTION_DAYS")
	unsetEnv(t, "CHATGPT2API_AUTO_REMOVE_INVALID_ACCOUNTS")
	unsetEnv(t, "CHATGPT2API_AUTO_REMOVE_RATE_LIMITED_ACCOUNTS")
	unsetEnv(t, "CHATGPT2API_REGISTRATION_ENABLED")
	unsetEnv(t, "CHATGPT2API_LOG_LEVELS")
	unsetLinuxDoEnv(t)

	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	got, err := store.Update(map[string]any{
		"base_url":                        "https://example.test/root/",
		"proxy":                           "http://127.0.0.1:8080",
		"refresh_account_interval_minute": 7,
		"image_concurrent_limit":          3,
		"image_task_timeout_seconds":      420,
		"user_default_concurrent_limit":   2,
		"user_default_rpm_limit":          30,
		"image_retention_days":            14,
		"image_storage_limit_mb":          512,
		"force_image_url_response":        true,
		"default_image_visibility":        "public",
		"log_retention_days":              21,
		"registration_enabled":            true,
		"log_levels":                      []any{"debug", "error"},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if store.BaseURL() != "https://example.test/root" {
		t.Fatalf("BaseURL() = %q", store.BaseURL())
	}
	assertConfigValue(t, got, "registration_enabled", true)
	assertConfigValue(t, got, "force_image_url_response", true)
	assertConfigValue(t, got, "default_image_visibility", "public")
	if !store.ForceImageURLResponse() {
		t.Fatal("ForceImageURLResponse() = false, want true")
	}
	if store.DefaultImageVisibility() != "public" {
		t.Fatalf("DefaultImageVisibility() = %q, want public", store.DefaultImageVisibility())
	}
	if _, ok := got["image_concurrent_limit"]; ok {
		t.Fatalf("removed image_concurrent_limit leaked in config response: %#v", got)
	}

	envData, err := os.ReadFile(filepath.Join(root, ".env"))
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	envText := string(envData)
	for _, want := range []string{
		"CHATGPT2API_BASE_URL=https://example.test/root/",
		"CHATGPT2API_PROXY=http://127.0.0.1:8080",
		"CHATGPT2API_REFRESH_ACCOUNT_INTERVAL_MINUTE=7",
		"CHATGPT2API_IMAGE_TASK_TIMEOUT_SECONDS=420",
		"CHATGPT2API_USER_DEFAULT_CONCURRENT_LIMIT=2",
		"CHATGPT2API_USER_DEFAULT_RPM_LIMIT=30",
		"CHATGPT2API_IMAGE_RETENTION_DAYS=14",
		"CHATGPT2API_IMAGE_STORAGE_LIMIT_MB=512",
		"CHATGPT2API_FORCE_IMAGE_URL_RESPONSE=true",
		"CHATGPT2API_DEFAULT_IMAGE_VISIBILITY=public",
		"CHATGPT2API_LOG_RETENTION_DAYS=21",
		"CHATGPT2API_REGISTRATION_ENABLED=true",
		"CHATGPT2API_LOG_LEVELS=debug,error",
	} {
		if !strings.Contains(envText, want) {
			t.Fatalf(".env missing %q in:\n%s", want, envText)
		}
	}
	if strings.Contains(envText, "CHATGPT2API_IMAGE_CONCURRENT_LIMIT") {
		t.Fatalf(".env persisted removed image concurrent limit:\n%s", envText)
	}
}

func TestStoreNormalizesDefaultImageVisibility(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CHATGPT2API_ROOT", root)
	unsetEnv(t, "CHATGPT2API_DEFAULT_IMAGE_VISIBILITY")
	unsetLinuxDoEnv(t)

	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if store.DefaultImageVisibility() != "private" {
		t.Fatalf("default DefaultImageVisibility() = %q, want private", store.DefaultImageVisibility())
	}

	got, err := store.Update(map[string]any{"default_image_visibility": "public"})
	if err != nil {
		t.Fatalf("Update(public) error = %v", err)
	}
	assertConfigValue(t, got, "default_image_visibility", "public")
	if gotEnv := os.Getenv("CHATGPT2API_DEFAULT_IMAGE_VISIBILITY"); gotEnv != "public" {
		t.Fatalf("CHATGPT2API_DEFAULT_IMAGE_VISIBILITY = %q, want public", gotEnv)
	}

	got, err = store.Update(map[string]any{"default_image_visibility": "shared"})
	if err != nil {
		t.Fatalf("Update(invalid) error = %v", err)
	}
	assertConfigValue(t, got, "default_image_visibility", "private")
	if store.DefaultImageVisibility() != "private" {
		t.Fatalf("DefaultImageVisibility() = %q, want private", store.DefaultImageVisibility())
	}
	envData, err := os.ReadFile(filepath.Join(root, ".env"))
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	if !strings.Contains(string(envData), "CHATGPT2API_DEFAULT_IMAGE_VISIBILITY=private") {
		t.Fatalf(".env missing normalized default image visibility:\n%s", string(envData))
	}
}

func TestStoreNormalizesUnsupportedLoginPageImageMode(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CHATGPT2API_ROOT", root)
	unsetEnv(t, "CHATGPT2API_LOGIN_PAGE_IMAGE_MODE")
	unsetLinuxDoEnv(t)

	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	got, err := store.Update(map[string]any{"login_page_image_mode": "repeat"})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	assertConfigValue(t, got, "login_page_image_mode", "contain")
	if store.LoginPageImageMode() != "contain" {
		t.Fatalf("LoginPageImageMode() = %q, want contain", store.LoginPageImageMode())
	}
	envData, err := os.ReadFile(filepath.Join(root, ".env"))
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	envText := string(envData)
	if strings.Contains(envText, "CHATGPT2API_LOGIN_PAGE_IMAGE_MODE=repeat") {
		t.Fatalf(".env persisted unsupported login page image mode:\n%s", envText)
	}
	if !strings.Contains(envText, "CHATGPT2API_LOGIN_PAGE_IMAGE_MODE=contain") {
		t.Fatalf(".env missing normalized login page image mode:\n%s", envText)
	}
}

func TestStoreNormalizesImageTaskTimeoutSeconds(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CHATGPT2API_ROOT", root)
	unsetEnv(t, "CHATGPT2API_IMAGE_TASK_TIMEOUT_SECONDS")
	unsetLinuxDoEnv(t)

	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	got, err := store.Update(map[string]any{"image_task_timeout_seconds": 5})
	if err != nil {
		t.Fatalf("Update() min error = %v", err)
	}
	assertConfigValue(t, got, "image_task_timeout_seconds", 30)
	if store.ImageTaskTimeoutSeconds() != 30 {
		t.Fatalf("ImageTaskTimeoutSeconds() = %d, want 30", store.ImageTaskTimeoutSeconds())
	}

	got, err = store.Update(map[string]any{"image_task_timeout_seconds": 7200})
	if err != nil {
		t.Fatalf("Update() max error = %v", err)
	}
	assertConfigValue(t, got, "image_task_timeout_seconds", 3600)
	if store.ImageTaskTimeoutSeconds() != 3600 {
		t.Fatalf("ImageTaskTimeoutSeconds() = %d, want 3600", store.ImageTaskTimeoutSeconds())
	}

	got, err = store.Update(map[string]any{"image_task_timeout_seconds": float64(900)})
	if err != nil {
		t.Fatalf("Update() json number error = %v", err)
	}
	assertConfigValue(t, got, "image_task_timeout_seconds", 900)
	if store.ImageTaskTimeoutSeconds() != 900 {
		t.Fatalf("ImageTaskTimeoutSeconds() = %d, want 900", store.ImageTaskTimeoutSeconds())
	}
}

func TestStoreUpdatePersistsLinuxDoSettingsWithoutLeakingSecret(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CHATGPT2API_ROOT", root)
	unsetLinuxDoEnv(t)

	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	got, err := store.Update(map[string]any{
		"linuxdo_enabled":               true,
		"linuxdo_client_id":             "client-id",
		"linuxdo_client_secret":         "client-secret",
		"linuxdo_redirect_url":          "https://example.test/auth/linuxdo/oauth/callback",
		"linuxdo_frontend_redirect_url": "http://127.0.0.1:5173/auth/linuxdo/callback",
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	assertConfigValue(t, got, "linuxdo_enabled", true)
	assertConfigValue(t, got, "linuxdo_client_id", "client-id")
	assertConfigValue(t, got, "linuxdo_client_secret_configured", true)
	assertConfigValue(t, got, "linuxdo_redirect_url", "https://example.test/auth/linuxdo/oauth/callback")
	assertConfigValue(t, got, "linuxdo_frontend_redirect_url", "http://127.0.0.1:5173/auth/linuxdo/callback")
	if _, ok := got["linuxdo_client_secret"]; ok {
		t.Fatalf("Get() leaked linuxdo_client_secret: %#v", got)
	}
	if !store.LinuxDoOAuth().Ready() {
		t.Fatalf("LinuxDoOAuth() should be ready: %#v", store.LinuxDoOAuth())
	}

	envData, err := os.ReadFile(filepath.Join(root, ".env"))
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	envText := string(envData)
	for _, want := range []string{
		"CHATGPT2API_LINUXDO_ENABLED=true",
		"CHATGPT2API_LINUXDO_CLIENT_ID=client-id",
		"CHATGPT2API_LINUXDO_CLIENT_SECRET=client-secret",
		"CHATGPT2API_LINUXDO_FRONTEND_REDIRECT_URL=http://127.0.0.1:5173/auth/linuxdo/callback",
		"CHATGPT2API_LINUXDO_REDIRECT_URL=https://example.test/auth/linuxdo/oauth/callback",
	} {
		if !strings.Contains(envText, want) {
			t.Fatalf(".env missing %q in:\n%s", want, envText)
		}
	}

	got, err = store.Update(map[string]any{
		"linuxdo_enabled":               true,
		"linuxdo_client_id":             "client-id-next",
		"linuxdo_client_secret":         "",
		"linuxdo_redirect_url":          "https://example.test/auth/linuxdo/oauth/callback",
		"linuxdo_frontend_redirect_url": "/auth/linuxdo/callback",
	})
	if err != nil {
		t.Fatalf("Update() with blank secret error = %v", err)
	}
	assertConfigValue(t, got, "linuxdo_client_id", "client-id-next")
	assertConfigValue(t, got, "linuxdo_client_secret_configured", true)
	assertConfigValue(t, got, "linuxdo_frontend_redirect_url", "/auth/linuxdo/callback")
	if store.LinuxDoOAuth().ClientSecret != "client-secret" {
		t.Fatalf("blank secret update should preserve existing secret")
	}
}

func TestStoreUpdateRejectsIncompleteLinuxDoSettings(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CHATGPT2API_ROOT", root)
	unsetLinuxDoEnv(t)

	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}

	_, err = store.Update(map[string]any{
		"linuxdo_enabled":      true,
		"linuxdo_client_id":    "client-id",
		"linuxdo_redirect_url": "https://example.test/auth/linuxdo/oauth/callback",
	})
	if err == nil || !strings.Contains(err.Error(), "Client Secret") {
		t.Fatalf("Update() error = %v, want missing secret", err)
	}
}

func TestStoreUpdateRefreshesEnvFileBackedRuntimeSettings(t *testing.T) {
	root := t.TempDir()
	envText := strings.Join([]string{
		"CHATGPT2API_BASE_URL=https://old.example/root",
		"CHATGPT2API_PROXY=http://127.0.0.1:8080",
		"CHATGPT2API_REFRESH_ACCOUNT_INTERVAL_MINUTE=5",
		"CHATGPT2API_IMAGE_TASK_TIMEOUT_SECONDS=300",
		"CHATGPT2API_USER_DEFAULT_CONCURRENT_LIMIT=2",
		"CHATGPT2API_USER_DEFAULT_RPM_LIMIT=30",
		"CHATGPT2API_IMAGE_RETENTION_DAYS=30",
		"CHATGPT2API_IMAGE_STORAGE_LIMIT_MB=2048",
		"CHATGPT2API_LOG_RETENTION_DAYS=7",
		"CHATGPT2API_AUTO_REMOVE_INVALID_ACCOUNTS=true",
		"CHATGPT2API_AUTO_REMOVE_RATE_LIMITED_ACCOUNTS=false",
		"CHATGPT2API_LOG_LEVELS=warning,error",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte(envText), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	t.Setenv("CHATGPT2API_ROOT", root)
	t.Setenv("CHATGPT2API_BASE_URL", "https://old.example/root")
	t.Setenv("CHATGPT2API_PROXY", "http://127.0.0.1:8080")
	t.Setenv("CHATGPT2API_REFRESH_ACCOUNT_INTERVAL_MINUTE", "5")
	t.Setenv("CHATGPT2API_IMAGE_TASK_TIMEOUT_SECONDS", "300")
	t.Setenv("CHATGPT2API_USER_DEFAULT_CONCURRENT_LIMIT", "2")
	t.Setenv("CHATGPT2API_USER_DEFAULT_RPM_LIMIT", "30")
	t.Setenv("CHATGPT2API_IMAGE_RETENTION_DAYS", "30")
	t.Setenv("CHATGPT2API_IMAGE_STORAGE_LIMIT_MB", "2048")
	t.Setenv("CHATGPT2API_LOG_RETENTION_DAYS", "7")
	t.Setenv("CHATGPT2API_AUTO_REMOVE_INVALID_ACCOUNTS", "true")
	t.Setenv("CHATGPT2API_AUTO_REMOVE_RATE_LIMITED_ACCOUNTS", "false")
	t.Setenv("CHATGPT2API_LOG_LEVELS", "warning,error")

	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	got, err := store.Update(map[string]any{
		"base_url":                          "https://new.example/root/",
		"proxy":                             "http://127.0.0.1:9090",
		"refresh_account_interval_minute":   9,
		"image_task_timeout_seconds":        480,
		"user_default_concurrent_limit":     3,
		"user_default_rpm_limit":            45,
		"image_retention_days":              12,
		"image_storage_limit_mb":            1024,
		"log_retention_days":                30,
		"auto_remove_invalid_accounts":      false,
		"auto_remove_rate_limited_accounts": true,
		"log_levels":                        []any{"debug", "info"},
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	assertConfigValue(t, got, "base_url", "https://new.example/root")
	assertConfigValue(t, got, "proxy", "http://127.0.0.1:9090")
	assertConfigValue(t, got, "refresh_account_interval_minute", 9)
	assertConfigValue(t, got, "image_task_timeout_seconds", 480)
	assertConfigValue(t, got, "user_default_concurrent_limit", 3)
	assertConfigValue(t, got, "user_default_rpm_limit", 45)
	assertConfigValue(t, got, "image_retention_days", 12)
	assertConfigValue(t, got, "image_storage_limit_mb", 1024)
	if store.ImageStorageLimitBytes() != 1024*1024*1024 {
		t.Fatalf("ImageStorageLimitBytes() = %d, want 1GiB", store.ImageStorageLimitBytes())
	}
	assertConfigValue(t, got, "log_retention_days", 30)
	assertConfigValue(t, got, "auto_remove_invalid_accounts", false)
	assertConfigValue(t, got, "auto_remove_rate_limited_accounts", true)
	if levels := strings.Join(store.LogLevels(), ","); levels != "debug,info" {
		t.Fatalf("LogLevels() = %q, want debug,info", levels)
	}

	for key, want := range map[string]string{
		"CHATGPT2API_BASE_URL":                          "https://new.example/root/",
		"CHATGPT2API_PROXY":                             "http://127.0.0.1:9090",
		"CHATGPT2API_REFRESH_ACCOUNT_INTERVAL_MINUTE":   "9",
		"CHATGPT2API_IMAGE_TASK_TIMEOUT_SECONDS":        "480",
		"CHATGPT2API_USER_DEFAULT_CONCURRENT_LIMIT":     "3",
		"CHATGPT2API_USER_DEFAULT_RPM_LIMIT":            "45",
		"CHATGPT2API_IMAGE_RETENTION_DAYS":              "12",
		"CHATGPT2API_IMAGE_STORAGE_LIMIT_MB":            "1024",
		"CHATGPT2API_LOG_RETENTION_DAYS":                "30",
		"CHATGPT2API_AUTO_REMOVE_INVALID_ACCOUNTS":      "false",
		"CHATGPT2API_AUTO_REMOVE_RATE_LIMITED_ACCOUNTS": "true",
		"CHATGPT2API_LOG_LEVELS":                        "debug,info",
	} {
		if gotEnv := os.Getenv(key); gotEnv != want {
			t.Fatalf("%s = %q, want %q", key, gotEnv, want)
		}
	}
}

func TestStoreEnvFileValueWinsOverStaleProcessEnvironment(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte(strings.Join([]string{
		"CHATGPT2API_REGISTRATION_ENABLED=false",
		"",
	}, "\n")), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	t.Setenv("CHATGPT2API_ROOT", root)
	t.Setenv("CHATGPT2API_REGISTRATION_ENABLED", "true")

	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if store.RegistrationEnabled() {
		t.Fatal("RegistrationEnabled() used stale process environment instead of .env")
	}
	got, err := store.Update(map[string]any{"registration_enabled": false})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	assertConfigValue(t, got, "registration_enabled", false)
	if gotEnv := os.Getenv("CHATGPT2API_REGISTRATION_ENABLED"); gotEnv != "false" {
		t.Fatalf("CHATGPT2API_REGISTRATION_ENABLED = %q, want saved false", gotEnv)
	}
}

func TestStoreUpdateOverridesEnvOnlyRuntimeSetting(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CHATGPT2API_ROOT", root)
	t.Setenv("CHATGPT2API_REGISTRATION_ENABLED", "true")

	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if !store.RegistrationEnabled() {
		t.Fatal("RegistrationEnabled() should seed from env-only setting")
	}
	got, err := store.Update(map[string]any{"registration_enabled": false})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	assertConfigValue(t, got, "registration_enabled", false)
	if store.RegistrationEnabled() {
		t.Fatal("RegistrationEnabled() stayed enabled after saving false")
	}
	if gotEnv := os.Getenv("CHATGPT2API_REGISTRATION_ENABLED"); gotEnv != "false" {
		t.Fatalf("CHATGPT2API_REGISTRATION_ENABLED = %q, want saved false", gotEnv)
	}
	envData, err := os.ReadFile(filepath.Join(root, ".env"))
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	if !strings.Contains(string(envData), "CHATGPT2API_REGISTRATION_ENABLED=false") {
		t.Fatalf(".env missing saved registration setting:\n%s", string(envData))
	}
}

func TestStoreUpdateOverridesEnvOnlyRuntimeSettings(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CHATGPT2API_ROOT", root)
	unsetLinuxDoEnv(t)
	for key, value := range map[string]string{
		"CHATGPT2API_BASE_URL":                          "https://old.example/root",
		"CHATGPT2API_PROXY":                             "http://127.0.0.1:8080",
		"CHATGPT2API_REFRESH_ACCOUNT_INTERVAL_MINUTE":   "5",
		"CHATGPT2API_IMAGE_TASK_TIMEOUT_SECONDS":        "300",
		"CHATGPT2API_USER_DEFAULT_CONCURRENT_LIMIT":     "2",
		"CHATGPT2API_USER_DEFAULT_RPM_LIMIT":            "30",
		"CHATGPT2API_DEFAULT_BILLING_TYPE":              "standard",
		"CHATGPT2API_DEFAULT_STANDARD_BALANCE":          "1",
		"CHATGPT2API_DEFAULT_SUBSCRIPTION_QUOTA":        "2",
		"CHATGPT2API_DEFAULT_SUBSCRIPTION_PERIOD":       "monthly",
		"CHATGPT2API_IMAGE_RETENTION_DAYS":              "30",
		"CHATGPT2API_IMAGE_STORAGE_LIMIT_MB":            "2048",
		"CHATGPT2API_LOG_RETENTION_DAYS":                "7",
		"CHATGPT2API_AUTO_REMOVE_INVALID_ACCOUNTS":      "true",
		"CHATGPT2API_AUTO_REMOVE_RATE_LIMITED_ACCOUNTS": "false",
		"CHATGPT2API_LOG_LEVELS":                        "warning,error",
		"CHATGPT2API_REGISTRATION_ENABLED":              "true",
		"CHATGPT2API_LOGIN_PAGE_IMAGE_URL":              "https://old.example/login.png",
		"CHATGPT2API_LOGIN_PAGE_IMAGE_MODE":             "contain",
		"CHATGPT2API_LOGIN_PAGE_IMAGE_ZOOM":             "1",
		"CHATGPT2API_LOGIN_PAGE_IMAGE_POSITION_X":       "50",
		"CHATGPT2API_LOGIN_PAGE_IMAGE_POSITION_Y":       "50",
	} {
		t.Setenv(key, value)
	}

	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	got, err := store.Update(map[string]any{
		"base_url":                          "https://new.example/root/",
		"proxy":                             "http://127.0.0.1:9090",
		"refresh_account_interval_minute":   9,
		"image_task_timeout_seconds":        480,
		"user_default_concurrent_limit":     3,
		"user_default_rpm_limit":            45,
		"default_billing_type":              "subscription",
		"default_standard_balance":          11,
		"default_subscription_quota":        22,
		"default_subscription_period":       "weekly",
		"image_retention_days":              12,
		"image_storage_limit_mb":            1024,
		"log_retention_days":                30,
		"auto_remove_invalid_accounts":      false,
		"auto_remove_rate_limited_accounts": true,
		"log_levels":                        []any{"debug", "info"},
		"registration_enabled":              false,
		"login_page_image_url":              "https://new.example/login.png",
		"login_page_image_mode":             "cover",
		"login_page_image_zoom":             2,
		"login_page_image_position_x":       25,
		"login_page_image_position_y":       75,
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	assertConfigValue(t, got, "base_url", "https://new.example/root")
	assertConfigValue(t, got, "proxy", "http://127.0.0.1:9090")
	assertConfigValue(t, got, "refresh_account_interval_minute", 9)
	assertConfigValue(t, got, "image_task_timeout_seconds", 480)
	assertConfigValue(t, got, "user_default_concurrent_limit", 3)
	assertConfigValue(t, got, "user_default_rpm_limit", 45)
	assertConfigValue(t, got, "default_billing_type", "subscription")
	assertConfigValue(t, got, "default_standard_balance", 11)
	assertConfigValue(t, got, "default_subscription_quota", 22)
	assertConfigValue(t, got, "default_subscription_period", "weekly")
	assertConfigValue(t, got, "image_retention_days", 12)
	assertConfigValue(t, got, "image_storage_limit_mb", 1024)
	assertConfigValue(t, got, "log_retention_days", 30)
	assertConfigValue(t, got, "auto_remove_invalid_accounts", false)
	assertConfigValue(t, got, "auto_remove_rate_limited_accounts", true)
	assertConfigValue(t, got, "registration_enabled", false)
	assertConfigValue(t, got, "login_page_image_url", "https://new.example/login.png")
	assertConfigValue(t, got, "login_page_image_mode", "cover")
	assertConfigValue(t, got, "login_page_image_zoom", float64(2))
	assertConfigValue(t, got, "login_page_image_position_x", float64(25))
	assertConfigValue(t, got, "login_page_image_position_y", float64(75))
	if levels := strings.Join(store.LogLevels(), ","); levels != "debug,info" {
		t.Fatalf("LogLevels() = %q, want debug,info", levels)
	}

	for key, want := range map[string]string{
		"CHATGPT2API_BASE_URL":                          "https://new.example/root/",
		"CHATGPT2API_PROXY":                             "http://127.0.0.1:9090",
		"CHATGPT2API_REFRESH_ACCOUNT_INTERVAL_MINUTE":   "9",
		"CHATGPT2API_IMAGE_TASK_TIMEOUT_SECONDS":        "480",
		"CHATGPT2API_USER_DEFAULT_CONCURRENT_LIMIT":     "3",
		"CHATGPT2API_USER_DEFAULT_RPM_LIMIT":            "45",
		"CHATGPT2API_DEFAULT_BILLING_TYPE":              "subscription",
		"CHATGPT2API_DEFAULT_STANDARD_BALANCE":          "11",
		"CHATGPT2API_DEFAULT_SUBSCRIPTION_QUOTA":        "22",
		"CHATGPT2API_DEFAULT_SUBSCRIPTION_PERIOD":       "weekly",
		"CHATGPT2API_IMAGE_RETENTION_DAYS":              "12",
		"CHATGPT2API_IMAGE_STORAGE_LIMIT_MB":            "1024",
		"CHATGPT2API_LOG_RETENTION_DAYS":                "30",
		"CHATGPT2API_AUTO_REMOVE_INVALID_ACCOUNTS":      "false",
		"CHATGPT2API_AUTO_REMOVE_RATE_LIMITED_ACCOUNTS": "true",
		"CHATGPT2API_LOG_LEVELS":                        "debug,info",
		"CHATGPT2API_REGISTRATION_ENABLED":              "false",
		"CHATGPT2API_LOGIN_PAGE_IMAGE_URL":              "https://new.example/login.png",
		"CHATGPT2API_LOGIN_PAGE_IMAGE_MODE":             "cover",
		"CHATGPT2API_LOGIN_PAGE_IMAGE_ZOOM":             "2",
		"CHATGPT2API_LOGIN_PAGE_IMAGE_POSITION_X":       "25",
		"CHATGPT2API_LOGIN_PAGE_IMAGE_POSITION_Y":       "75",
	} {
		if gotEnv := os.Getenv(key); gotEnv != want {
			t.Fatalf("%s = %q, want %q", key, gotEnv, want)
		}
	}
}

func TestStoreUpdateOverridesLinuxDoEnvOnlyRuntimeSettings(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CHATGPT2API_ROOT", root)
	t.Setenv("CHATGPT2API_BASE_URL", "https://old.example")
	t.Setenv("CHATGPT2API_LINUXDO_ENABLED", "true")
	t.Setenv("CHATGPT2API_LINUXDO_CLIENT_ID", "old-client")
	t.Setenv("CHATGPT2API_LINUXDO_CLIENT_SECRET", "old-secret")
	t.Setenv("CHATGPT2API_LINUXDO_REDIRECT_URL", "https://old.example/auth/linuxdo/oauth/callback")
	t.Setenv("CHATGPT2API_LINUXDO_FRONTEND_REDIRECT_URL", "/old/callback")
	for _, key := range []string{
		"CHATGPT2API_LINUXDO_AUTHORIZE_URL",
		"CHATGPT2API_LINUXDO_TOKEN_URL",
		"CHATGPT2API_LINUXDO_USERINFO_URL",
		"CHATGPT2API_LINUXDO_SCOPES",
		"CHATGPT2API_LINUXDO_TOKEN_AUTH_METHOD",
		"CHATGPT2API_LINUXDO_USE_PKCE",
		"CHATGPT2API_LINUXDO_USERINFO_EMAIL_PATH",
		"CHATGPT2API_LINUXDO_USERINFO_ID_PATH",
		"CHATGPT2API_LINUXDO_USERINFO_USERNAME_PATH",
	} {
		unsetEnv(t, key)
	}

	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	got, err := store.Update(map[string]any{
		"base_url":                      "https://new.example",
		"linuxdo_enabled":               false,
		"linuxdo_client_id":             "new-client",
		"linuxdo_client_secret":         "new-secret",
		"linuxdo_redirect_url":          "https://new.example/auth/linuxdo/oauth/callback",
		"linuxdo_frontend_redirect_url": "/auth/linuxdo/callback",
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	assertConfigValue(t, got, "linuxdo_enabled", false)
	assertConfigValue(t, got, "linuxdo_client_id", "new-client")
	assertConfigValue(t, got, "linuxdo_redirect_url", "https://new.example/auth/linuxdo/oauth/callback")
	assertConfigValue(t, got, "linuxdo_frontend_redirect_url", "/auth/linuxdo/callback")
	if got["linuxdo_client_secret_configured"] != true {
		t.Fatalf("linuxdo_client_secret_configured = %#v, want true", got["linuxdo_client_secret_configured"])
	}
	linuxdo := store.LinuxDoOAuth()
	if linuxdo.Enabled || linuxdo.ClientID != "new-client" || linuxdo.ClientSecret != "new-secret" ||
		linuxdo.RedirectURL != "https://new.example/auth/linuxdo/oauth/callback" ||
		linuxdo.FrontendRedirectURL != "/auth/linuxdo/callback" {
		t.Fatalf("LinuxDoOAuth() = %#v", linuxdo)
	}
	for key, want := range map[string]string{
		"CHATGPT2API_BASE_URL":                      "https://new.example",
		"CHATGPT2API_LINUXDO_ENABLED":               "false",
		"CHATGPT2API_LINUXDO_CLIENT_ID":             "new-client",
		"CHATGPT2API_LINUXDO_CLIENT_SECRET":         "new-secret",
		"CHATGPT2API_LINUXDO_REDIRECT_URL":          "https://new.example/auth/linuxdo/oauth/callback",
		"CHATGPT2API_LINUXDO_FRONTEND_REDIRECT_URL": "/auth/linuxdo/callback",
	} {
		if gotEnv := os.Getenv(key); gotEnv != want {
			t.Fatalf("%s = %q, want %q", key, gotEnv, want)
		}
	}
}

func TestNewStoreDiscoversEnvFromParentDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("CHATGPT2API_BASE_URL=https://parent.example\n"), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	nested := filepath.Join(root, "cmd", "chatgpt2api")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(nested); err != nil {
		t.Fatalf("Chdir() error = %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalWD)
	})
	unsetEnv(t, "CHATGPT2API_ROOT")
	unsetEnv(t, "CHATGPT2API_BASE_URL")

	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if store.RootDir != root {
		t.Fatalf("RootDir = %q, want %q", store.RootDir, root)
	}
	if store.BaseURL() != "https://parent.example" {
		t.Fatalf("BaseURL() = %q", store.BaseURL())
	}
}

func TestStoreReadsUpdateGitHubTokenFromEnvFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("CHATGPT2API_UPDATE_GITHUB_TOKEN=ghp_test_token\n"), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	t.Setenv("CHATGPT2API_ROOT", root)
	unsetEnv(t, "CHATGPT2API_UPDATE_GITHUB_TOKEN")

	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if got := store.UpdateGitHubToken(); got != "ghp_test_token" {
		t.Fatalf("UpdateGitHubToken() = %q, want token from .env", got)
	}
	if _, ok := store.Get()["update_github_token"]; ok {
		t.Fatal("Get() leaked update GitHub token")
	}
	if got := store.Get()["update_github_token_configured"]; got != true {
		t.Fatalf("Get() update_github_token_configured = %#v, want true", got)
	}
}

func TestStoreUpdatePersistsUpdateSettings(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CHATGPT2API_ROOT", root)
	unsetEnv(t, "CHATGPT2API_UPDATE_GITHUB_TOKEN")
	unsetEnv(t, "CHATGPT2API_UPDATE_REPO")

	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	got, err := store.Update(map[string]any{
		"update_repo":         "owner/project",
		"update_github_token": "github_pat_test",
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if got["update_repo"] != "owner/project" {
		t.Fatalf("Update() update_repo = %#v, want owner/project", got["update_repo"])
	}
	if got["update_github_token_configured"] != true {
		t.Fatalf("Update() update_github_token_configured = %#v, want true", got["update_github_token_configured"])
	}
	if _, ok := got["update_github_token"]; ok {
		t.Fatalf("Update() leaked update_github_token: %#v", got)
	}
	if store.UpdateRepo() != "owner/project" {
		t.Fatalf("UpdateRepo() = %q, want owner/project", store.UpdateRepo())
	}
	if store.UpdateGitHubToken() != "github_pat_test" {
		t.Fatalf("UpdateGitHubToken() = %q, want saved token", store.UpdateGitHubToken())
	}
	envData, err := os.ReadFile(filepath.Join(root, ".env"))
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	envText := string(envData)
	for _, want := range []string{
		"CHATGPT2API_UPDATE_REPO=owner/project",
		"CHATGPT2API_UPDATE_GITHUB_TOKEN=github_pat_test",
	} {
		if !strings.Contains(envText, want) {
			t.Fatalf(".env missing %q:\n%s", want, envText)
		}
	}
}

func TestStoreUpdateRejectsInvalidUpdateRepo(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CHATGPT2API_ROOT", root)
	unsetEnv(t, "CHATGPT2API_UPDATE_REPO")

	store, err := NewStore()
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if _, err := store.Update(map[string]any{"update_repo": "invalid"}); err == nil {
		t.Fatal("Update() accepted invalid update_repo")
	}
}

func assertConfigValue(t *testing.T, data map[string]any, key string, want any) {
	t.Helper()
	if got := data[key]; got != want {
		t.Fatalf("%s = %#v, want %#v", key, got, want)
	}
}

func unsetEnv(t *testing.T, key string) {
	t.Helper()
	original, existed := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("Unsetenv(%s): %v", key, err)
	}
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv(key, original)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func unsetLinuxDoEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"CHATGPT2API_LINUXDO_ENABLED",
		"CHATGPT2API_LINUXDO_CLIENT_ID",
		"CHATGPT2API_LINUXDO_CLIENT_SECRET",
		"CHATGPT2API_LINUXDO_REDIRECT_URL",
		"CHATGPT2API_LINUXDO_AUTHORIZE_URL",
		"CHATGPT2API_LINUXDO_TOKEN_URL",
		"CHATGPT2API_LINUXDO_USERINFO_URL",
		"CHATGPT2API_LINUXDO_SCOPES",
		"CHATGPT2API_LINUXDO_FRONTEND_REDIRECT_URL",
		"CHATGPT2API_LINUXDO_TOKEN_AUTH_METHOD",
		"CHATGPT2API_LINUXDO_USE_PKCE",
		"CHATGPT2API_LINUXDO_USERINFO_EMAIL_PATH",
		"CHATGPT2API_LINUXDO_USERINFO_ID_PATH",
		"CHATGPT2API_LINUXDO_USERINFO_USERNAME_PATH",
	} {
		unsetEnv(t, key)
	}
}
