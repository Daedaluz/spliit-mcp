package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shopspring/decimal"
	"go.chrastecky.dev/spliit-api/spliit/model"
	"go.chrastecky.dev/spliit-api/spliit/shape"

	"github.com/daedaluz/spliit-mcp/internal/spliit"
	"github.com/daedaluz/spliit-mcp/internal/store"
)

// groupArg is embedded by every tool input that targets one group.
type groupArg struct {
	Group string `json:"group" jsonschema:"The group alias from list_groups"`
}

func (t *tools) register(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:  "list_groups",
		Title: "List available groups",
		Description: "List the Spliit groups available to you, with the alias to use in other " +
			"tools, which participant represents you, and which Spliit server hosts each one.",
	}, t.listGroups)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_group",
		Title:       "Get group details",
		Description: "Get a group's participants, currency, and which participant represents you.",
	}, t.getGroup)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_balances",
		Title:       "Get balances",
		Description: "Get who owes what in a group, framed relative to you, plus suggested settlements.",
	}, t.getBalances)

	mcp.AddTool(s, &mcp.Tool{
		Name:  "list_expenses",
		Title: "List expenses",
		Description: "List a group's expenses, newest first. Pass mine=true for expenses you paid " +
			"— a question like \"my latest expense\" needs it, since the group's most recent " +
			"expense is usually somebody else's. Also filters by paid_by, involving, and title text.",
	}, t.listExpenses)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_expense",
		Title:       "Get an expense",
		Description: "Get one expense in full, including how it was split.",
	}, t.getExpense)

	mcp.AddTool(s, &mcp.Tool{
		Name:  "create_expense",
		Title: "Record an expense",
		Description: "Record a new expense. Defaults to you having paid, split evenly between " +
			"all participants. Amounts are decimal numbers in the group's currency.",
	}, t.createExpense)

	mcp.AddTool(s, &mcp.Tool{
		Name:  "update_expense",
		Title: "Update an expense",
		Description: "Change an existing expense. Pass only the fields that should change; " +
			"everything else, including who paid, who shares it and how it is split, is kept " +
			"exactly as it is. To add receipt details, pass just notes (or title). Pass paid_for " +
			"only when the split itself should change — it resets the split to even shares " +
			"between the named participants.",
	}, t.updateExpense)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete_expense",
		Title:       "Delete an expense",
		Description: "Permanently delete an expense from a group.",
	}, t.deleteExpense)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_stats",
		Title:       "Get spending totals",
		Description: "Get total group spending and your own share.",
	}, t.getStats)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_activities",
		Title:       "List recent activity",
		Description: "List a group's recent activity log entries.",
	}, t.listActivities)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_categories",
		Title:       "List expense categories",
		Description: "List the expense categories available on a group's Spliit server, with the IDs create_expense accepts.",
	}, t.listCategories)

	mcp.AddTool(s, &mcp.Tool{
		Name:  "create_group",
		Title: "Create a group",
		Description: "Create a new Spliit group and join it so the other tools can use it. " +
			"You are added as a participant automatically.",
	}, t.createGroup)

	mcp.AddTool(s, &mcp.Tool{
		Name:  "update_group",
		Title: "Update a group",
		Description: "Rename a group, change its currency, or add, rename and remove participants. " +
			"Only the fields you pass change. A participant with expenses cannot be removed.",
	}, t.updateGroup)

	// Group management. Spliit has no notion of membership, so "joining" means
	// registering a group ID here so the other tools can reach it.
	mcp.AddTool(s, &mcp.Tool{
		Name:  "get_server_info",
		Title: "Get this server's URLs",
		Description: "Get this server's MCP endpoint and config page URL, plus a ready-to-paste " +
			"command for adding it on another machine.",
	}, t.getServerInfo)

	mcp.AddTool(s, &mcp.Tool{
		Name:  "inspect_group",
		Title: "Inspect a group before joining",
		Description: "Look up a Spliit group by ID or URL without joining it, to see its " +
			"participants. Use this before join_group to learn which name to pass as you.",
	}, t.inspectGroup)

	mcp.AddTool(s, &mcp.Tool{
		Name:  "join_group",
		Title: "Join an existing group",
		Description: "Make an existing Spliit group available to these tools. You must say which " +
			"participant is you; call inspect_group first if you do not know the names.",
	}, t.joinGroup)

	mcp.AddTool(s, &mcp.Tool{
		Name:  "leave_group",
		Title: "Remove a group",
		Description: "Remove a group from the ones available to you. Nothing is deleted in " +
			"Spliit and the group can be joined again with the same ID.",
	}, t.leaveGroup)

	mcp.AddTool(s, &mcp.Tool{
		Name:  "set_active_participant",
		Title: "Set who you are in a group",
		Description: "Change which participant represents you in a group. Use this if the " +
			"participant was renamed or re-created in Spliit.",
	}, t.setActiveParticipant)
}

// ---------------------------------------------------------------------------
// list_groups

type listGroupsInput struct{}

type groupSummary struct {
	Alias     string `json:"alias"`
	Name      string `json:"name"`
	Currency  string `json:"currency"`
	Server    string `json:"server"`
	ServerURL string `json:"server_url"`
	// URL is the link to open the group in a browser.
	URL             string `json:"url,omitempty"`
	YouAre          string `json:"you_are,omitempty"`
	NeedsSetup      bool   `json:"needs_setup,omitempty"`
	SpliitGroupID   string `json:"spliit_group_id"`
	SetupHintForYou string `json:"setup_hint,omitempty"`
}

type listGroupsOutput struct {
	Groups []groupSummary `json:"groups"`
}

func (t *tools) listGroups(ctx context.Context, req *mcp.CallToolRequest, _ listGroupsInput) (*mcp.CallToolResult, listGroupsOutput, error) {
	sub, err := t.userFromRequest(req)
	if err != nil {
		return toolError[listGroupsOutput](err)
	}

	groups, err := t.deps.Store.ListGroups(ctx, sub)
	if err != nil {
		return toolError[listGroupsOutput](err)
	}

	out := listGroupsOutput{Groups: make([]groupSummary, 0, len(groups))}
	for _, g := range groups {
		summary := groupSummary{
			Alias:         g.Alias,
			Name:          g.GroupName,
			Currency:      g.Currency,
			Server:        spliit.HostOf(g.BaseURL),
			YouAre:        g.ParticipantName,
			SpliitGroupID: g.SpliitGroupID,
			ServerURL:     g.BaseURL,
			URL:           spliit.WebURL(g.BaseURL, g.SpliitGroupID),
		}
		if g.ParticipantID == "" {
			summary.NeedsSetup = true
			summary.SetupHintForYou = "no participant is set as you; fix it with set_active_participant, or in " + t.atConfigPage()
		}
		out.Groups = append(out.Groups, summary)
	}

	if len(out.Groups) == 0 {
		return toolResult(fmt.Sprintf(
			"You have no groups available yet. Join one with join_group, or add it in %s.",
			t.atConfigPage()), out)
	}

	// Name them: a bare count is useless to a client that shows only this line.
	lines := make([]string, 0, len(out.Groups))
	for _, g := range out.Groups {
		line := fmt.Sprintf("%s — %s (%s, %s)", g.Alias, g.Name, g.Currency, g.Server)
		if g.URL != "" {
			line += " " + g.URL
		}
		if g.YouAre != "" {
			line += ", you are " + g.YouAre
		}
		if g.NeedsSetup {
			line += " — no participant set as you"
		}
		lines = append(lines, line)
	}
	return toolResult(fmt.Sprintf("%d group(s) available:\n%s",
		len(out.Groups), strings.Join(lines, "\n")), out)
}

// ---------------------------------------------------------------------------
// get_group

type getGroupInput struct {
	groupArg
}

type participantInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	IsYou  bool   `json:"is_you,omitempty"`
	Active bool   `json:"has_expenses"`
}

type getGroupOutput struct {
	Alias        string            `json:"alias"`
	Name         string            `json:"name"`
	Currency     string            `json:"currency"`
	Server       string            `json:"server"`
	URL          string            `json:"url,omitempty"`
	YouAre       string            `json:"you_are,omitempty"`
	Participants []participantInfo `json:"participants"`
	Information  string            `json:"information,omitempty"`
}

