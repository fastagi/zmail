# Cloudflare Self-Serve Domains Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let an organization whose DNS is anywhere connect its domain to Zmail by pointing the apex's nameservers at Cloudflare, after which Zmail sets up sending (and optionally receiving) automatically and lets the customer edit the zone's other DNS records.

**Architecture:** A new `mail_zones` table records the Cloudflare zones Zmail creates per organization. `ConnectMailDomain` resolves the apex with the public suffix list, reuses or creates the zone, and leaves the domain `pending` until the zone is `active`; a `mail:zone:work` worker polls pending zones every 10 minutes, runs the existing setup on activation, and removes zones never activated within 14 days. DNS records are read and written straight through the Cloudflare API, with mail records locked.

**Tech Stack:** Go (Gin, GORM, PostgreSQL, Wire), `golang.org/x/net/publicsuffix`, Cloudflare API v4, Next.js BFF + React (TanStack Query, shadcn), OpenAPI contract.

**Spec:** [docs/plans/cloudflare-self-serve-domains.zh-CN.md](cloudflare-self-serve-domains.zh-CN.md), extending [docs/plans/mail-domains.md](mail-domains.md) and [contracts/MAILBOXES.md](../../contracts/MAILBOXES.md) § Domains.

## Global Constraints

- Feature flag stays `MAILBOX_CUSTOM_DOMAINS_ENABLED=true`; without it every new route is `404 MAILBOX.DOMAINS_DISABLED`.
- Capabilities: `send_receive` (default) and `send_only`. `send_only` never calls Email Routing and its check lists no receiving records.
- Zone statuses: `pending`, `active`. Zones are created with `type: "full"` in the instance account (`MAILBOX_CLOUDFLARE_ACCOUNT_ID`).
- At most 3 managed zones per organization: `MAILBOX_MAX_ZONES_PER_ORGANIZATION`, default `3`. One more is `422 MAILBOX.ZONE_LIMIT_REACHED`.
- Pending-zone poll interval: 10 minutes (`mail:zone:work --poll=10m`). Pending zones older than 14 days are deleted at Cloudflare and in the database.
- An `active` managed zone is never deleted automatically, and never by deleting a mail domain.
- New error codes: `409 MAILBOX.DOMAIN_TAKEN` (reused), `409 MAILBOX.ZONE_IN_USE`, `409 MAILBOX.ZONE_STILL_DELEGATED`, `409 MAILBOX.ZONE_PENDING`, `409 MAILBOX.DNS_RECORD_LOCKED`, `422 MAILBOX.DNS_RECORD_REJECTED`, `422 MAILBOX.DNS_RECORD_INVALID`, `422 MAILBOX.ZONE_LIMIT_REACHED`, `429 MAILBOX.ZONE_CHECK_LIMITED`.
- Editable DNS record types: `A`, `AAAA`, `CNAME`, `TXT`, `MX`. Fields: `name`, `content`, `ttl` (1 = automatic, or 60–86400), `priority` (MX only, 0–65535), `proxied` (A/AAAA/CNAME only).
- Members read zones and records; owners and admins write. Every DNS write records an audit entry with type and name, never content.
- Zmail stores no copy of DNS records; Cloudflare is the source of truth.
- English-only source and API text; user copy in `web/src/i18n/modules/mailbox/{en-US,zh-Hans}.ts`. PostgreSQL only. Brand `Zmail`.
- Never send real mail from tests. Unit tests use fake providers; Cloudflare clients use `httptest`.

## Review Focus

