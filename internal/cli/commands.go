package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/langtind/ctlkit"

	"github.com/langtind/checkinctl/internal/checkin"
)

const (
	defaultLimit = 200
	maxLimit     = 1000

	defaultInterval = 10 * time.Second
	minInterval     = 5 * time.Second
	maxWait         = 24 * time.Hour
)

// commands is the command table, the single description of the command
// line. Add a command or a flag here and nowhere else.
func (a *App) commands() []ctlkit.Command {
	return []ctlkit.Command{
		{
			Name:    "login",
			Summary: "Store a ticket link in the system keyring, under a name",
			Long:    loginSteps,
			Args:    []ctlkit.Arg{{Name: "name", Required: true, Help: "what to call the ticket, such as the first name of its holder"}},
			Flags:   []ctlkit.Flag{{Name: "link-stdin", Kind: ctlkit.Bool, Help: "read the link from stdin instead of prompting"}},
			Run:     a.login,
		},
		{
			Name:    "logout",
			Summary: "Remove a stored ticket link",
			Args:    []ctlkit.Arg{{Name: "name", Required: true, Help: "the name, as status prints it"}},
			Run:     a.logout,
		},
		{
			Name:    "status",
			Summary: "List the stored tickets, without calling Checkin",
			Flags:   []ctlkit.Flag{ctlkit.JSONFlag},
			Run:     a.status,
		},
		{
			Name:    "event",
			Summary: "Show an event and its ticket types",
			Long:    "Needs no stored ticket. The event id is the number status prints for a ticket.",
			Args:    []ctlkit.Arg{{Name: "event-id", Required: true, ID: true, Help: "the number of the event"}},
			Flags:   []ctlkit.Flag{ctlkit.JSONFlag, ctlkit.FieldsFlag},
			Run:     a.event,
		},
		{
			Name:    "options",
			Summary: "List what a ticket can choose at an event, and how many places each option has left",
			Long:    "Names the event with --event, or with --ticket for the event of a stored ticket. With one stored ticket and neither flag, its event is used. In JSON, left is null for an option without a limit and 0 for one that is full.",
			Flags: []ctlkit.Flag{
				{Name: "event", Kind: ctlkit.String, Value: "event-id", Help: "the number of the event"},
				{Name: "ticket", Kind: ctlkit.String, Value: "name", Help: "a stored ticket, for its event"},
				{Name: "group", Kind: ctlkit.List, Value: "group-id", Help: "only these groups"},
				{Name: "available", Kind: ctlkit.Bool, Help: "only options that can be chosen now"},
				ctlkit.LimitFlag(defaultLimit, maxLimit), ctlkit.JSONFlag, ctlkit.FieldsFlag,
			},
			Run: a.options,
		},
		{
			Name:    "ticket",
			Summary: "Show what a stored ticket has chosen",
			Long:    "Shows the choices as the ticket's own edit page shows them. What the holder wrote in text fields is not read.",
			Args:    []ctlkit.Arg{{Name: "name", Required: true, Help: "the name, as status prints it"}},
			Flags:   []ctlkit.Flag{ctlkit.JSONFlag, ctlkit.FieldsFlag},
			Run:     a.showTicket,
		},
		{
			Name:    "choose",
			Summary: "Change what a ticket has chosen in one group",
			Long:    chooseLong,
			Mutates: true,
			Args: []ctlkit.Arg{
				{Name: "name", Required: true, Help: "the name of the ticket, as status prints it"},
				{Name: "group-id", Required: true, ID: true, Help: "the group, as options prints it"},
				{Name: "option-id", Required: true, ID: true, Help: "the option to choose, as options prints it"},
			},
			Flags: []ctlkit.Flag{
				{Name: "wait", Kind: ctlkit.String, Value: "duration", Help: "when the option is full, keep looking for this long, such as 30m or 6h, and choose it when a place opens"},
				{Name: "interval", Kind: ctlkit.String, Value: "duration", Default: defaultInterval.String(), Help: "how long to wait between two looks, at least " + minInterval.String()},
				{Name: "then", Kind: ctlkit.List, Value: "group-id:option-id", Help: "when the change is made, also choose this option in another group, such as a level that shows only with the new choice"},
				ctlkit.DryRunFlag, ctlkit.YesFlag, ctlkit.JSONFlag,
			},
			Run: a.choose,
		},
	}
}

