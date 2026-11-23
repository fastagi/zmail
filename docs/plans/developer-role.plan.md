# Developer Access Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task (decided: the planner executes it inline, TDD per task). Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let an owner or admin grant one member a "developer" feature, scoped to sending domains, that opens the developer console (transactional templates, webhooks, suppressions, usage, API keys) without any organization-management power.

**Architecture:** A new table `member_developer_grants` (one row per membership) holds the grant and its sending scope; `MemberFeatures.Developer` is derived from it when memberships and request contexts are loaded. Owner/admin-only checks in the mailbox, webhook, and usage modules widen to "manager, or member with the developer grant", and a single domain value `DeveloperGrant.AllowsDomain` decides scope everywhere (template writes, API key creation, transactional sends, webhook fan-out). The Web adds the toggle with a domain picker and replaces the console's raw 403s with an explanation page.

**Tech Stack:** Go (Gin, GORM, Wire, PostgreSQL), Next.js 16 / React 19 / zod-mini schemas / next-intl, Vitest.

**Spec:** `docs/plans/developer-role.zh-CN.md` (§11 items 1–4 approved as written; the "decided by me" items are settled).

## Global Constraints

- PostgreSQL only; versioned migrations are SQL, never `AutoMigrate` of a live PO. Migration timestamp `2026_12_28_090000` (others in flight use `2026_12_27_*`).
- Error code `DEVELOPER.DOMAIN_NOT_ALLOWED`, HTTP 403; synced to `web` and `admin` `codes.ts` and the openapi `ErrorCode` enum.
- Owners and admins have every developer capability with no scope (`EffectiveFeatures` convention).
- The developer grant includes the API keys grant; turning developer off keeps a separately granted `api_keys`.
- "All verified domains" is dynamic; the Zmail shared platform domain is never in a developer scope.
- Webhooks: a member-created endpoint is managed only by its creator (and owners/admins). Transactional templates are governed by sender domain, not creator; no creator column on templates.
- Developer webhooks receive only `email.*` (transactional/API) events whose sender domain is in the creator's current scope.
- Suppression removal is not domain-limited and is always audited with the actor.
- Usage and send lanes: whole-organization numbers, read-only for developers.
- UI copy in Chinese (next-intl, zh and en files kept structurally aligned); source code English; brand Zmail.
- Do not touch until told: `web/src/features/organization/components/organization-members.tsx`, `roster-import-dialog*`, `api/internal/modules/mailbox/roster_service.go`, `roster_handler.go`, `api/internal/domain/roster_import.go`, mail domain settings page components.

## Rulings made while planning (not in the spec)

1. Superseded after merging origin/main, which added `mail_domains.is_default`: the grant dialog preselects the organization's default mail domain (else the first verified one), and invitations or roster rows carrying developer access get a grant for the default domain at accept time (none while no domain is verified). The API requires either `all_domains=true` or at least one domain.
2. Grant domains must be verified custom domains of the organization at grant time (`DEVELOPER.DOMAIN_NOT_ALLOWED` otherwise). A domain that later stops being verified simply stops matching.
3. Transactional sends by a member's key (and console test sends by a member) need the developer grant; without it `403 ORGANIZATION.FEATURE_NOT_GRANTED`. Mailbox (`mail:send`) sends are unchanged: they are governed by mailbox access.
4. A developer creating an organization-bound key with no `sending_domains` gets the scope's explicit list copied in; with `all_domains` the key stays unrestricted by domain (the send-time check enforces the live scope).
5. `mail.message.*` (mailbox) events are never delivered to member-created endpoints; a member-created endpoint stops receiving events while its creator lacks the grant, and stays visible/manageable by admins.
6. A developer lists only the endpoints and deliveries of endpoints he created.
7. The developer flag is exposed as `features.developer`; the scope as `developer_scope: {all_domains, domains} | null` beside `features` on member, organization, and context responses.

## Review Focus

1. Admin narrows a scope after a key/template exists → next send through that developer's key with an out-of-scope template is refused (`DEVELOPER.DOMAIN_NOT_ALLOWED`); test in Task 4.
2. Developer edits an in-scope template to move its sender out of scope, or edits an out-of-scope template → refused; test in Task 4.
3. Grant turned off while API keys still granted → `mail:send` still works, transactional refused; test in Task 4.
4. Member removed from the organization / role changed to admin → grant row must not make a later re-join a developer, and an admin row ignores scope; test in Task 3 (grant deleted with the membership via `ON DELETE CASCADE`; role promotion keeps the row but `EffectiveFeatures` wins).
5. Developer tries to mutate an admin-created webhook by id → 404-style not found (no existence leak) ; test in Task 6.

