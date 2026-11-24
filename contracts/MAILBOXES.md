# Mailbox Contract

This contract owns the optional `mailbox` starter across the Go API, the Web mailbox feature, the
fixed production API adapter, the development mock BFF, and the Cloudflare Email Worker that
delivers inbound mail. It defines mailboxes owned by an organization on the instance's shared
domain or on a domain the organization connected, sending through a mail provider from a session or
an API key, per-mailbox access (personal and shared mailboxes with member permissions), and signed
inbound delivery. It does not define domains on DNS providers other than Cloudflare.

## Subdomain Contracts

The mailbox contract is organized into six subdomain specifications under [`mail/`](mail/):

| Document | Scope |
|---|---|
| [`mail/INBOX.md`](mail/INBOX.md) | Mailboxes, per-mailbox access, read state, handover, sealed history, mail roles, messages, placements, folders/labels, threads, thread notes, search, counts, personal send, attachments, log/stats, unified inbox, signatures, drafts, idempotency, masked secrets, and member feature grants |
| [`mail/DOMAINS.md`](mail/DOMAINS.md) | Custom domains, managed zones, DNS records, and organization Cloudflare discovery and connection setup |
| [`mail/DELIVERY.md`](mail/DELIVERY.md) | Templates, contacts, contact lists, contact import, broadcasts, unsubscribes, send lane usage, programmatic sending, developer access, transactional mail, suppressions, delivery events, webhooks, and inbound delivery |
| [`mail/EXTERNAL_MAIL.md`](mail/EXTERNAL_MAIL.md) | External mailbox connections, OAuth and app-password binding, shared connected mailboxes, Outlook/Gmail connectors, sync budgets and runs, revoked-copy retention, push subscriptions, connection directory, and tenant connections |
| [`mail/TRIAGE_AND_AGENTS.md`](mail/TRIAGE_AND_AGENTS.md) | AI triage settings, manual overrides, reply drafts, agent grants, agent feed, and agent reading/drafting |
| [`mail/GATEWAY.md`](mail/GATEWAY.md) | IMAP/SMTP mail client access, app passwords, client settings, mailbox directory, mailbox import/export, roster import, member welcome mail, and platform accounts |

## Activation

The API registers these routes only when `OPTIONAL_STARTERS` contains `organization` and
`mailbox`; `mailbox` alone is refused at startup. The Web feature is active only when
`NEXT_PUBLIC_OPTIONAL_FEATURES` contains `organization` and `mailbox`. Configuration keys are
listed in [`api/docs/CONFIGURATION.md`](../api/docs/CONFIGURATION.md).

## Ownership And Paths

Mailboxes and their messages belong to an organization. Every request names the organization
with the `Organization-Id` header defined in [`ORGANIZATIONS.md`](ORGANIZATIONS.md), and the API
resolves the caller's membership of it on each request: a missing header is
`400 ORGANIZATION.CONTEXT_REQUIRED`, a malformed one `400 ORGANIZATION.CONTEXT_INVALID`, and an
organization the caller does not belong to is `404 ORGANIZATION.NOT_FOUND`, exactly like one that
does not exist. A mailbox or message of another organization is likewise reported as missing.

