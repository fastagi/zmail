# Contacts, Templates, and Broadcasts — Implementation Plan

Lets an operations team, not only developers, send mail to many people. Written to
[BUSINESS_IMPLEMENTATION_PLAN_STANDARD.md](../BUSINESS_IMPLEMENTATION_PLAN_STANDARD.md) as an
extension of [mailbox.md](mailbox.md) and [mailbox-organizations.md](mailbox-organizations.md);
what those plans settle is not repeated. Public HTTP behavior is owned by
[contracts/MAILBOXES.md](../../contracts/MAILBOXES.md).

## 1. Executive Summary

**Outcome.** An operator writes a reusable email once, keeps the people it goes to in lists, and
sends it to a list with each recipient's name filled in. Recipients can unsubscribe, and Zmail never
mails them again.

**Problem.** Today every message is written by hand or by code. A message holds at most 50
recipients, who all see each other, and there is no way to keep or import addresses. Operations
staff need three things that comparable products ship together:

| Need | Resend | Postmark | SendGrid |
|---|---|---|---|
| Reusable content with variables | Templates with `{{{var\|fallback}}}` | Templates with Mustachio `{{var}}`, a JSON test-data editor, starter templates | Dynamic templates with Handlebars and test data |
| People to send to | Contacts with properties, Segments, Topics | — (transactional only) | Contacts and lists |
| One send to many, with unsubscribe | Broadcasts, unsubscribe handled automatically | — | Single sends |

**Decisions.**

- Variables are logic-free placeholders, `{{name}}` and `{{name|fallback}}`, like Postmark's
  deliberately small Mustache. Operators can write them without learning a language, and the output
  of a template is predictable. Values are always HTML-escaped in the HTML part; there is no raw
  form.
- Contacts belong to an organization, with one subscription status each. Lists group contacts for
  the organization's own use; recipients never see them. Topics (Resend's per-subject preferences)
  are deferred.
- Broadcasts run in a separate worker, like webhook delivery, so a send to thousands of people
  never holds an HTTP request open and survives a restart.

**In scope**, in three slices (Section 16): templates and sending by template; contacts, lists,
and CSV import; broadcasts with unsubscribe and suppression.

**Out of scope.** A drag-and-drop visual editor (starter templates and live preview cover the
first need); conditionals and loops in templates; topics; A/B tests; open and click tracking;
draft and published versions of a template.

## 2. Actors and Access Surfaces

| Actor | Surface | May |
|---|---|---|
| Member | Web console; session with `Organization-Id` | Read and edit templates, contacts, and lists; import contacts; create, schedule, and cancel broadcasts; send test messages |
| Owner, admin | Same | Everything a member may |
| Backend service | API key with `mail:send` | Send one message by template |
| Recipient | Public unsubscribe link, no sign-in | Unsubscribe the one address the link names |
| Broadcast worker | `fastbuild mail:broadcast:work` | Send the queued recipients of due broadcasts |

All members get the same rights because mailboxes already work that way; a separate operator role
is deferred until an organization asks for it.

## 3. End-to-End Workflows

1. **Write a template.** An operator starts from a blank template or a starter (welcome,
   notification, newsletter), edits the subject, HTML, and optional text, and fills sample values
   for the variables. The preview renders the sample values on every change. A test send renders
   the template with the sample values and sends it to an address the operator types.
2. **Send by template from code.** A service sends `template_id` and `variables` instead of a
   subject and body. A variable without a value and without a fallback rejects the send, naming the
   variable. The stored message holds the rendered content.
3. **Build a list.** An operator creates a list and adds contacts one by one or imports a CSV whose
   header has `email` and optional `first_name`, `last_name`, and other columns. Existing contacts
   are updated, invalid rows are reported by line, and unsubscribed contacts stay unsubscribed.
4. **Broadcast.** An operator picks a mailbox to send from, a template, and a list, then sends now
   or at a time. The worker sends one message per subscribed, unsuppressed contact, filling
   variables from the contact. Progress and per-recipient outcomes show on the broadcast.
5. **Unsubscribe.** Every broadcast message carries an unsubscribe link and the
   `List-Unsubscribe` headers. Opening the link, or a mail client's one-click unsubscribe, marks
   the contact unsubscribed for the organization.
6. **Suppress.** A permanent bounce or a complaint from a broadcast recipient marks the contact
   suppressed; later broadcasts skip it.

## 4. Module Decomposition

All of it lives in the existing `mailbox` starter: templates render into the existing send path,
broadcasts produce ordinary outbound messages with their delivery events, and suppression reacts to
those events. A separate module would have to reach into all of these.

| Owner | Rules |
|---|---|
| `mailbox` templates | Variable syntax, rendering, missing-variable errors, starter content |
| `mailbox` contacts | Address normalization, one contact per address and organization, subscription status, import rules |
| `mailbox` broadcasts | State machine, recipient snapshot, per-recipient idempotency, unsubscribe tokens, suppression |
| `webhook` | Unchanged; broadcast messages publish the existing mail events |

## 5. Domain Model and Vocabulary

- **Template**: named, reusable subject, HTML, and text with variables, owned by an organization.
- **Variable**: `{{name}}` or `{{name|fallback}}`. A name is letters, digits, and underscores,
  starting with a letter, up to 64 characters. Whitespace inside the braces is ignored.
- **Contact**: one email address of an organization, with `first_name`, `last_name`, up to 50
  string properties, and a status: `subscribed`, `unsubscribed`, or `suppressed`.
