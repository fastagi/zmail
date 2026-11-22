# Invitation Delivery State And Resend (reduced plan)

Issue: #104. Owner: the optional `organization` starter (`api/internal/modules/organization/`),
with its Web feature (`web/src/features/organization/`). Contract: `contracts/ORGANIZATIONS.md`.

## Problem

`POST /v1/organizations/:id/invitations` returns `email_send_status` once, but the invitation
resource does not keep it. After a reload an invitation whose email was never handed to a mailer
looks the same as one that was. The only recovery is to revoke and invite again.

## Scope

1. Persist the outcome of the latest send attempt on the invitation and expose it on the
   invitation view as `email_send_status` (`pending`, `accepted_by_provider`, `failed`,
   `not_configured`, or `null` for invitations created before tracking) and `email_attempted_at`.
   `pending` is written with the invitation (or the new token) before the attempt starts. It stays
   if the process stops before recording an outcome. Provider detail is never stored.
2. Add `POST /v1/organizations/:id/invitations/:invitation_id/resend`, available to owners and
   admins only, for a pending, unexpired invitation. It answers `200` with the same
   `{ invitation, email_send_status }` shape as create.
3. Web: the invitation list shows the delivery state and a Resend action with a cooldown. Both
   locales, the fixed BFF adapter route, and the development mock are updated.

## Decisions

- **Token rotation.** Only the SHA-256 hash of a token is stored, so the original token cannot be
  sent again. A resend that reaches a configured mailer therefore rotates the token: the earlier
  emailed token stops working and the new email carries the only valid one. When no mailer is
  configured the token is not rotated (nothing would carry the new one), and the status is
  recorded as `not_configured`. The expiry is not extended.
- **Rate limit.** Per invitation, using the shared `ratelimit.Stores` factory (process-local or
  PostgreSQL-shared, like the authentication and mailbox quotas). The limits are 1 resend per
  minute and 5 per 24 hours. A refusal is `429 COMMON.RATE_LIMITED` with `Retry-After`. The
  original send on create is not counted.
- **Errors.** No new codes. Missing, foreign, accepted, revoked, or expired invitations answer
  `404 ORGANIZATION.INVITATION.NOT_FOUND`, as revoke does. A known `member` gets
  `403 PERMISSION.DENIED`, and a non-member gets `404 ORGANIZATION.NOT_FOUND`.
- **Stale results.** Recording an outcome is conditional on the token hash that was sent, so a
  slow earlier attempt cannot overwrite the state of a newer resend.
- **Audit.** `resend` is an `organization_invitations` change with organization ID, role, and
  `email_send_status`. Email addresses and tokens are not recorded.

## Acceptance

- After a reload the list shows "Not sent: email is not configured", "Sending failed", "Sent",
  or "Sending", with the time of the last attempt.
- An owner or admin can resend a pending invitation. A second resend within a minute is refused
  with a visible cooldown, and members cannot resend.
- Existing invitation rows migrate with a `null` status and keep working.

## Verification

- API: PostgreSQL repository and service tests for status persistence, rotation, the stale-result
  guard, authorization, the non-pending branch, and both limits. Also a handler test for 429 with
  `Retry-After`, migration and golden-schema tests, and `make route-catalog-check`.
- Contracts: `pnpm generate`, `pnpm check`, `pnpm check:routes`, and the error-contract guard.
- Web: Vitest for the schema, the service, the BFF route and mock, and the list component. Also
  type check and lint.
- Full local gate: `make check`.
