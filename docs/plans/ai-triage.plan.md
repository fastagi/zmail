# AI Triage (Slice 3, Offline Part): Implementation Plan

Spec: [ai-triage.zh-CN.md](ai-triage.zh-CN.md). This plan builds only what §9 allows before a real
AI provider is confirmed: the data model, the switches (all default off), the `TriageModel` seam
with the `disabled` (default) and `fake` implementations, enqueue on inbound mail, the triage
worker, the override and reply-draft endpoints, the unified inbox `triage` filter/field/counts,
the organization and mailbox switches, and the Web skeleton. No real provider, no SDK dependency,
no API key is read.

Owner: the `mailbox` starter (API) owns every rule and record; `contracts/` joins it to `web/`.
Admin is unchanged apart from regenerated contract types.

## Rulings

| # | Ruling |
|---|---|
| R1 | Everything lives in the `mailbox` starter in new `triage_*.go` files plus `domain/mail_triage.go`; existing files only gain hooks (enqueue call, inbox filter/field/counts, routes, manifest). This keeps the concurrent `feat/shared-mailbox-admin` branch conflict surface small. |
| R2 | Config: `AI_TRIAGE_PROVIDER` (`disabled` default, `fake`), `AI_TRIAGE_MODEL` (a label stored on results; empty takes the provider name), `AI_TRIAGE_DAILY_CAP` (instance ceiling of model calls per organization per UTC day, default 500, 1..100000). `AI_TRIAGE_API_KEY` from the spec is **not** read until a provider is confirmed. `fake` with `APP_ENV=production` fails config validation, so the server and every CLI command refuse to start. |
| R3 | Tables: `mail_triage` (one row per inbound message, `message_id` primary key cascading from `mail_messages`), `mail_triage_settings` (one row per organization), and `mailboxes.ai_triage_enabled boolean NOT NULL DEFAULT false` (metadata-only on PostgreSQL 11+). `mail_triage` also has `next_attempt_at` (backoff) and `category` is null until a result exists. The pending index is `(next_attempt_at, message_id) WHERE status = 'pending'`; the count index is `(mailbox_id, category) WHERE status IN ('done','failed')`. |
| R4 | Visible results are `done` and `failed` rows (`failed` always carries `review`). `pending` and `skipped` rows are never shown. |
| R5 | Backoff: a failed model call counts an attempt; the next try waits 1 minute after the 1st failure and 5 minutes after the 2nd; the 3rd failure marks the row `failed`/`review`. The 30-minute wait applies when the model reports `ErrTriageUnavailable` (provider `disabled`): the row is deferred 30 minutes without counting an attempt. |
| R6 | Daily cap: before each `Classify` call the worker reserves one call on the organization's settings row in one `UPDATE … RETURNING` (`usage_day`, `usage_count`), against `min(daily_message_cap, AI_TRIAGE_DAILY_CAP)`. An exhausted cap defers the row to the next UTC midnight without counting an attempt. |
| R7 | At claim time a row whose organization switch or mailbox switch is now off becomes `skipped` (no model call). |
| R8 | Endpoints: `GET/PUT /v1/mail-triage-setting` (read: any member; write: owners/admins); `PUT /v1/mailboxes/{id}/triage-setting` (personal: only its owner, never an admin; shared: `manage`); `PUT /v1/mailboxes/{id}/messages/{id}/triage` (`view`); `POST /v1/mailboxes/{id}/messages/{id}/reply-drafts` (`send`). Writes and drafts need the provider available (and the org switch on for override/draft; `draft_enabled` for drafts), else `409 MAILBOX.TRIAGE_UNAVAILABLE`. |
| R9 | `PUT /v1/mail-triage-setting` replaces all three fields; `daily_message_cap` is required (1..100000), so the model is never called without an explicit cap row. |
| R10 | Override accepts `reply`, `deadline`, `fyi` (not `review`, which is the model's "unsure"); `due_at` only with `deadline`. It upserts a `done` row with `source=manual`, `decided_by`, an empty reason, and works on any inbound message of the mailbox (pending or never triaged). An outbound message is `422 COMMON.VALIDATION_FAILED`. |
| R11 | Unified inbox: `triage=<category>[,<category>…]` filters (the Web's "needs reply" tab sends `reply,review`); only with `view=all` or `today`, never with `group`. Each row gains nullable `triage {category, reason, due_at, source}`. Counts gain `triage {reply, deadline, fyi, review}` of unread Inbox mail, **null** when the organization switch is off or no provider is configured (the Web renders the tabs only when it is non-null). |
| R12 | Message detail is unchanged; the reading pane takes the row's `triage`. Results follow message visibility because every read path goes through `authorizeMailbox` or the inbox's readable-mailbox set. |
| R13 | Model input is built only from `maskInboundSecrets` output (subject and text; HTML-only mail uses the masked HTML stripped to text), truncated to 8000 characters, placed in a JSON data block after fixed instructions that say the data is untrusted and must not be followed. Few-shot examples are the mailbox's 10 newest manual overrides (masked subject + category). The stored reason is masked again and capped at 120 characters; an invalid category, reason, or due date yields `review`. |
| R14 | Drafts (D2): never stored. Input is the masked thread (newest 10 messages, oldest first) plus the organization's standard template names; `basis` items `{kind: message|template, id, label}` are kept only when they name an input item. A draft daily cap waits for §9 item 2. |
| R15 | Worker command `mail:triage:work [--batch=20] [--poll=5s] [--once]` (repository naming convention instead of the spec's `mail:triage-worker`). It is not added to Compose while the provider is `disabled`. |
| R16 | A handover (personal to shared) turns the mailbox switch off: the previous owner's consent does not carry over. Enqueue also requires a personal mailbox's owner to still be a member. |
| R17 | The `fake` model: `?`, the full-width question mark, the Chinese for "can you" (`\u80fd\u4e0d\u80fd`), or "can you" → `reply`; a date followed by the Chinese for "before" (`\u524d`), or "before"/"by" followed by a date → `deadline` (due date parsed when it is `YYYY-MM-DD`); otherwise `fyi`. Its draft quotes nothing from the body. |
| R18 | Web: the new response fields (`Mailbox.ai_triage_enabled`, inbox row `triage`, counts `triage`) are optional in the strict Zod schemas, following the `role_id` precedent, so an older API still parses; a missing value reads as off. |
| R19 | Web: the override menu opens with Shift+F (shown as `⇧F`), because `f` is already Forward. The menu offers reply/deadline/fyi only. |
| R20 | Web: three triage tabs ("needs your reply" = `reply`+`review`, "has a deadline", "just so you know") plus "everything", only for `all`/`today` without a search, and only while counts carry `triage`; a tab lists single messages (no thread grouping). The URL keeps the tab as `triage=`. "Reply with this draft" opens the quoted reply with the draft above the quote. |
| R21 | Mock BFF: it stands in for the `fake` provider (`available: true`) and computes results when read, from subject and snippet, so it also covers mail received before the switches were turned on; a reader's override wins. The org settings page and mailbox switch UI (spec §7 "management") are left for after a provider is confirmed; the BFF routes and services for them exist. |

## Tasks

1. **Migration** `2026_11_26_090000_add_mail_triage` (+ manifest, golden schema, chain tests).
   Tests: `TestMailTriageMigrationUpDown`, `TestMailTriageConstraints`; regenerated golden schema.
2. **Config** `AI_TRIAGE_*` in `internal/infra/config/ai_triage.go`, `.env.example`.
   Tests: `TestAITriageConfigDefaultsOff`, `TestAITriageFakeRefusedInProduction`,
   `TestAITriageConfigRejectsUnknownProviderAndCap`.
3. **Model seam** `triage_model.go`: `TriageModel`, `disabled`, `fake`, input building, output validation.
   Tests: `TestDisabledTriageModelIsUnavailable`, `TestFakeTriageModelRules`,
   `TestTriageInputIsMaskedAndBounded` (asserts no code in any model input field),
   `TestValidateTriageOutput`, `TestTriagePromptKeepsMailAsData`.
4. **Repository + enqueue** `triage_repository.go`, hook in `ReceiveInbound`.
   Tests: `TestTriageEnqueueNeedsBothSwitches`, `TestTriageEnqueueSkipsFormerOwnersMailbox`,
   `TestHandoverTurnsTriageOff`.
5. **Worker** `triage_worker.go` (`DispatchTriage`), command `mail:triage:work`.
   Tests: `TestTriageWorkerClassifiesPending`, `TestTriageWorkerSkipsLockedRows`,
   `TestTriageWorkerBackoffThenFailed`, `TestTriageWorkerDailyCap`,
   `TestTriageWorkerDefersWhenUnavailable`, `TestTriageWorkerSkipsSwitchedOff`,
   `TestTriageWorkerSendsOnlyMaskedInput`, `TestMailTriageWorkCommandRegistered`.
6. **Switches, override, draft service + HTTP** `triage_service.go`, `triage_handler.go`.
   Tests: `TestTriageSettingNeedsOwnerOrAdmin`, `TestTriageSettingUnavailableWithoutProvider`,
   `TestMailboxTriageSwitchPermissions`, `TestTriageOverridePermissionsAndValidation`,
   `TestReplyDraftNeedsSendAndIsNotStored`, `TestTriageHTTPFlow`.
7. **Inbox** filter, field, counts.
   Tests: `TestInboxTriageFilterFieldAndCounts`, `TestInboxTriageHiddenWhenOrganizationOff`,
   `TestParseInboxRequestTriage`.
8. **Contract** `contracts/openapi.yaml`, `contracts/MAILBOXES.md`, error catalogs;
   `make contract-generate`, `make contract-check`, `make starter-catalog`, route catalog, UPGRADING.md.
9. **Web**: schemas/types/service/hooks, BFF routes (mutations through the mailbox mutation
   resolver), mock store support, inbox tabs + reason line + draft block + F override menu,
   i18n. Tests: `web/src/test/ai-triage-route.test.ts`, `web/src/test/ai-triage-ui.test.tsx`,
   hook mocks added to existing UI tests.
10. **Kest** `api/tests/kest/ai_triage.flow.md` + `.kestenv`: switches → signed inbound →
    `mail:triage:work --once` → triage with reason → counts → override → draft.
11. **Full `make check`** from the worktree root.