---

### Task 1: Domain values, error code

**Files:**
- Modify: `api/internal/domain/member_feature.go`, `api/internal/domain/organization_context.go`, `api/internal/domain/organization.go`, `api/internal/domain/errors.go`, `api/internal/domain/error_codes.go`
- Create: `api/internal/domain/developer_grant.go`, `api/internal/domain/developer_grant_test.go`
- Test: `api/internal/domain/member_feature_test.go` (existing or new)

**Interfaces — Produces:**
- `const MemberFeatureDeveloper MemberFeature = "developer"` (last in `MemberFeatureCatalog()`); `MemberFeatures.Developer bool`; `MemberFeaturePatch.Developer *bool`; `Has(MemberFeatureAPIKeys)` is true when `APIKeys || Developer`.
- `type DeveloperGrant struct { AllDomains bool; Domains []string }` and `func (g DeveloperGrant) AllowsDomain(name string, verifiedOrganizationDomain bool) bool` — lowercases/trims trailing dot; `AllDomains` → `verifiedOrganizationDomain`; else name ∈ Domains.
- `func AddressDomain(address string) string` (lowercase part after last `@`, "" if none).
- `OrganizationContext.DeveloperGrant *DeveloperGrant` (nil without grant) and `func (c OrganizationContext) DeveloperScope() (grant DeveloperGrant, unrestricted bool, ok bool)`: managers → unrestricted; member with `Features.Developer` and grant → grant; else ok=false.
- `OrganizationMembership.DeveloperGrant *DeveloperGrant`.
- `ErrDeveloperDomainNotAllowed`, `CodeDeveloperDomainNotAllowed = "DEVELOPER.DOMAIN_NOT_ALLOWED"`.
- `MemberDeveloperGrantChange` carried in `OrganizationMembershipFeatureChange` as `BeforeGrant *DeveloperGrant`.

- [ ] Step 1: tests — `TestDeveloperGrantAllowsDomain` (explicit list case-insensitive, `FAST-UI.com.` matches `fast-ui.com`; all-domains needs verified flag; empty name never allowed), `TestMemberFeaturesDeveloperImpliesAPIKeys`, `TestOrganizationContextDeveloperScope` (owner unrestricted; member without grant not ok).
- [ ] Step 2: run `cd api && go test ./internal/domain/ -run 'Developer|MemberFeatures'` → FAIL (undefined).
- [ ] Step 3: implement.
- [ ] Step 4: run → PASS; `go build ./...` fixes any exhaustive switch fallout.
- [ ] Step 5: commit `feat(domain): developer feature and sending scope`.

### Task 2: Migration `2026_12_28_090000_add_member_developer_grants`

**Files:**
- Create: `api/database/migrations/2026_12_28_090000_add_member_developer_grants.go`, `api/database/migrations/member_developer_grants_test.go`
- Modify: `api/database/migrations/testdata/schema.golden.sql`, `api/internal/modules/organization/provider.go` (manifest migration name), `UPGRADING.md`

Table: `member_developer_grants(id bigserial PK, membership_id bigint NOT NULL UNIQUE REFERENCES organization_memberships(id) ON DELETE CASCADE, organization_id bigint NOT NULL REFERENCES organizations(id) ON DELETE CASCADE, all_domains boolean NOT NULL DEFAULT false, sending_domains text NOT NULL DEFAULT '[]' (JSON array), granted_by bigint NOT NULL, created_at, updated_at timestamptz NOT NULL)`, index `(organization_id)`, check `chk_member_developer_grants_scope CHECK (all_domains OR sending_domains <> '[]')`. Down drops the table. Creating a table with FKs takes `SHARE ROW EXCLUSIVE` on the referenced tables briefly — note in UPGRADING.

- [ ] Step 1: test `TestAddMemberDeveloperGrantsUpDown` — insert grant, cascade on membership delete, check refuses empty scope, down removes, up-after-down equals golden.
- [ ] Step 2: run → FAIL. Step 3: implement + regenerate golden (`UPDATE_GOLDEN`/documented flag in `schema_golden_postgres_test.go`). Step 4: run → PASS. Step 5: commit.

### Task 3: Organization module — grant persistence and responses

**Files:**
- Modify: `api/internal/modules/organization/{model.go,member_feature_repository.go,context_repository.go,membership_repository.go,repository.go,membership_service.go,dto.go,membership_handler.go,error_mappings.go}`
- Create: `api/internal/modules/organization/developer_grant.go` (PO `MemberDeveloperGrantPO`, load helpers)
- Test: `member_feature_repository_test.go`, `context_repository_test.go`, `service_test.go`