func (t *tools) getGroup(ctx context.Context, req *mcp.CallToolRequest, in getGroupInput) (*mcp.CallToolResult, getGroupOutput, error) {
	sub, err := t.userFromRequest(req)
	if err != nil {
		return toolError[getGroupOutput](err)
	}
	r, err := t.resolve(ctx, sub, in.Group)
	if err != nil {
		return toolError[getGroupOutput](err)
	}

	group, withExpenses, err := t.deps.Clients.GetGroupDetails(ctx, r.baseURL(), r.spliitID())
	if err != nil {
		return toolError[getGroupOutput](err)
	}

	active := make(map[string]bool, len(withExpenses))
	for _, id := range withExpenses {
		active[id] = true
	}

	out := getGroupOutput{
		Alias:    r.group.Alias,
		Name:     group.Name,
		Currency: group.Currency,
		Server:   spliit.HostOf(r.baseURL()),
		URL:      spliit.WebURL(r.baseURL(), r.spliitID()),
		YouAre:   r.group.ParticipantName,
	}
	if group.Information != nil {
		out.Information = *group.Information
	}
	for _, p := range group.Participants {
		if p == nil {
			continue
		}
		out.Participants = append(out.Participants, participantInfo{
			ID: p.ID, Name: p.Name,
			IsYou:  p.ID == r.group.ParticipantID,
			Active: active[p.ID],
		})
	}
	return toolResult(fmt.Sprintf("%s — %d participants, currency %s. %s",
		group.Name, len(out.Participants), group.Currency, out.URL), out)
}

// ---------------------------------------------------------------------------
// get_balances

type getBalancesInput struct {
	groupArg
}

type balanceEntry struct {
	Participant string `json:"participant"`
	IsYou       bool   `json:"is_you,omitempty"`
	Paid        string `json:"paid"`
	Share       string `json:"share"`
	// Net is positive when this participant is owed money and negative when
	// they owe it.
	Net string `json:"net"`
}

type settlement struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Amount string `json:"amount"`
}

type getBalancesOutput struct {
	Group       string         `json:"group"`
	Currency    string         `json:"currency"`
	YourNet     string         `json:"your_net,omitempty"`
	YourSummary string         `json:"your_summary,omitempty"`
	Balances    []balanceEntry `json:"balances"`
	Settlements []settlement   `json:"suggested_settlements"`
}

func (t *tools) getBalances(ctx context.Context, req *mcp.CallToolRequest, in getBalancesInput) (*mcp.CallToolResult, getBalancesOutput, error) {
	sub, err := t.userFromRequest(req)
	if err != nil {
		return toolError[getBalancesOutput](err)
	}
	r, err := t.resolve(ctx, sub, in.Group)
	if err != nil {
		return toolError[getBalancesOutput](err)
	}

	group, err := t.deps.Clients.GetGroup(ctx, r.baseURL(), r.spliitID())
	if err != nil {
		return toolError[getBalancesOutput](err)
	}
	balances, err := t.deps.Clients.ListBalances(ctx, r.baseURL(), r.spliitID())
	if err != nil {
		return toolError[getBalancesOutput](err)
	}

	// Spliit keys balances by participant ID; the model needs names.
	names := make(map[string]string, len(group.Participants))
	for _, p := range group.Participants {
		if p != nil {
			names[p.ID] = p.Name
		}
	}
	nameOf := func(id string) string {
		if n, ok := names[id]; ok {
			return n
		}
		return id
	}

	out := getBalancesOutput{
		Group: r.group.Alias, Currency: group.Currency,
		Balances:    make([]balanceEntry, 0, len(balances.Balances)),
		Settlements: make([]settlement, 0, len(balances.Reimbursements)),
	}

	for id, b := range balances.Balances {
		entry := balanceEntry{
			Participant: nameOf(id),
			IsYou:       id == r.group.ParticipantID,
			Paid:        b.Paid.AsDecimal().StringFixed(2),
			Share:       b.PaidFor.AsDecimal().StringFixed(2),
			Net:         b.Total.AsDecimal().StringFixed(2),
		}
		if entry.IsYou {
			net := b.Total.AsDecimal()
			out.YourNet = net.StringFixed(2)
			switch {
			case net.IsPositive():
				out.YourSummary = fmt.Sprintf("You are owed %s %s overall.",
					net.StringFixed(2), group.Currency)
			case net.IsNegative():
				out.YourSummary = fmt.Sprintf("You owe %s %s overall.",
					net.Neg().StringFixed(2), group.Currency)
			default:
				out.YourSummary = "You are settled up."
			}
		}
		out.Balances = append(out.Balances, entry)
	}

	for _, reimbursement := range balances.Reimbursements {
		out.Settlements = append(out.Settlements, settlement{
			From:   nameOf(reimbursement.From),
			To:     nameOf(reimbursement.To),
			Amount: reimbursement.Amount.AsDecimal().StringFixed(2),
		})
	}

	summary := out.YourSummary
	if summary == "" {
		summary = fmt.Sprintf("Balances for %s.", r.group.Alias)
	}
	return toolResult(summary, out)
}

// ---------------------------------------------------------------------------
// list_expenses

type listExpensesInput struct {
	groupArg
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum expenses to return (default 20, max 100)"`
	Cursor int    `json:"cursor,omitempty" jsonschema:"Pagination cursor from a previous call"`
	Filter string `json:"filter,omitempty" jsonschema:"Only return expenses whose title matches this text"`
	// Mine is how a question about "my expenses" gets answered correctly.
	// Without it the whole group's expenses come back and the most recent one
	// usually belongs to somebody else.
	Mine   bool   `json:"mine,omitempty" jsonschema:"Only expenses you paid for. Use this for questions like 'my latest expense'"`
	PaidBy string `json:"paid_by,omitempty" jsonschema:"Only expenses paid by this participant name"`
	// Involving covers the other reading of "mine": expenses you share in,
	// whoever actually paid.
	Involving string `json:"involving,omitempty" jsonschema:"Only expenses this participant shares in, whoever paid. Pass 'me' for yourself"`
}

type expenseSummary struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Date     string `json:"date"`
	Amount   string `json:"amount"`
	PaidBy   string `json:"paid_by"`
	PaidByMe bool   `json:"paid_by_you,omitempty"`
	Category string `json:"category,omitempty"`
}

type listExpensesOutput struct {
	Group      string           `json:"group"`
	Currency   string           `json:"currency"`
	Expenses   []expenseSummary `json:"expenses"`
	HasMore    bool             `json:"has_more"`
	NextCursor int              `json:"next_cursor,omitempty"`
}

func (t *tools) listExpenses(ctx context.Context, req *mcp.CallToolRequest, in listExpensesInput) (*mcp.CallToolResult, listExpensesOutput, error) {
	sub, err := t.userFromRequest(req)
	if err != nil {
		return toolError[listExpensesOutput](err)
	}
	r, err := t.resolve(ctx, sub, in.Group)
	if err != nil {
		return toolError[listExpensesOutput](err)
	}

	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	var filter *string
	if strings.TrimSpace(in.Filter) != "" {
		filter = &in.Filter
	}

	// Resolve the participant filters to IDs before paging. Spliit can only
	// filter by title, so who-paid and who-shares are applied here.
	payerID, involvedID, err := t.resolveExpenseFilters(ctx, r, in)
	if err != nil {
		return toolError[listExpensesOutput](err)
	}

	out := listExpensesOutput{
		Group: r.group.Alias, Currency: r.group.Currency,
		Expenses: make([]expenseSummary, 0, limit),
	}

	// Filtering happens after fetching, so a page may yield nothing. Keep
	// pulling pages until the limit is met, bounded so a narrow filter over a
	// long history cannot walk the whole group.
	const maxPages = 10
	cursor := in.Cursor
	for page := 0; page < maxPages; page++ {
		var cursorArg *int
		if cursor > 0 {
			cursorArg = &cursor
		}

		fetch := limit
		result, err := t.deps.Clients.ListExpenses(ctx, r.baseURL(), r.spliitID(), &fetch, cursorArg, filter)
		if err != nil {
			return toolError[listExpensesOutput](err)
		}

		for i := range result.Expenses {
			expense := &result.Expenses[i]
			if payerID != "" && expense.PaidByID != payerID {
				continue
			}
			if involvedID != "" && !sharesIn(expense, involvedID) {
				continue
			}
			out.Expenses = append(out.Expenses, t.summarize(expense, r.group.ParticipantID))
			if len(out.Expenses) == limit {
				break
			}
		}

		out.HasMore, out.NextCursor = result.HasMore, result.NextCursor
		if len(out.Expenses) == limit || !result.HasMore {
			break
		}
		cursor = result.NextCursor
	}

	return toolResult(t.expensesSummary(r, in, out), out)
}

