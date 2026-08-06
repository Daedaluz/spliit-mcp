package handlers

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/daedaluz/spliit-mcp/internal/spliit"
	"github.com/daedaluz/spliit-mcp/internal/store"
)

// GetMe returns the signed-in user's identity and the display name used to
// recognise them among group participants.
func (s *Server) GetMe(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"sub":          user.Sub,
		"email":        user.Email,
		"display_name": user.DisplayName,
	})
}

// UpdateMe changes the display name. This is the "who you are" half of the
// config page: it is the default used to find your participant when adding a
// group.
func (s *Server) UpdateMe(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())

	var body struct {
		DisplayName string `json:"display_name"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	name := strings.TrimSpace(body.DisplayName)
	if name == "" {
		writeError(w, http.StatusBadRequest, "display_name must not be empty")
		return
	}

	if err := s.store.SetDisplayName(r.Context(), user.Sub, name); err != nil {
		s.serverError(w, r, "set display name", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sub": user.Sub, "email": user.Email, "display_name": name,
	})
}

// ListServers returns the user's registered Spliit instances.
func (s *Server) ListServers(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())

	servers, err := s.store.ListServers(r.Context(), user.Sub)
	if err != nil {
		s.serverError(w, r, "list servers", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"servers": servers})
}

type serverBody struct {
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
}

// validate normalises and checks a server payload. The base URL must be an
// absolute http(s) URL because it is dialled directly.
func (b *serverBody) validate() error {
	b.Name = strings.TrimSpace(b.Name)
	b.BaseURL = strings.TrimRight(strings.TrimSpace(b.BaseURL), "/")

	if b.Name == "" {
		return errors.New("name must not be empty")
	}
	if b.BaseURL == "" {
		return errors.New("base_url must not be empty")
	}
	parsed, err := url.Parse(b.BaseURL)
	if err != nil {
		return errors.New("base_url is not a valid URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("base_url must be an http or https URL")
	}
	if parsed.Host == "" {
		return errors.New("base_url must include a host")
	}
	return nil
}

// CreateServer registers another Spliit instance.
func (s *Server) CreateServer(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())

	var body serverBody
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := body.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	server, err := s.store.CreateServer(r.Context(), user.Sub, body.Name, body.BaseURL)
	if errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict, "a server with that name already exists")
		return
	}
	if err != nil {
		s.serverError(w, r, "create server", err)
		return
	}
	writeJSON(w, http.StatusCreated, server)
}

// UpdateServer renames a server or repoints its URL.
func (s *Server) UpdateServer(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())

	var body serverBody
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := body.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	id := chi.URLParam(r, "id")
	err := s.store.UpdateServer(r.Context(), user.Sub, id, body.Name, body.BaseURL)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "server not found")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, "a server with that name already exists")
	case err != nil:
		s.serverError(w, r, "update server", err)
	default:
		server, err := s.store.GetServer(r.Context(), user.Sub, id)
		if err != nil {
			s.serverError(w, r, "reload server", err)
			return
		}
		writeJSON(w, http.StatusOK, server)
	}
}

// DeleteServer removes a Spliit instance. Groups still pointing at it must be
// removed first; silently cascading would drop group IDs the user cannot
// recover, since Spliit has no way to list them.
func (s *Server) DeleteServer(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	id := chi.URLParam(r, "id")

	count, err := s.store.CountGroupsForServer(r.Context(), user.Sub, id)
	if err != nil {
		s.serverError(w, r, "count groups for server", err)
		return
	}
	if count > 0 {
		writeError(w, http.StatusConflict,
			"remove this server's groups before deleting it")
		return
	}

	err = s.store.DeleteServer(r.Context(), user.Sub, id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "server not found")
	case err != nil:
		s.serverError(w, r, "delete server", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// groupView augments a stored group with a health flag, so the config page can
// call out rows whose pinned participant has gone missing.
type groupView struct {
	store.Group
	ServerName string `json:"server_name"`
	NeedsSetup bool   `json:"needs_setup"`
}

// ListGroups returns every group the user has made available.
func (s *Server) ListGroups(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())

	groups, err := s.store.ListGroups(r.Context(), user.Sub)
	if err != nil {
		s.serverError(w, r, "list groups", err)
		return
	}
	servers, err := s.store.ListServers(r.Context(), user.Sub)
	if err != nil {
		s.serverError(w, r, "list servers", err)
		return
	}

	names := make(map[string]string, len(servers))
	for _, srv := range servers {
		names[srv.ID] = srv.Name
	}

	views := make([]groupView, 0, len(groups))
	for _, g := range groups {
		views = append(views, groupView{
			Group:      g,
			ServerName: names[g.ServerID],
			NeedsSetup: g.ParticipantID == "",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": views})
}

// PreviewGroup fetches a group from Spliit without storing anything, so the
// config page can show its participants and suggest which one is "you".
//
// The suggestion is a case-insensitive match on the user's display name. When
// it does not hit exactly one participant the UI must ask the user to pick.
func (s *Server) PreviewGroup(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())

	var body struct {
		ServerID string `json:"server_id"`
		GroupID  string `json:"group_id"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	body.GroupID = extractGroupID(strings.TrimSpace(body.GroupID))
	if body.GroupID == "" {
		writeError(w, http.StatusBadRequest, "group_id must not be empty")
		return
	}

	server, err := s.store.GetServer(r.Context(), user.Sub, body.ServerID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	if err != nil {
		s.serverError(w, r, "load server", err)
		return
	}

	group, err := s.clients.GetGroup(r.Context(), server.BaseURL, body.GroupID)
	if err != nil {
		// This is an upstream lookup of user-supplied input; a bad group ID is
		// a client error, and the message is worth showing.
		writeError(w, http.StatusBadGateway, "could not fetch that group: "+err.Error())
		return
	}

	type participant struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	participants := make([]participant, 0, len(group.Participants))
	var suggested string
	matches := 0
	for _, p := range group.Participants {
		if p == nil {
			continue
		}
		participants = append(participants, participant{ID: p.ID, Name: p.Name})
		if strings.EqualFold(strings.TrimSpace(p.Name), strings.TrimSpace(user.DisplayName)) {
			suggested = p.ID
			matches++
		}
	}
	if matches != 1 {
		// Ambiguous or absent: make the UI ask rather than guess wrong.
		suggested = ""
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"group_id":                body.GroupID,
		"name":                    group.Name,
		"currency":                group.Currency,
		"participants":            participants,
		"suggested_participant":   suggested,
		"suggested_from_name":     user.DisplayName,
		"suggestion_is_ambiguous": matches > 1,
	})
}

