# Organization Mail Domains — Implementation Plan

Lets an organization send and receive mail on its own domain instead of only the instance's shared
domain. Written to [BUSINESS_IMPLEMENTATION_PLAN_STANDARD.md](../BUSINESS_IMPLEMENTATION_PLAN_STANDARD.md)
as an extension of [mailbox.md](mailbox.md), [mailbox-organizations.md](mailbox-organizations.md),
and [mail-campaigns.md](mail-campaigns.md); what those plans settle is not repeated. Public HTTP
behavior is owned by [contracts/MAILBOXES.md](../../contracts/MAILBOXES.md).

## 1. Executive Summary

**Outcome.** An owner connects `example.com`, Zmail sets up receiving, sending, and DMARC in
Cloudflare, shows each DNS record with its status, and once every record is in place the
organization creates mailboxes such as `hello@example.com` that send and receive like the ones on
the shared domain.

**Problem.** Every address lives on the one `MAILBOX_DOMAIN`. A business sending to its customers
needs its own domain, and setting up MX, SPF, DKIM, and DMARC by hand is where most people fail.
Comparable products ship domain onboarding as the first step:

| Need | Resend | Postmark | Mailflare |
|---|---|---|---|
| Add a domain | Domains page, records to copy | Sender signatures and domains | Domains page, writes Cloudflare DNS itself |
| See what is missing | Per-record status, re-verify | DKIM and Return-Path status | MX, SPF, DKIM, DMARC status, "set up" per record |

**Decisions.**

- A domain must be a zone, or a subdomain of a zone, that the instance's Cloudflare credentials can
  manage. Zmail then writes the records itself, which removes the hardest step. Domains on other
  DNS providers are deferred.
- The Cloudflare API token is the trust boundary. Any organization owner or admin may connect a
  domain the token can reach, first come first served, and a domain belongs to exactly one
  organization. An operator who serves several companies scopes the token to the zones they may
  use. The feature is off until `MAILBOX_CUSTOM_DOMAINS_ENABLED=true`.
- Receiving uses one Email Routing rule per mailbox address, pointing at the Email Worker. A
  zone's catch-all is never touched, because it belongs to whoever owns the zone and may already
  route other mail.
- Zmail adds records that are missing and never edits or deletes existing ones. A conflicting
  record is reported, not overwritten.
- The shared `MAILBOX_DOMAIN` keeps working for every organization, unchanged.

**In scope.** Connect, set up, check, list, and remove domains; mailboxes on a verified domain;
routing rules created and removed with mailboxes; a console page.

**Out of scope.** Domains on DNS providers other than Cloudflare; Resend or SES transports;
receive-only or send-only domains; automatic periodic re-checks; per-domain send limits.

## 2. Actors and Access Surfaces

| Actor | Surface | May |
|---|---|---|
| Owner, admin | Web console; session with `Organization-Id` | Connect, set up, check, and remove the organization's domains; create mailboxes on them |
| Member | Same | See the domains and their status; use mailboxes on them |
| Operator | Configuration | Enable the feature, scope the Cloudflare token, name the Email Worker |

## 3. End-to-End Workflows

1. **Connect.** An owner types `example.com`. Zmail finds the Cloudflare zone that contains it,
   records the domain as pending, and runs the setup and the check below. The page shows every
   record and its status.
2. **Set up.** Zmail enables Email Routing for the domain (Cloudflare adds the MX and SPF records),
   enables Email Sending for it, adds any sending record Cloudflare lists that the zone lacks, and
   adds `_dmarc` with `v=DMARC1; p=none` when the domain has no DMARC record. Running it again is
   safe.
3. **Check.** Zmail reads the zone's records and compares them with what receiving, sending, and
   DMARC need. When all are present the domain becomes verified. A check never writes.
4. **Create a mailbox.** On a verified domain, creating `hello@example.com` also creates the
   routing rule that hands its mail to the Email Worker. Deleting the mailbox removes the rule.
5. **Remove a domain.** Allowed only when no mailbox uses it. Zmail forgets the domain and leaves
   Cloudflare as it is, so removing a domain can never break other mail on the zone.

## 4. Module Decomposition

| Owner | Rules |
|---|---|
| `mailbox` domains | Domain names, ownership, limits, required records, verification, routing rules per mailbox |
| `infra/mailtransport` Cloudflare | Zone lookup, Email Routing and Email Sending calls, DNS records; no business rules |

The domain rules live in the `mailbox` starter because mailbox creation depends on them and the
inbound path already resolves mail by full address.