// resolveExpenseFilters maps the participant filters onto IDs in this group.
func (t *tools) resolveExpenseFilters(ctx context.Context, r resolved, in listExpensesInput) (payerID, involvedID string, err error) {
	wantsMe := in.Mine ||
		strings.EqualFold(strings.TrimSpace(in.PaidBy), "me") ||
		strings.EqualFold(strings.TrimSpace(in.Involving), "me")

	if wantsMe {
		me, err := r.me()
		if err != nil {
			return "", "", err
		}
		if in.Mine || strings.EqualFold(strings.TrimSpace(in.PaidBy), "me") {
			payerID = me
		}
		if strings.EqualFold(strings.TrimSpace(in.Involving), "me") {
			involvedID = me
		}
	}

	// Named participants need the group's roster to resolve.
	named := (in.PaidBy != "" && payerID == "") || (in.Involving != "" && involvedID == "")
	if !named {
		return payerID, involvedID, nil
	}

	group, err := t.deps.Clients.GetGroup(ctx, r.baseURL(), r.spliitID())
	if err != nil {
		return "", "", err
	}
	if in.PaidBy != "" && payerID == "" {
		p, err := findByName(group, in.PaidBy)
		if err != nil {
			return "", "", err
		}
		payerID = p.ID
	}
	if in.Involving != "" && involvedID == "" {
		p, err := findByName(group, in.Involving)
		if err != nil {
			return "", "", err
		}
		involvedID = p.ID
	}
	return payerID, involvedID, nil
}

// sharesIn reports whether a participant is among an expense's shares.
func sharesIn(expense *model.Expense, participantID string) bool {
	for _, pf := range expense.PaidFor {
		if pf != nil && pf.ParticipantID == participantID {
			return true
		}
	}
	return false
}

// expensesSummary states plainly whose expenses these are, so a filtered answer
// is not mistaken for the whole group's.
func (t *tools) expensesSummary(r resolved, in listExpensesInput, out listExpensesOutput) string {
	whose := "in " + r.group.Alias
	switch {
	case in.Mine || strings.EqualFold(strings.TrimSpace(in.PaidBy), "me"):
		whose = fmt.Sprintf("paid by you (%s) in %s", r.group.ParticipantName, r.group.Alias)
	case in.PaidBy != "":
		whose = fmt.Sprintf("paid by %s in %s", in.PaidBy, r.group.Alias)
	case strings.EqualFold(strings.TrimSpace(in.Involving), "me"):
		whose = fmt.Sprintf("shared by you (%s) in %s", r.group.ParticipantName, r.group.Alias)
	case in.Involving != "":
		whose = fmt.Sprintf("shared by %s in %s", in.Involving, r.group.Alias)
	}

	summary := fmt.Sprintf("%d expense(s) %s.", len(out.Expenses), whose)
	if r.group.ParticipantName != "" && whose == "in "+r.group.Alias {
		// Unfiltered: name who "you" are so the whole group's list is not read
		// as the caller's own.
		summary += fmt.Sprintf(" These are the whole group's; you are %s.", r.group.ParticipantName)
	}
	return summary
}

func (t *tools) summarize(e *model.Expense, meID string) expenseSummary {
	summary := expenseSummary{
		ID:     e.ID,
		Title:  e.Title,
		Date:   e.ExpenseDate.Format(time.DateOnly),
		Amount: e.Amount.AsDecimal().StringFixed(2),
	}
	if e.PaidBy != nil {
		summary.PaidBy = e.PaidBy.Name
	}
	summary.PaidByMe = meID != "" && e.PaidByID == meID
	if e.Category != nil {
		summary.Category = e.Category.Name
	}
	return summary
}

// ---------------------------------------------------------------------------
// get_expense

type getExpenseInput struct {
	groupArg
	ExpenseID string `json:"expense_id" jsonschema:"The expense ID from list_expenses"`
}

type expenseShare struct {
	Participant string `json:"participant"`
	IsYou       bool   `json:"is_you,omitempty"`
	Shares      uint   `json:"shares"`
}

type getExpenseOutput struct {
	expenseSummary
	Group           string         `json:"group"`
	Currency        string         `json:"currency"`
	SplitMode       string         `json:"split_mode"`
	PaidFor         []expenseShare `json:"paid_for"`
	Notes           string         `json:"notes,omitempty"`
	IsReimbursement bool           `json:"is_reimbursement,omitempty"`
}

func (t *tools) getExpense(ctx context.Context, req *mcp.CallToolRequest, in getExpenseInput) (*mcp.CallToolResult, getExpenseOutput, error) {
	sub, err := t.userFromRequest(req)
	if err != nil {
		return toolError[getExpenseOutput](err)
	}
	r, err := t.resolve(ctx, sub, in.Group)
	if err != nil {
		return toolError[getExpenseOutput](err)
	}

	expense, err := t.deps.Clients.GetExpense(ctx, r.baseURL(), r.spliitID(), in.ExpenseID)
	if err != nil {
		return toolError[getExpenseOutput](err)
	}

	out := getExpenseOutput{
		expenseSummary:  t.summarize(expense, r.group.ParticipantID),
		Group:           r.group.Alias,
		Currency:        r.group.Currency,
		SplitMode:       string(expense.SplitMode),
		IsReimbursement: expense.IsReimbursement,
	}
	if expense.Notes != nil {
		out.Notes = *expense.Notes
	}
	for _, pf := range expense.PaidFor {
		if pf == nil {
			continue
		}
		share := expenseShare{Shares: pf.Shares, IsYou: pf.ParticipantID == r.group.ParticipantID}
		if pf.Participant != nil {
			share.Participant = pf.Participant.Name
		} else {
			share.Participant = pf.ParticipantID
		}
		out.PaidFor = append(out.PaidFor, share)
	}
	return toolResult(fmt.Sprintf("%s — %s %s", expense.Title, out.Amount, r.group.Currency), out)
}

// ---------------------------------------------------------------------------
// create_expense / update_expense

type expenseWriteInput struct {
	groupArg
	Title string `json:"title" jsonschema:"What the expense was for"`
	// Amount is a decimal string rather than a float so that money never passes
	// through binary floating point.
	Amount     string   `json:"amount" jsonschema:"Total amount as a decimal number in the group's currency, e.g. 12.50"`
	Date       string   `json:"date,omitempty" jsonschema:"Date in YYYY-MM-DD form (defaults to today)"`
	PaidBy     string   `json:"paid_by,omitempty" jsonschema:"Participant name who paid (defaults to you)"`
	PaidFor    []string `json:"paid_for,omitempty" jsonschema:"Participant names sharing the cost (defaults to everyone)"`
	CategoryID int      `json:"category_id,omitempty" jsonschema:"Category ID from list_categories"`
	Notes      string   `json:"notes,omitempty" jsonschema:"Free-text notes"`
	// IsReimbursement marks a payment settling an existing debt rather than a
	// new shared cost.
	IsReimbursement bool `json:"is_reimbursement,omitempty" jsonschema:"True if this is a repayment settling a debt"`

	// keepSplit, when set, is the existing expense whose split (participants,
	// shares and mode) must be sent back unchanged. update_expense sets it when
	// the caller did not pass paid_for. Unexported, so it never reaches the
	// model's schema.
	keepSplit *model.Expense
}

type createExpenseOutput struct {
	ExpenseID string   `json:"expense_id"`
	Group     string   `json:"group"`
	Title     string   `json:"title"`
	Amount    string   `json:"amount"`
	PaidBy    string   `json:"paid_by"`
	PaidFor   []string `json:"paid_for"`
}

