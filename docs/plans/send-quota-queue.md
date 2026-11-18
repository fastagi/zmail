# Send Quota Queue Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A direct send that hits the organization's rolling 24-hour send limit is queued and sent
automatically when the window has room, instead of being rejected. People are told only "quota
full, queued"; usage numbers stay with admins.

**Architecture:** The send path stores the message as `queued` with `queued_reason = send_limit`
and answers `202`. The send limit counts outbound messages by a new `send_attempted_at` instant, so
waiting messages are not counted until they are attempted. The existing `mail:broadcast:work`
worker gains a second dispatcher that leases waiting messages oldest first per organization, within
the room the window has, and sends them through the same provider-and-record step as a direct send.

**Tech Stack:** Go (Gin, GORM, PostgreSQL, Wire), Next.js 16 / React 19 Web console, OpenAPI.

**Spec:** [docs/MAILBOX_ACCESS_PRD.zh-CN.md](../MAILBOX_ACCESS_PRD.zh-CN.md) §3.7, row "组织发送额度用量":
members blocked by the limit see only "额度已满，排队中". The user chose real queueing over a copy
change (2026-11-18). Builds on [mailbox-access.md](mailbox-access.md). Written to
[BUSINESS_IMPLEMENTATION_PLAN_STANDARD.md](../BUSINESS_IMPLEMENTATION_PLAN_STANDARD.md).

## Global Constraints

- The limit stays per organization over a rolling 24 hours (`daily_send_limit`), and failed
  attempts still count.
- A send is never reported as sent before the provider accepts it.
- Waiting messages per organization are capped at `daily_send_limit`; one more is
  `429 MAILBOX.SEND_LIMIT_REACHED` with nothing stored.
- Broadcasts keep their own pause behavior (`stop_reason` `send_limit_reached`) and never queue
  through this path.
- Responses and Web copy never show usage numbers to anyone; the Web says only that the limit is
  full and the message is queued.
- Sealed or deleted mail is never sent later: a waiting message whose mailbox is deleted or whose
  creation is before the seal boundary is canceled.
- PostgreSQL only; new migrations are frozen SQL; code and contract text are English.

## Review Focus

1. Two workers on two hosts: the lease must stop a waiting message from being sent twice.
2. A waiting message idempotency replay answers `202` with the same message, not
   `MAILBOX.SEND_IN_PROGRESS`.
3. A worker must not send more than the room the window has, even with several organizations.
4. Handover of a mailbox with waiting messages created before the boundary cancels them.
5. Deleting a waiting message (existing delete route) means it is never sent.

## 1. Executive Summary

Today a direct send at the limit is `429` and nothing is stored, which the PRD rejects for
members. After this change the send is stored and queued; the worker sends it when the window has
room. Scope: Web console send, API-key send, template test send. Out of scope: broadcasts (already
pause and resume), usage numbers for admins, retrying failed sends.

## 2. Actors and Access Surfaces

| Actor | Surface | Effect |
|---|---|---|
| Member with `send` | Web compose, reply, template test send | Gets `202` and a queued message at the limit |
| API key owner with `send` | `POST /v1/outbound-mail-messages` | Same `202` |
| Worker | `fastbuild mail:broadcast:work` | Sends waiting messages when there is room |
| Member with `send` | Delete message (existing) | Cancels a waiting message |

Access checks are unchanged: they run before anything is stored or counted.

## 3. End-to-End Workflows

1. **Queued send.** Access check, validation, idempotency lookup as today. Count attempts in the
   window; if at the limit, count waiting messages; below the cap, store `queued` +
   `queued_reason = send_limit`, `send_attempted_at` NULL, answer `202`. At the cap: `429`.
2. **Dispatch.** Each worker tick first dispatches waiting messages, then broadcasts. For each
   organization with waiting messages, room = limit − attempts in the window; lease up to
   min(room, batch) oldest waiting messages with `FOR UPDATE SKIP LOCKED`; for each, re-check the
   mailbox and seal, set `send_attempted_at` and clear `queued_reason`, call the provider, record
   the outcome and webhooks exactly as a direct send.
3. **Cancel.** Delete removes the row (never sent). Handover marks waiting messages created before
   the boundary `failed` with `failure_code` `send.canceled` in the handover transaction. A worker
   that finds the mailbox deleted or the message sealed marks it `failed` `send.canceled` without
   calling the provider.

## 4. Module Decomposition

All changes are in the mailbox module: `service.go` (send path), a new `queued_send_service.go`
(dispatcher), `repository.go` (count, lease, cancel), `seal_repository.go` (handover cancel), the
worker command in `internal/bootstrap/operatorcommands/mail_broadcast.go`, and the Web mailbox
feature.

## 5. Domain Model

`MailMessage` gains `QueuedReason string` (`""` or `send_limit`) and `SendAttemptedAt *time.Time`.
`MailQueuedSendDispatcher.DispatchQueuedSends(ctx, limit) (int, error)` is added to the domain
dispatcher interface the worker uses. Failure code `send.canceled` is added.

