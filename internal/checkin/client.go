// Package checkin is the client for Checkin, as reference/README.md
// describes it.
package checkin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/langtind/ctlkit"
	"github.com/langtind/ctlkit/httpx"
)

const service = "Checkin"

type Client struct {
	HTTP      *http.Client
	BaseURL   string
	UserAgent string
}

func NewClient() *Client {
	return &Client{HTTP: httpx.NewClient(), BaseURL: "https://app.checkin.no"}
}

// Ticket is a ticket link taken apart. The code is the whole login: whoever
// holds it can change the ticket.
type Ticket struct {
	ID   int    `json:"ticket"`
	Code string `json:"code"`
	// Event is the event the ticket belongs to, as the edit page names it.
	Event int `json:"event"`
}

// Link is what a pasted link names: a ticket, or an order that holds tickets.
type Link struct {
	Ticket int
	Order  int
	Code   string
}

var linkPath = regexp.MustCompile(`/(t|o)/(\d+)/([A-Za-z0-9]+)(?:/edit)?/?(?:[?#].*)?$`)

// ParseLink reads a link from a confirmation email: the order page,
// /o/<order>/<code>, or the edit page of a ticket, /t/<ticket>/<code>/edit.
func ParseLink(text string) (Link, error) {
	m := linkPath.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return Link{}, ctlkit.Usagef("that is not a Checkin link").
			WithHint("it looks like https://app.checkin.no/o/<number>/<code> or https://app.checkin.no/t/<number>/<code>/edit.")
	}
	id, err := strconv.Atoi(m[2])
	if err != nil {
		return Link{}, ctlkit.Usagef("the number in the link is too long")
	}
	if m[1] == "t" {
		return Link{Ticket: id, Code: m[3]}, nil
	}
	return Link{Order: id, Code: m[3]}, nil
}

// Event is an event with every choice a ticket can make.
type Event struct {
	ID         int        `json:"id"`
	Name       string     `json:"name"`
	Organizer  string     `json:"organizer,omitempty"`
	Start      string     `json:"start,omitempty"`
	End        string     `json:"end,omitempty"`
	Opens      string     `json:"opens,omitempty"`
	Closes     string     `json:"closes,omitempty"`
	Categories []Category `json:"categories"`
}

// Category is a ticket type.
type Category struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Price string `json:"price,omitempty"`
	// Left is how many tickets are left, and nil when there is no limit.
	Left   *int    `json:"left"`
	Groups []Group `json:"groups"`
}

// Group is one question of a ticket type. Checkin calls it an additional.
type Group struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
	// ShownBy lists the options that make the group show. Empty means that
	// it always shows.
	ShownBy []int    `json:"shownBy"`
	Options []Option `json:"options"`
}

type Option struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Price string `json:"price,omitempty"`
	// Left is how many places are left, 0 when it is full, and nil when the
	// option has no limit.
	Left        *int `json:"left"`
	WaitingList bool `json:"waitingList"`
}

// Free says whether the option can be chosen now.
func (o Option) Free() bool { return o.Left == nil || *o.Left > 0 }

// Group finds a group by its id, in any ticket type.
func (e *Event) Group(id int) *Group {
	for c := range e.Categories {
		for g := range e.Categories[c].Groups {
			if e.Categories[c].Groups[g].ID == id {
				return &e.Categories[c].Groups[g]
			}
		}
	}
	return nil
}

// Option finds an option of the group by its id.
func (g *Group) Option(id int) *Option {
	for o := range g.Options {
		if g.Options[o].ID == id {
			return &g.Options[o]
		}
	}
	return nil
}

const eventQuery = `query ($id: Int!) {
  event(id: $id) {
    id name organizer start end opens close
    categories {
      id name price left
      additionals {
        id name type required triggered_by
        children { id name price left waiting_list }
      }
    }
  }
}`

