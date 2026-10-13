package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/langtind/ctlkit"
	"github.com/langtind/ctlkit/ctltest"
	"github.com/langtind/ctlkit/keystore"

	"github.com/langtind/checkinctl/internal/checkin"
)

// The fake event: one ticket type with an activity group, a level group
// that shows only for Skiing, and a text field. Everything is made up.
const (
	eventID  = 500
	ticketID = 7
	code     = "abc123"
	link     = "https://app.checkin.no/t/7/abc123/edit"

	activities = 100
	skiing     = 101
	climbing   = 102
	chess      = 103
	levels     = 200
	beginner   = 201
)

// harness runs checkinctl against a fake Checkin, with the tickets in memory.
type harness struct {
	t      *testing.T
	fake   *ctltest.Fake
	store  *keystore.Memory
	now    time.Time
	chosen map[int]int
	// left is how many places each limited option has.
	left map[int]int
	// sleeps counts the waits, and onSleep runs before each one returns.
	sleeps  int
	onSleep func()
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{
		t: t, fake: ctltest.NewFake(t), store: &keystore.Memory{},
		now:    time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC),
		chosen: map[int]int{activities: skiing},
		left:   map[int]int{skiing: 5, climbing: 0},
	}
	h.fake.Check = func(t *testing.T, c ctltest.Call) {
		if got := c.Header.Get("User-Agent"); got != Name+"/1.2.3 (+https://github.com/langtind/"+Name+")" {
			t.Errorf("User-Agent = %q", got)
		}
	}
	h.fake.Handle("GET /t/7/abc123/edit", func(ctltest.Call) ctltest.Answer { return ctltest.Answer{Body: h.editPage()} })
	h.fake.On("GET /o/40/abc123", http.StatusOK, `<a href="/t/7/abc123/edit">Endre</a> <a href="/t/7/abc123/edit">Endre</a>`)
	// Checkin answers a wrong code with a redirect to its front page.
	h.fake.Handle("GET /t/7/wrong1/edit", func(ctltest.Call) ctltest.Answer {
		return ctltest.Answer{Status: http.StatusFound, Header: http.Header{"Location": {"/"}}}
	})
	h.fake.Handle("POST /graphql", h.graphql)
	return h
}

// editPage is the edit page of the ticket, as much of it as the client reads.
func (h *harness) editPage() string {
	var page strings.Builder
	radio := func(group, option int, extra string) {
		checked := ""
		if h.chosen[group] == option {
			checked = ` checked="checked"`
		}
		fmt.Fprintf(&page, `<input type="radio" name="additionals[%d]"  id="additionals-%d_%d" value="%d"%s%s />`+"\n", group, group, option, option, checked, extra)
	}
	radio(activities, skiing, ` data-depencies="200"`)
	radio(activities, climbing, "")
	radio(activities, chess, "")
	// The level group is hidden, with nothing checked, unless Skiing is chosen.
	if h.chosen[activities] == skiing {
		radio(levels, beginner, "")
	} else {
		fmt.Fprintf(&page, `<input type="radio" name="additionals[%d]" value="%d" />`+"\n", levels, beginner)
	}
	page.WriteString(`<input type="text" name="additionals[300]" value="made up" />` + "\n")
	fmt.Fprintf(&page, "<script>graph.query(`($id: Int!) { event(id: $id) { categories { id } } }`, {\n id: %d }).then()</script>", eventID)
	return page.String()
}

func (h *harness) leftJSON(option int) string {
	if left, ok := h.left[option]; ok {
		return fmt.Sprint(left)
	}
	return "null"
}

