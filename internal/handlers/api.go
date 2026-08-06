package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.chrastecky.dev/spliit-api/spliit/shape"

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

// GetConfig returns the details needed to connect an MCP client to this server.
//
// The endpoint comes from the configured public URL rather than the browser's
// origin: behind a reverse proxy the two can differ, and the public URL is the
// one the OIDC redirect and token audience are already built from, so it is the
// address that actually works.
func (s *Server) GetConfig(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"mcp_url": s.cfg.MCPResourceURL(),
		"issuer":  s.cfg.OIDC.Issuer,
		// Empty unless a client was pre-registered for providers that do not
		// allow dynamic client registration.
		"mcp_client_id": s.cfg.OIDC.MCPClientID,
	})
}

// resolveBaseURL picks the Spliit instance for a request: an explicit base URL
// wins, then one derived from a pasted group link, and finally the configured
// default. This is what makes a separate server registry unnecessary.
func (s *Server) resolveBaseURL(explicit, groupRef string) string {
	if trimmed := strings.TrimRight(strings.TrimSpace(explicit), "/"); trimmed != "" {
		return trimmed
	}
	if derived := spliit.DeriveBaseURL(groupRef); derived != "" {
		return derived
	}
	return s.cfg.Spliit.DefaultURL
}

// groupView augments a stored group with display and health fields, so the
// config page can show which instance hosts it and call out rows whose pinned
// participant has gone missing.
type groupView struct {
	store.Group
	Host       string `json:"host"`
	NeedsSetup bool   `json:"needs_setup"`
}