## 5. Domain Model and Vocabulary

- **Shared domain**: `MAILBOX_DOMAIN`, usable by every organization, set up by the operator.
- **Organization domain**: a domain one organization connected; `pending` or `verified`.
- **Required record**: one DNS record receiving, sending, or DMARC needs, with a `purpose`
  (`receiving`, `sending`, `dmarc`) and a `status` (`ok`, `missing`, `conflict`).
- Invariants: a name is lowercase and unique across the instance, is not the shared domain, and has
  at most 253 characters; an organization connects at most 10 domains; mailboxes are created only
  on the shared domain or a verified domain of the same organization.

## 6. Persistence Design

`mail_domains`: `id`, `organization_id` (cascade), `name` (unique), `zone_id`, `zone_name`,
`sending_tag`, `status`, `records` (jsonb, last check), `last_error`, `checked_at`, `verified_at`,
`created_by`, timestamps. `mailboxes.routing_rule_id` stores the Cloudflare rule of a mailbox on an
organization domain; it is empty on the shared domain.

## 7. State Machines

`pending → verified` when a check finds every required record. `verified → pending` when a later
check finds one missing; existing mailboxes keep working, new ones wait. Removing deletes the row.

## 8. HTTP Contract Design

| Route | Who | Result |
|---|---|---|
| `GET /v1/mail-domains` | Member | The organization's domains with records |
| `POST /v1/mail-domains` | Owner, admin | `201`; `409 MAILBOX.DOMAIN_TAKEN`, `422 MAILBOX.DOMAIN_ZONE_NOT_FOUND`, `422 MAILBOX.DOMAIN_LIMIT_REACHED` |
| `GET /v1/mail-domains/{id}` | Member | One domain |
| `POST /v1/mail-domains/{id}/setups` | Owner, admin | Runs setup then check |
| `POST /v1/mail-domains/{id}/checks` | Owner, admin | Runs check |
| `DELETE /v1/mail-domains/{id}` | Owner, admin | `204`; `409 MAILBOX.DOMAIN_IN_USE` |
| `POST /v1/mailboxes` | Owner, admin | Gains optional `domain`; `422 MAILBOX.DOMAIN_NOT_VERIFIED` |
| `GET /v1/mailbox-domains` | Member | Shared domain plus verified organization domains, each with `kind` |

With the feature off, the `mail-domains` routes answer `404 MAILBOX.DOMAINS_DISABLED`. A
Cloudflare failure during connect or setup keeps the domain pending, stores `last_error`, and still
answers with the domain, so the owner can see what failed and retry.

## 9. Authorization Matrix

Reads need membership; every write needs owner or admin. A domain of another organization is
reported as not found. Mailbox creation keeps its existing owner-or-admin rule.

## 10. Cross-Module Data Flow

Inbound mail for `hello@example.com` reaches the Email Worker through its routing rule and the API
resolves it by address, exactly like the shared domain. Sending uses the mailbox address as before;
Cloudflare accepts it once the domain's sending is enabled.

## 11. Scheduled and Asynchronous Work

None. Checks run when someone asks; Cloudflare calls happen in the request with the existing
provider timeout.

## 12. Browser-Surface Plan

A Domains page under the console lists domains, connects a new one, and shows each record (type,
name, value, purpose, status) with copy buttons and Set up, Check, and Remove actions. The mailbox
form gains a domain picker when more than one domain is available. The development mock BFF keeps
domains in memory and verifies them on the first check.

## 13. Security, Privacy, and Audit

The token never leaves the server. Zmail only adds records, never edits or deletes them, and never
changes a zone's catch-all. Domain names are validated before any Cloudflare call. A domain that a
token can reach is claimable by any organization on the instance; the operator documentation says
so and tells multi-tenant operators to scope the token.

## 14. Observability and Operations

Cloudflare failures are logged with the domain and the call. The deployment guide lists the token
permissions: Zone Read, DNS Edit, Zone Settings Edit, Email Routing Rules Edit, and Email Sending
Edit.

## 15. Test and Verification Strategy

Service tests with a fake Cloudflare client cover connect, setup idempotency, check transitions,
conflicts, limits, ownership, mailbox creation and deletion with rules, and removal rules. Client
tests use an HTTP test server for each Cloudflare call. Repository tests run against PostgreSQL.
Web tests cover the mock store and the page. A live check connects a subdomain of a test zone,
creates a mailbox, and sends and receives through it.

## 16. Delivery Plan

One slice: migration, Cloudflare client, service and routes, contract, Web page and mailbox picker,
documentation.