// graphql answers the event query and the change, as reference/README.md
// describes them.
func (h *harness) graphql(c ctltest.Call) ctltest.Answer {
	var request struct {
		Query     string `json:"query"`
		Variables struct {
			ID          int              `json:"id"`
			TicketID    int              `json:"ticket_id"`
			Hash        string           `json:"hash"`
			Additionals []checkin.Change `json:"additionals"`
		} `json:"variables"`
	}
	if err := json.Unmarshal([]byte(c.Body), &request); err != nil {
		h.t.Errorf("graphql body = %q", c.Body)
	}

	if !strings.Contains(request.Query, "updateOldEventTicket") {
		if request.Variables.ID != eventID {
			return ctltest.Answer{Body: `{"errors":[{"message":"Cannot find event","extensions":{"code":"NOTFOUND"}}],"data":{"event":null}}`}
		}
		return ctltest.Answer{Body: fmt.Sprintf(`{"data":{"event":{"id":500,"name":"Winter Camp","organizer":"The Camp Club","start":"2026-12-28T16:00:00+01:00","end":"2027-01-01T10:00:00+01:00","opens":"2026-10-13T17:00:00+02:00","close":"2026-12-28T00:00:00+01:00","categories":[{"id":10,"name":"Main ticket","price":"3000","left":28,"additionals":[
			{"id":100,"name":"Activities","type":"radio","required":true,"triggered_by":[],"children":[
				{"id":101,"name":"Skiing","price":"0","left":%s,"waiting_list":false},
				{"id":102,"name":"Climbing","price":"700","left":%s,"waiting_list":false},
				{"id":103,"name":"Chess","price":"0","left":null,"waiting_list":false}]},
			{"id":200,"name":"Level","type":"radio","required":false,"triggered_by":[101],"children":[
				{"id":201,"name":"Beginner","price":"0","left":null,"waiting_list":false}]},
			{"id":300,"name":"Diet","type":"text","required":false,"triggered_by":[],"children":[]}]}]}}}`, h.leftJSON(skiing), h.leftJSON(climbing))}
	}

	if request.Variables.TicketID != ticketID || request.Variables.Hash != code || len(request.Variables.Additionals) != 1 {
		h.t.Errorf("change = %s", c.Body)
	}
	change := request.Variables.Additionals[0]
	warnings := "[]"
	if left, limited := h.left[change.Option]; limited && left <= 0 {
		warnings = fmt.Sprintf(`[{"id":%d,"type":"additional","warning":"full"}]`, change.Option)
	} else {
		h.chosen[change.Group] = change.Option
	}
	lines := []string{`{"id":1,"additional_id":300,"additional_parent_id":null}`}
	for group, option := range h.chosen {
		lines = append(lines, fmt.Sprintf(`{"id":2,"additional_id":%d,"additional_parent_id":%d}`, option, group))
	}
	return ctltest.Answer{Body: fmt.Sprintf(`{"data":{"updateOldEventTicket":{"additionals":[%s],"warnings":%s}}}`, strings.Join(lines, ","), warnings)}
}

// login stores the ticket as "kari".
func (h *harness) login() {
	h.t.Helper()
	h.store.Secret = fmt.Sprintf(`{"kari":{"ticket":%d,"code":%q,"event":%d}}`, ticketID, code, eventID)
}

// program returns checkinctl wired to the fake service.
func (h *harness) program() *ctlkit.Program {
	app := &App{
		Client:  &checkin.Client{HTTP: h.fake.Client(), BaseURL: h.fake.URL()},
		Tickets: h.store,
		Now:     func() time.Time { return h.now },
		Sleep: func(_ context.Context, d time.Duration) error {
			h.sleeps++
			h.now = h.now.Add(d)
			if h.onSleep != nil {
				h.onSleep()
			}
			return nil
		},
	}
	return app.program(ctlkit.Build{Version: "1.2.3"}, os.DirFS("../../skills/"+Name))
}

// run runs one command. h.fake.Calls holds what it sent.
func (h *harness) run(stdin string, args ...string) (code int, stdout, stderr string) {
	h.t.Helper()
	h.fake.Reset()
	return ctltest.Run(h.program(), stdin, args...)
}