// ListGroups returns every group the user has made available.
func (s *Server) ListGroups(c *gin.Context) {
	user := UserFromContext(c)

	groups, err := s.store.ListGroups(c.Request.Context(), user.Sub)
	if err != nil {
		s.serverError(c, "list groups", err)
		return
	}

	views := make([]groupView, 0, len(groups))
	for _, g := range groups {
		views = append(views, groupView{
			Group:      g,
			Host:       spliit.HostOf(g.BaseURL),
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
		// GroupID is a bare ID or a full group URL; the hosting instance is
		// derived from the latter.
		GroupID string `json:"group_id"`
		// BaseURL overrides that derivation, for re-checking a known group.
		BaseURL string `json:"base_url"`
	}
	if err := bindJSON(c, &body); err != nil {
		writeError(c, http.StatusBadRequest, "invalid request body")
		return
	}

	baseURL := s.resolveBaseURL(body.BaseURL, body.GroupID)
	body.GroupID = spliit.ExtractGroupID(strings.TrimSpace(body.GroupID))
	if body.GroupID == "" {
		writeError(c, http.StatusBadRequest, "group_id must not be empty")
		return
	}

	group, err := s.clients.GetGroup(ctx, baseURL, body.GroupID)
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
		"base_url":                baseURL,
		"host":                    spliit.HostOf(baseURL),
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
		GroupID       string `json:"group_id"`
		BaseURL       string `json:"base_url"`
		Alias         string `json:"alias"`
		ParticipantID string `json:"participant_id"`
	}
	if err := bindJSON(c, &body); err != nil {
		writeError(c, http.StatusBadRequest, "invalid request body")
		return
	}

	baseURL := s.resolveBaseURL(body.BaseURL, body.GroupID)
	body.GroupID = spliit.ExtractGroupID(strings.TrimSpace(body.GroupID))
	body.Alias = strings.TrimSpace(body.Alias)
	if body.GroupID == "" {
		writeError(c, http.StatusBadRequest, "group_id must not be empty")
		return
	}
	// Joining without knowing who you are leaves a group that every write tool
	// would reject later, so require it up front rather than storing a stub.
	if strings.TrimSpace(body.ParticipantID) == "" {
		writeError(c, http.StatusBadRequest,
			"participant_id is required: pick which participant is you in this group")
		return
	}

	group, err := s.clients.GetGroup(ctx, baseURL, body.GroupID)
	if err != nil {
		writeError(c, http.StatusBadGateway, "could not fetch that group: "+err.Error())
		return
	}

	// The participant must really exist in this group, or every later tool call
	// would fail with a confusing upstream error instead of a clear one here.
	participant := spliit.FindParticipant(group, body.ParticipantID)
	if participant == nil {
		writeError(c, http.StatusBadRequest, "that participant does not exist in this group")
		return
	}

	if body.Alias == "" {
		body.Alias = group.Name
	}

	row := &store.Group{
		UserSub:         user.Sub,
		BaseURL:         baseURL,
		SpliitGroupID:   group.ID,
		Alias:           body.Alias,
		GroupName:       group.Name,
		Currency:        group.Currency,
		ParticipantID:   participant.ID,
		ParticipantName: participant.Name,
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

// CreateSpliitGroup creates a brand new group on a Spliit instance and joins it
// in one step, with the caller as a participant.
//
// Creating and joining are deliberately not separable here: a group that exists
// in Spliit but was never registered is unreachable through this server, and
// Spliit offers no way to list groups to find it again.
func (s *Server) CreateSpliitGroup(c *gin.Context) {
	user := UserFromContext(c)
	ctx := c.Request.Context()

	var body struct {
		// BaseURL names the instance to create it on; empty uses the default.
		BaseURL  string `json:"base_url"`
		Name     string `json:"name"`
		Currency string `json:"currency"`
		Alias    string `json:"alias"`
		// Participants excludes the caller, who is added automatically.
		Participants []string `json:"participants"`
		// YourName is the participant name to use for the caller, defaulting to
		// their display name.
		YourName string `json:"your_name"`
	}
	if err := bindJSON(c, &body); err != nil {
		writeError(c, http.StatusBadRequest, "invalid request body")
		return
	}

	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" {
		writeError(c, http.StatusBadRequest, "name must not be empty")
		return
	}

	yourName := strings.TrimSpace(body.YourName)
	if yourName == "" {
		yourName = user.DisplayName
	}
	if yourName == "" {
		writeError(c, http.StatusBadRequest,
			"set your name first, or pass your_name")
		return
	}

	baseURL := s.resolveBaseURL(body.BaseURL, "")

	form, err := buildGroupForm(body.Name, body.Currency, yourName, body.Participants)
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}

	groupID, err := s.clients.CreateGroup(ctx, baseURL, form)
	if err != nil {
		writeError(c, http.StatusBadGateway, "could not create the group: "+err.Error())
		return
	}

	// Read it back for the participant IDs Spliit generated.
	group, err := s.clients.GetGroup(ctx, baseURL, groupID)
	if err != nil {
		writeError(c, http.StatusBadGateway,
			"the group was created but could not be read back: "+err.Error())
		return
	}

	alias := strings.TrimSpace(body.Alias)
	if alias == "" {
		alias = group.Name
	}

	row := &store.Group{
		UserSub: user.Sub, BaseURL: baseURL, SpliitGroupID: group.ID,
		Alias: alias, GroupName: group.Name, Currency: group.Currency,
	}
	if p := spliit.FindParticipantByName(group, yourName); p != nil {
		row.ParticipantID, row.ParticipantName = p.ID, p.Name
	}

	created, err := s.store.CreateGroup(ctx, row)
	if errors.Is(err, store.ErrConflict) {
		// The group exists upstream now, so surface its ID rather than losing it.
		writeError(c, http.StatusConflict, fmt.Sprintf(
			"the group was created (id %s) but that alias is already taken; "+
				"join it manually with a different alias", group.ID))
		return
	}
	if err != nil {
		s.serverError(c, "register created group", err)
		return
	}
	c.JSON(http.StatusCreated, created)
}

// buildGroupForm assembles a Spliit group form with the caller first.
func buildGroupForm(name, currency, yourName string, others []string) (shape.ModifyGroupForm, error) {
	participants := []shape.ModifyGroupParticipant{{Name: yourName}}
	seen := map[string]bool{strings.ToLower(yourName): true}

	for _, other := range others {
		other = strings.TrimSpace(other)
		if other == "" || seen[strings.ToLower(other)] {
			continue
		}
		// Spliit rejects duplicate participant names outright, so drop them here
		// rather than sending a request that is guaranteed to fail.
		seen[strings.ToLower(other)] = true
		participants = append(participants, shape.ModifyGroupParticipant{Name: other})
	}

	for _, p := range participants {
		if len(p.Name) < 2 {
			return shape.ModifyGroupForm{}, fmt.Errorf(
				"participant name %q is too short; Spliit requires at least 2 characters", p.Name)
		}
	}
	if len(name) < 2 {
		return shape.ModifyGroupForm{}, errors.New(
			"group name is too short; Spliit requires at least 2 characters")
	}

	if strings.TrimSpace(currency) == "" {
		currency = "USD"
	}
	return shape.ModifyGroupForm{
		Name:         name,
		Currency:     strings.TrimSpace(currency),
		Participants: participants,
	}, nil
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
		group, err := s.clients.GetGroup(ctx, existing.BaseURL, existing.SpliitGroupID)
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