1. Multi-label public suffixes (`example.com.cn`, `foo.co.uk`, `x.github.io`): apex must come from the public suffix list, and a name that *is* a public suffix is `422 MAILBOX.DOMAIN_INVALID` — Task 3 test `TestApexDomain`.
2. Two organizations connecting names under the same new apex at once: exactly one zone is created; the loser gets `409 MAILBOX.DOMAIN_TAKEN` and no orphan Cloudflare zone is left — Task 1 repository race test + Task 3 test `TestConnectCreatesZoneOnceUnderRace` (zone deleted when the insert loses).
3. A PATCH that renames an unlocked record onto a locked name, or a create at a locked name/type: must be `409 MAILBOX.DNS_RECORD_LOCKED`, not just edits *of* locked records — Task 6 test `TestDNSRecordLockAppliesToTarget`.
4. Deleting a managed zone whose nameservers still point to Cloudflare, or when the public NS lookup fails: refuse (`409 MAILBOX.ZONE_STILL_DELEGATED`), never assume "not delegated" on a lookup error — Task 5 test `TestDeleteZoneRefusesOnLookupError`.
5. Cloudflare rate-limits activation checks: a limited re-check during `POST …/checks` must not fail the check, only report `zone_check_limited: true` — Task 4 test `TestCheckPendingZoneToleratesActivationLimit`.

---

### Task 1: Zones schema, domain types, repository

**Files:**
- Create: `api/database/migrations/2026_11_19_100000_create_mail_zones.go`
- Modify: `api/database/migrations/testdata/schema.golden.sql`
- Create: `api/internal/domain/mail_zone.go`
- Modify: `api/internal/domain/mail_domain.go` (fields `MailZoneID *uint`, `Capability MailDomainCapability`)
- Create: `api/internal/modules/mailbox/zone_repository.go`, `zone_repository_test.go`
- Modify: `api/internal/modules/mailbox/domain_repository.go` (persist the two new columns)

**Interfaces:**
- Produces:
  - `domain.MailZoneStatus` (`MailZoneStatusPending = "pending"`, `MailZoneStatusActive = "active"`)
  - `domain.MailDomainCapability` (`MailDomainSendReceive = "send_receive"`, `MailDomainSendOnly = "send_only"`)
  - `domain.MailZone{ID, OrganizationID uint; Name, CloudflareZoneID string; Status MailZoneStatus; NameServers, OriginalNameServers []string; ActivatedAt, LastCheckedAt *time.Time; CreatedBy uint; CreatedAt, UpdatedAt time.Time}`
  - `domain.MailZoneRepository`:
    - `CreateZone(ctx, zone *MailZone, limit int) error` — `ErrMailZoneLimitReached` past limit; `ErrMailDomainTaken` on duplicate `name` or `cloudflare_zone_id`
    - `FindZone(ctx, organizationID, id uint) (*MailZone, error)` / `FindZoneByName(ctx, name string) (*MailZone, error)` — `ErrMailZoneNotFound`
    - `ListZones(ctx, organizationID uint) ([]*MailZone, error)`
    - `ListPendingZones(ctx, limit int) ([]*MailZone, error)` — oldest `last_checked_at` first (nulls first)
    - `UpdateZone(ctx, zone *MailZone) error`
    - `DeleteZone(ctx, organizationID, id uint) error` — `ErrMailZoneInUse` when any `mail_domains.mail_zone_id` references it
    - `DeletePendingZone(ctx, id uint) error` — deletes the zone and its (necessarily pending) domains in one transaction
    - `ListDomainsInZone(ctx, zoneID uint) ([]*MailDomain, error)`
  - Errors in `api/internal/modules/mailbox/errors.go`: `ErrMailZoneNotFound`, `ErrMailZoneLimitReached`, `ErrMailZoneInUse`, `ErrMailZoneStillDelegated`, `ErrMailZonePending`, `ErrMailZoneCheckLimited`.

- [ ] **Step 1: Write the failing repository tests** (PostgreSQL via `FASTBUILD_TEST_POSTGRES_DSN`, same harness as `domain_repository_test.go`):
  - `TestZoneRepositoryLimit`: 3 zones succeed for org 1, the 4th returns `ErrMailZoneLimitReached`.
  - `TestZoneRepositoryNameUnique`: a second org creating `example.com` gets `ErrMailDomainTaken`.
  - `TestZoneRepositoryConcurrentCreate`: two goroutines create `example.com` for two orgs; exactly one `nil` error.
  - `TestZoneRepositoryDeleteInUse`: a domain with `MailZoneID` set makes `DeleteZone` return `ErrMailZoneInUse`.
  - `TestDeletePendingZoneRemovesDomains`: zone + 2 pending domains → both rows gone.
  - `TestDomainCapabilityRoundTrip`: create domain with `send_only` and a zone ID, read back equal; a domain created without capability reads `send_receive`.
