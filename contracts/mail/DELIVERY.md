# Mail Delivery Contract

Part of the [Mailbox Contract](../MAILBOXES.md). Defines templates, contacts, contact lists, broadcasts, unsubscribes, send lane usage, programmatic sending, developer access, transactional mail, suppressions, delivery events, webhooks, and inbound delivery. See [`../MAILBOXES.md`](../MAILBOXES.md) for activation, ownership, route overview, and the master error catalog.

## Templates

A template is reusable content of an organization: a `name`, a `subject`, and an `html` and `text`
body, at least one of which is not empty. A member with the `templates` grant edits the
organization's standard templates (transactional templates below need an owner or admin); reading
templates needs the `templates`, `broadcasts`, or `api_keys` grant (see
[Member Feature Grants](INBOX.md#member-feature-grants)). An organization keeps at most 200, and names are
unique within it.

Placeholders may appear in the subject and both bodies:

| Form | Meaning |
|---|---|
| `{{name}}` | The value of `name`. A send without it is refused. |
| `{{name\|fallback}}` | The value of `name`, or `fallback` when it is absent or empty. |

A name is a letter followed by letters, digits, or underscores, at most 64 characters. Spaces
inside the braces are ignored. Any `{{` that does not open such a placeholder makes the template
`422 MAILBOX.TEMPLATE_INVALID`, so a typo is caught when the template is saved. There are no
conditionals, loops, or helpers.

Values are always escaped in the HTML body, so a value cannot add markup; there is no unescaped
form. In the subject, line breaks inside a value become spaces. The text body uses values as they
are.

`sample_variables` holds example values for the editor. They are used by previews and test sends
and never by a real send.

`design` holds the visual editor's document, an object with a positive integer `version`, at most
256 KiB. The console renders `html` and `text` from it before saving; the API stores it as given,
never renders it, and always sends the bodies. A response has `design: null` for a template written
in HTML, and a replace without `design` clears it. Anything else in `design` is
`422 MAILBOX.TEMPLATE_INVALID`.

| Route | Result |
|---|---|
| `GET /v1/mail-templates` | Every template by name, with `variables` and without bodies |
| `POST /v1/mail-templates` | `201` with the template |
| `GET`, `PUT`, `DELETE /v1/mail-templates/{id}` | Read, replace, or delete one template |
| `POST /v1/mail-template-previews` | Renders `template_id` or an unsaved `source`, with `variables` over the sample values; returns `subject`, `html_body`, `text_body`, `variables`, and `missing`. Sends nothing. |
| `POST /v1/mail-templates/{id}/tests` | Renders with `variables` over the sample values and sends it from `mailbox_id` to 1 to 5 addresses in `to`, like any send; needs `send` on that mailbox |

Sending with `template` renders the stored template with the given `variables` only. If a variable
without a fallback has no value, the send is `422 MAILBOX.TEMPLATE_VARIABLES_MISSING` with one field
error per variable, named `template.variables.<name>`, and nothing is stored or sent. The message
stores the rendered content and `template_id`; editing or deleting the template later does not change
it. The idempotency fingerprint covers the rendered content.

A template of another organization is `404 MAILBOX.TEMPLATE_NOT_FOUND`.

### Transactional templates

Every template has a `kind`: `standard` (the default, everything above) or `transactional`. The kind
is fixed when the template is created; a replace that changes it is
`409 MAILBOX.TEMPLATE_KIND_IMMUTABLE`. `GET /v1/mail-templates?kind=standard|transactional` lists one
kind; without `kind` it lists every template.

A transactional template also has:

| Field | Rule |
|---|---|
| `key` | Required. `^[a-z][a-z0-9-]{0,63}$`, unique within the organization (`409 MAILBOX.TEMPLATE_KEY_TAKEN`). The API names the template by it. |
| `from_address` | Required. A bare address, stored lowercased, whose domain is a `verified` domain of the organization (`send_receive` or `send_only`). With the `log` provider the instance's own `MAILBOX_DOMAIN` is also allowed. Otherwise `422 MAILBOX.DOMAIN_NOT_VERIFIED`. |
| `from_name` | Optional display name, at most 100 characters. |
| `variable_definitions` | At most 100 items of `{ name, secret, required }`, names unique and following the placeholder name rule. Empty or absent when the content has no placeholders. |

The variable definitions must match the content exactly, or the save is
`422 MAILBOX.TEMPLATE_INVALID` with an `error` message naming the problem:

- every placeholder in the subject and bodies is declared, and every declared variable is used;
- a `required` variable has no fallback, and every placeholder of an optional variable has one;
- a `secret` variable is `required`, so it never has a fallback.

A `standard` template must not carry `key`, `from_address`, `from_name`, or `variable_definitions`
(`422 MAILBOX.TEMPLATE_INVALID`). Responses always carry `kind`, `key` (null for standard), `from`
(`{ address, name }`, null for standard), and `variable_definitions` (empty for standard).
`sample_variables` may hold example values for secret variables too; they are used only by previews.

Every member reads every template and writes standard ones with the `templates` grant. Creating,
replacing, or deleting a transactional template needs an owner, an admin, or a developer whose grant
covers the sender domain (both the stored and the new one on a replace; see
[Developer access](#developer-access)). A developer acting outside the grant gets
`403 DEVELOPER.DOMAIN_NOT_ALLOWED`; any other member `403 PERMISSION.DENIED`. Every template
response carries `outside_sending_scope`: true only for a developer whose grant does not cover a
transactional template's sender, which the template is then read-only for.

A transactional template serves only transactional messages. Naming one in a mailbox send
(`template`), a template test (`POST /v1/mail-templates/{id}/tests`), or a broadcast (create,
update, or send) is `422 MAILBOX.TEMPLATE_KIND_MISMATCH`. A broadcast copies its template when it
starts, so a running broadcast is not affected. If the sender domain later loses verification, the template is
kept and sends from it are refused.

### Create a template from an AI tool

`POST /v1/agent-mail-templates` creates a template with an API key instead of a session, so an AI
tool or agent can save a draft template for people to review. Configure the tool runner with a
user-owned key scoped to `templates:create` and the organization ID; keep the key in the runner's
secret store, and let the model supply only the JSON body. The body, the validation rules, and the
response are those of `POST /v1/mail-templates`, and the key acts with its owner's current
membership (see [Programmatic Sending](#programmatic-sending)):

- a key without `templates:create` (or `*`) is `403 PERMISSION.DENIED`;
- an owner who is not a member of the `Organization-Id` organization is `404 ORGANIZATION.NOT_FOUND`;
- a member owner needs both the `api_keys` and the `templates` grant, like the session route:
  either one missing is `403 ORGANIZATION.FEATURE_NOT_GRANTED`; owners and admins always have both;
- a transactional template still needs an owner or admin (`403 PERMISSION.DENIED`).

```sh
curl --fail-with-body -X POST https://zmail.im/v1/agent-mail-templates \
  -H "X-API-Key: $ZMAIL_API_KEY" \
  -H "Organization-Id: $ZMAIL_ORG_ID" \
  -H 'Content-Type: application/json' \
  -d '{"name":"Welcome follow-up","subject":"Welcome, {{name}}","text":"Hello {{name}}, how can we help?","sample_variables":{"name":"Alex"}}'
```

A successful request is `201` with the saved template in `data`; members review it on the
organization's Templates page. Creation sends no email. A duplicate name is
`409 MAILBOX.TEMPLATE_NAME_TAKEN`. The scope grants no sending, reading, editing, or deletion, and
test keys are refused.

## Contacts

A contact is one address an organization sends broadcasts to: `email`, `first_name`, `last_name`,
up to 50 string `properties`, a `status`, and the lists it belongs to. An organization keeps at most
100000 contacts, one per address. A member with the `contacts` grant reads and edits them; the
`broadcasts` grant alone reads only the list names (`GET /v1/mail-contact-lists`).

| Field | Rule |
|---|---|
| `email` | A bare address, lowercased, within the RFC 5321 limits (local part 64 characters, each domain label 63, the whole address 254). Set when the contact is created and never changed. |
| `first_name`, `last_name` | Whitespace collapsed; at most 100 characters each; may be empty. |
| `properties` | Names follow the template variable rules and cannot be `email`, `first_name`, or `last_name`. Values are trimmed, at most 500 characters; an empty value removes the property. |
| `status` | `subscribed`, `unsubscribed`, or `suppressed`. Members set the first two; a new contact is subscribed. Zmail sets `suppressed` after a permanent bounce or a complaint (with the broadcasts slice). |
| `status_reason` | `manual` when a member unsubscribed the contact; empty when subscribed; kept while suppressed. |
| `list_ids` | The complete set of lists the contact belongs to. Every id must be a list of the organization. |

`PUT` replaces the names, properties, status, and lists; it ignores `email`. Leaving `status` out
keeps the current status, so editing a suppressed contact does not resubscribe it.

`GET /v1/mail-contacts` pages contacts newest first. `q` matches the address, first name, or last
name case-insensitively; `status` and `list_id` narrow it.

A list is a named group of the organization's contacts, used to choose who receives a broadcast.
Recipients never see lists. An organization keeps at most 100 lists, names are unique within it,
and a contact may be in any number of them. `GET /v1/mail-contact-lists` returns every list with
`contact_count`. Deleting a list keeps its contacts; deleting a contact removes it from its lists.

A contact, list, or list id of another organization is `404` (`MAILBOX.CONTACT_NOT_FOUND` or
`MAILBOX.CONTACT_LIST_NOT_FOUND`).

## Contact Import

```json
{ "csv": "email,first_name,plan\nann@example.com,Ann,pro\n", "list_id": 3 }
```

`POST /v1/mail-contact-imports` reads UTF-8 CSV text of at most 5 MiB and 10000 data rows, with a
header row. A leading byte-order mark is ignored and blank lines are skipped. Header names are
compared case-insensitively, with spaces and hyphens read as underscores, so `First Name` is
`first_name`.

| Column | Meaning |
|---|---|
| `email` (also `e-mail`, `email address`) | Required. The contact's address. |
| `first_name`, `last_name` | Names. |
| Any other name that can be a property | A property. |
| Anything else | Ignored, and listed in `ignored_columns`. |

For each row:

- A new address becomes a subscribed contact.
- An existing address is updated: a non-empty cell replaces the stored value, an empty cell keeps
  it, and properties are merged. The status never changes, so an unsubscribed contact stays
  unsubscribed.
- A row whose address is unusable, repeated earlier in the file, or whose names or properties
  break the rules above is skipped with a reason: `invalid_email`, `duplicate`, `invalid_name`,
  `invalid_property`, or `too_many_properties`.

With `list_id`, every stored row joins that list. The stored rows are written in one transaction:
an import that would pass the contact limit is `409 MAILBOX.CONTACT_LIMIT_REACHED` and stores
nothing. A file that is not UTF-8, too large, has more rows, has no `email` column, repeats a column,
or has broken quoting is `422 MAILBOX.CONTACT_IMPORT_INVALID`.

The response counts `rows`, `created`, `updated`, and `skipped_count`, and lists the first 100
skipped rows by `line` (the header is line 1) and `reason`. Values are stored as plain strings;
nothing in a cell is evaluated.

## Broadcasts

A broadcast sends one template from one mailbox to the subscribed contacts of one list, one
message per contact. Access follows the sender mailbox:

- Listing shows only broadcasts whose sender mailbox the caller may `view`. Reading one and its
  recipients needs `view`; without any access to the sender mailbox the broadcast is
  `404 MAILBOX.BROADCAST_NOT_FOUND`, and with some access but not `view` it is `403`.
- Creating, editing, and deleting need `send` on the sender mailbox; editing needs it on the stored
  and on the new sender. A new sender without any access is `404 MAILBOX.NOT_FOUND`.
- Every broadcast route needs the `broadcasts` grant (owners and admins always have it); without it
  the answer is `403 ORGANIZATION.FEATURE_NOT_GRANTED`.
- Owners, admins, and members with the grant send, cancel, pause, and resume broadcasts. Sending
  and resuming also need `send` on the sender mailbox at that call; the worker does not check
  access again, so a broadcast already sending keeps sending when that access is later removed.
  A member cancels or pauses only a broadcast it could send. Owners and admins need no access to
  the sender mailbox to cancel or pause, so an admin can always stop mail; without `view` on it
  (or for a sealed broadcast) the answer is `204` with no body, so nothing about the broadcast is
  revealed.

Broadcasts need unsubscribe links, so they are available only when the API has
`MAILBOX_INBOUND_SECRET` (the links are signed with a key derived from it) and `MAILBOX_PUBLIC_URL`
(the public origin of the Web console). `GET /v1/mailbox-domains` reports `broadcasts_enabled`;
without it, sending is `503 MAILBOX.BROADCAST_DISABLED`.

| Status | Meaning |
|---|---|
| `draft` | Editable. Has no recipients. |
| `scheduled` | Recipients are fixed; sending starts at `scheduled_at`. |
| `sending` | The worker is sending its recipients. |
| `paused` | Recipients stay pending and nothing is sent; `stop_reason` says why. |
| `sent` | No recipient is pending, and a recipient was sent or none failed. |
| `canceled` | Stopped; recipients not yet sent became `canceled`. A broadcast that ends with failed recipients and none sent is `canceled` with `stop_reason` `all_recipients_failed`, never `sent`. |

`POST /v1/mail-broadcasts` creates a draft from `name`, `mailbox_id`, `template_id`, `list_id`,
and the optional `unsubscribe_label`, `test_percent`, `test_wait_minutes`, and `variables`. Each must belong to the organization.
`variables` are template values shared by every recipient, such as a promotion code or an event
date: at most 100, with variable names, values of at most 2,000 characters, and none of `email`,
`first_name`, `last_name`, or `unsubscribe_url`; otherwise `422 MAILBOX.BROADCAST_INVALID`. They are
fixed with the rest of the draft when sending starts. `PUT` replaces a draft
and is `409 MAILBOX.BROADCAST_NOT_EDITABLE` in any other state.

`POST /v1/mail-broadcasts/{id}/sends` with an optional `scheduled_at` starts a draft:

- The template is copied into the broadcast, so later edits to the template do not change it.
- One recipient is added per contact of the list whose status is `subscribed`. A list without one
  is `422 MAILBOX.BROADCAST_NO_RECIPIENTS`, and the draft stays a draft.
- A template variable without a fallback that no recipient could fill (it is not in the
  broadcast's `variables`, not `email`, `first_name`, `last_name`, or `unsubscribe_url`, and not a
  property of any subscribed contact of the list) is
  `422 MAILBOX.TEMPLATE_VARIABLES_MISSING` with one field error per variable, named
  `variables.<name>`, and the draft stays a draft. A variable only some contacts lack still fails
  just those recipients when they are sent.
- Without `scheduled_at`, or with a time in the past, the broadcast is `sending`; otherwise it is
  `scheduled`, at most a year ahead.

`POST /v1/mail-broadcasts/{id}/cancellations` cancels a `scheduled`, `sending`, or `paused` broadcast.

### Staged Sending And Automatic Pause

- With `test_percent` from 1 to 50, that share of the recipients, picked at random and at least
  one, forms the test wave (`current_wave` 1). When it is done the broadcast is `paused` with
  `stop_reason` `test_wave_complete` and `resume_at` set `test_wait_minutes` later (default 30, 5 to
  1440). At `resume_at` the worker checks the rates below and sends everyone else
  (`current_wave` 2).
- Before every round the worker checks each sending broadcast, and each waiting test wave, once at
  least 20 recipients are finished. At 5% bounces (`counts.bounced`, including send-time bounces) it
  pauses with `bounce_rate_high`, and at 0.3% spam complaints (`counts.complained`, Gmail's limit
  for bulk senders) with `complaint_rate_high`.
- `POST /v1/mail-broadcasts/{id}/pauses` holds a sending broadcast, or a waiting test wave, with
  `paused_by_owner`; holding a waiting test wave keeps its `resume_at`.
  `POST /v1/mail-broadcasts/{id}/resumptions` continues a paused one: a waiting test wave continues
  with everyone, a broadcast paused by hand continues the wave it was in (a test wave that is already
  done goes back to `paused` with `test_wave_complete`, waiting until the `resume_at` it had, or
  `test_wait_minutes` from the resume when it finished while paused), and
  one the health check paused continues with everyone and `safety_override` true, so it is not
  paused automatically again. Either is `409 MAILBOX.BROADCAST_NOT_EDITABLE` in another state. A
broadcast can be deleted when it is `draft`, `sent`, or `canceled`; messages already sent stay in
the mailbox.

### Sending

The `mail:broadcast:work` worker sends each recipient as an ordinary outbound message from the
broadcast's mailbox, so the organization's broadcast lane limit, delivery events, the log, and
webhooks apply to it.

- Variables come from the broadcast's `variables`, then the contact's properties (which override a
  shared value of the same name), then the contact's `email`, `first_name`, `last_name`, and
  `unsubscribe_url`. A variable without a value or fallback fails that recipient with
  `template.variables_missing`.
- When no recipient is pending the broadcast ends: `sent`, or `canceled` with `stop_reason`
  `all_recipients_failed` when recipients failed and none was sent.
- If the template does not use `{{unsubscribe_url}}`, a small link with `unsubscribe_label` (or
  `Unsubscribe`) is added at the end of the HTML body and the text body.
- Every message carries `List-Unsubscribe` and `List-Unsubscribe-Post: List-Unsubscribe=One-Click`,
  so mail clients can offer one-click unsubscribe.
- A contact that is no longer `subscribed` when its turn comes is `skipped`
  (`contact_not_subscribed`); a deleted contact is `skipped` (`contact_deleted`); a contact whose
  address is on the organization's suppression list (see [Transactional Mail](#transactional-mail))
  is `skipped` (`contact_suppressed`) and keeps its status.
- Each recipient is sent with an idempotency key of its broadcast and contact, so a worker that stops
  after the provider call never sends that contact a second message.
- When the broadcast lane limit (`daily_broadcast_limit`) is reached, the broadcast keeps `sending`
  with `stop_reason` `send_limit_reached` and continues when the 24-hour window has room. Personal
  and transactional sends never use the broadcast lane's room, and a broadcast never uses theirs.

The broadcast reports `counts` of its recipients by status. `GET
/v1/mail-broadcasts/{id}/recipients` pages them, with an optional `status` filter, each with the
stored `message_id`, a `failure_code`, the message's `last_event` (see [Delivery Events](#delivery-events),
so `delivered`, `bounced`, or `complained`), and the contact's current `contact_status` (`unsubscribed`
once the recipient used the link; null once the contact is deleted).

### Progress And Estimated Finish

`GET /v1/mail-broadcasts/{id}` adds `progress` for a `scheduled`, `sending`, or `paused`
broadcast; it is null in the listing, in answers to writes, and for any other status. It is
computed on every request and never cached:

```json
{
  "rate_per_minute": 42.5,
  "rate_window_minutes": 10,
  "estimated_finish_at": "2026-11-24T08:39:00Z",
  "send_limit_pauses": true,
  "send_limit_resumes_at": "2026-11-24T08:00:00Z",
  "computed_at": "2026-11-23T08:00:00Z"
}
```

| Field | Meaning |
|---|---|
| `rate_per_minute` | Recipients that became `sent`, `failed`, or `skipped` in the last `rate_window_minutes` (10), per minute, counted over the part of that window since `started_at` and at least one minute; two decimals. Null when none finished in the window, and always null unless the broadcast is `sending`. |
| `estimated_finish_at` | When the last pending recipient should be finished. Null when it cannot be told: a broadcast paused by a person or by the health check, or one without a rate whose pending recipients all fit in the broadcast lane's room. |
| `send_limit_pauses` | True when the organization's 24-hour broadcast lane limit (`daily_broadcast_limit`, see [Sending](#sending)) holds the broadcast now or will hold it before every pending recipient is sent. |
| `send_limit_resumes_at` | When the lane has room again for a broadcast the limit holds or will hold; null when `send_limit_pauses` is false. |

The estimate uses only what the API stores: the pending count, the rate, the lane limit that
applies to the organization now, and the instants of the lane's attempts in the rolling window
(deleted messages included, exactly as the limit counts them). Sending starts now, at
`scheduled_at`, or at `resume_at` of a waiting test wave. While the lane has room for every pending
recipient, the finish is that start plus pending divided by the rate. Otherwise sending pauses once
the room is used, resumes when the oldest attempt leaves the 24-hour window, and the n-th recipient
over the room is sent when the n-th oldest attempt leaves it; past the attempts in the window,
each further day sends one limit's worth. Without a rate, only the limit's waits count. The estimate
assumes no other broadcast of the organization uses the lane meanwhile, so it is a guide, not a
promise.

### Notices

The mail worker tells the person who started a broadcast (who sent or scheduled it) when it:

| Event | `kind` |
|---|---|
| Ends `sent` | `mail.broadcast_sent` |
| Ends `canceled` with `stop_reason` `all_recipients_failed` | `mail.broadcast_failed` |
| Is paused with `bounce_rate_high` or `complaint_rate_high` | `mail.broadcast_paused` |
| Starts waiting with `send_limit_reached` | `mail.broadcast_paused` |

A broadcast canceled or paused by a person, or a test wave waiting out its wait, sends no notice.
Each notice says what happened in counts, what Zmail does next (for the send limit, when sending
resumes on its own, in UTC), and what the person can do, and links to
`{MAILBOX_PUBLIC_URL}/console/mail-broadcasts?org={organization_id}&open={broadcast_id}`.

- When the notification starter runs, the notice is an in-app notification of that `kind` for the
  starter (`in_app` channel only, `action_url` the console path above).
- When the system mailer (`MAIL_PROVIDER` and `MAIL_FROM`, the mailer of password resets) is configured, it also goes by email to the starter's account
  address. Email is sent whatever the person's notification preferences say, because it is about
  mail they sent.
- Each event is told once. A notice whose email could not be sent is tried again on the next
  worker round under the same idempotency key; one the mailer refuses for good is not. A
  broadcast that leaves a state and enters it again (the limit holds it on a later day) is told
  again. A starter who is no longer a member of the organization is not told. Broadcasts that
  finished more than seven days ago are not looked at, and those that had ended or paused before
  this feature was deployed are not told about.

### Unsubscribe And Suppression

An unsubscribe link is `{MAILBOX_PUBLIC_URL}/unsubscribe/{token}`, a page of the Web console. The
token names one contact and does not expire. `POST /v1/mail-unsubscribes/{token}` needs no session:

- A valid token is `204`; the contact becomes `unsubscribed` with `status_reason`
  `unsubscribe_link`. A contact already unsubscribed or suppressed, or one that no longer exists,
  also gets `204`, so the answer discloses nothing.
- Any other token is `404 MAILBOX.UNSUBSCRIBE_INVALID`.

The `List-Unsubscribe` header points at `{MAILBOX_PUBLIC_URL}/api/mail-unsubscribes/{token}`,
which the Web console forwards to this route for mail clients.

A broadcast recipient whose message bounces permanently, is refused by the provider's suppression
list, or draws a spam complaint becomes `suppressed`, with `status_reason` `bounced`, `rejected`, or
`complained`. Later broadcasts skip it. A `bounced` or `complained` delivery event for a broadcast
message also adds the address to the organization's suppression list.

## Lane Usage

`GET /v1/mail-send-lanes` shows owners, admins, and developers (see
[Developer access](#developer-access)) how full each lane is, for the whole organization; any other
member gets `403 PERMISSION.DENIED` and reads only the limits from `GET /v1/mailbox-domains`. It is computed on
every request and never cached:

```json
{
  "lanes": [
    { "lane": "transactional", "limit": 200, "used": 37, "waiting": 0 },
    { "lane": "broadcast", "limit": 200, "used": 200, "waiting": 412 },
    { "lane": "personal", "limit": 200, "used": 12, "waiting": 0 }
  ],
  "window_hours": 24,
  "computed_at": "2026-11-23T08:00:00Z"
}
```

`lanes` always has the three lanes in this order. `used` counts the lane's provider calls that
started in the 24 hours before `computed_at`, failed attempts and those of messages deleted since
included, exactly as the limit counts them, and may exceed `limit` by the requests that were in flight. `waiting` is the messages waiting
for room in the transactional or personal lane, and for the broadcast lane the pending recipients
of broadcasts paused because the lane was full.

## Programmatic Sending

Backend services send with an API key instead of a browser session. Create the key with
`POST /v1/api-keys` (see [`API_KEYS.md`](../API_KEYS.md)) and pass it as `X-API-Key`, together with
the `Organization-Id` header. A key belongs to a user, and it acts with that user's current
membership of the named organization: the same header errors apply, and removing the user from the
organization stops the key from working there on the next request. Every route below also needs
the membership's `api_keys` grant (owners and admins always have it): a member's key without it is
`403 ORGANIZATION.FEATURE_NOT_GRANTED` in that organization, though the key itself stays valid.
Keys owned by an organization are not defined.

A key may be [restricted](../API_KEYS.md#restrictions) when it is created. A key bound to an
organization may omit `Organization-Id` on every route below and then acts in that organization;
naming any other organization is `403 API_KEY.ORGANIZATION_MISMATCH`, checked before membership.
A key restricted to sending domains is checked on both sending routes before anything else about the
sender: `POST /v1/outbound-mail-messages` compares the domain of `from`, and
`POST /v1/transactional-messages` the domain of the template's `from_address`, ignoring case. Any
other domain is `403 API_KEY.DOMAIN_NOT_ALLOWED`; nothing is stored, sent, or counted in a send
limit. The restriction does not change what the key may read, and it does not apply to agent drafts,
which never send. Unrestricted keys behave exactly as before.

| Route | Required scope |
|---|---|
| `POST /v1/outbound-mail-messages` | `mail:send` |
| `GET /v1/outbound-mail-messages/:message_id` | `mail:read` |
| `POST /v1/transactional-messages` | `mail:transactional` |
| `GET /v1/transactional-messages/:message_id` | `mail:transactional` |
| `GET /v1/agent-mail-events` | `mail:agent` (see [Agent Grants](TRIAGE_AND_AGENTS.md#agent-grants)) |
| `GET /v1/agent-mail-messages/:message_id` | `mail:agent` |
| `POST /v1/agent-mail-drafts` | `mail:agent` |
| `POST /v1/agent-mail-templates` | `templates:create`, and the `templates` grant (see [Create a template from an AI tool](#create-a-template-from-an-ai-tool)) |

`mail:transactional` neither includes nor is included by `mail:send`; `*` grants both. Only the two
transactional routes accept test keys (see [`API_KEYS.md`](../API_KEYS.md)); every other route here
refuses a test key with `403 PERMISSION.DENIED`.

```json
{
  "from": "hello@mail.example.com",
  "to": ["bob@example.com"],
  "subject": "Your receipt",
  "text": "Thanks for your order."
}
```

`from` must be the bare address of a mailbox of that organization; any other address, including
another organization's mailbox, is `404 MAILBOX.NOT_FOUND`. The key has exactly its owner's
[access](INBOX.md#access): sending needs `send` on the `from` mailbox, and a mailbox the owner has no access
to is `404 MAILBOX.NOT_FOUND`; reading a sent message needs `view` on its mailbox, and a message of
a mailbox the owner has no access to is `404 MAILBOX.MESSAGE_NOT_FOUND`. A message these routes
return, and one a template test send returns, has `starred` always `false`, because a star belongs
to a person viewing the mailbox. `placement`, `folder_id`, `label_ids`, `trashed_at`, and
`thread_id` are reported as stored. The remaining fields, limits, outcomes, and
`Idempotency-Key` behavior are those of [Send](INBOX.md#send). A key without the scope gets
`403 PERMISSION.DENIED`. The read route returns only outbound messages, so a `mail:read` key cannot
read mail that was received. Both routes are limited to the organization in the header.

## Developer Access

An owner or admin may give a `member` the `developer` feature (see
[ORGANIZATIONS.md](../ORGANIZATIONS.md)) to connect the organization's own systems without managing the
organization. The grant carries a sending scope: every verified domain of the organization (followed
as domains are verified later), or listed verified domains. The platform's shared sending domain is
never in a scope. Owners and admins have every developer capability with no scope.

| Capability | Developer |
|---|---|
| Developer console, send log, API keys with `mail:transactional` | Yes; the grant includes `api_keys` |
| Transactional templates | Create, replace, delete when the sender domain is in scope; read-only otherwise |
| Webhook endpoints | Manage only the endpoints they created (see [WEBHOOKS.md](../WEBHOOKS.md)) |
| Suppression list | List and remove; no bulk import |
| Lane usage and organization usage | Read only, whole organization |
| Members, domains, organization settings, colleagues' mailboxes, broadcasts | No (unchanged) |

The scope is checked when it matters, against the grant as it stands then:

- saving a transactional template (sender out of scope: `403 DEVELOPER.DOMAIN_NOT_ALLOWED`);
- creating an organization-bound API key: `sending_domains` must lie in the scope
  (`403 DEVELOPER.DOMAIN_NOT_ALLOWED`); a key created without them takes the listed domains of an
  explicit scope, and stays open under an all-domains scope;
- every transactional send by a member's key and every console test send by a member: the member
  needs the grant (`403 ORGANIZATION.FEATURE_NOT_GRANTED`) and the template's sender domain must be in
  scope (`403 DEVELOPER.DOMAIN_NOT_ALLOWED`), whoever created the template. Narrowing or withdrawing a
  grant applies to the next send; keys are never deleted, and a key the member also holds under a
  separate `api_keys` grant keeps working for `mail:send`.

Mailbox sends (`POST /v1/outbound-mail-messages`) are governed by mailbox access, not by this scope.

## Transactional Mail

A transactional message is one message to one recipient, rendered from a
[transactional template](#transactional-templates) and sent by a backend service with an API key:
sign-up codes, password resets, receipts. Zmail itself also sends [member welcome mails](GATEWAY.md#member-welcome-mail) as
transactional messages, with no template row (`template` `member-welcome`). It is a separate resource from mailbox messages: it has
no mailbox, never appears in a mailbox, the mail log, or the statistics, and counts only in the
transactional lane (see [Send](INBOX.md#send)).

The instance enables it with `MAILBOX_TRANSACTIONAL_ENABLED=true`, which also requires
`MAILBOX_SECRET_ENCRYPTION_KEY`. While it is off, `POST /v1/transactional-messages` and
`POST /v1/transactional-test-sends` answer `503 MAILBOX.TRANSACTIONAL_DISABLED`;
`GET /v1/mailbox-domains` reports the switch as `transactional_enabled`.

### Sending

```http
POST /v1/transactional-messages
X-API-Key: fastbuild_xxxxxxxx.yyyyyyyy
Organization-Id: 42
Idempotency-Key: signup-u_8812
Content-Type: application/json

{
  "template": "signup-code",
  "to": "lilei@example.com",
  "variables": { "code": "482913", "app": "Acme" }
}
```

| Field | Rule |
|---|---|
| `template` | Required. The `key` of a template of the organization, `^[a-z][a-z0-9-]{0,63}$`. An unknown key, or one of another organization, is `404 MAILBOX.TEMPLATE_NOT_FOUND`; a standard template is `422 MAILBOX.TEMPLATE_KIND_MISMATCH`. |
| `to` | Required. One bare address of 3 to 320 characters, trimmed and lowercased. A display name or several addresses is `422 MAILBOX.MESSAGE_INVALID`. |
| `variables` | Optional values by name, as for templates (at most 100, each at most 2000 characters). A name the template does not declare is `422 MAILBOX.MESSAGE_INVALID` with a field error `variables.<name>`. A required variable that is missing or empty is `422 MAILBOX.TEMPLATE_VARIABLES_MISSING` with one field error per variable. |
| `Idempotency-Key` header | Required. 1 to 255 printable ASCII characters; missing or malformed is `422 MAILBOX.IDEMPOTENCY_KEY_INVALID`. |

The body is at most 64 KiB (`413 COMMON.REQUEST_TOO_LARGE`). The template's sender domain must be
verified (with the `log` provider the instance's `MAILBOX_DOMAIN` also passes), else
`422 MAILBOX.DOMAIN_NOT_VERIFIED`. A refused request stores nothing.

The checks run in this order, and the first failure answers:

1. API key authentication (`401`), the organization header and membership (`400`, `404`).
2. Scope `mail:transactional` (`403 PERMISSION.DENIED`). Authorization comes before any lookup, so a
   refused caller learns nothing about templates.
3. The instance switch (`503 MAILBOX.TRANSACTIONAL_DISABLED`).
4. The body (`413`, `422 COMMON.VALIDATION_FAILED`) and the `Idempotency-Key`
   (`422 MAILBOX.IDEMPOTENCY_KEY_INVALID`).
5. The per-key rate limit (`429 COMMON.RATE_LIMITED`).
6. The template, the recipient, and the variable names (`404 MAILBOX.TEMPLATE_NOT_FOUND`,
   `422 MAILBOX.TEMPLATE_KIND_MISMATCH`, `422 MAILBOX.MESSAGE_INVALID`).
7. Idempotent replay: a stored message with the same key answers here (`200` or
   `409 MAILBOX.IDEMPOTENCY_CONFLICT`), so a replay uses no recipient allowance.
8. The per-recipient rate limit (`429 MAILBOX.RECIPIENT_RATE_LIMITED`).
9. The sender domain (`422 MAILBOX.DOMAIN_NOT_VERIFIED`) and the required variables
   (`422 MAILBOX.TEMPLATE_VARIABLES_MISSING`).
10. The suppression list and the transactional lane (`429 MAILBOX.SEND_LIMIT_REACHED`).

Because the template is read before the replay check, replaying a key after its template was
deleted is `404 MAILBOX.TEMPLATE_NOT_FOUND`; the stored message stays readable by id.

A new message answers `202` and a replay `200`, both with `Location: /v1/transactional-messages/<id>`
and the message:

```json
{
  "id": 7012,
  "mode": "live",
  "simulated": false,
  "template": "signup-code",
  "from": { "address": "no-reply@notify.acme-app.com", "name": "Acme" },
  "to": "lilei@example.com",
  "to_masked": "li••@example.com",
  "subject": "Acme verification code: ••••••",
  "variables": { "code": "••••••", "app": "Acme" },
  "status": "queued",
  "queued_reason": null,
  "failure_code": null,
  "last_event": null,
  "attempts": 0,
  "idempotency_key": "signup-u_8812",
  "created_at": "2026-11-19T14:22:08Z",
  "sent_at": null,
  "events": []
}
```

The request returns once the message is stored; the provider is called afterwards.
`subject` and `variables` are copies taken at creation with every secret variable masked; the
rendered content is kept encrypted only while the message is `queued` and is wiped when it reaches
any other status.

### Idempotency

The `Idempotency-Key` names one message within the organization and the key's mode, for 24 hours
from that message's `created_at`. A live key and a test key of the same organization using the same
`Idempotency-Key` create two independent messages. Within the 24 hours, a repeat with the same
template, recipient (after trimming and lowercasing), and variables answers `200` with the stored
message whatever its status, and nothing is sent again; different content is
`409 MAILBOX.IDEMPOTENCY_CONFLICT`. After 24 hours the key is released (`idempotency_key` becomes
null on the old message) and names a new message. Two concurrent requests with one key create one
message; the other answers as a repeat.

### Modes And Simulation

Every message has the `mode` of the key that sent it (see [`API_KEYS.md`](../API_KEYS.md)):

- A `test` message renders and validates everything, but never calls the provider, never counts in
  a lane, and never writes the suppression list. To a normal address it is simulated as delivered.
- Addresses on the instance's `simulator_domain` (`MAILBOX_SIMULATOR_DOMAIN`, default
  `test.zmail.im`) are simulated in both modes, the same way. The local part, case-insensitive and
  with an optional `+tag`, picks the outcome:

| Local part | Outcome |
|---|---|
| `delivered` | `sent`, with `accepted` and `delivered` events |
| `bounced` | `sent`, with `accepted` and `bounced` events |
| `soft-bounced` | `sent`, with `accepted` and `deferred` events |
| `complained` | `sent`, with `accepted`, `delivered`, and `complained` events |
| `suppressed` | `suppressed`, with a `rejected` event |

Any other local part on the simulator domain is `422 MAILBOX.MESSAGE_INVALID`. Simulated messages
have `simulated: true`, their events have `detail` `simulated`, and they publish webhooks as usual.

### Rate Limits

| Limit | Scope | Answer |
|---|---|---|
| `transactional_key_limit_per_second` (`MAILBOX_TRANSACTIONAL_KEY_PER_SECOND`, default 20) | One API key, per second | `429 COMMON.RATE_LIMITED` |
| `transactional_recipient_limit_per_minute` (`MAILBOX_TRANSACTIONAL_RECIPIENT_PER_MINUTE`, default 3) | One organization, mode, template, and recipient, per minute | `429 MAILBOX.RECIPIENT_RATE_LIMITED` |
| 5 per minute | Console test sends of one user | `429 COMMON.RATE_LIMITED` |

Each `429` sets `Retry-After` in whole seconds, at least 1. Windows are fixed, and a request refused
by a rate limit stores nothing.

### Suppression And The Lane

A recipient on the organization's suppression list is not sent to: the message is stored as
`suppressed` and answers `202`. A live, non-simulated message counts in the transactional lane
(see [Send](INBOX.md#send)). When the lane is full the message is stored as `queued` with `queued_reason`
`send_limit` and sent when the lane has room; when as many messages already wait as the lane allows,
the request is `429 MAILBOX.SEND_LIMIT_REACHED` and nothing is stored.

### Suppression List

The suppression list holds one entry per organization and address (lowercased). An address is
added when a delivery event reports it `bounced` or `complained` (see [Delivery Events](#delivery-events));
`rejected` events and provider refusals at send time never add one. Transactional messages to a
listed address are `suppressed`, and broadcasts skip it. Owners, admins, and developers manage the
list (developers are not limited by domain; the list has none); any other member gets
`403 PERMISSION.DENIED`. Every removal that deletes an entry is audited with who removed it.

`GET /v1/mail-suppressions` lists entries newest first, by `created_at` then `id`, in keyset pages.
Each entry has `id`, `email`, `reason` (`bounced` or `complained`), `source` (`transactional`,
`broadcast`, or `backfill` for a contact suppressed before the list existed), and `created_at`.

| Query | Meaning |
|---|---|
| `email` | An exact address, at most 320 characters, compared after trimming and lowercasing |
| `reason` | `bounced` or `complained` |
| `cursor` | `meta.next_cursor` of the previous page |
| `per_page` | 1 to 100, default 50 |

An unusable filter or a malformed cursor is `400 COMMON.INVALID_INPUT`. `meta` holds only
`next_cursor`, null on the last page.

`DELETE /v1/mail-suppressions/:suppression_id` removes an entry and answers `204`. It is idempotent
and discloses nothing: an entry already removed, or one of another organization, also answers `204`
and changes nothing. A removal that deleted an entry is audited as `mail_suppression.removed` with
the entry's id and reason. Removing an address lets transactional messages reach it again; it never
changes a contact's status, so a contact still `suppressed` is still skipped by broadcasts until it
is changed too.

### Status And Retries

| Status | Meaning |
|---|---|
| `queued` | Waiting to be sent. `queued_reason` is `send_limit` (waiting for the lane), `retry` (waiting for a retry), or null. |
| `sent` | The provider took the message (or the simulator decided). Later delivery events update `last_event`. |
| `failed` | Not sent; `failure_code` says why. |
| `suppressed` | The recipient is on the suppression list; nothing was sent. |

A temporary provider failure for which the provider returned an HTTP status is retried after 30
seconds, 2 minutes, and 10 minutes: at most 4 provider calls (`attempts`). Any failure without an
HTTP status, such as an unreachable provider or an unreadable answer, may have been accepted, so the
message becomes `failed` with `failure_code` `transport.unknown_outcome` and is never sent again.
Other `failure_code` values: a provider's permanent refusal code, `send.expired` (queued more than an
hour), `send.sender_unverified` (the sender domain lost verification before dispatch), and
`transport.not_configured` (the deployment has no outbound transport; the provider was never called). To send
again on purpose, use a new `Idempotency-Key`.

`last_event` follows [Delivery Events](#delivery-events): the most severe recorded event, null when
none. Messages and their events are deleted 30 days after `created_at`.

### Reading A Message

`GET /v1/transactional-messages/:message_id` needs `mail:transactional` and returns the same object
with up to 50 events (`type`, `detail`, `occurred_at`), oldest first. A live key reads only live
messages and a test key only test messages; a message of the other mode or another organization is
`404 MAILBOX.TRANSACTIONAL_MESSAGE_NOT_FOUND`.

### Send Log

`GET /v1/transactional-mail-log` serves the console; any member of the organization may read it.
Each entry has `id`, `mode`, `simulated`, `template`, `to_masked`, `subject` (masked), `status`,
`queued_reason`, `failure_code`, `last_event`, `idempotency_key`, `created_at`, and `sent_at`, and
never the full recipient or the variables. Entries are newest first, in keyset pages.

| Query | Meaning |
|---|---|
| `mode` | `live` (default) or `test` |
| `status` | `queued`, `sent`, `failed`, or `suppressed` |
| `last_event` | `delivered`, `deferred`, `bounced`, `failed`, `rejected`, or `complained` |
| `outcome` | `delivered` (`last_event` is `delivered`) or `problem` (`last_event` is `bounced`, `complained`, `failed`, or `rejected`, or `status` is `failed` or `suppressed`); combined with `status` and `last_event` as an intersection |
| `template` | A template key |
| `to` | An exact recipient, at most 320 characters, compared after trimming and lowercasing |
| `since`, `until` | `created_at` range, `since` inclusive and `until` exclusive; default the last 7 days, at most 30 days |
| `cursor` | `meta.next_cursor` of the previous page |
| `per_page` | 1 to 100, default 50 |

An unusable filter, a longer range, or a malformed cursor is `400 COMMON.INVALID_INPUT`. `meta` holds
`next_cursor` (null on the last page) and `total_24h`: the messages of the requested mode created in
the last 24 hours, of the requested `template` when one is given, ignoring the other filters and
counted up to 10000.

`GET /v1/transactional-mail-log/:message_id` returns one entry of either mode with `from`, the masked
`variables`, and up to 50 events, oldest first; still without the full recipient. A message of
another organization is `404 MAILBOX.TRANSACTIONAL_MESSAGE_NOT_FOUND`.

### Masking

- A recipient keeps its domain and the first two characters of its local part (one when the local
  part has at most two), followed by `••`: `lilei@example.com` shows `li••@example.com`.
- A secret variable that parses as an `http` or `https` URL with a host shows `<host>/••••`, without
  user information, port, path, or query: `https://u:p@reset.acme.com/t?code=1` shows
  `reset.acme.com/••••`. Any other secret value shows `••••••`.
- The subject is rendered with masked secret values. Non-secret variables are shown as sent.

### Console Test Sends

`POST /v1/transactional-test-sends` with `{ "template", "mode", "variables" }` sends a
transactional template to the caller's own account email; the recipient cannot be named. Any member
may. Variables not given take the template's sample values. `mode` `live` counts in the
transactional lane and calls the provider; `test` is simulated as delivered. The server generates
the idempotency key, so every request is a new message. At most 5 per user per minute
(`429 COMMON.RATE_LIMITED`). Otherwise the request follows the sending rules above from the
per-recipient rate limit on, and answers `202` with the message, including `to`.

### Transactional Errors

| Condition | Status | `error_code` | Retry |
|---|---:|---|---|
| Organization header missing or malformed | 400 | `ORGANIZATION.CONTEXT_REQUIRED` / `ORGANIZATION.CONTEXT_INVALID` | After fixing |
| Key missing or invalid | 401 | `AUTH.UNAUTHORIZED` (or an `API_KEY.*` code) | No |
| Scope missing | 403 | `PERMISSION.DENIED` | No |
| Not a member, or no such organization | 404 | `ORGANIZATION.NOT_FOUND` | No |
| Unknown template key | 404 | `MAILBOX.TEMPLATE_NOT_FOUND` | No |
| Unknown message, or another mode's message | 404 | `MAILBOX.TRANSACTIONAL_MESSAGE_NOT_FOUND` | No |
| Same key, different content | 409 | `MAILBOX.IDEMPOTENCY_CONFLICT` | With a new key |
| Body too large | 413 | `COMMON.REQUEST_TOO_LARGE` | After fixing |
| A field failed validation | 422 | `COMMON.VALIDATION_FAILED` | After fixing |
| Unusable recipient, undeclared variable, unknown simulator address | 422 | `MAILBOX.MESSAGE_INVALID` | After fixing |
| `Idempotency-Key` missing or malformed | 422 | `MAILBOX.IDEMPOTENCY_KEY_INVALID` | After fixing |
| Not a transactional template | 422 | `MAILBOX.TEMPLATE_KIND_MISMATCH` | No |
| The key's owner is a member without the developer grant | 403 | `ORGANIZATION.FEATURE_NOT_GRANTED` | After a grant |
| The template's sender domain is outside the key owner's developer grant | 403 | `DEVELOPER.DOMAIN_NOT_ALLOWED` | After the grant covers it |
| Required variable missing or empty | 422 | `MAILBOX.TEMPLATE_VARIABLES_MISSING` | After fixing |
| Sender domain not verified | 422 | `MAILBOX.DOMAIN_NOT_VERIFIED` | After verifying |
| Per-key or per-user rate limit | 429 | `COMMON.RATE_LIMITED` with `Retry-After` | Yes |
| Per-recipient rate limit | 429 | `MAILBOX.RECIPIENT_RATE_LIMITED` with `Retry-After` | Yes |
| Lane full and as many messages waiting as it allows | 429 | `MAILBOX.SEND_LIMIT_REACHED` | Later |
| Feature off | 503 | `MAILBOX.TRANSACTIONAL_DISABLED` | No |
| Database or dependency unavailable | 503 | `COMMON.SERVICE_UNAVAILABLE` | Yes |

## Delivery Events

`status` says whether the provider took a message. What happened to it afterwards is recorded as
events, one per recipient:

| Event | Meaning |
|---|---|
| `accepted` | The provider took the message. Recorded once per message, with no recipient. |
| `delivered` | The recipient's mail server accepted the message. |
| `deferred` | A temporary failure; the provider is still retrying. |
| `bounced` | The recipient's mail server refused the message for good. |
| `failed` | The provider gave up without a bounce. |
| `rejected` | The provider did not attempt the recipient, for example a suppressed address. |
| `complained` | The recipient reported the message as spam. |

Each event has `type`, `recipient`, `occurred_at`, and `detail`. `detail` holds short
machine-readable tokens such as `5.1.1 permanent_failure` or `suppressed`. Free-form provider text,
including the SMTP response line, is never stored or returned.

Events come from two places. The send call records `accepted` and whatever the provider reported
for each recipient at once (`delivered`, `bounced`, `rejected`). Later events arrive from the mail
edge through `POST /v1/mail-delivery-events`.

`last_event` is the most severe current state among the recipients, where a recipient's current
state is its latest event: `complained`, then `bounced`, `failed`, `rejected`, `deferred`,
`delivered`. With no recipient event it is `accepted` when the provider took the message. So a
message to two recipients, one delivered and one bounced, shows `bounced`; a recipient deferred
and then delivered counts as `delivered`.

### Webhooks

When the `webhook` starter is selected, an organization's webhook endpoints can subscribe to mail
events. Mailbox mail events (`mail.message.*`) are published only for messages of a shared mailbox whose
[webhook switch](INBOX.md#webhook-switch) is on, and only for messages created at or after its seal
boundary; personal mailboxes never publish. Endpoints, signing, retries, and delivery history are those of [WEBHOOKS.md](../WEBHOOKS.md);
this section defines only the mail events. Transactional mail publishes the separate `email.*`
events of [Transactional Mail Events](#transactional-mail-events).

| Event type | Published when |
|---|---|
| `mail.message.sent` | The provider took an outbound message. |
| `mail.message.failed` | The provider refused an outbound message at send time, or later gave up on a recipient without a bounce. |
| `mail.message.received` | An inbound message was stored in a mailbox. |
| `mail.message.delivered` | A recipient's mail server accepted the message. |
| `mail.message.delivery_delayed` | A temporary failure; the provider is still retrying. |
| `mail.message.bounced` | A recipient's mail server refused the message for good. |
| `mail.message.complained` | A recipient reported the message as spam. |
| `mail.message.suppressed` | The provider did not attempt a recipient on its suppression list. |

Every mail event has the same `data` object, with every field always present:

| Field | Value |
|---|---|
| `message_id` | The message's id, as used by the message routes. |
| `mailbox_id` | The mailbox that holds the message. |
| `mailbox_address` | That mailbox's address. |
| `direction` | `outbound` or `inbound`. |
| `from` | The sender address. |
| `to` | The `to` recipients, possibly empty. |
| `recipient` | The address a per-recipient event is about; empty for `sent`, `received`, and a send-time `failed`. |
| `reason` | A short machine-readable code such as `5.1.1 permanent_failure`, `suppressed`, or a send failure code; empty when there is nothing to explain. |

The payload never contains a subject, a body, or free-form provider text. A receiver that needs
the content reads the message through the API with its own credentials.

A webhook is queued in the same database transaction as the state change it reports, so an event
is published exactly when its change is stored, and each change publishes once: a repeated inbound
delivery, a repeated provider event, or an idempotent replay of a send publishes nothing new. A
send to several recipients publishes one `sent` and then one event per recipient outcome.

If the webhook cannot be queued, the mail state is still recorded and the failure is logged; that
one webhook is lost. Mail that was really sent or received is never dropped because of a webhook
failure. Without the `webhook` starter nothing is published.

#### Transactional Mail Events

[Transactional messages](#transactional-mail) publish their own `email.*` events, listed by
`GET /v1/webhook-event-types` with the mail events. A transactional message belongs to no
mailbox, so the mailbox webhook switch and seal boundary do not apply: when the `webhook` starter
is selected, every enabled endpoint of the organization that subscribes to a type receives it.
Test-mode and simulated messages publish too; `mode` and `simulated` tell them apart.

| Event type | Published when |
|---|---|
| `email.sent` | The provider accepted the message (`status` becomes `sent`), or a test-mode or simulated message finished. |
| `email.delivered` | A `delivered` delivery event. |
| `email.bounced` | A `bounced` delivery event (`bounce_type: "permanent"`) or a `deferred` one (`bounce_type: "temporary"`). |
| `email.complained` | A `complained` delivery event. |
| `email.suppressed` | The recipient was on the organization's suppression list at acceptance or before dispatch, or the provider reported it on its own suppression list (at send time or as a `rejected` event). |
| `email.failed` | `status` became `failed` (permanent provider refusal, unknown outcome, retries exhausted, queued too long, sender domain no longer verified, payload unavailable), or a `failed` delivery event (the provider gave up without a bounce). |

Every transactional event has the same `data` object, with every field always present:

| Field | Value |
|---|---|
| `message_id` | The transactional message's id, as used by `GET /v1/transactional-messages/{id}`. |
| `template` | The template key the message was sent with. |
| `to` | The full normalized (trimmed, lowercased) recipient address. |
| `mode` | `live` or `test`. |
| `simulated` | `true` when the message was simulated and never reached the provider. |
| `bounce_type` | `permanent` or `temporary` for `email.bounced`; `""` otherwise. |
| `reason` | A short machine-readable code such as `5.1.1 permanent_failure`, `suppressed`, `simulated`, or a failure code; `""` when there is nothing to explain. |

The payload never contains the subject, the body, or any variable, secret or not. The recipient is
given in full, unlike the masked send log: the receiver is the organization's own signed endpoint,
and only the full address lets it match an outcome to its user.

Each event's id is `t<message id>:<type without "email.">`, followed by `:` and the first 12 bytes
of the SHA-256 of a distinguishing key (hex) when one message can publish that type more than once,
such as one per provider event. The webhook starter deduplicates by
`(organization_id, source, event_id)`, so a repeated provider event or a replayed send publishes
nothing new. A consumer may still receive more than one event of the same type for one message:
for example, an `email.delivered` or `email.suppressed` reported by the provider during the send
and the same outcome reported again by a later provider event carry different event ids. Consumers
that act once per outcome deduplicate on `data.message_id` plus the event type. Events are queued
in the same transaction as the state change they report, with the same loss rule as the mail
events. Signing, delivery history, and retries are those of
[WEBHOOKS.md](../WEBHOOKS.md): up to ten attempts, retried after about 1 minute, 5 minutes, 15
minutes, 1 hour, 3 hours, 6 hours, 12 hours, 18 hours, and 24 hours, each with ±20% deterministic
jitter.

### Event Intake

`POST /v1/mail-delivery-events` carries one Cloudflare Email Sending event
(`cf.email.sending.message.*`, schema version 1) as JSON, at most 64 KiB, with
`X-Mailbox-Signature: t=<unix seconds>,v1=<hex>`. `v1` is HMAC-SHA256, keyed with
`MAILBOX_INBOUND_SECRET`, over `<t>.delivery-event.<body>`. The fixed `delivery-event` scope takes
the place of the recipient in the inbound signature, so a signed inbound message cannot be replayed
as an event. The same five-minute window applies.

| Condition | Response | Mail edge action |
|---|---|---|
| Recorded | `201` `{ "message_id", "message_kind", "type", "duplicate": false }` | Acknowledge |
| Event already recorded | `200` with `duplicate: true` | Acknowledge |
| Missing, stale, or wrong signature | `401 MAILBOX.INBOUND_SIGNATURE_INVALID` | Retry |
| No outbound or transactional message has the event's message id | `404 MAILBOX.MESSAGE_NOT_FOUND` | Acknowledge |
| The message was deleted inside the send window | `404 MAILBOX.MESSAGE_NOT_FOUND`; a bounce or complaint still counts toward the personal sending pause of [Send](INBOX.md#send) | Acknowledge |
| Not a usable Email Sending event | `422 MAILBOX.DELIVERY_EVENT_INVALID` | Acknowledge |
| No inbound secret configured | `503 MAILBOX.INBOUND_DISABLED` | Retry |

An event is matched to a message by the provider's message id, which the send call stores as
`message_id`, and is idempotent by the provider's event id. Mailbox messages are searched first,
then transactional messages; `message_kind` (`mailbox` or `transactional`) says which matched, and
`message_id` is that message's id. A transactional message records the event without a recipient
(it has one), updates its `last_event` by the same severity order, and a `bounced` or `complained`
event adds the recipient to the organization's suppression list (see
[Transactional Mail](#transactional-mail)); `rejected` never does. Opens and clicks are not tracked.
Messages sent with the development `log` provider have no provider id and never receive events.

## Inbound Delivery

`POST /v1/inbound-mail-messages` carries one raw RFC 5322 message (`Content-Type: message/rfc822`,
at most 5 MiB) and two headers:

| Header | Value |
|---|---|
| `X-Mailbox-Recipient` | The envelope recipient |
| `X-Mailbox-Signature` | `t=<unix seconds>,v1=<hex>` |

`v1` is HMAC-SHA256, keyed with `MAILBOX_INBOUND_SECRET`, over
`<t>.<lowercase recipient>.<raw body>`. The API compares in constant time and rejects a timestamp
more than five minutes from its clock. The signature is checked before the recipient is looked up,
so an unsigned caller cannot learn which addresses exist.

| Condition | Response | Mail edge action |
|---|---|---|
| Stored | `201` `{ "id", "duplicate": false }` | Accept |
| Same message already stored for this mailbox | `200` `{ "id", "duplicate": true }` | Accept |
| Missing, stale, or wrong signature | `401 MAILBOX.INBOUND_SIGNATURE_INVALID` | Temporary failure |
| No mailbox has the recipient address | `404 MAILBOX.RECIPIENT_NOT_FOUND` | Reject (bounce) |
| Larger than 5 MiB | `413 MAILBOX.INBOUND_TOO_LARGE` | Reject (bounce) |
| Not a parseable message with a sender | `422 MAILBOX.INBOUND_UNPARSEABLE` | Reject (bounce) |
| No inbound secret configured | `503 MAILBOX.INBOUND_DISABLED` | Temporary failure |

The inbound route carries no organization header: the recipient address identifies the mailbox,
and the mailbox its organization. A message is identified by its `Message-ID`, or by a hash of its
content when it has none, once per mailbox. The API stores the first `text/plain` and first `text/html` part, converted to UTF-8 and
truncated to 256 KiB each, the attached files as described under [Attachments](INBOX.md#attachments), and
the raw message byte for byte as its source (see [Message Sources](GATEWAY.md#message-sources)). A repeated
delivery stores nothing new.

### New-Mail Notifications

When the optional notification starter runs (see [`NOTIFICATIONS.md`](../NOTIFICATIONS.md)), a newly
stored inbound delivery (`201`, never a `200` duplicate) publishes one `mail.received` notification to
every user who can read the mailbox at that moment, and to no one else:

- a personal mailbox: its owner, while the owner is a member of the organization;
- a shared mailbox: each member whose permission includes `view`, while still a member of the
  organization.

The audience is the same rule as reading the mail (`MailboxAccessFor` granting `View`): organization
owners and admins who only administer the mailbox are not notified. Mail synced from a connected
outside account does not publish.

| Field | Value |
|---|---|
| `kind` | `mail.received` |
| `idempotency_key` | `mail.received:<message_id>` |
| `title` | The sender's display name, or address without one; control characters folded, at most 160 characters |
| `body` | The subject with codes and sensitive links masked as in [Masked Secrets](INBOX.md#masked-secrets), at most 80 characters (cut with `…`), or `(no subject)` |
| `action_url` | `/console/mailboxes?org=<organization_id>&mailbox=<mailbox_id>&open=<message_id>` |
| `channels` | `web_push` only, not required; the inbox already lists new mail, so it never enters the notification center |

No message body, recipient list, or attachment name is published. Publishing runs after the message
is stored; a failure is logged at `warn` with mailbox, message, and user ids only and never changes the
inbound response. Without the notification starter nothing is published.
