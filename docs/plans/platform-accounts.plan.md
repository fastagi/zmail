# Platform Account Ledger And Owner Handover: Implementation Plan

Spec: [external-accounts.zh-CN.md](external-accounts.zh-CN.md) §2 (part B). Backend, contract, and
the Web BFF/service/hooks layer only; the console page waits for the admin area skeleton.

## Rulings

| # | Ruling |
|---|---|
| R1 | The ledger lives in the `mailbox` starter (new `platform_account_*.go` files): visibility depends on mailbox readability, and the migration joins the mailbox manifest. |
| R2 | Routes: `GET/POST /v1/platform-accounts`, `PUT/DELETE /v1/platform-accounts/:account_id`, `GET /v1/platform-accounts/:account_id/events`, `POST /v1/platform-account-handovers`. |
| R3 | `status` defaults to `active`; `PUT` replaces every field (as mail roles do); the last write wins. |
| R4 | Event columns are `from_value`/`to_value` in SQL (`from`/`to` are reserved words) and `from`/`to` in JSON. Owner and mailbox events carry ids as text. |
| R5 | Deleting writes a `deleted` event (a fifth kind) and hard-deletes the account. `platform_account_events.account_id` has no foreign key so the deletion record survives; the events route of a deleted account is `404`. |
| R6 | Members see an account when they can read (`view`) its registration mailbox or own it. Owners and admins see all. A member who owns an account whose mailbox they cannot see gets `mailbox_address: null`, and `q` matches the registration address only through mailboxes the caller may read. |
| R7 | A member leaving the organization keeps `owner_user_id` (the handover finds their accounts); the API does not clear it. |
| R8 | Handover: owners/admins; `to_user_id` must be a current member, `from_user_id` may be a former one; `from == to` is `422`. Answers `201` with the moved account ids; zero accounts is still `201`. |
| R9 | New error codes `MAILBOX.PLATFORM_ACCOUNT_NOT_FOUND` (404) and `MAILBOX.PLATFORM_ACCOUNT_TAKEN` (409). Field errors are `422 COMMON.VALIDATION_FAILED`; a bad query filter is `400 COMMON.INVALID_INPUT`. |
| R10 | Listing order: `lower(platform)`, `lower(handle)`, `id`; page/per_page envelope like the access history. Events newest first. |

## Tasks

1. Migration `2026_11_25_120000_create_platform_accounts` (new tables only).
   Tests: `TestPlatformAccountsMigrationUpDown`, `TestPlatformAccountConstraints`; golden schema.
2. Domain types and repository (`platform_account_repository.go`).
   Tests: `TestPlatformAccountRepositoryCreateFindList`, `TestPlatformAccountUniqueIgnoringCase`,
   `TestPlatformAccountRepositorySearchAndFilters`, `TestPlatformAccountRepositoryHandover`,
   `TestPlatformAccountRepositoryDeleteKeepsEvent`.
3. Service (`platform_account_service.go`): validation, permissions, visibility, events.
   Tests: `TestPlatformAccountWriteNeedsOwnerOrAdmin`, `TestPlatformAccountValidation`,
   `TestPlatformAccountMemberVisibility`, `TestPlatformAccountUpdateRecordsEvents`,
   `TestPlatformAccountHandover`, `TestPlatformAccountDelete`.
4. Handler, DTOs, routes, error mapping (`platform_account_handler.go`).
   Tests: `TestPlatformAccountHTTPFlow`, `TestPlatformAccountHTTPErrors`.
5. Contract: `contracts/openapi.yaml`, `contracts/MAILBOXES.md` section "Platform Accounts",
   error catalogs; `make contract-generate`, `make contract-check`, starter catalog, UPGRADING.md.
6. Web: schemas, types, `platform-account-service.ts`, hooks, BFF routes in `mailbox-route.ts`,
   mock store `mock-platform-account-store.ts`.
   Tests: `platform-account-schema.test.ts`, `platform-account-route.test.ts`, parity additions.
7. Kest `api/tests/kest/platform_accounts.flow.md`: create, search by registration address,
   member visibility, handover with events.
8. Full `make check`.