// CreateGroup makes a group available to the user's MCP session.
func (s *Server) CreateGroup(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())

	var body struct {
		ServerID      string `json:"server_id"`
		GroupID       string `json:"group_id"`
		Alias         string `json:"alias"`
		ParticipantID string `json:"participant_id"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	body.GroupID = extractGroupID(strings.TrimSpace(body.GroupID))
	body.Alias = strings.TrimSpace(body.Alias)
	if body.GroupID == "" {
		writeError(w, http.StatusBadRequest, "group_id must not be empty")
		return
	}

	server, err := s.store.GetServer(r.Context(), user.Sub, body.ServerID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "server not found")
		return
	}
	if err != nil {
		s.serverError(w, r, "load server", err)
		return
	}

	group, err := s.clients.GetGroup(r.Context(), server.BaseURL, body.GroupID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "could not fetch that group: "+err.Error())
		return
	}

	// The participant must really exist in this group, or every later tool call
	// would fail with a confusing upstream error instead of a clear one here.
	participant := spliit.FindParticipant(group, body.ParticipantID)
	if body.ParticipantID != "" && participant == nil {
		writeError(w, http.StatusBadRequest,
			"that participant does not exist in this group")
		return
	}

	if body.Alias == "" {
		body.Alias = group.Name
	}

	row := &store.Group{
		UserSub:       user.Sub,
		ServerID:      server.ID,
		SpliitGroupID: group.ID,
		Alias:         body.Alias,
		GroupName:     group.Name,
		Currency:      group.Currency,
	}
	if participant != nil {
		row.ParticipantID, row.ParticipantName = participant.ID, participant.Name
	}

	created, err := s.store.CreateGroup(r.Context(), row)
	if errors.Is(err, store.ErrConflict) {
		writeError(w, http.StatusConflict,
			"that group is already registered, or the alias is taken")
		return
	}
	if err != nil {
		s.serverError(w, r, "create group", err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

// UpdateGroup changes a group's alias or re-pins which participant is "you".
func (s *Server) UpdateGroup(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())
	id := chi.URLParam(r, "id")

	var body struct {
		Alias         *string `json:"alias"`
		ParticipantID *string `json:"participant_id"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	existing, err := s.store.GetGroup(r.Context(), user.Sub, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "group not found")
		return
	}
	if err != nil {
		s.serverError(w, r, "load group", err)
		return
	}

	if body.Alias != nil {
		alias := strings.TrimSpace(*body.Alias)
		if alias == "" {
			writeError(w, http.StatusBadRequest, "alias must not be empty")
			return
		}
		existing.Alias = alias
	}

	if body.ParticipantID != nil {
		server, err := s.store.GetServer(r.Context(), user.Sub, existing.ServerID)
		if err != nil {
			s.serverError(w, r, "load server", err)
			return
		}
		group, err := s.clients.GetGroup(r.Context(), server.BaseURL, existing.SpliitGroupID)
		if err != nil {
			writeError(w, http.StatusBadGateway, "could not fetch that group: "+err.Error())
			return
		}
		participant := spliit.FindParticipant(group, *body.ParticipantID)
		if participant == nil {
			writeError(w, http.StatusBadRequest, "that participant does not exist in this group")
			return
		}
		existing.ParticipantID, existing.ParticipantName = participant.ID, participant.Name
		// Refresh the cached fields while we have the group in hand.
		existing.GroupName, existing.Currency = group.Name, group.Currency
	}

	if err := s.store.UpdateGroup(r.Context(), existing); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(w, http.StatusConflict, "that alias is already taken")
			return
		}
		s.serverError(w, r, "update group", err)
		return
	}
	writeJSON(w, http.StatusOK, existing)
}

// DeleteGroup unlinks a group. Nothing is deleted in Spliit itself.
func (s *Server) DeleteGroup(w http.ResponseWriter, r *http.Request) {
	user := UserFromContext(r.Context())

	err := s.store.DeleteGroup(r.Context(), user.Sub, chi.URLParam(r, "id"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "group not found")
	case err != nil:
		s.serverError(w, r, "delete group", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// extractGroupID accepts either a bare group ID or a full Spliit URL such as
// https://spliit.app/groups/<id>/expenses, since pasting the browser URL is the
// natural thing to do when adding a group.
func extractGroupID(input string) string {
	if !strings.Contains(input, "/") {
		return input
	}
	parsed, err := url.Parse(input)
	if err != nil {
		return input
	}

	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for i, part := range parts {
		if part == "groups" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return input
}
