# Mail AI Triage And Agents Contract

Part of the [Mailbox Contract](../MAILBOXES.md). Defines AI triage settings, manual overrides, reply drafts, agent grants, the agent feed, and agent reading/drafting. See [`../MAILBOXES.md`](../MAILBOXES.md) for activation, ownership, route overview, and the master error catalog.

## AI Triage

AI triage sorts new inbound mail into `reply` (the reader should answer), `deadline` (it carries a
date), `fyi` (information only), and `review` (the model could not decide), with a one-line reason.
It is off by default at three levels, and all three must be on for mail to be queued:

1. The instance names a model with `AI_TRIAGE_PROVIDER`. Without one, `available` is `false` and the
   feature cannot be turned on (`409 MAILBOX.TRIAGE_UNAVAILABLE`).
2. The organization switch, `GET` and `PUT /v1/mail-triage-setting`. Any member reads it; owners and
   admins replace it with every field: `{ "enabled": true, "daily_message_cap": 500,
   "draft_enabled": true }`. The response adds `available`, `instance_daily_cap`, and `updated_at`
   (`null` until saved). `daily_message_cap` is 1 to 100000; the effective cap is the lower of it
   and the instance cap, counted per UTC day, and mail over it waits for the next day.
3. The mailbox switch, `PUT /v1/mailboxes/:mailbox_id/triage-setting` with `{ "enabled": true }`,
   answering the mailbox. A personal mailbox's switch belongs to its owner alone; a shared
   mailbox's needs `manage_members`. Handover turns it off.

Only mail received while every switch is on is queued; turning triage on never classifies older
mail, and a message whose switches turn off before the worker reaches it is skipped. The worker
(`mail:triage:work`) classifies queued mail in batches; a model failure is retried after 1 and
5 minutes, and the third failure leaves the message in `review` with an empty reason.

A result is shared by every reader of the mailbox and shown only to callers who may read the
message: `{ "category": "deadline", "reason": "Asks for the contract by Friday",
"due_at": "2026-11-21T00:00:00Z", "source": "ai" }`. `due_at` is only for `deadline` and may be
`null`. While the organization switch is off, no result, filter, or count is shown.

`PUT /v1/mailboxes/:mailbox_id/messages/:message_id/triage` with `{ "category": "reply" }` records
a reader's own decision and needs `view`. It answers the result with `source` `manual` and an empty
reason; the model never overwrites it. `review` cannot be chosen, `due_at` is only for `deadline`
and lies within one year back and five years ahead, and an outbound message is
`422 COMMON.VALIDATION_FAILED`.

`POST /v1/mailboxes/:mailbox_id/messages/:message_id/reply-drafts` has no body and needs `send` on
the mailbox and the organization's `draft_enabled`. It answers
`{ "body": "...", "basis": [{ "kind": "message", "id": 9021, "label": "Contract" }] }`: plain text
for the reader to edit and the thread messages or templates it relied on. The draft is never stored
or sent, and a model failure is `502 MAILBOX.TRIAGE_MODEL_FAILED`.

