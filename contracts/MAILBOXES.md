# Mailbox Contract

This contract owns the optional `mailbox` starter across the Go API, the Web mailbox feature, the
fixed production API adapter, the development mock BFF, and the Cloudflare Email Worker that
delivers inbound mail. It defines mailboxes owned by an organization on the instance's shared
domain or on a domain the organization connected, sending through a mail provider from a session or
an API key, per-mailbox access (personal and shared mailboxes with member permissions), and signed
inbound delivery. It does not define domains on DNS providers other than Cloudflare.

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
(see [Access](#access)), not from the organization role alone:

| Role | May |
|---|---|
| `member` | List domains, managed zones, and their DNS records; preview a domain; list the mailboxes they have access to; read, send, organize, and delete mail as their access allows; read the unified inbox, the log, and statistics of the mailboxes they may read; list the mail roles of the mailboxes they can see; read the platform accounts registered with a mailbox they may read or that they own, with their history |
| `owner`, `admin` | Everything a member may; see every mailbox's metadata; create, rename, and delete mailboxes; list the mailbox directory, and import and export mailboxes; hand over personal mailboxes and delete sealed history; manage the members and webhook switch of shared mailboxes, including themselves; read every mailbox's access history; connect, set up, check, enable receiving on, and remove domains; remove managed zones; and create, change, and delete their DNS records; list every mail role, create, change, and delete mail roles, and put mailboxes in them; list, create, change, and delete every platform account and hand over an owner's accounts; read the usage of each send lane; list the suppression list and remove addresses from it |

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
| Inbound delivery | none | `POST /v1/inbound-mail-messages` | `201`, or `200` for a repeat |
| Delivery event | none | `POST /v1/mail-delivery-events` | `201`, or `200` for a repeat |

The Web production API adapter owns the HttpOnly API session credential and forwards only these
fixed relative paths together with the `Organization-Id` header. Every member response is
`Cache-Control: private, no-store` and varies on `Authorization` and `Organization-Id`. The inbound route
has no browser route: only the mail edge calls it.

## Mailboxes

`POST /v1/mailboxes`:

```json
{
  "local_part": "hello",
  "display_name": "Hello Team",
  "domain": "example.com",
  "type": "shared",
  "members": [{ "user_id": 7, "permission": "send" }]
}
```

- `type` is required: `personal` with `owner_user_id`, or `shared` with `members`. See
  [Access](#access) for what each grants and how invalid access answers.

- `local_part` is trimmed and lowercased. It is 1 to 64 characters of lowercase ASCII letters and
  digits, with single inner `.`, `_`, or `-`. Anything else is `422 MAILBOX.ADDRESS_INVALID`.
- Role addresses (`abuse`, `admin`, `administrator`, `hostmaster`, `mailer-daemon`, `no-reply`,
  `noreply`, `postmaster`, `root`, `security`, `support`, `webmaster`) on the shared domain are
  `422 MAILBOX.ADDRESS_RESERVED`. On a domain the organization connected, the organization
  operates the domain and may create them.
- `display_name` is optional, at most 120 characters, and may not contain `<`, `>`, or `"`; a
  violation is also `422 MAILBOX.ADDRESS_INVALID`.
- The address is `<local_part>@<domain>`. Without `domain`, or with the shared domain, it is the
  instance's `MAILBOX_DOMAIN`. Any other `domain` must be a verified domain of the organization
  (see Domains): a pending one is `422 MAILBOX.DOMAIN_NOT_VERIFIED`, and one the organization did
  not connect is `404 MAILBOX.DOMAIN_NOT_FOUND`.
- `GET /v1/mailbox-domains` lists the shared domain first (`kind: "shared"`), then the
  organization's verified domains (`kind: "organization"`). Every item repeats the instance
  settings: `max_mailboxes` (per organization, across all domains), the outbound `provider`
  (`cloudflare` delivers; `log` only records and is for development), `inbound_enabled`,
  the per-organization lane limits `daily_send_limit` (personal), `daily_broadcast_limit`, and
  `daily_transactional_limit` (see [Send](#send)), `transactional_enabled`,
  `transactional_recipient_limit_per_minute`, `transactional_key_limit_per_second`, and
  `simulator_domain` (see [Transactional Mail](#transactional-mail)), `broadcasts_enabled`, and
  `custom_domains_enabled`.
- An address that is or ever was a mailbox is
  `409 MAILBOX.ADDRESS_TAKEN`: deleting a mailbox keeps its address reserved, so a later owner
  never receives mail meant for the previous one.
- An organization owns at most `MAILBOX_MAX_PER_ORGANIZATION` mailboxes (default 50, valid 1 to
  5000); one more is `409 MAILBOX.LIMIT_REACHED`. `max_mailboxes` reports the value. Lowering it
  below an organization's current count changes no existing mailbox: they all stay listed and
  usable, and only new creates and import rows get `409 MAILBOX.LIMIT_REACHED`.

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "id": 12,
    "address": "hello@mail.example.com",
    "display_name": "Hello Team",
    "type": "shared",
    "origin": "zmail",
    "owner_user_id": null,
    "awaiting_handover": false,
    "sealed_before": null,
    "webhooks_enabled": false,
    "ai_triage_enabled": false,
    "access": { "view": false, "send": false, "manage_members": true, "administer": true, "see_history": true, "organize": false },
    "unread_count": 0,
    "created_at": "2026-11-16T10:00:00Z",
    "role_id": null
  }
}
```

`unread_count` is the number of inbound messages in the default placement (the Inbox) after the seal
boundary that the caller has not read (see [Read State](#read-state)); sent messages and messages that
are archived, in a folder, or in the trash never count. It is `0` for a caller who may not
read the mailbox. `access` is what the caller may do with the mailbox; clients show or hide
controls from it. `origin` is `zmail` or `connected` (see [Access](#access)). `awaiting_handover` is
`true` while a Zmail personal mailbox's owner has left the organization and nobody has taken it over
(see [Departure And Handover](#departure-and-handover)); it is never `true` for a connected mailbox.
`sealed_before` is the seal boundary set at handover, `null` when nothing is sealed.
`webhooks_enabled` is the [webhook switch](#webhook-switch); it is always `false` on a personal
mailbox. `ai_triage_enabled` is the mailbox's [AI triage](#ai-triage) switch. `role_id` is the
[mail role](#mail-roles) the mailbox belongs to, `null` when it has none; clients that do not know it
ignore it.

`GET /v1/mailboxes` lists every mailbox the caller has any access to, oldest first, without
pages (an organization never owns more than 5000). `scope=usable` narrows it to the mailboxes the
caller can read or send from, plus, for owners and admins, personal mailboxes with
`awaiting_handover: true`, so a workspace does not fill with mailboxes an admin can only manage.
Any other `scope` value is `422 COMMON.VALIDATION_FAILED`.

`PUT /v1/mailboxes/:mailbox_id` with `{ "display_name": "Support" }` changes the display name and
answers the mailbox. Only owners and admins may (`403 PERMISSION.DENIED` otherwise); the name
follows the create rules (`422 MAILBOX.ADDRESS_INVALID`), `""` clears it, and `display_name` is
required. A change writes a `renamed` access event; sending the current name writes nothing.
Renaming grants the caller no access to the mail.

Deleting a mailbox deletes its messages in the same transaction; the address stays reserved and
later mail for it is answered `404 MAILBOX.RECIPIENT_NOT_FOUND`. A mailbox on a connected domain
also loses its routing rule; if Cloudflare refuses to delete the rule, the deletion still succeeds
and mail for the address is answered `404` as above. Deleting a user account does not
touch mailboxes: the organization starter already refuses to delete an account that still belongs
to an organization.

### Mailbox Directory

`GET /v1/mailbox-directory` is the administration list. Only owners and admins may read it; a
member gets `403 PERMISSION.DENIED`. It is separate from `GET /v1/mailboxes`, which stays as cheap
as before for the workspace.

- `type` (`shared` or `personal`) narrows the list; absent lists both.
- `q` is a case-insensitive substring of `address` or `display_name`, 1 to 120 characters after
  trimming (longer is `422 COMMON.VALIDATION_FAILED`); `%`, `_`, and `\` match themselves.
- Keyset pages, newest first: `per_page` 1 to 100 (default 15), `cursor` from `meta.next_cursor`. A
  malformed cursor or a `page` parameter is `400 COMMON.INVALID_INPUT`.

```json
{
  "code": 0,
  "message": "success",
  "data": [
    { "id": 12, "address": "docs@mail.example.com", "display_name": "Docs", "type": "shared",
      "owner_user_id": null, "awaiting_handover": false, "created_at": "2026-11-16T10:00:00Z",
      "member_count": 5, "sent_last_24h": 0 }
  ],
  "meta": { "per_page": 15, "has_more": false, "next_cursor": null,
            "daily_send_limit": 200, "sent_last_24h": 37, "mailbox_count": 12, "max_mailboxes": 50 }
}
```

`member_count` is the members of a shared mailbox (0 for a personal one). `sent_last_24h` counts the
mailbox's personal-lane send attempts whose provider call started in the rolling 24 hours, failed ones
included: the same predicate as the personal lane of [Lane Usage](#lane-usage). In `meta`,
`daily_send_limit` is that lane's limit and `sent_last_24h` the organization's attempts in it, the
figure the limit is checked against. Deleting sent mail for good, one message, the trash, sealed
history, or a whole mailbox, frees no room: its attempts still count in both figures until they
leave the window, and so do the bounces and complaints they drew in the sending-health check. A
deleted mailbox has no row, so the rows of every page together add up to `sent_last_24h` less the
attempts of mailboxes deleted in the window. `mailbox_count` counts the
organization's live mailboxes and `max_mailboxes` is `MAILBOX_MAX_PER_ORGANIZATION`. Entries carry no
message content, unread count, or access.

### Import And Export

`POST /v1/mailbox-imports` creates shared mailboxes from spreadsheet rows. Only owners and admins may.

```json
{
  "dry_run": true,
  "rows": [
    { "address": "sales@mail.example.com", "display_name": "Sales", "members": "ana@example.com:manage;bo@example.com" }
  ]
}
```

- 1 to 500 rows; `dry_run` is required. `address` is at most 320 characters, `display_name` 480,
  and `members` 8192; body violations are `422 COMMON.VALIDATION_FAILED`.
- `members` is `email[:permission]` joined by `;`. Segments are trimmed and empty ones ignored; the
  permission is `view`, `send`, or `manage` and defaults to `send`. Emails are matched, ignoring
  case, to the account emails of the organization's current members, on the server only.
- One leading `'` is removed from `address`, `display_name`, and `members` when the next character
  is `=`, `+`, `-`, or `@`, so a file exported by Zmail imports back unchanged.
- Every row is checked exactly like `POST /v1/mailboxes` with `type: "shared"`. A dry run creates
  nothing; a real run creates every row that passes, each in its own transaction, and checks again
  at creation, so a row can pass a dry run and fail the real run. No organization invitation is
  ever sent.

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "dry_run": true, "succeeded": 1, "failed": 1,
    "rows": [
      { "row": 1, "address": "sales@mail.example.com", "status": "valid", "mailbox_id": null, "error_code": null, "missing_emails": [] },
      { "row": 2, "address": "admin@mail.example.com", "status": "failed", "mailbox_id": null, "error_code": "MAILBOX.ADDRESS_RESERVED", "missing_emails": [] }
    ]
  }
}
```

`status` is `valid` (a dry run would create it), `created` (with `mailbox_id`), or `failed` (with
`error_code`). Row error codes:

| `error_code` | Meaning |
|---|---|
| `MAILBOX.ADDRESS_INVALID` | No `@`, a bad local part, or a bad display name |
| `MAILBOX.ADDRESS_RESERVED` | A role address on the shared domain |
| `MAILBOX.ADDRESS_TAKEN` | The address is or was a mailbox |
| `MAILBOX.IMPORT_DUPLICATE_ROW` | An earlier row has the same address, ignoring case |
| `MAILBOX.DOMAIN_NOT_FOUND` | The domain is neither the shared domain nor one the organization connected |
| `MAILBOX.DOMAIN_NOT_VERIFIED` | The organization's domain is still pending |
| `MAILBOX.ACCESS_INVALID` | Bad `members` grammar, 0 or more than 100 members, an unknown permission, or a member listed twice |
| `MAILBOX.IMPORT_MEMBER_NOT_FOUND` | The emails in `missing_emails` are not members of the organization; invite them first |
| `MAILBOX.LIMIT_REACHED` | The organization would pass `MAILBOX_MAX_PER_ORGANIZATION`, counted in row order |
| `MAILBOX.DOMAIN_PROVIDER_FAILED` | Cloudflare refused the routing rule |
| `COMMON.INTERNAL` | Anything else |

### Roster Import

`POST /v1/member-imports` onboards people from a roster in one action: it invites each person to
the organization, reserves each a personal mailbox on a verified organization domain, and gives
each invitation the feature grants the membership starts with. Only owners and admins may.

```json
{
  "dry_run": false,
  "domain": "company.com",
  "features": { "broadcasts": false, "contacts": true, "templates": true, "api_keys": false },
  "rows": [
    { "email": "alice@gmail.com", "name": "Alice Wang", "role": "member", "mailbox": "alice" },
    { "email": "bo@outlook.com", "name": "", "role": "admin", "mailbox": "" }
  ]
}
```

- 1 to 500 rows; `dry_run` and `domain` are required. `email` is required and at most 320
  characters, `name` 480, `role` 16, and `mailbox` 320; body violations are
  `422 COMMON.VALIDATION_FAILED`.
- `email` is the address the person signs in with and accepts the invitation from; it is
  lowercased and must be one address of at most 100 characters. `role` is `member` (the default
  when empty) or `admin`, ignoring case. `name` becomes the mailbox display name.
- `mailbox` is a local part (`alice`) on `domain`, or a whole address whose domain overrides
  `domain`. Empty means the email's local part without a `+tag`. The address must be on a
  verified domain of the organization, never the shared domain.
- `features` is applied to every invitation the request creates; absent means the new-member
  defaults, contacts and templates. Owners and admins always have every feature, so an admin row's
  grants only matter if the admin is later made a member.
- One leading `'` is removed from `email`, `name`, and `mailbox` when the next character is `=`,
  `+`, `-`, or `@`.

Each row reconciles toward the same end state, so sending a file again is safe:

1. A current member who owns a personal Zmail mailbox is skipped as `already_member`. One who owns
   none gets the mailbox at once (`created`); their role and grants are never changed.
2. Anyone else gets the mailbox reserved first, then the invitation, so a refused address never
   sends an invitation. An existing pending invitation or reservation for the same email is
   reused. When both already exist the row is skipped as `already_invited`.
3. An address held by any other mailbox, live or deleted, is skipped as `mailbox_taken`.

A reserved mailbox is a personal mailbox without an owner. Under the ordinary access rules nobody
reads it: owners and admins see its metadata and may delete it, never its mail. Its routing rule
exists from the start, so mail sent before the person joins is kept. When the account whose email
matches the reservation joins the organization (accepting an invitation checks the email), the
mailbox becomes theirs and `owner_assigned` is written to its access history. A revoked or expired
invitation leaves the reservation in place for a later import of the same row.

A dry run makes the same checks, counts the mailbox limit in row order, and writes nothing. A real
run handles each row on its own, so a failed row never undoes another; a row whose invitation
fails after its mailbox was reserved keeps the reservation, and sending that row again finishes it.

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "dry_run": false, "total": 3, "valid": 0, "invited": 1, "created": 0, "skipped": 1, "failed": 1,
    "rows": [
      { "row": 1, "email": "alice@gmail.com", "address": "alice@company.com", "status": "invited", "skip_reason": null, "error_code": null, "mailbox_id": 51, "invitation_id": 12, "email_send_status": "accepted_by_provider" },
      { "row": 2, "email": "sam@gmail.com", "address": "sales@company.com", "status": "skipped", "skip_reason": "mailbox_taken", "error_code": null, "mailbox_id": null, "invitation_id": null, "email_send_status": null },
      { "row": 3, "email": "not-an-email", "address": "", "status": "failed", "skip_reason": null, "error_code": "MAILBOX.IMPORT_EMAIL_INVALID", "mailbox_id": null, "invitation_id": null, "email_send_status": null }
    ]
  }
}
```

`status` is `valid` (dry run only), `invited`, `created`, `skipped` (with `skip_reason`
`already_member`, `already_invited`, or `mailbox_taken`), or `failed` (with `error_code`).
`email_send_status` is the invitation email outcome (`accepted_by_provider`, `failed`, or
`not_configured`) when the row sent one. The invitation link is never returned here; a manager
shares one through a resend. Row error codes:

| `error_code` | Meaning |
|---|---|
| `MAILBOX.IMPORT_EMAIL_INVALID` | The email is not one address of at most 100 characters |
| `MAILBOX.IMPORT_ROLE_INVALID` | The role is neither `member` nor `admin` |
| `MAILBOX.IMPORT_DUPLICATE_ROW` | An earlier row has the same email or mailbox address |
| `MAILBOX.IMPORT_DOMAIN_REQUIRED` | The mailbox would be on the shared domain, or no domain was given |
| `MAILBOX.ADDRESS_INVALID` | A bad local part or display name |
| `MAILBOX.DOMAIN_NOT_FOUND` | The domain is not one the organization connected |
| `MAILBOX.DOMAIN_NOT_VERIFIED` | The organization's domain is still pending |
| `MAILBOX.LIMIT_REACHED` | The organization would pass `MAILBOX_MAX_PER_ORGANIZATION`, counted in row order |
| `MAILBOX.DOMAIN_PROVIDER_FAILED` | Cloudflare refused the routing rule |
| `COMMON.INTERNAL` | Anything else, such as an invitation that could not be stored |

`GET /v1/mailbox-exports?type=shared` (or `personal`; `type` is required, otherwise
`422 COMMON.VALIDATION_FAILED`) answers a CSV file. Only owners and admins may. It is the only
surface that carries member email addresses.

- `Content-Type: text/csv; charset=utf-8` and `Content-Disposition: attachment;
  filename="shared-mailboxes-YYYYMMDD.csv"` (`personal-mailboxes-…`), dated in UTC; the body starts
  with a UTF-8 byte order mark and uses CRLF line ends.
- Header `address,display_name,members,member_count,sent_last_24h,created_at`, then one line per
  mailbox, newest first, at most 5000. `members` is `email:permission` joined by `;`, ordered by
  email; a personal mailbox lists its owner's email alone, and none once the owner left.
  `created_at` is RFC 3339 in UTC.
- A cell starting with `=`, `+`, `-`, or `@` gets a leading `'` so spreadsheets do not run it as a
  formula; importing the file removes it again.

## Access

A mailbox has two independent dimensions. Its `type` says who may use it, and its `origin` says
where its mail comes from:

- `origin` `zmail` mailboxes receive and send at a Zmail address; every mailbox created through
  `POST /v1/mailboxes` is one.
- `origin` `connected` mailboxes hold copies of an outside account (see
  [External Mailboxes](#external-mailboxes)). Whatever the type and permission, nobody gets
  `organize`, and nobody sends from them unless the account was bound with its SMTP server (see
  [Sending From A Connected Mailbox](#sending-from-a-connected-mailbox)); their address never takes delivery, never blocks a Zmail address, and
  does not count toward the organization's mailbox limit. They are never created through
  `POST /v1/mailboxes`. A connected `personal` mailbox is its owner's own account: only the owner
  sees it, with `view` and `see_history`, and the owner and admin roles get nothing on it, so it is
  absent from their mailbox lists, the directory and its `mailbox_count`, the export, the unified
  inbox and its counts, and every route of it answers them `404`. Owners and admins see connected
  accounts only in the [connection directory](#connection-directory), with the address masked.
  Domain receiving and domain deletion ignore connected mailboxes too: an address on an
  organization domain gets no routing rule because of one, and never keeps the domain from being
  deleted. Operator mailbox counts count Zmail mailboxes only.
- A connected `personal` mailbox **assigned from the organization's tenant** (see
  [Tenant Connections](#tenant-connections)) is at the organization's own address, so the owner and
  admin roles get `administer` and `see_history` on it as on a Zmail personal mailbox: it is in the
  directory with its full address, can be deleted and handed over, and its history read. They still
  never read its mail, and it is absent from their `GET /v1/mailboxes` (a tenant may hold thousands;
  the paginated directory lists them).
- Deleting a connected mailbox (only a connected shared one has `administer`) revokes its connection
  in the same operation: the grant is handed back and the token erased, so no stored grant outlives
  its mailbox.

Every mailbox is one of two types:

- **Personal** mailboxes belong to one member, the owner (`owner_user_id`). The owner reads,
  sends, replies, and deletes; nobody else reads or sends, and a personal mailbox has no members.
- **Shared** mailboxes are worked by named members, 1 to 100, each with one permission. Each level
  includes the previous one:

| Permission | Grants |
|---|---|
| `view` | Read messages, threads, attachments, and the mailbox's broadcasts; mark messages read or unread; star them; search |
| `send` | Also send and reply; move messages between placements (archive, folders, trash, and back), and add or remove labels; create, edit, and delete the mailbox's broadcasts |
| `manage` | Also list, add, change, and remove members, and read the access history; delete messages for good and empty the trash; create, rename, and delete folders and labels |

A caller's access is the union of what the organization role, the personal ownership, and the
member row grant. It is returned as `access`:

| Flag | Who has it |
|---|---|
| `view`, `send` | The personal owner; shared members by permission |
| `manage_members` | Owners and admins on shared mailboxes; `manage` members |
| `administer` | Owners and admins: delete the mailbox, hand it over, delete its sealed history |
| `see_history` | Owners and admins; the personal owner; `manage` members |
| `organize` | The personal owner; `manage` members. Never the owner or admin role alone |

A caller with no access at all to a mailbox gets `404 MAILBOX.NOT_FOUND` on every route of it,
exactly like a mailbox that does not exist, and does not see it in `GET /v1/mailboxes`. A caller
with some access but not the one a route needs gets `403 PERMISSION.DENIED`. So an admin who is not
a member sees a mailbox listed but gets `403` when opening its mail; a member's connected personal mailbox is the exception and is never listed for them.

`organize` changes what everyone sees and cannot be undone: it deletes mail for good and changes the
mailbox's folders and labels. An owner or admin who is not a member cannot read the mail, so they do
not get `organize` either; otherwise they could delete mail they cannot see.

| Route | Needs |
|---|---|
| List, search, read a message or thread, counts, list folders and labels, download attachments, mark read or unread, star | `view` |
| Send; move a message to another placement or folder, to the trash, or out of it; set, add, or remove labels | `send` |
| Delete a message that is already in the trash, empty the trash; create, rename, and delete folders and labels | `organize` |
| Delete the mailbox | `administer` |
| List, set, remove members; webhook switch | `manage_members` |
| Hand over; delete sealed history | `administer` |
| Access history | `see_history` |
| Unified inbox and its counts | Any organization member; only mailboxes with `view` are read, the others match nothing |
| Create, replace, and delete mail roles | The `owner` or `admin` organization role; setting a mailbox's mail role needs `administer` |

### Read State

Read state is per person. `read` on a message and `unread_count` on a mailbox are the caller's own:
marking a message read or unread with `PUT .../messages/:message_id` needs `view` and changes it
only for the caller. Stars work the same way: `starred` is the caller's own, starring needs `view`,
and nobody else sees it. Placement, folder, labels, and the trash are shared by everyone with access. Outbound messages are always `read: true` for everyone, and marking one changes
nothing. Reading mail with an API key never changes read state. A person who loses access to a
mailbox (removed as a member, handed over as the former owner, or gone from the organization) loses
their read records and stars for it, so access granted again starts with everything unread and
nothing starred.

Only owners and admins create mailboxes. At creation, a personal mailbox takes an optional
`owner_user_id` (without it the mailbox belongs to the caller) and no `members`; a shared one needs 1 to 100 `members`, each a different `user_id` with `view`,
`send`, or `manage`, and no `owner_user_id`. Every owner and member must be a current member of the
organization. Anything else, including a missing or unknown `type`, is
`422 MAILBOX.ACCESS_INVALID`, and nothing is created. A member who tries is `403 PERMISSION.DENIED`.

`PUT /v1/mailboxes/:mailbox_id/members/:user_id` with `{ "permission": "send" }` adds the user or
changes their permission and answers the member:

```json
{ "user_id": 7, "permission": "send", "granted_by": 3, "created_at": "2026-11-19T10:00:00Z" }
```

`GET .../members` lists members ordered by `user_id`. `DELETE .../members/:user_id` removes one;
an absent member is `404 MAILBOX.NOT_FOUND`. A user who is not a member of the organization, or an
unknown permission, is `422 MAILBOX.ACCESS_INVALID`; a 101st member is also
`422 MAILBOX.ACCESS_INVALID`, also when two additions race. Every member route on a personal
mailbox is `409 MAILBOX.ACCESS_CONFLICT` for anyone who may know the mailbox. Owners and admins may
add themselves.

`GET /v1/mailboxes/:mailbox_id/access-events` pages the access history, newest first, at most 100
per page:

```json
{ "id": 41, "type": "member_set", "actor_user_id": 3, "subject_user_id": 3,
  "detail": { "membership_id": 30, "permission": "view" }, "created_at": "2026-11-19T10:00:00Z" }
```

| `type` | Written when | `subject_user_id` | `detail` |
|---|---|---|---|
| `created` | The mailbox is created | The personal owner, or `null` | `{ "type" }` |
| `member_set` | A member is added or their permission changes | The member | `{ "membership_id", "permission" }` |
| `member_removed` | A member is removed, or leaves the organization | The member | `{}`, or `{ "reason": "left_organization" }` |
| `owner_left` | A personal mailbox's owner leaves the organization | The owner | `{ "reason": "left_organization" }` |
| `owner_returned` | That owner rejoins before a handover | The owner | `{}` |
| `owner_assigned` | The person a [roster import](#roster-import) reserved the mailbox for joins | The new owner | `{}` |
| `handed_over` | An admin hands the mailbox over | The successor | `{ "former_owner_user_id", "sealed_before" }` |
| `sealed_deleted` | An admin deletes the sealed history | `null` | `{ "deleted_messages", "deleted_broadcasts" }` |
| `webhooks_changed` | The webhook switch changes | `null` | `{ "enabled" }` |
| `renamed` | An admin changes the display name | `null` | `{ "from", "to" }` |
| `connection_bound` | A member binds an outside account into a new connected mailbox | The owner | `{ "connection_id", "provider" }` |
| `connection_reconnected` | The owner signs in to the account again | The owner | `{ "connection_id", "provider" }` |
| `connection_revoked` | The owner unbinds the account | The owner | `{ "connection_id", "provider" }` |
| `connection_force_disconnected` | An owner or admin disconnects another member's account | The owner | `{ "connection_id", "provider" }` |
| `connection_revoked_on_leave` | The owner leaves the organization | The owner | `{ "connection_id", "provider" }` |
| `agent_granted` | An [agent grant](#agent-grants) is issued | The key's owner | `{ "grant_id", "api_key_id", "capabilities", "reveal_secrets", "expires_at", "sender_filters", "label_filters" }` (filter counts) |
| `agent_revoked` | An agent grant is revoked, by a person or by Zmail | The key's owner | `{ "grant_id", "api_key_id", "reason" }` |

`actor_user_id` is `null` for a change Zmail made itself, such as `owner_left`, `owner_returned`, `owner_assigned`,
and `member_removed` for a departure. `detail` holds ids, permissions, timestamps, counts, and display
names only, never message content. Connection events never carry the outside account's address. Creating a shared mailbox writes `created` and one `member_set` per first
member. Clients ignore `detail` keys they do not know.

### Departure And Handover

When a user leaves the organization, their access to its mailboxes ends at once and their read
records and stars go with it. Each personal mailbox they owned stays personal, with nobody able to read it,
and shows `awaiting_handover: true`; an `owner_left` event is written. Their memberships of shared
mailboxes are recorded as `member_removed` with `detail.reason = "left_organization"`. If they
rejoin before a handover, their personal mailboxes are theirs again with all mail visible,
`awaiting_handover` turns `false`, and `owner_returned` is written.

`POST /v1/mailboxes/:mailbox_id/handovers` turns a personal mailbox, awaiting handover or not, into
a shared one:

```json
{ "successor_user_id": 9 }
```

- Only owners and admins hand over; anyone else with access gets `403 PERMISSION.DENIED`.
- The successor must be a current member of the organization, otherwise
  `422 MAILBOX.ACCESS_INVALID`. They become a `send` member.
- The mailbox becomes `shared` with `owner_user_id: null` and `awaiting_handover: false`, and the
  former owner keeps no access, no read records, and no stars.
- The seal boundary `sealed_before` is the time the owner left when they are no longer in the
  organization, otherwise the time of the handover. It is written once and never moves. An owner
  who rejoins before the handover commits is sealed at the handover.
- Broadcasts of the mailbox created before the boundary that are still `scheduled`, `sending`, or
  `paused` are cancelled in the same transaction, with their pending recipients, so a sealed
  broadcast sends no new mail the successor could read.
- Messages of the mailbox created before the boundary that still wait for the send limit become
  `failed` with `failure_code` `send.canceled` in the same transaction; they are never sent.
- A shared mailbox, including one a concurrent handover already converted, is
  `409 MAILBOX.ACCESS_CONFLICT`.
- `handed_over` is written, and the answer is `201` with the mailbox as the caller sees it.

A personal mailbox [assigned from a tenant](#tenant-connections) follows these rules too, because its
address is the organization's: when its owner leaves it awaits handover (`owner_left`), its agent
grants end, and nobody reads it, but its connection is **not** revoked and keeps syncing, so mail
that arrives meanwhile is waiting for the successor. The handover seals what arrived before the
departure, makes the mailbox `shared` + `connected`, and makes its connection the organization's
(`shared: true`, no owner). The successor becomes a `send` member, which on a tenant-assigned mailbox
reads only: a mailbox assigned from a tenant is always read-only (its Gmail or Outlook token reads
mail and nothing else, and it never gets an SMTP server of its own), so nobody sends as the address,
neither before nor after the handover; this is the one part of the Zmail rule that cannot apply. A rejoin before the handover restores it as for any personal mailbox. Until then the
assignment cannot be disconnected, purged, deleted, or reassigned, and the worker's retention purge leaves it
alone. The handover does not check the organization's shared connection limit. Whoever is later
assigned the address gets nothing received before the departure or the handover (see
[Tenant Connections](#tenant-connections)).

### Sealed History

Messages created before `sealed_before` are sealed: nobody sees them on any surface. Message list,
search, threads, counts, and detail, every message update, delete, emptying the trash, attachment
download, the log, statistics, `unread_count`, and
`GET /v1/outbound-mail-messages/:message_id` treat a sealed message as absent
(`404 MAILBOX.MESSAGE_NOT_FOUND` by id). Broadcasts of the mailbox created before the boundary are
left out of the broadcast list and are `404 MAILBOX.BROADCAST_NOT_FOUND` by id; their recipients
are not listed. An `Idempotency-Key` that matches a sealed message is
`409 MAILBOX.IDEMPOTENCY_CONFLICT` with no message data, and nothing is sent.

Delivery events for a sealed message that arrive after the handover are stored but stay hidden,
count nowhere, and publish no webhook. A permanent bounce or complaint among them still marks the
contact `suppressed`: suppression protects the organization's sending reputation, so sealing does
not exempt it.

`DELETE /v1/mailboxes/:mailbox_id/sealed-messages` deletes the sealed history for good:

```json
{ "confirm_address": "alice@mail.example.com" }
```

- Only owners and admins delete it; anyone else with access gets `403 PERMISSION.DENIED`.
- A mailbox without a seal boundary is `409 MAILBOX.ACCESS_CONFLICT`.
- `confirm_address` must equal the mailbox address, ignoring case, otherwise
  `422 MAILBOX.ACCESS_INVALID` and nothing is deleted.
- Sealed broadcasts still `scheduled`, `sending`, or `paused` are cancelled first; then the sealed
  broadcasts and their recipients, and the sealed messages with their events and attachments, are
  deleted in one transaction. The boundary stays.
- `sealed_deleted` is written with the counts, and the answer is:

```json
{ "code": 0, "message": "success", "data": { "deleted_messages": 312, "deleted_broadcasts": 2 } }
```

### Webhook Switch

`PUT /v1/mailboxes/:mailbox_id/webhook-setting` with `{ "enabled": true }` turns mail
[webhooks](#webhooks) for a shared mailbox on or off and answers `200` with the mailbox. It needs
`manage_members`; on a personal mailbox it is `409 MAILBOX.ACCESS_CONFLICT`. A change writes
`webhooks_changed`; setting the current value writes nothing. Mailboxes start with the switch off.

Mailboxes that existed before access rules are shared, with their creator as a `manage` member
while the creator is still in the organization. A mailbox left without members is visible only to
owners and admins, who add members to it.

## Mail Roles

A mail role groups an organization's mailboxes by the business identity they speak for, such as a
brand, a column, or a person, for navigation and display. A mailbox belongs to at most one mail
role; a mailbox without one is ungrouped. A mail role is not an organization role or an access role:
it grants and revokes nothing, and its owner gets no access to its mailboxes. Assigning a mailbox
writes no access event.

```json
{
  "id": 3,
  "name": "Design desk",
  "color": "orange",
  "description": "Behance and Medium accounts",
  "owner_user_id": 7,
  "mailbox_ids": [14, 15],
  "created_at": "2026-11-19T10:00:00Z",
  "updated_at": "2026-11-19T10:00:00Z"
}
```

`GET /v1/mail-roles` lists at most 50 mail roles by name ignoring case, then by id. Owners and admins
see every mail role, with every mailbox in it. Members see only the mail roles that hold at least one
mailbox they can see (one listed by `GET /v1/mailboxes`), and `mailbox_ids` holds only those
mailboxes, ascending. `owner_user_id` is `null` when the user is no longer a member.

`POST /v1/mail-roles` (`201`) and `PUT /v1/mail-roles/:role_id` (`200`) take the same body and need the
`owner` or `admin` organization role; `PUT` replaces every field, an omitted one taking its default,
and the last write wins:

```json
{ "name": "Design desk", "color": "orange", "description": "", "owner_user_id": 7 }
```

| Field | Rule | Key in `errors` |
|---|---|---|
| `name` | Required; trimmed, then 1 to 64 characters, unique in the organization ignoring case | `name` |
| `color` | `gray`, `red`, `orange`, `yellow`, `green`, `teal`, `blue`, `purple`, or `pink`; defaults to `gray` | `color` |
| `description` | Trimmed, then at most 280 characters without control characters; defaults to empty | `description` |
| `owner_user_id` | A current member of the organization, or `null`; defaults to `null`. For display only | `owner_user_id` |

Unknown fields are ignored. A body over 4 KiB or that is not a JSON object is
`400 COMMON.INVALID_INPUT`; a field that breaks its rule is `422 COMMON.VALIDATION_FAILED`. A taken
name is `409 MAILBOX.ROLE_NAME_TAKEN`. An organization has at most 50 mail roles; one more is
`422 MAILBOX.ROLE_LIMIT_REACHED`, and concurrent creates never pass the limit. A member gets
`403 PERMISSION.DENIED`. A mail role that does not exist or belongs to another organization is
`404 MAILBOX.ROLE_NOT_FOUND`. Checks run in the order `400`, `403`, `404`, `422`, `409`.

`DELETE /v1/mail-roles/:role_id` needs the `owner` or `admin` role and answers `204`; its mailboxes
become ungrouped in the same statement. Deleting it again is `404 MAILBOX.ROLE_NOT_FOUND`.

`PUT /v1/mailboxes/:mailbox_id/role-assignment` puts a mailbox in a mail role or, with `null`, takes
it out, and answers `200` with the mailbox:

```json
{ "role_id": 3 }
```

`role_id` is required and is a positive integer or `null`; anything else is
`422 COMMON.VALIDATION_FAILED` keyed `role_id`. It needs `administer` on the mailbox, so only owners
and admins may assign: a caller with no access gets `404 MAILBOX.NOT_FOUND`, one with access but not
`administer` gets `403 PERMISSION.DENIED`. A mail role that does not exist, belongs to another
organization, or is deleted meanwhile is `404 MAILBOX.ROLE_NOT_FOUND`. Setting the current value
succeeds and changes nothing.

When a member leaves the organization, every mail role they owned gets `owner_user_id` `null`.

## Platform Accounts

The platform account ledger records the organization's accounts on outside platforms (Medium,
Behance, and so on): the mailbox each was registered with, its mail role, the user responsible for
it, and its status, with a history of handovers and status changes. Like mail roles, the ledger
grants and revokes no mailbox access.

```json
{
  "id": 12,
  "platform": "Medium",
  "handle": "@acme",
  "profile_url": "https://medium.com/@acme",
  "role_id": 3,
  "mailbox_id": 14,
  "mailbox_address": "design@mail.example.com",
  "owner_user_id": 7,
  "status": "active",
  "notes": "",
  "created_at": "2026-11-20T10:00:00Z",
  "updated_at": "2026-11-20T10:00:00Z"
}
```

Owners and admins see and write every account. A member sees, read-only, only the accounts
registered with a mailbox they may read (`view`) or whose `owner_user_id` is them; any other
account does not exist for them, so the ledger never reveals another member's private mailbox. For
an account a member owns but whose mailbox they cannot see, `mailbox_address` is `null`.
`mailbox_address` is also `null` without a mailbox or when the mailbox was deleted.

`GET /v1/platform-accounts` is paginated (`page`, `per_page`) and ordered by platform, then handle,
both ignoring case, then id. Filters, all optional and combined with AND:

| Parameter | Matches |
|---|---|
| `q` | The platform, the handle, or the registration address, anywhere and ignoring case; `%`, `_`, and `\` match themselves. Trimmed; at most 100 characters. For a member, the address part only matches mailboxes they can see |
| `role_id`, `mailbox_id`, `owner_user_id` | That id exactly |
| `status` | One status |

A malformed id, an unknown status, or a longer `q` is `400 COMMON.INVALID_INPUT`.

`POST /v1/platform-accounts` (`201`) and `PUT /v1/platform-accounts/:account_id` (`200`) take the
same body and need the `owner` or `admin` role; `PUT` replaces every field, an omitted one taking
its default, and the last write wins:

```json
{ "platform": "Medium", "handle": "@acme", "profile_url": "https://medium.com/@acme",
  "role_id": 3, "mailbox_id": 14, "owner_user_id": 7, "status": "active", "notes": "" }
```

| Field | Rule | Key in `errors` |
|---|---|---|
| `platform` | Required; trimmed, then 1 to 64 characters without control characters | `platform` |
| `handle` | Required; trimmed, then 1 to 200 characters without control characters | `handle` |
| `profile_url` | Trimmed; empty for none, otherwise an absolute `http` or `https` URL with a host and no credentials, at most 2048 bytes | `profile_url` |
| `role_id` | A mail role of the organization, or `null` (the default) | `role_id` |
| `mailbox_id` | A live mailbox of the organization, or `null` (the default) | `mailbox_id` |
| `owner_user_id` | A current member of the organization, or `null` (the default) | `owner_user_id` |
| `status` | `invited`, `awaiting_confirmation`, `active` (the default), `timed_out`, `needs_review`, or `retired` | `status` |
| `notes` | Trimmed, then at most 2000 characters; defaults to empty | `notes` |

Unknown fields are ignored. A body over 8 KiB, that is not a JSON object, or with a field of the
wrong JSON type is `400 COMMON.INVALID_INPUT`; a field that breaks its rule is
`422 COMMON.VALIDATION_FAILED`. Platform and handle are unique in the organization ignoring case;
a taken pair is `409 MAILBOX.PLATFORM_ACCOUNT_TAKEN`. A member gets `403 PERMISSION.DENIED`. An
account that does not exist or belongs to another organization is
`404 MAILBOX.PLATFORM_ACCOUNT_NOT_FOUND`. Checks run in the order `400`, `403`, `404`, `422`,
`409`. Deleting a mail role, mailbox, or user sets the matching field to `null`; a member who
leaves the organization stays `owner_user_id` until their accounts are handed over.

`DELETE /v1/platform-accounts/:account_id` needs the `owner` or `admin` role and answers `204`.

`GET /v1/platform-accounts/:account_id/events` lists the account's history, newest first,
paginated, with the listing's visibility (a hidden account is
`404 MAILBOX.PLATFORM_ACCOUNT_NOT_FOUND`):

```json
{ "id": 40, "account_id": 12, "kind": "owner_changed", "actor_user_id": 1,
  "from": "7", "to": "9", "created_at": "2026-11-21T09:00:00Z" }
```

| `kind` | Written when | `from` / `to` |
|---|---|---|
| `created` | The account is added | `null` / the status |
| `status_changed` | A write changes the status | The statuses |
| `owner_changed` | A write or a handover changes the owner | User ids as text, `null` for none |
| `mailbox_changed` | A write changes the registration mailbox | Mailbox ids as text, `null` for none |
| `deleted` | The account is deleted | The last status / `null` |

A write that changes none of status, owner, and mailbox records no event. The `deleted` event is
kept after the account is gone, but the account's events are no longer listed.

`POST /v1/platform-account-handovers` moves every account of the organization owned by one user to
another in one transaction, records an `owner_changed` event for each, and answers `201`:

```json
{ "from_user_id": 7, "to_user_id": 9 }
```

```json
{ "from_user_id": 7, "to_user_id": 9, "account_ids": [12, 15] }
```

It needs the `owner` or `admin` role. `to_user_id` must be a current member other than
`from_user_id`; `from_user_id` may have left the organization. Either breaking its rule is
`422 COMMON.VALIDATION_FAILED` keyed by that field. Nothing to move still answers `201` with an
empty `account_ids`. It is independent of a mailbox handover: handing over a personal mailbox moves
no platform account.

## External Mailboxes

A member can bind an outside account through a **connector**: Gmail, Outlook, or any
provider the instance adds later. Its mail is copied into a `personal` mailbox with `origin`
`connected` that the member owns alone (see [Access](#access)); every copy is inbound, `received`, carries the connector id as
`source` (`gmail`, `outlook`, ...), and the provider's receive time as `created_at`. A copy is stored
once per mailbox and provider message id, so a reconnect or a repeated fetch never adds a second
one. The first sync looks back 14 days unless the binding asked for 1 to 90.

Connector ids match `^[a-z][a-z0-9_]{1,15}$`; a new connector adds ids without a contract change, so
clients render an id they do not know generically. A connected mailbox carries the outside account's
address, but that address is never Zmail's to deliver to: inbound delivery, sending through Zmail's
provider, and the address check ignore connected mailboxes. Within one organization and connector an outside account belongs
to one member at a time, or to the organization as a [shared connected mailbox](#shared-connected-mailboxes);
the same address may still exist under another connector or as a Zmail mailbox, which is all the
relaxed address index allows.

Every newly stored message, delivered by Zmail or synced, goes through the same post-store step in
the transaction that stores it: it is queued for [AI triage](#ai-triage) when the switches allow,
and announced as `mail.message.received` (webhooks still publish for shared mailboxes only). Only a
connected personal mailbox's owner may switch triage on for it. A connected mailbox whose connection
is not `active` queues nothing for triage and hands no queued message to the model, and any revoke,
including the one when the owner leaves, turns its switch off and drops its queued messages.

While the instance enables no connector (`EXTERNAL_MAIL_PROVIDERS` empty, the default) the connector
list is empty, starting a sign-in answers `404 MAILBOX.EXTERNAL_PROVIDER_UNAVAILABLE`, the connection
routes answer `404 MAILBOX.EXTERNAL_CONNECTION_NOT_FOUND`, and no account is synced.

### Binding

`GET /v1/external-mail-connectors` lists the instance's connectors in a stable order:
`{ "id": "gmail", "auth_kind": "oauth2", "capabilities": { "read": true, "send": false, "tenant": true } }`.
`capabilities.send` is `true` for a connector that can also send through the account's own server
(`imap`, when bound with `smtp_host`; see [Sending From A Connected Mailbox](#sending-from-a-connected-mailbox)).
`tenant` says an owner may authorize a whole tenant of the provider (see
[Tenant Connections](#tenant-connections)).

`POST /v1/external-mail-authorizations` with `{ "provider": "gmail" }` (optionally
`"backfill_days"`) starts a sign-in; with `{ "connection_id": 3 }` it reconnects that connection,
which must be the caller's own. Neither `provider` nor `connection_id`, a `provider` other than the
connection's, or a `backfill_days` outside 1 to 90 is `422 MAILBOX.EXTERNAL_AUTHORIZATION_INVALID`,
as on the app password route below; `0` or no `backfill_days` takes 14. It stores a pending sign-in,
single use and valid for 10 minutes, holding only the SHA-256 of a random `state` and the sealed
PKCE verifier, and answers `201`
`{ "authorization_url", "expires_at" }`. The browser goes to `authorization_url`; the provider sends
it back to `EXTERNAL_MAIL_REDIRECT_URL` with `state` and `code`.

`POST /v1/external-mail-authorizations/completions` with `{ "state", "code" }` uses the pending
sign-in up, whatever the outcome, then exchanges the code with the PKCE verifier:

| Account signed in to | Result |
|---|---|
| New in the organization for this connector | `201`: a private, read-only connected personal mailbox and an active connection, synced at once (`connection_bound`) |
| The caller's own connection, in any state | `200`: the same connection, active, error cleared, synced at once; mailbox and history kept (a `revoked` one backfills again, without copies, and needs a free place under the limit below) (`connection_reconnected`) |
| A reconnect that signed in to another account | `409 MAILBOX.EXTERNAL_ACCOUNT_MISMATCH`; nothing changes |
| Bound by another member, connection not `revoked` | `409 MAILBOX.EXTERNAL_ACCOUNT_TAKEN` |
| Bound by another member whose connection is `revoked` (they left, unbound it, or were disconnected) | `201`: the old connection's copies, mailbox, and connection are deleted for good at once (`copies_purged` with `reason` `rebound` and the caller as actor), then the account is bound into a new mailbox; `409 MAILBOX.EXTERNAL_ACCOUNT_TAKEN` only while the sync worker holds the old connection |
| A new account, or the caller's own `revoked` connection, while the caller holds 10 live (`active` or `needs_reconnect`) connections | `409 MAILBOX.EXTERNAL_CONNECTION_LIMIT`; nothing changes. Reconnecting a live connection never hits the limit |

On a `409` the grant is handed back to the provider. A refused code is
`422 MAILBOX.EXTERNAL_AUTHORIZATION_FAILED`; an unknown, expired, used, or another member's `state`
is `404 MAILBOX.EXTERNAL_AUTHORIZATION_NOT_FOUND`. Both routes are session only and never cached.

### Binding With An App Password

Connectors whose `auth_kind` is `password` never redirect: the generic `imap` connector (enabled by
`imap` in `EXTERNAL_MAIL_PROVIDERS`) reads INBOX of any provider that offers IMAP over TLS and app
passwords, such as iCloud, Yahoo, Fastmail, QQ, 163, or a self-hosted server.

`POST /v1/external-mail-credentials` takes
`{ "provider": "imap", "host", "port"?, "username", "password", "smtp_host"?, "smtp_port"?, "backfill_days"? }`.
Before anything is stored the server signs in once over implicit TLS on port 993 (the only port
accepted) with a verified certificate, and opens INBOX read-only, within 45 seconds in all. A host
that resolves to a loopback, private, shared, benchmarking, reserved, link-local, multicast, or
unspecified address, or to an IPv6 form embedding an IPv4 address (NAT64, 6to4, IPv4-compatible), is
refused.

The bound account is never named by the caller: it is the lowercased `username` when it contains
`@`, otherwise `username@host`. The sealed grant keeps the lowercased host, and restoring a
connection with credentials for another host starts its backfill over. The outcomes are those of a
completed sign-in: `201` for a new account, `200` for the caller's own connection restored with the
new password, `409 MAILBOX.EXTERNAL_ACCOUNT_TAKEN`, and `409 MAILBOX.EXTERNAL_CONNECTION_LIMIT`. In
addition:

| Failure | Result |
|---|---|
| The server refused the username or password | `422 MAILBOX.EXTERNAL_AUTHORIZATION_FAILED` |
| The host does not resolve, is not allowed, does not answer over TLS in time, its certificate does not verify, or the server refused the login for a transient reason (`UNAVAILABLE`, `LIMIT`, `INUSE`, `SERVERBUG`, `CONTACTADMIN`, or a `BAD`) | `422 MAILBOX.EXTERNAL_SERVER_UNREACHABLE` |
| A value carries a control character, or `backfill_days` is out of range | `422 MAILBOX.EXTERNAL_AUTHORIZATION_INVALID` |
| More than 10 attempts by the member in 15 minutes, or 30 by the organization's members in an hour | `429 COMMON.RATE_LIMITED` with `Retry-After` |
| The connector is not enabled or binds with OAuth | `404 MAILBOX.EXTERNAL_PROVIDER_UNAVAILABLE` |

The password is sealed at rest with the connection's grant, never logged, never returned, and never
written to audit metadata. The sync reads INBOX only and never changes the account: messages are
fetched with `BODY.PEEK`, and a message larger than 5 MiB is skipped. One sync of a connection takes
at most 45 seconds and bounds the bytes it accepts; a message the server fails to deliver is retried
on its own once and then skipped, so it never blocks the mailbox. Copies carry `source` `imap` and
are known by their Message-ID, so a mailbox the provider rebuilds (a new UIDVALIDITY) is read again
from the backfill window without second copies. Only a refused login without a transient reason
moves the connection to `needs_reconnect`; posting the credentials again restores it. Unbinding erases the stored password; revoking the app password itself is done at
the provider. The route is session only and never cached.

### Sending From A Connected Mailbox

An `imap` binding may name the account's outgoing server: `smtp_host`, and `smtp_port` 465
(implicit TLS, the default) or 587 (STARTTLS, required). The server then also signs in there with
the same login and certificate rules, within the same 45 seconds, and a refusal answers as for
IMAP. The connected mailbox then has `send` access for whoever may send from it (its owner, or a
shared connected mailbox's members with `send`), and a message written there, a reply included, is
delivered through that server from the account's address instead of through Zmail's provider. It
counts against the organization's send limits like any personal send, and a refusal by the server
fails the message with `failure_code` `smtp_auth_failed`, `smtp_rejected`, `smtp_unavailable`, or
`smtp_not_connected` (the connection is not active). Binding the account again without
`smtp_host` makes the mailbox read-only again. Gmail and Outlook connections stay read-only.

### Shared Connected Mailboxes

An `owner` or `admin` binds a **team account** hosted at an outside provider (for example
`support@` at Google Workspace) into a `shared` mailbox with `origin` `connected`, and assigns its
members exactly as `POST /v1/mailboxes` creates a shared mailbox. Both binding routes take one
optional object; without it the personal flow above is unchanged:

```json
{ "provider": "gmail",
  "sharing": { "display_name": "Support",
               "members": [ { "user_id": 7, "permission": "manage" }, { "user_id": 9, "permission": "view" } ] } }
```

`POST /v1/external-mail-authorizations` stores `sharing` with the pending sign-in;
`POST /v1/external-mail-credentials` takes it next to the credentials. `members` follows the shared
mailbox rules: 1 to 100 distinct current members, each `view`, `send`, or `manage`
(`422 MAILBOX.ACCESS_INVALID`). `display_name` is optional, at most 120 characters (not bytes, as
for every mailbox), and defaults to the account address cut to 120 characters
(`422 MAILBOX.ADDRESS_INVALID` when invalid). A caller who is not an `owner` or `admin` gets
`403 PERMISSION.DENIED`, checked before any attempt is counted or any server contacted; `sharing`
with `connection_id` is `422 MAILBOX.EXTERNAL_AUTHORIZATION_INVALID`. The role and the members are
checked again when the sign-in completes, before the code is exchanged, so a member who left or a
role that changed in between refuses the completion (`422 MAILBOX.ACCESS_INVALID` or
`403 PERMISSION.DENIED`) and nothing is stored. A shared sign-in is completed only by an API version
that knows shared bindings; an older one answers `404 MAILBOX.EXTERNAL_AUTHORIZATION_NOT_FOUND`.

A new account creates, in one transaction, the mailbox, its `created` and `member_set` access
events, the connection, and `connection_bound`, and answers `201` with `"shared": true`. The
connection belongs to the **organization**:

- It has no owner. Who bound it is recorded for the history only; it never counts toward that
  person's 10 connections, and leaving the organization does not revoke it.
- An organization holds at most `EXTERNAL_MAIL_MAX_SHARED_CONNECTIONS` (20 by default, 1 to 100)
  live (`active` or `needs_reconnect`) shared connections; one more, or restoring a revoked one at
  the limit, is `409 MAILBOX.EXTERNAL_CONNECTION_LIMIT`. Like every connected mailbox it is outside
  the organization's mailbox limit.
- An account is either a member's own or the team's: a live connection of the other kind, or a live
  shared connection of the same account, is `409 MAILBOX.EXTERNAL_ACCOUNT_TAKEN`. A revoked personal
  connection is freed for a shared binding as for any other binder (purged with `reason` `rebound`).
  A revoked shared connection stays the team's while its mailbox exists: it is restored by a
  reconnect, never taken over; once the mailbox is deleted, a new binding of either kind purges it
  and binds fresh. Converting a member's connection into a shared one, or back, is not offered:
  unbind and bind again.

Access follows the shared mailbox rules (see [Access](#access)), minus what the read-only origin
forbids: members read with their own read state according to `view`, `send`, or `manage`; nobody
sends, replies, composes from it, deletes mail for good, or changes its folders, whatever the
permission; an `owner` or `admin` who is not a member manages members, webhooks, and the history but
reads no mail until adding themselves, which is recorded. The member, webhook, and AI triage
switches and agent grants follow the shared mailbox matrix; an agent grant never gets `draft`. Handover
does not apply, and deleting the mailbox revokes its connection in the same transaction.

Whoever manages the mailbox's members (a `manage` member, an `owner`, or an `admin`) manages the
connection: they reconnect it with `connection_id` on either binding route (the same account, or
`409 MAILBOX.EXTERNAL_ACCOUNT_MISMATCH`; checked again at completion), unbind it, and read its sync
runs. Purging a revoked shared connection at once deletes the mailbox with every copy and member, so
only an `owner` or `admin`, who may delete the mailbox anyway, may do it; a `manage` member gets
`403 PERMISSION.DENIED`, and the worker's purge after the retention is unchanged. Everyone else gets
`404 MAILBOX.EXTERNAL_CONNECTION_NOT_FOUND`, and so does anyone once the mailbox is deleted. An unbind follows the revoke path below and records
`connection_revoked` with the caller as actor, plus an organization audit entry with `reason`
`shared_disconnect`, filed like a forced disconnect under the caller and every other `owner` and
`admin`; the mailbox and its members stay until the retention ends, the connection is
reconnected, or the mailbox is deleted.

### Connections

`GET /v1/external-mail-connections` lists the caller's own connections in the organization, then
the shared connections the caller manages, at most 220 in all; it never carries a token. The
caller's own live connections come first, id ascending (at most 10, the limit), then their 10 most
recently revoked ones, newest revocation first, so a member at the limit still sees the revoked ones
whose copies they may purge or rebind. Then come the caller's personal mailboxes
[assigned from a tenant](#tenant-connections), live ones first, at most 100, with `tenant_id` set (it
is `null` otherwise). Then come the shared connections the caller manages (every one for an `owner`
or `admin`, those of mailboxes held with `manage` otherwise) whose mailbox still exists, live ones
first and then id ascending, at most 100. Revoked connections are kept so a rebind restores the same
mailbox, and do not count toward the limits:

```json
{ "id": 3, "provider": "gmail", "account_address": "me@gmail.com", "mailbox_id": 41, "shared": false,
  "tenant_id": null, "status": "active", "last_synced_at": "2026-11-20T08:00:00Z", "last_error_code": null,
  "created_at": "2026-11-19T08:00:00Z", "push": null }
```

`push` is the connection's [push subscription](#push-subscriptions) while push is on, and `null` when
push is off or the connection has none.

| `status` | Meaning |
|---|---|
| `active` | Synced every two minutes, or within seconds of new mail while a [push subscription](#push-subscriptions) is shown to work |
| `needs_reconnect` | The provider refused the grant (`last_error_code` `external.auth_failed`); syncing stops until the member reconnects |
| `revoked` | Unbound; never synced again |

A provider failure that is not about the grant keeps the connection `active` with
`last_error_code` `external.fetch_failed` and retries with exponential backoff: two minutes after
the first failure in a row, then 4, 8, 16, 32 minutes, and at most one hour, each less up to a fifth
of random jitter. A successful fetch resets the backoff to the two-minute interval. A connection whose
connector the instance no longer runs stays `active` with its grant and records
`external.provider_unavailable` until the connector is enabled again.

`DELETE /v1/external-mail-connections/:connection_id` unbinds: the member who bound the account
(`connection_revoked`), or an `owner` or `admin` (`connection_force_disconnected`), may. The grant is revoked at the provider on a best-effort basis, the stored
token is erased, and syncing stops for good, including a fetch already running. Mail already synced
stays in the connected mailbox until the retention below ends or the member purges it. Unbinding a revoked connection answers `204` again and records
nothing. A member who leaves the organization has every connection there revoked the same way at
once (`connection_revoked_on_leave`): the accounts are their own, so the connected mailbox never
awaits a handover, the handover routes refuse it, and the copies stay readable by nobody.
Binding the account again after rejoining restores the same mailbox without copies. Another member's
connection and another organization's id are `404 MAILBOX.EXTERNAL_CONNECTION_NOT_FOUND`.

### Outlook

The `outlook` connector runs when `EXTERNAL_MAIL_PROVIDERS` lists it and its OAuth client is set
(`EXTERNAL_MAIL_OUTLOOK_CLIENT_ID`, `EXTERNAL_MAIL_OUTLOOK_CLIENT_SECRET`, and
`EXTERNAL_MAIL_OUTLOOK_TENANT`, `common` by default). It signs in through the Microsoft identity
platform with the delegated scopes `offline_access Mail.Read User.Read openid email` and binds the
account's `mail` address, or its user principal name when it has none. It syncs the Inbox through
Microsoft Graph message delta; Sent Items, Drafts, Deleted Items, and Junk Email are not synced, and
neither are drafts. A message removed at Microsoft stays in the connected mailbox. Graph returns at
most about 5,000 messages in the first sync's look-back, so the first sync of a busier Inbox may
stop short of it; later syncs read every new message. A body over 256 KiB is cut there, and a message whose
body Graph cannot return within the connector's bound is kept with its headers only. Unbinding erases
the stored grant; Microsoft has no per-grant revocation, so the member removes the app's consent in
their Microsoft account to end it there as well.

### Gmail

The `gmail` connector signs in on Google's consent page with the read-only Gmail scope and offline
access. The first sync copies the backfill window newest first, a page at a time, and then follows
the account's history from where the backfill began, so mail that arrives during the backfill is
not missed. Only inbound mail is copied: drafts, spam, trash, and sent mail that is not also in the
inbox are skipped; mail moved out of spam into the inbox is copied then. When Google no longer
replays the history, the sync starts the backfill over without copies. Only a grant Google refuses
or one that lost access to the mailbox moves the connection to `needs_reconnect`; rate limits,
quotas, outages, and a misconfigured OAuth client are retried at the next interval.

### Sync Budget

Each pass of the `mail:external:sync` worker takes due connections round-robin across providers:
the oldest due connection of every provider, then the second of every provider, and so on, and at
most `EXTERNAL_MAIL_PROVIDER_FETCHES_PER_PASS` (20 by default) of one provider. One provider's
outage or one large backfill therefore never starves the others. Each fetch has its own deadline,
`EXTERNAL_MAIL_FETCH_TIMEOUT` (60 seconds by default): a fetch that hits it is `fetch_failed`, backs
off, and the pass moves on to the next connection. A connection whose provider has another page
ready is fetched again at once, for at most `EXTERNAL_MAIL_MAX_IMMEDIATE_PAGES` (20 by default)
pages in a row; after that each further page waits `EXTERNAL_MAIL_MIN_FETCH_SPACING` (5 seconds by
default), so one backfill cannot monopolize the worker. A connection whose cursor the provider no
longer honors starts over from an empty cursor at once; the restart counts toward the same bound.

### Sync Runs

Every fetch records one sync run, in the transaction that saves its result. A run holds times,
counts, and codes, never message data. The worker keeps a connection's latest 50 runs and none older
than 14 days.

`GET /v1/external-mail-connections/:connection_id/sync-runs` lists the latest runs of the caller's
own connection, or of a shared connection they manage, newest first, at most 50. Anyone else, an `owner` or `admin` included, gets
`404 MAILBOX.EXTERNAL_CONNECTION_NOT_FOUND`. The response is never cached:

```json
{ "id": 912, "started_at": "2026-11-20T08:00:00Z", "finished_at": "2026-11-20T08:00:01Z",
  "outcome": "ok", "fetched_count": 25, "stored_count": 24, "more": true, "error_code": null,
  "timed_out": false, "throttled": false, "trigger": "poll" }
```

`trigger` is `poll` for a sync on the schedule and `push` for one that answered a provider
notification (see [Push Subscriptions](#push-subscriptions)).

| `outcome` | Meaning | `error_code` |
|---|---|---|
| `ok` | A page was saved | `null` |
| `auth_failed` | The provider refused the grant; the connection needs reconnecting | `external.auth_failed` |
| `fetch_failed` | Any other provider failure; the next attempt backs off | `external.fetch_failed` |
| `cursor_expired` | The provider no longer honored the cursor; the sync starts over within the backfill window without repeating stored copies | `null` |
| `provider_unavailable` | The instance no longer runs the connector | `external.provider_unavailable` |
| `dropped` | The connection was revoked or lost its grant while the fetch ran; nothing fetched was stored | `null` |

`fetched_count` is how many messages the provider returned and `stored_count` how many were new;
`more` says the provider had another page ready. `timed_out` marks a `fetch_failed` fetch that hit
its deadline, and `throttled` a page after which the next one waits the spacing because the
connection already fetched the most pages in a row allowed at once.

### Retention Of Revoked Copies

Revoking a connection, by its member, an `owner` or `admin`, or because the member left the
organization, starts a retention of `EXTERNAL_MAIL_REVOKED_RETENTION` (720 hours, 30 days, by
default; at least 24 hours, written as a duration such as `720h` or a day count such as `90d`; a
value that does not parse stops startup). Binding the same account again before it ends keeps the
mailbox and its copies and cancels the deletion. When it ends, the mail worker (`mail:broadcast:work`
ticks, which run whenever the mailbox starter is on, with or without `EXTERNAL_MAIL_PROVIDERS`)
deletes the copies for good in bounded batches, then deletes
the connected mailbox and the connection with its sync runs, and stores a `copies_purged` access
event on the deleted mailbox with `{ "connection_id", "provider", "reason": "retention" }`, the
member who bound the account as its subject. The event is an audit record: the access history route
of a deleted mailbox answers `404`, so it is never listed. Binding the account afterwards starts a
new mailbox.

`POST /v1/external-mail-connections/:connection_id/purges` deletes the copies of the caller's own
revoked connection, or, for an `owner` or `admin`, of a revoked shared connection, at once, the same way, with `reason` `owner` and the caller as the actor:

| Case | Answer |
|---|---|
| Every copy, the mailbox, and the connection are deleted | `200` `{ "deleted_messages": 120, "completed": true }` |
| More copies than one request deletes | `202` `{ "deleted_messages": 5000, "completed": false }`: the rest is due at once and the worker deletes it in its next passes |
| The connection is not revoked | `409 MAILBOX.EXTERNAL_CONNECTION_NOT_REVOKED` |
| Another member's connection, an `owner` or `admin` included | `404 MAILBOX.EXTERNAL_CONNECTION_NOT_FOUND` |

The route works while `EXTERNAL_MAIL_PROVIDERS` is empty, so switching the feature off never
strands copies. A purge only ever touches the connection's own mailbox, and only when that mailbox
holds synced copies; a purge that fails is logged and retried an hour later without holding up the
connections revoked after it. A claimed connection whose mailbox is gone is not fetched: its grant
is handed back, it is revoked, and its run is recorded as `dropped`.

### Push Subscriptions

Gmail and Outlook can tell Zmail that a connected mailbox received mail, so it syncs within seconds
instead of at the next poll. Push is off unless the operator sets `EXTERNAL_MAIL_PUSH_ENABLED=true`
and a provider's settings: for Gmail, `EXTERNAL_MAIL_GMAIL_PUSH_TOPIC`,
`EXTERNAL_MAIL_GMAIL_PUSH_AUDIENCE`, and `EXTERNAL_MAIL_GMAIL_PUSH_SERVICE_ACCOUNT`, all three or
startup fails; for Outlook, `EXTERNAL_MAIL_OUTLOOK_PUSH_NOTIFICATION_URL`. IMAP has no push (IMAP
IDLE is future work).

A notification is only a hint. It never carries mail, and nothing in it is trusted for mail. It
marks the connections it names due, and the regular sync reads the mail with each connection's own
grant, cursor, backfill window, and `received_not_before` floor. That sync still keeps the
connection's backoff, lease, fetch spacing (`EXTERNAL_MAIL_MIN_FETCH_SPACING`, so a throttled
backfill keeps its pace), and the per-provider budget. Polling stays the safety net.

When a notification arrives:

- The route records it on the subscription only, without waiting for any lock and without touching
  the connection. The `mail:external:sync` worker's next pass then makes the connection due, so the
  delay from a notification to the start of its sync is at most the worker's idle wait (two seconds
  by default while push is on) plus the coalescing window.
- If a sync is saving the subscription at that very moment, the route records the notification on
  the other subscriptions it names and answers `503 COMMON.SERVICE_UNAVAILABLE` with `Retry-After: 1`
  at once, rather than waiting or dropping it: Pub/Sub and Graph deliver it again a moment later.
- If a worker is fetching the connection, the connection syncs again once that fetch ends. This
  holds even when the fetch was itself answering an earlier notification.
- If the connection is backing off, the notification waits for the backoff.
- However many notifications arrive, one connection syncs at most once per
  `EXTERNAL_MAIL_PUSH_COALESCE_WINDOW` (10 seconds by default) because of them.

**Lifecycle.**

- **Subscribe and renew.** An `active` connection whose connector supports push subscribes after a
  sync that leaves no further page, and renews once half of the subscription's life has passed. Both
  run in the worker, with the grant the sync used. A mailbox [assigned from a tenant](#tenant-connections)
  uses the token the tenant credential mints for that one mailbox.
- **Long poll only while push is shown to work.** The poll slows to `EXTERNAL_MAIL_PUSH_POLL_INTERVAL`
  (15 minutes by default, 2 minutes to 1 hour), never past the next renewal or the expiry, only
  while the subscription is live and both of these hold:
  - A notification arrived, or the subscription went live, within `EXTERNAL_MAIL_PUSH_QUIET_LIMIT`
    (24 hours by default, 1 to 168 hours).
  - No poll has caught it missing mail.
- **Missed mail.** An incremental poll that reads a whole round and inserts a new copy, received
  more than five minutes after the subscription went live and more than five minutes before the
  poll, with no notification for it, marks the subscription degraded. Its renewal then becomes due,
  and the connection polls every two minutes until a change notification for it arrives again (a
  lifecycle event, `missed` included, does not clear it). Only copies the poll actually inserts
  count: a message the provider reports again (Graph delta also returns messages that were read,
  flagged, or categorized), a backfill, a restart after an expired cursor, and pages in the middle
  of a round never mark it. This catches a deleted Pub/Sub subscription, a misconfigured audience,
  or a provider that stopped delivering.
- **Failures.** A subscribe or renewal that fails backs off from two minutes to an hour. It records
  `external.auth_failed` or `external.fetch_failed` on the subscription only, and never changes the
  connection's status or stops its sync. The poll returns to two minutes once no subscription is live.
- **Stopping.** Every path stops the subscription in Zmail in the transaction that ends the
  connection, and its notifications are ignored from then on:
  - Unbinding by the member, a forced disconnect, a member's departure, and deleting the mailbox stop
    it at the provider too, and hand the grant back. Both happen only after that transaction
    committed; a departure's connections are released a few at a time, and a Gmail watch that
    another live connection of the same account shares is left running.
  - What is left to do at the provider is recorded in that same transaction, so a process that stops
    right after the commit loses nothing: the `mail:external:sync` worker performs it, retrying with
    backoff. The record outlives the purge of the connection's copies and is deleted with the
    organization. It keeps a sealed copy of the grant only for a connector whose revocation really
    revokes a provider grant (Gmail), never for a tenant mailbox, an app password, or a release that
    only stops a push subscription. The copy is never readable through the API and never logged, and
    is kept only until the release succeeds or for at most 24 hours; the mail worker's purge pass,
    which runs whether or not a connector is enabled, deletes it then. A Gmail grant that could not be
    handed back by then is not revoked by Zmail and does not lapse on its own: the member removes
    Zmail's access in their Google account to end it.
  - Deleting an organization deletes its pending releases undone. Binding an account again deletes
    the organization's pending releases of that address. Assigning or restoring a tenant mailbox
    deletes only those that stop a subscription: a member's own grant is still handed back, since the
    tenant reads with its own credential.
  - A release is also postponed, not dropped, while the member (or, for a shared connection, anyone
    in the organization) has a sign-in pending for the provider, and everything that would make a
    hand-back harmful is checked again right before the hand-back call. The release records, when
    it is made, whose connection it was, so the same rule applies after the copies were purged and
    the connection is gone: another member's sign-in never postpones the release of a member's own
    connection, and once that member's user account is deleted no sign-in postpones it.
  - A recorded release never touches an account that is in use again: binding the same account again
    in the organization deletes it; a release whose connection is active again is dropped unperformed;
    a watch is stopped only while no live connection watches the same Gmail address; and a grant is
    handed back only while no live connection, in any organization, reads the same account with a
    grant of its own, since at Google revoking one token revokes the user's whole grant. A Google
    account bound by several connections therefore keeps Zmail's access until its last binding goes.
  - A revoked tenant, a connection that needs reconnecting, and the retention purge stop it in Zmail
    only (the worker does so at most once a minute; the connection's notifications are ignored from
    the moment it stops being `active`). Their provider subscription lapses at its expiry: at most 7 days for Gmail, 70 hours for
    Outlook.
  - A subscription a sync created while the connection was being revoked is stopped again at the
    provider, unless another live connection of the same account (by Gmail's primary address)
    shares it.

**Gmail.**

- **Watch.** The connector calls `users.watch` on the account's `INBOX` with the operator's Cloud
  Pub/Sub topic. Google publishes to the topic, and a Pub/Sub push subscription posts to
  `POST /v1/external-mail-notifications/gmail` with an OIDC token.
- **Authentication.** The request counts only when all of these hold:
  - The token is RS256-signed by a key Google publishes at `EXTERNAL_MAIL_GMAIL_PUSH_JWKS_URL`. The
    key set is size- and count-bounded, cached for an hour, and refetched for an unknown key at most
    once a minute by one request while others wait.
  - Its issuer is `accounts.google.com` and its audience is `EXTERNAL_MAIL_GMAIL_PUSH_AUDIENCE`.
  - It has not expired.
  - It was issued to the verified service account `EXTERNAL_MAIL_GMAIL_PUSH_SERVICE_ACCOUNT`.

  Otherwise the answer is `401 AUTH.UNAUTHORIZED` and nothing changes.
- **Matching.** The subscription is keyed by the address Gmail's profile reports when it is created,
  which is the address notifications name, so a connection bound under an alias still matches. A
  notification makes every `active` connection of that address due (up to 1,000 per request, in one
  bounded query), in any organization, and each is synced with its own grant only. Every
  authenticated request is answered `204`.
- **Workspace tenants.** A Gmail mailbox assigned from a Workspace tenant is not pushed: its tokens
  come from the customer's own service account in the customer's Cloud project, and Gmail only
  publishes to a topic of the token's own project. Its subscription reads `unsupported`, it is
  polled every two minutes, and the connector asks again once a day without reporting a failure.

**Outlook.**

- **Subscription.** The connector creates a Microsoft Graph subscription on the mailbox's Inbox
  messages (`created`), with `EXTERNAL_MAIL_OUTLOOK_PUSH_NOTIFICATION_URL` as both the notification
  and the lifecycle URL, a fresh random `clientState`, and a 70-hour expiry. It renews with `PATCH`
  and creates a new subscription when Graph no longer knows the old one. Zmail stores only the
  SHA-256 of the `clientState`.
- **Handshake.** `POST /v1/external-mail-notifications/outlook?validationToken=...` is Graph's
  validation handshake: the token (at most 1,024 printable ASCII characters) is echoed as `text/plain`.
- **Notifications.** Any other request is a Graph notification collection. Its at most 20
  notifications are looked up together. Each counts only when its `clientState` matches the
  subscription's, compared in constant time, and only marks that subscription's connection due.
- **Lifecycle notifications.** `reauthorizationRequired` makes the renewal due now;
  `subscriptionRemoved` ends the subscription's life so the next sync creates it again; `missed`
  only marks the connection due. None of them clears a degraded mark.
- **Answers.** Every readable request is answered `202`. A body that is not JSON is
  `400 COMMON.INVALID_INPUT`.

**Both routes.**

- **No disclosure.** They need no session or API key and never say whether a subscription exists:
  a known and an unknown subscription get the same answer, which also tells the provider to stop
  retrying.
- **Limits.** Bodies are at most 64 KiB (`413 COMMON.REQUEST_TOO_LARGE`), and each request does a
  bounded amount of work. When a budget is used up, the answer is `429 COMMON.RATE_LIMITED` with
  `Retry-After`:
  - Only a request that authenticates a notification (a verified token, or a matching
    `clientState`) spends the provider's budget, `EXTERNAL_MAIL_PUSH_MAX_PER_MINUTE` a minute
    (6,000 by default). That budget is per replica, or shared across replicas with
    `RATE_LIMIT_STORE=postgres`.
  - A request that authenticates nothing (a refused token, a malformed body, or notifications that
    match no subscription) spends only its sender IP's budget of 120 a minute, and a validation
    handshake only the sender's separate budget of 30 handshakes a minute, so junk never blocks a new
    Outlook subscription. Both are kept in each replica's memory and never written to the database.
  - The sender is the address `SERVER_TRUSTED_PROXIES` resolves. In production push is refused at
    startup unless `SERVER_TRUSTED_PROXIES` names the proxies in front of the API or
    `EXTERNAL_MAIL_PUSH_DIRECT_EXPOSURE=true` states there is none, since behind an unnamed proxy every
    request would seem to come from it.
- **Push off.** While push is off, or the provider's push settings are not set, both routes answer
  `404 MAILBOX.EXTERNAL_PROVIDER_UNAVAILABLE` and read nothing.
- **Logging.** Nothing from a body is logged, only the provider and counts. Refused requests are
  counted by `zmail_external_mail_push_notifications_total` and logged at most once a minute per
  provider and reason, never once the sender is over its budget. The subscription id and secret
  never appear in a response, a log, or an audit row.

A connection's `push`, on the [connection list](#connections), is:

```json
{ "status": "active", "expires_at": "2026-11-27T08:00:00Z", "last_notified_at": "2026-11-20T08:00:04Z",
  "last_error_code": null }
```

| `status` | Meaning | Poll |
|---|---|---|
| `active` | A live subscription that is shown to work; new mail syncs within seconds | `EXTERNAL_MAIL_PUSH_POLL_INTERVAL` |
| `quiet` | Live, but no notification within `EXTERNAL_MAIL_PUSH_QUIET_LIMIT` (normal for a quiet mailbox) | two minutes |
| `degraded` | Live, but a poll found mail no notification announced; renewal is due, and an authenticated notification clears it | two minutes |
| `pending` | No live subscription yet, or it expired or was removed; the next attempt backs off | two minutes |
| `unsupported` | The provider cannot push for this connection (a Gmail mailbox assigned from a Workspace tenant) | two minutes |
| `stopped` | Ended with its connection | none |

`expires_at` is set while the subscription is live. Operators also see
`zmail_external_mail_push_notifications_total{provider,result}`,
`zmail_external_mail_push_degraded_total{provider}`, and
`zmail_external_mail_push_delay_seconds{provider}` (notification to sync start).

### Connection Directory

Owners and admins learn that members connected outside accounts, and can disconnect them, without
ever seeing the account's mail or full address. Members get `403 PERMISSION.DENIED` on both routes.

`GET /v1/external-mail-connection-directory` returns keyset pages of every connection of the
organization in any status, newest first (`cursor`, `per_page` 1 to 100, default 15; `page` is
`400 COMMON.INVALID_INPUT`). It is empty while external mail is off, and never cached:

```json
{ "id": 3, "provider": "gmail", "status": "active", "shared": false, "tenant_id": null, "account_address": null,
  "mailbox_id": null, "masked_account_address": "st***@gmail.com", "owner_user_id": 5,
  "owner_display_name": "stark", "last_synced_at": "2026-11-20T08:00:00Z",
  "created_at": "2026-11-19T08:00:00Z" }
```

The mask keeps the first two characters of the local part (one when it is that short) and the
domain. `owner_display_name` is the owner's nickname, or username without one, while they are a
member, otherwise `null`. No token, message, or count is ever returned, and for a member's own
account neither its full address nor its mailbox id. A shared connection is the organization's own
team account: it carries `"shared": true`, its full `account_address`, and its `mailbox_id`, with
`owner_user_id` and `owner_display_name` `null`. A tenant assignment carries its `tenant_id`, and,
being at an organization address, its full `account_address` and `mailbox_id` too.

`DELETE /v1/external-mail-connection-directory/:connection_id` force-disconnects any connection of
the organization with the effect of the owner's unbind and writes `connection_force_disconnected`
to the connected mailbox's history; a shared connection is unbound as in
[Shared Connected Mailboxes](#shared-connected-mailboxes) instead. A revoked connection answers `204` again and writes nothing;
another organization's id, and any id while external mail is off, is
`404 MAILBOX.EXTERNAL_CONNECTION_NOT_FOUND`. A forced disconnect that changed anything is also
written to the organization audit log (resource `external_mail_connections`, the connection id,
`provider`, and `owner_user_id`; never the address). The record is filed under the acting manager
and, as a copy with the same actor, under every other owner and admin of the organization, so each
of them finds it in `GET /v1/audit-logs?resource=external_mail_connections` and sees who
disconnected which account. The owner may bind the account again afterwards, which restores the
same mailbox like any rebind of their own; until then another member who proves control of the
account may bind it instead (see [Binding](#binding)).

### Tenant Connections

An organization **owner** authorizes a whole Google Workspace or Microsoft 365 tenant once; owners
and admins then pick mailboxes from its directory and assign each to a member (a personal connected
mailbox) or as a shared connected mailbox, without anyone signing in to the mailbox.

What this guarantees: authorizing a tenant grants no read access by itself; it creates no mailbox,
and only assigned mailboxes are synced, each with a short-lived token the tenant credential mints
for that one mailbox. Owners and admins decide who is assigned which mailbox; every assignment is
written to the organization audit log and the mailbox access history. A personal assignment cannot
be redirected to another reader while its member is in the organization (see the assignment rules
below). Every tenant route needs a session; there is none on the API key surface, so an agent never
gets tenant-level access, only the per-mailbox [agent grants](#agent-grants) of assigned mailboxes,
issued as for any mailbox. Members get `403 PERMISSION.DENIED` on every tenant route.

Tenant connect is off unless the instance sets `EXTERNAL_MAIL_TENANTS_ENABLED=true` (and enables the
`gmail` or `outlook` connector). While it is off, as while external mail is off, no connector lists
`capabilities.tenant`, authorizing answers `404 MAILBOX.EXTERNAL_PROVIDER_UNAVAILABLE`, and the
directory and assignment routes answer `404 MAILBOX.EXTERNAL_CONNECTION_NOT_FOUND`. Listing
(`GET /v1/external-mail-tenants`) and revoking (`DELETE /v1/external-mail-tenants/:tenant_id`) keep
working, so an owner can always see and revoke a stored credential. Mailboxes already assigned keep
syncing, and owners and admins still disconnect them one by one with the connection routes.

`POST /v1/external-mail-tenants` (owner only; `admin` is `403 PERMISSION.DENIED`):

| Provider | Body | Answer |
|---|---|---|
| `gmail` (Google Workspace) | `service_account_key` (the JSON key file, as an object or a string) and `admin_subject` (an administrator's address) | `201` tenant |
| `outlook` (Microsoft 365) | `tenant_id` (the Entra tenant id, a GUID), `client_id`, and `client_secret` of the organization's own application | `201` tenant |

Only a credential the organization owns is accepted. A body without one, or with `client_id` and no
`client_secret` (or the reverse), is `422 MAILBOX.EXTERNAL_AUTHORIZATION_INVALID` before any provider
is asked. There is no admin consent redirect: an admin consent to an operator's application cannot be
tied to the organization that claims it.

- **Google**: the organization creates a service account in its own Google Cloud project, authorizes
  its client id for domain-wide delegation in the Workspace Admin console with the scopes
  `https://www.googleapis.com/auth/gmail.readonly` and
  `https://www.googleapis.com/auth/admin.directory.user.readonly`, and posts its key. Zmail signs an
  RS256 assertion with the key for the administrator (both scopes) and reads one Directory API user to
  learn the customer id, which becomes `tenant_identifier`; `display_name` is the administrator's
  domain. A mailbox token is an assertion with the mailbox as the subject and `gmail.readonly` only.
  The key's own `token_uri` is ignored. The customer revokes by deleting the key or the delegation.
- **Microsoft**: the organization registers an application in its own Entra tenant with the Graph
  application permissions `Mail.Read` and `User.Read.All`, grants admin consent there, and posts the
  tenant id with the application's client id and secret. Zmail proves them with a client credentials
  token and one Graph `/users` read before anything is stored, and repeats both whenever it checks
  the tenant, so a removed permission is noticed. Tokens are cached in memory for their lifetime, never
  logged.
- **Microsoft, scope**: an application token is tenant-wide at Microsoft; Zmail only ever
  addresses the assigned mailbox (`/users/{id}`) with it. The customer should restrict the
  application to the assigned mailboxes with **RBAC for Applications in Exchange Online** (a
  management scope and an `Application Mail.Read` role assignment; changes take 30 minutes to 2 hours
  at Microsoft). When revoking in Zmail, the customer should also delete the application's secret
  in Entra.

A malformed body is `422 MAILBOX.EXTERNAL_AUTHORIZATION_INVALID`; a credential the provider refuses
is `422 MAILBOX.EXTERNAL_AUTHORIZATION_FAILED`. A tenant identifier is live (not `revoked`) in at most
one organization of the instance: another organization's is `409 MAILBOX.EXTERNAL_ACCOUNT_TAKEN`.
An organization holds at most 50 live tenants; one more is `409 MAILBOX.EXTERNAL_CONNECTION_LIMIT`
(revoked ones do not count). Authorizing a tenant this organization already holds re-authorizes it in
place (`200`): the new credential replaces the old, the tenant is `active` again, and the assignments
the lost tenant credential stopped (`last_error_code` `external.tenant_auth_failed`) resume at once;
one stopped by its own mailbox's refusal stays `needs_reconnect`. A check that ran with a credential
the owner has since replaced never stops the re-authorized tenant. The tenant, never its credential:

```json
{ "id": 4, "provider": "gmail", "tenant_identifier": "C01abc2de", "display_name": "acme.com",
  "status": "active", "last_error_code": null, "assignments": 12, "authorized_by_user_id": 1,
  "authorized_at": "2026-12-08T09:00:00Z", "revoked_at": null,
  "created_at": "2026-12-08T09:00:00Z", "updated_at": "2026-12-08T09:00:00Z" }
```

| `status` | Meaning |
|---|---|
| `active` | Mints tokens for its assigned mailboxes |
| `needs_reauthorization` | The provider refused the tenant credential (`last_error_code` `external.tenant_auth_failed`); every assignment is `needs_reconnect` until the owner authorizes the tenant again |
| `revoked` | Revoked by the owner; kept for the record without its credential |

`GET /v1/external-mail-tenants` (owners and admins) lists the organization's tenants, live first,
then id ascending, at most 50. `DELETE /v1/external-mail-tenants/:tenant_id` (owner only) revokes the
tenant and, in one transaction, every connection of it: each stops syncing at once, follows the
revoke path (triage off, agent grants end) and records `connection_revoked` with
`detail.reason` `tenant_revoked`; the tenant's credential is erased. It answers `204`, again for a
revoked tenant; another organization's id is `404 MAILBOX.EXTERNAL_CONNECTION_NOT_FOUND`.

`GET /v1/external-mail-tenants/:tenant_id/directory` (owners and admins) reads one page of the
tenant's directory from the provider (`cursor`, `per_page` 1 to 100, default 50). The cursor is
opaque and carries only the provider's page token: Zmail always builds the provider request itself,
so a cursor never names another resource. A cursor that is malformed or that the provider refuses is
`400 COMMON.INVALID_INPUT` and says nothing about the tenant. Suspended and archived Google users and
Microsoft users without a mail address are left out. A tenant that is not `active` is
`409 MAILBOX.ACCESS_CONFLICT`, and so is one whose credential the provider refuses now (it becomes
`needs_reauthorization`):

```json
{ "address": "alice@acme.com", "display_name": "Alice", "directory_id": "1067...", "assigned": true,
  "assignment": "personal", "connection_id": 31, "mailbox_id": 88, "available": false }
```

`assigned` marks a live connection of this tenant (`assignment` `personal` or `shared`);
`available` is `false` while any live connection of the organization holds the address, such as a
member's own binding.

`POST /v1/external-mail-tenants/:tenant_id/assignments` (owners and admins) takes 1 to 100 rows
(`422 MAILBOX.ACCESS_INVALID` otherwise) and answers `200` with one result per row, in order:

```json
{ "assignments": [
  { "address": "alice@acme.com", "assign": "personal", "user_id": 7 },
  { "address": "support@acme.com", "assign": "shared", "display_name": "Support",
    "members": [ { "user_id": 7, "permission": "manage" }, { "user_id": 9, "permission": "view" } ] } ] }
```

```json
{ "address": "alice@acme.com", "status": "created", "connection_id": 31, "mailbox_id": 88, "error_code": null }
```

`status` is `created`, `restored` (a revoked assignment of the same target comes back with its
copies), `unchanged` (the same live assignment already exists: rows are idempotent per address), or
`failed` with `error_code`; each row is stored in its own transaction, so a failed row is retried by
sending it again alone. Row failures:

| `error_code` | Why |
|---|---|
| `MAILBOX.ACCESS_INVALID` | Not one address; `personal` without a current member `user_id`, or with members; `shared` without valid members (the `POST /v1/mailboxes` rules); the address twice in the batch |
| `MAILBOX.EXTERNAL_ACCOUNT_MISMATCH` | Not a mailbox of the tenant's directory (an alias is not its user's address) |
| `MAILBOX.EXTERNAL_ACCOUNT_TAKEN` | A live connection of the address that is a member's own, a shared one, another tenant's, or this tenant's with another target; a revoked shared one whose mailbox exists; or an address last assigned as personal to another member who is still in the organization |
| `MAILBOX.EXTERNAL_CONNECTION_LIMIT` | The tenant holds `EXTERNAL_MAIL_MAX_TENANT_ASSIGNMENTS` (1000 by default, 1 to 10000) live assignments, or a shared row would pass `EXTERNAL_MAIL_MAX_SHARED_CONNECTIONS` |
| `MAILBOX.ACCESS_CONFLICT` | The tenant credential stopped working, or the tenant was revoked, during the batch; this and later rows are not tried |
| `MAILBOX.EXTERNAL_SERVER_UNREACHABLE` | The provider could not be asked; retry the row |

Each row holds the tenant row shared until it commits, so a revocation waits for rows in progress and
revokes what they created. Restoring a shared assignment makes its members exactly the posted set:
the others are removed (`member_removed`).

A personal assignment cannot be redirected, and **a holder only ever reads mail received during their
own tenures**:

- Zmail keeps a record of every address any connection of the organization ever read, by any kind
  of binding (a member's own grant, a team account, a tenant assignment), under every connector
  (matched by address, ignoring case, and by directory id): who holds it now, its privacy floor, which
  only ever rises, and the member exempt from the floor. It is written when the binding is made, so
  it survives disconnects, purges, deletions, departures, handovers, and tenant revocation. A member's
  own binding is a tenure like any other: assigning the address from the tenant to someone else
  afterwards starts after it.
- The first personal holder is exempt, with the normal look-back, across departures and returns, for
  as long as nobody else has held the address. Once anyone else held it (another member, a shared
  mailbox, or a handover), nobody is exempt any more.
- Every other assignment, personal or shared, and every restore of one, starts its connection at the
  later of the floor and the moment of the assignment: the connector is asked for nothing older, and
  every copy the provider received before it, or without a receive time, is dropped before it is
  stored, whatever the connector returned. A connection's start never goes down and is never
  cleared. A member assigned the address again keeps the copies their mailbox still holds from their
  own earlier tenure, but nothing received while they did not hold it is fetched.
- While that member is in the organization, the address goes to nobody else, personal or shared
  (`409 MAILBOX.EXTERNAL_ACCOUNT_TAKEN`), unless it was handed over. The same member may have it back
  at any time, with the normal look-back.
- When the member leaves, the assignment awaits handover: it cannot be disconnected, purged, or its
  mailbox deleted (`409 MAILBOX.ACCESS_CONFLICT`), the worker's retention purge skips it, and it is not reassigned
  (`409 MAILBOX.EXTERNAL_ACCOUNT_TAKEN`). The only ways on are the handover, which seals it, or the
  member's return, which finds it intact.
- A departure or a handover raises the floor to that time; the handover's seal keeps the successor
  from what came before it.
- Once a tenant assigned the address, nobody but the exempt first holder binds it with an own grant,
  personal or shared (`409 MAILBOX.EXTERNAL_ACCOUNT_TAKEN`; matched by address and by the directory id
  the provider reports): such a binding has no floor. This strict rule is pending confirmation. Before
  any tenant assignment the earlier rules hold: a member who proves control of the account may bind it
  once the previous binding is revoked.
- After the tenant was revoked and authorized again, assigning the address back to its holder (or the
  same shared mailbox) restores the existing mailbox under the new tenant, under the same rules.

A mailbox assigned from a tenant, personal or shared, is read-only whatever the permission: sending
from a connected mailbox through its own SMTP server is only for an IMAP account bound with
`smtp_host`, and a tenant-assigned mailbox never sends even if its record says otherwise.

A personal assignment creates a `personal` + `connected` mailbox owned by the member with
`created` and `connection_bound` events naming the tenant. It is private to that member exactly like
a self-bound one, never counts toward their 10 own connections, and follows the leave rule in
[Departure And Handover](#departure-and-handover). The member cannot unbind, reconnect, or purge it
(`403 PERMISSION.DENIED`; a reconnect with its `connection_id` is
`422 MAILBOX.EXTERNAL_AUTHORIZATION_INVALID`); the member and owners and admins read its sync runs,
which carry counts and codes only, never message content or headers, and owners and admins purge it
once disconnected, unless it awaits handover, and cannot self-bind an assigned address
(`409 MAILBOX.EXTERNAL_ACCOUNT_TAKEN`). A shared assignment follows
[Shared Connected Mailboxes](#shared-connected-mailboxes) and counts toward the organization's shared
limit. Owners and admins unassign one mailbox with `DELETE /v1/external-mail-connections/:id` or the
connection directory route (`connection_force_disconnected`, or `connection_revoked` with an audit
`reason` `shared_disconnect` for a shared one).

When a mailbox token is refused, Zmail checks the tenant credential: if it is refused too, the tenant
becomes `needs_reauthorization` and every `active` assignment `needs_reconnect` in one statement, so
all sync stops at once; otherwise only that assignment becomes `needs_reconnect` (for example a
Microsoft mailbox outside the application's RBAC scope, or a deleted Google user). Every
authorization, re-authorization, revocation, and created or restored assignment is written to the
organization audit log (resources `external_mail_tenants` and `external_mail_connections`,
`reason` `tenant_authorized`, `tenant_revoked`, or `tenant_assignment`, with the tenant id), and
assignment and revocation events to the mailbox access history; neither ever carries a key, secret,
token, or credential.

## Domains

With `MAILBOX_CUSTOM_DOMAINS_ENABLED=true`, an organization may use its own domains next to the
shared one. Without it every `/v1/mail-domains`, `/v1/mail-domain-previews`, and `/v1/mail-zones`
route is `404 MAILBOX.DOMAINS_DISABLED`.

- `POST /v1/mail-domains` takes `name` and an optional `capability`: `send_receive` (the default)
  receives and sends through Zmail; `send_only` only sends and leaves the domain's MX with another
  mail service, so Email Routing is never enabled for it and its mailboxes get no routing rule. Any
  other `capability` is `422 COMMON.VALIDATION_FAILED`.
- The name is trimmed, lowercased, and loses a trailing dot; one that is not a domain, or that is
  itself a public suffix, is `422 MAILBOX.DOMAIN_INVALID`. Its apex is the registrable domain below
  the public suffix: `example.com` for `mail.example.com`, `example.com.cn` for `a.example.com.cn`.
- The domain goes into the first zone that applies: a managed zone (see Managed Zones) the
  organization already has for the apex; else a Cloudflare zone, or parent zone, the operator added
  to the account the instance's `MAILBOX_CLOUDFLARE_API_TOKEN` manages; else a new managed zone
  Zmail creates for the apex. A managed zone of another organization, or an apex another Cloudflare
  account holds, is `409 MAILBOX.DOMAIN_TAKEN`. A new managed zone beyond the organization's limit
  (`MAILBOX_MAX_ZONES_PER_ORGANIZATION`, 3 by default, at most 20) is
  `422 MAILBOX.ZONE_LIMIT_REACHED`.
- A domain belongs to one organization. A name another organization connected, or the shared
  domain, is `409 MAILBOX.DOMAIN_TAKEN`. An organization connects at most 10 domains; one more is
  `422 MAILBOX.DOMAIN_LIMIT_REACHED`.
- A domain in a managed zone carries the zone in `zone`; `zone` is `null` in an operator zone. While
  the zone is `pending`, the domain stays `pending` without records and no setup runs:
  `POST /v1/mail-domains/:domain_id/setups` is `409 MAILBOX.ZONE_PENDING`, and
  `POST /v1/mail-domains/:domain_id/checks` asks Cloudflare to look for the zone's name servers now
  and answers with the domain. Cloudflare limits how often it looks; a refused request sets
  `zone_check_limited` to true on that answer, and the domain should be checked again later. The
  setup of every pending domain in a zone starts when the zone becomes active.
- Connecting into an operator zone or an active managed zone runs the setup: Zmail enables Email
  Routing for the domain unless it is `send_only` (Cloudflare adds its MX and SPF records), enables
  Email Sending for it, adds each sending record Cloudflare lists that the zone lacks, and adds
  `_dmarc.<domain>` with `v=DMARC1; p=none` when the domain has no DMARC record. It never edits or
  deletes a record and never changes a zone's catch-all rule.
- The setup runs in the background, because it makes several provider calls that can outlast the
  request: connecting answers at once with the domain `pending` and `setting_up` true, and clients
  read the domain again until `setting_up` is false. A step the provider refuses leaves the domain
  `pending` with `last_error` (the provider's words) and `last_error_code` (below), and
  `POST /v1/mail-domains/:domain_id/setups` starts every step again
  safely. One setup runs per domain at a time; a check while it runs returns the domain unchanged,
  and a setup or receiving request while it runs makes it run once more afterwards, from the domain
  as stored then. A failed zone lookup while connecting is `502 MAILBOX.DOMAIN_PROVIDER_FAILED`.
- `last_error_code` classifies the first failure of the last setup or check, so a client can say what
  went wrong and what to do; both fields are absent once a check passes. Clients treat a code they
  do not know like `domain.setup_failed`.

  | Code | Meaning |
  |---|---|
  | `domain.permission_denied` | The Cloudflare token may not make the call (HTTP 401/403 or an authentication error code); reconnect Cloudflare with DNS and Email Routing edit permissions |
  | `domain.provider_unavailable` | Cloudflare was unavailable, rate limited, or timed out; retry the setup later |
  | `domain.routing_refused` | Cloudflare refused to enable Email Routing for the domain, usually because another mail service's MX records are in the zone |
  | `domain.sending_refused` | Cloudflare refused to enable Email Sending for the domain or list its sending records |
  | `domain.dns_record_refused` | Cloudflare refused to add a required DNS record, usually because a conflicting record exists |
  | `domain.routing_rule_refused` | Cloudflare refused a mailbox's Email Routing rule |
  | `domain.provider_refused` | Cloudflare refused another call |
  | `domain.zone_not_found` | The domain's Cloudflare zone is no longer reachable with the connection |
  | `domain.setup_failed` | Any other failure |
- `POST /v1/mail-domains/:domain_id/checks` reads the zone and reports each required record with
  `purpose` (`receiving`, `sending`, `dmarc`), `type`, `name`, `content`, optional `priority`, and
  `status`: `ok`, `missing`, or `conflict` with the conflicting value in `found`. A `send_only`
  domain has no receiving records. Receiving MX records match by host, since Cloudflare chooses
  their priorities, and an MX of another mail service is a `conflict`. An SPF record matches when it
  includes what is required, and any DMARC record satisfies DMARC. A check never writes DNS.
- The domain is `verified` when every record is `ok`, and `pending` otherwise. A later check can
  return it to `pending`; its mailboxes keep working, but new ones wait until it verifies again.
  `verified_at` keeps the first verification time.
- Cloudflare can take a few minutes after a domain verifies before it accepts mail from it; a send
  in that window fails with the provider's code and can be sent again.
- Creating a mailbox on a verified domain also creates an Email Routing rule that delivers that one
  address to the Email Worker named by `MAILBOX_CLOUDFLARE_EMAIL_WORKER`. A rule Cloudflare refuses
  is `502 MAILBOX.DOMAIN_PROVIDER_FAILED`, and no mailbox is created.
- `POST /v1/mail-domains/:domain_id/receiving` turns a `send_only` domain into `send_receive` and
  starts its setup again, which enables Email Routing and gives every live mailbox on the domain
  that has no routing rule its rule; running the setup again later retries a rule Cloudflare
  refused. The answer is the domain with `setting_up` true. A domain that already receives is
  answered `200` unchanged, and one whose managed zone is pending is `409 MAILBOX.ZONE_PENDING`.
  Email Routing replaces the domain's MX, so mail stops reaching the other mail service; while that
  service's MX stays on the name, the receiving MX rows are `conflict` and the domain stays `pending`.
- `POST /v1/mail-domain-previews` with `{ "name": "mail.example.com" }` changes nothing. It answers
  the normalized `name`, its `apex`, the name's public MX hosts in `existing_mx` (lowercase, without
  the trailing dot, at most 20), and `suggested_capability`: `send_only` when any of those hosts is
  not Cloudflare Email Routing's (`*.mx.cloudflare.net`), else `send_receive`. A failed lookup,
  including a name without MX records, answers `existing_mx: []` and `send_receive`. An unusable
  name is `422 MAILBOX.DOMAIN_INVALID`, and the shared domain `409 MAILBOX.DOMAIN_TAKEN`.
- Removing a domain that a mailbox still uses, or that a transactional template sends from, is
  `409 MAILBOX.DOMAIN_IN_USE`. Removing forgets the
  domain and leaves its DNS and Cloudflare settings as they are, including a managed zone, which is
  removed on its own (see Managed Zones).
- Any owner or admin may connect any domain in a zone the operator added to the account. The token
  covers every zone of the account, so it can reach the managed zones Zmail creates; an operator
  serving several companies keeps zones they must not use in a separate Cloudflare account.

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "id": 3,
    "name": "example.com",
    "zone_name": "example.com",
    "capability": "send_receive",
    "zone": null,
    "status": "verified",
    "records": [
      { "purpose": "receiving", "type": "MX", "name": "example.com", "content": "route1.mx.cloudflare.net", "status": "ok" },
      { "purpose": "dmarc", "type": "TXT", "name": "_dmarc.example.com", "content": "v=DMARC1; p=none", "status": "ok" }
    ],
    "setting_up": false,
    "zone_check_limited": false,
    "checked_at": "2026-11-17T10:00:00Z",
    "verified_at": "2026-11-17T10:00:00Z",
    "created_at": "2026-11-17T09:59:58Z"
  }
}
```

### Managed Zones

A managed zone is a Cloudflare zone Zmail created for an organization's apex when no zone in the
account held it. Cloudflare serves it only after the organization changes the apex's name servers
at its registrar to the two in `name_servers`.

- A zone is `pending` until Cloudflare sees the delegation, and `active` from then on; an active zone
  never returns to `pending`. Zmail looks at pending zones periodically, and a domain check looks at
  its zone at once. A zone still pending 14 days after it was created is removed together with its
  domains: nobody ever delegated to it.
- When Zmail created the zone, Cloudflare imported the records it could find for the apex, and
  `original_name_servers` holds the name servers the apex had then, so the organization can return
  to them.
- `GET /v1/mail-zones` lists the organization's zones by name; `GET /v1/mail-zones/:zone_id` reads
  one. A zone of another organization is `404 MAILBOX.ZONE_NOT_FOUND`.
- `DELETE /v1/mail-zones/:zone_id` removes the zone from Zmail and from the Cloudflare account. A zone
  any domain still lives in is `409 MAILBOX.ZONE_IN_USE`. Zmail then reads the apex's public name
  servers, for a pending zone as well as an active one: while any is Cloudflare's
  (`*.ns.cloudflare.com`), or when they cannot be read, the answer is
  `409 MAILBOX.ZONE_STILL_DELEGATED`, since removing the zone would take down DNS the registrar
  points at, and a registrar may delegate to a zone before Cloudflare activates it; the organization
  first changes the name servers back, such as to `original_name_servers`. Only for a pending zone
  does a name that does not exist (`NXDOMAIN`) count as not delegated. A Cloudflare refusal is
  `502 MAILBOX.DOMAIN_PROVIDER_FAILED` and keeps the zone; a zone Cloudflare no longer has is
  removed.

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "id": 4,
    "name": "example.org",
    "status": "pending",
    "name_servers": ["ada.ns.cloudflare.com", "bob.ns.cloudflare.com"],
    "original_name_servers": ["dns1.registrar.example", "dns2.registrar.example"],
    "activated_at": null,
    "last_checked_at": "2026-11-17T10:10:00Z",
    "created_at": "2026-11-17T10:00:00Z"
  }
}
```

### DNS Records

An organization edits the records of its managed zones, `pending` or `active`, in Zmail. Zmail reads
and writes them at Cloudflare on every request and keeps no copy. Members may list them; owners and
admins may create, change, and delete them. A zone of another organization is
`404 MAILBOX.ZONE_NOT_FOUND`, and a record the zone does not have is
`404 MAILBOX.DNS_RECORD_NOT_FOUND`.

- `GET /v1/mail-zones/:zone_id/dns-records` lists every record by name, then type: `id` (Cloudflare's
  record ID), `type`, fully qualified `name`, `content`, `ttl` (`1` is automatic), `priority` for MX
  only, `proxied`, `locked`, and `locked_reason` when `locked` is true. A zone holds at most 3500
  records, Cloudflare's quota; a longer list is cut at 3500.
- A record is locked when Zmail mail relies on it. For each mail domain `d` in the zone:
  `mail_receiving` is an MX record at `d` pointing at Cloudflare Email Routing
  (`*.mx.cloudflare.net`); `mail_spf` a TXT record at `d` starting `v=spf1` when `d` is
  `send_receive`; `mail_dmarc` any record at `_dmarc.d`; `mail_dkim` any record under
  `._domainkey.d`; and `mail_sending`, at the name of any sending record of `d`, every TXT record
  starting `v=spf1` and every MX record, plus any record matching a sending record by type, name, and
  content. A locked record cannot be changed or deleted, and no record may be created or changed into
  one that would be locked: both are `409 MAILBOX.DNS_RECORD_LOCKED`. Neither may an MX record be
  created at, or changed into an MX at, a `send_receive` domain, where Email Routing owns the MX. To
  change a locked record, first remove the mail domain.
- Everything else is the organization's: an MX of another mail service at `d`, such as one the zone
  imported, can be deleted so `d` verifies, and a `send_only` domain's own MX and SPF records can be
  created, changed, and deleted.
- `POST /v1/mail-zones/:zone_id/dns-records` takes `type`, `name`, `content`, and optional `ttl`,
  `priority`, and `proxied`, and answers `201` with the record. A `PATCH` of
  `/v1/mail-zones/:zone_id/dns-records/:record_id` takes any of those fields; an omitted field keeps
  its current value, and the merged record is validated and written whole. A `DELETE` of it answers
  `204`; only records of the five types below can be changed or deleted, and another type is
  `422 MAILBOX.DNS_RECORD_INVALID` on `type`.
- Zmail validates before calling Cloudflare. A failure is `422 MAILBOX.DNS_RECORD_INVALID` with one
  field error under the request field (`type`, `name`, `content`, `ttl`, `priority`, or `proxied`):
  `type` is one of `A`, `AAAA`, `CNAME`, `TXT`, `MX`; `name` is `@` (the zone apex), a single label
  (taken relative to the zone), or a name equal to or ending in `.` and the zone name; `A` content
  is an IPv4 address and `AAAA` content an IPv6 address; `TXT` content is at most 2048 characters;
  a `CNAME` is never at the apex and never shares its name with another record, nor another record
  with a `CNAME`; `MX` needs a `priority` from 0 to 65535; `proxied` is allowed only on `A`, `AAAA`,
  and `CNAME`; `ttl` is `1` (automatic, the default when omitted on create) or 60 to 86400, so `0` is invalid. `priority` is ignored on other
  types.
- A write Cloudflare refuses is `422 MAILBOX.DNS_RECORD_REJECTED`, with Cloudflare's reason in
  `error`. A failed read is `502 MAILBOX.DOMAIN_PROVIDER_FAILED`.
- Each write is recorded in the audit log as `create`, `update`, or `delete` of `mail_dns_records`,
  targeting the Cloudflare record ID, with the zone, type, and name; record content is never
  audited.

```json
{
  "code": 0,
  "message": "success",
  "data": [
    { "id": "8f2c6a0e4b1d4e6f9a3c2b1d0e9f8a7b", "type": "MX", "name": "example.org", "content": "route1.mx.cloudflare.net", "ttl": 1, "priority": 12, "proxied": false, "locked": true, "locked_reason": "mail_receiving" },
    { "id": "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d", "type": "A", "name": "www.example.org", "content": "192.0.2.1", "ttl": 1, "proxied": true, "locked": false }
  ]
}
```

## Messages

Listing returns summaries without bodies, newest first, except the trash, which is ordered by when
messages were trashed. Without `placement` it lists every
placement except the trash, so a client that knows nothing of placements sees what it saw before
they existed. See [Listing And Search](#listing-and-search) for the filters.

| Field | Meaning |
|---|---|
| `direction` | `inbound` or `outbound` |
| `status` | `received` for inbound. Outbound is `queued` while it waits for the send limit or while the provider call is in flight, then `sent` or `failed`. |
| `queued_reason` | `send_limit` while a queued message waits for room in the organization's send limit; otherwise `null`. |
| `from` | `{ "address", "name" }` |
| `to`, `cc` | Lowercase addresses |
| `read` | Whether the caller has read the message; see [Read State](#read-state). Outbound messages are always read. |
| `failure_code` | Stable provider or transport reason when `status` is `failed`, otherwise empty |
| `last_event` | Summary of the delivery events of an outbound message; see [Delivery Events](#delivery-events). Empty when none are recorded. |
| `attachment_count` | How many files the message has |
| `source` | `zmail` for mail Zmail received or sent; `gmail` or `outlook` for a read-only copy synced into a connected mailbox. Also on log entries and unified inbox rows. |
| `template_id` | The template the message was rendered from; `null` when none was used |
| `placement` | `default`, `archive`, `folder`, or `trash`; see [Placements](#placements). Shared by everyone. |
| `folder_id` | The folder that holds the message when `placement` is `folder`, otherwise `null` |
| `label_ids` | The ids of the mailbox labels on the message, ascending. Shared by everyone. |
| `starred` | Whether the caller starred the message; per person, see [Read State](#read-state). Always `false` on API-key routes and template test sends. |
| `thread_id` | The thread of the message in this mailbox; see [Threads](#threads) |
| `trashed_at` | When the message moved to the trash, `null` outside it |
| `thread` | Only with `group=thread`: `{ "message_count", "message_ids", "unread", "starred", "label_ids" }` for the thread in the listed view |

The detail response adds `text_body`, `html_body`, `message_id`, `in_reply_to`, `events` (the
delivery history of an outbound message), and `attachments` (see [Attachments](#attachments)). `html_body` is
untrusted sender content: a client must render it only inside a sandbox that runs no script and
loads nothing with the user's credentials.

### Placements

Every message is in exactly one placement:

| `placement` | Meaning | Shown as |
|---|---|---|
| `default` | Where a message starts | Inbox for inbound messages, Sent for outbound ones |
| `archive` | Archived | Archive |
| `folder` | In the folder `folder_id` | That folder |
| `trash` | Moved to the trash at `trashed_at` | Trash |

- New messages, inbound and outbound, start in `default`.
- Moving a message out of the trash clears `trashed_at`; restoring it is moving it to `default`.
- A message in the trash is deleted for good 30 days after `trashed_at`, with its files and events,
  by a worker. Until then it can be restored.
- The placement is shared: moving a message moves it for everyone with access.
- Sealed messages (see [Sealed History](#sealed-history)) keep their placement and stay hidden in
  every placement.

`DELETE /v1/mailboxes/:mailbox_id/messages/:message_id` answers `204` in both of its cases:

| The message is | Effect | Needs |
|---|---|---|
| Outside the trash | Moves to the trash; `trashed_at` is set | `send` |
| In the trash | Deleted for good, with its files and events | `organize` |

A message that waits for the send limit (`queued` with `queued_reason` `send_limit`) is never sent
once it is in the trash, exactly as deleting it used to stop it: it becomes `failed` with
`failure_code` `send.canceled`, and restoring it does not send it. A message whose provider call is
already in flight is recorded with its real outcome wherever it is.

`POST /v1/mailboxes/:mailbox_id/trash-purges` empties the trash: every message of the mailbox in the
trash is deleted for good, with its files and events. It needs `organize` and answers:

```json
{ "code": 0, "message": "success", "data": { "deleted": 12 } }
```

### Folders And Labels

Folders and labels belong to the mailbox, not to a person: everyone with access sees the same ones.
They are flat; a folder holds no folders.

- A name is trimmed and then 1 to 64 characters. Names are unique in the mailbox ignoring case,
  separately for folders and labels; a taken name is `409 MAILBOX.FOLDER_NAME_TAKEN` or
  `409 MAILBOX.LABEL_NAME_TAKEN`.
- A mailbox has at most 100 folders and 100 labels; one more is `422 MAILBOX.FOLDER_LIMIT_REACHED`
  or `422 MAILBOX.LABEL_LIMIT_REACHED`.
- A label has a `color` from the fixed palette `gray`, `red`, `orange`, `yellow`, `green`, `teal`,
  `blue`, `purple`, `pink`.
- A folder or label of another mailbox, or one that does not exist, is
  `404 MAILBOX.FOLDER_NOT_FOUND` or `404 MAILBOX.LABEL_NOT_FOUND` wherever it is named.
- Listing needs `view` and returns every folder or label by name, ignoring case. Creating,
  renaming, recoloring, and deleting need `organize`.
- Deleting a folder moves its messages to `default`. Deleting a label removes it from every message.

```json
{ "id": 4, "name": "Invoices", "created_at": "2026-11-19T10:00:00Z" }
```

```json
{ "id": 9, "name": "Urgent", "color": "red", "created_at": "2026-11-19T10:00:00Z" }
```

`POST .../mail-folders` and `PUT .../mail-folders/:folder_id` take `{ "name" }`.
`POST .../mail-labels` and `PUT .../mail-labels/:label_id` take `{ "name", "color" }`; `PUT`
replaces both.

### Updating Messages

`PUT /v1/mailboxes/:mailbox_id/messages/:message_id` takes at least one of:

| Field | Meaning | Needs |
|---|---|---|
| `read` | The caller's read state | `view` |
| `starred` | The caller's star | `view` |
| `placement` | The new placement | `send` |
| `folder_id` | The folder, with `placement` `folder` | `send` |
| `label_ids` | The complete set of the message's labels, replacing the current one; at most 100 | `send` |

It answers `200` with the message. `POST /v1/mailboxes/:mailbox_id/message-updates` applies one
change to several messages:

```json
{ "message_ids": [41, 42, 57], "placement": "archive", "add_label_ids": [9] }
```

- `message_ids` holds 1 to 100 different ids, and at least one change is required: `read`,
  `starred`, `placement`, `folder_id`, `add_label_ids`, or `remove_label_ids`, with the same
  permissions as above. A label may not be both added and removed.
- The change applies to every named message in one transaction. When any named message is not a
  visible message of the mailbox (`404 MAILBOX.MESSAGE_NOT_FOUND`), or the folder or a label is not
  found, nothing changes.
- It answers `{ "updated": 3 }`, the number of named messages.
- A client that acts on a thread row sends that row's `thread.message_ids`; see
  [Listing And Search](#listing-and-search).

On both routes, `placement` `folder` needs `folder_id`, and `folder_id` is allowed only with it;
`placement` `trash` sets `trashed_at` like `DELETE`, and a message already in the trash keeps its
`trashed_at`. Breaking these rules is `422 COMMON.VALIDATION_FAILED` with field errors under these
keys:

| Rule | Key in `errors` |
|---|---|
| No change field at all | `change` |
| A label both added and removed | `add_label_ids` |
| `folder_id` missing with `placement` `folder`, or present without it | `folder_id` |

Outbound messages are always read; marking them changes nothing.

Every message route of a mailbox answers in this order, and the first failing check decides:

1. The mailbox exists for the caller, otherwise `404 MAILBOX.NOT_FOUND`.
2. The caller has the permission the request needs, otherwise `403 PERMISSION.DENIED`. A caller who
   may `view` but asks for a shared change is refused here, and nothing changes.
3. Every message, folder, and label the request names exists in the mailbox, otherwise
   `404 MAILBOX.MESSAGE_NOT_FOUND`, `404 MAILBOX.FOLDER_NOT_FOUND`, or `404 MAILBOX.LABEL_NOT_FOUND`.
4. The body is valid, otherwise `422 COMMON.VALIDATION_FAILED`.

A malformed path, query, or JSON body is `400 COMMON.INVALID_INPUT` before any of these.

`DELETE .../messages/:message_id` is the exception, because the permission it needs depends on
where the message is:

1. The mailbox exists for the caller, otherwise `404 MAILBOX.NOT_FOUND`.
2. The caller has `send`, otherwise `403 PERMISSION.DENIED`.
3. The message exists in the mailbox, otherwise `404 MAILBOX.MESSAGE_NOT_FOUND`.
4. When the message is in the trash, the caller has `organize`, otherwise `403 PERMISSION.DENIED`.

### Threads

A thread is a conversation within one mailbox, linked by RFC 5322 `Message-ID`, `In-Reply-To`, and
`References`. Threads are never merged by subject.

- Every outbound message gets a `Message-ID` that Zmail generates on the mailbox's domain when the
  message is stored, before it is sent; a message that waits for the send limit, or a repeat with
  the same `Idempotency-Key`, keeps the same one. Inbound messages keep the sender's `Message-ID`.
- A reply (`in_reply_to`) is sent with `In-Reply-To` and with `References` set to the replied
  message's `References` followed by its `Message-ID`.
- When a message is stored, Zmail looks in the same mailbox for a message whose `Message-ID` is the
  new message's `In-Reply-To` or is in its `References`. If it finds one, the new message joins that
  message's thread; otherwise it starts a thread, and `thread_id` is its own id.
- Messages sent before this existed have no Zmail `Message-ID`, so replies to them start a new
  thread. Messages stored before threads existed were grouped once by `in_reply_to`.
- `thread_id` is set on every stored message, whichever route stored it: inbound delivery, a send
  from a mailbox, a message waiting for the send limit, an API-key send, a broadcast message, and a
  template test send.

`GET /v1/mailboxes/:mailbox_id/threads/:thread_id` needs `view` and lists the thread's messages in
every placement except the trash, inbound and outbound, oldest first, with their bodies, as the
message detail. It returns at most the latest 100. Sealed messages are left out. A thread with no
such message is `404 MAILBOX.MESSAGE_NOT_FOUND`. So a thread opened from the Inbox also shows the
replies in Sent.

#### Assignment

A conversation of a shared Zmail mailbox can be assigned to one member and marked `open` or
`closed`, so two members do not answer the same mail. Personal and connected mailboxes do not
assign threads: their routes are `409 MAILBOX.ASSIGNMENT_UNAVAILABLE`.

| Action | Route | Needs | Body |
|---|---|---|---|
| Read | `GET /v1/mailboxes/:mailbox_id/threads/:thread_id/assignment` | `view` | none |
| Replace | `PUT /v1/mailboxes/:mailbox_id/threads/:thread_id/assignment` | `send` | `{ assignee_user_id, status }` |

Both return `{ thread_id, assignee_user_id, status, updated_by_user_id, updated_at,
assignable_user_ids }`.

- A thread nobody assigned is `open`, `assignee_user_id` and `updated_at` are `null`.
- `assignable_user_ids` lists, ascending, the mailbox's members with `send` or `manage`; the
  assignee must be one of them or the thread's current assignee (so a thread kept on a member who
  lost `send` can still change status), else `422 COMMON.VALIDATION_FAILED` on
  `assignee_user_id`. `assignee_user_id` is required: an explicit `null` unassigns the thread and
  an omitted field is `422`. `status` is `open` or `closed`.
- The thread must have a message the caller sees outside the trash, else
  `404 MAILBOX.MESSAGE_NOT_FOUND`.
- When the assignee or the last editor is deleted, that field becomes `null`; removing a member
  from the mailbox keeps the assignment until someone changes it.

### Thread Notes

Members of a shared Zmail mailbox can leave internal notes on a conversation. Notes are never sent
to the correspondent. Personal and connected mailboxes have no notes:
`409 MAILBOX.ASSIGNMENT_UNAVAILABLE`.

| Action | Route | Needs | Body |
|---|---|---|---|
| List | `GET /v1/mailboxes/:mailbox_id/threads/:thread_id/notes` | `view` | none |
| Add | `POST /v1/mailboxes/:mailbox_id/threads/:thread_id/notes` | `send` | `{ body }` |
| Delete own | `DELETE /v1/mailboxes/:mailbox_id/threads/:thread_id/notes/:note_id` | `view` | none |

A note is `{ id, thread_id, author_user_id, body, created_at }`; the list is
`{ items: [note...] }`, oldest first.

- `body` is trimmed and `\r\n` becomes `\n`; it must then be 1 to 2000 characters without
  control characters other than line breaks and tabs, else `422` on `body`.
- A thread keeps at most 200 notes; one more is `409 MAILBOX.NOTE_LIMIT_REACHED`.
- Only the author deletes a note; another member's, or a missing one, is
  `404 MAILBOX.NOTE_NOT_FOUND`. Deleting the author's account keeps the note with
  `author_user_id: null`.
- The thread must have a message the caller sees outside the trash, else
  `404 MAILBOX.MESSAGE_NOT_FOUND`.

### Listing And Search

`GET /v1/mailboxes/:mailbox_id/messages` takes these filters, combined with AND:

| Query | Meaning |
|---|---|
| `direction` | `inbound` or `outbound`. With `placement=default`, `inbound` is the Inbox and `outbound` is Sent. |
| `placement` | One placement. Without it, every placement except `trash`. |
| `folder_id` | The folder; required with `placement=folder` and allowed only with it |
| `label_id` | Messages with this label |
| `starred` | `true` for messages the caller starred, `false` for the others |
| `unread` | `true` for inbound messages the caller has not read (`read` is `false`; outbound messages are always read, so never match), `false` for the others (`read` is `true`). Read state is the caller's own: another member reading a message does not change it. |
| `has_attachments` | `true` for messages with files, `false` for the others |
| `q` | Search; at most 200 characters as sent, then trimmed and not empty, without control characters |
| `group` | `thread` lists one row per thread |
| `page`, `per_page` | Pagination |

An unknown value (including a non-boolean `starred`, `unread`, or `has_attachments`), a filter
given more than once, `folder_id` without `placement=folder`, `placement=folder` without
`folder_id`, `q` with `placement=trash`, a `q` longer than 200 characters as sent, a `q` that is
empty after trimming or contains a control character, or `group` outside the rules below is
`400 COMMON.INVALID_INPUT`. An unknown folder
or label is `404 MAILBOX.FOLDER_NOT_FOUND` or `404 MAILBOX.LABEL_NOT_FOUND`.

The trash (`placement=trash`) is ordered by `trashed_at`, newest first, then by id, highest first.
Every other list is newest first by id.

`group=thread` is for placement views only: it needs `placement` `default`, `archive`, or `folder`,
and cannot be combined with `label_id`, `starred`, `unread`, `has_attachments`, or `q`. With
`placement=default` it also needs `direction`, because Inbox and Sent are separate views:
`placement=default&direction=inbound` is the Inbox and `placement=default&direction=outbound` is
Sent. Archive and folder views may group with or without `direction`. The view is the placement
(with its folder and `direction`). Each row is the newest message of one thread in the view and
carries `thread`:

- `message_count` is how many of the thread's messages are in the view.
- `message_ids` are the ids of the thread's messages in the view, ascending, at most the 100
  newest.
- `unread` and `starred` say whether the caller has not read or has starred at least one of them.
- `label_ids` is every label on at least one of them.

Rows are ordered by their newest message, newest first, and `meta` counts threads. Acting on a row
sends its `message_ids` to `POST .../message-updates`. When `message_count` is more than 100, the
action covers only the listed ids, and after a refresh the row stays in the view with the rest. The
starred, label, unread, and attachment filters, search, and the trash list single messages.

`q` searches case-insensitively for the text anywhere in the subject, the sender address and name,
the `to` and `cc` addresses, and the text body; `%` and `_` match literally. It searches every
placement except the trash, and the filters above narrow it. Results are single messages, newest
first. A search the database cannot finish within 2 seconds, which can happen for one or two
characters in a large mailbox, is `422 MAILBOX.SEARCH_TOO_BROAD`; clients ask for a longer text.

### Counts

`GET /v1/mailboxes/:mailbox_id/message-counts` needs `view` and answers the caller's own counts for
a mailbox's navigation:

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "placements": { "default_inbound_unread": 3, "archive_unread": 0 },
    "folders": [{ "folder_id": 4, "unread": 1 }],
    "starred": 7
  }
}
```

- `default_inbound_unread` counts inbound messages in `default` the caller has not read; it equals
  the mailbox's `unread_count`. `archive_unread` and each folder's `unread` count inbound messages
  there the caller has not read.
- `folders` has one entry per folder of the mailbox, including folders with nothing unread.
- `starred` counts messages outside the trash the caller starred, read or not.
- Sealed messages and the trash are never counted.

### Send

```json
{
  "to": ["bob@example.com"],
  "cc": [],
  "subject": "Hello",
  "text": "Plain body",
  "html": "<p>HTML body</p>",
  "in_reply_to": "<previous-id@example.com>",
  "attachments": [
    { "filename": "invoice.pdf", "content_type": "application/pdf", "content": "JVBERi0xLjQK..." }
  ]
}
```

- `to` has 1 to 50 bare addresses; `to` and `cc` together have at most 50 after duplicates are
  removed. Addresses are lowercased. Display-name forms are rejected.
- `subject` is required, at most 998 characters, with no line breaks. Characters are Unicode code
  points counted after trimming and before encoding, so a CJK character and an ASCII letter count one each. RFC 5322's
  998 is a limit on the octets of a header line, and a non-ASCII subject is sent RFC 2047 encoded
  and folded over several lines, so it never applies to the subject's text. A template's subject
  and a rendered one follow the same rule, and a received or synced subject is cut at 998
  characters, never inside one.
- At least one of `text` and `html` is required; each is at most 256 KiB.
- `in_reply_to` is an optional Message-ID. It is sent as the `In-Reply-To` header and ends the
  `References` header; see [Threads](#threads).
- `attachments` is optional; see [Attachments](#attachments) for its limits.
- `template` is optional: `{ "id", "variables" }` sends a stored template in place of `subject`,
  `text`, and `html`, which must then be absent. See [Templates](#templates).
- Any of these violations is `422 MAILBOX.MESSAGE_INVALID`.
- The sender is always the mailbox address and display name; a request cannot set `from`.

The API stores the message as `queued`, calls the provider, and records the real outcome before it
answers:

| Outcome | Response | Stored status |
|---|---|---|
| Provider accepted | `201` with the message | `sent` |
| Provider rejected or was unreachable | `502 MAILBOX.DELIVERY_FAILED` | `failed`, with `failure_code` |
| No provider configured | `503 MAILBOX.SENDER_NOT_CONFIGURED` | `failed` |
| Organization's rolling 24-hour personal lane limit reached | `202` with the message | `queued`, `queued_reason` `send_limit` |
| Limit reached and as many messages already waiting as the limit | `429 MAILBOX.SEND_LIMIT_REACHED` | nothing stored |
| At least 20 personal-lane attempts in the rolling 24 hours, and 5% or more of them bounced or 0.3% or more drew a spam complaint | `403 MAILBOX.SENDING_PAUSED` | nothing stored |

The sending pause covers the personal lane only (broadcasts have their own health check) and lifts by
itself once enough of those attempts leave the 24-hour window. While it holds, messages already
waiting for the send limit stay queued.

A send is never reported as successful before the provider accepts it.

The send limit is split into three lanes. Each lane is counted separately per organization over a
rolling 24 hours by the instants provider calls started, failed attempts count toward it, and a
full lane never consumes another lane's room. Every message belongs to exactly one lane, fixed when
it is stored:

When `MAILBOX_NEW_ORGANIZATION_DAILY_SEND_LIMIT` is set, an organization created less than
`MAILBOX_NEW_ORGANIZATION_PERIOD` ago gets at most that many attempts in each lane, and at most that
many personal messages may wait. `GET /v1/mailbox-domains` keeps reporting the instance limits;
`GET /v1/mail-send-lanes` and the `daily_send_limit` of the mailbox directory report the limit that
applies to the organization now.

| Lane | Counts | Limit (`GET /v1/mailbox-domains`) | When full |
|---|---|---|---|
| Personal | Console sends, replies, `POST /v1/outbound-mail-messages`, and template test sends | `daily_send_limit` | The message waits (below) |
| Broadcast | Each broadcast recipient | `daily_broadcast_limit` | The broadcast pauses (see [Broadcasts](#broadcasts)) |
| Transactional | Live, non-simulated transactional messages | `daily_transactional_limit` | The message is queued |

A `202` message waits; nothing about the limit's usage is in the answer, and the Web console tells
everyone the same thing: the limit is full and the message is queued. The `mail:broadcast:work`
worker sends waiting messages oldest first, per organization, as the rolling window gains room,
and records each outcome exactly like a direct send (`sent`, or `failed` with its `failure_code`,
and the same webhooks). No webhook is published when a message starts waiting. A waiting message
moved to the trash is never sent (see [Placements](#placements)); deleting its mailbox deletes it too. One
that a handover seals is not sent: it becomes `failed` with `failure_code` `send.canceled`. Broadcasts
never wait this way; they pause as described in [Broadcasts](#broadcasts). This applies to both
send routes and to template test sends. The limit is a soft bound: concurrent requests may exceed it by the number of
requests in flight. There is no automatic retry. A client that retries a `502` without an
`Idempotency-Key` may send a duplicate when the first attempt reached the provider but the answer
was lost (`failure_code` `transport.unreachable`); see [Idempotency](#idempotency).

#### Lane Usage

`GET /v1/mail-send-lanes` shows owners and admins how full each lane is; a member gets
`403 PERMISSION.DENIED` and reads only the limits from `GET /v1/mailbox-domains`. It is computed on
every request and never cached:

```json
{
  "lanes": [
    { "lane": "transactional", "limit": 200, "used": 37, "waiting": 0 },
    { "lane": "broadcast", "limit": 200, "used": 200, "waiting": 412 },
    { "lane": "personal", "limit": 200, "used": 12, "waiting": 0 }
  ],
  "window_hours": 24,
  "computed_at": "2026-11-23T08:00:00Z"
}
```

`lanes` always has the three lanes in this order. `used` counts the lane's provider calls that
started in the 24 hours before `computed_at`, failed attempts and those of messages deleted since
included, exactly as the limit counts them, and may exceed `limit` by the requests that were in flight. `waiting` is the messages waiting
for room in the transactional or personal lane, and for the broadcast lane the pending recipients
of broadcasts paused because the lane was full.

### Attachments

A message has at most 10 attachments, and together they are at most 4 MiB after decoding.

To send files, each entry of `attachments` has:

| Field | Rule |
|---|---|
| `filename` | Required. Only the last path segment is kept and control characters are removed; a name that is empty afterwards is rejected. At most 255 bytes are stored. |
| `content_type` | Optional media type. Anything that is not a plain `type/subtype` becomes `application/octet-stream`. |
| `content` | Required, standard base64, not empty. |

More than 10 files, more than 4 MiB in total, or an unusable entry is `422 MAILBOX.MESSAGE_INVALID`
and nothing is stored or sent. The request body is also subject to the API's general body limit
(10 MiB by default). Attachments are part of the [idempotency](#idempotency) fingerprint.

Inbound mail keeps its attached files under the same limits. Every leaf part that is not used as
the text or HTML body is a file, including inline images. A part without a file name is named
`attachment-N`. Because inbound mail is at most 5 MiB raw, its files always fit the size limit;
parts after the tenth are not stored.

The message detail lists files as `{ "id", "filename", "content_type", "size" }` without content.
Listings carry only `attachment_count`.

```http
GET /v1/mailboxes/{mailbox_id}/messages/{message_id}/attachments/{attachment_id}
```

returns the bytes of one file to a member of the owning organization. A file that does not exist
under that message is `404 MAILBOX.MESSAGE_NOT_FOUND`. The response is always an opaque download:
`Content-Type: application/octet-stream`, `Content-Disposition: attachment` with the stored name,
`X-Content-Type-Options: nosniff`, and `Cache-Control: private, no-store`. The declared
`content_type` is untrusted sender data and is never used to serve the file. Files are not
scanned for malware.

Binary files arrive byte for byte. A file that is entirely ASCII text may be carried as text, and
mail delivery then changes its line endings to CRLF; the received copy can differ from the sent
one in exactly that way.

Deleting a message for good, or deleting its mailbox, deletes its files. API-key routes can send attachments and see
their metadata; they cannot download files.

### Templates

A template is reusable content of an organization: a `name`, a `subject`, and an `html` and `text`
body, at least one of which is not empty. A member with the `templates` grant edits the
organization's standard templates (transactional templates below need an owner or admin); reading
templates needs the `templates`, `broadcasts`, or `api_keys` grant (see
[Member Feature Grants](#member-feature-grants)). An organization keeps at most 200, and names are
unique within it.

Placeholders may appear in the subject and both bodies:

| Form | Meaning |
|---|---|
| `{{name}}` | The value of `name`. A send without it is refused. |
| `{{name\|fallback}}` | The value of `name`, or `fallback` when it is absent or empty. |

A name is a letter followed by letters, digits, or underscores, at most 64 characters. Spaces
inside the braces are ignored. Any `{{` that does not open such a placeholder makes the template
`422 MAILBOX.TEMPLATE_INVALID`, so a typo is caught when the template is saved. There are no
conditionals, loops, or helpers.

Values are always escaped in the HTML body, so a value cannot add markup; there is no unescaped
form. In the subject, line breaks inside a value become spaces. The text body uses values as they
are.

`sample_variables` holds example values for the editor. They are used by previews and test sends
and never by a real send.

`design` holds the visual editor's document, an object with a positive integer `version`, at most
256 KiB. The console renders `html` and `text` from it before saving; the API stores it as given,
never renders it, and always sends the bodies. A response has `design: null` for a template written
in HTML, and a replace without `design` clears it. Anything else in `design` is
`422 MAILBOX.TEMPLATE_INVALID`.

| Route | Result |
|---|---|
| `GET /v1/mail-templates` | Every template by name, with `variables` and without bodies |
| `POST /v1/mail-templates` | `201` with the template |
| `GET`, `PUT`, `DELETE /v1/mail-templates/{id}` | Read, replace, or delete one template |
| `POST /v1/mail-template-previews` | Renders `template_id` or an unsaved `source`, with `variables` over the sample values; returns `subject`, `html_body`, `text_body`, `variables`, and `missing`. Sends nothing. |
| `POST /v1/mail-templates/{id}/tests` | Renders with `variables` over the sample values and sends it from `mailbox_id` to 1 to 5 addresses in `to`, like any send; needs `send` on that mailbox |

Sending with `template` renders the stored template with the given `variables` only. If a variable
without a fallback has no value, the send is `422 MAILBOX.TEMPLATE_VARIABLES_MISSING` with one field
error per variable, named `template.variables.<name>`, and nothing is stored or sent. The message
stores the rendered content and `template_id`; editing or deleting the template later does not change
it. The idempotency fingerprint covers the rendered content.

A template of another organization is `404 MAILBOX.TEMPLATE_NOT_FOUND`.

#### Transactional templates

Every template has a `kind`: `standard` (the default, everything above) or `transactional`. The kind
is fixed when the template is created; a replace that changes it is
`409 MAILBOX.TEMPLATE_KIND_IMMUTABLE`. `GET /v1/mail-templates?kind=standard|transactional` lists one
kind; without `kind` it lists every template.

A transactional template also has:

| Field | Rule |
|---|---|
| `key` | Required. `^[a-z][a-z0-9-]{0,63}$`, unique within the organization (`409 MAILBOX.TEMPLATE_KEY_TAKEN`). The API names the template by it. |
| `from_address` | Required. A bare address, stored lowercased, whose domain is a `verified` domain of the organization (`send_receive` or `send_only`). With the `log` provider the instance's own `MAILBOX_DOMAIN` is also allowed. Otherwise `422 MAILBOX.DOMAIN_NOT_VERIFIED`. |
| `from_name` | Optional display name, at most 100 characters. |
| `variable_definitions` | At most 100 items of `{ name, secret, required }`, names unique and following the placeholder name rule. Empty or absent when the content has no placeholders. |

The variable definitions must match the content exactly, or the save is
`422 MAILBOX.TEMPLATE_INVALID` with an `error` message naming the problem:

- every placeholder in the subject and bodies is declared, and every declared variable is used;
- a `required` variable has no fallback, and every placeholder of an optional variable has one;
- a `secret` variable is `required`, so it never has a fallback.

A `standard` template must not carry `key`, `from_address`, `from_name`, or `variable_definitions`
(`422 MAILBOX.TEMPLATE_INVALID`). Responses always carry `kind`, `key` (null for standard), `from`
(`{ address, name }`, null for standard), and `variable_definitions` (empty for standard).
`sample_variables` may hold example values for secret variables too; they are used only by previews.

Every member reads every template and writes standard ones. Creating, replacing, or deleting a
transactional template needs an owner or admin; a member is `403 PERMISSION.DENIED`.

A transactional template serves only transactional messages. Naming one in a mailbox send
(`template`), a template test (`POST /v1/mail-templates/{id}/tests`), or a broadcast (create,
update, or send) is `422 MAILBOX.TEMPLATE_KIND_MISMATCH`. A broadcast copies its template when it
starts, so a running broadcast is not affected. If the sender domain later loses verification, the template is
kept and sends from it are refused.

### Contacts

A contact is one address an organization sends broadcasts to: `email`, `first_name`, `last_name`,
up to 50 string `properties`, a `status`, and the lists it belongs to. An organization keeps at most
100000 contacts, one per address. A member with the `contacts` grant reads and edits them; the
`broadcasts` grant alone reads only the list names (`GET /v1/mail-contact-lists`).

| Field | Rule |
|---|---|
| `email` | A bare address, lowercased, within the RFC 5321 limits (local part 64 characters, each domain label 63, the whole address 254). Set when the contact is created and never changed. |
| `first_name`, `last_name` | Whitespace collapsed; at most 100 characters each; may be empty. |
| `properties` | Names follow the template variable rules and cannot be `email`, `first_name`, or `last_name`. Values are trimmed, at most 500 characters; an empty value removes the property. |
| `status` | `subscribed`, `unsubscribed`, or `suppressed`. Members set the first two; a new contact is subscribed. Zmail sets `suppressed` after a permanent bounce or a complaint (with the broadcasts slice). |
| `status_reason` | `manual` when a member unsubscribed the contact; empty when subscribed; kept while suppressed. |
| `list_ids` | The complete set of lists the contact belongs to. Every id must be a list of the organization. |

`PUT` replaces the names, properties, status, and lists; it ignores `email`. Leaving `status` out
keeps the current status, so editing a suppressed contact does not resubscribe it.

`GET /v1/mail-contacts` pages contacts newest first. `q` matches the address, first name, or last
name case-insensitively; `status` and `list_id` narrow it.

A list is a named group of the organization's contacts, used to choose who receives a broadcast.
Recipients never see lists. An organization keeps at most 100 lists, names are unique within it,
and a contact may be in any number of them. `GET /v1/mail-contact-lists` returns every list with
`contact_count`. Deleting a list keeps its contacts; deleting a contact removes it from its lists.

A contact, list, or list id of another organization is `404` (`MAILBOX.CONTACT_NOT_FOUND` or
`MAILBOX.CONTACT_LIST_NOT_FOUND`).

### Contact Import

```json
{ "csv": "email,first_name,plan\nann@example.com,Ann,pro\n", "list_id": 3 }
```

`POST /v1/mail-contact-imports` reads UTF-8 CSV text of at most 5 MiB and 10000 data rows, with a
header row. A leading byte-order mark is ignored and blank lines are skipped. Header names are
compared case-insensitively, with spaces and hyphens read as underscores, so `First Name` is
`first_name`.

| Column | Meaning |
|---|---|
| `email` (also `e-mail`, `email address`) | Required. The contact's address. |
| `first_name`, `last_name` | Names. |
| Any other name that can be a property | A property. |
| Anything else | Ignored, and listed in `ignored_columns`. |

For each row:

- A new address becomes a subscribed contact.
- An existing address is updated: a non-empty cell replaces the stored value, an empty cell keeps
  it, and properties are merged. The status never changes, so an unsubscribed contact stays
  unsubscribed.
- A row whose address is unusable, repeated earlier in the file, or whose names or properties
  break the rules above is skipped with a reason: `invalid_email`, `duplicate`, `invalid_name`,
  `invalid_property`, or `too_many_properties`.

With `list_id`, every stored row joins that list. The stored rows are written in one transaction:
an import that would pass the contact limit is `409 MAILBOX.CONTACT_LIMIT_REACHED` and stores
nothing. A file that is not UTF-8, too large, has more rows, has no `email` column, repeats a column,
or has broken quoting is `422 MAILBOX.CONTACT_IMPORT_INVALID`.

The response counts `rows`, `created`, `updated`, and `skipped_count`, and lists the first 100
skipped rows by `line` (the header is line 1) and `reason`. Values are stored as plain strings;
nothing in a cell is evaluated.

### Broadcasts

A broadcast sends one template from one mailbox to the subscribed contacts of one list, one
message per contact. Access follows the sender mailbox:

- Listing shows only broadcasts whose sender mailbox the caller may `view`. Reading one and its
  recipients needs `view`; without any access to the sender mailbox the broadcast is
  `404 MAILBOX.BROADCAST_NOT_FOUND`, and with some access but not `view` it is `403`.
- Creating, editing, and deleting need `send` on the sender mailbox; editing needs it on the stored
  and on the new sender. A new sender without any access is `404 MAILBOX.NOT_FOUND`.
- Every broadcast route needs the `broadcasts` grant (owners and admins always have it); without it
  the answer is `403 ORGANIZATION.FEATURE_NOT_GRANTED`.
- Owners, admins, and members with the grant send, cancel, pause, and resume broadcasts. Sending
  and resuming also need `send` on the sender mailbox at that call; the worker does not check
  access again, so a broadcast already sending keeps sending when that access is later removed.
  A member cancels or pauses only a broadcast it could send. Owners and admins need no access to
  the sender mailbox to cancel or pause, so an admin can always stop mail; without `view` on it
  (or for a sealed broadcast) the answer is `204` with no body, so nothing about the broadcast is
  revealed.

Broadcasts need unsubscribe links, so they are available only when the API has
`MAILBOX_INBOUND_SECRET` (the links are signed with a key derived from it) and `MAILBOX_PUBLIC_URL`
(the public origin of the Web console). `GET /v1/mailbox-domains` reports `broadcasts_enabled`;
without it, sending is `503 MAILBOX.BROADCAST_DISABLED`.

| Status | Meaning |
|---|---|
| `draft` | Editable. Has no recipients. |
| `scheduled` | Recipients are fixed; sending starts at `scheduled_at`. |
| `sending` | The worker is sending its recipients. |
| `paused` | Recipients stay pending and nothing is sent; `stop_reason` says why. |
| `sent` | No recipient is pending, and a recipient was sent or none failed. |
| `canceled` | Stopped; recipients not yet sent became `canceled`. A broadcast that ends with failed recipients and none sent is `canceled` with `stop_reason` `all_recipients_failed`, never `sent`. |

`POST /v1/mail-broadcasts` creates a draft from `name`, `mailbox_id`, `template_id`, `list_id`,
and the optional `unsubscribe_label`, `test_percent`, `test_wait_minutes`, and `variables`. Each must belong to the organization.
`variables` are template values shared by every recipient, such as a promotion code or an event
date: at most 100, with variable names, values of at most 2,000 characters, and none of `email`,
`first_name`, `last_name`, or `unsubscribe_url`; otherwise `422 MAILBOX.BROADCAST_INVALID`. They are
fixed with the rest of the draft when sending starts. `PUT` replaces a draft
and is `409 MAILBOX.BROADCAST_NOT_EDITABLE` in any other state.

`POST /v1/mail-broadcasts/{id}/sends` with an optional `scheduled_at` starts a draft:

- The template is copied into the broadcast, so later edits to the template do not change it.
- One recipient is added per contact of the list whose status is `subscribed`. A list without one
  is `422 MAILBOX.BROADCAST_NO_RECIPIENTS`, and the draft stays a draft.
- A template variable without a fallback that no recipient could fill (it is not in the
  broadcast's `variables`, not `email`, `first_name`, `last_name`, or `unsubscribe_url`, and not a
  property of any subscribed contact of the list) is
  `422 MAILBOX.TEMPLATE_VARIABLES_MISSING` with one field error per variable, named
  `variables.<name>`, and the draft stays a draft. A variable only some contacts lack still fails
  just those recipients when they are sent.
- Without `scheduled_at`, or with a time in the past, the broadcast is `sending`; otherwise it is
  `scheduled`, at most a year ahead.

`POST /v1/mail-broadcasts/{id}/cancellations` cancels a `scheduled`, `sending`, or `paused` broadcast.

#### Staged Sending And Automatic Pause

- With `test_percent` from 1 to 50, that share of the recipients, picked at random and at least
  one, forms the test wave (`current_wave` 1). When it is done the broadcast is `paused` with
  `stop_reason` `test_wave_complete` and `resume_at` set `test_wait_minutes` later (default 30, 5 to
  1440). At `resume_at` the worker checks the rates below and sends everyone else
  (`current_wave` 2).
- Before every round the worker checks each sending broadcast, and each waiting test wave, once at
  least 20 recipients are finished. At 5% bounces (`counts.bounced`, including send-time bounces) it
  pauses with `bounce_rate_high`, and at 0.3% spam complaints (`counts.complained`, Gmail's limit
  for bulk senders) with `complaint_rate_high`.
- `POST /v1/mail-broadcasts/{id}/pauses` holds a sending broadcast, or a waiting test wave, with
  `paused_by_owner`. `POST /v1/mail-broadcasts/{id}/resumptions` continues a paused one: a waiting
  test wave continues with everyone, a broadcast paused by hand continues the wave it was in, and
  one the health check paused continues with everyone and `safety_override` true, so it is not
  paused automatically again. Either is `409 MAILBOX.BROADCAST_NOT_EDITABLE` in another state. A
broadcast can be deleted when it is `draft`, `sent`, or `canceled`; messages already sent stay in
the mailbox.

#### Sending

The `mail:broadcast:work` worker sends each recipient as an ordinary outbound message from the
broadcast's mailbox, so the organization's broadcast lane limit, delivery events, the log, and
webhooks apply to it.

- Variables come from the broadcast's `variables`, then the contact's properties (which override a
  shared value of the same name), then the contact's `email`, `first_name`, `last_name`, and
  `unsubscribe_url`. A variable without a value or fallback fails that recipient with
  `template.variables_missing`.
- When no recipient is pending the broadcast ends: `sent`, or `canceled` with `stop_reason`
  `all_recipients_failed` when recipients failed and none was sent.
- If the template does not use `{{unsubscribe_url}}`, a small link with `unsubscribe_label` (or
  `Unsubscribe`) is added at the end of the HTML body and the text body.
- Every message carries `List-Unsubscribe` and `List-Unsubscribe-Post: List-Unsubscribe=One-Click`,
  so mail clients can offer one-click unsubscribe.
- A contact that is no longer `subscribed` when its turn comes is `skipped`
  (`contact_not_subscribed`); a deleted contact is `skipped` (`contact_deleted`); a contact whose
  address is on the organization's suppression list (see [Transactional Mail](#transactional-mail))
  is `skipped` (`contact_suppressed`) and keeps its status.
- Each recipient is sent with an idempotency key of its broadcast and contact, so a worker that stops
  after the provider call never sends that contact a second message.
- When the broadcast lane limit (`daily_broadcast_limit`) is reached, the broadcast keeps `sending`
  with `stop_reason` `send_limit_reached` and continues when the 24-hour window has room. Personal
  and transactional sends never use the broadcast lane's room, and a broadcast never uses theirs.

The broadcast reports `counts` of its recipients by status. `GET
/v1/mail-broadcasts/{id}/recipients` pages them, with an optional `status` filter, each with the
stored `message_id`, a `failure_code`, the message's `last_event` (see [Delivery Events](#delivery-events),
so `delivered`, `bounced`, or `complained`), and the contact's current `contact_status` (`unsubscribed`
once the recipient used the link; null once the contact is deleted).

#### Progress And Estimated Finish

`GET /v1/mail-broadcasts/{id}` adds `progress` for a `scheduled`, `sending`, or `paused`
broadcast; it is null in the listing, in answers to writes, and for any other status. It is
computed on every request and never cached:

```json
{
  "rate_per_minute": 42.5,
  "rate_window_minutes": 10,
  "estimated_finish_at": "2026-11-24T08:39:00Z",
  "send_limit_pauses": true,
  "send_limit_resumes_at": "2026-11-24T08:00:00Z",
  "computed_at": "2026-11-23T08:00:00Z"
}
```

| Field | Meaning |
|---|---|
| `rate_per_minute` | Recipients that became `sent`, `failed`, or `skipped` in the last `rate_window_minutes` (10), per minute, counted over the part of that window since `started_at` and at least one minute; two decimals. Null when none finished in the window, and always null unless the broadcast is `sending`. |
| `estimated_finish_at` | When the last pending recipient should be finished. Null when it cannot be told: a broadcast paused by a person or by the health check, or one without a rate whose pending recipients all fit in the broadcast lane's room. |
| `send_limit_pauses` | True when the organization's 24-hour broadcast lane limit (`daily_broadcast_limit`, see [Sending](#sending)) holds the broadcast now or will hold it before every pending recipient is sent. |
| `send_limit_resumes_at` | When the lane has room again for a broadcast the limit holds or will hold; null when `send_limit_pauses` is false. |

The estimate uses only what the API stores: the pending count, the rate, the lane limit that
applies to the organization now, and the instants of the lane's attempts in the rolling window
(deleted messages included, exactly as the limit counts them). Sending starts now, at
`scheduled_at`, or at `resume_at` of a waiting test wave. While the lane has room for every pending
recipient, the finish is that start plus pending divided by the rate. Otherwise sending pauses once
the room is used, resumes when the oldest attempt leaves the 24-hour window, and the n-th recipient
over the room is sent when the n-th oldest attempt leaves it; past the attempts in the window,
each further day sends one limit's worth. Without a rate, only the limit's waits count. The estimate
assumes no other broadcast of the organization uses the lane meanwhile, so it is a guide, not a
promise.

#### Notices

The mail worker tells the person who started a broadcast (who sent or scheduled it) when it:

| Event | `kind` |
|---|---|
| Ends `sent` | `mail.broadcast_sent` |
| Ends `canceled` with `stop_reason` `all_recipients_failed` | `mail.broadcast_failed` |
| Is paused with `bounce_rate_high` or `complaint_rate_high` | `mail.broadcast_paused` |
| Starts waiting with `send_limit_reached` | `mail.broadcast_paused` |

A broadcast canceled or paused by a person, or a test wave waiting out its wait, sends no notice.
Each notice says what happened in counts, what Zmail does next (for the send limit, when sending
resumes on its own, in UTC), and what the person can do, and links to
`{MAILBOX_PUBLIC_URL}/console/mail-broadcasts?org={organization_id}&open={broadcast_id}`.

- When the notification starter runs, the notice is an in-app notification of that `kind` for the
  starter (`in_app` channel only, `action_url` the console path above).
- When the system mailer (`MAIL_PROVIDER` and `MAIL_FROM`, the mailer of password resets) is configured, it also goes by email to the starter's account
  address. Email is sent whatever the person's notification preferences say, because it is about
  mail they sent.
- Each event is told once. A notice whose email could not be sent is tried again on the next
  worker round under the same idempotency key; one the mailer refuses for good is not. A
  broadcast that leaves a state and enters it again (the limit holds it on a later day) is told
  again. A starter who is no longer a member of the organization is not told. Broadcasts that
  finished more than seven days ago are not looked at, and those that had ended or paused before
  this feature was deployed are not told about.

#### Unsubscribe And Suppression

An unsubscribe link is `{MAILBOX_PUBLIC_URL}/unsubscribe/{token}`, a page of the Web console. The
token names one contact and does not expire. `POST /v1/mail-unsubscribes/{token}` needs no session:

- A valid token is `204`; the contact becomes `unsubscribed` with `status_reason`
  `unsubscribe_link`. A contact already unsubscribed or suppressed, or one that no longer exists,
  also gets `204`, so the answer discloses nothing.
- Any other token is `404 MAILBOX.UNSUBSCRIBE_INVALID`.

The `List-Unsubscribe` header points at `{MAILBOX_PUBLIC_URL}/api/mail-unsubscribes/{token}`,
which the Web console forwards to this route for mail clients.

A broadcast recipient whose message bounces permanently, is refused by the provider's suppression
list, or draws a spam complaint becomes `suppressed`, with `status_reason` `bounced`, `rejected`, or
`complained`. Later broadcasts skip it. A `bounced` or `complained` delivery event for a broadcast
message also adds the address to the organization's suppression list.

### Log And Statistics

`GET /v1/mail-messages` lists messages across the mailboxes of the organization the caller may
`view`, newest first, as summaries with the owning `mailbox_address` and without bodies. Mail of
other mailboxes is neither listed nor counted in `total`. Read a message through
`GET /v1/mailboxes/:mailbox_id/messages/:message_id`.

| Query | Meaning |
|---|---|
| `direction` | `inbound` or `outbound` |
| `status` | `received`, `queued`, `sent`, or `failed` |
| `mailbox_id` | One mailbox. A mailbox the caller may not read, or another organization's, matches nothing. |
| `q` | Case-insensitive match on the subject, the sender address, or a recipient address; at most 200 characters. `%` and `_` match literally. |
| `page`, `per_page` | Pagination |

An unknown `direction` or `status`, a non-positive `mailbox_id`, or an over-long `q` is
`400 COMMON.INVALID_INPUT`. Messages of deleted mailboxes do not appear.

The log, the statistics, and `GET /v1/outbound-mail-messages/:message_id` are delivery records, not
mailbox views: they ignore placement, so archived, filed, and trashed messages appear and count as
before. A message deleted for good disappears from them.

`GET /v1/mail-message-stats?days=N` returns one entry per UTC day for the last `N` days ending
today, oldest first, including days without mail. `N` defaults to 7 and is 1 to 90.

```json
{ "day": "2026-11-16", "sent": 3, "failed": 1, "received": 2 }
```

`sent` and `failed` count outbound messages by their final status; a message still `queued` is in
neither. `received` counts inbound messages. Only mailboxes the caller may `view` are counted.

### Unified Inbox

`GET /v1/inbox-messages` lists the caller's mail across every mailbox of the organization they have
`view` on, in one cursor-paged list. Owners and admins read nothing more through it than through the
mailboxes themselves, and a [mail role](#mail-roles) never widens it. Sealed messages and the trash
never appear.

| View | Direction | Placement | Also | `group=thread` |
|---|---|---|---|---|
| `all` (default) | `inbound` | `default` | | Allowed |
| `today` | `inbound` | Any except `trash` | `created_at` is in today (see below) | No |
| `pending` | Both | Any except `archive` and `trash` | The caller starred it | No |
| `sent` | `outbound` | `default` | | Allowed |

`all` and `sent` are every mailbox's Inbox and Sent. `today` is what arrived today, read or not,
including mail archived or filed since; mail moved to the trash leaves it. `pending` is the caller's
own stars.

| Query | Meaning |
|---|---|
| `view` | `all`, `today`, `pending`, or `sent` |
| `role_id` | A positive mail role id, or `none` for mailboxes without a role. A role the caller cannot see matches nothing. |
| `mailbox_id` | One mailbox. A mailbox the caller may not read, or another organization's, matches nothing. |
| `unread` | `true` or `false`, as in the single-mailbox list; outbound messages are always read |
| `has_attachments` | `true` keeps only messages with files, `false` only those without |
| `q` | Search, only with `view=all`; the same rules, matching, and 2-second limit as the single-mailbox search |
| `since` | A calendar day `YYYY-MM-DD` (1970 or later) in `tz`; keeps messages created on or after its start |
| `until` | A calendar day `YYYY-MM-DD` in `tz`, not before `since`; keeps messages created before the end of that day |
| `group` | `thread`, only with `view` `all` or `sent`, and never with `unread`, `has_attachments`, `q`, `since`, or `until` |
| `assignment` | `mine` keeps open conversations assigned to the caller, `unassigned` open conversations without an assignee, `closed` conversations marked done; see [Assignment](#assignment). Allowed with `group`. A conversation never assigned is open and unassigned, so in a personal mailbox only `unassigned` matches. |
| `triage` | Comma-separated distinct [triage](#ai-triage) categories, such as `reply,review`; only with `view` `all` or `today`, and never with `group` or `q`. Matches nothing while the organization's AI triage is off. |
| `tz` | The IANA time zone that defines today, at most 64 characters; defaults to `UTC` |
| `cursor` | `meta.next_cursor` from the previous page, unchanged |
| `per_page` | 1 to 100, default 15; an integer outside the range is clamped |

Filters combine with AND. Each parameter may appear once. A repeated parameter, `page`, an unknown
value, a malformed `cursor`, a `tz` that is not a known IANA zone (including `Local` and an empty
value), `q` with a view other than `all`, or `group` or `triage` outside its rule is
`400 COMMON.INVALID_INPUT`. An unreadable, missing, or foreign `mailbox_id` or `role_id` answers an
empty page, never `403` or `404`, so the filters cannot probe what exists.

With `q`, the list covers every placement except the trash, in both directions, in the readable
mailboxes, narrowed by `role_id`, `mailbox_id`, and `unread`. A search the database cannot finish
within 2 seconds is `422 MAILBOX.SEARCH_TOO_BROAD`.

Today starts at 00:00 of the current date in `tz` and ends at 00:00 of the next date there; the start
is inclusive and the end exclusive, so a day can last 23 or 25 hours when the clocks change. The
counts route echoes both instants.

The list is ordered by `created_at`, newest first, then by id, highest first. `meta` is
`{ per_page, has_more, next_cursor }`; pass `next_cursor` as `cursor` for the next, older page. Mail
that arrives while paging lands before the first page and never repeats or skips a later one.

```json
{
  "code": 0,
  "message": "success",
  "data": [
    {
      "id": 9021, "mailbox_id": 12, "mailbox_address": "news@example.com", "role_id": 3,
      "direction": "inbound", "status": "received", "queued_reason": null,
      "from": { "address": "ann@example.com", "name": "Ann Lee" },
      "to": ["news@example.com"], "cc": [],
      "subject": "Can the Pro plan grow to 20 members?",
      "snippet": "Hi, folders work great. Can the Pro plan grow to 20 members?",
      "read": false, "starred": false, "attachment_count": 0, "template_id": null,
      "placement": "default", "folder_id": null, "label_ids": [], "thread_id": 9001,
      "trashed_at": null, "failure_code": "", "last_event": "",
      "created_at": "2026-11-19T06:22:00Z",
      "thread": { "message_count": 3, "message_ids": [9001, 9010, 9021], "unread": true, "starred": false, "label_ids": [] }
    }
  ],
  "meta": { "per_page": 15, "has_more": true, "next_cursor": "eyJmIjoiY3JlYXRlZF9hdCIs" }
}
```

Each item has every field of the single-mailbox list item, plus `mailbox_address`, `role_id` (the
mailbox's mail role now, so moving a mailbox to another role relabels its old mail), and `snippet`:
the first 400 characters of the text body with whitespace runs collapsed to one space, trimmed, and
cut to 140 characters; it is empty for mail without a text body. `read` and `starred` are the
caller's own. `triage` is the message's [triage result](#ai-triage) or `null`. There is no body,
`message_id`, `in_reply_to`, event list, or attachment list.

With `group=thread`, each row is the newest message of one thread of one mailbox in the view and
carries `thread`, exactly as in the [single-mailbox list](#listing-and-search). A thread that gets a
new message while paging moves above the cursor and reappears on the first page.

`GET /v1/inbox-message-counts` takes only `tz` and answers the caller's live counts over the mailboxes
they may read:

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "views": { "all_unread": 9, "today": 14, "today_unread": 5, "pending": 3, "sent_waiting": 0 },
    "roles": [{ "role_id": 3, "unread": 4 }, { "role_id": 5, "unread": 3 }],
    "ungrouped_unread": 2,
    "mailboxes": [{ "mailbox_id": 12, "unread": 3 }, { "mailbox_id": 14, "unread": 1 }],
    "today_starts_at": "2026-11-18T16:00:00Z",
    "today_ends_at": "2026-11-19T16:00:00Z",
    "triage": { "reply": 2, "deadline": 1, "fyi": 5, "review": 0 }
  }
}
```

- `mailboxes` has one entry per readable mailbox, by `mailbox_id` ascending; each `unread` equals that
  mailbox's `unread_count`. `all_unread` is their sum.
- `roles` has one entry per mail role with at least one readable mailbox, by `role_id` ascending;
  `ungrouped_unread` covers the readable mailboxes without a role, so `all_unread` is the sum of
  `roles` plus `ungrouped_unread`.
- `today` and `today_unread` count the `today` view and its unread part; `pending` counts the
  `pending` view.
- `sent_waiting` counts outbound messages of the readable mailboxes waiting for the send limit
  (`queued_reason` `send_limit`); it is not organization usage.
- A caller with no readable mailbox gets zeros and empty arrays. Sealed messages and the trash are
  never counted.
- `triage` counts the caller's unread inbound messages in `default` by triage category; it is `null`
  while the organization's AI triage is off, and clients hide triage when it is.

### Signatures

Each member keeps their own plain-text signature per mailbox they may send from (`send`);
another member of a shared mailbox never sees it. `GET /v1/mailboxes/:mailbox_id/signature`
answers `{ "mailbox_id": 12, "body": "Best,\nAda", "updated_at": "..." }`, with an empty `body`
and a null `updated_at` until one is saved. `PUT` with `{ "body": "..." }` replaces it: line
breaks become `\n`, trailing white space is dropped, and an empty result removes it. The body is
at most 2000 characters and holds no control characters other than line breaks and tabs
(`422 COMMON.VALIDATION_FAILED`). A viewer without `send` gets `403`; one without any access
`404 MAILBOX.NOT_FOUND`. Neither response is cached. The API never adds the signature to mail:
clients place it in what is written, where the writer can still change it.

### Drafts

A draft is a message a member saved without sending; only its writer sees it, and an organization
member keeps at most 200 (`409 MAILBOX.DRAFT_LIMIT_REACHED` beyond that). `GET /v1/mail-drafts`
lists the caller's drafts in the organization, the latest change first, as
`{ "id", "mailbox_id", "to", "cc", "subject", "preview", "in_reply_to", "created_at", "updated_at" }`,
where `preview` is at most the first 200 characters of the body on one line.
`GET /v1/mail-drafts/:draft_id` returns one draft with its whole `body` in place of `preview`.
`POST` saves a new one and `PUT /v1/mail-drafts/:draft_id` replaces all of one with
`{ "mailbox_id", "to", "cc", "subject", "body", "in_reply_to" }`. `mailbox_id` is the sender and
needs `send` (`403`; `404 MAILBOX.NOT_FOUND` without any access). `to` and `cc` are kept as
written, one line each (a line break between addresses becomes `,`), and checked only when the
message is sent; `body` line breaks become `\n`.
Limits are 4000 characters for `to` and `cc`, 998 for `subject` and `in_reply_to`, and 100000 for
`body`, with no control characters other than tabs and body line breaks
(`422 COMMON.VALIDATION_FAILED`). Another member's draft, or a deleted one, is
`404 MAILBOX.DRAFT_NOT_FOUND`. Concurrent saves never take a member past 200. Deleting a mailbox deletes its drafts. Attachments stay in the
browser. Responses are never cached.

### AI Triage

AI triage sorts new inbound mail into `reply` (the reader should answer), `deadline` (it carries a
date), `fyi` (information only), and `review` (the model could not decide), with a one-line reason.
It is off by default at three levels, and all three must be on for mail to be queued:

1. The instance names a model with `AI_TRIAGE_PROVIDER`. Without one, `available` is `false` and the
   feature cannot be turned on (`409 MAILBOX.TRIAGE_UNAVAILABLE`).
2. The organization switch, `GET` and `PUT /v1/mail-triage-setting`. Any member reads it; owners and
   admins replace it with every field: `{ "enabled": true, "daily_message_cap": 500,
   "draft_enabled": true }`. The response adds `available`, `instance_daily_cap`, and `updated_at`
   (`null` until saved). `daily_message_cap` is 1 to 100000; the effective cap is the lower of it
   and the instance cap, counted per UTC day, and mail over it waits for the next day.
3. The mailbox switch, `PUT /v1/mailboxes/:mailbox_id/triage-setting` with `{ "enabled": true }`,
   answering the mailbox. A personal mailbox's switch belongs to its owner alone; a shared
   mailbox's needs `manage_members`. Handover turns it off.

Only mail received while every switch is on is queued; turning triage on never classifies older
mail, and a message whose switches turn off before the worker reaches it is skipped. The worker
(`mail:triage:work`) classifies queued mail in batches; a model failure is retried after 1 and
5 minutes, and the third failure leaves the message in `review` with an empty reason.

A result is shared by every reader of the mailbox and shown only to callers who may read the
message: `{ "category": "deadline", "reason": "Asks for the contract by Friday",
"due_at": "2026-11-21T00:00:00Z", "source": "ai" }`. `due_at` is only for `deadline` and may be
`null`. While the organization switch is off, no result, filter, or count is shown.

`PUT /v1/mailboxes/:mailbox_id/messages/:message_id/triage` with `{ "category": "reply" }` records
a reader's own decision and needs `view`. It answers the result with `source` `manual` and an empty
reason; the model never overwrites it. `review` cannot be chosen, `due_at` is only for `deadline`
and lies within one year back and five years ahead, and an outbound message is
`422 COMMON.VALIDATION_FAILED`.

`POST /v1/mailboxes/:mailbox_id/messages/:message_id/reply-drafts` has no body and needs `send` on
the mailbox and the organization's `draft_enabled`. It answers
`{ "body": "...", "basis": [{ "kind": "message", "id": 9021, "label": "Contract" }] }`: plain text
for the reader to edit and the thread messages or templates it relied on. The draft is never stored
or sent, and a model failure is `502 MAILBOX.TRIAGE_MODEL_FAILED`.

The model reads only the [masked](#masked-secrets) form of mail, placed in the prompt as data it must
not follow. Triage sees the sender, subject, text body (8000 characters at most), the mailbox's mail
role, and up to 10 recent manual decisions of the mailbox; a draft sees the masked thread up to the
message, the organization's template names, and the mail role. Every setting change writes an audit
entry. All responses are `Cache-Control: private, no-store`.

### Idempotency

Both send routes accept an optional `Idempotency-Key` header of 1 to 255 printable ASCII
characters; anything else is `422 MAILBOX.IDEMPOTENCY_KEY_INVALID`. The key names one send within
one mailbox. A later send in the same mailbox with the same key never calls the provider again:

| First attempt | Repeat with the same content | Repeat with different content |
|---|---|---|
| `sent` | `200` with the stored message | `409 MAILBOX.IDEMPOTENCY_CONFLICT` |
| `failed` | The same error as the first attempt, with the stored message kept | `409 MAILBOX.IDEMPOTENCY_CONFLICT` |
| Waiting for the send limit | `202` with the stored message | `409 MAILBOX.IDEMPOTENCY_CONFLICT` |
| Still `queued` with the provider call in flight | `409 MAILBOX.SEND_IN_PROGRESS` | `409 MAILBOX.IDEMPOTENCY_CONFLICT` |

A key that matches a sealed message (see [Sealed History](#sealed-history)) is
`409 MAILBOX.IDEMPOTENCY_CONFLICT` with no message data, whatever the content.

Content is compared after normalization, so address case, duplicate recipients, and surrounding
whitespace in the subject do not matter. A failed attempt is replayed rather than retried because
the provider may have accepted it before the answer was lost; to send again on purpose, use a new
key. Without a key, every request is a new message.

### Masked Secrets

Inbound mail often carries one-time codes and account links. Every response that carries the
subject or a body of an inbound message masks them: message listings, threads, search, the
unified inbox (`subject` and `snippet`), the organization log, and the message detail. Outbound
messages are never masked, and stored mail is never changed.

- A code is 4–8 digits, `123 456` or `123-456`, or 6–8 letters and digits with at least one of
  each, within 40 characters after or 20 characters before a keyword such as `code`, `OTP`,
  `PIN`, `verification`, `2FA`, or the Chinese words for verification code and dynamic code. Numbers that belong to a date, time,
  decimal, version, amount, or `#` color are not codes. A code shows as `••••••`.
- A sensitive link is an `http` or `https` URL whose path or query contains `reset`, `recover`,
  `verify`, `confirm`, `magic`, `token`, `login`, `signin`, `auth`, `otp`, `password`, or
  `activate`. In text it shows as `host/••••`; in an HTML `href` as `https://host/••••`.
- HTML text is masked node by node; style and script contents are left alone, and every other
  byte of the body is kept.

The detail sets `secrets_masked` when anything was masked. `POST
/v1/mailboxes/:mailbox_id/messages/:message_id/reveal` needs the same access as reading the
message, returns it with nothing masked and `secrets_masked` `false`, answers
`Cache-Control: private, no-store`, and logs `mail.secret_revealed` with the organization, user,
mailbox, and message ids. API keys cannot call it. Anything that hands inbound mail to an AI
model must use the masked form.

### Member Feature Grants

An organization grants each `member` four features (see
[`ORGANIZATIONS.md`](ORGANIZATIONS.md#member-feature-grants)); owners and admins always have all of
them. A route that needs a feature the active membership lacks returns
`403 ORGANIZATION.FEATURE_NOT_GRANTED` before anything else is checked.

| Feature | Routes |
|---|---|
| `broadcasts` | Every `/v1/mail-broadcasts` route |
| `contacts` | Every `/v1/mail-contacts` route, `POST /v1/mail-contact-imports`, and writes to `/v1/mail-contact-lists` |
| `contacts` or `broadcasts` | `GET /v1/mail-contact-lists` |
| `templates` | `POST`, `PUT`, and `DELETE` on `/v1/mail-templates`, and `POST /v1/mail-templates/:template_id/tests` |
| `templates`, `broadcasts`, or `api_keys` | `GET /v1/mail-templates`, `GET /v1/mail-templates/:template_id`, `POST /v1/mail-template-previews` |
| `api_keys` | Every route authenticated by `X-API-Key` under [Programmatic Sending](#programmatic-sending), [Agent Grants](#agent-grants), and [Transactional Mail](#transactional-mail) |

## Programmatic Sending

Backend services send with an API key instead of a browser session. Create the key with
`POST /v1/api-keys` (see [`API_KEYS.md`](API_KEYS.md)) and pass it as `X-API-Key`, together with
the `Organization-Id` header. A key belongs to a user, and it acts with that user's current
membership of the named organization: the same header errors apply, and removing the user from the
organization stops the key from working there on the next request. Every route below also needs
the membership's `api_keys` grant (owners and admins always have it): a member's key without it is
`403 ORGANIZATION.FEATURE_NOT_GRANTED` in that organization, though the key itself stays valid.
Keys owned by an organization are not defined.

| Route | Required scope |
|---|---|
| `POST /v1/outbound-mail-messages` | `mail:send` |
| `GET /v1/outbound-mail-messages/:message_id` | `mail:read` |
| `POST /v1/transactional-messages` | `mail:transactional` |
| `GET /v1/transactional-messages/:message_id` | `mail:transactional` |
| `GET /v1/agent-mail-events` | `mail:agent` (see [Agent Grants](#agent-grants)) |
| `GET /v1/agent-mail-messages/:message_id` | `mail:agent` |
| `POST /v1/agent-mail-drafts` | `mail:agent` |

`mail:transactional` neither includes nor is included by `mail:send`; `*` grants both. Only the two
transactional routes accept test keys (see [`API_KEYS.md`](API_KEYS.md)); every other route here
refuses a test key with `403 PERMISSION.DENIED`.

```json
{
  "from": "hello@mail.example.com",
  "to": ["bob@example.com"],
  "subject": "Your receipt",
  "text": "Thanks for your order."
}
```

`from` must be the bare address of a mailbox of that organization; any other address, including
another organization's mailbox, is `404 MAILBOX.NOT_FOUND`. The key has exactly its owner's
[access](#access): sending needs `send` on the `from` mailbox, and a mailbox the owner has no access
to is `404 MAILBOX.NOT_FOUND`; reading a sent message needs `view` on its mailbox, and a message of
a mailbox the owner has no access to is `404 MAILBOX.MESSAGE_NOT_FOUND`. A message these routes
return, and one a template test send returns, has `starred` always `false`, because a star belongs
to a person viewing the mailbox. `placement`, `folder_id`, `label_ids`, `trashed_at`, and
`thread_id` are reported as stored. The remaining fields, limits, outcomes, and
`Idempotency-Key` behavior are those of [Send](#send). A key without the scope gets
`403 PERMISSION.DENIED`. The read route returns only outbound messages, so a `mail:read` key cannot
read mail that was received. Both routes are limited to the organization in the header.

## Agent Grants

An agent grant lets one API key, the agent, receive the new mail of one mailbox and act on it
without widening anyone's access. The agent is an API key of a person: it reads exactly what that
person may read, never more, and stops the moment that person stops. Agents never send; with the
`draft` capability they store reply drafts that a person reviews and sends.

### Issuing And Revoking

Only these people manage a mailbox's grants (issue, list, revoke, activity):

| Mailbox | Who |
|---|---|
| Personal, including a connected personal mailbox (`origin` `connected`) | Its owner alone. An organization owner or admin who is not the owner gets `403 PERMISSION.DENIED`, or `404 MAILBOX.NOT_FOUND` for a connected mailbox, which managers cannot see |
| Shared | A member with `manage`. An organization owner or admin only after adding themselves as a `manage` member (recorded like any member change) |

Anyone else who can see the mailbox gets `403 PERMISSION.DENIED`; someone with no access gets
`404 MAILBOX.NOT_FOUND`.

`POST /v1/mailboxes/:mailbox_id/agent-grants` (session) answers `201` with the grant:

```json
{
  "api_key_id": 12,
  "capabilities": ["read", "draft"],
  "reveal_secrets": false,
  "filters": { "senders": ["example.com", "billing@vendor.test"], "label_ids": [] },
  "expires_at": "2026-12-31T00:00:00Z"
}
```

- `api_key_id` must name a live (not test), unexpired, unrevoked key holding `mail:agent` (or `*`),
  whose owner is a current member of the organization with `view` on the mailbox: for a personal or
  connected mailbox that is only the owner; for a shared mailbox any member with `view`. A key of
  anyone other than the issuer is refused with one undifferentiated `422 MAILBOX.AGENT_GRANT_INVALID`
  whatever is wrong with it (unknown, another organization's, inactive, a test key, without the
  scope, or its owner unable to read the mailbox); only the issuer's own keys get a specific reason.
- `capabilities` must include `read`; `draft` is optional and needs the key's owner to have `send`.
  A connected mailbox is read-only whatever the permission, so `draft` is refused on it.
- `reveal_secrets` (default `false`) returns one-time codes and sign-in links unmasked; every such
  read is audited as `read_revealed`.
- `filters.senders`: at most 20 addresses or domains, case-insensitive. An entry with `@` matches the
  sender address exactly; a domain (a leading `@` is ignored) matches that domain and its
  subdomains. `filters.label_ids`: at most 20 labels of the mailbox; a message matches when it
  carries one of them as its event is written (labels applied on arrival; a label a person adds
  later does not create an event). Empty filters match every message. Filters narrow what is
  delivered; they are not authentication. A sender filter compares the message's `From` header,
  which a sender can forge: Zmail stores no SPF, DKIM, or DMARC verdict for inbound mail today, so it
  cannot refuse a spoofed sender. An agent must not treat a matching sender as proof of identity.
- `expires_at` is required, in the future, and at most 90 days ahead.

A request that breaks these rules is `422 MAILBOX.AGENT_GRANT_INVALID`. A key holds at most one
working grant per mailbox (`409 MAILBOX.AGENT_GRANT_EXISTS`), and a mailbox at most 10 working
grants (`409 MAILBOX.AGENT_GRANT_LIMIT`). A grant is working while it is live, its key is neither
deleted, revoked, nor expired, and its key's owner can still read the mailbox; a grant that stopped
working holds no slot and receives no events. Issuing writes an `agent_granted` access event.

A grant:

```json
{
  "id": 7, "mailbox_id": 3, "api_key_id": 12, "api_key_name": "Support agent", "api_key_prefix": "sk_live_ab",
  "key_user_id": 4, "granted_by": 4, "capabilities": ["read", "draft"], "reveal_secrets": false,
  "filters": { "senders": ["example.com"], "label_ids": [] },
  "status": "active", "expires_at": "2026-12-31T00:00:00Z", "revoked_at": null, "revoke_reason": null,
  "created_at": "2026-12-01T09:00:00Z"
}
```

`status` is `active`, `expired`, or `revoked`. `GET /v1/mailboxes/:mailbox_id/agent-grants` pages the
mailbox's grants newest first (`page`, `per_page`). `DELETE
/v1/mailboxes/:mailbox_id/agent-grants/:grant_id` revokes at once and answers `204`, also for an
already ended grant; an id that is not a grant of the mailbox is `404 MAILBOX.AGENT_GRANT_NOT_FOUND`.
`GET /v1/mailboxes/:mailbox_id/agent-grants/:grant_id/activity` returns the grant's newest 100 audit
rows, newest first: `{ "id", "grant_id", "api_key_id", "message_id", "action", "created_at" }` with
`action` `read`, `read_revealed`, or `draft`. Audit rows never hold a subject or a body and are kept
90 days (`MAIL_AGENT_AUDIT_RETENTION`). Like sealed mail, grants and audit rows created before a
mailbox's seal boundary are hidden from everyone: they are not listed, and their ids answer
`404 MAILBOX.AGENT_GRANT_NOT_FOUND`.

A grant stops working at once, on every agent route, when any of these happens:

| Event | Effect |
|---|---|
| A manager of the grants revokes it | `revoke_reason` `revoked` |
| `expires_at` passes | `status` `expired` |
| The key is revoked or expires | Authentication fails (`401`) |
| The key's owner leaves the organization | Every grant of their keys is revoked (`left_organization`), and the drafts of their personal mailboxes deleted |
| The key's owner is removed from a shared mailbox | Their grants of it are revoked (`member_removed`) |
| The connected account is disconnected (by its owner, a manager, deleting the mailbox, or the owner leaving) | The mailbox's grants are revoked (`connection_revoked`, or `left_organization`) and its drafts deleted; a revoked connection's mailbox writes no events |
| The mailbox is deleted, or a revoked connection's copies are purged | Every grant, event, draft, and audit row of the mailbox is deleted with it |
| A personal mailbox is handed over | Its grants are revoked (`handed_over`) and its drafts deleted, before its history is sealed |
| The key's owner no longer has `view` for any other reason, or the mailbox is deleted | The grant is ignored until it would apply again |

Every agent request checks the grant, the mailbox, and the key owner's current access; the
revocations above are the durable record. Revocations write `agent_revoked` access events. Nothing
in a message's content can create, widen, or end a grant.

### The Agent Feed

Agents authenticate with `X-API-Key` and `Organization-Id` like
[programmatic sending](#programmatic-sending); every agent route needs the `mail:agent` scope and
refuses test keys (`403 PERMISSION.DENIED`). `mail:agent` grants no other route, and no other scope
grants these.

Every inbound message Zmail stores, delivered or synced from a connected account, writes one
`message.received` event for each live grant of its mailbox whose filters it passes, in the
transaction that stores it. A message is stored once, so a grant gets exactly one event per
message, however often the delivery is retried. Events are never published to organization
webhooks.

`GET /v1/agent-mail-events?after=<cursor>&limit=<n>` returns the events of the key's working grants
in the organization, ascending:

```json
{
  "events": [
    { "id": 913, "type": "message.received", "grant_id": 7, "mailbox_id": 3, "message_id": 5512,
      "created_at": "2026-12-01T09:30:00Z" }
  ],
  "next_cursor": "913",
  "has_more": false
}
```

- `after` is a `next_cursor` from an earlier page; omitted, the feed starts at the oldest event kept.
  `limit` is 1 to 100, default 50. Anything else is `400 COMMON.INVALID_INPUT`.
- `next_cursor` is the last event's id, or `after` itself when the page is empty. Delivery is at
  least once: the same cursor returns the same events while the grants stay as they are, so an agent
  stores the cursor only after it has handled the page. Within an organization, event ids become
  visible in id order, so a cursor never skips an event committed later.
- A key without a working grant gets an empty feed. Events are kept 30 days
  (`MAIL_AGENT_EVENT_RETENTION`); every `mail:broadcast:work` pass deletes older events and audit
  rows in small batches, and `mail:agent-events:prune` deletes old events at once.
- Each key may make 600 requests a minute across the feed and message reads
  (`MAIL_AGENT_READS_PER_MINUTE`); past that it gets `429 COMMON.RATE_LIMITED` with `Retry-After`.

### Reading And Drafting

`GET /v1/agent-mail-messages/:message_id` returns a message one of the key's working grants received
an event for, in the shape of the message detail. Codes and sign-in links are masked
(`secrets_masked`) unless the grant has `reveal_secrets`. Each read writes an audit row (`read` or
`read_revealed`) and a revealing read logs `mail.agent_secret_revealed`. Agent reads never change
anyone's read state, star, or placement. Any other message, including mail stored before the grant
and mail of other mailboxes, is `404 MAILBOX.MESSAGE_NOT_FOUND`, as if it did not exist.

`POST /v1/agent-mail-drafts` stores a reply draft for a person:

```json
{ "message_id": 5512, "text": "Thanks, we will look into it.", "html": "<p>Thanks, we will look into it.</p>" }
```

It needs a working grant that received the message, holds `draft`, and whose key owner still has
`send` (`403 PERMISSION.DENIED` otherwise; a message the key cannot read is `404`). `text` is
required, and `text` and `html` are at most 100 KB each (`422 MAILBOX.MESSAGE_INVALID`). A grant keeps
one pending draft per message: the first answers `201`, a later one replaces its body and answers
`200`. Each draft writes a `draft` audit row. A draft:

```json
{ "id": 21, "mailbox_id": 3, "grant_id": 7, "message_id": 5512, "api_key_id": 12,
  "text": "Thanks, we will look into it.", "html": "", "status": "pending",
  "created_at": "2026-12-01T09:31:00Z", "updated_at": "2026-12-01T09:31:00Z" }
```

`GET /v1/mailboxes/:mailbox_id/agent-drafts` (session, `send` on the mailbox) pages the mailbox's
drafts newest first, only while their message exists and is not sealed. Nothing sends a draft; a
person replies through [Send](#send).

## Transactional Mail

A transactional message is one message to one recipient, rendered from a
[transactional template](#transactional-templates) and sent by a backend service with an API key:
sign-up codes, password resets, receipts. It is a separate resource from mailbox messages: it has
no mailbox, never appears in a mailbox, the mail log, or the statistics, and counts only in the
transactional lane (see [Send](#send)).

The instance enables it with `MAILBOX_TRANSACTIONAL_ENABLED=true`, which also requires
`MAILBOX_SECRET_ENCRYPTION_KEY`. While it is off, `POST /v1/transactional-messages` and
`POST /v1/transactional-test-sends` answer `503 MAILBOX.TRANSACTIONAL_DISABLED`;
`GET /v1/mailbox-domains` reports the switch as `transactional_enabled`.

### Sending

```http
POST /v1/transactional-messages
X-API-Key: fastbuild_xxxxxxxx.yyyyyyyy
Organization-Id: 42
Idempotency-Key: signup-u_8812
Content-Type: application/json

{
  "template": "signup-code",
  "to": "lilei@example.com",
  "variables": { "code": "482913", "app": "Acme" }
}
```

| Field | Rule |
|---|---|
| `template` | Required. The `key` of a template of the organization, `^[a-z][a-z0-9-]{0,63}$`. An unknown key, or one of another organization, is `404 MAILBOX.TEMPLATE_NOT_FOUND`; a standard template is `422 MAILBOX.TEMPLATE_KIND_MISMATCH`. |
| `to` | Required. One bare address of 3 to 320 characters, trimmed and lowercased. A display name or several addresses is `422 MAILBOX.MESSAGE_INVALID`. |
| `variables` | Optional values by name, as for templates (at most 100, each at most 2000 characters). A name the template does not declare is `422 MAILBOX.MESSAGE_INVALID` with a field error `variables.<name>`. A required variable that is missing or empty is `422 MAILBOX.TEMPLATE_VARIABLES_MISSING` with one field error per variable. |
| `Idempotency-Key` header | Required. 1 to 255 printable ASCII characters; missing or malformed is `422 MAILBOX.IDEMPOTENCY_KEY_INVALID`. |

The body is at most 64 KiB (`413 COMMON.REQUEST_TOO_LARGE`). The template's sender domain must be
verified (with the `log` provider the instance's `MAILBOX_DOMAIN` also passes), else
`422 MAILBOX.DOMAIN_NOT_VERIFIED`. A refused request stores nothing.

The checks run in this order, and the first failure answers:

1. API key authentication (`401`), the organization header and membership (`400`, `404`).
2. Scope `mail:transactional` (`403 PERMISSION.DENIED`). Authorization comes before any lookup, so a
   refused caller learns nothing about templates.
3. The instance switch (`503 MAILBOX.TRANSACTIONAL_DISABLED`).
4. The body (`413`, `422 COMMON.VALIDATION_FAILED`) and the `Idempotency-Key`
   (`422 MAILBOX.IDEMPOTENCY_KEY_INVALID`).
5. The per-key rate limit (`429 COMMON.RATE_LIMITED`).
6. The template, the recipient, and the variable names (`404 MAILBOX.TEMPLATE_NOT_FOUND`,
   `422 MAILBOX.TEMPLATE_KIND_MISMATCH`, `422 MAILBOX.MESSAGE_INVALID`).
7. Idempotent replay: a stored message with the same key answers here (`200` or
   `409 MAILBOX.IDEMPOTENCY_CONFLICT`), so a replay uses no recipient allowance.
8. The per-recipient rate limit (`429 MAILBOX.RECIPIENT_RATE_LIMITED`).
9. The sender domain (`422 MAILBOX.DOMAIN_NOT_VERIFIED`) and the required variables
   (`422 MAILBOX.TEMPLATE_VARIABLES_MISSING`).
10. The suppression list and the transactional lane (`429 MAILBOX.SEND_LIMIT_REACHED`).

Because the template is read before the replay check, replaying a key after its template was
deleted is `404 MAILBOX.TEMPLATE_NOT_FOUND`; the stored message stays readable by id.

A new message answers `202` and a replay `200`, both with `Location: /v1/transactional-messages/<id>`
and the message:

```json
{
  "id": 7012,
  "mode": "live",
  "simulated": false,
  "template": "signup-code",
  "from": { "address": "no-reply@notify.acme-app.com", "name": "Acme" },
  "to": "lilei@example.com",
  "to_masked": "li••@example.com",
  "subject": "Acme verification code: ••••••",
  "variables": { "code": "••••••", "app": "Acme" },
  "status": "queued",
  "queued_reason": null,
  "failure_code": null,
  "last_event": null,
  "attempts": 0,
  "idempotency_key": "signup-u_8812",
  "created_at": "2026-11-19T14:22:08Z",
  "sent_at": null,
  "events": []
}
```

The request returns once the message is stored; the provider is called afterwards.
`subject` and `variables` are copies taken at creation with every secret variable masked; the
rendered content is kept encrypted only while the message is `queued` and is wiped when it reaches
any other status.

### Idempotency

The `Idempotency-Key` names one message within the organization and the key's mode, for 24 hours
from that message's `created_at`. A live key and a test key of the same organization using the same
`Idempotency-Key` create two independent messages. Within the 24 hours, a repeat with the same
template, recipient (after trimming and lowercasing), and variables answers `200` with the stored
message whatever its status, and nothing is sent again; different content is
`409 MAILBOX.IDEMPOTENCY_CONFLICT`. After 24 hours the key is released (`idempotency_key` becomes
null on the old message) and names a new message. Two concurrent requests with one key create one
message; the other answers as a repeat.

### Modes And Simulation

Every message has the `mode` of the key that sent it (see [`API_KEYS.md`](API_KEYS.md)):

- A `test` message renders and validates everything, but never calls the provider, never counts in
  a lane, and never writes the suppression list. To a normal address it is simulated as delivered.
- Addresses on the instance's `simulator_domain` (`MAILBOX_SIMULATOR_DOMAIN`, default
  `test.zmail.im`) are simulated in both modes, the same way. The local part, case-insensitive and
  with an optional `+tag`, picks the outcome:

| Local part | Outcome |
|---|---|
| `delivered` | `sent`, with `accepted` and `delivered` events |
| `bounced` | `sent`, with `accepted` and `bounced` events |
| `soft-bounced` | `sent`, with `accepted` and `deferred` events |
| `complained` | `sent`, with `accepted`, `delivered`, and `complained` events |
| `suppressed` | `suppressed`, with a `rejected` event |

Any other local part on the simulator domain is `422 MAILBOX.MESSAGE_INVALID`. Simulated messages
have `simulated: true`, their events have `detail` `simulated`, and they publish webhooks as usual.

### Rate Limits

| Limit | Scope | Answer |
|---|---|---|
| `transactional_key_limit_per_second` (`MAILBOX_TRANSACTIONAL_KEY_PER_SECOND`, default 20) | One API key, per second | `429 COMMON.RATE_LIMITED` |
| `transactional_recipient_limit_per_minute` (`MAILBOX_TRANSACTIONAL_RECIPIENT_PER_MINUTE`, default 3) | One organization, mode, template, and recipient, per minute | `429 MAILBOX.RECIPIENT_RATE_LIMITED` |
| 5 per minute | Console test sends of one user | `429 COMMON.RATE_LIMITED` |

Each `429` sets `Retry-After` in whole seconds, at least 1. Windows are fixed, and a request refused
by a rate limit stores nothing.

### Suppression And The Lane

A recipient on the organization's suppression list is not sent to: the message is stored as
`suppressed` and answers `202`. A live, non-simulated message counts in the transactional lane
(see [Send](#send)). When the lane is full the message is stored as `queued` with `queued_reason`
`send_limit` and sent when the lane has room; when as many messages already wait as the lane allows,
the request is `429 MAILBOX.SEND_LIMIT_REACHED` and nothing is stored.

### Suppression List

The suppression list holds one entry per organization and address (lowercased). An address is
added when a delivery event reports it `bounced` or `complained` (see [Delivery Events](#delivery-events));
`rejected` events and provider refusals at send time never add one. Transactional messages to a
listed address are `suppressed`, and broadcasts skip it. Owners and admins manage the list; a
member gets `403 PERMISSION.DENIED`.

`GET /v1/mail-suppressions` lists entries newest first, by `created_at` then `id`, in keyset pages.
Each entry has `id`, `email`, `reason` (`bounced` or `complained`), `source` (`transactional`,
`broadcast`, or `backfill` for a contact suppressed before the list existed), and `created_at`.

| Query | Meaning |
|---|---|
| `email` | An exact address, at most 320 characters, compared after trimming and lowercasing |
| `reason` | `bounced` or `complained` |
| `cursor` | `meta.next_cursor` of the previous page |
| `per_page` | 1 to 100, default 50 |

An unusable filter or a malformed cursor is `400 COMMON.INVALID_INPUT`. `meta` holds only
`next_cursor`, null on the last page.

`DELETE /v1/mail-suppressions/:suppression_id` removes an entry and answers `204`. It is idempotent
and discloses nothing: an entry already removed, or one of another organization, also answers `204`
and changes nothing. A removal that deleted an entry is audited as `mail_suppression.removed` with
the entry's id and reason. Removing an address lets transactional messages reach it again; it never
changes a contact's status, so a contact still `suppressed` is still skipped by broadcasts until it
is changed too.

### Status And Retries

| Status | Meaning |
|---|---|
| `queued` | Waiting to be sent. `queued_reason` is `send_limit` (waiting for the lane), `retry` (waiting for a retry), or null. |
| `sent` | The provider took the message (or the simulator decided). Later delivery events update `last_event`. |
| `failed` | Not sent; `failure_code` says why. |
| `suppressed` | The recipient is on the suppression list; nothing was sent. |

A temporary provider failure for which the provider returned an HTTP status is retried after 30
seconds, 2 minutes, and 10 minutes: at most 4 provider calls (`attempts`). Any failure without an
HTTP status, such as an unreachable provider or an unreadable answer, may have been accepted, so the
message becomes `failed` with `failure_code` `transport.unknown_outcome` and is never sent again.
Other `failure_code` values: a provider's permanent refusal code, `send.expired` (queued more than an
hour), `send.sender_unverified` (the sender domain lost verification before dispatch), and
`transport.not_configured` (the deployment has no outbound transport; the provider was never called). To send
again on purpose, use a new `Idempotency-Key`.

`last_event` follows [Delivery Events](#delivery-events): the most severe recorded event, null when
none. Messages and their events are deleted 30 days after `created_at`.

### Reading A Message

`GET /v1/transactional-messages/:message_id` needs `mail:transactional` and returns the same object
with up to 50 events (`type`, `detail`, `occurred_at`), oldest first. A live key reads only live
messages and a test key only test messages; a message of the other mode or another organization is
`404 MAILBOX.TRANSACTIONAL_MESSAGE_NOT_FOUND`.

### Send Log

`GET /v1/transactional-mail-log` serves the console; any member of the organization may read it.
Each entry has `id`, `mode`, `simulated`, `template`, `to_masked`, `subject` (masked), `status`,
`queued_reason`, `failure_code`, `last_event`, `idempotency_key`, `created_at`, and `sent_at`, and
never the full recipient or the variables. Entries are newest first, in keyset pages.

| Query | Meaning |
|---|---|
| `mode` | `live` (default) or `test` |
| `status` | `queued`, `sent`, `failed`, or `suppressed` |
| `last_event` | `delivered`, `deferred`, `bounced`, `failed`, `rejected`, or `complained` |
| `outcome` | `delivered` (`last_event` is `delivered`) or `problem` (`last_event` is `bounced`, `complained`, `failed`, or `rejected`, or `status` is `failed` or `suppressed`); combined with `status` and `last_event` as an intersection |
| `template` | A template key |
| `to` | An exact recipient, at most 320 characters, compared after trimming and lowercasing |
| `since`, `until` | `created_at` range, `since` inclusive and `until` exclusive; default the last 7 days, at most 30 days |
| `cursor` | `meta.next_cursor` of the previous page |
| `per_page` | 1 to 100, default 50 |

An unusable filter, a longer range, or a malformed cursor is `400 COMMON.INVALID_INPUT`. `meta` holds
`next_cursor` (null on the last page) and `total_24h`: the messages of the requested mode created in
the last 24 hours, of the requested `template` when one is given, ignoring the other filters and
counted up to 10000.

`GET /v1/transactional-mail-log/:message_id` returns one entry of either mode with `from`, the masked
`variables`, and up to 50 events, oldest first; still without the full recipient. A message of
another organization is `404 MAILBOX.TRANSACTIONAL_MESSAGE_NOT_FOUND`.

### Masking

- A recipient keeps its domain and the first two characters of its local part (one when the local
  part has at most two), followed by `••`: `lilei@example.com` shows `li••@example.com`.
- A secret variable that parses as an `http` or `https` URL with a host shows `<host>/••••`, without
  user information, port, path, or query: `https://u:p@reset.acme.com/t?code=1` shows
  `reset.acme.com/••••`. Any other secret value shows `••••••`.
- The subject is rendered with masked secret values. Non-secret variables are shown as sent.

### Console Test Sends

`POST /v1/transactional-test-sends` with `{ "template", "mode", "variables" }` sends a
transactional template to the caller's own account email; the recipient cannot be named. Any member
may. Variables not given take the template's sample values. `mode` `live` counts in the
transactional lane and calls the provider; `test` is simulated as delivered. The server generates
the idempotency key, so every request is a new message. At most 5 per user per minute
(`429 COMMON.RATE_LIMITED`). Otherwise the request follows the sending rules above from the
per-recipient rate limit on, and answers `202` with the message, including `to`.

### Transactional Errors

| Condition | Status | `error_code` | Retry |
|---|---:|---|---|
| Organization header missing or malformed | 400 | `ORGANIZATION.CONTEXT_REQUIRED` / `ORGANIZATION.CONTEXT_INVALID` | After fixing |
| Key missing or invalid | 401 | `AUTH.UNAUTHORIZED` (or an `API_KEY.*` code) | No |
| Scope missing | 403 | `PERMISSION.DENIED` | No |
| Not a member, or no such organization | 404 | `ORGANIZATION.NOT_FOUND` | No |
| Unknown template key | 404 | `MAILBOX.TEMPLATE_NOT_FOUND` | No |
| Unknown message, or another mode's message | 404 | `MAILBOX.TRANSACTIONAL_MESSAGE_NOT_FOUND` | No |
| Same key, different content | 409 | `MAILBOX.IDEMPOTENCY_CONFLICT` | With a new key |
| Body too large | 413 | `COMMON.REQUEST_TOO_LARGE` | After fixing |
| A field failed validation | 422 | `COMMON.VALIDATION_FAILED` | After fixing |
| Unusable recipient, undeclared variable, unknown simulator address | 422 | `MAILBOX.MESSAGE_INVALID` | After fixing |
| `Idempotency-Key` missing or malformed | 422 | `MAILBOX.IDEMPOTENCY_KEY_INVALID` | After fixing |
| Not a transactional template | 422 | `MAILBOX.TEMPLATE_KIND_MISMATCH` | No |
| Required variable missing or empty | 422 | `MAILBOX.TEMPLATE_VARIABLES_MISSING` | After fixing |
| Sender domain not verified | 422 | `MAILBOX.DOMAIN_NOT_VERIFIED` | After verifying |
| Per-key or per-user rate limit | 429 | `COMMON.RATE_LIMITED` with `Retry-After` | Yes |
| Per-recipient rate limit | 429 | `MAILBOX.RECIPIENT_RATE_LIMITED` with `Retry-After` | Yes |
| Lane full and as many messages waiting as it allows | 429 | `MAILBOX.SEND_LIMIT_REACHED` | Later |
| Feature off | 503 | `MAILBOX.TRANSACTIONAL_DISABLED` | No |
| Database or dependency unavailable | 503 | `COMMON.SERVICE_UNAVAILABLE` | Yes |

## Delivery Events

`status` says whether the provider took a message. What happened to it afterwards is recorded as
events, one per recipient:

| Event | Meaning |
|---|---|
| `accepted` | The provider took the message. Recorded once per message, with no recipient. |
| `delivered` | The recipient's mail server accepted the message. |
| `deferred` | A temporary failure; the provider is still retrying. |
| `bounced` | The recipient's mail server refused the message for good. |
| `failed` | The provider gave up without a bounce. |
| `rejected` | The provider did not attempt the recipient, for example a suppressed address. |
| `complained` | The recipient reported the message as spam. |

Each event has `type`, `recipient`, `occurred_at`, and `detail`. `detail` holds short
machine-readable tokens such as `5.1.1 permanent_failure` or `suppressed`. Free-form provider text,
including the SMTP response line, is never stored or returned.

Events come from two places. The send call records `accepted` and whatever the provider reported
for each recipient at once (`delivered`, `bounced`, `rejected`). Later events arrive from the mail
edge through `POST /v1/mail-delivery-events`.

`last_event` is the most severe current state among the recipients, where a recipient's current
state is its latest event: `complained`, then `bounced`, `failed`, `rejected`, `deferred`,
`delivered`. With no recipient event it is `accepted` when the provider took the message. So a
message to two recipients, one delivered and one bounced, shows `bounced`; a recipient deferred
and then delivered counts as `delivered`.

### Webhooks

When the `webhook` starter is selected, an organization's webhook endpoints can subscribe to mail
events. Mailbox mail events (`mail.message.*`) are published only for messages of a shared mailbox whose
[webhook switch](#webhook-switch) is on, and only for messages created at or after its seal
boundary; personal mailboxes never publish. Endpoints, signing, retries, and delivery history are those of [WEBHOOKS.md](WEBHOOKS.md);
this section defines only the mail events. Transactional mail publishes the separate `email.*`
events of [Transactional Mail Events](#transactional-mail-events).

| Event type | Published when |
|---|---|
| `mail.message.sent` | The provider took an outbound message. |
| `mail.message.failed` | The provider refused an outbound message at send time, or later gave up on a recipient without a bounce. |
| `mail.message.received` | An inbound message was stored in a mailbox. |
| `mail.message.delivered` | A recipient's mail server accepted the message. |
| `mail.message.delivery_delayed` | A temporary failure; the provider is still retrying. |
| `mail.message.bounced` | A recipient's mail server refused the message for good. |
| `mail.message.complained` | A recipient reported the message as spam. |
| `mail.message.suppressed` | The provider did not attempt a recipient on its suppression list. |

Every mail event has the same `data` object, with every field always present:

| Field | Value |
|---|---|
| `message_id` | The message's id, as used by the message routes. |
| `mailbox_id` | The mailbox that holds the message. |
| `mailbox_address` | That mailbox's address. |
| `direction` | `outbound` or `inbound`. |
| `from` | The sender address. |
| `to` | The `to` recipients, possibly empty. |
| `recipient` | The address a per-recipient event is about; empty for `sent`, `received`, and a send-time `failed`. |
| `reason` | A short machine-readable code such as `5.1.1 permanent_failure`, `suppressed`, or a send failure code; empty when there is nothing to explain. |

The payload never contains a subject, a body, or free-form provider text. A receiver that needs
the content reads the message through the API with its own credentials.

A webhook is queued in the same database transaction as the state change it reports, so an event
is published exactly when its change is stored, and each change publishes once: a repeated inbound
delivery, a repeated provider event, or an idempotent replay of a send publishes nothing new. A
send to several recipients publishes one `sent` and then one event per recipient outcome.

If the webhook cannot be queued, the mail state is still recorded and the failure is logged; that
one webhook is lost. Mail that was really sent or received is never dropped because of a webhook
failure. Without the `webhook` starter nothing is published.

#### Transactional Mail Events

[Transactional messages](#transactional-mail) publish their own `email.*` events, listed by
`GET /v1/webhook-event-types` with the mail events. A transactional message belongs to no
mailbox, so the mailbox webhook switch and seal boundary do not apply: when the `webhook` starter
is selected, every enabled endpoint of the organization that subscribes to a type receives it.
Test-mode and simulated messages publish too; `mode` and `simulated` tell them apart.

| Event type | Published when |
|---|---|
| `email.sent` | The provider accepted the message (`status` becomes `sent`), or a test-mode or simulated message finished. |
| `email.delivered` | A `delivered` delivery event. |
| `email.bounced` | A `bounced` delivery event (`bounce_type: "permanent"`) or a `deferred` one (`bounce_type: "temporary"`). |
| `email.complained` | A `complained` delivery event. |
| `email.suppressed` | The recipient was on the organization's suppression list at acceptance or before dispatch, or the provider reported it on its own suppression list (at send time or as a `rejected` event). |
| `email.failed` | `status` became `failed` (permanent provider refusal, unknown outcome, retries exhausted, queued too long, sender domain no longer verified, payload unavailable), or a `failed` delivery event (the provider gave up without a bounce). |

Every transactional event has the same `data` object, with every field always present:

| Field | Value |
|---|---|
| `message_id` | The transactional message's id, as used by `GET /v1/transactional-messages/{id}`. |
| `template` | The template key the message was sent with. |
| `to` | The full normalized (trimmed, lowercased) recipient address. |
| `mode` | `live` or `test`. |
| `simulated` | `true` when the message was simulated and never reached the provider. |
| `bounce_type` | `permanent` or `temporary` for `email.bounced`; `""` otherwise. |
| `reason` | A short machine-readable code such as `5.1.1 permanent_failure`, `suppressed`, `simulated`, or a failure code; `""` when there is nothing to explain. |

The payload never contains the subject, the body, or any variable, secret or not. The recipient is
given in full, unlike the masked send log: the receiver is the organization's own signed endpoint,
and only the full address lets it match an outcome to its user.

Each event's id is `t<message id>:<type without "email.">`, followed by `:` and the first 12 bytes
of the SHA-256 of a distinguishing key (hex) when one message can publish that type more than once,
such as one per provider event. The webhook starter deduplicates by
`(organization_id, source, event_id)`, so a repeated provider event or a replayed send publishes
nothing new. A consumer may still receive more than one event of the same type for one message:
for example, an `email.delivered` or `email.suppressed` reported by the provider during the send
and the same outcome reported again by a later provider event carry different event ids. Consumers
that act once per outcome deduplicate on `data.message_id` plus the event type. Events are queued
in the same transaction as the state change they report, with the same loss rule as the mail
events. Signing, delivery history, and retries are those of
[WEBHOOKS.md](WEBHOOKS.md): up to ten attempts, retried after about 1 minute, 5 minutes, 15
minutes, 1 hour, 3 hours, 6 hours, 12 hours, 18 hours, and 24 hours, each with ±20% deterministic
jitter.

### Event Intake

`POST /v1/mail-delivery-events` carries one Cloudflare Email Sending event
(`cf.email.sending.message.*`, schema version 1) as JSON, at most 64 KiB, with
`X-Mailbox-Signature: t=<unix seconds>,v1=<hex>`. `v1` is HMAC-SHA256, keyed with
`MAILBOX_INBOUND_SECRET`, over `<t>.delivery-event.<body>`. The fixed `delivery-event` scope takes
the place of the recipient in the inbound signature, so a signed inbound message cannot be replayed
as an event. The same five-minute window applies.

| Condition | Response | Mail edge action |
|---|---|---|
| Recorded | `201` `{ "message_id", "message_kind", "type", "duplicate": false }` | Acknowledge |
| Event already recorded | `200` with `duplicate: true` | Acknowledge |
| Missing, stale, or wrong signature | `401 MAILBOX.INBOUND_SIGNATURE_INVALID` | Retry |
| No outbound or transactional message has the event's message id | `404 MAILBOX.MESSAGE_NOT_FOUND` | Acknowledge |
| The message was deleted inside the send window | `404 MAILBOX.MESSAGE_NOT_FOUND`; a bounce or complaint still counts toward the personal sending pause of [Send](#send) | Acknowledge |
| Not a usable Email Sending event | `422 MAILBOX.DELIVERY_EVENT_INVALID` | Acknowledge |
| No inbound secret configured | `503 MAILBOX.INBOUND_DISABLED` | Retry |

An event is matched to a message by the provider's message id, which the send call stores as
`message_id`, and is idempotent by the provider's event id. Mailbox messages are searched first,
then transactional messages; `message_kind` (`mailbox` or `transactional`) says which matched, and
`message_id` is that message's id. A transactional message records the event without a recipient
(it has one), updates its `last_event` by the same severity order, and a `bounced` or `complained`
event adds the recipient to the organization's suppression list (see
[Transactional Mail](#transactional-mail)); `rejected` never does. Opens and clicks are not tracked.
Messages sent with the development `log` provider have no provider id and never receive events.

## Inbound Delivery

`POST /v1/inbound-mail-messages` carries one raw RFC 5322 message (`Content-Type: message/rfc822`,
at most 5 MiB) and two headers:

| Header | Value |
|---|---|
| `X-Mailbox-Recipient` | The envelope recipient |
| `X-Mailbox-Signature` | `t=<unix seconds>,v1=<hex>` |

`v1` is HMAC-SHA256, keyed with `MAILBOX_INBOUND_SECRET`, over
`<t>.<lowercase recipient>.<raw body>`. The API compares in constant time and rejects a timestamp
more than five minutes from its clock. The signature is checked before the recipient is looked up,
so an unsigned caller cannot learn which addresses exist.

| Condition | Response | Mail edge action |
|---|---|---|
| Stored | `201` `{ "id", "duplicate": false }` | Accept |
| Same message already stored for this mailbox | `200` `{ "id", "duplicate": true }` | Accept |
| Missing, stale, or wrong signature | `401 MAILBOX.INBOUND_SIGNATURE_INVALID` | Temporary failure |
| No mailbox has the recipient address | `404 MAILBOX.RECIPIENT_NOT_FOUND` | Reject (bounce) |
| Larger than 5 MiB | `413 MAILBOX.INBOUND_TOO_LARGE` | Reject (bounce) |
| Not a parseable message with a sender | `422 MAILBOX.INBOUND_UNPARSEABLE` | Reject (bounce) |
| No inbound secret configured | `503 MAILBOX.INBOUND_DISABLED` | Temporary failure |

The inbound route carries no organization header: the recipient address identifies the mailbox,
and the mailbox its organization. A message is identified by its `Message-ID`, or by a hash of its
content when it has none, once per mailbox. The API stores the first `text/plain` and first `text/html` part, converted to UTF-8 and
truncated to 256 KiB each. Attachments are not stored.

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

### Organization Cloudflare discovery

`POST /v1/mail-cloudflare-previews` accepts an organization manager's candidate `api_token` and returns the active zones visible to that token, with each zone's `id`, `name`, `account_id`, and `account_name`. The operation is read-only and the credential is never stored or returned. An empty list is valid. A successful response verifies visibility only; email routing, DNS writes and sending require their own checks.

Organization owners and administrators can read the masked state with `GET /v1/mail-cloudflare-connection` and bind one zone with `POST /v1/mail-cloudflare-connection` (`api_token`, `zone_id`). The response returns the signing secret once, a generated Worker name and a callback path. The operator's public HTTPS API origin completes the URL. Enable Email Routing on the exact receiving domain and deploy the Worker in its own account. The daily token needs the selected zone's DNS Edit, Zone Read and Email Routing Rules Edit plus the corresponding account's Email Sending Edit.

`POST /v1/mail-cloudflare-connection/{connection_id}/domains` accepts a name and capability. A domain with foreign MX is refused for receiving. DNS setup and checks, exact-address rules and sending use the connection of that domain. The Worker posts signed mail to `/v1/cloudflare-connections/{connection_id}/inbound-mail-messages`. Its organization and the recipient's domain must match the stored mailbox. `DELETE /v1/mail-cloudflare-connection/{connection_id}` clears the credentials and stops later sends and callbacks through this connection. The organization's existing messages and its Cloudflare resources are retained.