const loginSteps = `Checkin has no account for a participant. The link in the confirmation email is the whole login: whoever holds it can change the ticket. "` + Name + ` login <name>" asks for the link and stores it in the system keyring. In a script, pipe it in with --link-stdin, and never pass it as an argument.

The link is the one behind "Klikk her for å se hele bestillingen" in the email, or the one behind "Endre" on that page. An order with more than one ticket needs the "Endre" link of the right person.

A link lasts for as long as the organiser lets a ticket be changed. There is nothing to renew.`

const chooseLong = `The change is stored at Checkin at once, and the place the ticket had is given to others. Run it with --dry-run first.

When the option is full, Checkin refuses the change and the ticket keeps what it had. With --wait, the command looks again every --interval and sends the change when a place opens, until the time is up.

With --then, a second choice is made right after the first, such as the level of the new activity. It is sent only when the first change was made. When it is refused, the first change stays, and the error says so.

Choosing the option the ticket already has sends nothing and fails with a conflict. Only groups of the type radio can be chosen.`

func (a *App) login(ctx context.Context, c *ctlkit.Call) error {
	name := c.Arg(0)
	if err := checkName(name); err != nil {
		return err
	}
	text, err := c.Secret("Ticket link", "link-stdin")
	if err != nil {
		return err
	}
	link, err := checkin.ParseLink(text)
	if err != nil {
		return err
	}

	if link.Order != 0 {
		tickets, err := a.Client.OrderTickets(ctx, link.Order, link.Code)
		if err != nil {
			return err
		}
		switch len(tickets) {
		case 0:
			return ctlkit.Errorf(ctlkit.KindConflict, "the order page has no link for changing a ticket").
				WithHint("the organiser may not have opened for changes yet.")
		case 1:
			link.Ticket = tickets[0]
		default:
			return ctlkit.Usagef("the order has %d tickets", len(tickets)).
				WithHint(`open the order page, copy the link behind "Endre" next to the right person, and give that one.`)
		}
	}

	event, _, err := a.Client.Read(ctx, link.Ticket, link.Code)
	if err != nil {
		return err
	}
	tickets, err := a.tickets()
	if err != nil {
		return err
	}
	tickets[name] = checkin.Ticket{ID: link.Ticket, Code: link.Code, Event: event}
	if err := a.saveTickets(tickets); err != nil {
		return err
	}
	fmt.Fprintf(c.Stdout, "Stored ticket %d of event %d as %q. The link is in the system keyring.\n", link.Ticket, event, name)
	return nil
}

func (a *App) logout(ctx context.Context, c *ctlkit.Call) error {
	name := c.Arg(0)
	tickets, err := a.tickets()
	if err != nil {
		return err
	}
	if _, ok := tickets[name]; !ok {
		return ctlkit.NotFoundf("no ticket is stored as %q", name).WithHint(`run "%s status" to see the names.`, Name)
	}
	delete(tickets, name)
	if err := a.saveTickets(tickets); err != nil {
		return err
	}
	fmt.Fprintf(c.Stdout, "Removed the stored ticket %q.\n", name)
	return nil
}

// storedTicket is one line of status. It never holds the code.
type storedTicket struct {
	Name     string `json:"name"`
	TicketID int    `json:"ticketId"`
	EventID  int    `json:"eventId"`
}

type storedTickets struct {
	Tickets []storedTicket `json:"tickets"`
}

func (a *App) status(ctx context.Context, c *ctlkit.Call) error {
	tickets, err := a.tickets()
	if err != nil {
		return err
	}
	list := storedTickets{Tickets: []storedTicket{}}
	for _, name := range names(tickets) {
		list.Tickets = append(list.Tickets, storedTicket{Name: name, TicketID: tickets[name].ID, EventID: tickets[name].Event})
	}
	return c.Print(list, func(w io.Writer) { renderStatus(w, list) })
}

func number(what, value string) (int, error) {
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return 0, ctlkit.Usagef("%s takes a number, not %q", what, value)
	}
	return n, nil
}

func (a *App) event(ctx context.Context, c *ctlkit.Call) error {
	id, err := number("event-id", c.Arg(0))
	if err != nil {
		return err
	}
	event, err := a.Client.Event(ctx, id)
	if err != nil {
		return err
	}
	return c.Print(event, func(w io.Writer) { renderEvent(w, event) })
}