// Event fetches an event. It needs no login.
func (c *Client) Event(ctx context.Context, id int) (*Event, error) {
	var answer struct {
		Event *struct {
			ID         int    `json:"id"`
			Name       string `json:"name"`
			Organizer  string `json:"organizer"`
			Start      string `json:"start"`
			End        string `json:"end"`
			Opens      string `json:"opens"`
			Close      string `json:"close"`
			Categories []struct {
				ID          int    `json:"id"`
				Name        string `json:"name"`
				Price       string `json:"price"`
				Left        *int   `json:"left"`
				Additionals []struct {
					ID          int    `json:"id"`
					Name        string `json:"name"`
					Type        string `json:"type"`
					Required    bool   `json:"required"`
					TriggeredBy []int  `json:"triggered_by"`
					Children    []struct {
						ID          int    `json:"id"`
						Name        string `json:"name"`
						Price       string `json:"price"`
						Left        *int   `json:"left"`
						WaitingList bool   `json:"waiting_list"`
					} `json:"children"`
				} `json:"additionals"`
			} `json:"categories"`
		} `json:"event"`
	}
	if err := c.graphql(ctx, eventQuery, map[string]any{"id": id}, &answer); err != nil {
		return nil, err
	}
	if answer.Event == nil {
		return nil, ctlkit.NotFoundf("%s has no event with the id %d", service, id)
	}

	from := answer.Event
	event := &Event{ID: from.ID, Name: from.Name, Organizer: from.Organizer, Start: from.Start, End: from.End, Opens: from.Opens, Closes: from.Close, Categories: []Category{}}
	for _, cat := range from.Categories {
		category := Category{ID: cat.ID, Name: cat.Name, Price: cat.Price, Left: cat.Left, Groups: []Group{}}
		for _, add := range cat.Additionals {
			group := Group{ID: add.ID, Name: add.Name, Type: add.Type, Required: add.Required, ShownBy: add.TriggeredBy, Options: []Option{}}
			if group.ShownBy == nil {
				group.ShownBy = []int{}
			}
			for _, child := range add.Children {
				group.Options = append(group.Options, Option{ID: child.ID, Name: child.Name, Price: child.Price, Left: child.Left, WaitingList: child.WaitingList})
			}
			category.Groups = append(category.Groups, group)
		}
		event.Categories = append(event.Categories, category)
	}
	return event, nil
}

// Chosen is what a ticket has chosen: the option for each group.
type Chosen map[int]int

var (
	inputTag  = regexp.MustCompile(`(?s)<input\b[^>]*>`)
	attribute = regexp.MustCompile(`([\w-]+)(?:\s*=\s*"([^"]*)")?`)
	groupName = regexp.MustCompile(`^additionals\[(\d+)\]$`)
	// The page asks for its event with event(id: $id), and gives the id
	// as the variable further down.
	eventMark = "event(id: $id)"
	eventID   = regexp.MustCompile(`\bid:\s*(\d+)`)
	editLink  = regexp.MustCompile(`href="/t/(\d+)/([A-Za-z0-9]+)/edit"`)
)

// Read fetches the edit page of a ticket and returns the event it belongs to
// and what it has chosen. Only radio groups are read. What the participant
// wrote in the text fields is left alone.
func (c *Client) Read(ctx context.Context, ticketID int, code string) (event int, chosen Chosen, err error) {
	page, err := c.page(ctx, fmt.Sprintf("/t/%d/%s/edit", ticketID, code))
	if err != nil {
		return 0, nil, err
	}

	chosen = Chosen{}
	radios := 0
	for _, tag := range inputTag.FindAllString(page, -1) {
		attrs := map[string]string{}
		checked := false
		for _, m := range attribute.FindAllStringSubmatch(tag[len("<input"):], -1) {
			attrs[m[1]] = m[2]
			checked = checked || m[1] == "checked"
		}
		group := groupName.FindStringSubmatch(attrs["name"])
		if attrs["type"] != "radio" || group == nil {
			continue
		}
		radios++
		if !checked {
			continue
		}
		groupID, _ := strconv.Atoi(group[1])
		optionID, err := strconv.Atoi(attrs["value"])
		if err != nil {
			continue
		}
		chosen[groupID] = optionID
	}

	if at := strings.Index(page, eventMark); at >= 0 {
		if m := eventID.FindStringSubmatch(page[at+len(eventMark):]); m != nil {
			event, _ = strconv.Atoi(m[1])
		}
	}
	if event == 0 && radios == 0 {
		return 0, nil, fmt.Errorf("%s: the edit page of ticket %d does not look as expected: it names no event and has no choices", service, ticketID)
	}
	return event, chosen, nil
}

// OrderTickets returns the tickets an order page links to.
func (c *Client) OrderTickets(ctx context.Context, order int, code string) ([]int, error) {
	page, err := c.page(ctx, fmt.Sprintf("/o/%d/%s", order, code))
	if err != nil {
		return nil, err
	}
	tickets := []int{}
	seen := map[int]bool{}
	for _, m := range editLink.FindAllStringSubmatch(page, -1) {
		id, err := strconv.Atoi(m[1])
		if err != nil || m[2] != code || seen[id] {
			continue
		}
		seen[id] = true
		tickets = append(tickets, id)
	}
	return tickets, nil
}