- [ ] **Step 2: Run** `cd api && go test ./internal/modules/mailbox/ -run 'TestZoneRepository|TestDeletePendingZone|TestDomainCapability'` — Expected: FAIL (types undefined).
- [ ] **Step 3: Migration.** `mail_zones` columns exactly as spec §4 (`name varchar(253)` unique, `cloudflare_zone_id varchar(64)` unique, `status varchar(20) NOT NULL DEFAULT 'pending'` with `CHECK (status IN ('pending','active'))`, `name_servers jsonb NOT NULL DEFAULT '[]'`, `original_name_servers jsonb NOT NULL DEFAULT '[]'`, `activated_at`, `last_checked_at`, `created_by bigint NOT NULL`, timestamps), FK to `organizations` `ON DELETE CASCADE`, index on `organization_id`. `mail_domains`: add `mail_zone_id bigint NULL REFERENCES mail_zones(id) ON DELETE NO ACTION` (index; `NO ACTION`, not `RESTRICT`, so deleting an organization can cascade to both tables in one statement) and `capability varchar(20) NOT NULL DEFAULT 'send_receive'` with `CHECK (capability IN ('send_receive','send_only'))`. Down reverses. Regenerate the golden schema with the existing golden test's update flag.
- [ ] **Step 4: Implement the repository**; limit counted inside the insert transaction with `SELECT … FOR UPDATE` on the organization row, the same way `CreateDomain` does.
- [ ] **Step 5: Run** the Step 2 command plus `go test ./database/migrations/...` — Expected: PASS.
- [ ] **Step 6: Commit** `feat(mailbox): add managed mail zones and domain capability`.

### Task 2: Cloudflare zone and DNS record client

**Files:**
- Modify: `api/internal/infra/mailtransport/cloudflare_domains.go`
- Modify: `api/internal/infra/mailtransport/cloudflare_domains_test.go`

**Interfaces:**
- Produces (added to `DomainProvider`, also implemented by `unconfiguredDomains` returning `ErrNotConfigured`):
  - `type ZoneDetails struct { ID, Name, Status string; NameServers, OriginalNameServers []string }` — `Status` is Cloudflare's (`pending`, `active`, `moved`, …).
  - `CreateZone(ctx, name string) (ZoneDetails, error)` — `POST /zones` `{"name":name,"account":{"id":accountID},"type":"full"}`; Cloudflare error code `1061` (zone exists) → `ErrZoneExists`.
  - `GetZone(ctx, zoneID string) (ZoneDetails, error)` — `GET /zones/{id}`.
  - `RequestActivationCheck(ctx, zoneID string) error` — `PUT /zones/{id}/activation_check`; HTTP 429 or Cloudflare code `1224` → `ErrActivationCheckLimited`.
  - `DeleteZone(ctx, zoneID string) error` — `DELETE /zones/{id}`.
  - `ScanRecords(ctx, zoneID string) error` — `POST /zones/{id}/dns_records/scan`.
  - `ListZoneRecords(ctx, zoneID string) ([]DNSRecord, error)` — all records, paginated `per_page=100` until `result_info.total_pages`.
  - `UpdateRecord(ctx, zoneID string, record DNSRecord) error` — `PATCH /zones/{id}/dns_records/{record.ID}`.
  - `DeleteRecord(ctx, zoneID, recordID string) error`.
  - `DNSRecord` gains `ID string`, `TTL int`, `Proxied bool`; `CreateRecord` now returns `(DNSRecord, error)` with the created ID (update the one existing caller in `setUp`).
  - Sentinel errors `ErrZoneExists`, `ErrActivationCheckLimited`; a rejected record write stays a `*DomainError`.