// changes counts the changes that were sent. A read is a POST to the same
// path, so the body tells them apart.
func (h *harness) changes() int {
	n := 0
	for _, c := range h.fake.Calls {
		if c.Method != http.MethodGet && strings.Contains(c.Body, "updateOldEventTicket") {
			n++
		}
	}
	return n
}

func TestContract(t *testing.T) {
	ctltest.Contract(t, newHarness(t).program, "../../skills/"+Name+"/SKILL.md", "../../README.md")
}

// ctltest.ChangeNeedsAYes cannot be used here: it takes every request that
// is not a GET for a change, and a read at Checkin is a POST to /graphql.
func TestAChangeSendsNothingWithoutAYes(t *testing.T) {
	h := newHarness(t)
	h.login()
	code, stdout, stderr := h.run("", "choose", "kari", "100", "103", "--dry-run")
	if code != 0 || h.changes() != 0 || stdout != "Dry run. Would change \"Activities\" on ticket \"kari\" from \"Skiing\" to \"Chess\". Nothing was sent.\n\"Chess\" has no limit.\n" {
		t.Errorf("--dry-run: exit = %d, changes = %d, stdout = %q, stderr = %q", code, h.changes(), stdout, stderr)
	}

	code, _, stderr = h.run("", "choose", "kari", "100", "103")
	want := `refusing without confirmation: Change "Activities" on ticket "kari" from "Skiing" to "Chess"?`
	if code != 2 || !strings.Contains(stderr, want) || h.changes() != 0 || h.chosen[activities] != skiing {
		t.Errorf("without --yes: exit = %d, changes = %d, stderr = %q", code, h.changes(), stderr)
	}
}

func TestLoginStoresTheLinkAndNeverPrintsTheCode(t *testing.T) {
	h := newHarness(t)
	exit, stdout, stderr := h.run(link+"\n", "login", "kari", "--link-stdin")
	want := "Stored ticket 7 of event 500 as \"kari\". The link is in the system keyring.\n"
	if exit != 0 || stdout != want || stderr != "" || h.fake.Sent() != "GET /t/7/abc123/edit" {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q, sent = %s", exit, stdout, stderr, h.fake.Sent())
	}
	if h.store.Secret != `{"kari":{"ticket":7,"code":"abc123","event":500}}` {
		t.Errorf("stored = %s", h.store.Secret)
	}

	exit, stdout, _ = h.run("", "status")
	if exit != 0 || stdout != "# Stored tickets\n\n- kari\n  Ticket: 7\n  Event: 500\n" || len(h.fake.Calls) != 0 {
		t.Errorf("status: exit = %d, stdout = %q, sent = %s", exit, stdout, h.fake.Sent())
	}
	_, stdout, _ = h.run("", "status", "--json")
	var list storedTickets
	ctltest.OneJSON(t, stdout, &list)
	if strings.Contains(stdout, code) || len(list.Tickets) != 1 || list.Tickets[0] != (storedTicket{Name: "kari", TicketID: 7, EventID: 500}) {
		t.Errorf("status --json = %s", stdout)
	}
}

func TestLoginTakesAnOrderLink(t *testing.T) {
	h := newHarness(t)
	exit, _, stderr := h.run("https://app.checkin.no/o/40/abc123\n", "login", "kari", "--link-stdin")
	if exit != 0 || h.fake.Sent() != "GET /o/40/abc123, GET /t/7/abc123/edit" || !strings.Contains(h.store.Secret, `"ticket":7`) {
		t.Errorf("exit = %d, stderr = %q, sent = %s, stored = %s", exit, stderr, h.fake.Sent(), h.store.Secret)
	}

	h.fake.On("GET /o/40/abc123", http.StatusOK, `<a href="/t/7/abc123/edit">Endre</a><a href="/t/8/abc123/edit">Endre</a>`)
	if exit, _, stderr := h.run("https://app.checkin.no/o/40/abc123\n", "login", "ola", "--link-stdin"); exit != 2 || !strings.Contains(stderr, "the order has 2 tickets") {
		t.Errorf("two tickets: exit = %d, stderr = %q", exit, stderr)
	}
}

