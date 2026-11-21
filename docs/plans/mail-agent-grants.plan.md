# Mail Agent Grants And The Agent Mail Feed (D1): Implementation Plan

Roadmap: [mail-connectors-roadmap.zh-CN.md](mail-connectors-roadmap.zh-CN.md) §4.1, §5.3, §8 (decisions 4
and 5), §13 (conclusion 6). Loop task T8, backend and contract only; the Web Agent tab is a later
slice. Owner: the `mailbox` starter owns every rule and record; `contracts/` publishes the shape.

## 1. Executive Summary

A person authorizes an AI agent to receive the new mail of one mailbox they control. The agent is
an API key of a person (decision 5): it never sees more than that person may, and it loses access
the moment that person does. The agent pulls a per-grant, replayable feed of `message.received`
events, reads each message (codes masked unless the grant says otherwise), and may store a reply
draft for human review when the grant allows. It never sends. Every agent read and draft is
audited; issue and revoke appear in the mailbox access history.

Out of scope: sending drafts, pushing events (signed webhooks), the Web Agent tab, organization-
owned agents, agent access to attachments' bytes, events other than `message.received`.

## 2. Actors And Access Surfaces

| Actor | Surface | Authentication |
|---|---|---|
| Issuer: personal (or connected) mailbox owner; shared mailbox member with `manage` | `/v1/mailboxes/{id}/agent-grants*` | Session + `Organization-Id` |
| Organization owner/admin | Same routes, only as a `manage` member of a shared mailbox | Session |
| Draft reviewer: anyone with `send` on the mailbox | `GET /v1/mailboxes/{id}/agent-drafts` | Session |
| Agent: an API key with scope `mail:agent` | `/v1/agent-mail-events`, `/v1/agent-mail-messages/{id}`, `/v1/agent-mail-drafts` | `X-API-Key` + `Organization-Id` |
| Mail intake (Zmail delivery, connector sync) | `afterInboundStored` | Internal |
| Operator | `mail:agent-events:prune` command | Shell |

## 3. Workflows

1. **Issue.** The issuer picks one of the key owner's API keys (`api_key_id`), capabilities
   (`read` always, `draft` optional), `reveal_secrets`, optional filters, and `expires_at` (required,
   at most 90 days ahead). Zmail checks issuer rights, the key, the key owner's access, the limit,
   then stores the grant and an `agent_granted` access event in one transaction.
2. **Receive.** Every newly stored inbound message runs `afterInboundStored`. For each live grant of
   the mailbox (not revoked, not expired) whose filters match, one `mail_agent_events` row is
   written in the storing transaction. A message is stored once (dedupe key / external id), so each
   grant gets exactly one event per message; a unique `(grant_id, message_id)` makes it certain.
3. **Pull.** The agent calls `GET /v1/agent-mail-events?after=<cursor>&limit=<n>`: events of the
   key's *effective* grants with `id > cursor`, ascending, at most `limit` (default 50, max 100),
   plus `next_cursor` (the last returned id, or the given cursor when nothing is returned) and
   `has_more`.
4. **Read.** `GET /v1/agent-mail-messages/{message_id}` returns the message when one of the key's
   effective grants has an event for it. Masked like the normal detail; with `reveal_secrets` the
   unmasked content is returned and the audit action is `read_revealed`. Nobody's read state changes.
5. **Draft.** `POST /v1/agent-mail-drafts {message_id, text, html?}` stores a `pending` reply draft
   when the effective grant has `draft` and the key owner still has `send`. One pending draft per
   grant and message: a second POST replaces its body (200; the first answers 201).
6. **Review.** `send` holders list drafts with `GET /v1/mailboxes/{id}/agent-drafts`. Sending stays
   a person's action through the existing send route.
7. **Revoke.** The issuer side revokes (`DELETE`); leave, handover, and member removal revoke
   automatically. Activity (`GET .../activity`) shows the newest 100 audit rows.

## 4. Domain Model And Vocabulary

- **Agent grant**: permission for one API key to receive one mailbox's new mail. Fields:
  organization, mailbox, `api_key_id`, `key_user_id` (the key owner when issued), `granted_by`,
  capabilities `read` + optional `draft`, `reveal_secrets`, filters (`senders`: up to 20 addresses
  or domains; `label_ids`: up to 20 labels of the mailbox), `expires_at`, `revoked_at`,
  `revoked_by`, `revoke_reason`.
- **Live grant**: not revoked and not expired. Only live grants receive events.
- **Effective grant** (checked on every agent request, the authority): live, of the request's
  organization, issued to the request's key, and the key owner (the authenticated viewer) still has
  `view` on a mailbox that still exists. Owner left the organization → `organization_context`
  already rejects the key. Key revoked or expired → authentication rejects it.