func (t *tools) createExpense(ctx context.Context, req *mcp.CallToolRequest, in expenseWriteInput) (*mcp.CallToolResult, createExpenseOutput, error) {
	sub, err := t.userFromRequest(req)
	if err != nil {
		return toolError[createExpenseOutput](err)
	}
	r, err := t.resolve(ctx, sub, in.Group)
	if err != nil {
		return toolError[createExpenseOutput](err)
	}

	group, err := t.deps.Clients.GetGroup(ctx, r.baseURL(), r.spliitID())
	if err != nil {
		return toolError[createExpenseOutput](err)
	}

	form, names, err := t.buildExpenseForm(in, group, r)
	if err != nil {
		return toolError[createExpenseOutput](err)
	}

	me := participantOrNil(r.group.ParticipantID)
	id, err := t.deps.Clients.CreateExpense(ctx, r.baseURL(), r.spliitID(), form, me)
	if err != nil {
		return toolError[createExpenseOutput](err)
	}

	out := createExpenseOutput{
		ExpenseID: id, Group: r.group.Alias, Title: form.Title,
		Amount: form.Amount.AsDecimal().StringFixed(2),
		PaidBy: names.paidBy, PaidFor: names.paidFor,
	}
	return toolResult(fmt.Sprintf("Recorded %q for %s %s, paid by %s, split between %s.",
		form.Title, out.Amount, r.group.Currency, names.paidBy,
		strings.Join(names.paidFor, ", ")), out)
}

// updateExpenseInput deliberately does not embed expenseWriteInput: every field
// must be optional here, since an update names only what changes and the rest is
// carried over from the current expense.
type updateExpenseInput struct {
	groupArg
	ExpenseID string `json:"expense_id" jsonschema:"The expense ID to update"`

	Title           string   `json:"title,omitempty" jsonschema:"New title (unchanged if omitted)"`
	Amount          string   `json:"amount,omitempty" jsonschema:"New total as a decimal number (unchanged if omitted)"`
	Date            string   `json:"date,omitempty" jsonschema:"New date in YYYY-MM-DD form (unchanged if omitted)"`
	PaidBy          string   `json:"paid_by,omitempty" jsonschema:"New payer's participant name (unchanged if omitted)"`
	PaidFor         []string `json:"paid_for,omitempty" jsonschema:"New sharers' participant names (unchanged if omitted)"`
	CategoryID      int      `json:"category_id,omitempty" jsonschema:"New category ID (unchanged if omitted)"`
	Notes           string   `json:"notes,omitempty" jsonschema:"New notes (unchanged if omitted)"`
	IsReimbursement *bool    `json:"is_reimbursement,omitempty" jsonschema:"Mark as a repayment settling a debt (unchanged if omitted)"`
}

// asWriteInput reshapes an update into the common form-building input.
func (in updateExpenseInput) asWriteInput() expenseWriteInput {
	return expenseWriteInput{
		groupArg:        in.groupArg,
		Title:           in.Title,
		Amount:          in.Amount,
		Date:            in.Date,
		PaidBy:          in.PaidBy,
		PaidFor:         in.PaidFor,
		CategoryID:      in.CategoryID,
		Notes:           in.Notes,
		IsReimbursement: in.IsReimbursement != nil && *in.IsReimbursement,
	}
}

func (t *tools) updateExpense(ctx context.Context, req *mcp.CallToolRequest, in updateExpenseInput) (*mcp.CallToolResult, createExpenseOutput, error) {
	sub, err := t.userFromRequest(req)
	if err != nil {
		return toolError[createExpenseOutput](err)
	}
	r, err := t.resolve(ctx, sub, in.Group)
	if err != nil {
		return toolError[createExpenseOutput](err)
	}

	group, err := t.deps.Clients.GetGroup(ctx, r.baseURL(), r.spliitID())
	if err != nil {
		return toolError[createExpenseOutput](err)
	}

	// Spliit replaces the whole expense on update, so start from the current
	// values and let the caller override only what they named.
	current, err := t.deps.Clients.GetExpense(ctx, r.baseURL(), r.spliitID(), in.ExpenseID)
	if err != nil {
		return toolError[createExpenseOutput](err)
	}
	write := in.asWriteInput()
	applyExpenseDefaults(&write, current, in.IsReimbursement == nil)

	form, names, err := t.buildExpenseForm(write, group, r)
	if err != nil {
		return toolError[createExpenseOutput](err)
	}

	me := participantOrNil(r.group.ParticipantID)
	if err := t.deps.Clients.UpdateExpense(ctx, r.baseURL(), r.spliitID(), in.ExpenseID, form, me); err != nil {
		return toolError[createExpenseOutput](err)
	}

	out := createExpenseOutput{
		ExpenseID: in.ExpenseID, Group: r.group.Alias, Title: form.Title,
		Amount: form.Amount.AsDecimal().StringFixed(2),
		PaidBy: names.paidBy, PaidFor: names.paidFor,
	}
	split := "split unchanged"
	if write.keepSplit == nil {
		split = "now split evenly between " + strings.Join(names.paidFor, ", ")
	}
	return toolResult(fmt.Sprintf("Updated %q to %s %s, paid by %s, %s.",
		form.Title, out.Amount, r.group.Currency, names.paidBy, split), out)
}

// applyExpenseDefaults fills unset input fields from the existing expense, so a
// partial update does not silently blank the rest of it.
//
// The split is the subtle part. Spliit's expense payload carries participant
// IDs, shares and a split mode, and the nested participant objects are not
// guaranteed to be present; re-deriving names from them and sending the result
// through the even-split path would flatten by-share and by-amount expenses,
// and an empty name list would fall back to "everyone". So when the caller did
// not pass paid_for, the existing split is carried over verbatim instead.
func applyExpenseDefaults(in *expenseWriteInput, current *model.Expense, keepReimbursement bool) {
	if in.Title == "" {
		in.Title = current.Title
	}
	if in.Amount == "" {
		in.Amount = current.Amount.AsDecimal().StringFixed(2)
	}
	if in.Date == "" {
		in.Date = current.ExpenseDate.Format(time.DateOnly)
	}
	if in.PaidBy == "" && current.PaidBy != nil {
		in.PaidBy = current.PaidBy.Name
	}
	if len(in.PaidFor) == 0 {
		in.keepSplit = current
	}
	if keepReimbursement {
		in.IsReimbursement = current.IsReimbursement
	}
	if in.CategoryID == 0 {
		in.CategoryID = current.CategoryID
	}
	if in.Notes == "" && current.Notes != nil {
		in.Notes = *current.Notes
	}
}

// resolvedNames records the human-readable participants an expense ended up
// referring to, for the confirmation message.
type resolvedNames struct {
	paidBy  string
	paidFor []string
}