- [ ] **Step 1: Write failing httptest tests:** `TestCreateZoneSendsFullType` (asserts body `type":"full"` and account id; parses `name_servers`, `original_name_servers`), `TestCreateZoneExists` (code 1061 → `errors.Is(err, ErrZoneExists)`), `TestActivationCheckLimited` (429 → `ErrActivationCheckLimited`), `TestListZoneRecordsPaginates` (2 pages, 150 records), `TestUpdateRecordPatchesByID`, `TestDeleteRecord`, `TestCreateRecordReturnsID`.
- [ ] **Step 2: Run** `cd api && go test ./internal/infra/mailtransport/ -run 'Zone|Record'` — Expected: FAIL.
- [ ] **Step 3: Implement** using the existing `call` helper; extend `cloudflareEnvelope` with `result_info.total_pages`.
- [ ] **Step 4: Run** `cd api && go test ./internal/infra/mailtransport/ ./internal/modules/mailbox/` — Expected: PASS (fake provider in `domain_service_test.go` updated for the new methods).
- [ ] **Step 5: Commit** `feat(mailtransport): manage Cloudflare zones and DNS records`.

### Task 3: Connect flow with apex resolution, zone creation, capability

**Files:**
- Create: `api/internal/modules/mailbox/zone_service.go`, `zone_service_test.go`
- Modify: `api/internal/modules/mailbox/domain_service.go`, `domain_service_test.go`, `service.go` (inject `domain.MailZoneRepository`), `api/internal/infra/config/config.go` (`MaxZonesPerOrganization`, env `MAILBOX_MAX_ZONES_PER_ORGANIZATION`, default 3, must be ≥ 1), Wire provider set + `make wire`.
- Modify: `api/go.mod` only if `golang.org/x/net` is not already a direct dependency.

**Interfaces:**
- Consumes: Task 1 repository and types; Task 2 provider methods.
- Produces:
  - `apexDomain(name string) (string, error)` — `publicsuffix.EffectiveTLDPlusOne`; error → `ErrMailDomainInvalid`.
  - `ConnectMailDomain(ctx, organizationID, createdBy uint, name string, capability domain.MailDomainCapability) (*domain.MailDomain, error)` (capability `""` means `send_receive`; anything else invalid → `ErrMailDomainInvalid`).
  - `MailDomain.Zone *domain.MailZone` (not stored; filled on reads when `MailZoneID` is set).

Zone resolution order (spec §3.1 step 2): (a) `FindZoneByName(apex)` owned by this org → use it; owned by another org → `ErrMailDomainTaken`. (b) Otherwise `domainProvider.FindZone(name)` succeeds → operator zone, existing behavior, `MailZoneID` nil. (c) `ErrZoneNotFound` → `CreateZone(apex)`, `ScanRecords` (failure logged, not returned), store `MailZone` (`pending`, NS lists from Cloudflare); if the insert fails, `DeleteZone` at Cloudflare before returning the error. `ErrZoneExists` from Cloudflare → `ErrMailDomainTaken`. Then store the domain; run `startSetUp` only when the zone is operator-owned or `active`.

`setUp` skips `EnableRouting` and `evaluate` emits no receiving records when `Capability == send_only`.

- [ ] **Step 1: Write failing tests** with the fake provider:
  - `TestApexDomain`: `mail.example.com`→`example.com`; `a.b.example.com.cn`→`example.com.cn`; `foo.co.uk`→`foo.co.uk`; `com.cn`→error; `x.github.io`→`x.github.io`.
  - `TestConnectCreatesPendingZone`: provider `FindZone` → `ErrZoneNotFound`; result domain `pending`, `Zone.Status == pending`, `Zone.NameServers` from fake, `ScanRecords` called once, `EnableRouting` not called.
  - `TestConnectReusesOwnZone`, `TestConnectOtherOrgZoneTaken`, `TestConnectOperatorZoneUnchanged` (no `CreateZone` call, setup runs).
  - `TestConnectCreatesZoneOnceUnderRace`: repository `CreateZone` returns `ErrMailDomainTaken` → fake records one `DeleteZone` with the created ID; error is `ErrMailDomainTaken`.
  - `TestConnectZoneLimit`: 4th apex → `ErrMailZoneLimitReached`, no Cloudflare zone left.
  - `TestSendOnlySkipsRouting`: active zone, `send_only` → `EnableRouting` never called; records have no `receiving` purpose; all-ok verifies.