// optionRow is one option in the list options prints.
type optionRow struct {
	GroupID int    `json:"groupId"`
	Group   string `json:"group"`
	Type    string `json:"type"`
	ID      int    `json:"id"`
	Name    string `json:"name"`
	Price   string `json:"price,omitempty"`
	// Left is how many places are left, 0 when the option is full, and null
	// when it has no limit.
	Left *int `json:"left"`
	// ShownBy lists the options that make the group show. Empty means that
	// the group always shows.
	ShownBy []int `json:"shownBy"`
}

type optionList struct {
	EventID   int         `json:"eventId"`
	Event     string      `json:"event"`
	Items     []optionRow `json:"items"`
	Truncated bool        `json:"truncated"`
}

// eventOf finds the event options is asked about.
func (a *App) eventOf(c *ctlkit.Call) (int, error) {
	if c.Given("event") && c.Given("ticket") {
		return 0, ctlkit.Usagef("give --event or --ticket, not both")
	}
	if c.Given("event") {
		return number("--event", c.String("event"))
	}
	if c.Given("ticket") {
		ticket, err := a.ticket(c.String("ticket"))
		return ticket.Event, err
	}
	tickets, err := a.tickets()
	if err != nil {
		return 0, err
	}
	if len(tickets) != 1 {
		return 0, ctlkit.Usagef("name the event with --event or --ticket").
			WithHint(`%d tickets are stored. Run "%s status" to see them.`, len(tickets), Name)
	}
	for _, ticket := range tickets {
		return ticket.Event, nil
	}
	return 0, nil
}

func (a *App) options(ctx context.Context, c *ctlkit.Call) error {
	limit := c.Int("limit")
	if limit < 1 || limit > maxLimit {
		return ctlkit.Usagef("--limit takes 1 to %d, not %d", maxLimit, limit)
	}
	only := map[int]bool{}
	for _, value := range c.List("group") {
		id, err := number("--group", value)
		if err != nil {
			return err
		}
		only[id] = true
	}
	eventID, err := a.eventOf(c)
	if err != nil {
		return err
	}
	event, err := a.Client.Event(ctx, eventID)
	if err != nil {
		return err
	}
	for id := range only {
		if event.Group(id) == nil {
			return ctlkit.NotFoundf("event %d has no group with the id %d", eventID, id).
				WithHint(`run "%s options --event %d" to see the groups.`, Name, eventID)
		}
	}

	list := optionList{EventID: event.ID, Event: event.Name, Items: []optionRow{}}
	seen := map[int]bool{}
	for _, category := range event.Categories {
		for _, group := range category.Groups {
			// A group can belong to more than one ticket type.
			if seen[group.ID] || (len(only) > 0 && !only[group.ID]) {
				continue
			}
			seen[group.ID] = true
			for _, option := range group.Options {
				if c.Bool("available") && !option.Free() {
					continue
				}
				if len(list.Items) == limit {
					list.Truncated = true
					continue
				}
				list.Items = append(list.Items, optionRow{
					GroupID: group.ID, Group: group.Name, Type: group.Type,
					ID: option.ID, Name: option.Name, Price: option.Price, Left: option.Left, ShownBy: group.ShownBy,
				})
			}
		}
	}
	return c.Print(list, func(w io.Writer) { renderOptions(w, list) })
}

// choice is what a ticket has chosen in one group.
type choice struct {
	GroupID  int    `json:"groupId"`
	Group    string `json:"group"`
	OptionID int    `json:"optionId"`
	Option   string `json:"option"`
	// Left is how many places the option has left for others.
	Left *int `json:"left"`
}

type ticketView struct {
	Name     string   `json:"name"`
	TicketID int      `json:"ticketId"`
	EventID  int      `json:"eventId"`
	Event    string   `json:"event"`
	Choices  []choice `json:"choices"`
}

// choices names what a ticket has chosen, in the order the event has its
// groups. A choice the event no longer knows is shown by its ids.
func choices(event *checkin.Event, chosen checkin.Chosen) []choice {
	list := []choice{}
	seen := map[int]bool{}
	for _, category := range event.Categories {
		for _, group := range category.Groups {
			optionID, ok := chosen[group.ID]
			if !ok || seen[group.ID] {
				continue
			}
			seen[group.ID] = true
			item := choice{GroupID: group.ID, Group: group.Name, OptionID: optionID, Option: strconv.Itoa(optionID)}
			if option := group.Option(optionID); option != nil {
				item.Option, item.Left = option.Name, option.Left
			}
			list = append(list, item)
		}
	}
	return list
}