// buildExpenseForm turns tool input into a Spliit expense form, resolving
// participant names to IDs and defaulting the payer to the caller.
func (t *tools) buildExpenseForm(in expenseWriteInput, group *model.Group, r resolved) (shape.ModifyExpenseForm, resolvedNames, error) {
	var names resolvedNames

	amount, err := decimal.NewFromString(strings.TrimSpace(in.Amount))
	if err != nil {
		return shape.ModifyExpenseForm{}, names,
			fmt.Errorf("amount %q is not a decimal number", in.Amount)
	}
	if amount.IsZero() {
		return shape.ModifyExpenseForm{}, names, fmt.Errorf("amount must not be zero")
	}

	if strings.TrimSpace(in.Title) == "" {
		return shape.ModifyExpenseForm{}, names, fmt.Errorf("title is required")
	}

	date := time.Now().UTC().Truncate(24 * time.Hour)
	if in.Date != "" {
		parsed, err := time.Parse(time.DateOnly, strings.TrimSpace(in.Date))
		if err != nil {
			return shape.ModifyExpenseForm{}, names,
				fmt.Errorf("date %q is not in YYYY-MM-DD form", in.Date)
		}
		date = parsed
	}

	// Who paid: an explicit name, otherwise the participant pinned as "you".
	var payerID string
	if strings.TrimSpace(in.PaidBy) != "" {
		p, err := findByName(group, in.PaidBy)
		if err != nil {
			return shape.ModifyExpenseForm{}, names, err
		}
		payerID, names.paidBy = p.ID, p.Name
	} else {
		payerID, err = r.me()
		if err != nil {
			return shape.ModifyExpenseForm{}, names, err
		}
		if p := spliit.FindParticipant(group, payerID); p != nil {
			names.paidBy = p.Name
		} else {
			// The pinned participant was removed in Spliit since it was set.
			return shape.ModifyExpenseForm{}, names, fmt.Errorf(
				"%w: set it again with set_active_participant, or in %s",
				spliit.ErrParticipantMissing, t.atConfigPage())
		}
	}

	// Who shares it: the existing split when updating without paid_for, else
	// named participants, else everyone.
	splitMode := model.SplitModeEvenly
	paidFor := make([]shape.ModifyExpenseFormPaidFor, 0, len(group.Participants))
	if in.keepSplit != nil {
		if in.keepSplit.SplitMode != "" {
			splitMode = in.keepSplit.SplitMode
		}
		for _, pf := range in.keepSplit.PaidFor {
			if pf == nil {
				continue
			}
			paidFor = append(paidFor, shape.ModifyExpenseFormPaidFor{Participant: pf.ParticipantID, Shares: int(pf.Shares)})
			if p := spliit.FindParticipant(group, pf.ParticipantID); p != nil {
				names.paidFor = append(names.paidFor, p.Name)
			} else {
				names.paidFor = append(names.paidFor, pf.ParticipantID)
			}
		}
	} else if len(in.PaidFor) > 0 {
		for _, name := range in.PaidFor {
			p, err := findByName(group, name)
			if err != nil {
				return shape.ModifyExpenseForm{}, names, err
			}
			paidFor = append(paidFor, shape.ModifyExpenseFormPaidFor{Participant: p.ID, Shares: 1})
			names.paidFor = append(names.paidFor, p.Name)
		}
	} else {
		for _, p := range group.Participants {
			if p == nil {
				continue
			}
			paidFor = append(paidFor, shape.ModifyExpenseFormPaidFor{Participant: p.ID, Shares: 1})
			names.paidFor = append(names.paidFor, p.Name)
		}
	}
	if len(paidFor) == 0 {
		return shape.ModifyExpenseForm{}, names, fmt.Errorf("an expense must be shared by at least one participant")
	}

	form := shape.ModifyExpenseForm{
		ExpenseDate:     date,
		Title:           strings.TrimSpace(in.Title),
		CategoryID:      in.CategoryID,
		Amount:          spliit.ToAmount(amount),
		PaidBy:          payerID,
		PaidFor:         paidFor,
		SplitMode:       splitMode,
		IsReimbursement: in.IsReimbursement,
		RecurrenceRule:  model.RecurrenceRuleNone,
	}
	if strings.TrimSpace(in.Notes) != "" {
		notes := in.Notes
		form.Notes = &notes
	}
	return form, names, nil
}

// findByName resolves a participant by name, case-insensitively. An unknown
// name is reported with the valid options so the model can retry immediately.
func findByName(group *model.Group, name string) (*model.Participant, error) {
	want := strings.TrimSpace(name)
	for _, p := range group.Participants {
		if p != nil && strings.EqualFold(strings.TrimSpace(p.Name), want) {
			return p, nil
		}
	}
	return nil, fmt.Errorf("no participant named %q in this group; participants are: %s",
		name, strings.Join(spliit.ParticipantNames(group), ", "))
}

func participantOrNil(id string) *string {
	if id == "" {
		return nil
	}
	return &id
}

// ---------------------------------------------------------------------------
// delete_expense

type deleteExpenseInput struct {
	groupArg
	ExpenseID string `json:"expense_id" jsonschema:"The expense ID to delete"`
}

type deleteExpenseOutput struct {
	Deleted   bool   `json:"deleted"`
	ExpenseID string `json:"expense_id"`
}

func (t *tools) deleteExpense(ctx context.Context, req *mcp.CallToolRequest, in deleteExpenseInput) (*mcp.CallToolResult, deleteExpenseOutput, error) {
	sub, err := t.userFromRequest(req)
	if err != nil {
		return toolError[deleteExpenseOutput](err)
	}
	r, err := t.resolve(ctx, sub, in.Group)
	if err != nil {
		return toolError[deleteExpenseOutput](err)
	}

	// Read it first so the confirmation can name what was removed; a bare
	// "deleted" gives the user nothing to check against.
	expense, err := t.deps.Clients.GetExpense(ctx, r.baseURL(), r.spliitID(), in.ExpenseID)
	if err != nil {
		return toolError[deleteExpenseOutput](err)
	}

	me := participantOrNil(r.group.ParticipantID)
	if err := t.deps.Clients.DeleteExpense(ctx, r.baseURL(), r.spliitID(), in.ExpenseID, me); err != nil {
		return toolError[deleteExpenseOutput](err)
	}
	return toolResult(
		fmt.Sprintf("Deleted %q (%s %s) from %s.",
			expense.Title, expense.Amount.AsDecimal().StringFixed(2),
			r.group.Currency, r.group.Alias),
		deleteExpenseOutput{Deleted: true, ExpenseID: in.ExpenseID})
}

// ---------------------------------------------------------------------------
// get_stats

type getStatsInput struct {
	groupArg
}

type getStatsOutput struct {
	Group      string `json:"group"`
	Currency   string `json:"currency"`
	GroupTotal string `json:"group_total"`
	YourTotal  string `json:"your_total,omitempty"`
	YourShare  string `json:"your_share,omitempty"`
}

func (t *tools) getStats(ctx context.Context, req *mcp.CallToolRequest, in getStatsInput) (*mcp.CallToolResult, getStatsOutput, error) {
	sub, err := t.userFromRequest(req)
	if err != nil {
		return toolError[getStatsOutput](err)
	}
	r, err := t.resolve(ctx, sub, in.Group)
	if err != nil {
		return toolError[getStatsOutput](err)
	}

	stats, err := t.deps.Clients.GetStats(ctx, r.baseURL(), r.spliitID(),
		participantOrNil(r.group.ParticipantID))
	if err != nil {
		return toolError[getStatsOutput](err)
	}

	out := getStatsOutput{
		Group: r.group.Alias, Currency: r.group.Currency,
		GroupTotal: stats.TotalGroupSpendings.AsDecimal().StringFixed(2),
	}
	if stats.TotalParticipantSpendings != nil {
		out.YourTotal = stats.TotalParticipantSpendings.AsDecimal().StringFixed(2)
	}
	if stats.TotalParticipantShare != nil {
		out.YourShare = stats.TotalParticipantShare.StringFixed(2)
	}
	return toolResult(fmt.Sprintf("%s has spent %s %s in total.",
		r.group.Alias, out.GroupTotal, r.group.Currency), out)
}

// ---------------------------------------------------------------------------
// list_activities

type listActivitiesInput struct {
	groupArg
	Limit uint `json:"limit,omitempty" jsonschema:"Maximum entries to return (default 20, max 100)"`
}

type activityEntry struct {
	Type         string `json:"type"`
	At           string `json:"at"`
	Participant  string `json:"participant,omitempty"`
	ExpenseID    string `json:"expense_id,omitempty"`
	ExpenseTitle string `json:"expense_title,omitempty"`
}

type listActivitiesOutput struct {
	Group      string          `json:"group"`
	Activities []activityEntry `json:"activities"`
	HasMore    bool            `json:"has_more"`
}

func (t *tools) listActivities(ctx context.Context, req *mcp.CallToolRequest, in listActivitiesInput) (*mcp.CallToolResult, listActivitiesOutput, error) {
	sub, err := t.userFromRequest(req)
	if err != nil {
		return toolError[listActivitiesOutput](err)
	}
	r, err := t.resolve(ctx, sub, in.Group)
	if err != nil {
		return toolError[listActivitiesOutput](err)
	}

	limit := in.Limit
	if limit == 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	page, err := t.deps.Clients.ListActivities(ctx, r.baseURL(), r.spliitID(), &limit, nil)
	if err != nil {
		return toolError[listActivitiesOutput](err)
	}

	out := listActivitiesOutput{
		Group: r.group.Alias, HasMore: page.HasMore,
		Activities: make([]activityEntry, 0, len(page.Activities)),
	}
	for _, a := range page.Activities {
		entry := activityEntry{
			Type: string(a.ActivityType),
			At:   a.Time.Format(time.RFC3339),
		}
		if a.ExpenseID != nil {
			entry.ExpenseID = *a.ExpenseID
		}
		if a.Data != nil {
			entry.ExpenseTitle = *a.Data
		}
		if a.ParticipantID != nil {
			entry.Participant = *a.ParticipantID
		}
		out.Activities = append(out.Activities, entry)
	}
	return toolResult(fmt.Sprintf("%d activity entries for %s.", len(out.Activities), r.group.Alias), out)
}