## 6. Persistence Design

Migration `2026_11_20_090000_add_send_queue`:

- `ALTER TABLE mail_messages ADD COLUMN queued_reason varchar(32)` (NULL when not waiting),
  `ADD COLUMN send_attempted_at timestamptz`, `ADD COLUMN send_lease_until timestamptz`.
- Backfill `send_attempted_at = created_at` for every outbound message.
- Partial index `mail_messages_waiting_idx ON mail_messages (id) WHERE queued_reason IS NOT NULL`.
- Index `mail_messages_send_attempted_idx ON mail_messages (send_attempted_at) WHERE direction = 'outbound'`.
- Down drops the indexes and columns.

## 7. State Machines

Outbound: `queued(send_limit)` → `queued` (in flight) → `sent` | `failed`; `queued(send_limit)` →
`failed(send.canceled)`; `queued(send_limit)` → deleted. Nothing returns to waiting.

## 8. HTTP Contract

No new routes. Changed answers of existing mutation routes `POST /v1/mailboxes/:mailbox_id/messages`,
`POST /v1/outbound-mail-messages` and `POST /v1/mail-templates/:template_id/tests`:

| Outcome | Response | Stored status |
|---|---|---|
| Limit reached, fewer waiting than the limit | `202` with the message | `queued`, `queued_reason: "send_limit"` |
| Limit reached and waiting messages at the cap | `429 MAILBOX.SEND_LIMIT_REACHED` (stable `error_code`) | nothing stored |

Message responses gain `queued_reason` (`"send_limit"` or `null`). An idempotent replay of a
waiting message answers `202` with it. No `error_code` is added.

```text
COMMAND SendMessage(viewer, mailbox_id, input)
LOAD mailbox in viewer organization ELSE MAILBOX.NOT_FOUND
AUTHORIZE access.Send ELSE PERMISSION.DENIED (MAILBOX.NOT_FOUND without any access)
VALIDATE input ELSE MAILBOX.MESSAGE_INVALID
LOAD existing by idempotency key; replay: waiting -> RETURN 202 message
COUNT attempts since now-24h; IF attempts >= limit:
  COUNT waiting in organization; IF waiting >= limit RETURN 429 MAILBOX.SEND_LIMIT_REACHED
  BEGIN TRANSACTION mailbox
    INSERT message status queued, queued_reason send_limit
  COMMIT
  RETURN 202 message
ELSE existing send (send_attempted_at = now) RETURN 201 | 502 | 503
```

```text
COMMAND DispatchQueuedSends(limit)
FOR EACH organization with waiting messages:
  room = daily_send_limit - attempts since now-24h; SKIP when room <= 0
  BEGIN TRANSACTION mailbox
    LEASE min(room, limit) oldest waiting rows FOR UPDATE SKIP LOCKED; set send_lease_until
  COMMIT
  FOR EACH leased message:
    LOAD mailbox; deleted or sealed -> mark failed send.canceled, continue
    BEGIN TRANSACTION mailbox
      set send_attempted_at = now, queued_reason = NULL
    COMMIT
    call provider; record outcome, events, webhooks (existing step)
RETURN number processed
```

```text
COMMAND SendOutbound(api_key_owner, from, input)
LOAD mailbox by from address in the key's organization ELSE MAILBOX.NOT_FOUND
AUTHORIZE access.Send ELSE MAILBOX.NOT_FOUND
CONTINUE as SendMessage from the idempotency lookup
RETURN 201 | 202 | 429 | 502 | 503
```

```text
COMMAND SendTemplateTest(viewer, template_id, mailbox_id, to, variables)
AUTHORIZE existing template role rule ELSE PERMISSION.DENIED; access.Send on mailbox ELSE PERMISSION.DENIED
RENDER template ELSE MAILBOX.TEMPLATE_VARIABLES_MISSING
CONTINUE as SendMessage from the limit count
RETURN 201 | 202 | 429 | 502 | 503
```

```text
COMMAND DeleteMessage(viewer, mailbox_id, message_id)
AUTHORIZE access.Send ELSE PERMISSION.DENIED (MAILBOX.NOT_FOUND without any access)
BEGIN TRANSACTION mailbox
  DELETE message (a waiting message is removed before any lease; a leased one waits for its lease to end)
COMMIT
RETURN 204
```

```text
COMMAND HandOverMailbox(viewer, mailbox_id, successor_user_id)
EXISTING handover checks and transaction
BEGIN TRANSACTION mailbox
  existing handover steps
  UPDATE waiting messages of the mailbox created before the boundary: status failed, failure_code send.canceled, queued_reason NULL
COMMIT
RETURN 201 mailbox
```

## 9. Authorization Matrix

