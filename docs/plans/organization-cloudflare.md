# Organization Cloudflare connection

## Outcome and ownership

An organization owner or administrator opens Domains → Connect Cloudflare, enters a scoped API token, selects an accessible account and zone, and chooses an exact sending or receiving domain. The mailbox API owns secrets, authorization, domain ownership and routing. Web owns the wizard and uses the authenticated same-origin adapter. Ordinary organization members use provisioned mailboxes. Platform DNS and existing sending domains retain their current provider.

## Connection and domains

List active zones with the supplied token, following pagination. Group by account returned by Cloudflare. When there is one account, select it automatically. Display the effective outbound IP for customers using a token IP allowlist only when configured by the operator. Do not ask users for DNS record IDs or Zone IDs. Show separate results for token recognition, zone visibility, DNS reads, sending setup, receiving setup and actual delivery. Reading DNS proves no write permissions. Show unknown until the corresponding write or real delivery succeeds.

Use one active Cloudflare connection per organization in the first version. Store organization ID, Cloudflare account ID, verified Worker name, encrypted API token and encrypted organization-specific signing key, opaque connection ID, state, version and timestamps in PostgreSQL. Domains created through this connection explicitly reference its ID. A missing or revoked reference fails closed. Shared or previously configured domains continue using their existing provider. Use a unique normalized DNS name across tenants. Record the operator, action, target and result, excluding tokens, signing keys and email content.

## Worker ownership

The enterprise administrator enables Email Routing on the exact receiving domain and installs the provided Worker once in its own account. Cloudflare requires Zone Settings Write for the routing enablement step. The daily token uses DNS Edit, Zone Read and Email Routing Rules Edit on the selected zone plus Email Sending Edit on the selected account. Generate an installation package with a per-connection HTTPS callback URL, Worker name and an individually generated signing secret delivered once. The daily token never needs Workers Scripts Write. Accept callbacks only for that connection, current signing key, verified domain and recipient mailbox of the same organization. Check timestamp and HMAC before processing content. Preserve inbound duplicate suppression. Never supply the platform-wide secret to an enterprise.

## Contract and transitions

* Manager-only POST /mail-cloudflare-previews verifies a candidate token by listing accessible zones. It makes no writes, does not persist the token and returns allowlisted zone/account fields only.
* Manager-only PUT /mail-cloudflare-connection creates or rotates the encrypted token after checking account and zone access; validate the replacement before retiring a working token. Return masked metadata.
* GET returns management status for the active organization. POST checks refreshes diagnostics. Joining a zone to a connection enforces ownership server-side even when a browser submits a foreign zone ID.
* Preview existing MX before applying changes. Explicitly select a subdomain or confirm taking over an existing mail service before any receiving changes. Setup creates only missing records under the approved name. Conflicts remain visible. Verify the DNS and mailbox rule and then run a separate send/reply test.
* Route interactive, scheduled, transactional and broadcast sends by the persisted sending domain at dispatch time. Rotation or disconnect invalidates cached clients. Queued items keep their identity; unavailable connections produce an actionable error and must never fall back to platform credentials.
* DELETE previews affected domains, rules and pending work before confirmation. Stop new dispatch and callbacks on revocation, retain mail and clear the encrypted credentials. External DNS and Workers stay in the customer's account. Show explicit manual cleanup instructions.

## Failure, rollout and verification

Use organization-scoped locks/versions on connect, setup, rotation and disconnect. Provisioning saves intended state before external writes, then reconciles by exact provider resource IDs; repeated work can resume after timeout. Reject stale changes with 409. Distinguish expired credentials, zone scope, IP filtering, missing write permission, DNS propagation and provider unavailability. Surface generic errors to other organizations. Add nullable domain linkage before exposing the UI. Turn off new connections during rollback; keep workers for active connections processing.

Deliver and test discovery first, then encrypted connection lifecycle, domain selection and dispatch, then tenant-signed inbound. Exercise two independent orgs, failed rotation, revoked callbacks, queued sends, reconnect, existing platform domains and absence of secret values in responses/logs. Final acceptance uses a dedicated test subdomain, administrator-installed Worker, scoped token, public callback to the local test API, and a real external inbox replying to the original message. Record both message IDs and verify the parent MX before/after. A platform-domain round trip is a separate test.
