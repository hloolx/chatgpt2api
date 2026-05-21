package httpapi

import (
	"net"
	"net/http"
	"net/url"
	"strings"

	"chatgpt2api/internal/util"
	"chatgpt2api/internal/version"
)

type routeMatch int

const (
	exactRoute routeMatch = iota
	prefixRoute
)

type appRoute struct {
	method  string
	path    string
	match   routeMatch
	handler http.HandlerFunc
}

func (a *App) Handler() http.Handler {
	routes := a.routes()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.serveObservedHTTP(w, r, routes)
	})
}

func (a *App) routes() []appRoute {
	return []appRoute{
		exact(http.MethodGet, "/v1/models", a.handleModels),
		exact(http.MethodPost, "/v1/images/generations", a.handleImageGenerations),
		exact(http.MethodPost, "/v1/images/edits", a.handleImageEdits),
		exact(http.MethodPost, "/v1/chat/completions", a.handleChatCompletions),
		exact(http.MethodPost, "/v1/responses", a.handleResponses),
		exact(http.MethodPost, "/v1/messages", a.handleMessages),

		exact(http.MethodPost, "/auth/login", a.handleLogin),
		exact(http.MethodPost, "/auth/logout", a.handleLogout),
		exact(http.MethodPost, "/auth/register", a.handleAccountRegister),
		exact(http.MethodGet, "/auth/session", a.handleSession),
		exact("", "/auth/providers", a.handleAuthProviders),
		exact("", "/auth/linuxdo/start", a.handleLinuxDoOAuthStart),
		exact("", "/auth/linuxdo/oauth/callback", a.handleLinuxDoOAuthCallback),
		exact(http.MethodGet, "/auth/linuxdo/callback", a.serveWeb),
		exact(http.MethodHead, "/auth/linuxdo/callback", a.serveWeb),
		exact(http.MethodGet, "/health", a.handleHealth),
		exact(http.MethodGet, "/version", func(w http.ResponseWriter, _ *http.Request) {
			util.WriteJSON(w, http.StatusOK, map[string]any{"version": version.Get()})
		}),

		exact("", "/api/announcements", a.handlePublicAnnouncements),
		subtree("/api/admin/system", a.handleAdminSystem),
		subtree("/api/admin/announcements", a.handleAdminAnnouncements),
		subtree("/api/admin/roles", a.handleAdminRoles),
		subtree("/api/admin/users", a.handleAdminUsers),
		exact("", "/api/profile", a.handleProfile),
		exact(http.MethodPost, "/api/profile/password", a.handleProfilePassword),
		subtree("/api/profile/api-key", a.handleProfileAPIKey),
		subtree("/api/profile/prompt-favorites", a.handleProfilePromptFavorites),
		subtree("/api/auth/users", a.handleUserKeys),
		subtree("/api/accounts", a.handleAccounts),
		subtree("/api/cpa/pools", a.handleCPA),
		subtree("/api/sub2api/servers", a.handleSub2API),
		subtree("/api/creation-tasks", a.handleCreationTasks),
		subtree("/api/register", a.handleRegister),
		subtree("/api/hlool-mail", a.handleHLOOLMail),
		exact("", "/api/settings", a.handleSettings),
		exact("", "/api/settings/login-page-image", a.handleLoginPageImageSettings),
		exact(http.MethodGet, "/api/app-meta", a.handleAppMeta),
		exact(http.MethodGet, "/api/admin/permissions", a.handlePermissionCatalog),
		exact("", "/api/images/visibility", a.handleImageVisibility),
		exact("", "/api/images", a.handleImages),
		exact("", "/api/images/storage-governance", a.handleImageStorageGovernance),
		exact("", "/api/logs/governance", a.handleLogGovernance),
		exact(http.MethodGet, "/api/logs", a.handleLogs),
		exact("", "/api/proxy", a.handleProxy),
		exact("", "/api/proxy/test", a.handleProxy),
		exact(http.MethodGet, "/api/storage/info", a.handleStorageInfo),

		exact(http.MethodDelete, "/api/admin/cloud/cookies", a.handleCloudCookieDelete),
		exact("", "/api/admin/cloud/cookies", a.handleCloudCookies),
		exact(http.MethodPost, "/api/admin/cloud/cookies/check", a.handleCloudCookieCheck),
		exact(http.MethodGet, "/api/admin/cloud/status", a.handleCloudStorageStatus),
		exact(http.MethodPost, "/api/admin/cloud/test-upload", a.handleCloudTestUpload),

		prefix("/images/", a.handleImageFile),
		prefix("/image-references/", a.handleImageReferenceFile),
		prefix("/image-thumbnails/", a.handleImageThumbnail),
		prefix("/login-page-images/", http.StripPrefix("/login-page-images/", http.FileServer(http.Dir(a.config.LoginPageImagesDir()))).ServeHTTP),
	}
}

