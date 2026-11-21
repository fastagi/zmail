# Professional Zmail mail rollout

## Executive Summary and Scope

Configure the deployed Zmail instance for a recognizable sender, reusable bilingual
brand templates, verified authentication, and real provider delivery feedback.
Reuse existing mailbox, domain, template, message-event contracts; no new public
API, tables, browser adapters, or product modules. Scope is production configuration,
brand assets, repeatable operational checks and a runbook. Multi-provider support,
BIMI, public signup, bulk campaigns, and new billing plans are excluded.

## Actors and Access

Operator configures Cloudflare and the root-only runtime. ZGI owner creates
organization domains, mailboxes and templates through authenticated HTTP. Organization
members retain existing read/send/template rights. Cloudflare Worker alone submits
HMAC-authenticated delivery events. No authentication bypass or direct SQL writes.
DNS automation credential must be restricted to the zmail.im zone and the server IP.
New persistent credential requires browser action-time approval; independent template
work proceeds while that prerequisite is pending.

## Workflows

1. Prepare and preview brand templates without sending. Store via existing template API.
2. Configure verified send.zmail.im as shared trial domain. Connect notify.zmail.im
   to ZGI, create `hello@notify.zmail.im (localized Zmail team display name)` and literal inbound routing.
   Preserve existing owner@zmail.im and apex catch-all, and claim apex for ZGI before
   future registration is enabled. Never rewrite historical message senders.
3. Compare required Cloudflare DNS records with public DNS and show exact results.
   SPF, DKIM and DMARC receiver verdicts require actual received message headers;
   DNS success alone is not called receiver authentication success.
4. Create a new dedicated queue plus dead-letter queue and subscriptions for apex,
   notify and send. Leave the old queue serving other domains unchanged. Attach
   existing new-instance Worker; subscribe only after it is ready.
5. Use authorized recipient xishudev@gmail.com for bounded verification of the new
   identity/template, observe a real provider delivered/bounced event, and verify
   reply routing with a controlled message to the newly created Zmail mailbox.
   No fabricated provider events in production. No marketing broadcast.

## Module Ownership

Mailbox API owns domains, mailboxes, templates, messages and events. Existing Web
surfaces render those HTTP resources. Cloudflare owns authentication DNS, SMTP delivery,
queue delivery and event generation. Worker signs and transports events; it does not
own mail state. Brand assets under deploy/brand are Zmail operational content, not
framework defaults. No source imports cross deployable units.

## Domain and Persistence

Reuse MailDomain (organization ownership, pending/verified), Mailbox (address immutable),
MailTemplate (unique organization/name, HTML/text/sample variables), MailMessage and
MailEvent. No schema change, migration or backfill. Data changes go through the existing
API. Create templates only when names are absent; if names exist with different content,
compare before updating. Preserve original domain/env/Worker settings privately for rollback.

## State Machine

Domains: absent -> pending -> verified after authoritative checks; setup errors remain
pending and are retryable. Templates: absent -> saved -> rendered preview; missing variables
prevent sending. Messages: queued -> sent (provider accepted) or failed. Delivery event
history independently records deferred, delivered, bounced, failed, rejected, complained.
`delivered` means recipient server accepted, not inbox placement/read. Never conflate these.
All state and duplicate rules remain those in contracts/MAILBOXES.md.

## HTTP Contract and Algorithms

No contract change. Existing contract limits and stable error_code values apply.

```text
COMMAND ProvisionDomainAndMailbox(owner, domain, address)
BIND authenticated owner and Organization-Id
VALIDATE domain and local_part under existing mailbox contract
AUTHORIZE organization owner or admin
LOAD organization domains and mailboxes using scoped queries
REQUIRE name is not owned elsewhere ELSE MAILBOX.DOMAIN_TAKEN
DERIVE domain setup from Cloudflare zone configuration
BEGIN TRANSACTION mailbox (existing domain repository)
  INSERT pending domain with organization ownership when absent
COMMIT
AFTER COMMIT run existing bounded domain setup; poll committed domain
REQUIRE verified domain ELSE stop with recorded setup failure
BEGIN TRANSACTION mailbox (existing create service)
  INSERT mailbox through service after literal routing rule creation
COMMIT
RETURN existing 201 domain/mailbox DTO, or existing matching resource
```

```text
COMMAND SaveBrandTemplate(member, template)
BIND authenticated member and Organization-Id
VALIDATE name <= 100, subject <= 998, bodies <= 256 KiB, placeholder grammar
AUTHORIZE organization membership
LOAD bounded template catalog (maximum 200)
REQUIRE absent name or identical existing content ELSE stop and report conflict
BEGIN TRANSACTION mailbox (existing template repository)
  INSERT template only when name absent
COMMIT
RETURN 201 template DTO or matching saved resource
```