- [ ] **Step 2: Run** `cd api && go test ./internal/modules/mailbox/ -run 'TestApexDomain|TestConnect|TestSendOnly'` — Expected: FAIL.
- [ ] **Step 3: Implement.** Update existing `ConnectMailDomain` callers (handler passes `""` until Task 5).
- [ ] **Step 4: Run** `cd api && go test ./internal/modules/mailbox/... ./internal/infra/config/...` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(mailbox): create managed zones when connecting domains`.

### Task 4: Zone activation, polling worker, expiry, re-check

**Files:**
- Modify: `api/internal/modules/mailbox/zone_service.go`, `zone_service_test.go`, `domain_service.go`
- Create: `api/internal/bootstrap/operatorcommands/mail_zone.go` (+ register in `manifest.go`, test in `manifest_test.go`)
- Modify: `api/internal/domain/mailbox.go` (`MailZoneChecker` interface), `api/internal/app` wiring field `MailZones`
- Modify: `deploy/self-host/compose.yaml` (service `mail-zone-worker`, entrypoint `["/app/fastbuild", "mail:zone:work", "--batch=20", "--poll=10m"]`, same env/profile as the broadcast worker)

**Interfaces:**
- Produces:
  - `CheckPendingZones(ctx context.Context, limit int) (int, error)` — for each of `ListPendingZones(limit)`: if `now - CreatedAt > 14*24h` → `DeleteZone` at Cloudflare (404 tolerated) then `DeletePendingZone`; else `GetZone`; Cloudflare `active` → set `active`, `ActivatedAt`, then `startSetUp` every `pending` domain in the zone; always set `LastCheckedAt`. Returns zones processed. One zone's failure is logged and does not stop the batch.
  - `CheckMailDomain` on a domain whose zone is `pending`: `RequestActivationCheck` (`ErrActivationCheckLimited` → domain response field `ZoneCheckLimited bool`, not an error), then `GetZone`, then the same activation path; the domain stays `pending` with no record evaluation while the zone is pending.
  - `SetUpMailDomain` on a pending zone → `ErrMailZonePending`.
  - Command `mail:zone:work [--batch=20] [--poll=10m] [--once]` reusing `parseWebhookWorkerArgs`/`runWebhookWorker` (adapter like `mailBroadcastWorker`). Because `runWebhookWorker` only sleeps when a batch returns 0, `CheckPendingZones` returns 0 after a full pass so the loop waits `--poll`.

- [ ] **Step 1: Write failing tests:** `TestPendingZoneActivatesAndSetsUp` (fake `GetZone` → active; domain setup ran; zone `active`), `TestPendingZoneExpiresAfter14Days` (created 15 days ago → Cloudflare `DeleteZone` + rows gone; 13 days → kept), `TestActiveZoneNeverExpires`, `TestCheckPendingZoneToleratesActivationLimit`, `TestSetUpOnPendingZoneRefused`, `TestCheckPendingZonesContinuesAfterFailure`, `TestMailZoneWorkCommandRegistered`.
- [ ] **Step 2: Run** `cd api && go test ./internal/modules/mailbox/ ./internal/bootstrap/operatorcommands/ -run 'Zone'` — Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** the Step 2 command, then `cd api && bash ../.agents/skills/verification-before-completion/scripts/run-tiers.sh 1 ./internal/modules/mailbox/... ./internal/bootstrap/...` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(mailbox): activate, poll and expire managed zones`.

### Task 5: Zone, preview and receiving HTTP routes + contract

**Files:**
- Create: `api/internal/modules/mailbox/zone_handler.go`, extend `handler_test.go`
- Modify: `domain_handler.go` (request `capability`; response `capability`, `zone`, `zone_check_limited`), `routes.go`, `error_mappings.go`, `zone_service.go`
- Modify: `contracts/MAILBOXES.md` (§ Domains, new § Managed zones), `contracts/openapi.yaml`; regenerate `web/src/http/generated/openapi.ts` and `admin/src/http/generated/openapi.ts`; `cd api && make route-catalog-check` updates.

