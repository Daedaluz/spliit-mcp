// Package mcp exposes a user's Spliit groups as MCP tools.
//
// Every tool resolves its `group` argument against the calling user's own rows
// (see store.ResolveGroup). That scoping is the authorization boundary: Spliit
// itself will happily serve any group ID to anyone, so a group the user has not
// registered in the config page must be unreachable through this server, even
// if the model supplies a valid ID from somewhere else.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/daedaluz/spliit-mcp/internal/config"
	"github.com/daedaluz/spliit-mcp/internal/spliit"
	"github.com/daedaluz/spliit-mcp/internal/store"
)

// Deps are the collaborators the tool handlers need.
type Deps struct {
	Config  *config.Config
	Store   *store.Store
	Clients *spliit.Clients
	Log     *slog.Logger
	// Version is reported to MCP clients during initialization. Defaults to
	// "dev" when the binary was built without build metadata.
	Version string
}

// resolved is a group plus everything needed to call Spliit for it.
type resolved struct {
	group *store.Group
}

// baseURL is the tRPC endpoint of the instance hosting this group.
func (r resolved) baseURL() string { return r.group.BaseURL }

// spliitID is the group's ID within that instance.
func (r resolved) spliitID() string { return r.group.SpliitGroupID }

// errNoIdentity is returned when a group has no participant pinned as "you".
var errNoIdentity = errors.New("no participant is pinned as you for this group")

// me returns the participant ID representing the caller in this group.
func (r resolved) me() (string, error) {
	if r.group.ParticipantID == "" {
		return "", fmt.Errorf(
			"%w: set it with set_active_participant", errNoIdentity)
	}
	return r.group.ParticipantID, nil
}

// NewServer builds the MCP server and registers every tool.
func NewServer(deps Deps) *mcp.Server {
	version := deps.Version
	if version == "" {
		version = "dev"
	}

	t := &tools{deps: deps}

	server := mcp.NewServer(&mcp.Implementation{
		Name:    "spliit-mcp",
		Title:   "Spliit",
		Version: version,
		Description: "Read and record shared expenses in Spliit groups. " +
			"Groups are made available through the spliit-mcp config page.",
		// Surfaced to clients at initialize, so the config page is discoverable
		// without calling a tool first.
		WebsiteURL: t.configURL(),
	}, &mcp.ServerOptions{
		Instructions: "Each tool takes a `group`, which is the alias shown by list_groups. " +
			"Only groups the signed-in user has registered are reachable. " +
			"Amounts are in the group's currency as decimal numbers, e.g. 12.50. " +
			"When the user says \"I paid\" or \"my share\", that refers to the participant " +
			"pinned as them in that group; leave paid_by empty to default to it. " +
			"Groups can also be joined and left through the tools; call get_server_info " +
			"for this server's URLs when the user asks how to connect another client.",
	})

	t.register(server)
	return server
}

type tools struct {
	deps Deps
}

// configURL is the address of the config web UI, empty if unconfigured.
func (t *tools) configURL() string {
	if t.deps.Config == nil {
		return ""
	}
	return t.deps.Config.PublicURL
}

// mcpURL is this server's MCP endpoint, which doubles as its OAuth resource
// identifier.
func (t *tools) mcpURL() string {
	if t.deps.Config == nil {
		return ""
	}
	return t.deps.Config.MCPResourceURL()
}

// atConfigPage renders a pointer to the config UI, naming the URL when one is
// configured so the user can act on it without asking where to go.
func (t *tools) atConfigPage() string {
	if url := t.configURL(); url != "" {
		return "the config page at " + url
	}
	return "the spliit-mcp config page"
}

// userFromRequest extracts the authenticated OIDC subject from the verified
// bearer token attached to the MCP request.
func (t *tools) userFromRequest(req *mcp.CallToolRequest) (string, error) {
	if req.Extra == nil || req.Extra.TokenInfo == nil {
		return "", errors.New("this request carries no verified identity")
	}
	sub := req.Extra.TokenInfo.UserID
	if sub == "" {
		return "", errors.New("the access token has no subject")
	}
	return sub, nil
}

// resolve turns a user-supplied group reference into a concrete group plus its
// server, refusing anything the caller has not registered.
func (t *tools) resolve(ctx context.Context, sub, ref string) (resolved, error) {
	if ref == "" {
		return resolved{}, errors.New("group is required; call list_groups to see the available ones")
	}

	group, err := t.deps.Store.ResolveGroup(ctx, sub, ref)
	if errors.Is(err, store.ErrNotFound) {
		return resolved{}, fmt.Errorf(
			"no group %q is available to you; call list_groups to see them, "+
				"or join_group to add it", ref)
	}
	if err != nil {
		return resolved{}, err
	}
	return resolved{group: group}, nil
}

// baseURLFor picks the Spliit instance for a request that is not yet tied to a
// stored group: an explicit URL wins, then one derived from a pasted group
// link, and finally the configured default.
func (t *tools) baseURLFor(explicit, groupRef string) string {
	if trimmed := strings.TrimRight(strings.TrimSpace(explicit), "/"); trimmed != "" {
		return trimmed
	}
	if derived := spliit.DeriveBaseURL(groupRef); derived != "" {
		return derived
	}
	if t.deps.Config != nil {
		return t.deps.Config.Spliit.DefaultURL
	}
	return ""
}

// toolError converts a handler failure into a tool-level error result.
//
// These are returned as IsError results rather than protocol errors so the
// model sees the message and can correct itself — a wrong alias should prompt a
// list_groups call, not abort the conversation.
func toolError[Out any](err error) (*mcp.CallToolResult, Out, error) {
	var zero Out
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
	}, zero, nil
}

// toolResult pairs a structured output value with a short text rendering.
//
// The JSON is emitted as a second text block rather than left to the SDK, which
// only serializes structured output into text when Content is empty. Setting a
// summary would otherwise suppress it entirely, and a client that does not read
// structuredContent would see "1 group(s) available" with no groups in it.
func toolResult[Out any](summary string, out Out) (*mcp.CallToolResult, Out, error) {
	content := []mcp.Content{&mcp.TextContent{Text: summary}}

	if payload, err := json.Marshal(out); err == nil {
		content = append(content, &mcp.TextContent{Text: string(payload)})
	}
	return &mcp.CallToolResult{Content: content}, out, nil
}