func TestLoginRefusesWhatIsNotAWorkingLink(t *testing.T) {
	h := newHarness(t)
	for stdin, want := range map[string]int{
		"https://example.invalid/somewhere\n":       2,
		"https://app.checkin.no/t/7/wrong1/edit\n":  3,
		"https://app.checkin.no/t/99/abc123/edit\n": 3,
	} {
		exit, _, stderr := h.run(stdin, "login", "kari", "--link-stdin")
		if exit != want || h.store.Secret != "" || strings.Contains(stderr, "wrong1") {
			t.Errorf("%q: exit = %d, want %d, stderr = %q, stored = %q", strings.TrimSpace(stdin), exit, want, stderr, h.store.Secret)
		}
	}
	if exit, _, stderr := h.run(link+"\n", "login", "Kari Nordmann", "--link-stdin"); exit != 2 || !strings.Contains(stderr, "cannot name a ticket") {
		t.Errorf("a bad name: exit = %d, stderr = %q", exit, stderr)
	}
	// The link is a secret, so it never comes as an argument.
	if exit, _, stderr := h.run("", "login", "kari"); exit != 2 || !strings.Contains(stderr, "--link-stdin") {
		t.Errorf("without stdin: exit = %d, stderr = %q", exit, stderr)
	}
}

func TestLogoutRemovesTheTicket(t *testing.T) {
	h := newHarness(t)
	h.login()
	if exit, stdout, _ := h.run("", "logout", "kari"); exit != 0 || stdout != "Removed the stored ticket \"kari\".\n" || h.store.Secret != "" {
		t.Errorf("exit = %d, stdout = %q, stored = %q", exit, stdout, h.store.Secret)
	}
	if exit, stdout, _ := h.run("", "status"); exit != 0 || stdout != "No ticket is stored. Run \"checkinctl login <name>\".\n" {
		t.Errorf("status: exit = %d, stdout = %q", exit, stdout)
	}
	if exit, _, _ := h.run("", "logout", "kari"); exit != 3 {
		t.Errorf("a second logout: exit = %d", exit)
	}
	_, stdout, _ := h.run("", "status", "--json")
	if got := strings.Join(strings.Fields(stdout), ""); got != `{"tickets":[]}` {
		t.Errorf("status --json = %s", got)
	}
}

func TestEvent(t *testing.T) {
	h := newHarness(t)
	exit, stdout, stderr := h.run("", "event", "500")
	want := "# Winter Camp\n\nid: 500\nOrganiser: The Camp Club\nStarts: 2026-12-28T16:00:00+01:00\nEnds: 2027-01-01T10:00:00+01:00\n" +
		"Registration opens: 2026-10-13T17:00:00+02:00\nRegistration closes: 2026-12-28T00:00:00+01:00\n\n## Ticket types\n\n" +
		"- Main ticket: 28 left\n  Price: 3000\n  Groups: 3\n  id: 10\n\nRun \"checkinctl options --event 500\" to see what a ticket can choose.\n"
	if exit != 0 || stdout != want || stderr != "" || h.fake.Sent() != "POST /graphql" {
		t.Errorf("exit = %d, stdout = %q, stderr = %q, sent = %s", exit, stdout, stderr, h.fake.Sent())
	}

	exit, stdout, _ = h.run("", "event", "999", "--json")
	var doc ctltest.ErrorDocument
	ctltest.OneJSON(t, stdout, &doc)
	if exit != 3 || doc.Error.Kind != "not_found" || doc.Error.Message != "Checkin: Cannot find event" {
		t.Errorf("unknown event: exit = %d, error = %+v", exit, doc.Error)
	}
	if exit, _, stderr := h.run("", "event", "camp"); exit != 2 || !strings.Contains(stderr, `event-id takes a number, not "camp"`) {
		t.Errorf("not a number: exit = %d, stderr = %q", exit, stderr)
	}
}