```text
COMMAND RecordDeliveryEvent(worker, event)
BIND raw JSON and timestamped HMAC signature
VALIDATE <= 64 KiB, HMAC scope delivery-event and five-minute window
AUTHORIZE signature ELSE MAILBOX.INBOUND_SIGNATURE_INVALID
LOAD message by provider message ID
REQUIRE known message ELSE MAILBOX.MESSAGE_NOT_FOUND
VALIDATE supported event and recipient ELSE MAILBOX.DELIVERY_EVENT_INVALID
BEGIN TRANSACTION mailbox (existing event repository)
  INSERT event uniquely by provider event ID and update message projection
  ENQUEUE existing organization webhook transactionally under existing policy
COMMIT
RETURN 201 new event or 200 duplicate DTO
```

401 means invalid signature (retry after correcting secret); 404 unknown messages are
acknowledged under existing Worker policy; 413/422 permanent invalid events are acknowledged;
network/5xx errors retry. No new endpoint/status/error definitions. Queries are current
organization-scoped bounded domains/mailboxes/templates and paginated messages.

## Events and Asynchronous Work

Provider event subscription -> dedicated queue -> existing Worker -> signed API -> persisted
event -> Web message timeline. At-least-once queue delivery, provider event IDs deduplicate.
Queue consumer has max batch 10, batch timeout 5 seconds, retry delay 60 seconds, max retries
5 and dead-letter queue. UTC event timestamps. Failed work remains in dead-letter storage
for its configured retention and needs operator inspection/replay before expiry. No automatic
email resend on event failure. Old domain queue is outside this rollout.

## Browser Surface

Use existing Domains, Mailboxes, Templates and Emails pages. Previews show the real saved
HTML/text and required variables. Existing loading/error/empty states, localization and
accessibility remain. No Admin change. Official identity is selectable as a mailbox;
this does not automatically convert every existing transactional mail subsystem to it.

## Security and Operations

Credentials remain mode 0600, server root-only, never source/logged. Keep registration disabled.
Use only the existing account plan; no upgrade or purchase. Token creation/expanded privileges
requires explicit confirmation when its exact scope is prepared. Notify DNS uses provider
requirements with DMARC enforcement after validation. No destructive DNS replacement.
Monitor queue failures/dead letters and message events. A real delivered event proves transport,
not read/open. Authentication report must distinguish DNS from receiver evidence.
Rollback: retain previous env and Worker config, restore configuration and redeploy; keep new
records/templates/mailboxes to avoid destructive rollback. Pause subscriptions on failure.

## Test and Verification

- Existing node Worker tests for HMAC, ack/retry and event transport.
- API template previews: no missing variables, HTML escaped, plain-text equivalent.
- Public DNS compared against provider-required records, DMARC presence/enforcement.
- Runtime health, real branded send accepted, delivery event persisted and visible.
- Reply-capable mailbox receives controlled inbound mail.
- Receiver headers checked only with authorized access; otherwise explicitly pending.
- Strict plan validator and git diff --check. No full release gate for unchanged app images;
  rerun relevant tests and full make check if application code changes become necessary.

## Delivery and Acceptance

Slice 1: saved welcome and service-notification templates, reusable source and previews.
Slice 2: authenticated domains, official mailbox, trial-domain setting and reply rule.
Slice 3: isolated queue/subscriptions, existing Worker deployed, real feedback read back.
Slice 4: verification report, deployment runbook and backup. No bulk campaigns or avatar work.
Success requires each of the four user outcomes observable, with external limitations explicit.

## Readiness and Assumptions

readiness: READY WITH RECORDED ASSUMPTIONS

Assumptions: ZGI is the platform organization; Chinese sender display name and Chinese/English
variants suit the current product; authorized test recipient remains xishudev@gmail.com.
Use existing features and schemas. External access approval is an execution prerequisite for
slice 2, not an unresolved design decision. Slice 1 can proceed independently.

## Open Decisions

None. No new public contract or persistence semantics. If provider/account permissions or
billing prevent setup, record the precise blocker without broadening credentials or spend.

## Implementation Amendment: Shared Subdomain Reply Routing

Discovery found that promoting send.zmail.im to MAILBOX_DOMAIN bypassed the existing
literal-route creation path, and listed the domain twice for its former owner.
The existing domain registry now supplies verified routing configuration for a promoted
shared domain. All organizations may create shared addresses as before, but management
of the domain remains with the original owner. Pending state rejects mailbox creation;
provider failure creates no mailbox. Unregistered shared domains retain the operator's
catch-all behavior. No schema/DTO change. contracts/MAILBOXES.md documents this behavior.

The mailboxDomainFor resolver loads the configured shared name from the existing domain
repository when management is enabled: missing record returns the original unmanaged
shared behavior, other lookup failures propagate, pending records fail closed, verified
records feed existing CreateMailbox routing and repository commit/compensation logic.
Lists exclude the shared name from the organization-domain suffix of the result.
No extra transaction, job, or credential is introduced. Existing duplicate/address-limit
and routing cleanup semantics remain. This API-only correction has regression tests for
cross-organization reply routing, deduplicated listing, denied domain management,
pending domains and provider failure. Tests failed before the fix. The API image must be
rebuilt; focused package tests and the full release gate precede production deployment.

The API token cannot enable Email Routing DNS on this account despite being able to
manage rules. Local pre-existing Wrangler OAuth enabled the two approved subdomains;
that OAuth credential was not copied to the server. Existing domain checks then verified
both domains. Future domain onboarding may require this same operator step.
