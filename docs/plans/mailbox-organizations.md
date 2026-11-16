# Organization Mailboxes — Implementation Plan

Moves mailbox ownership from a user to an organization. Written to
[BUSINESS_IMPLEMENTATION_PLAN_STANDARD.md](../BUSINESS_IMPLEMENTATION_PLAN_STANDARD.md) as a change
to [mailbox.md](mailbox.md); sections that do not change are not repeated. Public HTTP behavior is
owned by [contracts/MAILBOXES.md](../../contracts/MAILBOXES.md) and the organization header by
[contracts/ORGANIZATIONS.md](../../contracts/ORGANIZATIONS.md).

## 1. Executive Summary

**Outcome.** A team shares its addresses. An organization owns mailboxes such as `sales@` and
`billing@`; every member reads what arrives and sends from them, and owners and admins decide which
addresses exist.

**Problem.** The first slice gave each mailbox to one user. Two people could not work the same
address, and mail stayed with whoever happened to create the mailbox. Comparable products put every
resource under the team: Resend scopes API keys, domains, and usage to a team with Admin and Member
roles, and Postmark scopes access per server. Neither has a personal layer.

**In scope.** Organization ownership of mailboxes, messages, limits, the log, and statistics; role
rules; the `Organization-Id` header on every mailbox route, including the API-key routes; moving
existing mailboxes; an organization picker in the Web console.

**Out of scope.** Access rules per mailbox, keys owned by an organization, and cleanup when an
organization is deleted. Section 13 explains each.

## 2. Actors and Access Surfaces

| Actor | Surface | May |
|---|---|---|
| Member | Web console; `/v1/...` with a session and `Organization-Id` | List mailboxes; list, read, send, mark, and delete messages; read the log and statistics |
| Owner, admin | Same | Everything a member may, and create and delete mailboxes |
| Backend service | `/v1/outbound-mail-messages` with a user's API key and `Organization-Id` | Send from the organization's mailboxes (`mail:send`) and read what it sent (`mail:read`), while the key's owner is a member |
| Mail edge | `POST /v1/inbound-mail-messages`, signed | Deliver to an existing address; no organization header |

A user who belongs to no organization has no mailbox access and is led to create or join one.

## 3. End-to-End Workflows

1. **Choose an organization.** The console lists the user's organizations and remembers the choice
   in the URL. Every mailbox request carries it as `Organization-Id`.
2. **Claim an address.** An owner or admin submits a local part. The mailbox is created for the
   organization, with the acting user recorded as its creator.
3. **Share the mail.** Any member opens a message; it becomes read for the whole organization. Any
   member replies or sends.
4. **Leave.** A member removed from the organization gets `404 ORGANIZATION.NOT_FOUND` on the next
   request. Mailboxes they created stay with the organization.
5. **Send from a service.** The service sends its key and the organization id. The API resolves the
   key owner's membership of that organization on every request.

## 4. Module Decomposition

Unchanged owners. `mailbox` now depends on the `organization` starter and consumes two things from
it: the `organization_context` middleware and `domain.OrganizationContext`. It does not read
organization tables except in its one data migration.

## 5. Domain Model and Vocabulary

- **Mailbox**: one address owned by exactly one organization. `created_by` names the creating user
  and grants nothing.
- The global invariants stand: an address is unique forever, and inbound mail is stored once per
  mailbox.
- New invariant: every query that reaches a mailbox or message is filtered by the organization the
  request acts in. Only inbound delivery reaches a mailbox by address alone.

## 6. Persistence Design

Migration `2026_11_17_090000_move_mailboxes_to_organizations`:

- Adds nullable `mailboxes.organization_id` with a foreign key to `organizations`
  (`ON DELETE CASCADE`), and an index on `(organization_id, id)` for live rows.
- Renames `user_id` to `created_by` and drops its foreign key and index.
- Backfills each existing mailbox with the organization its creator joined first.

A mailbox whose creator belongs to no organization keeps `organization_id` NULL. It and its mail
stay in the database, no member route can reach it, and inbound mail for its address is still
stored. The column is nullable for exactly this case; deleting such mail in a migration was
rejected because the migration cannot ask anyone. An operator assigns the mailbox with one
`UPDATE`. `mail_messages` is unchanged: a message's organization is its mailbox's.