Unchanged: send needs `send`; delete needs `send`; the worker acts as the system. A queued message
is visible exactly like any message of its mailbox.

## 10. Cross-Module Data Flow

Webhooks: no event when a message is queued; `mail.message.sent` / `mail.message.failed` when the
worker finishes it, through the existing notifier guard. A canceled message publishes nothing.

## 11. Scheduled and Asynchronous Work

`mail:broadcast:work` calls `DispatchQueuedSends` then `DispatchBroadcasts` each tick, with the
same batch flag. Lease is 2 minutes; an expired lease is leased again. A message whose attempt
started (`send_attempted_at` set) is never leased again.

## 12. Browser-Surface Plan

Compose, reply and template test send show an informational notice "Daily send limit reached —
your message is queued and will be sent automatically" (zh-Hans "额度已满，排队中，系统会自动发送")
on `202`, and close as on success. Message list and detail show status `queued` with the label
"Queued — waiting for the send limit". The mock store and Zod schemas accept `queued_reason`.

## 13. Security, Privacy, and Audit

No usage counts are added to any response. The queued notice is identical for every role. Waiting
messages keep attachments in the stored record only.

## 14. Observability and Operations

Log `mail_queued_send.dispatched` (organization, count) and `mail_queued_send.canceled` (message).
`api/docs/DEPLOYMENT.md` and `UPGRADING.md` say the broadcast worker also sends queued messages.

## 15. Test and Verification Strategy

- Service tests: queue at the limit, 429 at the cap, replay of a waiting message, broadcasts
  unaffected.
- PostgreSQL tests: count by `send_attempted_at`, lease skip-locked between two dispatchers, room
  per organization, handover cancel, delete before dispatch, deleted mailbox cancel, migration up
  and down, schema golden.
- Web tests: compose shows the queued notice on 202; list shows the queued label.
- Kest flow `api/tests/kest/mail_send_queue.flow.md` with `MAILBOX_DAILY_SEND_LIMIT=1`: first send
  201, second 202 queued, list shows it, delete cancels it, a third and fourth reach the cap 429.

## 16. Delivery Plan

One PR stacked on `feature/mailbox-access`, tasks 1 to 3 in order.

## Acceptance

- Q-1: at the limit, a member's send is `202` and the message is listed as queued.
- Q-2: the worker sends it once the window has room, and only as many as there is room for.
- Q-3: at the waiting cap the send is `429` and nothing is stored.
- Q-4: deleting a waiting message or handing over its mailbox means it is never sent.
- Q-5: the Web shows only "quota full, queued", with no numbers.

## Readiness

readiness: READY WITH RECORDED ASSUMPTIONS

Assumptions: the broadcast worker runs wherever the API sends mail (it does in self-host compose
and `scripts/dev.sh`); the product has no external API clients that depend on the `429`.

## Open Decisions

None.

---

## Implementation Tasks

### Task 1: Queue at the limit (API)

**Files:** migration `2026_11_20_090000_add_send_queue.go` (+ schema golden), `domain/mailbox.go`,
`modules/mailbox/service.go`, `repository.go`, `po.go`/mapping, `dto.go`, contract
`contracts/MAILBOXES.md`, `contracts/openapi.yaml`, regenerated `web` and `admin` OpenAPI types.

- [ ] Failing tests: `TestSendAtLimitQueues`, `TestSendAtWaitingCapIsRejected`,
  `TestReplayOfWaitingMessageAnswers202`, `TestBroadcastAtLimitStillPauses`,
  `TestCountOutboundSinceUsesAttemptTime` (PostgreSQL), migration up/down.
- [ ] Implement; handler maps a queued result to `202`.
- [ ] `go test ./internal/modules/mailbox/... ./database/...`, `make route-catalog-check`,
  contract check. Commit.

### Task 2: Dispatch and cancel (API)

**Files:** `queued_send_service.go`, `repository.go`, `seal_repository.go`,
`operatorcommands/mail_broadcast.go`, docs.

- [ ] Failing tests: `TestDispatchQueuedSendsWithinRoom`, `TestDispatchQueuedSendsSkipLocked`,
  `TestWaitingMessagesOfADeletedMailboxAreNeverSent`, `TestHandoverCancelsWaitingSends`,
  `TestDeletedWaitingMessageIsNeverSent`, `TestDispatchPublishesSentWebhook`.
- [ ] Implement; wire the worker. Run tests. Commit.

### Task 3: Web notice and labels, acceptance

**Files:** Web mailbox components, i18n `en-US.ts` / `zh-Hans.ts`, schemas, mock store, tests;
`api/tests/kest/mail_send_queue.flow.md`, `docs/testing/ACCEPTANCE_TEST_PLAN.md`.

- [ ] Failing Web tests for the notice and label; implement; `pnpm type-check lint vitest`.
- [ ] Kest flow green against a local API with `MAILBOX_DAILY_SEND_LIMIT=1`. Commit.