Within the organization, what a caller may do with a mailbox follows from the mailbox's access
(see [Access](mail/INBOX.md#access)), not from the organization role alone:

| Role | May |
|---|---|
| `member` | List domains, managed zones, and their DNS records; preview a domain; list the mailboxes they have access to; read, send, organize, and delete mail as their access allows; read the unified inbox, the log, and statistics of the mailboxes they may read; list the mail roles of the mailboxes they can see; read the platform accounts registered with a mailbox they may read or that they own, with their history |
| `owner`, `admin` | Everything a member may; see every mailbox's metadata; create, rename, and delete mailboxes; list the mailbox directory, and import and export mailboxes; hand over personal mailboxes and delete sealed history; manage the members and webhook switch of shared mailboxes, including themselves; read every mailbox's access history; connect, set up, check, enable receiving on, make the default, and remove domains; remove managed zones; and create, change, and delete their DNS records; list every mail role, create, change, and delete mail roles, and put mailboxes in them; list, create, change, and delete every platform account and hand over an owner's accounts; read the usage of each send lane; list the suppression list and remove addresses from it |

The owner and admin roles never grant reading or sending mail by themselves: an admin reads a
mailbox only as a member of it, and the access history records how they became one. A member who
creates or deletes a mailbox gets `403 PERMISSION.DENIED`. The user who created a mailbox is
recorded but has no special rights to it. A removed member loses access on their next request.

| Operation | Browser route | Go API route | Success |
|---|---|---|---|
| List domains | `GET /api/mailbox-domains` | `GET /v1/mailbox-domains` | `200` envelope, 1 to 11 items |
| List connected domains | `GET /api/mail-domains` | `GET /v1/mail-domains` | `200` envelope, at most 10 items |
| Connect a domain | `POST /api/mail-domains` | `POST /v1/mail-domains` | `201` envelope |
| Get a connected domain | `GET /api/mail-domains/:id` | `GET /v1/mail-domains/:domain_id` | `200` envelope |
| Set up a domain again | `POST /api/mail-domains/:id/setups` | `POST /v1/mail-domains/:domain_id/setups` | `200` envelope |
| Check a domain | `POST /api/mail-domains/:id/checks` | `POST /v1/mail-domains/:domain_id/checks` | `200` envelope |
| Remove a domain | `DELETE /api/mail-domains/:id` | `DELETE /v1/mail-domains/:domain_id` | `204` |
| Enable receiving on a domain | none yet | `POST /v1/mail-domains/:domain_id/receiving` | `200` envelope |
| Make a domain the default | `PUT /api/mail-domains/:id/default` | `PUT /v1/mail-domains/:domain_id/default` | `200` envelope |
| Preview a domain | none yet | `POST /v1/mail-domain-previews` | `200` envelope |
| List managed zones | none yet | `GET /v1/mail-zones` | `200` envelope, at most 20 items |
| Get a managed zone | none yet | `GET /v1/mail-zones/:zone_id` | `200` envelope |
| Remove a managed zone | none yet | `DELETE /v1/mail-zones/:zone_id` | `204` |
| List a zone's DNS records | none yet | `GET /v1/mail-zones/:zone_id/dns-records` | `200` envelope, at most 3500 items |
| Create a DNS record | none yet | `POST /v1/mail-zones/:zone_id/dns-records` | `201` envelope |
| Change a DNS record | none yet | `PATCH /v1/mail-zones/:zone_id/dns-records/:record_id` | `200` envelope |
| Delete a DNS record | none yet | `DELETE /v1/mail-zones/:zone_id/dns-records/:record_id` | `204` |
| List mailboxes | `GET /api/mailboxes` | `GET /v1/mailboxes` | `200` envelope, at most 5000 items |
| Create mailbox | `POST /api/mailboxes` | `POST /v1/mailboxes` | `201` envelope |
| Rename mailbox | `PUT /api/mailboxes/:id` | `PUT /v1/mailboxes/:mailbox_id` | `200` envelope |
| Delete mailbox | `DELETE /api/mailboxes/:id` | `DELETE /v1/mailboxes/:mailbox_id` | `204` |
| Mailbox directory | `GET /api/mailbox-directory` | `GET /v1/mailbox-directory` | Keyset envelope |
| Import mailboxes | `POST /api/mailbox-imports` | `POST /v1/mailbox-imports` | `200` envelope, at most 500 rows |
| Export mailboxes | `GET /api/mailbox-exports` | `GET /v1/mailbox-exports` | `200` CSV file |
| Import a roster | `POST /api/member-imports` | `POST /v1/member-imports` | `200` envelope, at most 500 rows |
| Read the welcome mail setting | `GET /api/member-welcome-mail-setting` | `GET /v1/member-welcome-mail-setting` | `200` envelope |
| Replace the welcome mail setting | `PUT /api/member-welcome-mail-setting` | `PUT /v1/member-welcome-mail-setting` | `200` envelope |
| Preview a welcome mail | `POST /api/member-welcome-mail-previews` | `POST /v1/member-welcome-mail-previews` | `200` envelope |
| List welcome mail status | `GET /api/member-welcome-mails` | `GET /v1/member-welcome-mails` | `200` envelope, at most the mailbox ceiling |
| Resend a welcome mail | `POST /api/member-welcome-mails` | `POST /v1/member-welcome-mails` | `201` envelope |
| List members | `GET /api/mailboxes/:id/members` | `GET /v1/mailboxes/:mailbox_id/members` | `200` envelope, at most 100 items |
| Set a member | `PUT /api/mailboxes/:id/members/:userId` | `PUT /v1/mailboxes/:mailbox_id/members/:user_id` | `200` envelope |
| Remove a member | `DELETE /api/mailboxes/:id/members/:userId` | `DELETE /v1/mailboxes/:mailbox_id/members/:user_id` | `204` |
| Access history | `GET /api/mailboxes/:id/access-events` | `GET /v1/mailboxes/:mailbox_id/access-events` | Paginated envelope |
| Hand over a personal mailbox | `POST /api/mailboxes/:id/handovers` | `POST /v1/mailboxes/:mailbox_id/handovers` | `201` envelope |
| Delete sealed history | `DELETE /api/mailboxes/:id/sealed-messages` | `DELETE /v1/mailboxes/:mailbox_id/sealed-messages` | `200` envelope |
| Webhook switch | `PUT /api/mailboxes/:id/webhook-setting` | `PUT /v1/mailboxes/:mailbox_id/webhook-setting` | `200` envelope |
| List messages | `GET /api/mailboxes/:id/messages` | `GET /v1/mailboxes/:mailbox_id/messages` | Paginated envelope |
| Send message | `POST /api/mailboxes/:id/messages` | `POST /v1/mailboxes/:mailbox_id/messages` | `201` envelope |
| Get message | `GET /api/mailboxes/:id/messages/:messageId` | `GET /v1/mailboxes/:mailbox_id/messages/:message_id` | `200` envelope |
| Reveal masked codes | `POST /api/mailboxes/:id/messages/:messageId/reveal` | `POST /v1/mailboxes/:mailbox_id/messages/:message_id/reveal` | `200` envelope |
| Update a message | `PUT /api/mailboxes/:id/messages/:messageId` | `PUT /v1/mailboxes/:mailbox_id/messages/:message_id` | `200` envelope |
| Trash or delete a message | `DELETE /api/mailboxes/:id/messages/:messageId` | `DELETE /v1/mailboxes/:mailbox_id/messages/:message_id` | `204` |
| Update messages at once | `POST /api/mailboxes/:id/message-updates` | `POST /v1/mailboxes/:mailbox_id/message-updates` | `200` envelope |
| Empty the trash | `POST /api/mailboxes/:id/trash-purges` | `POST /v1/mailboxes/:mailbox_id/trash-purges` | `200` envelope |
| Get a thread | `GET /api/mailboxes/:id/threads/:threadId` | `GET /v1/mailboxes/:mailbox_id/threads/:thread_id` | `200` envelope, at most 100 items |
| Unread and starred counts | `GET /api/mailboxes/:id/message-counts` | `GET /v1/mailboxes/:mailbox_id/message-counts` | `200` envelope |
| List folders | `GET /api/mailboxes/:id/mail-folders` | `GET /v1/mailboxes/:mailbox_id/mail-folders` | `200` envelope, at most 100 items |
| Create a folder | `POST /api/mailboxes/:id/mail-folders` | `POST /v1/mailboxes/:mailbox_id/mail-folders` | `201` envelope |
| Rename a folder | `PUT /api/mailboxes/:id/mail-folders/:folderId` | `PUT /v1/mailboxes/:mailbox_id/mail-folders/:folder_id` | `200` envelope |
| Delete a folder | `DELETE /api/mailboxes/:id/mail-folders/:folderId` | `DELETE /v1/mailboxes/:mailbox_id/mail-folders/:folder_id` | `204` |
| List labels | `GET /api/mailboxes/:id/mail-labels` | `GET /v1/mailboxes/:mailbox_id/mail-labels` | `200` envelope, at most 100 items |
| Create a label | `POST /api/mailboxes/:id/mail-labels` | `POST /v1/mailboxes/:mailbox_id/mail-labels` | `201` envelope |
| Update a label | `PUT /api/mailboxes/:id/mail-labels/:labelId` | `PUT /v1/mailboxes/:mailbox_id/mail-labels/:label_id` | `200` envelope |
| Delete a label | `DELETE /api/mailboxes/:id/mail-labels/:labelId` | `DELETE /v1/mailboxes/:mailbox_id/mail-labels/:label_id` | `204` |
| List the log | `GET /api/mail-messages` | `GET /v1/mail-messages` | Paginated envelope |
| Per-day statistics | `GET /api/mail-message-stats` | `GET /v1/mail-message-stats` | `200` envelope, at most 90 items |
| Unified inbox | `GET /api/inbox-messages` | `GET /v1/inbox-messages` | Cursor envelope |
| Unified inbox counts | `GET /api/inbox-message-counts` | `GET /v1/inbox-message-counts` | `200` envelope |
| List mail roles | `GET /api/mail-roles` | `GET /v1/mail-roles` | `200` envelope, at most 50 items |
| Create a mail role | `POST /api/mail-roles` | `POST /v1/mail-roles` | `201` envelope |
| Replace a mail role | `PUT /api/mail-roles/:roleId` | `PUT /v1/mail-roles/:role_id` | `200` envelope |
| Delete a mail role | `DELETE /api/mail-roles/:roleId` | `DELETE /v1/mail-roles/:role_id` | `204` |
| Set a mailbox's mail role | `PUT /api/mailboxes/:id/role-assignment` | `PUT /v1/mailboxes/:mailbox_id/role-assignment` | `200` envelope |
| Read the AI triage setting | `GET /api/mail-triage-setting` | `GET /v1/mail-triage-setting` | `200` envelope |
| Replace the AI triage setting | `PUT /api/mail-triage-setting` | `PUT /v1/mail-triage-setting` | `200` envelope |
| Mailbox AI triage switch | `PUT /api/mailboxes/:id/triage-setting` | `PUT /v1/mailboxes/:mailbox_id/triage-setting` | `200` envelope |
| Read my signature | `GET /api/mailboxes/:id/signature` | `GET /v1/mailboxes/:mailbox_id/signature` | `200` envelope |
| Replace my signature | `PUT /api/mailboxes/:id/signature` | `PUT /v1/mailboxes/:mailbox_id/signature` | `200` envelope |
| List my drafts | `GET /api/mail-drafts` | `GET /v1/mail-drafts` | `200` envelope |
| Save a new draft | `POST /api/mail-drafts` | `POST /v1/mail-drafts` | `201` envelope |
| Replace a draft | `PUT /api/mail-drafts/:id` | `PUT /v1/mail-drafts/:draft_id` | `200` envelope |
| Delete a draft | `DELETE /api/mail-drafts/:id` | `DELETE /v1/mail-drafts/:draft_id` | `204` |
| Set a message's triage | `PUT /api/mailboxes/:id/messages/:messageId/triage` | `PUT /v1/mailboxes/:mailbox_id/messages/:message_id/triage` | `200` envelope |
| Draft a reply | `POST /api/mailboxes/:id/messages/:messageId/reply-drafts` | `POST /v1/mailboxes/:mailbox_id/messages/:message_id/reply-drafts` | `200` envelope |
| List platform accounts | `GET /api/platform-accounts` | `GET /v1/platform-accounts` | Paginated envelope |
| Add a platform account | `POST /api/platform-accounts` | `POST /v1/platform-accounts` | `201` envelope |
| Replace a platform account | `PUT /api/platform-accounts/:accountId` | `PUT /v1/platform-accounts/:account_id` | `200` envelope |
| Delete a platform account | `DELETE /api/platform-accounts/:accountId` | `DELETE /v1/platform-accounts/:account_id` | `204` |
| Platform account history | `GET /api/platform-accounts/:accountId/events` | `GET /v1/platform-accounts/:account_id/events` | Paginated envelope |
| Hand over platform accounts | `POST /api/platform-account-handovers` | `POST /v1/platform-account-handovers` | `201` envelope |
| List the connectors accounts can be bound with | none yet | `GET /v1/external-mail-connectors` | `200` envelope, at most 32 items |
| Start binding or reconnecting an account | none yet | `POST /v1/external-mail-authorizations` | `201` |
| Finish binding or reconnecting an account | none yet | `POST /v1/external-mail-authorizations/completions` | `201` new, `200` restored |
| List the caller's connected accounts | none yet | `GET /v1/external-mail-connections` | `200` envelope, at most 120 items |
| Unbind a connected account | none yet | `DELETE /v1/external-mail-connections/:connection_id` | `204` |
| List a connection's latest sync runs | none yet | `GET /v1/external-mail-connections/:connection_id/sync-runs` | `200` envelope, at most 50 items |
| Delete a revoked connection's copies at once | none yet | `POST /v1/external-mail-connections/:connection_id/purges` | `200` done, `202` finishing in the background |
| List the organization's connected accounts (owners and admins) | none yet | `GET /v1/external-mail-connection-directory` | Cursor page, at most 100 items |
| Force-disconnect a member's connected account (owners and admins) | none yet | `DELETE /v1/external-mail-connection-directory/:connection_id` | `204` |
| Send with an API key | none | `POST /v1/outbound-mail-messages` | `201`, or `200` for a repeat |
| Get a sent message with an API key | none | `GET /v1/outbound-mail-messages/:message_id` | `200` envelope |
| Send a transactional message | none | `POST /v1/transactional-messages` | `202`, or `200` for a repeat |
| Get a transactional message | none | `GET /v1/transactional-messages/:message_id` | `200` envelope |
| Transactional send log | `GET /api/transactional-mail-log` | `GET /v1/transactional-mail-log` | Keyset envelope |
| Transactional log entry | `GET /api/transactional-mail-log/:id` | `GET /v1/transactional-mail-log/:message_id` | `200` envelope |
| Transactional test send | `POST /api/transactional-test-sends` | `POST /v1/transactional-test-sends` | `202` envelope |
| Send lane usage | `GET /api/mail-send-lanes` | `GET /v1/mail-send-lanes` | `200` envelope, 3 lanes |
| List the suppression list | `GET /api/mail-suppressions` | `GET /v1/mail-suppressions` | Keyset envelope |
| Remove a suppressed address | `DELETE /api/mail-suppressions/:id` | `DELETE /v1/mail-suppressions/:suppression_id` | `204` |
| List my mail client passwords | `GET /api/mail-app-passwords` | `GET /v1/mail-app-passwords` | `200` envelope, at most 20 items |
| Create a mail client password | `POST /api/mail-app-passwords` | `POST /v1/mail-app-passwords` | `201` envelope |
| Revoke my mail client password | `DELETE /api/mail-app-passwords/:id` | `DELETE /v1/mail-app-passwords/:password_id` | `204` |
| List the organization's mail client passwords (owners and admins) | none yet | `GET /v1/mail-app-password-directory` | Paginated envelope |
| Revoke a member's mail client password (owners and admins) | none yet | `DELETE /v1/mail-app-password-directory/:password_id` | `204` |
| Read the mail client switch | `GET /api/mail-client-settings` | `GET /v1/mail-client-settings` | `200` envelope |
| Replace the mail client switch | `PUT /api/mail-client-settings` | `PUT /v1/mail-client-settings` | `200` envelope |
| Inbound delivery | none | `POST /v1/inbound-mail-messages` | `201`, or `200` for a repeat |
| Delivery event | none | `POST /v1/mail-delivery-events` | `201`, or `200` for a repeat |

The Web production API adapter owns the HttpOnly API session credential and forwards only these
fixed relative paths together with the `Organization-Id` header. Every member response is
`Cache-Control: private, no-store` and varies on `Authorization` and `Organization-Id`. The inbound route
has no browser route: only the mail edge calls it.

## Errors

| Code | Status |
|---|---|
| `MAILBOX.NOT_FOUND` | 404 |
| `MAILBOX.MESSAGE_NOT_FOUND` | 404 |
| `MAILBOX.ADDRESS_TAKEN` | 409 |
| `MAILBOX.LIMIT_REACHED` | 409 |
| `MAILBOX.IMPORT_DUPLICATE_ROW` | Import row only |
| `MAILBOX.IMPORT_MEMBER_NOT_FOUND` | Import row only |
| `MAILBOX.IMPORT_EMAIL_INVALID` | Roster import row only |
| `MAILBOX.IMPORT_ROLE_INVALID` | Roster import row only |
| `MAILBOX.IMPORT_DOMAIN_REQUIRED` | Roster import row only |
| `MAILBOX.ACCESS_CONFLICT` | 409 |
| `MAILBOX.ACCESS_INVALID` | 422 |
| `MAILBOX.ADDRESS_INVALID` | 422 |
| `MAILBOX.ADDRESS_RESERVED` | 422 |
| `MAILBOX.MESSAGE_INVALID` | 422 |
| `MAILBOX.IDEMPOTENCY_CONFLICT` | 409 |
| `MAILBOX.SEND_IN_PROGRESS` | 409 |
| `MAILBOX.IDEMPOTENCY_KEY_INVALID` | 422 |
| `MAILBOX.SEND_LIMIT_REACHED` | 429 |
| `MAILBOX.SENDING_PAUSED` | 403 |
| `MAILBOX.DELIVERY_FAILED` | 502 |
| `MAILBOX.SENDER_NOT_CONFIGURED` | 503 |
| `MAILBOX.INBOUND_SIGNATURE_INVALID` | 401 |
| `MAILBOX.RECIPIENT_NOT_FOUND` | 404 |
| `MAILBOX.INBOUND_TOO_LARGE` | 413 |
| `MAILBOX.INBOUND_UNPARSEABLE` | 422 |
| `MAILBOX.INBOUND_DISABLED` | 503 |
| `MAILBOX.DELIVERY_EVENT_INVALID` | 422 |
| `MAILBOX.TEMPLATE_NOT_FOUND` | 404 |
| `MAILBOX.TEMPLATE_NAME_TAKEN` | 409 |
| `MAILBOX.TEMPLATE_LIMIT_REACHED` | 409 |
| `MAILBOX.TEMPLATE_INVALID` | 422 |
| `MAILBOX.TEMPLATE_VARIABLES_MISSING` | 422 |
| `MAILBOX.TEMPLATE_KEY_TAKEN` | 409 |
| `MAILBOX.TEMPLATE_KIND_IMMUTABLE` | 409 |
| `MAILBOX.TEMPLATE_KIND_MISMATCH` | 422 |
| `MAILBOX.TRANSACTIONAL_MESSAGE_NOT_FOUND` | 404 |
| `MAILBOX.RECIPIENT_RATE_LIMITED` | 429 |
| `MAILBOX.TRANSACTIONAL_DISABLED` | 503 |
| `MAILBOX.CONTACT_NOT_FOUND` | 404 |
| `MAILBOX.CONTACT_LIST_NOT_FOUND` | 404 |
| `MAILBOX.CONTACT_EXISTS` | 409 |
| `MAILBOX.CONTACT_LIMIT_REACHED` | 409 |
| `MAILBOX.CONTACT_LIST_NAME_TAKEN` | 409 |
| `MAILBOX.CONTACT_LIST_LIMIT_REACHED` | 409 |
| `MAILBOX.CONTACT_INVALID` | 422 |
| `MAILBOX.CONTACT_IMPORT_INVALID` | 422 |
| `MAILBOX.BROADCAST_NOT_FOUND` | 404 |
| `MAILBOX.UNSUBSCRIBE_INVALID` | 404 |
| `MAILBOX.BROADCAST_NOT_EDITABLE` | 409 |
| `MAILBOX.BROADCAST_INVALID` | 422 |
| `MAILBOX.BROADCAST_NO_RECIPIENTS` | 422 |
| `MAILBOX.BROADCAST_DISABLED` | 503 |
| `MAILBOX.DOMAIN_NOT_FOUND` | 404 |
| `MAILBOX.DOMAINS_DISABLED` | 404 |
| `MAILBOX.DOMAIN_TAKEN` | 409 |
| `MAILBOX.DOMAIN_IN_USE` | 409 |
| `MAILBOX.DOMAIN_INVALID` | 422 |
| `MAILBOX.DOMAIN_LIMIT_REACHED` | 422 |
| `MAILBOX.DOMAIN_ZONE_NOT_FOUND` | 422 |
| `MAILBOX.DOMAIN_NOT_VERIFIED` | 422 |
| `MAILBOX.DOMAIN_PROVIDER_FAILED` | 502 |
| `MAILBOX.FOLDER_NOT_FOUND` | 404 |
| `MAILBOX.LABEL_NOT_FOUND` | 404 |
| `MAILBOX.FOLDER_NAME_TAKEN` | 409 |
| `MAILBOX.LABEL_NAME_TAKEN` | 409 |
| `MAILBOX.FOLDER_LIMIT_REACHED` | 422 |
| `MAILBOX.LABEL_LIMIT_REACHED` | 422 |
| `MAILBOX.SEARCH_TOO_BROAD` | 422 |
| `MAILBOX.WELCOME_NOT_ELIGIBLE` | 409 |
| `MAILBOX.WELCOME_DISABLED` | 409 |
| `MAILBOX.WELCOME_RESEND_LIMITED` | 429 |
| `MAILBOX.ROLE_NOT_FOUND` | 404 |
| `MAILBOX.ROLE_NAME_TAKEN` | 409 |
| `MAILBOX.ROLE_LIMIT_REACHED` | 422 |
| `MAILBOX.PLATFORM_ACCOUNT_NOT_FOUND` | 404 |
| `MAILBOX.PLATFORM_ACCOUNT_TAKEN` | 409 |
| `MAILBOX.ZONE_NOT_FOUND` | 404 |
| `MAILBOX.ZONE_PENDING` | 409 |
| `MAILBOX.ZONE_IN_USE` | 409 |
| `MAILBOX.ZONE_STILL_DELEGATED` | 409 |
| `MAILBOX.ZONE_LIMIT_REACHED` | 422 |
| `MAILBOX.DNS_RECORD_NOT_FOUND` | 404 |
| `MAILBOX.DNS_RECORD_LOCKED` | 409 |
| `MAILBOX.DNS_RECORD_INVALID` | 422 |
| `MAILBOX.DNS_RECORD_REJECTED` | 422 |
| `MAILBOX.TRIAGE_UNAVAILABLE` | 409 |
| `MAILBOX.TRIAGE_MODEL_FAILED` | 502 |
| `MAILBOX.EXTERNAL_CONNECTION_NOT_FOUND` | 404 |
| `MAILBOX.EXTERNAL_CONNECTION_NOT_REVOKED` | 409 |
| `MAILBOX.AGENT_GRANT_NOT_FOUND` | 404 |
| `MAILBOX.AGENT_GRANT_EXISTS` | 409 |
| `MAILBOX.AGENT_GRANT_LIMIT` | 409 |
| `MAILBOX.AGENT_GRANT_INVALID` | 422 |
| `MAILBOX.APP_PASSWORD_NOT_FOUND` | 404 |
| `MAILBOX.APP_PASSWORD_LIMIT_REACHED` | 409 |
| `MAILBOX.MAIL_CLIENT_ACCESS_DISABLED` | 403 |