**Interfaces:**
- Produces routes (all under the existing authenticated organization group, `WhereNumber` on IDs):
  - `POST /v1/mail-domain-previews` `{name}` → `200 {name, apex, existing_mx: string[], suggested_capability}`; MX via a `mxResolver` seam (`LookupMX`), lookup failure → `existing_mx: []`, suggestion `send_receive`. Suggests `send_only` when any MX host does not end in `.mx.cloudflare.net`.
  - `GET /v1/mail-zones` (bounded-list max=3), `GET /v1/mail-zones/:zone_id` → `{id, name, status, name_servers, original_name_servers, activated_at, last_checked_at, created_at}`.
  - `DELETE /v1/mail-zones/:zone_id` → `204`; `409 MAILBOX.ZONE_IN_USE` with any domain; then public `LookupNS(name)` via an `nsResolver` seam: any NS ending `.ns.cloudflare.com` **or** a lookup error → `409 MAILBOX.ZONE_STILL_DELEGATED`; a `pending` zone skips the NS check. Then Cloudflare `DeleteZone` and the row.
  - `POST /v1/mail-domains/:domain_id/receiving` → `200` domain; zone pending → `409 MAILBOX.ZONE_PENDING`; already `send_receive` → unchanged `200`; else set capability and `startSetUp`.
  - Writes (`DELETE` zone, `receiving`, previews are reads) require owner/admin exactly like existing domain writes.

- [ ] **Step 1: Write failing handler tests:** `TestPreviewSuggestsSendOnlyForForeignMX`, `TestPreviewLookupFailureSuggestsSendReceive`, `TestListZonesScopedToOrganization`, `TestDeleteZoneInUse`, `TestDeleteZoneStillDelegated`, `TestDeleteZoneRefusesOnLookupError`, `TestDeletePendingZoneSkipsNSCheck`, `TestReceivingOnPendingZone409`, `TestReceivingUpgradesSendOnly`, `TestConnectAcceptsCapability`, `TestZoneRoutesDisabledWithoutFlag` (404 `MAILBOX.DOMAINS_DISABLED`), `TestZoneWriteRequiresAdmin`.
- [ ] **Step 2: Run** `cd api && go test ./internal/modules/mailbox/ -run 'Preview|Zone|Receiving|ConnectAccepts'` — Expected: FAIL.
- [ ] **Step 3: Implement handler, routes, mappings; write the contract text and OpenAPI; regenerate clients.**
- [ ] **Step 4: Run** the Step 2 command, `cd api && make route-catalog-check`, and the owning contract guard (`make governance` subset the contract docs name, e.g. the oasdiff check) — Expected: PASS.
- [ ] **Step 5: Commit** `feat(mailbox): expose managed zones, previews and receiving upgrade`.

### Task 6: DNS record editor API

**Files:**
- Create: `api/internal/modules/mailbox/dns_record_service.go`, `dns_record_service_test.go`, `dns_record_handler.go`
- Modify: `routes.go`, `error_mappings.go`, `contracts/MAILBOXES.md`, `contracts/openapi.yaml`, generated clients

**Interfaces:**
- Consumes: Task 2 `ListZoneRecords`/`CreateRecord`/`UpdateRecord`/`DeleteRecord`; zones and domains from Task 1.
- Produces:
  - `GET /v1/mail-zones/:zone_id/dns-records` → `{id, type, name, content, ttl, priority?, proxied, locked, locked_reason?}[]` (bounded by Cloudflare; document max 3500 per zone).
  - `POST …/dns-records` → `201`; `PATCH …/dns-records/:record_id` → `200`; `DELETE …/dns-records/:record_id` → `204`.
  - `lockReason(record DNSRecord, domains []*MailDomain) string` — non-empty when, for any mail domain `d` in the zone: type `MX` and name `== d.Name`; type `TXT` at `d.Name` starting `v=spf1`; name `_dmarc.<d.Name>`; name ending `._domainkey.<d.Name>`; or `(type, name, content)` equals one of `d.Records` with purpose `sending`. Reason strings: `mail_receiving`, `mail_spf`, `mail_dmarc`, `mail_dkim`, `mail_sending`.
  - Lock applies to the existing record (PATCH/DELETE) **and** to the requested target (POST/PATCH result) → `409 MAILBOX.DNS_RECORD_LOCKED`.
  - Validation before Cloudflare (`422 MAILBOX.DNS_RECORD_INVALID` with field): type in the allowed set; name equals the zone name or ends with `.`+zone name (input `@` or a relative label is qualified first); A is IPv4, AAAA is IPv6; TXT ≤ 2048 chars; CNAME not at the apex and no other record at the same name; MX priority required; `proxied` only on A/AAAA/CNAME; TTL 1 or 60–86400.
  - Cloudflare `*DomainError` on write → `422 MAILBOX.DNS_RECORD_REJECTED` with the provider message.
  - Audit via the module's existing audit hook: action `mail_dns_record.created|updated|deleted`, metadata `{zone, type, name}`.

