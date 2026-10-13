# checkinctl

A command line tool for a ticket at [Checkin](https://www.checkinevent.com), the registration service many organisers use. It shows how many places every option of an event has left, shows what a ticket has chosen, and changes a choice, also by waiting until a full option gets a free place.

The output is Markdown, so it reads well in a terminal and when an AI agent relays it into a chat. Every data command also prints JSON.

> checkinctl is unofficial and not affiliated with Checkin. It reads the pages and requests a participant's own browser uses, so it can break when the service changes. Checkin's official API is for organisers.

## Install

```bash
brew install langtind/tap/checkinctl
```

Or with Go:

```bash
go install github.com/langtind/checkinctl@latest
```

## Use

Checkin has no account for a participant. The link in the confirmation email is the whole login, so checkinctl keeps it in the system keyring under a name you give it:

```bash
checkinctl login kari          # asks for the link
checkinctl status
```

The link is the one behind "Klikk her for å se hele bestillingen" in the email, or the one behind "Endre" on that page. In a script, pipe it in, and never pass it as an argument:

```bash
op read "op://vault/item/link" | checkinctl login kari --link-stdin
```

Then:

```bash
checkinctl ticket kari                         # what the ticket has chosen
checkinctl options --ticket kari --available   # what can be chosen now
checkinctl choose kari <group-id> <option-id> --dry-run
checkinctl choose kari <group-id> <option-id> --yes
checkinctl choose kari <group-id> <option-id> --yes --wait 6h
checkinctl choose kari <group-id> <option-id> --then <group-id>:<option-id> --yes
```

A change is stored at once, and the place the ticket had goes to others. A full option is refused and the ticket keeps what it had. With `--wait`, the command looks again every ten seconds and makes the change when a place opens. With `--then`, it makes a second choice right after the first, such as the level of a new activity.

An event can be looked at without a ticket:

```bash
checkinctl event <event-id>
checkinctl options --event <event-id>
```

## For agents

```bash
checkinctl install-skill   # copies the skill to ~/.claude/skills
checkinctl schema          # every command, flag and error kind as JSON
```

Add `--json` to a data command for one JSON document on stdout, and `--fields id,name` to keep only some fields. A failed command exits with a code that says why: 2 wrong call, 3 not found, 4 no ticket stored, 5 full or already chosen, 7 service unavailable. With `--json` the error is a JSON document too.

## Develop

```bash
go build -o checkinctl .
make check
```

`reference/README.md` holds Checkin as it was observed, and what is verified.

## License

MIT