**Interfaces — Consumes:** Task 1. **Produces:**
- `UpdateOrganizationMemberFeaturesRequest` gains `Developer *bool json:"developer"`, `DeveloperAllDomains *bool json:"developer_all_domains"`, `DeveloperDomains []string json:"developer_domains"` (max 50). Scope fields are only valid with `developer=true` or while a grant exists (changing scope alone is allowed).
- `domain.MemberFeaturePatch.DeveloperGrant *DeveloperGrant` passed to `UpdateMemberFeatures`; repository upserts/deletes the grant row in the same transaction, validating domains against `SELECT lower(name) FROM mail_domains WHERE organization_id=? AND status='verified'` (ruling 2) → `ErrDeveloperDomainNotAllowed`.
- `ResolveContext` LEFT JOINs the grant (sets `Features.Developer`, `DeveloperGrant`).
- Member list, organization list, and context responses include `features.developer` and `developer_scope`.
- Audit `change_features` includes `developer` and `developer_scope` before/after.

- [ ] Step 1: repo tests on Postgres — grant with explicit domain stores row and resolves context; unverified/foreign domain refused; `developer=false` deletes row; owner/admin target → `MEMBER_FEATURES_FIXED`; list members reports scope; removing the member deletes the row.
- [ ] Step 2: run `go test ./internal/modules/organization/...` → FAIL. Step 3: implement. Step 4: PASS. Step 5: commit.

### Task 4: Mailbox authorization — templates, transactional sends, suppressions, lanes

**Files:**
- Create: `api/internal/modules/mailbox/developer_scope.go`, `developer_scope_test.go`
- Modify: `transactional_template.go`, `template_service.go`, template handler/DTO (list marker), `transactional_service.go`, `transactional_handler.go`, `suppression_service.go`, `send_lane_service.go`, `routes.go`, `error_mappings.go`

**Interfaces — Produces:**
- `func (s *service) requireDeveloperSender(ctx context.Context, viewer domain.OrganizationContext, address string) error` — managers pass; non-developer member → `domain.ErrOrganizationFeatureNotGranted`-equivalent (`ErrPermissionDenied` mapped by existing path is not enough: use the error that maps to `ORGANIZATION.FEATURE_NOT_GRANTED`); out of scope → `ErrDeveloperDomainNotAllowed`. Verified check via `s.domains.FindDomainByName` (org match, status verified, not `settings.Domain`).
- `func (s *service) developerCanManage(viewer) bool` used by suppressions and lanes (manager or `CanUse(Developer)`).
- `requireTemplateWriter` becomes a service method: standard kind needs `CanUse(Templates)`; transactional needs `requireDeveloperSender` on the new sender and (update/delete) the stored sender.
- Template responses gain `outside_sending_scope bool` (true only for a developer member viewer on an out-of-scope transactional template).
- `TransactionalActor` gains `Viewer domain.OrganizationContext`; `SendTransactional` and `SendTransactionalTest` call `requireDeveloperSender(template.FromAddress)` right after loading the template.
- Routes: template writes use `requireFeature(Templates, Developer)`, template reads add `Developer`.

- [ ] Step 1: service tests (existing fakes): developer creates in-scope transactional template; out-of-scope create/update/delete refused with `ErrDeveloperDomainNotAllowed`; member without grant refused; send via developer key out of scope refused after narrowing; grant off + api_keys on → transactional refused, standard outbound unaffected; developer lists/removes suppressions (audit records); developer reads lanes; plain member still 403.
- [ ] Step 2: FAIL. Step 3: implement. Step 4: `go test ./internal/modules/mailbox/...` PASS. Step 5: commit.

### Task 5: API key creation within the developer scope

**Files:** `api/internal/domain/api_key.go` (interface), `api/internal/modules/apikey/service.go`, `service_test.go`, `api/internal/modules/mailbox/api_key_restriction.go`, error mapping for the new code in apikey.

**Interfaces — Produces:** `SendingDomainDirectory.DeveloperSendingScope(ctx, organizationID, userID uint) (grant domain.DeveloperGrant, limited bool, err error)` — limited only for a member with the developer grant; mailbox implements it via `memberships.FindForUser` + grant. `resolveRestriction`: when limited, requested domains must satisfy `AllowsDomain` (all-domains: requested ⊆ verified org domains, shared domain excluded) else `ErrDeveloperDomainNotAllowed`; omitted domains → ruling 4.

- [ ] Steps: tests (in-scope ok and auto-filled; out-of-scope refused with new error; admin unchanged) → FAIL → implement → PASS → commit.

### Task 6: Webhooks — creator ownership and developer audience