- **Agent event**: `{id, grant_id, mailbox_id, message_id, type: message.received, created_at}`.
- **Agent audit row**: `{grant_id, api_key_id, mailbox_id, message_id, action, created_at}` with action
  `read`, `read_revealed`, or `draft`. Never subjects or bodies.
- **Agent draft**: `{grant_id, mailbox_id, message_id, api_key_id, text, html, status: pending}`.

Filter matching (sender): an entry with `@` matches the full sender address; an entry without
matches the sender's domain or any subdomain of it. Case-insensitive. Label filters match labels the
message carries when the event is written (labels applied at arrival, for example by a connector);
a label added later by a person does not create an event in this slice. Empty filters match all.

## 5. Persistence (migration `2026_12_05_090000_add_mail_agent_grants`)

| Table | Key columns and constraints |
|---|---|
| `mail_agent_grants` | FKs to organizations, mailboxes, api_keys (`ON DELETE CASCADE`); `can_draft`, `reveal_secrets` booleans default false; `sender_filters`, `label_ids` jsonb default `[]`; `expires_at NOT NULL`; `revoke_reason` CHECK in (`''`, `revoked`, `left_organization`, `handed_over`, `member_removed`); partial indexes on `(mailbox_id)` and `(api_key_id)` `WHERE revoked_at IS NULL` |
| `mail_agent_events` | FKs to grants, mailboxes, mail_messages (cascade); `type` CHECK `message.received`; unique `(grant_id, message_id)`; index `(grant_id, id)`; index `(created_at)` for retention |
| `mail_agent_audit` | no FKs (outlives what it names, like `mail_content_access_audits`); `action` CHECK; index `(grant_id, id DESC)` |
| `mail_agent_drafts` | FKs cascade to grant, mailbox, message; unique `(grant_id, message_id)`; `status` CHECK `pending`; index `(mailbox_id, id DESC)` |

All new tables; no lock on an existing table. Down drops them. Limits: at most 10 live grants per
mailbox (counted under a `FOR UPDATE` lock of the mailbox row), one live grant per key and mailbox
(`409 MAILBOX.AGENT_GRANT_EXISTS`), draft text and html at most 100 KB each.

**Cursor safety.** Sequence ids are allocated before commit, so a plain `id > cursor` read could
skip a lower id that commits later. Every event insert first takes a transaction-scoped advisory
lock keyed by the organization (`pg_advisory_xact_lock`) and holds it to commit, so within one
organization event ids become visible in id order. A key reads only its organization's events.
The lock is taken only when a live matching grant exists, so organizations without agents pay
nothing.

**Retention.** Every `mail:broadcast:work` pass deletes events older than `MAIL_AGENT_EVENT_RETENTION`
(30 days) and audit rows older than `MAIL_AGENT_AUDIT_RETENTION` (90 days) in bounded batches;
`mail:agent-events:prune [--before=<RFC3339>]` deletes old events at once. Grants are kept.

**Review hardening.** A grant that stopped working (key deleted, revoked, or expired; owner lost the
mailbox) receives no events and holds no slot: one SQL condition checks it where events are written
and where slots are counted. Handover and an owner's departure delete the mailbox's drafts in the
same transaction, and the draft list only shows drafts of existing, unsealed messages. Grants and
audit rows from before a seal boundary are hidden. Keys of anyone but the issuer get one
undifferentiated refusal. Each key gets `MAIL_AGENT_READS_PER_MINUTE` (600) agent requests a minute.
Sender filters read the forgeable `From` header; no SPF, DKIM, or DMARC verdict is stored today.

## 6. State

Grant: `active` → `revoked` (person, leave, handover, member removal; terminal) and `active` →
`expired` (time; terminal). The response derives `status` from `revoked_at` and `expires_at`. Drafts
have only `pending` in this slice.

## 7. Authorization Matrix

| Action | Personal/connected owner | Shared `manage` member | Shared `send`/`view` member | Org owner/admin not a manage member | Other member | API key (`mail:agent`) |
|---|---|---|---|---|---|---|
| Issue / list / revoke / activity | yes | yes | 403 | 403 (personal: 403; no access at all: 404) | 404 | no route |
| List agent drafts | yes (has send) | yes | `send`: yes, `view`: 403 | 403 unless send member | 404 | no route |
| Be the key owner of a grant | only the owner | any member with `view` | yes | only as a member with `view` | no | — |
| Feed / read / draft | — | — | — | — | — | only with an effective grant; otherwise empty feed / 404 |

`draft` capability needs the key owner to have `send` when issued and when drafting. A key without
`mail:agent` gets `403 PERMISSION.DENIED` on agent routes; a key with only `mail:agent` gets 403 on
every other key route. Test keys are refused (live keys only). Mail content never changes a grant.