func TestOptionsAsMarkdown(t *testing.T) {
	h := newHarness(t)
	h.login()
	// With one stored ticket, its event is used.
	exit, stdout, stderr := h.run("", "options")
	want := "# Winter Camp\n\n## Activities\n\ngroup id: 100, type: radio\n\n" +
		"- Skiing: 5 left\n  id: 101\n- Climbing: full\n  Price: 700\n  id: 102\n- Chess: no limit\n  id: 103\n\n" +
		"## Level\n\ngroup id: 200, type: radio\nShows only with option 101\n\n- Beginner: no limit\n  id: 201\n"
	if exit != 0 || stdout != want || stderr != "" || h.fake.Sent() != "POST /graphql" {
		t.Errorf("exit = %d, stdout = %q, stderr = %q, sent = %s", exit, stdout, stderr, h.fake.Sent())
	}
}

func TestOptionsFiltersAndSaysWhenTheListIsCutOff(t *testing.T) {
	h := newHarness(t)
	var list struct {
		Items     []map[string]any `json:"items"`
		Truncated bool             `json:"truncated"`
	}

	exit, stdout, _ := h.run("", "options", "--event", "500", "--group", "100", "--available", "--json", "--fields", "id,left")
	ctltest.OneJSON(t, stdout, &list)
	if exit != 0 || list.Truncated || len(list.Items) != 2 || list.Items[0]["id"] != float64(skiing) || list.Items[0]["left"] != float64(5) || list.Items[1]["left"] != nil || len(list.Items[1]) != 2 {
		t.Errorf("exit = %d, list = %+v", exit, list)
	}

	exit, stdout, _ = h.run("", "options", "--event", "500", "--limit", "1", "--json")
	ctltest.OneJSON(t, stdout, &list)
	if exit != 0 || !list.Truncated || len(list.Items) != 1 {
		t.Errorf("--limit 1: exit = %d, list = %+v", exit, list)
	}
	if _, stdout, _ = h.run("", "options", "--event", "500", "--limit", "1"); !strings.HasSuffix(stdout, "\nThere are more. Raise --limit, or narrow the list with --group or --available.\n") {
		t.Errorf("--limit 1: stdout = %q", stdout)
	}

	if exit, _, stderr := h.run("", "options", "--event", "500", "--group", "900"); exit != 3 || !strings.Contains(stderr, "event 500 has no group with the id 900") {
		t.Errorf("unknown group: exit = %d, stderr = %q", exit, stderr)
	}
	if exit, _, stderr := h.run("", "options"); exit != 2 || !strings.Contains(stderr, "name the event with --event or --ticket") || len(h.fake.Calls) != 0 {
		t.Errorf("no event: exit = %d, stderr = %q", exit, stderr)
	}
	if exit, _, _ := h.run("", "options", "--ticket", "kari"); exit != 4 {
		t.Errorf("no stored ticket: exit = %d", exit)
	}
}

func TestTicketShowsWhatItHasChosen(t *testing.T) {
	h := newHarness(t)
	h.login()
	h.chosen[levels] = beginner
	exit, stdout, stderr := h.run("", "ticket", "kari")
	want := "# Ticket kari\n\nEvent: Winter Camp (500)\nTicket: 7\n\n## Chosen\n\n" +
		"- Activities: Skiing\n  group id: 100\n  option id: 101\n- Level: Beginner\n  group id: 200\n  option id: 201\n"
	if exit != 0 || stdout != want || stderr != "" || h.fake.Sent() != "GET /t/7/abc123/edit, POST /graphql" {
		t.Errorf("exit = %d, stdout = %q, stderr = %q, sent = %s", exit, stdout, stderr, h.fake.Sent())
	}
	// The text field of the page is never read.
	if _, stdout, _ = h.run("", "ticket", "kari", "--json"); strings.Contains(stdout, "made up") || strings.Contains(stdout, code) {
		t.Errorf("ticket --json = %s", stdout)
	}

	if exit, _, stderr := h.run("", "ticket", "ola"); exit != 3 || !strings.Contains(stderr, `no ticket is stored as "ola"`) {
		t.Errorf("unknown name: exit = %d, stderr = %q", exit, stderr)
	}
}