// read fetches what a ticket has chosen and the event it belongs to.
func (a *App) read(ctx context.Context, ticket checkin.Ticket) (*checkin.Event, checkin.Chosen, error) {
	eventID, chosen, err := a.Client.Read(ctx, ticket.ID, ticket.Code)
	if err != nil {
		return nil, nil, err
	}
	if eventID == 0 {
		eventID = ticket.Event
	}
	event, err := a.Client.Event(ctx, eventID)
	if err != nil {
		return nil, nil, err
	}
	return event, chosen, nil
}

func (a *App) showTicket(ctx context.Context, c *ctlkit.Call) error {
	ticket, err := a.ticket(c.Arg(0))
	if err != nil {
		return err
	}
	event, chosen, err := a.read(ctx, ticket)
	if err != nil {
		return err
	}
	view := ticketView{Name: c.Arg(0), TicketID: ticket.ID, EventID: event.ID, Event: event.Name, Choices: choices(event, chosen)}
	return c.Print(view, func(w io.Writer) { renderTicket(w, view) })
}

// named is a group or an option in what choose prints.
type named struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// chooseResult is what choose prints. With DryRun set, nothing was sent and
// Request is what would have been, without the code of the link.
type chooseResult struct {
	DryRun   bool   `json:"dryRun"`
	Ticket   string `json:"ticket"`
	TicketID int    `json:"ticketId"`
	Group    named  `json:"group"`
	// From is what the ticket had in the group, and null when it had nothing.
	From *named `json:"from"`
	To   named  `json:"to"`
	// Left is how many places the option had when it was last looked at.
	Left    *int                  `json:"left"`
	Request checkin.ChangeRequest `json:"request"`
	// Attempts is how many times the change was sent. A refused change
	// changes nothing.
	Attempts int `json:"attempts"`
	// Then holds the choices --then asked for, in the order they are made.
	Then []thenStep `json:"then,omitempty"`
	// Choices is what the ticket has after the change.
	Choices []choice `json:"choices,omitempty"`
}

// thenStep is one choice that follows the change. Done says that the
// ticket has it.
type thenStep struct {
	Group named `json:"group"`
	To    named `json:"to"`
	Done  bool  `json:"done"`
}

func duration(flag, value string) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	if err != nil || d <= 0 {
		return 0, ctlkit.Usagef("--%s takes a duration such as 30s, 10m or 2h, not %q", flag, value)
	}
	return d, nil
}