// Change is one line of a ChangeRequest, as the edit page sends it.
type Change struct {
	// Mode is "add" when the ticket has no choice in the group, and
	// "update" when it has one.
	Mode    string `json:"mode"`
	Group   int    `json:"id"`
	Option  int    `json:"child_id"`
	Waiting bool   `json:"waiting"`
}

// ChangeRequest is what Choose sends, without the code. A dry run prints it.
type ChangeRequest struct {
	TicketID    int      `json:"ticket_id"`
	Additionals []Change `json:"additionals"`
}

// ChangeResult is the answer to a change.
type ChangeResult struct {
	// Options holds the option the ticket has in each group after the
	// change, hidden groups included.
	Options Chosen
	// Warnings are the reasons the service gave for not making the change,
	// such as "full". Empty means that it was made.
	Warnings []string
}

const changeMutation = `mutation ($ticket_id: Int!, $hash: String!, $additionals: [AdditionalUpdateInput]) {
  updateOldEventTicket(ticket_id: $ticket_id, hash: $hash, additionals: $additionals) {
    additionals { id additional_id additional_parent_id }
    warnings { id type warning }
  }
}`

// WarningFull is the warning for an option with no places left. The ticket
// keeps what it had.
const WarningFull = "full"

// Choose sends one change. The service stores it at once. A change it
// refuses comes back as a warning, and the ticket keeps what it had.
func (c *Client) Choose(ctx context.Context, code string, request ChangeRequest) (*ChangeResult, error) {
	var answer struct {
		Ticket *struct {
			Additionals []struct {
				Option int  `json:"additional_id"`
				Group  *int `json:"additional_parent_id"`
			} `json:"additionals"`
			Warnings []struct {
				Warning string `json:"warning"`
			} `json:"warnings"`
		} `json:"updateOldEventTicket"`
	}
	variables := map[string]any{"ticket_id": request.TicketID, "hash": code, "additionals": request.Additionals}
	if err := c.graphql(ctx, changeMutation, variables, &answer); err != nil {
		return nil, err
	}
	if answer.Ticket == nil {
		return nil, fmt.Errorf("%s answered the change without the ticket", service)
	}
	result := &ChangeResult{Options: Chosen{}, Warnings: []string{}}
	for _, line := range answer.Ticket.Additionals {
		if line.Group != nil {
			result.Options[*line.Group] = line.Option
		}
	}
	for _, w := range answer.Ticket.Warnings {
		result.Warnings = append(result.Warnings, w.Warning)
	}
	return result, nil
}

// graphql sends one query or mutation and decodes its data into into. An
// error comes as HTTP 200 with a list of errors.
func (c *Client) graphql(ctx context.Context, query string, variables map[string]any, into any) error {
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/graphql", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	raw, err := httpx.Do(c.HTTP, req, service, c.UserAgent)
	if err != nil {
		return err
	}

	var answer struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message    string `json:"message"`
			Extensions struct {
				Code string `json:"code"`
			} `json:"extensions"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return fmt.Errorf("%s: read the answer: %w", service, err)
	}
	if len(answer.Errors) > 0 {
		first := answer.Errors[0]
		if first.Extensions.Code == "NOTFOUND" {
			return ctlkit.NotFoundf("%s: %s", service, first.Message)
		}
		return fmt.Errorf("%s: %s", service, first.Message)
	}
	if len(answer.Data) == 0 {
		return fmt.Errorf("%s answered without data", service)
	}
	if err := json.Unmarshal(answer.Data, into); err != nil {
		return fmt.Errorf("%s: read the answer: %w", service, err)
	}
	return nil
}

// page fetches one of the HTML pages a ticket link leads to. Checkin
// answers a wrong code and an unknown number alike, with a redirect to its
// front page.
func (c *Client) page(ctx context.Context, path string) (string, error) {
	client := *c.HTTP
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "text/html")
	raw, err := httpx.Do(&client, req, service, c.UserAgent)
	if status := httpx.Status(err); status >= 300 && status < 400 || status == http.StatusNotFound {
		return "", ctlkit.NotFoundf("%s does not know that ticket link", service).
			WithHint("the number or the code is wrong, or the link no longer works. Copy the link from the confirmation email again.")
	}
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