func TestChooseChangesTheTicket(t *testing.T) {
	h := newHarness(t)
	h.login()
	exit, stdout, stderr := h.run("", "choose", "kari", "100", "103", "--yes")
	want := "Changed \"Activities\" on ticket \"kari\" from \"Skiing\" to \"Chess\".\n\n## The ticket has now\n\n- Activities: Chess\n  group id: 100\n  option id: 103\n"
	if exit != 0 || stdout != want || stderr != "" || h.chosen[activities] != chess {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q, chosen = %v", exit, stdout, stderr, h.chosen)
	}
	// Look before, send once, look after.
	if h.fake.Sent() != "GET /t/7/abc123/edit, POST /graphql, POST /graphql, GET /t/7/abc123/edit" || h.changes() != 1 {
		t.Errorf("sent = %s, changes = %d", h.fake.Sent(), h.changes())
	}
	var sent struct {
		Variables map[string]json.RawMessage `json:"variables"`
	}
	if err := json.Unmarshal([]byte(h.fake.Calls[2].Body), &sent); err != nil || string(sent.Variables["additionals"]) != `[{"mode":"update","id":100,"child_id":103,"waiting":false}]` {
		t.Errorf("change = %s", h.fake.Calls[2].Body)
	}

	// Running it again sends nothing.
	exit, _, stderr = h.run("", "choose", "kari", "100", "103", "--yes")
	if exit != 5 || !strings.Contains(stderr, `ticket "kari" already has "Chess" in "Activities"`) || h.changes() != 0 {
		t.Errorf("again: exit = %d, stderr = %q, changes = %d", exit, stderr, h.changes())
	}
}

func TestADryRunPrintsTheRequestWithoutTheCode(t *testing.T) {
	h := newHarness(t)
	h.login()
	exit, stdout, _ := h.run("", "choose", "kari", "200", "201", "--dry-run", "--json")
	var result chooseResult
	ctltest.OneJSON(t, stdout, &result)
	want := checkin.Change{Mode: "add", Group: levels, Option: beginner}
	if exit != 0 || !result.DryRun || result.From != nil || result.To.Name != "Beginner" || result.Request.TicketID != ticketID ||
		len(result.Request.Additionals) != 1 || result.Request.Additionals[0] != want || strings.Contains(stdout, code) || h.changes() != 0 {
		t.Errorf("exit = %d, stdout = %s", exit, stdout)
	}
}

func TestAFullOptionIsRefusedAndTheTicketKeepsWhatItHad(t *testing.T) {
	h := newHarness(t)
	h.login()
	if _, stdout, _ := h.run("", "choose", "kari", "100", "102", "--dry-run"); !strings.Contains(stdout, `"Climbing" is full now, so the change would be refused.`) {
		t.Errorf("--dry-run of a full option: %q", stdout)
	}
	// The event says it is full, so nothing is sent.
	exit, _, stderr := h.run("", "choose", "kari", "100", "102", "--yes")
	if exit != 5 || !strings.Contains(stderr, `"Climbing" is full`) || !strings.Contains(stderr, "--wait") || h.changes() != 0 || h.chosen[activities] != skiing {
		t.Errorf("exit = %d, stderr = %q, changes = %d", exit, stderr, h.changes())
	}

	// The event says there is a place, and somebody else takes it first.
	h.left[skiing] = 0
	h.chosen[activities] = chess
	h.fake.Handle("POST /graphql", func(c ctltest.Call) ctltest.Answer {
		if !strings.Contains(c.Body, "updateOldEventTicket") {
			h.left[skiing] = 1
			defer func() { h.left[skiing] = 0 }()
		}
		return h.graphql(c)
	})
	exit, _, stderr = h.run("", "choose", "kari", "100", "101", "--yes")
	if exit != 5 || !strings.Contains(stderr, `"Skiing" is full`) || h.changes() != 1 || h.chosen[activities] != chess {
		t.Errorf("lost the race: exit = %d, stderr = %q, changes = %d, chosen = %v", exit, stderr, h.changes(), h.chosen)
	}
}