## 7. State Machines

Unchanged.

## 8. HTTP Contract Design

- Every member and API-key operation takes `Organization-Id`, answers
  `400 ORGANIZATION.CONTEXT_REQUIRED` or `CONTEXT_INVALID` for a bad header, and
  `404 ORGANIZATION.NOT_FOUND` for an organization the caller does not belong to.
- Creating or deleting a mailbox as a member is `403 PERMISSION.DENIED`.
- `max_mailboxes` is 50 and, like `daily_send_limit`, is per organization.
- Responses vary on `Authorization` and `Organization-Id`.
- No path changes. Paths stay flat rather than nested under `/organizations/:id`, matching the
  scaffold's other organization-owned resources, which also select the organization by header.

## 9. Authorization Matrix

| Operation | Owner, admin | Member | Non-member | Other organization's resource |
|---|---|---|---|---|
| List domains, mailboxes, log, statistics | Yes | Yes | 404 | n/a |
| Create, delete mailbox | Yes | 403 | 404 | 404 |
| List, read, send, update, delete messages | Yes | Yes | 404 | 404 |
| API-key send and lookup | While the key's owner is a member | Same | 404 | 404 |

Roles come from the verified organization context, never from the request. Three roles are enough
for this slice; the `permission` starter is not used because adding module permission keys to it
means editing the core catalog and would force that starter on every deployment.

## 10. Cross-Module Data Flow

`mailbox` reads the organization context that the `organization` starter's middleware puts on the
request. It no longer registers an account-deletion cleaner: mail is not a user's to take along,
and the organization starter already refuses to delete an account that still belongs to an
organization.

## 11. Scheduled and Asynchronous Work

None.

## 12. Browser-Surface Plan

The mail pages gain an organization picker; the choice lives in the URL so a link or reload keeps
it. With no organization the pages show how to create or join one. Create and delete controls for
mailboxes show only to owners and admins. The scaffold keeps organization features as tabs inside
the organization page; the mail pages stay top-level because they are the product, and they are
the first pages to select an organization outside that page.

## 13. Security, Privacy, and Audit

- **Isolation.** The repository takes the organization id on every member query, and tests cover
  another organization's mailbox, message, log filter, and statistics.
- **API keys.** A key acts with its owner's current membership, so one user's key can work in any
  organization that user belongs to, selected by header, and stops working when the membership
  ends. A key bound to one organization would be narrower; the scaffold defers organization-owned
  keys, and this plan does not invent them.
- **Shared read state.** Read state is per message, not per member. That is what a shared inbox
  needs, and it means one member cannot tell what another has read.
- **Audit.** The organization middleware adds the organization, membership, and role to the
  request audit record.

Known limits: any member can read every mailbox of the organization; there is no rule per mailbox.
The scaffold cannot delete an organization, but if that is added the cascade will delete its
mailbox rows and free their addresses, which breaks the rule that an address is never reissued;
that work must keep the addresses reserved.

## 14. Observability and Operations

Enable `organization` wherever `mailbox` is enabled; startup refuses `mailbox` alone. Run the
migration with both selected. After migrating, list unowned mailboxes with
`SELECT id, address, created_by FROM mailboxes WHERE organization_id IS NULL`.

## 15. Test and Verification Strategy

| Layer | Proof |
|---|---|
| Migration | Existing mailbox moves to its creator's earliest organization; a creator without one leaves it unowned; no mail deleted; rollback restores the columns |
| Repository | Organization filter on mailboxes, messages, log, statistics, limit, and send count |
| Handler | Managers create and delete; members get 403 and change nothing; members list and send; another organization's mailbox is 404; a missing context is 400 |
| Starter | `mailbox` without `organization` is refused; all three migrations are selected |
| Contract | Header and errors on every operation; generated types; runtime route comparison |
| Web | Header forwarded; role-gated controls; picker and empty state; mock parity |
| End to end | Two organizations and three roles against a running API, including an API key |

## 16. Delivery Plan

1. API, migration, contract (this change).
2. Web console (this change).
3. Later: access rules per mailbox; organization-owned keys when the scaffold defines them;
   address retention on organization deletion.
