package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"chatgpt2api/internal/storage"
	"chatgpt2api/internal/util"
)

var settingEnvKeys = map[string]string{
	"base_url":                          "CHATGPT2API_BASE_URL",
	"proxy":                             "CHATGPT2API_PROXY",
	"refresh_account_interval_minute":   "CHATGPT2API_REFRESH_ACCOUNT_INTERVAL_MINUTE",
	"image_task_timeout_seconds":        "CHATGPT2API_IMAGE_TASK_TIMEOUT_SECONDS",
	"user_default_concurrent_limit":     "CHATGPT2API_USER_DEFAULT_CONCURRENT_LIMIT",
	"user_default_rpm_limit":            "CHATGPT2API_USER_DEFAULT_RPM_LIMIT",
	"default_billing_type":              "CHATGPT2API_DEFAULT_BILLING_TYPE",
	"default_standard_balance":          "CHATGPT2API_DEFAULT_STANDARD_BALANCE",
	"default_subscription_quota":        "CHATGPT2API_DEFAULT_SUBSCRIPTION_QUOTA",
	"default_subscription_period":       "CHATGPT2API_DEFAULT_SUBSCRIPTION_PERIOD",
	"image_retention_days":              "CHATGPT2API_IMAGE_RETENTION_DAYS",
	"image_storage_limit_mb":            "CHATGPT2API_IMAGE_STORAGE_LIMIT_MB",
	"force_image_url_response":          "CHATGPT2API_FORCE_IMAGE_URL_RESPONSE",
	"default_image_visibility":          "CHATGPT2API_DEFAULT_IMAGE_VISIBILITY",
	"auto_remove_invalid_accounts":      "CHATGPT2API_AUTO_REMOVE_INVALID_ACCOUNTS",
	"auto_remove_rate_limited_accounts": "CHATGPT2API_AUTO_REMOVE_RATE_LIMITED_ACCOUNTS",
	"log_retention_days":                "CHATGPT2API_LOG_RETENTION_DAYS",
	"log_levels":                        "CHATGPT2API_LOG_LEVELS",
	"linuxdo_enabled":                   "CHATGPT2API_LINUXDO_ENABLED",
	"linuxdo_client_id":                 "CHATGPT2API_LINUXDO_CLIENT_ID",
	"linuxdo_client_secret":             "CHATGPT2API_LINUXDO_CLIENT_SECRET",
	"linuxdo_redirect_url":              "CHATGPT2API_LINUXDO_REDIRECT_URL",
	"linuxdo_frontend_redirect_url":     "CHATGPT2API_LINUXDO_FRONTEND_REDIRECT_URL",
	"update_repo":                       "CHATGPT2API_UPDATE_REPO",
	"update_github_token":               "CHATGPT2API_UPDATE_GITHUB_TOKEN",
	"registration_enabled":              "CHATGPT2API_REGISTRATION_ENABLED",
	"login_page_image_url":              "CHATGPT2API_LOGIN_PAGE_IMAGE_URL",
	"login_page_image_mode":             "CHATGPT2API_LOGIN_PAGE_IMAGE_MODE",
	"login_page_image_zoom":             "CHATGPT2API_LOGIN_PAGE_IMAGE_ZOOM",
	"login_page_image_position_x":       "CHATGPT2API_LOGIN_PAGE_IMAGE_POSITION_X",
	"login_page_image_position_y":       "CHATGPT2API_LOGIN_PAGE_IMAGE_POSITION_Y",
	"storage_mode":                      "CHATGPT2API_STORAGE_MODE",
	"cloud_storage_enabled":             "CHATGPT2API_CLOUD_STORAGE_ENABLED",
	"cloud_storage_uploader":            "CHATGPT2API_CLOUD_STORAGE_UPLOADER",
	"a4_cookie":                         "CHATGPT2API_A4_COOKIE",
	"cloud_cookie_check_interval":       "CHATGPT2API_CLOUD_COOKIE_CHECK_INTERVAL",
	"s3_endpoint":                       "CHATGPT2API_S3_ENDPOINT",
	"s3_region":                         "CHATGPT2API_S3_REGION",
	"s3_access_key_id":                  "CHATGPT2API_S3_ACCESS_KEY_ID",
	"s3_secret_access_key":              "CHATGPT2API_S3_SECRET_ACCESS_KEY",
	"s3_bucket":                         "CHATGPT2API_S3_BUCKET",
	"s3_public_url":                     "CHATGPT2API_S3_PUBLIC_URL",
	"s3_path_prefix":                    "CHATGPT2API_S3_PATH_PREFIX",
	"s3_force_path_style":               "CHATGPT2API_S3_FORCE_PATH_STYLE",
	"cloud_proxy":                       "CHATGPT2API_CLOUD_PROXY",
	"cloud_proxy_enabled":               "CHATGPT2API_CLOUD_PROXY_ENABLED",
}

var envKeyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

const (
	defaultImageTaskTimeoutSeconds = 300
	minImageTaskTimeoutSeconds     = 30
	maxImageTaskTimeoutSeconds     = 3600
)

type Store struct {
	mu             sync.RWMutex
	RootDir        string
	DataDir        string
	EnvFile        string
	data           map[string]any
	storageBackend storage.Backend
}

type LinuxDoOAuthConfig struct {
	Enabled              bool
	ClientID             string
	ClientSecret         string
	AuthorizeURL         string
	TokenURL             string
	UserInfoURL          string
	Scopes               string
	RedirectURL          string
	FrontendRedirectURL  string
	TokenAuthMethod      string
	UsePKCE              bool
	UserInfoEmailPath    string
	UserInfoIDPath       string
	UserInfoUsernamePath string
}

func NewStore() (*Store, error) {
	root, err := resolveRootDir()
	if err != nil {
		return nil, err
	}

	envFile := filepath.Join(root, ".env")
	envFileValues := readEnvObject(envFile)
	s := &Store{
		RootDir: root,
		DataDir: filepath.Join(root, "data"),
		EnvFile: envFile,
		data:    map[string]any{},
	}
	if err := os.MkdirAll(s.DataDir, 0o755); err != nil {
		return nil, err
	}
	s.loadEnvFile()
	s.data = settingsFromEnvValues(envFileValues)
	return s, nil
}

func resolveRootDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if configured := strings.TrimSpace(os.Getenv("CHATGPT2API_ROOT")); configured != "" {
		return filepath.Abs(configured)
	}
	if root := findAncestorWithFile(cwd, ".env"); root != "" {
		return root, nil
	}
	if root := findAncestorWithProjectGoMod(cwd); root != "" {
		return root, nil
	}
	return filepath.Abs(cwd)
}