**Files:** `api/internal/modules/webhook/{handler.go,service.go,repository.go,dto.go,model.go}`, tests; `api/internal/domain/webhook.go`; `api/internal/modules/mailbox/webhook_events.go`.

**Interfaces — Produces:**
- `webhookManager` admits managers and developer members; passes `ownerID uint` (0 for managers) to the service; list/mutate/test/deliveries/attempts filter `created_by = ownerID` when non-zero; a foreign endpoint is `ErrWebhookEndpointNotFound`.
- Endpoint responses gain `created_by` (user id).
- `domain.WebhookEvent.Origin *WebhookEventOrigin{ Programmatic bool; SenderDomain string; SenderDomainVerified bool }`; `NotifyTransactional` sets it (verified = domain ≠ instance shared domain). `subscribedEndpoints` drops member-created endpoints unless the creator is currently a developer whose grant `AllowsDomain` (ruling 5); owner/admin-created endpoints unchanged.

- [ ] Steps: repo tests (Postgres) for fan-out filter (in-scope delivered, out-of-scope not, `mail.message.*` not, admin endpoint gets all, grant removed → not); handler tests for ownership → FAIL → implement → PASS → commit.

### Task 7: Usage read for developers

**Files:** `api/internal/modules/usage/handler.go`, `handler_test.go`. `OrganizationList` admits `CanUse(MemberFeatureDeveloper)`.

- [ ] Steps: test developer 200, plain member 403 → implement → PASS → commit.

### Task 8: Contracts, openapi, error codes, catalogs

**Files:** `contracts/MAILBOXES.md`, `contracts/WEBHOOKS.md`, organization/API key contracts where features are documented, openapi ErrorCode enum + schemas, `web/src/http/**/codes.ts`, `admin/src/**/codes.ts`, route catalog / starter catalog if the gate asks, web/admin error translations.

- [ ] Steps: update; run contract guards (`make agent-check-changed`, openapi tests in api, `corepack pnpm vitest run src/test` contract tests) → PASS → commit.

### Task 9: Web — grant toggle and domain picker

**Files:** `web/src/features/organization/{schemas.ts,types.ts,utils/member-features.ts,components/member-feature-toggles.tsx,services/organization-service.ts}`, new `components/developer-grant-dialog.tsx`, BFF `src/app/api/organizations/[organizationId]/members/[memberId]/features/route.ts` + its server route/mock, messages `src/i18n/messages/{zh,en}/organization.json` (or equivalent), tests beside utils/components.

Behavior: `developer` appears last in `MEMBER_FEATURES`; turning it on opens the dialog (an "every verified domain" checkbox + multi-select of verified non-shared domains from `useMailboxDomains`, first preselected); saving sends `{developer:true, developer_all_domains, developer_domains}`. The read-only chip reads "Developer · fast-ui.com" (or "Developer · all domains"), localized. The API toggle shows on and disabled with an "included in developer access" hint while developer is on.

- [ ] Steps: Vitest for schema parsing and `developerScopeLabel()` util and toggle dialog submit → FAIL → implement → PASS; `corepack pnpm type-check && corepack pnpm lint` → commit.

### Task 10: Web — developer console for developers

**Files:** `web/src/app/(protected)/(console)/console/developers/page.tsx`, `web/src/features/developer/components/{developer-console.tsx,quick-start-tab.tsx,transactional-templates-tab.tsx}`, webhook endpoint schema (`created_by`), new `developer-access-required.tsx`, messages.

Behavior: page gate uses `developer`; fallback explains that an admin must grant developer access, with a contact-your-admin hint (no raw 403). Quick start limits example domains to the scope for a developer member. Templates tab badges `outside_sending_scope` (sender domain no longer in your scope) and disables edit/delete there.

- [ ] Steps: Vitest for gate fallback and scope filtering → FAIL → implement → PASS → type-check/lint → commit.

### Task 11: Gate, push, PR

- [ ] Run `PATH=/opt/homebrew/opt/node@24/bin:$PATH bash /Users/stark/item/zmail/.claude/ops/gate.sh <worktree> <worktree>/.superpowers/gate.log`; pass only on "All tiers (0+1+2) passed".
- [ ] Push `feat/developer-access`; `gh pr create` (draft while Task 12 waits).

### Task 12 (LAST, blocked on feat/welcome-resend in origin/main): invite and roster developer option

- [ ] When `git log origin/main --oneline | grep -i resend` shows the merge: merge origin/main; add developer (+ default scope = the default mail domain at accept) to invitations (`invitation_service.go`, invitation PO/migration column or grant on accept), roster `developer=yes`, invite dialog option, members-list chip; tests; gate; push.