- [ ] **Step 1: Write failing tests:** `TestListMarksMailRecordsLocked`, `TestDNSRecordLockAppliesToTarget` (PATCH renaming a TXT to `_dmarc.<domain>` → locked; POST MX at the mail domain → locked), `TestDeleteLockedRecord409`, `TestDNSRecordValidation` (table: bad IPv4, name outside zone, CNAME at apex, CNAME beside A, TXT 2049 chars, MX without priority, proxied TXT, TTL 30), `TestRelativeNameQualified` (`www` → `www.example.com`, `@` → `example.com`), `TestProviderRejectionMapped`, `TestDNSWriteAudited` (no content in metadata), `TestDNSRecordsMemberReadOnly`.
- [ ] **Step 2: Run** `cd api && go test ./internal/modules/mailbox/ -run 'DNS|Locked|Relative|ProviderRejection'` — Expected: FAIL.
- [ ] **Step 3: Implement; contract text and OpenAPI; regenerate clients.**
- [ ] **Step 4: Run** Step 2 command, `make route-catalog-check`, contract guard, then `run-tiers.sh 1 ./internal/modules/mailbox/...` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(mailbox): edit managed zone DNS records with mail records locked`.

### Task 7: Web BFF routes and mock store

**Files:**
- Modify: `web/src/features/mailbox/server/mailbox-route.ts`, `mock-domain-store.ts`
- Create: `web/src/features/mailbox/server/mock-zone-store.ts`
- Create: `web/src/app/api/mail-domain-previews/route.ts`, `web/src/app/api/mail-zones/route.ts`, `web/src/app/api/mail-zones/[zoneId]/route.ts`, `web/src/app/api/mail-zones/[zoneId]/dns-records/route.ts`, `web/src/app/api/mail-zones/[zoneId]/dns-records/[recordId]/route.ts`, `web/src/app/api/mail-domains/[domainId]/receiving/route.ts`
- Modify: `web/src/features/mailbox/services/mailbox-service.ts`, `hooks/use-mailbox.ts`
- Test: `web/src/test/mailbox-route.test.ts`

**Interfaces:**
- Consumes: Task 5/6 API shapes via the generated OpenAPI types.
- Produces: service functions `previewMailDomain`, `listMailZones`, `deleteMailZone`, `enableReceiving`, `listDnsRecords`, `createDnsRecord`, `updateDnsRecord`, `deleteDnsRecord`; hooks of the same names prefixed `use`. Mock: connecting an unknown apex creates a pending zone with NS `ada.ns.cloudflare.com`, `bob.ns.cloudflare.com` and two imported records (`A @ 192.0.2.10`, `CNAME www → example.com`); the first `checks` call activates it.

- [ ] **Step 1: Write failing route tests** for each new route in mock mode and proxy mode (proxy asserts upstream path and method), including `receiving` and `409 MAILBOX.DNS_RECORD_LOCKED` passthrough.
- [ ] **Step 2: Run** `cd web && corepack pnpm vitest run src/test/mailbox-route.test.ts` — Expected: FAIL.
- [ ] **Step 3: Implement** following the existing `mail-domains` adapter pattern.
- [ ] **Step 4: Run** Step 2 command and `cd web && corepack pnpm type-check` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(web): BFF routes for managed zones and DNS records`.