func findAncestorWithFile(start, name string) string {
	dir, err := filepath.Abs(start)
	if err != nil {
		return ""
	}
	for {
		info, statErr := os.Stat(filepath.Join(dir, name))
		if statErr == nil && !info.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func findAncestorWithProjectGoMod(start string) string {
	dir, err := filepath.Abs(start)
	if err != nil {
		return ""
	}
	for {
		data, readErr := os.ReadFile(filepath.Join(dir, "go.mod"))
		if readErr == nil && strings.Contains(string(data), "module chatgpt2api") {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func (s *Store) AdminUsername() string {
	value := strings.TrimSpace(os.Getenv("CHATGPT2API_ADMIN_USERNAME"))
	if value == "" {
		return "admin"
	}
	return value
}

func (s *Store) AdminPassword() string {
	return strings.TrimSpace(os.Getenv("CHATGPT2API_ADMIN_PASSWORD"))
}

func (s *Store) RegistrationEnabled() bool {
	return util.ToBool(s.settingValue("registration_enabled", false))
}

func (s *Store) RefreshAccountIntervalMinute() int {
	return intSetting(s.settingValue("refresh_account_interval_minute", 5), 5)
}

func (s *Store) ImageRetentionDays() int {
	value := intSetting(s.settingValue("image_retention_days", 30), 30)
	if value < 1 {
		return 1
	}
	return value
}

func (s *Store) ImageStorageLimitMB() int {
	value := intSetting(s.settingValue("image_storage_limit_mb", 0), 0)
	if value < 0 {
		return 0
	}
	return value
}

func (s *Store) ImageStorageLimitBytes() int64 {
	mb := s.ImageStorageLimitMB()
	if mb <= 0 {
		return 0
	}
	return int64(mb) * 1024 * 1024
}

func (s *Store) ForceImageURLResponse() bool {
	return util.ToBool(s.settingValue("force_image_url_response", false))
}

func (s *Store) DefaultImageVisibility() string {
	return normalizeImageVisibility(s.settingValue("default_image_visibility", "private"))
}

func (s *Store) LogRetentionDays() int {
	value := intSetting(s.settingValue("log_retention_days", 7), 7)
	if value < 1 {
		return 1
	}
	if value > 3650 {
		return 3650
	}
	return value
}

func (s *Store) ImageTaskTimeoutSeconds() int {
	return normalizeImageTaskTimeoutSeconds(s.settingValue("image_task_timeout_seconds", defaultImageTaskTimeoutSeconds))
}

func (s *Store) UserDefaultConcurrentLimit() int {
	value := intSetting(s.settingValue("user_default_concurrent_limit", 0), 0)
	if value < 0 {
		return 0
	}
	return value
}

func (s *Store) UserDefaultRPMLimit() int {
	value := intSetting(s.settingValue("user_default_rpm_limit", 0), 0)
	if value < 0 {
		return 0
	}
	return value
}

func (s *Store) DefaultBillingType() string {
	switch strings.ToLower(strings.TrimSpace(fmt.Sprint(s.settingValue("default_billing_type", "standard")))) {
	case "subscription":
		return "subscription"
	default:
		return "standard"
	}
}

func (s *Store) DefaultStandardBalance() int {
	value := intSetting(s.settingValue("default_standard_balance", 0), 0)
	if value < 0 {
		return 0
	}
	return value
}

func (s *Store) DefaultSubscriptionQuota() int {
	value := intSetting(s.settingValue("default_subscription_quota", 0), 0)
	if value < 0 {
		return 0
	}
	return value
}

func (s *Store) DefaultSubscriptionPeriod() string {
	switch strings.ToLower(strings.TrimSpace(fmt.Sprint(s.settingValue("default_subscription_period", "monthly")))) {
	case "daily", "weekly", "monthly":
		return strings.ToLower(strings.TrimSpace(fmt.Sprint(s.settingValue("default_subscription_period", "monthly"))))
	default:
		return "monthly"
	}
}

func (s *Store) AutoRemoveInvalidAccounts() bool {
	return util.ToBool(s.settingValue("auto_remove_invalid_accounts", false))
}

func (s *Store) AutoRemoveRateLimitedAccounts() bool {
	return util.ToBool(s.settingValue("auto_remove_rate_limited_accounts", false))
}

func (s *Store) BaseURL() string {
	return strings.TrimRight(strings.TrimSpace(fmt.Sprint(s.settingValue("base_url", ""))), "/")
}

func (s *Store) Proxy() string {
	return strings.TrimSpace(fmt.Sprint(s.settingValue("proxy", "")))
}

// CloudProxy returns the dedicated proxy URL for cloud storage operations.
// When set, cloud uploads/downloads use this proxy instead of the global proxy.
func (s *Store) CloudProxy() string {
	return strings.TrimSpace(fmt.Sprint(s.settingValue("cloud_proxy", "")))
}

// CloudProxyEnabled returns whether the dedicated cloud proxy is enabled.
// When false, cloud storage operations will not use any proxy (direct connection).
func (s *Store) CloudProxyEnabled() bool {
	return util.ToBool(s.settingValue("cloud_proxy_enabled", true))
}

func (s *Store) UpdateProxyURL() string {
	if value := strings.TrimSpace(os.Getenv("CHATGPT2API_UPDATE_PROXY_URL")); value != "" {
		return value
	}
	return s.Proxy()
}

func (s *Store) UpdateRepo() string {
	return normalizeUpdateRepo(s.settingValue("update_repo", "ZyphrZero/chatgpt2api"))
}

func (s *Store) UpdateGitHubToken() string {
	return strings.TrimSpace(fmt.Sprint(s.settingValue("update_github_token", "")))
}

func (s *Store) LogLevels() []string {
	raw := s.settingValue("log_levels", "")
	var parts []string
	switch v := raw.(type) {
	case []string:
		parts = v
	case []any:
		for _, item := range v {
			parts = append(parts, fmt.Sprint(item))
		}
	default:
		parts = strings.Split(fmt.Sprint(raw), ",")
	}
	allowed := map[string]struct{}{"debug": {}, "info": {}, "warning": {}, "error": {}}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		level := strings.ToLower(strings.TrimSpace(part))
		if _, ok := allowed[level]; ok {
			out = append(out, level)
		}
	}
	return out
}

func (s *Store) LinuxDoOAuth() LinuxDoOAuthConfig {
	s.mu.RLock()
	data := util.CopyMap(s.data)
	s.mu.RUnlock()
	return s.linuxDoOAuthFromData(data)
}

func (s *Store) linuxDoOAuthFromData(data map[string]any) LinuxDoOAuthConfig {
	redirectURL := strings.TrimSpace(fmt.Sprint(s.settingValueFromData(data, "linuxdo_redirect_url", "")))
	baseURL := strings.TrimRight(strings.TrimSpace(fmt.Sprint(s.settingValueFromData(data, "base_url", ""))), "/")
	if redirectURL == "" && baseURL != "" {
		redirectURL = baseURL + "/auth/linuxdo/oauth/callback"
	}
	return LinuxDoOAuthConfig{
		Enabled:              util.ToBool(s.settingValueFromData(data, "linuxdo_enabled", false)),
		ClientID:             strings.TrimSpace(fmt.Sprint(s.settingValueFromData(data, "linuxdo_client_id", ""))),
		ClientSecret:         strings.TrimSpace(fmt.Sprint(s.settingValueFromData(data, "linuxdo_client_secret", ""))),
		AuthorizeURL:         envString("CHATGPT2API_LINUXDO_AUTHORIZE_URL", "https://connect.linux.do/oauth2/authorize"),
		TokenURL:             envString("CHATGPT2API_LINUXDO_TOKEN_URL", "https://connect.linux.do/oauth2/token"),
		UserInfoURL:          envString("CHATGPT2API_LINUXDO_USERINFO_URL", "https://connect.linux.do/api/user"),
		Scopes:               envString("CHATGPT2API_LINUXDO_SCOPES", "user"),
		RedirectURL:          redirectURL,
		FrontendRedirectURL:  strings.TrimSpace(fmt.Sprint(s.settingValueFromData(data, "linuxdo_frontend_redirect_url", "/auth/linuxdo/callback"))),
		TokenAuthMethod:      strings.ToLower(envString("CHATGPT2API_LINUXDO_TOKEN_AUTH_METHOD", "client_secret_post")),
		UsePKCE:              envBool("CHATGPT2API_LINUXDO_USE_PKCE", false),
		UserInfoEmailPath:    envString("CHATGPT2API_LINUXDO_USERINFO_EMAIL_PATH", ""),
		UserInfoIDPath:       envString("CHATGPT2API_LINUXDO_USERINFO_ID_PATH", ""),
		UserInfoUsernamePath: envString("CHATGPT2API_LINUXDO_USERINFO_USERNAME_PATH", ""),
	}
}

func (c LinuxDoOAuthConfig) Ready() bool {
	if !c.Enabled {
		return false
	}
	if c.ClientID == "" || c.AuthorizeURL == "" || c.TokenURL == "" || c.UserInfoURL == "" || c.RedirectURL == "" {
		return false
	}
	switch c.TokenAuthMethod {
	case "", "client_secret_post", "client_secret_basic":
		return c.ClientSecret != ""
	case "none":
		return c.UsePKCE
	default:
		return false
	}
}

func (s *Store) ImagesDir() string {
	path := filepath.Join(s.DataDir, "images")
	_ = os.MkdirAll(path, 0o755)
	return path
}

func (s *Store) ImageThumbnailsDir() string {
	path := filepath.Join(s.DataDir, "image_thumbnails")
	_ = os.MkdirAll(path, 0o755)
	return path
}

func (s *Store) ImageMetadataDir() string {
	path := filepath.Join(s.DataDir, "image_metadata")
	_ = os.MkdirAll(path, 0o755)
	return path
}

func (s *Store) LoginPageImagesDir() string {
	path := filepath.Join(s.DataDir, "login_page_images")
	_ = os.MkdirAll(path, 0o755)
	return path
}

func (s *Store) LoginPageImageURL() string {
	return strings.TrimSpace(fmt.Sprint(s.settingValue("login_page_image_url", "")))
}

func (s *Store) LoginPageImageMode() string {
	return normalizeLoginPageImageMode(s.settingValue("login_page_image_mode", "contain"))
}

func (s *Store) LoginPageImageZoom() float64 {
	return clampFloat(floatSetting(s.settingValue("login_page_image_zoom", 1), 1), 1, 3)
}

func (s *Store) LoginPageImagePositionX() float64 {
	return clampFloat(floatSetting(s.settingValue("login_page_image_position_x", 50), 50), 0, 100)
}

func (s *Store) LoginPageImagePositionY() float64 {
	return clampFloat(floatSetting(s.settingValue("login_page_image_position_y", 50), 50), 0, 100)
}

func (s *Store) StorageMode() string {
	mode := strings.ToLower(strings.TrimSpace(fmt.Sprint(s.settingValue("storage_mode", ""))))
	if mode == "cloud" || mode == "local" {
		return mode
	}
	// Backward compat: if storage_mode is not set but cloud_storage_enabled is true, use "cloud".
	if util.ToBool(s.settingValue("cloud_storage_enabled", false)) {
		return "cloud"
	}
	return "local"
}

func (s *Store) CloudStorageEnabled() bool {
	return s.StorageMode() == "cloud"
}

func (s *Store) CloudStorageUploader() string {
	switch strings.ToLower(strings.TrimSpace(fmt.Sprint(s.settingValue("cloud_storage_uploader", "auto")))) {
	case "a4":
		return "a4"
	case "a1":
		return "a1"
	case "s3":
		return "s3"
	default:
		return "auto"
	}
}

func (s *Store) A4Cookie() string {
	return strings.TrimSpace(fmt.Sprint(s.settingValue("a4_cookie", "")))
}

func (s *Store) S3Endpoint() string {
	return strings.TrimSpace(fmt.Sprint(s.settingValue("s3_endpoint", "")))
}

func (s *Store) S3Region() string {
	v := strings.TrimSpace(fmt.Sprint(s.settingValue("s3_region", "")))
	if v == "" {
		return "auto"
	}
	return v
}

func (s *Store) S3AccessKeyID() string {
	return strings.TrimSpace(fmt.Sprint(s.settingValue("s3_access_key_id", "")))
}

func (s *Store) S3SecretAccessKey() string {
	return strings.TrimSpace(fmt.Sprint(s.settingValue("s3_secret_access_key", "")))
}

func (s *Store) S3Bucket() string {
	return strings.TrimSpace(fmt.Sprint(s.settingValue("s3_bucket", "")))
}

func (s *Store) S3PublicURL() string {
	return strings.TrimSpace(fmt.Sprint(s.settingValue("s3_public_url", "")))
}

func (s *Store) S3PathPrefix() string {
	v := strings.TrimSpace(fmt.Sprint(s.settingValue("s3_path_prefix", "")))
	if v != "" && !strings.HasSuffix(v, "/") {
		v += "/"
	}
	return v
}

func (s *Store) S3ForcePathStyle() bool {
	return util.ToBool(s.settingValue("s3_force_path_style", false))
}

func (s *Store) CloudCookieCheckIntervalMinutes() int {
	minutes := intSetting(s.settingValue("cloud_cookie_check_interval", 240), 240)
	if minutes < 1 {
		return 1
	}
	return minutes
}

func (s *Store) CloudCookieCheckInterval() time.Duration {
	return time.Duration(s.CloudCookieCheckIntervalMinutes()) * time.Minute
}

func (s *Store) Get() map[string]any {
	s.mu.RLock()
	data := util.CopyMap(s.data)
	s.mu.RUnlock()
	delete(data, "image_concurrent_limit")
	data["refresh_account_interval_minute"] = s.RefreshAccountIntervalMinute()
	data["image_task_timeout_seconds"] = s.ImageTaskTimeoutSeconds()
	data["user_default_concurrent_limit"] = s.UserDefaultConcurrentLimit()
	data["user_default_rpm_limit"] = s.UserDefaultRPMLimit()
	data["default_billing_type"] = s.DefaultBillingType()
	data["default_standard_balance"] = s.DefaultStandardBalance()
	data["default_subscription_quota"] = s.DefaultSubscriptionQuota()
	data["default_subscription_period"] = s.DefaultSubscriptionPeriod()
	data["image_retention_days"] = s.ImageRetentionDays()
	data["image_storage_limit_mb"] = s.ImageStorageLimitMB()
	data["force_image_url_response"] = s.ForceImageURLResponse()
	data["default_image_visibility"] = s.DefaultImageVisibility()
	data["log_retention_days"] = s.LogRetentionDays()
	data["auto_remove_invalid_accounts"] = s.AutoRemoveInvalidAccounts()
	data["auto_remove_rate_limited_accounts"] = s.AutoRemoveRateLimitedAccounts()
	data["log_levels"] = s.LogLevels()
	data["proxy"] = s.Proxy()
	data["cloud_proxy"] = s.CloudProxy()
	data["cloud_proxy_enabled"] = s.CloudProxyEnabled()
	data["base_url"] = s.BaseURL()
	data["registration_enabled"] = s.RegistrationEnabled()
	linuxdo := s.LinuxDoOAuth()
	data["linuxdo_enabled"] = linuxdo.Enabled
	data["linuxdo_client_id"] = linuxdo.ClientID
	data["linuxdo_client_secret_configured"] = linuxdo.ClientSecret != ""
	data["linuxdo_redirect_url"] = linuxdo.RedirectURL
	data["linuxdo_frontend_redirect_url"] = linuxdo.FrontendRedirectURL
	data["update_repo"] = s.UpdateRepo()
	data["update_github_token_configured"] = s.UpdateGitHubToken() != ""
	data["login_page_image_url"] = s.LoginPageImageURL()
	data["login_page_image_mode"] = s.LoginPageImageMode()
	data["login_page_image_zoom"] = s.LoginPageImageZoom()
	data["login_page_image_position_x"] = s.LoginPageImagePositionX()
	data["login_page_image_position_y"] = s.LoginPageImagePositionY()
	data["storage_mode"] = s.StorageMode()
	data["cloud_storage_enabled"] = s.CloudStorageEnabled()
	data["cloud_storage_uploader"] = s.CloudStorageUploader()
	data["a4_cookie_configured"] = s.A4Cookie() != ""
	data["cloud_cookie_check_interval"] = s.CloudCookieCheckIntervalMinutes()
	data["s3_endpoint"] = s.S3Endpoint()
	data["s3_region"] = s.S3Region()
	data["s3_access_key_id"] = s.S3AccessKeyID()
	data["s3_secret_access_key_configured"] = s.S3SecretAccessKey() != ""
	data["s3_bucket"] = s.S3Bucket()
	data["s3_public_url"] = s.S3PublicURL()
	data["s3_path_prefix"] = s.S3PathPrefix()
	data["s3_force_path_style"] = s.S3ForcePathStyle()
	delete(data, "a4_cookie")
	delete(data, "s3_secret_access_key")
	delete(data, "linuxdo_client_secret")
	delete(data, "update_github_token")
	return data
}

func (s *Store) Update(data map[string]any) (map[string]any, error) {
	s.mu.Lock()
	next := util.CopyMap(s.data)
	for key, value := range data {
		if key == "linuxdo_client_secret_configured" {
			continue
		}
		if key == "update_github_token_configured" {
			continue
		}
		if key == "linuxdo_client_secret" && strings.TrimSpace(fmt.Sprint(value)) == "" {
			continue
		}
		if key == "update_github_token" && strings.TrimSpace(fmt.Sprint(value)) == "" {
			continue
		}
		if key == "a4_cookie_configured" {
			continue
		}
		if key == "a4_cookie" && strings.TrimSpace(fmt.Sprint(value)) == "" {
			continue
		}
		if key == "cloud_proxy" && strings.TrimSpace(fmt.Sprint(value)) == "" {
			continue
		}
		if key == "s3_secret_access_key_configured" {
			continue
		}
		if key == "s3_secret_access_key" && strings.TrimSpace(fmt.Sprint(value)) == "" {
			continue
		}
		next[key] = value
	}
	delete(next, "image_concurrent_limit")
	if value, ok := next["login_page_image_mode"]; ok {
		next["login_page_image_mode"] = normalizeLoginPageImageMode(value)
	}
	if value, ok := next["image_task_timeout_seconds"]; ok {
		next["image_task_timeout_seconds"] = normalizeImageTaskTimeoutSeconds(value)
	}
	if value, ok := next["image_storage_limit_mb"]; ok {
		next["image_storage_limit_mb"] = normalizeNonNegativeInt(value)
	}
	if value, ok := next["default_billing_type"]; ok {
		next["default_billing_type"] = normalizeDefaultBillingType(value)
	}
	if value, ok := next["default_subscription_period"]; ok {
		next["default_subscription_period"] = normalizeDefaultSubscriptionPeriod(value)
	}
	// Sync cloud_storage_enabled → storage_mode for backward compatibility.
	if _, ok := next["cloud_storage_enabled"]; ok {
		if util.ToBool(next["cloud_storage_enabled"]) {
			next["storage_mode"] = "cloud"
		} else {
			next["storage_mode"] = "local"
		}
	}
	if value, ok := next["storage_mode"]; ok {
		next["storage_mode"] = normalizeStorageMode(value)
	}
	if value, ok := next["cloud_storage_uploader"]; ok {
		next["cloud_storage_uploader"] = normalizeCloudStorageUploader(value)
	}
	if value, ok := next["default_image_visibility"]; ok {
		next["default_image_visibility"] = normalizeImageVisibility(value)
	}
	next["update_repo"] = normalizeUpdateRepo(util.ValueOr(next["update_repo"], "ZyphrZero/chatgpt2api"))
	if err := s.validateSettingsUpdateLocked(next); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.data = next
	err := s.saveLocked()
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return s.Get(), nil
}

func (s *Store) CleanupOldImages() int {
	cutoff := time.Now().Add(-time.Duration(s.ImageRetentionDays()) * 24 * time.Hour)
	removed := 0
	for _, dir := range []string{s.ImagesDir(), s.ImageThumbnailsDir(), s.ImageMetadataDir()} {
		_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			info, statErr := d.Info()
			if statErr == nil && info.ModTime().Before(cutoff) {
				if os.Remove(path) == nil {
					removed++
				}
			}
			return nil
		})
		removeEmptyDirs(dir)
	}
	return removed
}

func (s *Store) StorageBackend() (storage.Backend, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.storageBackend != nil {
		return s.storageBackend, nil
	}
	backend, err := storage.NewBackendFromEnv(s.DataDir)
	if err != nil {
		return nil, err
	}
	s.storageBackend = backend
	return backend, nil
}

func (s *Store) settingValue(key string, fallback any) any {
	envKey := settingEnvKeys[key]
	s.mu.RLock()
	if value, ok := s.data[key]; ok {
		s.mu.RUnlock()
		return value
	}
	s.mu.RUnlock()
	if envKey != "" {
		if value, ok := os.LookupEnv(envKey); ok {
			return value
		}
	}
	return fallback
}

func (s *Store) settingValueFromData(data map[string]any, key string, fallback any) any {
	if data != nil {
		if value, ok := data[key]; ok {
			return value
		}
	}
	if envKey := settingEnvKeys[key]; envKey != "" {
		if value, ok := os.LookupEnv(envKey); ok {
			return value
		}
	}
	return fallback
}

func (s *Store) validateSettingsUpdateLocked(data map[string]any) error {
	if err := validateUpdateRepo(util.Clean(util.ValueOr(data["update_repo"], "ZyphrZero/chatgpt2api"))); err != nil {
		return err
	}
	linuxdo := s.linuxDoOAuthFromData(data)
	if !linuxdo.Enabled {
		return nil
	}
	if linuxdo.ClientID == "" {
		return errors.New("Linuxdo Client ID is required when enabled")
	}
	if linuxdo.RedirectURL == "" {
		return errors.New("Linuxdo Redirect URL is required when enabled")
	}
	if linuxdo.FrontendRedirectURL == "" {
		return errors.New("Linuxdo Frontend Redirect URL is required when enabled")
	}
	if err := validateAbsoluteHTTPURL(linuxdo.RedirectURL); err != nil {
		return errors.New("Linuxdo Redirect URL must be an absolute http(s) URL")
	}
	if err := validateFrontendRedirectURL(linuxdo.FrontendRedirectURL); err != nil {
		return errors.New("Linuxdo Frontend Redirect URL must be an absolute http(s) URL or a relative path")
	}
	switch linuxdo.TokenAuthMethod {
	case "", "client_secret_post", "client_secret_basic":
		if linuxdo.ClientSecret == "" {
			return errors.New("Linuxdo Client Secret is required when enabled")
		}
	case "none":
		if !linuxdo.UsePKCE {
			return errors.New("Linuxdo PKCE must be enabled when token auth method is none")
		}
	default:
		return errors.New("Linuxdo token auth method must be one of client_secret_post, client_secret_basic, none")
	}
	return nil
}

func normalizeUpdateRepo(value any) string {
	repo := strings.Trim(strings.TrimSpace(fmt.Sprint(value)), "/")
	if repo == "" {
		return "ZyphrZero/chatgpt2api"
	}
	return repo
}

func validateUpdateRepo(value string) error {
	if !regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`).MatchString(value) {
		return errors.New("Update repository must use owner/repo format")
	}
	return nil
}

func validateAbsoluteHTTPURL(value string) error {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("scheme must be http or https")
	}
	if parsed.Host == "" {
		return errors.New("host is required")
	}
	return nil
}

func validateFrontendRedirectURL(value string) error {
	value = strings.TrimSpace(value)
	if strings.ContainsAny(value, "\r\n") {
		return errors.New("newlines are not allowed")
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return err
	}
	if parsed.Scheme != "" {
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return errors.New("scheme must be http or https")
		}
		if parsed.Host == "" {
			return errors.New("host is required")
		}
		return nil
	}
	if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return errors.New("relative path must start with one slash")
	}
	return nil
}

func (s *Store) saveLocked() error {
	updates := map[string]string{}
	keys := make([]string, 0, len(settingEnvKeys))
	for key := range settingEnvKeys {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if value, ok := s.data[key]; ok {
			updates[settingEnvKeys[key]] = stringifyEnvValue(value)
		}
	}
	if err := writeEnvUpdates(s.EnvFile, updates); err != nil {
		return err
	}
	for key, value := range updates {
		_ = os.Setenv(key, value)
	}
	return nil
}

func (s *Store) loadEnvFile() {
	for key, value := range readEnvObject(s.EnvFile) {
		if _, ok := os.LookupEnv(key); !ok {
			_ = os.Setenv(key, value)
		}
	}
}

func settingsFromEnvValues(values map[string]string) map[string]any {
	settings := map[string]any{}
	for settingKey, envKey := range settingEnvKeys {
		if value, ok := values[envKey]; ok {
			settings[settingKey] = value
		}
	}
	return settings
}

func intSetting(value any, fallback int) int {
	switch v := value.(type) {
	case int:
		return v
	case int8:
		return int(v)
	case int16:
		return int(v)
	case int32:
		return int(v)
	case int64:
		return int(v)
	case uint:
		return int(v)
	case uint8:
		return int(v)
	case uint16:
		return int(v)
	case uint32:
		return int(v)
	case uint64:
		return int(v)
	case float32:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n)
		}
		if f, err := v.Float64(); err == nil {
			return int(f)
		}
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err == nil {
			return n
		}
	}
	return fallback
}

func floatSetting(value any, fallback float64) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case string:
		n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err == nil {
			return n
		}
	}
	return fallback
}

func normalizeLoginPageImageMode(value any) string {
	mode := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
	switch mode {
	case "cover", "contain", "fill":
		return mode
	default:
		return "contain"
	}
}

func normalizeImageTaskTimeoutSeconds(value any) int {
	seconds := intSetting(value, defaultImageTaskTimeoutSeconds)
	if seconds < minImageTaskTimeoutSeconds {
		return minImageTaskTimeoutSeconds
	}
	if seconds > maxImageTaskTimeoutSeconds {
		return maxImageTaskTimeoutSeconds
	}
	return seconds
}

func normalizeNonNegativeInt(value any) int {
	n := intSetting(value, 0)
	if n < 0 {
		return 0
	}
	return n
}

func normalizeDefaultBillingType(value any) string {
	switch strings.ToLower(strings.TrimSpace(fmt.Sprint(value))) {
	case "subscription":
		return "subscription"
	default:
		return "standard"
	}
}

func normalizeDefaultSubscriptionPeriod(value any) string {
	switch strings.ToLower(strings.TrimSpace(fmt.Sprint(value))) {
	case "daily", "weekly", "monthly":
		return strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
	default:
		return "monthly"
	}
}

func normalizeStorageMode(value any) string {
	switch strings.ToLower(strings.TrimSpace(fmt.Sprint(value))) {
	case "cloud":
		return "cloud"
	default:
		return "local"
	}
}

func normalizeCloudStorageUploader(value any) string {
	switch strings.ToLower(strings.TrimSpace(fmt.Sprint(value))) {
	case "a4":
		return "a4"
	case "a1":
		return "a1"
	case "s3":
		return "s3"
	default:
		return "auto"
	}
}

func normalizeImageVisibility(value any) string {
	switch strings.ToLower(strings.TrimSpace(fmt.Sprint(value))) {
	case "public":
		return "public"
	default:
		return "private"
	}
}

func clampFloat(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func envString(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return strings.TrimSpace(value)
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	if value, ok := os.LookupEnv(key); ok {
		value = strings.ToLower(strings.TrimSpace(value))
		return value == "1" || value == "true" || value == "yes" || value == "on"
	}
	return fallback
}

func readEnvObject(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		if info, statErr := os.Stat(path); statErr == nil && info.IsDir() {
			fmt.Fprintf(os.Stderr, "Warning: .env at %q is a directory, ignoring it.\n", path)
		}
		return map[string]string{}
	}
	result := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := parseEnvAssignment(line)
		if ok {
			result[key] = value
		}
	}
	return result
}

func parseEnvAssignment(line string) (string, string, bool) {
	stripped := strings.TrimSpace(line)
	if stripped == "" || strings.HasPrefix(stripped, "#") {
		return "", "", false
	}
	stripped = strings.TrimSpace(strings.TrimPrefix(stripped, "export "))
	key, value, ok := strings.Cut(stripped, "=")
	if !ok {
		return "", "", false
	}
	key = strings.TrimSpace(key)
	if !envKeyRE.MatchString(key) {
		return "", "", false
	}
	return key, unquoteEnvValue(value), true
}

func unquoteEnvValue(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && value[0] == value[len(value)-1] && (value[0] == '"' || value[0] == '\'') {
		inner := value[1 : len(value)-1]
		if value[0] == '"' {
			inner = strings.ReplaceAll(inner, `\n`, "\n")
			inner = strings.ReplaceAll(inner, `\r`, "\r")
			inner = strings.ReplaceAll(inner, `\t`, "\t")
			inner = strings.ReplaceAll(inner, `\"`, `"`)
			inner = strings.ReplaceAll(inner, `\\`, `\`)
		}
		return inner
	}
	for index, char := range value {
		if char == '#' && (index == 0 || value[index-1] == ' ' || value[index-1] == '\t') {
			return strings.TrimRight(value[:index], " \t")
		}
	}
	return value
}

func stringifyEnvValue(value any) string {
	switch v := value.(type) {
	case bool:
		if v {
			return "true"
		}
		return "false"
	case []string:
		return strings.Join(v, ",")
	case []any:
		items := make([]string, 0, len(v))
		for _, item := range v {
			if s := strings.TrimSpace(fmt.Sprint(item)); s != "" {
				items = append(items, s)
			}
		}
		return strings.Join(items, ",")
	default:
		return strings.TrimSpace(fmt.Sprint(util.ValueOr(value, "")))
	}
}

func writeEnvUpdates(path string, updates map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var lines []string
	if data, err := os.ReadFile(path); err == nil {
		lines = strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines = lines[:len(lines)-1]
		}
	}
	pending := map[string]string{}
	for key, value := range updates {
		pending[key] = value
	}
	next := make([]string, 0, len(lines)+len(updates)+1)
	for _, line := range lines {
		key, _, ok := parseEnvAssignment(line)
		if ok {
			if value, exists := pending[key]; exists {
				next = append(next, formatEnvAssignment(key, value))
				delete(pending, key)
				continue
			}
		}
		next = append(next, line)
	}
	if len(pending) > 0 {
		if len(next) > 0 && strings.TrimSpace(next[len(next)-1]) != "" {
			next = append(next, "")
		}
		keys := make([]string, 0, len(pending))
		for key := range pending {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			next = append(next, formatEnvAssignment(key, pending[key]))
		}
	}
	return os.WriteFile(path, []byte(strings.TrimRight(strings.Join(next, "\n"), "\n")+"\n"), 0o644)
}

func formatEnvAssignment(key, value string) string {
	return key + "=" + formatEnvValue(value)
}

func formatEnvValue(value string) string {
	if value == "" {
		return ""
	}
	if regexp.MustCompile(`^[A-Za-z0-9_./:@%+\-,]*$`).MatchString(value) {
		return value
	}
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	value = strings.ReplaceAll(value, "\n", `\n`)
	return `"` + value + `"`
}

func removeEmptyDirs(root string) {
	var dirs []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() && path != root {
			dirs = append(dirs, path)
		}
		return nil
	})
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, dir := range dirs {
		_ = os.Remove(dir)
	}
}
