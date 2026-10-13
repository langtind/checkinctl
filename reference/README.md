# Reference

Checkin (`https://app.checkin.no`) as it was observed. The client in `internal/checkin` is written from this directory and never from a guess. Ids, names and codes below are made up.

Checkin is a registration service that many organisers use. A person who has registered gets a link by email. The link is the whole login: whoever holds it can read and change the ticket. There is no account, no password and no token for a participant.

## Verified

Observed 2026-10-13 with one ticket at one event. Shapes only: no answer is kept here.

### The links

| Link | What it is |
| --- | --- |
| `https://app.checkin.no/o/<order-id>/<code>` | The order page, "Klikk her for å se hele bestillingen" in the confirmation email. It holds one link to `/t/<ticket-id>/<code>/edit` for each ticket of the order, once the organiser has opened for changes |
| `https://app.checkin.no/t/<ticket-id>/<code>/edit` | The page where the ticket's choices are changed |

The code was six characters, and the same in both links of one order. A wrong code and an unknown ticket id both answer `302` to `https://app.checkin.no/`, so the two cannot be told apart.

Both pages are HTML, rendered on the server. A request with the tool's own user agent got the same page as a browser.

### The edit page

`GET /t/<ticket-id>/<code>/edit`, HTML. What the client reads from it:

- Every choice is an `<input type="radio" name="additionals[<group-id>]" value="<option-id>">`, with `checked="checked"` on the one the ticket has. The attribute `data-depencies="<group-id>"` (spelled so) names a group that shows only when that option is chosen.
- The event id stands in a script on the page, as the variable of the query below: `event(id: $id)` and later `id: <event-id>`.
- A group that depends on an option the ticket does not have is hidden with `style="display:none"`, and the page marks nothing in it as checked, even when the ticket still holds a choice there (see "A choice in a hidden group" below).

### GraphQL

`POST /graphql` with `Content-Type: application/json` and `{"query": ..., "variables": ...}`. No login for the requests below. Introspection is open (`{ __schema { ... } }`), which is where the type names come from.

An error is HTTP 200 with `{"errors": [{"message": "...", "path": [...], "extensions": {"code": "NOTFOUND"}}], "data": {...}}`. An unknown event id gave the message `Cannot find event` and the code `NOTFOUND`.

**The event, with every choice and how many places each has left**

```graphql
query ($id: Int!) {
  event(id: $id) {
    id name organizer start end opens close total_left
    categories {
      id name price left waiting_list
      additionals {
        id name type required price left waiting_list triggered_by
        children { id name price left waiting_list }
      }
    }
  }
}
```

- `categories` are the ticket types. `additionals` are the questions of a ticket type, called groups in this tool. `children` are the options of a group.
- `type` was `radio`, `select`, `checkbox` or `text`. Only `radio` and `select` had children.
- `left` on an option is the number of places left, `0` when it is full, and `null` when the option has no limit. `left` on a group was `null`.
- `available` on an option is not the capacity: an option with 186 left had `available` 50. What it means is not known, and the client does not ask for it.
- `triggered_by` on a group lists the option ids that make the group show. It was empty for a group that always shows.
- `price` is a string, `"0"` or an amount such as `"700"` or `"-400"`.
- Times are RFC 3339 with an offset: `2026-12-28T16:00:00+01:00`.
- The order of options is the order the page shows them in. The whole event came in one answer of 8 kB. There is no paging.

**Changing a choice**

```graphql
mutation ($ticket_id: Int!, $hash: String!, $additionals: [AdditionalUpdateInput]) {
  updateOldEventTicket(ticket_id: $ticket_id, hash: $hash, additionals: $additionals) {
    additionals { id additional_id additional_parent_id }
    warnings { id type warning }
  }
}
```

with `additionals: [{"mode": "update", "id": <group-id>, "child_id": <option-id>, "waiting": false}]`. This is what the page sends when "Velg" is pressed on a radio option. `mode` is `add` when the ticket has no choice in the group, `update` when it has one, and `delete` to remove one ("Meld av"). The change is stored at once. There is no confirm step and no save button for a choice.

| What was sent | Answer |
| --- | --- |
| `update` to an option with places left | `warnings: []`, and `additionals` lists the new option under the group. The page showed the new choice at once |
| `update` back to the first option | The same |
| `update` to an option with `left: 0` | `warnings: [{"id": <option-id>, "type": "additional", "warning": "full"}]`, and `additionals` still lists the old option. **The ticket kept what it had** |

`additionals` in the answer is every line of the ticket, text fields included, each with its own line id, the `additional_id` (the option, or the group for a line without options) and `additional_parent_id` (the group, or `null`).

**A choice in a hidden group.** A level group shows only for one activity. After the activity was changed, the answer still listed the level of the old activity, and the edit page showed that group as hidden with nothing checked. When the activity was changed back, the level showed again. So the service keeps a choice in a group that no longer applies.

### Running a change twice

A refused change (`full`) changes nothing, so it can be tried again. What a second `update` to the option the ticket already has does was not tried. The tool looks first and sends nothing in that case.

## Reported by others

Read 2026-10-13 in Checkin's own `https://checkinevent.com/hubfs/llms.txt`:

- Checkin has an official API, built on GraphQL, for organisers. Its documentation is not public: "API access is available to enterprise clients", and documentation and credentials are requested through `checkinevent.com/support`.
- Checkin has an MCP server for organisers, `https://mcp.checkinevent.com`, with nine tools for finding events, attendees and orders and for reports. It takes a bearer token of the organisation.
- A Zapier integration, also for organisers.

All three are for the organiser's own data and need the organiser's credentials. Nothing was found for a participant who holds a ticket link, which is what this tool is for. No open client was found. The catalogue page `jentic.com/apis/checkin-no` describes the same GraphQL endpoint at second hand.

## Read out of the web client, not tried

- Warnings the page's script handles besides `full`: `already_exists` ("Alternativet er allerede valgt"), `full_waiting_available` (the option is full and has a waiting list, and the page asks before it sends the same change with `"waiting": true`), and `not_available`.
- `mode: "add"` for a group the ticket has no choice in, and `mode: "delete"` for "Meld av".
- Text and number fields are changed with the same mutation, with `text` or `number` in place of `child_id`. They hold what the participant wrote about health and next of kin, and the tool leaves them alone.
- `waiting_list` was `false` on every option of the event that was looked at, so no waiting list was seen.

## Unverified

- How long the organiser keeps the edit page open, and whether the link stops working then.
- Whether there is a limit to how many times a choice can be changed.
- Whether `updateOldEventTicket` with an empty `additionals` list is a safe way to read a ticket. The tool reads the edit page instead.
- `findEventRegistrationOrder(token: String!)` and the mutations next to it in the schema look like a newer way to change an order. Which token they take is not known.
- Rate limits. One request every ten seconds for an hour was answered every time.