- **List**: a named group of contacts of one organization. A contact may be in several lists.
- **Broadcast**: one template sent from one mailbox to one list, now or at a time.
- **Broadcast recipient**: one contact of one broadcast, frozen when sending starts.

## 6. Persistence Design

| Table | Key columns | Notes |
|---|---|---|
| `mail_templates` | `organization_id`, `name`, `subject`, `html_body`, `text_body`, `sample_variables` (jsonb), `created_by` | Name unique per organization; deleted rows are removed |
| `mail_messages` | adds `template_id` (nullable) | No foreign key: deleting a template keeps the messages sent with it |
| `mail_contacts` | `organization_id`, `email`, `first_name`, `last_name`, `properties` (jsonb), `status`, `status_reason` | Unique `(organization_id, email)` |
| `mail_contact_lists`, `mail_contact_list_members` | list name unique per organization | Deleting a list keeps its contacts |
| `mail_broadcasts` | `mailbox_id`, `template_id`, `list_id`, rendered template snapshot, `status`, `scheduled_at`, counts | The snapshot keeps an edit to the template from changing a broadcast in progress |
| `mail_broadcast_recipients` | `broadcast_id`, `contact_id`, `email`, `status`, `message_id`, lease columns | Unique `(broadcast_id, contact_id)` |

## 7. State Machines

Broadcast: `draft → scheduled → sending → sent`; `draft` or `scheduled → canceled`;
`sending → canceled` stops recipients not yet sent. Recipient: `pending → sent | failed | skipped`.
A recipient is `skipped` when the contact is unsubscribed or suppressed at the moment it is sent.

## 8. HTTP Contract Design

Slice 1:

| Route | Purpose |
|---|---|
| `GET, POST /v1/mail-templates`, `GET, PUT, DELETE /v1/mail-templates/{id}` | Manage templates |
| `POST /v1/mail-template-previews` | Render unsaved or saved template source with variables; returns subject, HTML, text, and the missing variables. Sends nothing |
| `POST /v1/mail-templates/{id}/tests` | Render and send to the given addresses from a chosen mailbox |
| `template` on both send routes | `{ "id", "variables" }` in place of `subject`, `text`, and `html` |

Slices 2 and 3 add `/v1/mail-contacts`, `/v1/mail-contact-lists`, `/v1/mail-contact-imports`,
`/v1/mail-broadcasts`, and the public `GET, POST /v1/mail-unsubscribes/{token}`; each slice updates
the contract before its code.

## 9. Authorization Matrix

| Capability | Member, owner, admin | API key (`mail:send`) | Recipient | Other organizations |
|---|---|---|---|---|
| Templates | Own organization | Send by template of the key owner's organization | Forbidden | 404 |
| Contacts, lists, import | Own organization | Forbidden | Forbidden | 404 |
| Broadcasts | Own organization | Forbidden | Forbidden | 404 |
| Unsubscribe | — | — | The one contact the signed token names | — |

A template, list, or mailbox of another organization is reported as not found, never as forbidden.

## 10. Cross-Module Data Flow

A broadcast recipient's message is created through the same path as any send, so the daily send
limit, delivery events, and webhooks apply unchanged. The recipient row and its message are written
in one transaction with an idempotency key `broadcast:<id>:<contact>`, so a worker that dies after
calling the provider never sends twice to the same person. Suppression consumes `bounced` and
`complained` events of broadcast messages inside the event's transaction.

## 11. Scheduled and Asynchronous Work

`fastbuild mail:broadcast:work` leases due recipients in batches with `FOR UPDATE SKIP LOCKED`, at a
configured rate per organization, and marks a broadcast `sent` when no recipient is pending.
`make dev` starts it next to the webhook worker.

## 12. Browser-Surface Plan

Console pages: **Templates** (list; editor with code view, variable sample table, live sandboxed
preview, test send, starter picker), **Contacts** (search, add, edit, status, CSV import with a
result report, lists), **Broadcasts** (compose from mailbox, template, and list; schedule; progress).
The compose dialog gains "use a template".

## 13. Security, Privacy, and Audit

- Template output is untrusted HTML exactly like any message body: previews render only in the
  existing sandboxed frame. Variable values are HTML-escaped, so a contact's name cannot inject
  markup.
- Unsubscribe tokens are HMAC-signed with the inbound secret's key material under a separate scope,
  name one contact, and do not expire; the page discloses nothing but the result.
- CSV import is bounded to 5 MiB and 10 000 rows, parsed without formulas, and stores only the
  recognized columns as strings.
- Broadcasts respect the organization's daily send limit; a broadcast that would exceed it stops
  and shows why rather than queuing beyond it.

## 14. Observability and Operations

The broadcast worker logs each batch with the broadcast id, counts, and duration, never addresses.
A broadcast shows pending, sent, failed, and skipped counts.

## 15. Test and Verification Strategy

Rendering and parsing as table tests; repositories against disposable PostgreSQL; the worker with a
fake transport for idempotency after a crash and for cancellation; Web route, service, and UI tests
like the existing mailbox suites; a live broadcast to the test mailbox on `mail.zgi.im`.

## 16. Delivery Plan

1. **Templates** (done): the template store, renderer, previews, test sends, sending by
   template on both send routes, starter templates, and the Templates console page.
2. **Contacts** (done): contacts, lists, CSV import, and the Contacts console page. The import
   takes CSV text inside JSON rather than a file upload, so the browser adapter forwards it like
   any other body; the 5 MiB bound keeps that reasonable.
3. **Broadcasts**: broadcasts, the worker, unsubscribe and suppression, and the Broadcasts console
   page.