The model reads only the [masked](INBOX.md#masked-secrets) form of mail, placed in the prompt as data it must
not follow. Triage sees the sender, subject, text body (8000 characters at most), the mailbox's mail
role, and up to 10 recent manual decisions of the mailbox; a draft sees the masked thread up to the
message, the organization's template names, and the mail role. Every setting change writes an audit
entry. All responses are `Cache-Control: private, no-store`.

## Agent Grants

An agent grant lets one API key, the agent, receive the new mail of one mailbox and act on it
without widening anyone's access. The agent is an API key of a person: it reads exactly what that
person may read, never more, and stops the moment that person stops. Agents never send; with the
`draft` capability they store reply drafts that a person reviews and sends.

### Issuing And Revoking

Only these people manage a mailbox's grants (issue, list, revoke, activity):

| Mailbox | Who |
|---|---|
| Personal, including a connected personal mailbox (`origin` `connected`) | Its owner alone. An organization owner or admin who is not the owner gets `403 PERMISSION.DENIED`, or `404 MAILBOX.NOT_FOUND` for a connected mailbox, which managers cannot see |
| Shared | A member with `manage`. An organization owner or admin only after adding themselves as a `manage` member (recorded like any member change) |

Anyone else who can see the mailbox gets `403 PERMISSION.DENIED`; someone with no access gets
`404 MAILBOX.NOT_FOUND`.

`POST /v1/mailboxes/:mailbox_id/agent-grants` (session) answers `201` with the grant:

```json
{
  "api_key_id": 12,
  "capabilities": ["read", "draft"],
  "reveal_secrets": false,
  "filters": { "senders": ["example.com", "billing@vendor.test"], "label_ids": [] },
  "expires_at": "2026-12-31T00:00:00Z"
}
```

- `api_key_id` must name a live (not test), unexpired, unrevoked key holding `mail:agent` (or `*`),
  whose owner is a current member of the organization with `view` on the mailbox: for a personal or
  connected mailbox that is only the owner; for a shared mailbox any member with `view`. A key of
  anyone other than the issuer is refused with one undifferentiated `422 MAILBOX.AGENT_GRANT_INVALID`
  whatever is wrong with it (unknown, another organization's, inactive, a test key, without the
  scope, or its owner unable to read the mailbox); only the issuer's own keys get a specific reason.
- `capabilities` must include `read`; `draft` is optional and needs the key's owner to have `send`.
  A connected mailbox is read-only whatever the permission, so `draft` is refused on it.
- `reveal_secrets` (default `false`) returns one-time codes and sign-in links unmasked; every such
  read is audited as `read_revealed`.
- `filters.senders`: at most 20 addresses or domains, case-insensitive. An entry with `@` matches the
  sender address exactly; a domain (a leading `@` is ignored) matches that domain and its
  subdomains. `filters.label_ids`: at most 20 labels of the mailbox; a message matches when it
  carries one of them as its event is written (labels applied on arrival; a label a person adds
  later does not create an event). Empty filters match every message. Filters narrow what is
  delivered; they are not authentication. A sender filter compares the message's `From` header,
  which a sender can forge: Zmail stores no SPF, DKIM, or DMARC verdict for inbound mail today, so it
  cannot refuse a spoofed sender. An agent must not treat a matching sender as proof of identity.
- `expires_at` is required, in the future, and at most 90 days ahead.

A request that breaks these rules is `422 MAILBOX.AGENT_GRANT_INVALID`. A key holds at most one
working grant per mailbox (`409 MAILBOX.AGENT_GRANT_EXISTS`), and a mailbox at most 10 working
grants (`409 MAILBOX.AGENT_GRANT_LIMIT`). A grant is working while it is live, its key is neither
deleted, revoked, nor expired, and its key's owner can still read the mailbox; a grant that stopped
working holds no slot and receives no events. Issuing writes an `agent_granted` access event.

A grant:

```json
{
  "id": 7, "mailbox_id": 3, "api_key_id": 12, "api_key_name": "Support agent", "api_key_prefix": "sk_live_ab",
  "key_user_id": 4, "granted_by": 4, "capabilities": ["read", "draft"], "reveal_secrets": false,
  "filters": { "senders": ["example.com"], "label_ids": [] },
  "status": "active", "expires_at": "2026-12-31T00:00:00Z", "revoked_at": null, "revoke_reason": null,
  "created_at": "2026-12-01T09:00:00Z"
}
```

`status` is `active`, `expired`, or `revoked`. `GET /v1/mailboxes/:mailbox_id/agent-grants` pages the
mailbox's grants newest first (`page`, `per_page`). `DELETE
/v1/mailboxes/:mailbox_id/agent-grants/:grant_id` revokes at once and answers `204`, also for an
already ended grant; an id that is not a grant of the mailbox is `404 MAILBOX.AGENT_GRANT_NOT_FOUND`.
`GET /v1/mailboxes/:mailbox_id/agent-grants/:grant_id/activity` returns the grant's newest 100 audit
rows, newest first: `{ "id", "grant_id", "api_key_id", "message_id", "action", "created_at" }` with
`action` `read`, `read_revealed`, or `draft`. Audit rows never hold a subject or a body and are kept
90 days (`MAIL_AGENT_AUDIT_RETENTION`). Like sealed mail, grants and audit rows created before a
mailbox's seal boundary are hidden from everyone: they are not listed, and their ids answer
`404 MAILBOX.AGENT_GRANT_NOT_FOUND`.

A grant stops working at once, on every agent route, when any of these happens:

| Event | Effect |
|---|---|
| A manager of the grants revokes it | `revoke_reason` `revoked` |
| `expires_at` passes | `status` `expired` |
| The key is revoked or expires | Authentication fails (`401`) |
| The key's owner leaves the organization | Every grant of their keys is revoked (`left_organization`), and the drafts of their personal mailboxes deleted |
| The key's owner is removed from a shared mailbox | Their grants of it are revoked (`member_removed`) |
| The connected account is disconnected (by its owner, a manager, deleting the mailbox, or the owner leaving) | The mailbox's grants are revoked (`connection_revoked`, or `left_organization`) and its drafts deleted; a revoked connection's mailbox writes no events |
| The mailbox is deleted, or a revoked connection's copies are purged | Every grant, event, draft, and audit row of the mailbox is deleted with it |
| A personal mailbox is handed over | Its grants are revoked (`handed_over`) and its drafts deleted, before its history is sealed |
| The key's owner no longer has `view` for any other reason, or the mailbox is deleted | The grant is ignored until it would apply again |

Every agent request checks the grant, the mailbox, and the key owner's current access; the
revocations above are the durable record. Revocations write `agent_revoked` access events. Nothing
in a message's content can create, widen, or end a grant.

### The Agent Feed

Agents authenticate with `X-API-Key` and `Organization-Id` like
[programmatic sending](DELIVERY.md#programmatic-sending); every agent route needs the `mail:agent` scope and
refuses test keys (`403 PERMISSION.DENIED`). `mail:agent` grants no other route, and no other scope
grants these.

Every inbound message Zmail stores, delivered or synced from a connected account, writes one
`message.received` event for each live grant of its mailbox whose filters it passes, in the
transaction that stores it. A message is stored once, so a grant gets exactly one event per
message, however often the delivery is retried. Events are never published to organization
webhooks.

`GET /v1/agent-mail-events?after=<cursor>&limit=<n>` returns the events of the key's working grants
in the organization, ascending:

```json
{
  "events": [
    { "id": 913, "type": "message.received", "grant_id": 7, "mailbox_id": 3, "message_id": 5512,
      "created_at": "2026-12-01T09:30:00Z" }
  ],
  "next_cursor": "913",
  "has_more": false
}
```

- `after` is a `next_cursor` from an earlier page; omitted, the feed starts at the oldest event kept.
  `limit` is 1 to 100, default 50. Anything else is `400 COMMON.INVALID_INPUT`.
- `next_cursor` is the last event's id, or `after` itself when the page is empty. Delivery is at
  least once: the same cursor returns the same events while the grants stay as they are, so an agent
  stores the cursor only after it has handled the page. Within an organization, event ids become
  visible in id order, so a cursor never skips an event committed later.
- A key without a working grant gets an empty feed. Events are kept 30 days
  (`MAIL_AGENT_EVENT_RETENTION`); every `mail:broadcast:work` pass deletes older events and audit
  rows in small batches, and `mail:agent-events:prune` deletes old events at once.
- Each key may make 600 requests a minute across the feed and message reads
  (`MAIL_AGENT_READS_PER_MINUTE`); past that it gets `429 COMMON.RATE_LIMITED` with `Retry-After`.

### Reading And Drafting

`GET /v1/agent-mail-messages/:message_id` returns a message one of the key's working grants received
an event for, in the shape of the message detail. Codes and sign-in links are masked
(`secrets_masked`) unless the grant has `reveal_secrets`. Each read writes an audit row (`read` or
`read_revealed`) and a revealing read logs `mail.agent_secret_revealed`. Agent reads never change
anyone's read state, star, or placement. Any other message, including mail stored before the grant
and mail of other mailboxes, is `404 MAILBOX.MESSAGE_NOT_FOUND`, as if it did not exist.

`POST /v1/agent-mail-drafts` stores a reply draft for a person:

```json
{ "message_id": 5512, "text": "Thanks, we will look into it.", "html": "<p>Thanks, we will look into it.</p>" }
```

It needs a working grant that received the message, holds `draft`, and whose key owner still has
`send` (`403 PERMISSION.DENIED` otherwise; a message the key cannot read is `404`). `text` is
required, and `text` and `html` are at most 100 KB each (`422 MAILBOX.MESSAGE_INVALID`). A grant keeps
one pending draft per message: the first answers `201`, a later one replaces its body and answers
`200`. Each draft writes a `draft` audit row. A draft:

```json
{ "id": 21, "mailbox_id": 3, "grant_id": 7, "message_id": 5512, "api_key_id": 12,
  "text": "Thanks, we will look into it.", "html": "", "status": "pending",
  "created_at": "2026-12-01T09:31:00Z", "updated_at": "2026-12-01T09:31:00Z" }
```

`GET /v1/mailboxes/:mailbox_id/agent-drafts` (session, `send` on the mailbox) pages the mailbox's
drafts newest first, only while their message exists and is not sealed. Nothing sends a draft; a
person replies through [Send](INBOX.md#send).
