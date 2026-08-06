// Package spliit wraps the Spliit tRPC client with the concerns this server
// adds on top: one client per registered instance, group resolution scoped to
// the calling user, and resolving which participant is "you".
//
// Note that Spliit itself performs no authentication whatsoever — its tRPC
// context is empty and every procedure is public. The group ID *is* the
// credential. Nothing in this package should ever be handed a group ID that did
// not come out of the caller's own store rows.
package spliit

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/shopspring/decimal"
	api "go.chrastecky.dev/spliit-api/spliit"
	"go.chrastecky.dev/spliit-api/spliit/amount"
	"go.chrastecky.dev/spliit-api/spliit/endpoint"
	"go.chrastecky.dev/spliit-api/spliit/model"
	"go.chrastecky.dev/spliit-api/spliit/shape"
)

// ErrParticipantMissing means the participant pinned as "you" no longer exists
// in the group — usually because it was removed and re-added in Spliit, which
// mints a fresh ID. The user has to re-pick it in the config page.
var ErrParticipantMissing = errors.New("pinned participant no longer exists in this group")

// Clients hands out one API client per Spliit base URL. Clients are safe for
// concurrent use, so they are cached rather than rebuilt per call.
type Clients struct {
	timeout time.Duration

	mu      sync.Mutex
	clients map[string]api.Client
}

// NewClients returns a cache whose clients use the given per-request timeout.
func NewClients(timeout time.Duration) *Clients {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Clients{timeout: timeout, clients: make(map[string]api.Client)}
}

// For returns the client for a Spliit instance's tRPC base URL.
func (c *Clients) For(baseURL string) api.Client {
	c.mu.Lock()
	defer c.mu.Unlock()

	if client, ok := c.clients[baseURL]; ok {
		return client
	}
	client := api.NewClientWithSender(
		api.NewHTTPSender(baseURL, &http.Client{Timeout: c.timeout}),
	)
	c.clients[baseURL] = client
	return client
}

// send dispatches a single typed call and returns its decoded output.
//
// The input is typed as TIn rather than `any` so that the type parameters are
// inferred from the argument; Go cannot infer them from the endpoint alone,
// since a concrete endpoint is only converted to the interface at the call.
//
// The library reports failures in two places: an error from SendRequests for
// transport-level problems, and a per-call error for tRPC-level ones. Both must
// be checked or a failed call silently yields a zero value.
func send[TIn, TOut any](
	ctx context.Context, client api.Client,
	ep endpoint.Endpoint[TIn, TOut], input TIn,
) (TOut, error) {
	var zero TOut

	call := api.NewCall(ep, input)
	if _, err := client.SendRequests(ctx, call); err != nil {
		return zero, fmt.Errorf("spliit %s: %w", ep.Name(), err)
	}
	if err := call.ErrValue(); err != nil {
		return zero, fmt.Errorf("spliit %s: %w", ep.Name(), err)
	}
	return call.Output(), nil
}

// GetGroup fetches a group, including its participant list.
func (c *Clients) GetGroup(ctx context.Context, baseURL, groupID string) (*model.Group, error) {
	out, err := send(ctx, c.For(baseURL), &endpoint.GetGroup{},
		shape.GetGroupRequest{GroupID: groupID})
	if err != nil {
		return nil, err
	}
	if out.Group == nil {
		return nil, fmt.Errorf("group %q not found on this Spliit instance", groupID)
	}
	return out.Group, nil
}

// GetGroupDetails fetches a group plus the set of participants that have
// expenses, which Spliit uses to decide who may be safely removed.
func (c *Clients) GetGroupDetails(ctx context.Context, baseURL, groupID string) (*model.Group, []string, error) {
	out, err := send(ctx, c.For(baseURL), &endpoint.GetGroupDetails{},
		shape.GetGroupDetailsRequest{GroupID: groupID})
	if err != nil {
		return nil, nil, err
	}
	if out.GetGroupResponse == nil || out.Group == nil {
		return nil, nil, fmt.Errorf("group %q not found on this Spliit instance", groupID)
	}
	return out.Group, out.ParticipantsWithExpenses, nil
}

// CreateGroup creates a new group and returns its ID.
func (c *Clients) CreateGroup(ctx context.Context, baseURL string, form shape.ModifyGroupForm) (string, error) {
	out, err := send(ctx, c.For(baseURL), &endpoint.CreateGroup{},
		shape.CreateGroupRequest{FormValues: form})
	if err != nil {
		return "", err
	}
	return out.GroupID, nil
}

// UpdateGroup edits a group's name, currency, or participants.
func (c *Clients) UpdateGroup(ctx context.Context, baseURL, groupID string, form shape.ModifyGroupForm, participantID *string) error {
	_, err := send(ctx, c.For(baseURL), &endpoint.UpdateGroup{},
		shape.UpdateGroupRequest{GroupID: groupID, FormValues: form, ParticipantID: participantID})
	return err
}