func (a *App) choose(ctx context.Context, c *ctlkit.Call) error {
	name := c.Arg(0)
	groupID, err := number("group-id", c.Arg(1))
	if err != nil {
		return err
	}
	optionID, err := number("option-id", c.Arg(2))
	if err != nil {
		return err
	}
	var wait time.Duration
	if c.Given("wait") {
		if wait, err = duration("wait", c.String("wait")); err != nil {
			return err
		}
		if wait > maxWait {
			return ctlkit.Usagef("--wait takes at most %s, not %s", maxWait, wait)
		}
	}
	interval, err := duration("interval", c.String("interval"))
	if err != nil {
		return err
	}
	if interval < minInterval {
		return ctlkit.Usagef("--interval takes at least %s, not %s", minInterval, interval)
	}

	ticket, err := a.ticket(name)
	if err != nil {
		return err
	}
	// Look before: what the ticket has, and what the event offers.
	event, chosen, err := a.read(ctx, ticket)
	if err != nil {
		return err
	}
	optionsHint := fmt.Sprintf(`run "%s options --ticket %s" to see the groups and their options.`, Name, name)
	group := event.Group(groupID)
	if group == nil {
		return ctlkit.NotFoundf("event %d has no group with the id %d", event.ID, groupID).WithHint("%s", optionsHint)
	}
	if group.Type != "radio" {
		return ctlkit.Usagef("group %d, %q, is of the type %s, and only radio groups can be chosen", groupID, oneLine(group.Name), group.Type)
	}
	option := group.Option(optionID)
	if option == nil {
		return ctlkit.NotFoundf("group %d, %q, has no option with the id %d", groupID, oneLine(group.Name), optionID).WithHint("%s", optionsHint)
	}
	if len(group.ShownBy) > 0 && !shown(group, chosen) {
		return ctlkit.Errorf(ctlkit.KindConflict, "group %d, %q, does not apply to what the ticket has chosen", groupID, oneLine(group.Name)).
			WithHint("it shows only with one of the options %s. Choose that first.", ids(group.ShownBy))
	}

	result := chooseResult{
		Ticket: name, TicketID: ticket.ID,
		Group: named{group.ID, group.Name}, To: named{option.ID, option.Name}, Left: option.Left,
	}
	mode := "add"
	from := "nothing"
	if current, ok := chosen[groupID]; ok {
		mode = "update"
		result.From = &named{ID: current, Name: strconv.Itoa(current)}
		if had := group.Option(current); had != nil {
			result.From.Name = had.Name
		}
		from = fmt.Sprintf("%q", oneLine(result.From.Name))
		if current == optionID {
			return ctlkit.Errorf(ctlkit.KindConflict, "ticket %q already has %q in %q", name, oneLine(option.Name), oneLine(group.Name)).
				WithHint("nothing was sent, and there is nothing to change.")
		}
	}
	result.Request = checkin.ChangeRequest{TicketID: ticket.ID, Additionals: []checkin.Change{{Mode: mode, Group: groupID, Option: optionID}}}

	// What follows the change is checked against what the ticket will have.
	question := fmt.Sprintf("Change %q on ticket %q from %s to %q", oneLine(group.Name), name, from, oneLine(option.Name))
	after := checkin.Chosen{groupID: optionID}
	for g, o := range chosen {
		if g != groupID {
			after[g] = o
		}
	}
	taken := map[int]bool{groupID: true}
	for _, pair := range c.List("then") {
		g, o, ok := strings.Cut(pair, ":")
		thenGroup, errGroup := strconv.Atoi(g)
		thenOption, errOption := strconv.Atoi(o)
		if !ok || errGroup != nil || errOption != nil {
			return ctlkit.Usagef("--then takes <group-id>:<option-id>, not %q", pair)
		}
		next := event.Group(thenGroup)
		if next != nil && next.Type != "radio" {
			return ctlkit.Usagef("--then %s: group %q is of the type %s, and only radio groups can be chosen", pair, oneLine(next.Name), next.Type)
		}
		if next == nil || next.Option(thenOption) == nil {
			return ctlkit.NotFoundf("--then %s: the event has no such group and option", pair).WithHint("%s", optionsHint)
		}
		if taken[thenGroup] {
			return ctlkit.Usagef("--then %s: group %q is already being changed by this command", pair, oneLine(next.Name))
		}
		if len(next.ShownBy) > 0 && !shown(next, after) {
			return ctlkit.Errorf(ctlkit.KindConflict, "--then %s: group %q does not apply after the change", pair, oneLine(next.Name)).
				WithHint("it shows only with one of the options %s.", ids(next.ShownBy))
		}
		taken[thenGroup] = true
		after[thenGroup] = thenOption
		result.Then = append(result.Then, thenStep{Group: named{next.ID, next.Name}, To: named{thenOption, next.Option(thenOption).Name}})
		question += fmt.Sprintf(", and then choose %q in %q", oneLine(next.Option(thenOption).Name), oneLine(next.Name))
	}

	if c.DryRun() {
		result.DryRun = true
		return c.Print(result, func(w io.Writer) { renderChoose(w, result) })
	}
	if !option.Free() && wait == 0 {
		return full(option, name)
	}
	if err := c.Confirm(question + "?"); err != nil {
		return err
	}

	deadline := a.Now().Add(wait)
	for {
		if option.Free() {
			result.Attempts++
			answer, err := a.Client.Choose(ctx, ticket.Code, result.Request)
			if err != nil {
				if err := a.unclear(ctx, ticket, name, groupID, optionID, err); err != nil {
					return err
				}
				c.Note("The answer to the change was lost, but the ticket shows that it was made.")
				break
			}
			if len(answer.Warnings) == 0 && answer.Options[groupID] == optionID {
				break
			}
			if len(answer.Warnings) != 1 || answer.Warnings[0] != checkin.WarningFull {
				return ctlkit.Errorf(ctlkit.KindConflict, "the change was refused with the warning %q", strings.Join(answer.Warnings, ", ")).
					WithHint(`run "%s ticket %s" to see what the ticket has.`, Name, name)
			}
			// Somebody else took the place first. The ticket keeps what it had.
			taken := 0
			option.Left = &taken
			result.Left = option.Left
		}
		if wait == 0 || a.Now().Add(interval).After(deadline) {
			return full(option, name)
		}
		if err := a.Sleep(ctx, interval); err != nil {
			return err
		}
		// A failed look is not a no. Keep what was known and look again.
		if again, err := a.Client.Event(ctx, event.ID); err == nil {
			if g := again.Group(groupID); g != nil && g.Option(optionID) != nil {
				option = g.Option(optionID)
				result.Left = option.Left
			}
		} else {
			c.Note("Could not look at the event: %v. Looking again in %s.", err, interval)
		}
	}

	for i := range result.Then {
		if err := a.then(ctx, c, ticket, name, &result.Then[i]); err != nil {
			return ctlkit.Errorf(ctlkit.KindOf(err), "%q was changed to %q, but the choice of %q in %q was not made: %w",
				oneLine(group.Name), oneLine(option.Name), oneLine(result.Then[i].To.Name), oneLine(result.Then[i].Group.Name), err).
				WithHint(`the first change stays. Run "%s ticket %s" to see what the ticket has.`, Name, name)
		}
	}

	// Look after: what the ticket has now.
	if _, after, err := a.Client.Read(ctx, ticket.ID, ticket.Code); err == nil {
		result.Choices = choices(event, after)
	} else {
		c.Note("The change was made, but the ticket could not be read afterwards: %v", err)
	}
	return c.Print(result, func(w io.Writer) { renderChoose(w, result) })
}