// ---------------------------------------------------------------------------
// list_categories

type listCategoriesInput struct {
	groupArg
}

type categoryEntry struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Grouping string `json:"grouping,omitempty"`
}

type listCategoriesOutput struct {
	Categories []categoryEntry `json:"categories"`
}

func (t *tools) listCategories(ctx context.Context, req *mcp.CallToolRequest, in listCategoriesInput) (*mcp.CallToolResult, listCategoriesOutput, error) {
	sub, err := t.userFromRequest(req)
	if err != nil {
		return toolError[listCategoriesOutput](err)
	}
	r, err := t.resolve(ctx, sub, in.Group)
	if err != nil {
		return toolError[listCategoriesOutput](err)
	}

	categories, err := t.deps.Clients.ListCategories(ctx, r.baseURL())
	if err != nil {
		return toolError[listCategoriesOutput](err)
	}

	out := listCategoriesOutput{Categories: make([]categoryEntry, 0, len(categories))}
	for _, c := range categories {
		out.Categories = append(out.Categories, categoryEntry{
			ID: c.ID, Name: c.Name, Grouping: c.Grouping,
		})
	}
	return toolResult(fmt.Sprintf("%d categories.", len(out.Categories)), out)
}

// ---------------------------------------------------------------------------
// create_group

type createGroupInput struct {
	Name string `json:"name" jsonschema:"Name of the new group"`
	// Participants excludes you; you are added automatically.
	Participants []string `json:"participants" jsonschema:"Names of the other participants; you are added automatically"`
	Currency     string   `json:"currency,omitempty" jsonschema:"Currency symbol or code, e.g. SEK (default USD)"`
	Alias        string   `json:"alias,omitempty" jsonschema:"Short alias for later tool calls (defaults to the group name)"`
	// Creating has no group link to derive an instance from, so this is the one
	// place a URL must be named to use a non-default instance.
	ServerURL string `json:"server_url,omitempty" jsonschema:"tRPC base URL of the Spliit instance to create it on (defaults to this server's default instance)"`
}

type createGroupOutput struct {
	Alias         string   `json:"alias"`
	Name          string   `json:"name"`
	SpliitGroupID string   `json:"spliit_group_id"`
	Server        string   `json:"server"`
	URL           string   `json:"url,omitempty"`
	YouAre        string   `json:"you_are"`
	Participants  []string `json:"participants"`
}

func (t *tools) createGroup(ctx context.Context, req *mcp.CallToolRequest, in createGroupInput) (*mcp.CallToolResult, createGroupOutput, error) {
	sub, err := t.userFromRequest(req)
	if err != nil {
		return toolError[createGroupOutput](err)
	}

	user, err := t.deps.Store.GetUser(ctx, sub)
	if err != nil {
		return toolError[createGroupOutput](err)
	}
	if strings.TrimSpace(in.Name) == "" {
		return toolError[createGroupOutput](fmt.Errorf("name is required"))
	}

	baseURL := t.baseURLFor(in.ServerURL, "")

	// The caller is always a participant; that is what makes the group usable
	// through the other tools without a trip to the config page.
	participants := []shape.ModifyGroupParticipant{{Name: user.DisplayName}}
	added := []string{user.DisplayName}
	for _, name := range in.Participants {
		name = strings.TrimSpace(name)
		if name == "" || strings.EqualFold(name, user.DisplayName) {
			continue
		}
		participants = append(participants, shape.ModifyGroupParticipant{Name: name})
		added = append(added, name)
	}

	currency := strings.TrimSpace(in.Currency)
	if currency == "" {
		currency = "USD"
	}

	groupID, err := t.deps.Clients.CreateGroup(ctx, baseURL, shape.ModifyGroupForm{
		Name:         strings.TrimSpace(in.Name),
		Currency:     currency,
		Participants: participants,
	})
	if err != nil {
		return toolError[createGroupOutput](err)
	}

	// Read it back to learn the generated participant IDs.
	group, err := t.deps.Clients.GetGroup(ctx, baseURL, groupID)
	if err != nil {
		return toolError[createGroupOutput](err)
	}

	alias := strings.TrimSpace(in.Alias)
	if alias == "" {
		alias = group.Name
	}

	stored, err := t.registerGroup(ctx, sub, baseURL, group, alias, user.DisplayName)
	if err != nil {
		return toolError[createGroupOutput](err)
	}

	out := createGroupOutput{
		Alias: stored.Alias, Name: group.Name, SpliitGroupID: group.ID,
		Server: spliit.HostOf(baseURL), URL: spliit.WebURL(baseURL, group.ID),
		YouAre:       stored.ParticipantName,
		Participants: spliit.ParticipantNames(group),
	}
	return toolResult(fmt.Sprintf("Created %q with %s. Use alias %q in other tools.\n%s",
		group.Name, strings.Join(added, ", "), stored.Alias, out.URL), out)
}

// registerGroup stores a freshly created group against the caller, pinning the
// participant whose name matches their display name.
//
// If the alias is taken, a numeric suffix is tried rather than failing: the
// group already exists in Spliit at this point, and refusing to record it would
// strand a group ID the user has no other way to recover.
func (t *tools) registerGroup(
	ctx context.Context, sub, baseURL string,
	group *model.Group, alias, displayName string,
) (*store.Group, error) {
	row := &store.Group{
		UserSub:       sub,
		BaseURL:       baseURL,
		SpliitGroupID: group.ID,
		Alias:         alias,
		GroupName:     group.Name,
		Currency:      group.Currency,
	}
	for _, p := range group.Participants {
		if p != nil && strings.EqualFold(strings.TrimSpace(p.Name), strings.TrimSpace(displayName)) {
			row.ParticipantID, row.ParticipantName = p.ID, p.Name
			break
		}
	}

	for attempt := 0; attempt < 10; attempt++ {
		candidate := *row
		if attempt > 0 {
			candidate.Alias = fmt.Sprintf("%s-%d", alias, attempt+1)
		}
		stored, err := t.deps.Store.CreateGroup(ctx, &candidate)
		if err == nil {
			return stored, nil
		}
		if !errors.Is(err, store.ErrConflict) {
			return nil, err
		}
	}
	return nil, fmt.Errorf(
		"created group %q (id %s) but could not register an alias for it; add it manually in the config page",
		group.Name, group.ID)
}

// ---------------------------------------------------------------------------
// inspect_group

type inspectGroupInput struct {
	GroupID string `json:"group_id" jsonschema:"A Spliit group ID, or the full group URL"`
	// ServerURL is only needed for a bare ID on a non-default instance; a full
	// group URL already says which instance hosts it.
	ServerURL string `json:"server_url,omitempty" jsonschema:"tRPC base URL of the Spliit instance, if the group is given as a bare ID on a non-default instance"`
}

type inspectGroupOutput struct {
	GroupID      string   `json:"group_id"`
	Name         string   `json:"name"`
	Currency     string   `json:"currency"`
	Server       string   `json:"server"`
	ServerURL    string   `json:"server_url"`
	URL          string   `json:"url,omitempty"`
	Participants []string `json:"participants"`
	// AlreadyJoined reports whether this group is already available to you.
	AlreadyJoined bool   `json:"already_joined"`
	JoinedAs      string `json:"joined_as,omitempty"`
	// SuggestedYou is the participant matching your name, when exactly one does.
	SuggestedYou string `json:"suggested_you,omitempty"`
}