func TestChooseWaitsForAPlace(t *testing.T) {
	h := newHarness(t)
	h.login()
	h.onSleep = func() {
		if h.sleeps == 3 {
			h.left[climbing] = 1
		}
	}
	exit, stdout, stderr := h.run("", "choose", "kari", "100", "102", "--yes", "--wait", "10m", "--interval", "30s", "--json")
	var result chooseResult
	ctltest.OneJSON(t, stdout, &result)
	if exit != 0 || h.sleeps != 3 || h.changes() != 1 || result.Attempts != 1 || h.chosen[activities] != climbing || len(result.Choices) != 1 || result.Choices[0].Option != "Climbing" {
		t.Errorf("exit = %d, sleeps = %d, changes = %d, stdout = %s, stderr = %q", exit, h.sleeps, h.changes(), stdout, stderr)
	}
}

func TestChooseGivesUpWhenTheWaitIsOver(t *testing.T) {
	h := newHarness(t)
	h.login()
	exit, _, stderr := h.run("", "choose", "kari", "100", "102", "--yes", "--wait", "1m", "--interval", "20s")
	if exit != 5 || h.sleeps != 3 || h.changes() != 0 || !strings.Contains(stderr, `"Climbing" is full`) {
		t.Errorf("exit = %d, sleeps = %d, changes = %d, stderr = %q", exit, h.sleeps, h.changes(), stderr)
	}

	for _, args := range [][]string{{"--wait", "soon"}, {"--interval", "1s"}, {"--wait", "48h"}} {
		if exit, _, _ := h.run("", append([]string{"choose", "kari", "100", "102", "--yes"}, args...)...); exit != 2 || len(h.fake.Calls) != 0 {
			t.Errorf("%v: exit = %d, sent = %s", args, exit, h.fake.Sent())
		}
	}
}

func TestAChangeWithoutAnAnswerIsNeverSentAgain(t *testing.T) {
	h := newHarness(t)
	h.login()
	fail := func(made bool) {
		h.fake.Handle("POST /graphql", func(c ctltest.Call) ctltest.Answer {
			if !strings.Contains(c.Body, "updateOldEventTicket") {
				return h.graphql(c)
			}
			if made {
				h.graphql(c)
			}
			return ctltest.Answer{Status: http.StatusBadGateway}
		})
	}

	fail(false)
	exit, _, stderr := h.run("", "choose", "kari", "100", "103", "--yes")
	if exit != 7 || h.changes() != 1 || !strings.Contains(stderr, "the change was not made") {
		t.Errorf("not made: exit = %d, changes = %d, stderr = %q", exit, h.changes(), stderr)
	}

	fail(true)
	exit, stdout, stderr := h.run("", "choose", "kari", "100", "103", "--yes")
	if exit != 0 || h.changes() != 1 || !strings.HasPrefix(stdout, "Changed ") || !strings.Contains(stderr, "the ticket shows that it was made") {
		t.Errorf("made: exit = %d, changes = %d, stdout = %q, stderr = %q", exit, h.changes(), stdout, stderr)
	}
}

func TestChooseRefusesWhatCannotBeChosen(t *testing.T) {
	h := newHarness(t)
	h.login()
	h.chosen[activities] = chess
	for want, args := range map[string][]string{
		"does not apply to what the ticket has chosen": {"200", "201"},
		"only radio groups can be chosen":              {"300", "301"},
		"has no group with the id 900":                 {"900", "1"},
		"has no option with the id 199":                {"100", "199"},
		`group-id takes a number, not "activities"`:    {"activities", "101"},
	} {
		exit, _, stderr := h.run("", append([]string{"choose", "kari"}, append(args, "--yes")...)...)
		if exit == 0 || !strings.Contains(stderr, want) || h.changes() != 0 {
			t.Errorf("%v: exit = %d, stderr = %q, changes = %d", args, exit, stderr, h.changes())
		}
	}
}