### Task 8: Web UI — connect, nameserver guide, receiving upgrade, DNS editor

**Files:**
- Modify: `web/src/features/mailbox/components/mail-domains.tsx`, `mail-domain-status.tsx`
- Create: `web/src/features/mailbox/components/mail-zone-nameservers.tsx`, `mail-dns-records.tsx`, `mail-domain-test-checklist.tsx`
- Create: `web/src/app/(protected)/(console)/console/mail-domain/[zoneId]/dns/page.tsx`
- Modify: `web/src/i18n/modules/mailbox/en-US.ts`, `zh-Hans.ts`
- Test: `web/src/test/mail-domains-ui.test.tsx` (create if absent)

Behavior (spec §3.1, §3.2, §3.3, §5):
- Connect form: name field; on blur call preview; capability radio defaults to `suggested_capability`; when `send_only` was suggested show the subdomain hint with the existing MX hosts.
- Pending zone panel: the two nameservers with copy buttons; registrar guides for Alibaba Cloud, Tencent Cloud DNSPod, Huawei Cloud, GoDaddy, Namecheap — the one whose NS suffix matches `original_name_servers` (`hichina.com`/`alidns.com`, `dnspod.net`, `hwclouddns`, `domaincontrol.com`, `registrar-servers.com`) opens by default; the imported-records list with a "Review records" link to the DNS page; a "Check now" button showing `zone_check_limited` as "Cloudflare limits re-checks; try again in a few minutes".
- Verified `send_only` domain: "Start receiving in Zmail" button → dialog listing the current MX hosts it will replace, then `enableReceiving`.
- Verified domain: test checklist (same-domain, to an external inbox, from an external inbox, reply), informational only.
- DNS page: table of records; locked rows show the reason and no edit/delete; add/edit dialog with type-specific fields; delete confirm; errors from `DNS_RECORD_INVALID`/`REJECTED`/`LOCKED` shown inline. Read-only for members.
- Zone "Remove from Zmail" action: shown only with no domains; on `ZONE_STILL_DELEGATED` show `original_name_servers` to restore.

- [ ] **Step 1: Write failing UI tests:** `suggests send-only for foreign MX`, `shows nameservers and opens matching registrar guide`, `check now shows rate-limit notice`, `locked DNS rows have no actions`, `member cannot add DNS records`, `receiving dialog lists current MX`, `still-delegated removal shows original nameservers`.
- [ ] **Step 2: Run** `cd web && corepack pnpm vitest run src/test/mail-domains-ui.test.tsx` — Expected: FAIL.
- [ ] **Step 3: Implement** with existing shadcn components; all copy through i18n in both locales.
- [ ] **Step 4: Run** Step 2 command, `cd web && corepack pnpm type-check && corepack pnpm lint` — Expected: PASS. Then start the web dev server in mock mode and walk connect → check → active → DNS edit in the built-in browser.
- [ ] **Step 5: Commit** `feat(web): self-serve domain onboarding and DNS editor`.

### Task 9: Operator docs and acceptance runbook

**Files:**
- Modify: `docs/plans/mail-domains.md` (link to the new design), `deploy/self-host/README*` or the existing env reference (new env var, `mail-zone-worker`, token permission **Account → Zone: Edit** added to `DNS Edit`, `Zone Settings Edit`, `Email Routing Rules Edit`, `Email Sending Edit`)
- Modify: `docs/DEPLOYMENT_FE5_0083.md` (worker service and token change on fe5)
- Create: `docs/runbooks/self-serve-domain-acceptance.md` — the real acceptance from spec §8: a non-Cloudflare test domain, change NS, wait for active, create a mailbox, send to an external Gmail inbox, confirm `spf=pass dkim=pass dmarc=pass` in the original headers, receive a reply; then `send_only` → receiving upgrade.

- [ ] **Step 1: Write the docs.**
- [ ] **Step 2: Run** `make agent-check-changed` — Expected: PASS.
- [ ] **Step 3: Commit** `docs: operate and accept self-serve domains`.

Final gate before merge: `make check` once.
