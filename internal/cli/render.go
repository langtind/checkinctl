package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/langtind/checkinctl/internal/checkin"
)

// Output is Markdown, which reads well both in a terminal and when an agent
// relays it into a chat. What the service returned is printed on one line,
// so that a line break in a name cannot pass for structure.

func renderStatus(w io.Writer, list storedTickets) {
	if len(list.Tickets) == 0 {
		fmt.Fprintf(w, "No ticket is stored. Run \"%s login <name>\".\n", Name)
		return
	}
	fmt.Fprintln(w, "# Stored tickets")
	fmt.Fprintln(w)
	for _, ticket := range list.Tickets {
		fmt.Fprintf(w, "- %s\n  Ticket: %d\n  Event: %d\n", ticket.Name, ticket.TicketID, ticket.EventID)
	}
}

// places says how many places are left, in words.
func places(left *int) string {
	switch {
	case left == nil:
		return "no limit"
	case *left <= 0:
		return "full"
	default:
		return fmt.Sprintf("%d left", *left)
	}
}

func renderEvent(w io.Writer, event *checkin.Event) {
	fmt.Fprintf(w, "# %s\n\n", oneLine(event.Name))
	fmt.Fprintf(w, "id: %d\n", event.ID)
	line(w, "Organiser", event.Organizer)
	line(w, "Starts", event.Start)
	line(w, "Ends", event.End)
	line(w, "Registration opens", event.Opens)
	line(w, "Registration closes", event.Closes)
	if len(event.Categories) == 0 {
		return
	}
	fmt.Fprintln(w, "\n## Ticket types")
	fmt.Fprintln(w)
	for _, category := range event.Categories {
		fmt.Fprintf(w, "- %s: %s\n", oneLine(category.Name), places(category.Left))
		field(w, "Price", category.Price)
		fmt.Fprintf(w, "  Groups: %d\n  id: %d\n", len(category.Groups), category.ID)
	}
	fmt.Fprintf(w, "\nRun \"%s options --event %d\" to see what a ticket can choose.\n", Name, event.ID)
}

func renderOptions(w io.Writer, list optionList) {
	fmt.Fprintf(w, "# %s\n", oneLine(list.Event))
	if len(list.Items) == 0 {
		fmt.Fprintln(w, "\nNo options.")
		return
	}
	group := 0
	for _, item := range list.Items {
		if item.GroupID != group {
			group = item.GroupID
			fmt.Fprintf(w, "\n## %s\n\ngroup id: %d, type: %s\n", oneLine(item.Group), item.GroupID, item.Type)
			if len(item.ShownBy) > 0 {
				fmt.Fprintf(w, "Shows only with option %s\n", ids(item.ShownBy))
			}
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "- %s: %s\n", oneLine(item.Name), places(item.Left))
		if item.Price != "" && item.Price != "0" {
			field(w, "Price", item.Price)
		}
		fmt.Fprintf(w, "  id: %d\n", item.ID)
	}
	if list.Truncated {
		fmt.Fprintln(w, "\nThere are more. Raise --limit, or narrow the list with --group or --available.")
	}
}

func renderChoices(w io.Writer, list []choice) {
	for _, item := range list {
		fmt.Fprintf(w, "- %s: %s\n  group id: %d\n  option id: %d\n", oneLine(item.Group), oneLine(item.Option), item.GroupID, item.OptionID)
	}
}

func renderTicket(w io.Writer, view ticketView) {
	fmt.Fprintf(w, "# Ticket %s\n\n", view.Name)
	fmt.Fprintf(w, "Event: %s (%d)\nTicket: %d\n", oneLine(view.Event), view.EventID, view.TicketID)
	if len(view.Choices) == 0 {
		fmt.Fprintln(w, "\nThe ticket has chosen nothing.")
		return
	}
	fmt.Fprintln(w, "\n## Chosen")
	fmt.Fprintln(w)
	renderChoices(w, view.Choices)
}

func renderChoose(w io.Writer, result chooseResult) {
	from := "nothing"
	if result.From != nil {
		from = fmt.Sprintf("%q", oneLine(result.From.Name))
	}
	change := fmt.Sprintf("%q on ticket %q from %s to %q", oneLine(result.Group.Name), result.Ticket, from, oneLine(result.To.Name))
	if result.DryRun {
		fmt.Fprintf(w, "Dry run. Would change %s. Nothing was sent.\n", change)
		for _, step := range result.Then {
			fmt.Fprintf(w, "Would then choose %q in %q.\n", oneLine(step.To.Name), oneLine(step.Group.Name))
		}
		switch {
		case result.Left == nil:
			fmt.Fprintf(w, "%q has no limit.\n", oneLine(result.To.Name))
		case *result.Left <= 0:
			fmt.Fprintf(w, "%q is full now, so the change would be refused. Give --wait to choose it when a place opens.\n", oneLine(result.To.Name))
		default:
			fmt.Fprintf(w, "%q has %d left now.\n", oneLine(result.To.Name), *result.Left)
		}
		return
	}
	fmt.Fprintf(w, "Changed %s.\n", change)
	for _, step := range result.Then {
		fmt.Fprintf(w, "Then chose %q in %q.\n", oneLine(step.To.Name), oneLine(step.Group.Name))
	}
	if len(result.Choices) > 0 {
		fmt.Fprintln(w, "\n## The ticket has now")
		fmt.Fprintln(w)
		renderChoices(w, result.Choices)
	}
}

func line(w io.Writer, label, value string) {
	if value != "" {
		fmt.Fprintf(w, "%s: %s\n", label, oneLine(value))
	}
}

// field writes one indented line under a list item, and nothing for an
// empty value.
func field(w io.Writer, label, value string) {
	if value != "" {
		fmt.Fprintf(w, "  %s: %s\n", label, oneLine(value))
	}
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