// inspectGroup reads a group without joining it, so the participant list is
// known before join_group has to name one.
func (t *tools) inspectGroup(ctx context.Context, req *mcp.CallToolRequest, in inspectGroupInput) (*mcp.CallToolResult, inspectGroupOutput, error) {
	sub, err := t.userFromRequest(req)
	if err != nil {
		return toolError[inspectGroupOutput](err)
	}
	user, err := t.deps.Store.GetUser(ctx, sub)
	if err != nil {
		return toolError[inspectGroupOutput](err)
	}

	baseURL := t.baseURLFor(in.ServerURL, in.GroupID)

	groupID := spliit.ExtractGroupID(strings.TrimSpace(in.GroupID))
	if groupID == "" {
		return toolError[inspectGroupOutput](fmt.Errorf("group_id is required"))
	}

	group, err := t.deps.Clients.GetGroup(ctx, baseURL, groupID)
	if err != nil {
		return toolError[inspectGroupOutput](err)
	}

	out := inspectGroupOutput{
		GroupID: group.ID, Name: group.Name, Currency: group.Currency,
		Server: spliit.HostOf(baseURL), ServerURL: baseURL,
		URL:          spliit.WebURL(baseURL, group.ID),
		Participants: spliit.ParticipantNames(group),
	}

	// Surface an existing membership so the model does not try to join twice.
	if existing, err := t.deps.Store.ResolveGroup(ctx, sub, group.ID); err == nil {
		out.AlreadyJoined, out.JoinedAs = true, existing.ParticipantName
	}

	if p := spliit.FindParticipantByName(group, user.DisplayName); p != nil {
		out.SuggestedYou = p.Name
	}

	summary := fmt.Sprintf("%s — participants: %s.",
		group.Name, strings.Join(out.Participants, ", "))
	if out.AlreadyJoined {
		summary += " You have already joined this group."
	}
	return toolResult(summary, out)
}

// ---------------------------------------------------------------------------
// join_group

type joinGroupInput struct {
	GroupID string `json:"group_id" jsonschema:"A Spliit group ID, or the full group URL"`
	// You is required: a group joined without knowing which participant the
	// caller is cannot be written to, so it is asked for up front.
	You   string `json:"you" jsonschema:"Which participant in the group is you (use inspect_group to see the names)"`
	Alias string `json:"alias,omitempty" jsonschema:"Short alias for later tool calls (defaults to the group name)"`
	// See inspectGroupInput.ServerURL.
	ServerURL string `json:"server_url,omitempty" jsonschema:"tRPC base URL of the Spliit instance, if the group is given as a bare ID on a non-default instance"`
}

type joinGroupOutput struct {
	Alias         string `json:"alias"`
	Name          string `json:"name"`
	SpliitGroupID string `json:"spliit_group_id"`
	Server        string `json:"server"`
	URL           string `json:"url,omitempty"`
	YouAre        string `json:"you_are"`
}

func (t *tools) joinGroup(ctx context.Context, req *mcp.CallToolRequest, in joinGroupInput) (*mcp.CallToolResult, joinGroupOutput, error) {
	sub, err := t.userFromRequest(req)
	if err != nil {
		return toolError[joinGroupOutput](err)
	}

	baseURL := t.baseURLFor(in.ServerURL, in.GroupID)

	groupID := spliit.ExtractGroupID(strings.TrimSpace(in.GroupID))
	if groupID == "" {
		return toolError[joinGroupOutput](fmt.Errorf("group_id is required"))
	}
	if strings.TrimSpace(in.You) == "" {
		return toolError[joinGroupOutput](fmt.Errorf(
			"you is required: name which participant you are, or call inspect_group first to see the options"))
	}

	group, err := t.deps.Clients.GetGroup(ctx, baseURL, groupID)
	if err != nil {
		return toolError[joinGroupOutput](err)
	}

	participant := spliit.FindParticipantByName(group, in.You)
	if participant == nil {
		return toolError[joinGroupOutput](fmt.Errorf(
			"no participant named %q in %q; participants are: %s",
			in.You, group.Name, strings.Join(spliit.ParticipantNames(group), ", ")))
	}

	alias := strings.TrimSpace(in.Alias)
	if alias == "" {
		alias = group.Name
	}

	stored, err := t.deps.Store.CreateGroup(ctx, &store.Group{
		UserSub: sub, BaseURL: baseURL, SpliitGroupID: group.ID,
		Alias: alias, GroupName: group.Name, Currency: group.Currency,
		ParticipantID: participant.ID, ParticipantName: participant.Name,
	})
	if errors.Is(err, store.ErrConflict) {
		return toolError[joinGroupOutput](fmt.Errorf(
			"you have already joined that group, or your alias %q is taken", alias))
	}
	if err != nil {
		return toolError[joinGroupOutput](err)
	}

	return toolResult(
		fmt.Sprintf("Joined %q as %s. Use alias %q in other tools.",
			group.Name, participant.Name, stored.Alias),
		joinGroupOutput{
			Alias: stored.Alias, Name: group.Name, SpliitGroupID: group.ID,
			Server: spliit.HostOf(baseURL), URL: spliit.WebURL(baseURL, group.ID),
			YouAre: participant.Name,
		})
}

// ---------------------------------------------------------------------------
// leave_group

type leaveGroupInput struct {
	groupArg
}

type leaveGroupOutput struct {
	Left  bool   `json:"left"`
	Alias string `json:"alias"`
}

// leaveGroup unlinks a group locally. Nothing is deleted in Spliit, and the
// group can be joined again with the same ID.
func (t *tools) leaveGroup(ctx context.Context, req *mcp.CallToolRequest, in leaveGroupInput) (*mcp.CallToolResult, leaveGroupOutput, error) {
	sub, err := t.userFromRequest(req)
	if err != nil {
		return toolError[leaveGroupOutput](err)
	}
	r, err := t.resolve(ctx, sub, in.Group)
	if err != nil {
		return toolError[leaveGroupOutput](err)
	}

	if err := t.deps.Store.DeleteGroup(ctx, sub, r.group.ID); err != nil {
		return toolError[leaveGroupOutput](err)
	}
	return toolResult(
		fmt.Sprintf("Removed %q from your available groups. "+
			"Nothing was deleted in Spliit; rejoin with group ID %s.",
			r.group.Alias, r.group.SpliitGroupID),
		leaveGroupOutput{Left: true, Alias: r.group.Alias})
}

// ---------------------------------------------------------------------------
// set_active_participant

type setActiveParticipantInput struct {
	groupArg
	You string `json:"you" jsonschema:"Which participant in the group is you"`
}

type setActiveParticipantOutput struct {
	Alias  string `json:"alias"`
	YouAre string `json:"you_are"`
}

// setActiveParticipant re-pins who "you" are in a group, which is the fix when
// a participant was removed and re-added in Spliit and the stored ID went stale.
func (t *tools) setActiveParticipant(ctx context.Context, req *mcp.CallToolRequest, in setActiveParticipantInput) (*mcp.CallToolResult, setActiveParticipantOutput, error) {
	sub, err := t.userFromRequest(req)
	if err != nil {
		return toolError[setActiveParticipantOutput](err)
	}
	r, err := t.resolve(ctx, sub, in.Group)
	if err != nil {
		return toolError[setActiveParticipantOutput](err)
	}

	group, err := t.deps.Clients.GetGroup(ctx, r.baseURL(), r.spliitID())
	if err != nil {
		return toolError[setActiveParticipantOutput](err)
	}

	participant := spliit.FindParticipantByName(group, in.You)
	if participant == nil {
		return toolError[setActiveParticipantOutput](fmt.Errorf(
			"no participant named %q in this group; participants are: %s",
			in.You, strings.Join(spliit.ParticipantNames(group), ", ")))
	}

	r.group.ParticipantID, r.group.ParticipantName = participant.ID, participant.Name
	r.group.GroupName, r.group.Currency = group.Name, group.Currency
	if err := t.deps.Store.UpdateGroup(ctx, r.group); err != nil {
		return toolError[setActiveParticipantOutput](err)
	}

	return toolResult(
		fmt.Sprintf("You are now %s in %q.", participant.Name, r.group.Alias),
		setActiveParticipantOutput{Alias: r.group.Alias, YouAre: participant.Name})
}

// ---------------------------------------------------------------------------
// update_group

type renameParticipant struct {
	From string `json:"from" jsonschema:"Current participant name"`
	To   string `json:"to" jsonschema:"New participant name"`
}

type updateGroupInput struct {
	groupArg
	Name     string `json:"name,omitempty" jsonschema:"New group name"`
	Currency string `json:"currency,omitempty" jsonschema:"New currency symbol or code, e.g. SEK"`
	// Participant edits are expressed as deltas; Spliit itself takes the full
	// list, so the handler merges these into what the group currently has.
	Add    []string            `json:"add_participants,omitempty" jsonschema:"Names of participants to add"`
	Remove []string            `json:"remove_participants,omitempty" jsonschema:"Names of participants to remove; only possible when they have no expenses"`
	Rename []renameParticipant `json:"rename_participants,omitempty" jsonschema:"Participants to rename, keeping their expenses"`
}

