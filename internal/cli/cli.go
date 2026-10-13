// Package cli implements the checkinctl command line.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"time"

	"github.com/langtind/ctlkit"
	"github.com/langtind/ctlkit/keystore"

	"github.com/langtind/checkinctl/internal/checkin"
)

const (
	// Name is the program name. A rename also touches the module path, the
	// skill directory with its embed line in skill_embed.go, and the docs.
	Name = "checkinctl"

	summary = "see what an event at Checkin has room for, and change what a ticket has chosen"
)

// App holds what the commands work with. Tests replace the fields to point
// checkinctl at a fake service and at a keyring in memory.
type App struct {
	Client *checkin.Client
	// Tickets holds the ticket links, each under a name the user gave it.
	Tickets keystore.Store
	Now     func() time.Time
	// Sleep waits between two looks at a full option, see choose --wait.
	Sleep func(ctx context.Context, d time.Duration) error
}

// New returns checkinctl as it runs for real.
func New(build ctlkit.Build, skill fs.FS) *ctlkit.Program {
	app := &App{
		Client:  checkin.NewClient(),
		Tickets: keystore.Keyring(Name, "tickets"),
		Now:     time.Now,
		Sleep:   sleep,
	}
	return app.program(build, skill)
}

func (a *App) program(build ctlkit.Build, skill fs.FS) *ctlkit.Program {
	p := ctlkit.New(Name, summary, build)
	p.URL = "https://github.com/langtind/" + Name
	p.Skill = skill
	p.Commands = a.commands()
	a.Client.UserAgent = p.UserAgent()
	return p
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// A ticket link is the whole login to a ticket at Checkin, so the links are
// kept in the system keyring, as one JSON document, and never in a file.

var ticketName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

func checkName(name string) error {
	if !ticketName.MatchString(name) {
		return ctlkit.Usagef("%q cannot name a ticket", name).
			WithHint("use lower-case letters, digits, - and _, such as kari or kari-2027.")
	}
	return nil
}

func (a *App) tickets() (map[string]checkin.Ticket, error) {
	tickets := map[string]checkin.Ticket{}
	secret, err := a.Tickets.Get()
	if errors.Is(err, keystore.ErrNotFound) || (err == nil && secret == "") {
		return tickets, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the tickets from the keyring: %w", err)
	}
	if err := json.Unmarshal([]byte(secret), &tickets); err != nil {
		return nil, fmt.Errorf("the tickets in the keyring cannot be read: %w", err)
	}
	return tickets, nil
}

func (a *App) saveTickets(tickets map[string]checkin.Ticket) error {
	if len(tickets) == 0 {
		if err := a.Tickets.Delete(); err != nil && !errors.Is(err, keystore.ErrNotFound) {
			return fmt.Errorf("remove the tickets from the keyring: %w", err)
		}
		return nil
	}
	secret, err := json.Marshal(tickets)
	if err != nil {
		return err
	}
	if err := a.Tickets.Set(string(secret)); err != nil {
		return fmt.Errorf("store the ticket in the keyring: %w", err)
	}
	return nil
}

func names(tickets map[string]checkin.Ticket) []string {
	list := make([]string, 0, len(tickets))
	for name := range tickets {
		list = append(list, name)
	}
	sort.Strings(list)
	return list
}

// ticket finds a stored ticket by its name.
func (a *App) ticket(name string) (checkin.Ticket, error) {
	tickets, err := a.tickets()
	if err != nil {
		return checkin.Ticket{}, err
	}
	ticket, ok := tickets[name]
	if !ok {
		if len(tickets) == 0 {
			return checkin.Ticket{}, ctlkit.Authf("no ticket is stored").
				WithHint(`run "%s login <name>" and paste the link from the confirmation email.`, Name)
		}
		return checkin.Ticket{}, ctlkit.NotFoundf("no ticket is stored as %q", name).
			WithHint(`run "%s status" to see the names.`, Name)
	}
	return ticket, nil
}