func TestChooseThenMakesASecondChoice(t *testing.T) {
	h := newHarness(t)
	h.login()
	h.chosen[activities] = chess

	exit, stdout, _ := h.run("", "choose", "kari", "100", "101", "--then", "200:201", "--dry-run")
	want := "Dry run. Would change \"Activities\" on ticket \"kari\" from \"Chess\" to \"Skiing\". Nothing was sent.\nWould then choose \"Beginner\" in \"Level\".\n\"Skiing\" has 5 left now.\n"
	if exit != 0 || stdout != want || h.changes() != 0 {
		t.Errorf("--dry-run: exit = %d, stdout = %q, changes = %d", exit, stdout, h.changes())
	}
	if exit, _, stderr := h.run("", "choose", "kari", "100", "101", "--then", "200:201"); exit != 2 || !strings.Contains(stderr, `to "Skiing", and then choose "Beginner" in "Level"?`) || h.changes() != 0 {
		t.Errorf("without --yes: exit = %d, stderr = %q", exit, stderr)
	}

	exit, stdout, stderr := h.run("", "choose", "kari", "100", "101", "--then", "200:201", "--yes")
	if exit != 0 || h.changes() != 2 || h.chosen[activities] != skiing || h.chosen[levels] != beginner ||
		!strings.Contains(stdout, "Then chose \"Beginner\" in \"Level\".\n") || !strings.Contains(stdout, "- Level: Beginner\n") {
		t.Fatalf("exit = %d, changes = %d, chosen = %v, stdout = %q, stderr = %q", exit, h.changes(), h.chosen, stdout, stderr)
	}
	var sent struct {
		Variables map[string]json.RawMessage `json:"variables"`
	}
	last := h.fake.Calls[len(h.fake.Calls)-2]
	if err := json.Unmarshal([]byte(last.Body), &sent); err != nil || string(sent.Variables["additionals"]) != `[{"mode":"add","id":200,"child_id":201,"waiting":false}]` {
		t.Errorf("second change = %s", last.Body)
	}
}

func TestChooseThenIsCheckedBeforeAnythingIsSent(t *testing.T) {
	h := newHarness(t)
	h.login()
	for want, then := range map[string]string{
		"does not apply after the change":                 "200:201",
		"takes <group-id>:<option-id>":                    "200",
		"the event has no such group and option":          "200:999",
		"is already being changed by this command":        "100:101",
		"only radio groups can be chosen":                 "300:301",
		"takes <group-id>:<option-id>, not \"level:pro\"": "level:pro",
	} {
		exit, _, stderr := h.run("", "choose", "kari", "100", "103", "--then", then, "--yes")
		if exit == 0 || !strings.Contains(stderr, want) || h.changes() != 0 || h.chosen[activities] != skiing {
			t.Errorf("--then %s: exit = %d, stderr = %q, changes = %d", then, exit, stderr, h.changes())
		}
	}
}

func TestARefusedSecondChoiceSaysThatTheFirstStays(t *testing.T) {
	h := newHarness(t)
	h.login()
	h.chosen[activities] = chess
	h.left[beginner] = 0
	exit, _, stderr := h.run("", "choose", "kari", "100", "101", "--then", "200:201", "--yes")
	if exit != 5 || h.chosen[activities] != skiing || h.changes() != 2 ||
		!strings.Contains(stderr, `"Activities" was changed to "Skiing", but the choice of "Beginner" in "Level" was not made`) || !strings.Contains(stderr, "the first change stays") {
		t.Errorf("exit = %d, chosen = %v, changes = %d, stderr = %q", exit, h.chosen, h.changes(), stderr)
	}
}
