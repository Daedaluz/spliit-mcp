package handlers

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/daedaluz/spliit-mcp/internal/spliit"
	"github.com/daedaluz/spliit-mcp/internal/store"
)

// GetMe returns the signed-in user's identity and the display name used to
// recognise them among group participants.
func (s *Server) GetMe(c *gin.Context) {
	user := UserFromContext(c)
	c.JSON(http.StatusOK, gin.H{
		"sub":          user.Sub,
		"email":        user.Email,
		"display_name": user.DisplayName,
	})
}

// UpdateMe changes the display name. This is the "who you are" half of the
// config page: it is the default used to find your participant when adding a
// group.
func (s *Server) UpdateMe(c *gin.Context) {
	user := UserFromContext(c)

	var body struct {
		DisplayName string `json:"display_name"`
	}
	if err := bindJSON(c, &body); err != nil {
		writeError(c, http.StatusBadRequest, "invalid request body")
		return
	}

	name := strings.TrimSpace(body.DisplayName)
	if name == "" {
		writeError(c, http.StatusBadRequest, "display_name must not be empty")
		return
	}

	if err := s.store.SetDisplayName(c.Request.Context(), user.Sub, name); err != nil {
		s.serverError(c, "set display name", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"sub": user.Sub, "email": user.Email, "display_name": name,
	})
}