## 8. HTTP Contract

| Method and path | Success | Errors |
|---|---|---|
| `GET /v1/mailboxes/{id}/agent-grants?page&page_size` | 200 paged, newest first | 404 `MAILBOX.NOT_FOUND`, 403 |
| `POST /v1/mailboxes/{id}/agent-grants` | 201 grant | 422 `MAILBOX.AGENT_GRANT_INVALID`, 409 `MAILBOX.AGENT_GRANT_LIMIT`, 409 `MAILBOX.AGENT_GRANT_EXISTS`, 403, 404 |
| `DELETE /v1/mailboxes/{id}/agent-grants/{grant_id}` | 204 (idempotent for an already revoked grant) | 404 `MAILBOX.AGENT_GRANT_NOT_FOUND`, 403, 404 |
| `GET /v1/mailboxes/{id}/agent-grants/{grant_id}/activity` | 200, newest 100 audit rows | 404, 403 |
| `GET /v1/mailboxes/{id}/agent-drafts?page&page_size` | 200 paged, newest first | 404, 403 |
| `GET /v1/agent-mail-events?after&limit` | 200 `{events, next_cursor, has_more}` | 400 `COMMON.INVALID_INPUT` (bad cursor/limit), 401, 403 |
| `GET /v1/agent-mail-messages/{message_id}` | 200 `MailMessageResponse` | 404 `MAILBOX.MESSAGE_NOT_FOUND`, 401, 403 |
| `POST /v1/agent-mail-drafts` | 201 new / 200 replaced | 404 `MAILBOX.MESSAGE_NOT_FOUND`, 403 (no `draft`), 422 `MAILBOX.MESSAGE_INVALID`, 401 |

Pseudocode of the effective-grant check, shared by all three agent routes:

```text
grants := live grants WHERE organization = viewer.org AND api_key_id = request key
for g in grants: mailbox := FindMailbox(org, g.mailbox); keep g if MailboxAccessFor(mailbox, viewer).View
feed: SELECT events WHERE grant_id IN kept AND id > after ORDER BY id LIMIT limit+1
read: event := FIRST events WHERE grant_id IN kept AND message_id = id (prefer a reveal_secrets grant)
      none -> ErrMailMessageNotFound; message := FindMessage(event.mailbox, id) (sealed rows hidden)
      audit(read | read_revealed) ; respond masked unless reveal
```

## 9. Cross-Module Data Flow

- The API key row is read by the mailbox repository (id, user, scopes, mode, expiry, revocation) at
  issue time only; the `mailbox` starter already depends on the `apikey` starter. Request-time key
  checks come from the `api_key` middleware (`apiKeyID` in the Gin context).
- Membership of the key owner comes from the organization starter seam (`membershipDirectory`).
- Leave (`MemberRemoved`), handover (`HandOverMailbox`), and member removal (`RemoveMailboxMember`)
  gain one additive call that revokes the affected grants in the same transaction and records
  `agent_revoked`. The request-time check stays the authority even if that call were missed.

## 10. Security, Privacy, And Audit

- Agent routes never mark mail read, never publish to organization webhooks, never send.
- Masking: the normal detail masking applies unless `reveal_secrets`; such reads are audited as
  `read_revealed` and logged as `mail.agent_secret_revealed`.
- Access history records `agent_granted` (grant id, key id, key owner, capabilities,
  reveal_secrets, expires_at, filter counts) and `agent_revoked` (grant id, reason).
- Audit rows store ids and the action only.

## 11. Tests (PostgreSQL)

Issuance rights: personal owner yes, colleague 404, organization admin on a member's personal
mailbox 403, shared manage member yes, shared send member 403, admin who is not a manage member 403;
key must be the owner's / a viewing member's live `mail:agent` key; expiry bounds; limit 10; duplicate
409. Feed: only granted mailboxes, sender and label filters, ascending and replayable, connector-
synced mail included, exactly one event per grant across delivery retries. Read: masked by default,
revealed with the flag and audited `read_revealed`, read state untouched. Drafts: need `draft`.
Revocation stops feed and read at once: revoke, expiry, key revoked (middleware), owner leaves,
owner loses view (member removed), handover. Scopes: `mail:agent` only cannot send; no
`mail:agent` cannot use agent routes. Migration Up/Down and golden schema.

## 12. Delivery

One pull request on `feat/connector-adapters`: migration and manifest, domain types, repository,
service, handlers, routes, hooks in leave/handover/member removal, prune command, contract
(MAILBOXES.md, API_KEYS.md, openapi.yaml, generated clients), error codes in Go, Web, Admin, and
OpenAPI, `UPGRADING.md`. Verified with `go test ./...` on PostgreSQL, `make governance`, route
catalog, Web and Admin type checks.