type updateGroupOutput struct {
	Alias        string   `json:"alias"`
	Name         string   `json:"name"`
	Currency     string   `json:"currency"`
	YouAre       string   `json:"you_are,omitempty"`
	Participants []string `json:"participants"`
}

// updateGroup edits a group in Spliit. Spliit replaces the participant list
// wholesale, keyed by ID: an entry with an ID is kept (and possibly renamed),
// an entry without one is created, and anything missing is deleted. Passing
// IDs through is what keeps renames from silently re-creating a participant
// and orphaning their expenses.
func (t *tools) updateGroup(ctx context.Context, req *mcp.CallToolRequest, in updateGroupInput) (*mcp.CallToolResult, updateGroupOutput, error) {
	sub, err := t.userFromRequest(req)
	if err != nil {
		return toolError[updateGroupOutput](err)
	}
	r, err := t.resolve(ctx, sub, in.Group)
	if err != nil {
		return toolError[updateGroupOutput](err)
	}
	if strings.TrimSpace(in.Name) == "" && strings.TrimSpace(in.Currency) == "" &&
		len(in.Add) == 0 && len(in.Remove) == 0 && len(in.Rename) == 0 {
		return toolError[updateGroupOutput](fmt.Errorf(
			"nothing to change: pass name, currency, add_participants, remove_participants or rename_participants"))
	}

	group, withExpenses, err := t.deps.Clients.GetGroupDetails(ctx, r.baseURL(), r.spliitID())
	if err != nil {
		return toolError[updateGroupOutput](err)
	}

	form, changes, err := buildGroupForm(in, group, withExpenses, r.group.ParticipantID)
	if err != nil {
		return toolError[updateGroupOutput](err)
	}

	me := participantOrNil(r.group.ParticipantID)
	if err := t.deps.Clients.UpdateGroup(ctx, r.baseURL(), r.spliitID(), form, me); err != nil {
		return toolError[updateGroupOutput](err)
	}

	// Read back rather than trusting the form: new participants have IDs now,
	// and the stored copy of the name and currency should follow Spliit.
	updated, err := t.deps.Clients.GetGroup(ctx, r.baseURL(), r.spliitID())
	if err != nil {
		return toolError[updateGroupOutput](err)
	}
	r.group.GroupName, r.group.Currency = updated.Name, updated.Currency
	if p := spliit.FindParticipant(updated, r.group.ParticipantID); p != nil {
		r.group.ParticipantName = p.Name
	}
	if err := t.deps.Store.UpdateGroup(ctx, r.group); err != nil {
		return toolError[updateGroupOutput](err)
	}

	out := updateGroupOutput{
		Alias: r.group.Alias, Name: updated.Name, Currency: updated.Currency,
		YouAre: r.group.ParticipantName, Participants: spliit.ParticipantNames(updated),
	}
	return toolResult(fmt.Sprintf("Updated %q: %s. Participants are now %s.",
		r.group.Alias, strings.Join(changes, "; "), strings.Join(out.Participants, ", ")), out)
}

// buildGroupForm merges the requested edits into the group's current state and
// returns the full form Spliit expects, plus a human summary of what changed.
// Every rejection names the valid options so the model can retry.
func buildGroupForm(in updateGroupInput, group *model.Group, withExpenses []string, meID string) (shape.ModifyGroupForm, []string, error) {
	form := shape.ModifyGroupForm{
		Name:         group.Name,
		Information:  group.Information,
		Currency:     group.Currency,
		CurrencyCode: group.CurrencyCode,
	}
	var changes []string

	if name := strings.TrimSpace(in.Name); name != "" && name != group.Name {
		form.Name = name
		changes = append(changes, fmt.Sprintf("renamed to %q", name))
	}
	if cur := strings.TrimSpace(in.Currency); cur != "" && cur != group.Currency {
		form.Currency = cur
		changes = append(changes, "currency "+cur)
	}

	hasExpenses := make(map[string]bool, len(withExpenses))
	for _, id := range withExpenses {
		hasExpenses[id] = true
	}

	// Start from everyone currently in the group, then apply renames and
	// removals against that list by ID.
	type entry struct {
		id   string
		name string
	}
	current := make([]entry, 0, len(group.Participants))
	for _, p := range group.Participants {
		if p != nil {
			current = append(current, entry{id: p.ID, name: p.Name})
		}
	}

	for _, rn := range in.Rename {
		p, err := findByName(group, rn.From)
		if err != nil {
			return form, nil, err
		}
		to := strings.TrimSpace(rn.To)
		if to == "" {
			return form, nil, fmt.Errorf("new name for %q is empty", p.Name)
		}
		for i := range current {
			if current[i].id == p.ID {
				current[i].name = to
			}
		}
		changes = append(changes, fmt.Sprintf("%s renamed to %s", p.Name, to))
	}

	removed := map[string]bool{}
	for _, name := range in.Remove {
		p, err := findByName(group, name)
		if err != nil {
			return form, nil, err
		}
		if p.ID == meID {
			return form, nil, fmt.Errorf(
				"%s is you in this group; use set_active_participant to become somebody else before removing them", p.Name)
		}
		if hasExpenses[p.ID] {
			return form, nil, fmt.Errorf(
				"%s has expenses and cannot be removed; reassign or delete those expenses first", p.Name)
		}
		removed[p.ID] = true
		changes = append(changes, "removed "+p.Name)
	}

	for _, e := range current {
		if removed[e.id] {
			continue
		}
		id := e.id
		form.Participants = append(form.Participants, shape.ModifyGroupParticipant{ID: &id, Name: e.name})
	}

	for _, name := range in.Add {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		for _, p := range form.Participants {
			if strings.EqualFold(strings.TrimSpace(p.Name), name) {
				return form, nil, fmt.Errorf("a participant named %q is already in the group", name)
			}
		}
		form.Participants = append(form.Participants, shape.ModifyGroupParticipant{Name: name})
		changes = append(changes, "added "+name)
	}

	if len(form.Participants) == 0 {
		return form, nil, fmt.Errorf("a group must keep at least one participant")
	}
	if len(changes) == 0 {
		return form, nil, fmt.Errorf("nothing to change: the group already matches the request")
	}
	return form, changes, nil
}

// ---------------------------------------------------------------------------
// get_server_info

type getServerInfoInput struct{}

type getServerInfoOutput struct {
	// MCPURL is what another client should be pointed at.
	MCPURL string `json:"mcp_url"`
	// ConfigURL is the web UI for managing groups and identity.
	ConfigURL string `json:"config_url"`
	Issuer    string `json:"oidc_issuer"`
	// MCPClientID is set when a client was pre-registered because the provider
	// does not allow dynamic client registration.
	MCPClientID string `json:"mcp_client_id,omitempty"`
	// ClaudeCommand is ready to paste on another machine.
	ClaudeCommand string `json:"claude_command"`
	Version       string `json:"version"`
}

// getServerInfo reports how to reach this server, so the answer to "how do I
// add this somewhere else" does not require reading the config page.
func (t *tools) getServerInfo(_ context.Context, req *mcp.CallToolRequest, _ getServerInfoInput) (*mcp.CallToolResult, getServerInfoOutput, error) {
	// Still identity-gated: these URLs describe a server whose whole purpose is
	// guarding group IDs, so they are not handed to unauthenticated callers.
	if _, err := t.userFromRequest(req); err != nil {
		return toolError[getServerInfoOutput](err)
	}

	out := getServerInfoOutput{
		MCPURL:    t.mcpURL(),
		ConfigURL: t.configURL(),
		Version:   t.deps.Version,
	}
	if t.deps.Config != nil {
		out.Issuer = t.deps.Config.OIDC.Issuer
		out.MCPClientID = t.deps.Config.OIDC.MCPClientID
	}

	command := "claude mcp add --transport http spliit " + out.MCPURL
	if out.MCPClientID != "" {
		// Without dynamic registration the client must be named, and its
		// callback port pinned so the redirect URI can be registered upfront.
		command += " --client-id " + out.MCPClientID + " --callback-port 45454"
	}
	out.ClaudeCommand = command

	return toolResult(fmt.Sprintf(
		"MCP endpoint: %s\nConfig page: %s\n\nTo add it on another machine:\n%s",
		out.MCPURL, out.ConfigURL, command), out)
}