// ListExpenses returns a page of expenses, newest first.
func (c *Clients) ListExpenses(ctx context.Context, baseURL, groupID string, limit, cursor *int, filter *string) (*shape.ListExpensesResponse, error) {
	out, err := send(ctx, c.For(baseURL), &endpoint.ListExpenses{},
		shape.ListExpensesRequest{GroupID: groupID, Limit: limit, Cursor: cursor, Filter: filter})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// GetExpense fetches a single expense.
func (c *Clients) GetExpense(ctx context.Context, baseURL, groupID, expenseID string) (*model.Expense, error) {
	out, err := send(ctx, c.For(baseURL), &endpoint.GetExpense{},
		shape.GetExpenseRequest{GroupID: groupID, ExpenseID: expenseID})
	if err != nil {
		return nil, err
	}
	return &out.Expense, nil
}

// CreateExpense records a new expense and returns its ID.
func (c *Clients) CreateExpense(ctx context.Context, baseURL, groupID string, form shape.ModifyExpenseForm, participantID *string) (string, error) {
	req := shape.CreateExpenseRequest{GroupID: groupID, FormValues: form, ParticipantID: participantID}
	req.ApplyDefaults()

	out, err := send(ctx, c.For(baseURL), &endpoint.CreateExpense{}, req)
	if err != nil {
		return "", err
	}
	return out.ExpenseID, nil
}

// UpdateExpense replaces an existing expense's fields.
func (c *Clients) UpdateExpense(ctx context.Context, baseURL, groupID, expenseID string, form shape.ModifyExpenseForm, participantID *string) error {
	req := shape.UpdateExpenseRequest{
		GroupID: groupID, ExpenseID: expenseID,
		FormValues: form, ParticipantID: participantID,
	}
	req.ApplyDefaults()

	_, err := send(ctx, c.For(baseURL), &endpoint.UpdateExpense{}, req)
	return err
}

// DeleteExpense removes an expense.
func (c *Clients) DeleteExpense(ctx context.Context, baseURL, groupID, expenseID string, participantID *string) error {
	_, err := send(ctx, c.For(baseURL), &endpoint.DeleteExpense{},
		shape.DeleteExpenseRequest{GroupID: groupID, ExpenseID: expenseID, ParticipantID: participantID})
	return err
}

// ListBalances returns per-participant balances and suggested settlements.
func (c *Clients) ListBalances(ctx context.Context, baseURL, groupID string) (*shape.ListBalancesResponse, error) {
	out, err := send(ctx, c.For(baseURL), &endpoint.ListBalances{},
		shape.ListBalancesRequest{GroupID: groupID})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// GetStats returns spend totals for a group, optionally narrowed to one participant.
func (c *Clients) GetStats(ctx context.Context, baseURL, groupID string, participantID *string) (*shape.GetStatsResponse, error) {
	out, err := send(ctx, c.For(baseURL), &endpoint.GetStats{},
		shape.GetStatsRequest{GroupID: groupID, ParticipantID: participantID})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ListActivities returns a page of the group's activity log.
func (c *Clients) ListActivities(ctx context.Context, baseURL, groupID string, limit, cursor *uint) (*shape.ListActivitiesResponse, error) {
	out, err := send(ctx, c.For(baseURL), &endpoint.ListActivities{},
		shape.ListActivitiesRequest{GroupID: groupID, Limit: limit, Cursor: cursor})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// ListCategories returns the instance's expense categories.
func (c *Clients) ListCategories(ctx context.Context, baseURL string) ([]model.Category, error) {
	out, err := send(ctx, c.For(baseURL), &endpoint.ListCategories{}, &shape.NilRequest{})
	if err != nil {
		return nil, err
	}
	return out.Categories, nil
}

// ToAmount converts a decimal major-unit value to Spliit's integer minor units.
//
// The upstream amount.FromDecimal insists on an exponent of exactly -2, so a
// plain value like 10.5 (exponent -1) would be rejected. Shifting and rounding
// accepts any scale and rounds half away from zero at the cent.
func ToAmount(value decimal.Decimal) amount.Amount {
	return amount.Amount(value.Shift(2).Round(0).IntPart())
}

// FindParticipant returns the participant with the given ID, or nil.
func FindParticipant(group *model.Group, participantID string) *model.Participant {
	for _, p := range group.Participants {
		if p != nil && p.ID == participantID {
			return p
		}
	}
	return nil
}

// ParticipantNames lists a group's participant names in order.
func ParticipantNames(group *model.Group) []string {
	names := make([]string, 0, len(group.Participants))
	for _, p := range group.Participants {
		if p != nil {
			names = append(names, p.Name)
		}
	}
	return names
}

// FindParticipantByName returns the participant with the given name,
// case-insensitively, or nil.
func FindParticipantByName(group *model.Group, name string) *model.Participant {
	want := strings.TrimSpace(name)
	for _, p := range group.Participants {
		if p != nil && strings.EqualFold(strings.TrimSpace(p.Name), want) {
			return p
		}
	}
	return nil
}

// ExtractGroupID accepts either a bare group ID or a full Spliit URL such as
// https://spliit.app/groups/<id>/expenses, since pasting the browser URL is the
// natural thing to do when naming a group.
func ExtractGroupID(input string) string {
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
