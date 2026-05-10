package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"chatgpt2api/internal/service"
	"chatgpt2api/internal/util"
)

func (a *App) handleUserKeys(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	filter, owner, canManage := userKeyScope(identity)
	if !canManage {
		util.WriteError(w, http.StatusForbidden, "Linuxdo login or admin permission required")
		return
	}
	base := "/api/auth/users"
	if r.URL.Path == base {
		switch r.Method {
		case http.MethodGet:
			items := a.auth.ListKeys(filter)
			if identity.Role != service.AuthRoleAdmin {
				items = a.auth.ListSingleAPIKeyForOwner(identity.OwnerID)
			}
			util.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
		case http.MethodPost:
			body, _ := readJSONMap(r)
			var item map[string]any
			var raw string
			var err error
			if identity.Role == service.AuthRoleAdmin {
				item, raw, err = a.auth.CreateAPIKey(service.AuthRoleUser, util.Clean(body["name"]), owner)
			} else {
				item, raw, err = a.auth.UpsertAPIKeyForOwner(util.Clean(body["name"]), owner)
			}
			if err != nil {
				util.WriteError(w, http.StatusBadRequest, err.Error())
				return
			}
			util.WriteJSON(w, http.StatusOK, map[string]any{"item": item, "key": raw, "items": a.auth.ListKeys(filter)})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	parts := splitPath(r.URL.Path)
	if len(parts) < 4 || parts[0] != "api" || parts[1] != "auth" || parts[2] != "users" {
		http.NotFound(w, r)
		return
	}
	keyID := parts[3]
	if len(parts) == 5 && parts[4] == "key" {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		key, found := a.auth.RevealKey(keyID, filter)
		if !found {
			util.WriteError(w, http.StatusNotFound, "user key not found")
			return
		}
		util.WriteJSON(w, http.StatusOK, map[string]any{"key": key})
		return
	}
	if len(parts) != 4 {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodPost:
		body, _ := readJSONMap(r)
		updates := map[string]any{}
		if value, ok := body["name"]; ok {
			updates["name"] = value
		}
		if value, ok := body["enabled"]; ok {
			updates["enabled"] = value
		}
		if len(updates) == 0 {
			util.WriteError(w, http.StatusBadRequest, "no updates provided")
			return
		}
		item := a.auth.UpdateKey(keyID, updates, filter)
		if item == nil {
			util.WriteError(w, http.StatusNotFound, "user key not found")
			return
		}
		util.WriteJSON(w, http.StatusOK, map[string]any{"item": item, "items": a.auth.ListKeys(filter)})
	case http.MethodDelete:
		if !a.auth.DeleteKey(keyID, filter) {
			util.WriteError(w, http.StatusNotFound, "user key not found")
			return
		}
		util.WriteJSON(w, http.StatusOK, map[string]any{"items": a.auth.ListKeys(filter)})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func userKeyScope(identity service.Identity) (service.AuthKeyFilter, service.AuthOwner, bool) {
	filter := service.AuthKeyFilter{Role: service.AuthRoleUser, Kind: service.AuthKindAPIKey}
	if identity.Role == service.AuthRoleAdmin {
		return filter, service.AuthOwner{}, true
	}
	if identity.Role != service.AuthRoleUser || identity.OwnerID == "" {
		return service.AuthKeyFilter{}, service.AuthOwner{}, false
	}
	filter.OwnerID = identity.OwnerID
	return filter, service.AuthOwner{ID: identity.OwnerID, Name: identity.Name, Provider: identity.Provider}, true
}

func (a *App) handleProfile(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		a.writeLoginResponse(w, identity, "")
	case http.MethodPost:
		body, err := readJSONMap(r)
		if err != nil {
			util.WriteError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		updated, err := a.auth.UpdateProfileName(identity, util.Clean(body["name"]))
		if err != nil {
			util.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.writeLoginResponse(w, *updated, "")
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *App) handleProfilePassword(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	body, err := readJSONMap(r)
	if err != nil {
		util.WriteError(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if err := a.auth.ChangeProfilePassword(identity, util.Clean(body["current_password"]), util.Clean(body["new_password"])); err != nil {
		util.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	util.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) handleProfileAPIKey(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	filter, ok := profileAPIKeyFilter(identity)
	if !ok {
		util.WriteError(w, http.StatusForbidden, "profile API key requires a bound user account")
		return
	}
	base := "/api/profile/api-key"
	if r.URL.Path == base {
		switch r.Method {
		case http.MethodGet:
			util.WriteJSON(w, http.StatusOK, map[string]any{"items": a.auth.ListPersonalAPIKey(identity)})
		case http.MethodPost:
			body, _ := readJSONMap(r)
			item, raw, err := a.auth.UpsertPersonalAPIKey(identity, util.Clean(body["name"]))
			if err != nil {
				util.WriteError(w, http.StatusBadRequest, err.Error())
				return
			}
			util.WriteJSON(w, http.StatusOK, map[string]any{"item": item, "key": raw, "items": a.auth.ListPersonalAPIKey(identity)})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}

	parts := splitPath(r.URL.Path)
	if len(parts) < 4 || parts[0] != "api" || parts[1] != "profile" || parts[2] != "api-key" {
		http.NotFound(w, r)
		return
	}
	keyID := parts[3]
	if len(parts) == 5 && parts[4] == "key" {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		key, found := a.auth.RevealKey(keyID, filter)
		if !found {
			util.WriteError(w, http.StatusNotFound, "profile API key not found")
			return
		}
		util.WriteJSON(w, http.StatusOK, map[string]any{"key": key})
		return
	}
	if len(parts) != 4 {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodPost:
		body, _ := readJSONMap(r)
		updates := map[string]any{}
		if value, ok := body["name"]; ok {
			updates["name"] = value
		}
		if value, ok := body["enabled"]; ok {
			updates["enabled"] = value
		}
		if len(updates) == 0 {
			util.WriteError(w, http.StatusBadRequest, "no updates provided")
			return
		}
		item := a.auth.UpdateKey(keyID, updates, filter)
		if item == nil {
			util.WriteError(w, http.StatusNotFound, "profile API key not found")
			return
		}
		util.WriteJSON(w, http.StatusOK, map[string]any{"item": item, "items": a.auth.ListPersonalAPIKey(identity)})
	case http.MethodDelete:
		if !a.auth.DeleteKey(keyID, filter) {
			util.WriteError(w, http.StatusNotFound, "profile API key not found")
			return
		}
		util.WriteJSON(w, http.StatusOK, map[string]any{"items": a.auth.ListPersonalAPIKey(identity)})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func profileAPIKeyFilter(identity service.Identity) (service.AuthKeyFilter, bool) {
	role := identity.Role
	if role != service.AuthRoleAdmin && role != service.AuthRoleUser {
		return service.AuthKeyFilter{}, false
	}
	ownerID := util.Clean(identity.OwnerID)
	if ownerID == "" {
		return service.AuthKeyFilter{}, false
	}
	return service.AuthKeyFilter{Role: role, Kind: service.AuthKindAPIKey, OwnerID: ownerID}, true
}

func (a *App) handleProfilePromptFavorites(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	ownerID := util.Clean(identity.OwnerID)
	if ownerID == "" {
		util.WriteError(w, http.StatusForbidden, "prompt favorites require a bound user account")
		return
	}

	base := "/api/profile/prompt-favorites"
	if r.URL.Path == base {
		switch r.Method {
		case http.MethodGet:
			util.WriteJSON(w, http.StatusOK, map[string]any{"items": a.prompts.List(ownerID)})
		case http.MethodPost:
			body, err := readJSONMap(r)
			if err != nil {
				util.WriteError(w, http.StatusBadRequest, "invalid json body")
				return
			}
			item, err := a.prompts.Upsert(ownerID, body)
			if err != nil {
				util.WriteError(w, http.StatusBadRequest, err.Error())
				return
			}
			util.WriteJSON(w, http.StatusOK, map[string]any{"item": item, "items": a.prompts.List(ownerID)})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}

	parts := splitPath(r.URL.Path)
	if len(parts) != 4 || parts[0] != "api" || parts[1] != "profile" || parts[2] != "prompt-favorites" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !a.prompts.Delete(ownerID, parts[3]) {
		util.WriteError(w, http.StatusNotFound, "prompt favorite not found")
		return
	}
	util.WriteJSON(w, http.StatusOK, map[string]any{"items": a.prompts.List(ownerID)})
}

func (a *App) handleAdminRoles(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireIdentity(w, r, ""); !ok {
		return
	}
	base := "/api/admin/roles"
	if r.URL.Path == base {
		switch r.Method {
		case http.MethodGet:
			util.WriteJSON(w, http.StatusOK, map[string]any{"items": a.auth.ListRoles()})
		case http.MethodPost:
			body, _ := readJSONMap(r)
			item, err := a.auth.CreateRole(body)
			if err != nil {
				util.WriteError(w, http.StatusBadRequest, err.Error())
				return
			}
			util.WriteJSON(w, http.StatusOK, map[string]any{"item": item, "items": a.auth.ListRoles()})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}

	parts := splitPath(r.URL.Path)
	if len(parts) != 4 || parts[0] != "api" || parts[1] != "admin" || parts[2] != "roles" {
		http.NotFound(w, r)
		return
	}
	roleID := parts[3]
	switch r.Method {
	case http.MethodPost:
		body, _ := readJSONMap(r)
		item, err := a.auth.UpdateRole(roleID, body)
		if err != nil {
			status := http.StatusBadRequest
			if err.Error() == "role not found" {
				status = http.StatusNotFound
			}
			util.WriteError(w, status, err.Error())
			return
		}
		util.WriteJSON(w, http.StatusOK, map[string]any{"item": item, "items": a.auth.ListRoles()})
	case http.MethodDelete:
		deleted, err := a.auth.DeleteRole(roleID)
		if err != nil {
			status := http.StatusBadRequest
			if err.Error() == "role is assigned to users" {
				status = http.StatusConflict
			}
			util.WriteError(w, status, err.Error())
			return
		}
		if !deleted {
			util.WriteError(w, http.StatusNotFound, "role not found")
			return
		}
		util.WriteJSON(w, http.StatusOK, map[string]any{"items": a.auth.ListRoles()})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *App) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireIdentity(w, r, ""); !ok {
		return
	}
	base := "/api/admin/users"
	if r.URL.Path == base {
		switch r.Method {
		case http.MethodGet:
			response, err := a.managedUsersResponse(r)
			if err != nil {
				util.WriteError(w, http.StatusBadRequest, err.Error())
				return
			}
			util.WriteJSON(w, http.StatusOK, response)
		case http.MethodPost:
			body, err := readJSONMap(r)
			if err != nil {
				util.WriteError(w, http.StatusBadRequest, "invalid json body")
				return
			}
			enabled := true
			if value, ok := body["enabled"]; ok {
				enabled = util.ToBool(value)
			}
			item, err := a.auth.CreatePasswordUser(
				util.Clean(body["username"]),
				util.Clean(body["password"]),
				util.Clean(body["name"]),
				util.Clean(body["role_id"]),
				enabled,
			)
			if err != nil {
				util.WriteError(w, http.StatusBadRequest, err.Error())
				return
			}
			response, err := a.managedUsersResponse(r)
			if err != nil {
				util.WriteError(w, http.StatusBadRequest, err.Error())
				return
			}
			if current := a.managedUser(util.Clean(item["id"])); current != nil {
				item = current
			}
			response["item"] = item
			util.WriteJSON(w, http.StatusOK, response)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}

	parts := splitPath(r.URL.Path)
	if len(parts) < 4 || parts[0] != "api" || parts[1] != "admin" || parts[2] != "users" {
		http.NotFound(w, r)
		return
	}
	userID := parts[3]
	if len(parts) == 5 && parts[4] == "key" {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		user := findManagedUser(a.auth.ListUsers(), userID)
		if user == nil {
			util.WriteError(w, http.StatusNotFound, "user not found")
			return
		}
		if util.Clean(user["provider"]) == service.AuthProviderLinuxDo {
			util.WriteError(w, http.StatusForbidden, "Linuxdo user tokens are not managed by administrators")
			return
		}
		key, found := a.auth.RevealUserAPIKey(userID)
		if !found {
			util.WriteError(w, http.StatusNotFound, "user API key not found")
			return
		}
		util.WriteJSON(w, http.StatusOK, map[string]any{"key": key})
		return
	}
	if len(parts) == 5 && parts[4] == "reset-key" {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		body, _ := readJSONMap(r)
		user := findManagedUser(a.auth.ListUsers(), userID)
		if user == nil {
			util.WriteError(w, http.StatusNotFound, "user not found")
			return
		}
		if util.Clean(user["provider"]) == service.AuthProviderLinuxDo {
			util.WriteError(w, http.StatusForbidden, "Linuxdo user tokens are not managed by administrators")
			return
		}
		item, apiKey, raw, found, err := a.auth.ResetUserAPIKey(userID, util.Clean(body["name"]))
		if err != nil {
			util.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		if !found {
			util.WriteError(w, http.StatusNotFound, "user not found")
			return
		}
		response, err := a.managedUsersResponse(r)
		if err != nil {
			util.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		if current := a.managedUser(userID); current != nil {
			item = current
		}
		response["item"] = item
		response["api_key"] = apiKey
		response["key"] = raw
		util.WriteJSON(w, http.StatusOK, response)
		return
	}
	if len(parts) != 4 {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodPost:
		body, _ := readJSONMap(r)
		updates := map[string]any{}
		if value, ok := body["name"]; ok {
			updates["name"] = value
		}
		if value, ok := body["enabled"]; ok {
			updates["enabled"] = value
		}
		if value, ok := body["role_id"]; ok {
			if roleID := util.Clean(value); roleID != "" && !a.auth.RoleExists(roleID) {
				util.WriteError(w, http.StatusBadRequest, "role not found")
				return
			}
			updates["role_id"] = value
		}
		if len(updates) == 0 {
			util.WriteError(w, http.StatusBadRequest, "no updates provided")
			return
		}
		item := a.auth.UpdateUser(userID, updates)
		if item == nil {
			util.WriteError(w, http.StatusNotFound, "user not found")
			return
		}
		response, err := a.managedUsersResponse(r)
		if err != nil {
			util.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		if current := a.managedUser(userID); current != nil {
			item = current
		}
		response["item"] = item
		util.WriteJSON(w, http.StatusOK, response)
	case http.MethodDelete:
		if !a.auth.DeleteUser(userID) {
			util.WriteError(w, http.StatusNotFound, "user not found")
			return
		}
		response, err := a.managedUsersResponse(r)
		if err != nil {
			util.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		util.WriteJSON(w, http.StatusOK, response)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

type managedUsersQuery struct {
	Page       int
	PageSize   int
	Search     string
	Provider   string
	Status     string
	Total      int
	TotalPages int
}

func (a *App) managedUsersResponse(r *http.Request) (map[string]any, error) {
	query, err := parseManagedUsersQuery(r)
	if err != nil {
		return nil, err
	}
	items := filterManagedUsers(a.auth.ListUsers(), query)
	query.Total = len(items)
	query.TotalPages = managedUsersTotalPages(query.Total, query.PageSize)
	if query.Page > query.TotalPages {
		query.Page = query.TotalPages
	}
	start := (query.Page - 1) * query.PageSize
	if start > query.Total {
		start = query.Total
	}
	end := start + query.PageSize
	if end > query.Total {
		end = query.Total
	}
	pageItems := items[start:end]
	a.attachManagedUserUsage(pageItems)
	return map[string]any{
		"items":       pageItems,
		"total":       query.Total,
		"page":        query.Page,
		"page_size":   query.PageSize,
		"total_pages": query.TotalPages,
	}, nil
}

func (a *App) managedUser(id string) map[string]any {
	item := findManagedUser(a.auth.ListUsers(), id)
	if item == nil {
		return nil
	}
	a.attachManagedUserUsage([]map[string]any{item})
	return item
}

func (a *App) attachManagedUserUsage(items []map[string]any) {
	stats := a.logs.UserUsageStats(14)
	for _, item := range items {
		userID := util.Clean(item["id"])
		usage := stats[userID]
		if usage == nil {
			usage = service.ZeroUserUsageStats(14)
		}
		for key, value := range usage {
			item[key] = value
		}
	}
}

func parseManagedUsersQuery(r *http.Request) (managedUsersQuery, error) {
	values := r.URL.Query()
	page, err := parseManagedUsersPage(values.Get("page"))
	if err != nil {
		return managedUsersQuery{}, err
	}
	pageSize, err := parseManagedUsersPageSize(values.Get("page_size"))
	if err != nil {
		return managedUsersQuery{}, err
	}
	return managedUsersQuery{
		Page:     page,
		PageSize: pageSize,
		Search:   strings.TrimSpace(values.Get("search")),
		Provider: strings.TrimSpace(values.Get("provider")),
		Status:   strings.TrimSpace(values.Get("status")),
	}, nil
}

func parseManagedUsersPage(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 1, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 0, fmt.Errorf("page 参数无效")
	}
	return value, nil
}

func parseManagedUsersPageSize(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 20, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 0, fmt.Errorf("page_size 参数无效")
	}
	return normalizedManagedUsersPageSize(value), nil
}

func normalizedManagedUsersPageSize(value int) int {
	if value <= 0 {
		return 20
	}
	if value > 100 {
		return 100
	}
	return value
}

func managedUsersTotalPages(total, pageSize int) int {
	if pageSize <= 0 {
		pageSize = 20
	}
	if total <= 0 {
		return 1
	}
	return (total + pageSize - 1) / pageSize
}

func filterManagedUsers(items []map[string]any, query managedUsersQuery) []map[string]any {
	out := make([]map[string]any, 0, len(items))
	search := strings.ToLower(strings.TrimSpace(query.Search))
	provider := strings.TrimSpace(query.Provider)
	status := strings.TrimSpace(query.Status)
	for _, item := range items {
		if provider != "" && provider != "all" && util.Clean(item["provider"]) != provider {
			continue
		}
		if status == "enabled" && !util.ToBool(item["enabled"]) {
			continue
		}
		if status == "disabled" && util.ToBool(item["enabled"]) {
			continue
		}
		if search != "" && !strings.Contains(managedUserSearchText(item), search) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func managedUserSearchText(item map[string]any) string {
	parts := []string{
		util.Clean(item["id"]),
		util.Clean(item["username"]),
		util.Clean(item["name"]),
		util.Clean(item["role_id"]),
		util.Clean(item["role_name"]),
		util.Clean(item["owner_id"]),
		util.Clean(item["owner_name"]),
		util.Clean(item["provider"]),
		util.Clean(item["linuxdo_level"]),
		util.Clean(item["session_id"]),
		util.Clean(item["session_name"]),
	}
	return strings.ToLower(strings.Join(parts, " "))
}

func findManagedUser(items []map[string]any, id string) map[string]any {
	for _, item := range items {
		if item["id"] == id {
			return item
		}
	}
	return nil
}

func (a *App) handlePublicAnnouncements(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	util.WriteJSON(w, http.StatusOK, map[string]any{"items": a.announce.ListVisible(strings.TrimSpace(r.URL.Query().Get("target")))})
}

func (a *App) handleAdminAnnouncements(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireIdentity(w, r, ""); !ok {
		return
	}
	base := "/api/admin/announcements"
	if r.URL.Path == base {
		switch r.Method {
		case http.MethodGet:
			util.WriteJSON(w, http.StatusOK, map[string]any{"items": a.announce.ListAll()})
		case http.MethodPost:
			body, err := readJSONMap(r)
			if err != nil {
				util.WriteError(w, http.StatusBadRequest, "invalid json body")
				return
			}
			if util.Clean(body["content"]) == "" {
				util.WriteError(w, http.StatusBadRequest, "content is required")
				return
			}
			item := a.announce.Create(body)
			util.WriteJSON(w, http.StatusOK, map[string]any{"item": item, "items": a.announce.ListAll()})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	parts := splitPath(r.URL.Path)
	if len(parts) != 4 || parts[0] != "api" || parts[1] != "admin" || parts[2] != "announcements" {
		http.NotFound(w, r)
		return
	}
	id := parts[3]
	switch r.Method {
	case http.MethodPost:
		body, err := readJSONMap(r)
		if err != nil {
			util.WriteError(w, http.StatusBadRequest, "invalid json body")
			return
		}
		if value, exists := body["content"]; exists && util.Clean(value) == "" {
			util.WriteError(w, http.StatusBadRequest, "content is required")
			return
		}
		item := a.announce.Update(id, body)
		if item == nil {
			util.WriteError(w, http.StatusNotFound, "announcement not found")
			return
		}
		util.WriteJSON(w, http.StatusOK, map[string]any{"item": item, "items": a.announce.ListAll()})
	case http.MethodDelete:
		if !a.announce.Delete(id) {
			util.WriteError(w, http.StatusNotFound, "announcement not found")
			return
		}
		util.WriteJSON(w, http.StatusOK, map[string]any{"items": a.announce.ListAll()})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (a *App) handleAccounts(w http.ResponseWriter, r *http.Request) {
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	switch {
	case r.URL.Path == "/api/accounts" && r.Method == http.MethodGet:
		util.WriteJSON(w, http.StatusOK, map[string]any{"items": a.accountItemsForIdentity(identity)})
	case r.URL.Path == "/api/accounts/tokens" && r.Method == http.MethodGet:
		util.WriteJSON(w, http.StatusOK, map[string]any{"tokens": a.accounts.ListTokens()})
	case r.URL.Path == "/api/accounts" && r.Method == http.MethodPost:
		body, _ := readJSONMap(r)
		tokens := util.AsStringSlice(body["tokens"])
		records := util.AsMapSlice(body["accounts"])
		if len(tokens) == 0 && len(records) == 0 {
			util.WriteError(w, http.StatusBadRequest, "tokens or accounts is required")
			return
		}
		var result map[string]any
		if len(records) > 0 {
			result = a.accounts.AddAccountRecords(records)
			for _, record := range records {
				if token := firstNonEmpty(util.Clean(record["access_token"]), util.Clean(record["accessToken"])); token != "" {
					tokens = append(tokens, token)
				}
			}
		} else {
			result = a.accounts.AddAccounts(tokens)
		}
		refresh := a.accounts.RefreshAccounts(r.Context(), tokens)
		for key, value := range refresh {
			if key == "refreshed" || key == "errors" || key == "items" {
				result[key] = value
			}
		}
		a.redactAccountPayloadForIdentity(identity, result)
		util.WriteJSON(w, http.StatusOK, result)
	case r.URL.Path == "/api/accounts" && r.Method == http.MethodDelete:
		body, _ := readJSONMap(r)
		tokens := util.AsStringSlice(body["tokens"])
		accountIDs := util.AsStringSlice(body["account_ids"])
		if len(tokens) == 0 {
			tokens = a.accounts.ListTokensByIDs(accountIDs)
		}
		if len(tokens) == 0 {
			if len(accountIDs) > 0 {
				util.WriteError(w, http.StatusNotFound, "account not found")
				return
			}
			util.WriteError(w, http.StatusBadRequest, "tokens or account_ids is required")
			return
		}
		result := a.accounts.DeleteAccounts(tokens)
		a.redactAccountPayloadForIdentity(identity, result)
		util.WriteJSON(w, http.StatusOK, result)
	case r.URL.Path == "/api/accounts/refresh" && r.Method == http.MethodPost:
		body, _ := readJSONMap(r)
		tokens := util.AsStringSlice(body["access_tokens"])
		accountIDs := util.AsStringSlice(body["account_ids"])
		if len(tokens) == 0 && len(accountIDs) > 0 {
			tokens = a.accounts.ListTokensByIDs(accountIDs)
		}
		if len(tokens) == 0 && len(accountIDs) == 0 {
			tokens = a.accounts.ListTokens()
		}
		if len(tokens) == 0 {
			if len(accountIDs) > 0 {
				util.WriteError(w, http.StatusNotFound, "account not found")
				return
			}
			util.WriteError(w, http.StatusBadRequest, "access_tokens or account_ids is required")
			return
		}
		result := a.accounts.RefreshAccounts(r.Context(), tokens)
		a.redactAccountPayloadForIdentity(identity, result)
		util.WriteJSON(w, http.StatusOK, result)
	case r.URL.Path == "/api/accounts/update" && r.Method == http.MethodPost:
		body, _ := readJSONMap(r)
		token := util.Clean(body["access_token"])
		accountID := util.Clean(body["account_id"])
		if token == "" && accountID != "" {
			token = a.accounts.GetTokenByID(accountID)
			if token == "" {
				util.WriteError(w, http.StatusNotFound, "account not found")
				return
			}
		}
		if token == "" {
			util.WriteError(w, http.StatusBadRequest, "access_token or account_id is required")
			return
		}
		updates := map[string]any{}
		for _, key := range []string{"type", "status", "quota"} {
			if value, ok := body[key]; ok && value != nil {
				updates[key] = value
			}
		}
		if len(updates) == 0 {
			util.WriteError(w, http.StatusBadRequest, "no updates provided")
			return
		}
		item := a.accounts.UpdateAccount(token, updates)
		if item == nil {
			util.WriteError(w, http.StatusNotFound, "account not found")
			return
		}
		result := map[string]any{"item": item, "items": a.accounts.ListAccounts()}
		a.redactAccountPayloadForIdentity(identity, result)
		util.WriteJSON(w, http.StatusOK, result)
	default:
		http.NotFound(w, r)
	}
}

func (a *App) accountItemsForIdentity(identity service.Identity) []map[string]any {
	items := a.accounts.ListAccounts()
	if !a.identityCanAccessAPI(identity, http.MethodGet, "/api/accounts/tokens") {
		redactAccountTokens(items)
	}
	return items
}

func (a *App) redactAccountPayloadForIdentity(identity service.Identity, payload map[string]any) {
	if a.identityCanAccessAPI(identity, http.MethodGet, "/api/accounts/tokens") {
		return
	}
	if item, ok := payload["item"].(map[string]any); ok {
		redactAccountToken(item)
	}
	if items, ok := payload["items"].([]map[string]any); ok {
		redactAccountTokens(items)
	}
	if errors, ok := payload["errors"].([]map[string]string); ok {
		for _, item := range errors {
			token := item["access_token"]
			delete(item, "access_token")
			if token != "" {
				item["account_id"] = util.SHA1Short(token, 16)
			}
		}
	}
	if results, ok := payload["results"].([]map[string]any); ok {
		for _, item := range results {
			token := util.Clean(item["access_token"])
			delete(item, "access_token")
			if token != "" {
				item["account_id"] = util.SHA1Short(token, 16)
			}
		}
	}
}

func redactAccountTokens(items []map[string]any) {
	for _, item := range items {
		redactAccountToken(item)
	}
}

func redactAccountToken(item map[string]any) {
	delete(item, "access_token")
}

func (a *App) handleCPA(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireIdentity(w, r, ""); !ok {
		return
	}
	parts := splitPath(r.URL.Path)
	if len(parts) == 3 && r.URL.Path == "/api/cpa/pools" {
		switch r.Method {
		case http.MethodGet:
			util.WriteJSON(w, http.StatusOK, map[string]any{"pools": sanitizeCPAPools(a.cpa.ListPools())})
		case http.MethodPost:
			body, _ := readJSONMap(r)
			if util.Clean(body["base_url"]) == "" {
				util.WriteError(w, http.StatusBadRequest, "base_url is required")
				return
			}
			if util.Clean(body["secret_key"]) == "" {
				util.WriteError(w, http.StatusBadRequest, "secret_key is required")
				return
			}
			pool := a.cpa.AddPool(util.Clean(body["name"]), util.Clean(body["base_url"]), util.Clean(body["secret_key"]))
			util.WriteJSON(w, http.StatusOK, map[string]any{"pool": sanitizeCPAPool(pool), "pools": sanitizeCPAPools(a.cpa.ListPools())})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	if len(parts) < 4 {
		http.NotFound(w, r)
		return
	}
	poolID := parts[3]
	pool := a.cpa.GetPool(poolID)
	if pool == nil {
		util.WriteError(w, http.StatusNotFound, "pool not found")
		return
	}
	if len(parts) == 4 {
		switch r.Method {
		case http.MethodPost:
			body, _ := readJSONMap(r)
			updated := a.cpa.UpdatePool(poolID, body)
			util.WriteJSON(w, http.StatusOK, map[string]any{"pool": sanitizeCPAPool(updated), "pools": sanitizeCPAPools(a.cpa.ListPools())})
		case http.MethodDelete:
			if !a.cpa.DeletePool(poolID) {
				util.WriteError(w, http.StatusNotFound, "pool not found")
				return
			}
			util.WriteJSON(w, http.StatusOK, map[string]any{"pools": sanitizeCPAPools(a.cpa.ListPools())})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	if len(parts) == 5 && parts[4] == "files" && r.Method == http.MethodGet {
		files, err := a.cpaImport.ListRemoteFiles(r.Context(), pool)
		if err != nil {
			util.WriteError(w, http.StatusBadGateway, err.Error())
			return
		}
		util.WriteJSON(w, http.StatusOK, map[string]any{"pool_id": poolID, "files": files})
		return
	}
	if len(parts) == 5 && parts[4] == "import" {
		if r.Method == http.MethodGet {
			util.WriteJSON(w, http.StatusOK, map[string]any{"import_job": pool["import_job"]})
			return
		}
		if r.Method == http.MethodPost {
			body, _ := readJSONMap(r)
			job, err := a.cpaImport.StartImport(pool, util.AsStringSlice(body["names"]))
			if err != nil {
				util.WriteError(w, http.StatusBadRequest, err.Error())
				return
			}
			util.WriteJSON(w, http.StatusOK, map[string]any{"import_job": job})
			return
		}
	}
	if len(parts) == 5 && parts[4] == "export" {
		if r.Method == http.MethodGet {
			util.WriteJSON(w, http.StatusOK, map[string]any{"export_job": pool["export_job"]})
			return
		}
		if r.Method == http.MethodPost {
			body, _ := readJSONMap(r)
			includeIncomplete := true
			if value, ok := body["include_incomplete"]; ok {
				includeIncomplete = util.ToBool(value)
			}
			job, err := a.cpaImport.StartExport(pool, util.AsStringSlice(body["account_ids"]), includeIncomplete)
			if err != nil {
				util.WriteError(w, http.StatusBadRequest, err.Error())
				return
			}
			util.WriteJSON(w, http.StatusOK, map[string]any{"export_job": job})
			return
		}
	}
	http.NotFound(w, r)
}

func (a *App) handleSub2API(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireIdentity(w, r, ""); !ok {
		return
	}
	parts := splitPath(r.URL.Path)
	if r.URL.Path == "/api/sub2api/servers" {
		switch r.Method {
		case http.MethodGet:
			util.WriteJSON(w, http.StatusOK, map[string]any{"servers": sanitizeSub2Servers(a.sub2.ListServers())})
		case http.MethodPost:
			body, _ := readJSONMap(r)
			if util.Clean(body["base_url"]) == "" {
				util.WriteError(w, http.StatusBadRequest, "base_url is required")
				return
			}
			hasLogin := util.Clean(body["email"]) != "" && util.Clean(body["password"]) != ""
			hasAPIKey := util.Clean(body["api_key"]) != ""
			if !hasLogin && !hasAPIKey {
				util.WriteError(w, http.StatusBadRequest, "email+password or api_key is required")
				return
			}
			server := a.sub2.AddServer(util.Clean(body["name"]), util.Clean(body["base_url"]), util.Clean(body["email"]), util.Clean(body["password"]), util.Clean(body["api_key"]), util.Clean(body["group_id"]))
			util.WriteJSON(w, http.StatusOK, map[string]any{"server": sanitizeSub2Server(server), "servers": sanitizeSub2Servers(a.sub2.ListServers())})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	if len(parts) < 4 {
		http.NotFound(w, r)
		return
	}
	serverID := parts[3]
	server := a.sub2.GetServer(serverID)
	if server == nil {
		util.WriteError(w, http.StatusNotFound, "server not found")
		return
	}
	if len(parts) == 4 {
		switch r.Method {
		case http.MethodPost:
			body, _ := readJSONMap(r)
			updated := a.sub2.UpdateServer(serverID, body)
			util.WriteJSON(w, http.StatusOK, map[string]any{"server": sanitizeSub2Server(updated), "servers": sanitizeSub2Servers(a.sub2.ListServers())})
		case http.MethodDelete:
			if !a.sub2.DeleteServer(serverID) {
				util.WriteError(w, http.StatusNotFound, "server not found")
				return
			}
			util.WriteJSON(w, http.StatusOK, map[string]any{"servers": sanitizeSub2Servers(a.sub2.ListServers())})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	if len(parts) == 5 && parts[4] == "groups" && r.Method == http.MethodGet {
		groups, err := a.sub2Import.ListRemoteGroups(r.Context(), server)
		if err != nil {
			util.WriteError(w, http.StatusBadGateway, err.Error())
			return
		}
		util.WriteJSON(w, http.StatusOK, map[string]any{"server_id": serverID, "groups": groups})
		return
	}
	if len(parts) == 5 && parts[4] == "accounts" && r.Method == http.MethodGet {
		accounts, err := a.sub2Import.ListRemoteAccounts(r.Context(), server)
		if err != nil {
			util.WriteError(w, http.StatusBadGateway, err.Error())
			return
		}
		util.WriteJSON(w, http.StatusOK, map[string]any{"server_id": serverID, "accounts": accounts})
		return
	}
	if len(parts) == 5 && parts[4] == "import" {
		if r.Method == http.MethodGet {
			util.WriteJSON(w, http.StatusOK, map[string]any{"import_job": server["import_job"]})
			return
		}
		if r.Method == http.MethodPost {
			body, _ := readJSONMap(r)
			job, err := a.sub2Import.StartImport(server, util.AsStringSlice(body["account_ids"]))
			if err != nil {
				util.WriteError(w, http.StatusBadRequest, err.Error())
				return
			}
			util.WriteJSON(w, http.StatusOK, map[string]any{"import_job": job})
			return
		}
	}
	http.NotFound(w, r)
}

func (a *App) handleCreationTasks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	identity, ok := a.requireIdentity(w, r, "")
	if !ok {
		return
	}
	parts := splitPath(r.URL.Path)
	if r.URL.Path == "/api/creation-tasks" && r.Method == http.MethodGet {
		util.WriteJSON(w, http.StatusOK, a.tasks.ListTasks(identity, util.ParseCommaList(r.URL.Query().Get("ids"))))
		return
	}
	if len(parts) == 4 && parts[0] == "api" && parts[1] == "creation-tasks" && parts[3] == "cancel" {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		task, err := a.tasks.CancelTask(identity, parts[2])
		if err != nil {
			util.WriteError(w, http.StatusNotFound, err.Error())
			return
		}
		util.WriteJSON(w, http.StatusOK, task)
		return
	}
	if r.URL.Path == "/api/creation-tasks/image-generations" && r.Method == http.MethodPost {
		body, _ := readJSONMap(r)
		task, err := a.tasks.SubmitGenerationWithOptions(r.Context(), identity, util.Clean(body["client_task_id"]), util.Clean(body["prompt"]), firstNonEmpty(util.Clean(body["model"]), util.ImageModelAuto), util.Clean(body["size"]), util.Clean(body["quality"]), a.resolveImageBaseURL(r), util.ToInt(body["n"], 1), body["messages"], imageTaskRequestMetadata(body), imageOutputOptionsFromBody(body), imageToolOptionsFromBody(body), util.Clean(body["visibility"]))
		if err != nil {
			writeCreationTaskSubmitError(w, err)
			return
		}
		util.WriteJSON(w, http.StatusOK, task)
		return
	}
	if r.URL.Path == "/api/creation-tasks/chat-completions" && r.Method == http.MethodPost {
		body, _ := readJSONMap(r)
		task, err := a.tasks.SubmitChat(r.Context(), identity, util.Clean(body["client_task_id"]), util.Clean(body["prompt"]), firstNonEmpty(util.Clean(body["model"]), util.ImageModelAuto), body["messages"])
		if err != nil {
			writeCreationTaskSubmitError(w, err)
			return
		}
		util.WriteJSON(w, http.StatusOK, task)
		return
	}
	if r.URL.Path == "/api/creation-tasks/image-edits" && r.Method == http.MethodPost {
		body, images, err := readMultipartImageBody(r)
		if err != nil {
			util.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		task, err := a.tasks.SubmitEditWithOptions(r.Context(), identity, util.Clean(body["client_task_id"]), util.Clean(body["prompt"]), firstNonEmpty(util.Clean(body["model"]), util.ImageModelAuto), util.Clean(body["size"]), util.Clean(body["quality"]), a.resolveImageBaseURL(r), images, util.ToInt(body["n"], 1), body["messages"], imageTaskRequestMetadata(body), imageOutputOptionsFromBody(body), imageToolOptionsFromBody(body), util.Clean(body["visibility"]))
		if err != nil {
			writeCreationTaskSubmitError(w, err)
			return
		}
		util.WriteJSON(w, http.StatusOK, task)
		return
	}
	http.NotFound(w, r)
}

func imageTaskRequestMetadata(body map[string]any) map[string]any {
	size := util.Clean(body["size"])
	metadata := map[string]any{}
	if preset := service.NormalizeImageResolutionPreset(util.Clean(body["image_resolution"])); preset != "" {
		metadata["image_resolution"] = preset
	}
	if size != "" {
		metadata["requested_size"] = size
	}
	return metadata
}

func imageOutputOptionsFromBody(body map[string]any) service.ImageOutputOptions {
	format := service.NormalizeImageOutputFormat(util.Clean(body["output_format"]))
	options := service.ImageOutputOptions{Format: format}
	if service.SupportsImageOutputCompression(format) {
		if compression, ok := imageOutputCompressionFromBody(body["output_compression"]); ok {
			options.Compression = &compression
		}
	}
	return options
}

func imageToolOptionsFromBody(body map[string]any) service.ImageToolOptions {
	options := service.ImageToolOptions{
		Background:     util.Clean(body["background"]),
		Moderation:     util.Clean(body["moderation"]),
		Style:          util.Clean(body["style"]),
		InputImageMask: util.Clean(body["input_image_mask"]),
	}
	if partialImages := util.ToInt(body["partial_images"], 0); partialImages > 0 {
		options.PartialImages = &partialImages
	}
	return options
}

func imageOutputCompressionFromBody(value any) (int, bool) {
	if value == nil || strings.TrimSpace(util.Clean(value)) == "" {
		return 0, false
	}
	compression := util.ToInt(value, -1)
	if compression < 0 {
		return 0, false
	}
	if compression > 100 {
		compression = 100
	}
	return compression, true
}

func writeCreationTaskSubmitError(w http.ResponseWriter, err error) {
	var limitErr service.ImageTaskLimitError
	if errors.As(err, &limitErr) {
		util.WriteError(w, http.StatusTooManyRequests, limitErr.Error())
		return
	}
	util.WriteError(w, http.StatusBadRequest, err.Error())
}

func (a *App) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/register/events" {
		token := r.URL.Query().Get("token")
		if _, ok := a.requireIdentity(w, r, "Bearer "+token); !ok {
			return
		}
		a.streamRegisterEvents(w, r)
		return
	}
	if _, ok := a.requireIdentity(w, r, ""); !ok {
		return
	}
	switch {
	case r.URL.Path == "/api/register" && r.Method == http.MethodGet:
		util.WriteJSON(w, http.StatusOK, map[string]any{"register": a.register.Get()})
	case r.URL.Path == "/api/register" && r.Method == http.MethodPost:
		body, _ := readJSONMap(r)
		util.WriteJSON(w, http.StatusOK, map[string]any{"register": a.register.Update(body)})
	case r.URL.Path == "/api/register/start" && r.Method == http.MethodPost:
		util.WriteJSON(w, http.StatusOK, map[string]any{"register": a.register.Start()})
	case r.URL.Path == "/api/register/stop" && r.Method == http.MethodPost:
		util.WriteJSON(w, http.StatusOK, map[string]any{"register": a.register.Stop()})
	case r.URL.Path == "/api/register/reset" && r.Method == http.MethodPost:
		util.WriteJSON(w, http.StatusOK, map[string]any{"register": a.register.Reset()})
	default:
		http.NotFound(w, r)
	}
}

func (a *App) streamRegisterEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	last := ""
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			payload := jsonString(a.register.Get())
			if payload != last {
				last = payload
				fmt.Fprintf(w, "data: %s\n\n", payload)
				if flusher != nil {
					flusher.Flush()
				}
			}
		}
	}
}

func sanitizeCPAPool(pool map[string]any) map[string]any {
	if pool == nil {
		return nil
	}
	out := util.CopyMap(pool)
	delete(out, "secret_key")
	return out
}

func sanitizeCPAPools(pools []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(pools))
	for _, pool := range pools {
		out = append(out, sanitizeCPAPool(pool))
	}
	return out
}

func sanitizeSub2Server(server map[string]any) map[string]any {
	if server == nil {
		return nil
	}
	out := util.CopyMap(server)
	out["has_api_key"] = util.Clean(server["api_key"]) != ""
	delete(out, "password")
	delete(out, "api_key")
	return out
}

func sanitizeSub2Servers(servers []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(servers))
	for _, server := range servers {
		out = append(out, sanitizeSub2Server(server))
	}
	return out
}

func splitPath(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}