// ListServers returns the user's registered Spliit instances.
func (s *Server) ListServers(c *gin.Context) {
	user := UserFromContext(c)

	servers, err := s.store.ListServers(c.Request.Context(), user.Sub)
	if err != nil {
		s.serverError(c, "list servers", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"servers": servers})
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
func (s *Server) CreateServer(c *gin.Context) {
	user := UserFromContext(c)

	var body serverBody
	if err := bindJSON(c, &body); err != nil {
		writeError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := body.validate(); err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}

	server, err := s.store.CreateServer(c.Request.Context(), user.Sub, body.Name, body.BaseURL)
	if errors.Is(err, store.ErrConflict) {
		writeError(c, http.StatusConflict, "a server with that name already exists")
		return
	}
	if err != nil {
		s.serverError(c, "create server", err)
		return
	}
	c.JSON(http.StatusCreated, server)
}

// UpdateServer renames a server or repoints its URL.
func (s *Server) UpdateServer(c *gin.Context) {
	user := UserFromContext(c)

	var body serverBody
	if err := bindJSON(c, &body); err != nil {
		writeError(c, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := body.validate(); err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}

	ctx, id := c.Request.Context(), c.Param("id")
	err := s.store.UpdateServer(ctx, user.Sub, id, body.Name, body.BaseURL)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(c, http.StatusNotFound, "server not found")
	case errors.Is(err, store.ErrConflict):
		writeError(c, http.StatusConflict, "a server with that name already exists")
	case err != nil:
		s.serverError(c, "update server", err)
	default:
		server, err := s.store.GetServer(ctx, user.Sub, id)
		if err != nil {
			s.serverError(c, "reload server", err)
			return
		}
		c.JSON(http.StatusOK, server)
	}
}

// DeleteServer removes a Spliit instance. Groups still pointing at it must be
// removed first; silently cascading would drop group IDs the user cannot
// recover, since Spliit has no way to list them.
func (s *Server) DeleteServer(c *gin.Context) {
	user := UserFromContext(c)
	ctx, id := c.Request.Context(), c.Param("id")

	count, err := s.store.CountGroupsForServer(ctx, user.Sub, id)
	if err != nil {
		s.serverError(c, "count groups for server", err)
		return
	}
	if count > 0 {
		writeError(c, http.StatusConflict, "remove this server's groups before deleting it")
		return
	}

	err = s.store.DeleteServer(ctx, user.Sub, id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(c, http.StatusNotFound, "server not found")
	case err != nil:
		s.serverError(c, "delete server", err)
	default:
		c.Status(http.StatusNoContent)
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
func (s *Server) ListGroups(c *gin.Context) {
	user := UserFromContext(c)
	ctx := c.Request.Context()

	groups, err := s.store.ListGroups(ctx, user.Sub)
	if err != nil {
		s.serverError(c, "list groups", err)
		return
	}
	servers, err := s.store.ListServers(ctx, user.Sub)
	if err != nil {
		s.serverError(c, "list servers", err)
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
	c.JSON(http.StatusOK, gin.H{"groups": views})
}

// PreviewGroup fetches a group from Spliit without storing anything, so the
// config page can show its participants and suggest which one is "you".
//
// The suggestion is a case-insensitive match on the user's display name. When
// it does not hit exactly one participant the UI must ask the user to pick.
func (s *Server) PreviewGroup(c *gin.Context) {
	user := UserFromContext(c)
	ctx := c.Request.Context()

	var body struct {
		ServerID string `json:"server_id"`
		GroupID  string `json:"group_id"`
	}
	if err := bindJSON(c, &body); err != nil {
		writeError(c, http.StatusBadRequest, "invalid request body")
		return
	}

	body.GroupID = extractGroupID(strings.TrimSpace(body.GroupID))
	if body.GroupID == "" {
		writeError(c, http.StatusBadRequest, "group_id must not be empty")
		return
	}

	server, err := s.store.GetServer(ctx, user.Sub, body.ServerID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(c, http.StatusNotFound, "server not found")
		return
	}
	if err != nil {
		s.serverError(c, "load server", err)
		return
	}

	group, err := s.clients.GetGroup(ctx, server.BaseURL, body.GroupID)
	if err != nil {
		// This is an upstream lookup of user-supplied input; a bad group ID is
		// a client error, and the message is worth showing.
		writeError(c, http.StatusBadGateway, "could not fetch that group: "+err.Error())
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

	c.JSON(http.StatusOK, gin.H{
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
func (s *Server) CreateGroup(c *gin.Context) {
	user := UserFromContext(c)
	ctx := c.Request.Context()

	var body struct {
		ServerID      string `json:"server_id"`
		GroupID       string `json:"group_id"`
		Alias         string `json:"alias"`
		ParticipantID string `json:"participant_id"`
	}
	if err := bindJSON(c, &body); err != nil {
		writeError(c, http.StatusBadRequest, "invalid request body")
		return
	}

	body.GroupID = extractGroupID(strings.TrimSpace(body.GroupID))
	body.Alias = strings.TrimSpace(body.Alias)
	if body.GroupID == "" {
		writeError(c, http.StatusBadRequest, "group_id must not be empty")
		return
	}

	server, err := s.store.GetServer(ctx, user.Sub, body.ServerID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(c, http.StatusNotFound, "server not found")
		return
	}
	if err != nil {
		s.serverError(c, "load server", err)
		return
	}

	group, err := s.clients.GetGroup(ctx, server.BaseURL, body.GroupID)
	if err != nil {
		writeError(c, http.StatusBadGateway, "could not fetch that group: "+err.Error())
		return
	}

	// The participant must really exist in this group, or every later tool call
	// would fail with a confusing upstream error instead of a clear one here.
	participant := spliit.FindParticipant(group, body.ParticipantID)
	if body.ParticipantID != "" && participant == nil {
		writeError(c, http.StatusBadRequest, "that participant does not exist in this group")
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

	created, err := s.store.CreateGroup(ctx, row)
	if errors.Is(err, store.ErrConflict) {
		writeError(c, http.StatusConflict, "that group is already registered, or the alias is taken")
		return
	}
	if err != nil {
		s.serverError(c, "create group", err)
		return
	}
	c.JSON(http.StatusCreated, created)
}

// UpdateGroup changes a group's alias or re-pins which participant is "you".
func (s *Server) UpdateGroup(c *gin.Context) {
	user := UserFromContext(c)
	ctx, id := c.Request.Context(), c.Param("id")

	var body struct {
		Alias         *string `json:"alias"`
		ParticipantID *string `json:"participant_id"`
	}
	if err := bindJSON(c, &body); err != nil {
		writeError(c, http.StatusBadRequest, "invalid request body")
		return
	}

	existing, err := s.store.GetGroup(ctx, user.Sub, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(c, http.StatusNotFound, "group not found")
		return
	}
	if err != nil {
		s.serverError(c, "load group", err)
		return
	}

	if body.Alias != nil {
		alias := strings.TrimSpace(*body.Alias)
		if alias == "" {
			writeError(c, http.StatusBadRequest, "alias must not be empty")
			return
		}
		existing.Alias = alias
	}

	if body.ParticipantID != nil {
		server, err := s.store.GetServer(ctx, user.Sub, existing.ServerID)
		if err != nil {
			s.serverError(c, "load server", err)
			return
		}
		group, err := s.clients.GetGroup(ctx, server.BaseURL, existing.SpliitGroupID)
		if err != nil {
			writeError(c, http.StatusBadGateway, "could not fetch that group: "+err.Error())
			return
		}
		participant := spliit.FindParticipant(group, *body.ParticipantID)
		if participant == nil {
			writeError(c, http.StatusBadRequest, "that participant does not exist in this group")
			return
		}
		existing.ParticipantID, existing.ParticipantName = participant.ID, participant.Name
		// Refresh the cached fields while we have the group in hand.
		existing.GroupName, existing.Currency = group.Name, group.Currency
	}

	if err := s.store.UpdateGroup(ctx, existing); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeError(c, http.StatusConflict, "that alias is already taken")
			return
		}
		s.serverError(c, "update group", err)
		return
	}
	c.JSON(http.StatusOK, existing)
}

// DeleteGroup unlinks a group. Nothing is deleted in Spliit itself.
func (s *Server) DeleteGroup(c *gin.Context) {
	user := UserFromContext(c)

	err := s.store.DeleteGroup(c.Request.Context(), user.Sub, c.Param("id"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(c, http.StatusNotFound, "group not found")
	case err != nil:
		s.serverError(c, "delete group", err)
	default:
		c.Status(http.StatusNoContent)
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