func exact(method, path string, handler http.HandlerFunc) appRoute {
	return appRoute{method: method, path: path, match: exactRoute, handler: handler}
}

func prefix(path string, handler http.HandlerFunc) appRoute {
	return appRoute{path: path, match: prefixRoute, handler: handler}
}

func subtree(path string, handler http.HandlerFunc) appRoute {
	return prefix(path, handler)
}

func (a *App) serveHTTP(w http.ResponseWriter, r *http.Request, routes []appRoute) {
	applyCORS(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if route := matchAppRoute(routes, r.Method, r.URL.Path); route != nil {
		route.handler(w, r)
		return
	}
	if isAPISpace(r.URL.Path) {
		http.NotFound(w, r)
		return
	}
	a.serveWeb(w, r)
}

func matchAppRoute(routes []appRoute, method, path string) *appRoute {
	for i := range routes {
		route := &routes[i]
		if route.method != "" && route.method != method {
			continue
		}
		switch route.match {
		case exactRoute:
			if path == route.path {
				return route
			}
		case prefixRoute:
			if path == route.path || strings.HasPrefix(path, strings.TrimRight(route.path, "/")+"/") {
				return route
			}
		}
	}
	return nil
}

func isAPISpace(path string) bool {
	return path == "/api" || strings.HasPrefix(path, "/api/") ||
		path == "/auth" || strings.HasPrefix(path, "/auth/") ||
		path == "/v1" || strings.HasPrefix(path, "/v1/")
}

func applyCORS(w http.ResponseWriter, r *http.Request) {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin != "" && isAllowedCredentialedOrigin(origin, r.Host) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Add("Vary", "Origin")
	} else {
		w.Header().Set("Access-Control-Allow-Origin", "*")
	}
	if requestedMethod := strings.TrimSpace(r.Header.Get("Access-Control-Request-Method")); requestedMethod != "" {
		w.Header().Set("Access-Control-Allow-Methods", requestedMethod)
		w.Header().Add("Vary", "Access-Control-Request-Method")
	} else {
		w.Header().Set("Access-Control-Allow-Methods", "GET,HEAD,POST,PUT,PATCH,DELETE,OPTIONS")
	}
	if requestedHeaders := strings.TrimSpace(r.Header.Get("Access-Control-Request-Headers")); requestedHeaders != "" {
		w.Header().Set("Access-Control-Allow-Headers", requestedHeaders)
		w.Header().Add("Vary", "Access-Control-Request-Headers")
	} else {
		w.Header().Set("Access-Control-Allow-Headers", "*")
	}
}

func isAllowedCredentialedOrigin(origin, requestHost string) bool {
	originURL, err := url.Parse(origin)
	if err != nil || originURL.Scheme == "" || originURL.Hostname() == "" {
		return false
	}
	requestHostname := requestHost
	if host, _, err := net.SplitHostPort(requestHost); err == nil {
		requestHostname = host
	}
	requestHostname = strings.Trim(requestHostname, "[]")
	originHostname := originURL.Hostname()
	return strings.EqualFold(originHostname, requestHostname) ||
		isLoopbackHostname(originHostname) && isLoopbackHostname(requestHostname)
}

func isLoopbackHostname(hostname string) bool {
	switch strings.ToLower(strings.TrimSpace(hostname)) {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}