// then makes one choice that follows the change: it looks at the ticket,
// sends once when the ticket does not have the option, and never again.
func (a *App) then(ctx context.Context, c *ctlkit.Call, ticket checkin.Ticket, name string, step *thenStep) error {
	_, has, err := a.Client.Read(ctx, ticket.ID, ticket.Code)
	if err != nil {
		return err
	}
	current, chosen := has[step.Group.ID]
	if chosen && current == step.To.ID {
		step.Done = true
		return nil
	}
	mode := "add"
	if chosen {
		mode = "update"
	}
	request := checkin.ChangeRequest{TicketID: ticket.ID, Additionals: []checkin.Change{{Mode: mode, Group: step.Group.ID, Option: step.To.ID}}}
	answer, err := a.Client.Choose(ctx, ticket.Code, request)
	if err != nil {
		if err := a.unclear(ctx, ticket, name, step.Group.ID, step.To.ID, err); err != nil {
			return err
		}
		c.Note("The answer to the second change was lost, but the ticket shows that it was made.")
		step.Done = true
		return nil
	}
	if len(answer.Warnings) > 0 || answer.Options[step.Group.ID] != step.To.ID {
		return ctlkit.Errorf(ctlkit.KindConflict, "it was refused with the warning %q", strings.Join(answer.Warnings, ", "))
	}
	step.Done = true
	return nil
}

// shown says whether the ticket has one of the options that make the group show.
func shown(group *checkin.Group, chosen checkin.Chosen) bool {
	for _, trigger := range group.ShownBy {
		for _, has := range chosen {
			if has == trigger {
				return true
			}
		}
	}
	return false
}

func ids(list []int) string {
	parts := make([]string, len(list))
	for i, id := range list {
		parts[i] = strconv.Itoa(id)
	}
	return strings.Join(parts, ", ")
}

func full(option *checkin.Option, ticket string) error {
	return ctlkit.Errorf(ctlkit.KindConflict, "%q is full", oneLine(option.Name)).
		WithHint(`ticket %q keeps what it had. Give --wait with a duration to choose it when a place opens.`, ticket)
}

// unclear handles a change that got no clear answer. It never sends the
// change again. It looks at the ticket, and returns nil when the ticket
// shows that the change was made.
func (a *App) unclear(ctx context.Context, ticket checkin.Ticket, name string, groupID, optionID int, sendErr error) error {
	_, after, err := a.Client.Read(ctx, ticket.ID, ticket.Code)
	switch {
	case err != nil:
		return ctlkit.Errorf(ctlkit.KindUnavailable, "it is not known whether the change was made: %w", sendErr).
			WithHint(`do not run the command again before "%s ticket %s" has shown what the ticket has.`, Name, name)
	case after[groupID] == optionID:
		return nil
	default:
		return ctlkit.Errorf(ctlkit.KindOf(sendErr), "the change was not made: %w", sendErr).
			WithHint("the ticket has what it had. It is safe to try again.")
	}
}
