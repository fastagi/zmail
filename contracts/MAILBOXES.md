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
| `member` | List domains, managed zones, and their DNS records; preview a domain; list the mailboxes they have access to; read, send, organize, and delete mail as their access allows; read the log and statistics of the mailboxes they may read |
| `owner`, `admin` | Everything a member may; see every mailbox's metadata; create and delete mailboxes; hand over personal mailboxes and delete sealed history; manage the members and webhook switch of shared mailboxes, including themselves; read every mailbox's access history; connect, set up, check, enable receiving on, and remove domains; remove managed zones; and create, change, and delete their DNS records |

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
| List mailboxes | `GET /api/mailboxes` | `GET /v1/mailboxes` | `200` envelope, at most 50 items |
| Create mailbox | `POST /api/mailboxes` | `POST /v1/mailboxes` | `201` envelope |
| Delete mailbox | `DELETE /api/mailboxes/:id` | `DELETE /v1/mailboxes/:mailbox_id` | `204` |
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
| Send with an API key | none | `POST /v1/outbound-mail-messages` | `201`, or `200` for a repeat |
| Get a sent message with an API key | none | `GET /v1/outbound-mail-messages/:message_id` | `200` envelope |
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
  `noreply`, `postmaster`, `root`, `security`, `support`, `webmaster`) are
  `422 MAILBOX.ADDRESS_RESERVED`.
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
  `daily_transactional_limit` (see [Send](#send)), `broadcasts_enabled`, and `custom_domains_enabled`.
- An address that is or ever was a mailbox is
  `409 MAILBOX.ADDRESS_TAKEN`: deleting a mailbox keeps its address reserved, so a later owner
  never receives mail meant for the previous one.
- An organization owns at most 50 mailboxes; one more is `409 MAILBOX.LIMIT_REACHED`.

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "id": 12,
    "address": "hello@mail.example.com",
    "display_name": "Hello Team",
    "type": "shared",
    "owner_user_id": null,
    "awaiting_handover": false,
    "sealed_before": null,
    "webhooks_enabled": false,
    "access": { "view": false, "send": false, "manage_members": true, "administer": true, "see_history": true, "organize": false },
    "unread_count": 0,
    "created_at": "2026-11-16T10:00:00Z"
  }
}
```

`unread_count` is the number of inbound messages in the default placement (the Inbox) after the seal
boundary that the caller has not read (see [Read State](#read-state)); sent messages and messages that
are archived, in a folder, or in the trash never count. It is `0` for a caller who may not
read the mailbox. `access` is what the caller may do with the mailbox; clients show or hide
controls from it. `awaiting_handover` is `true` while a personal mailbox's owner has left the
organization and nobody has taken it over (see [Departure And Handover](#departure-and-handover)).
`sealed_before` is the seal boundary set at handover, `null` when nothing is sealed.
`webhooks_enabled` is the [webhook switch](#webhook-switch); it is always `false` on a personal
mailbox.

Deleting a mailbox deletes its messages in the same transaction; the address stays reserved and
later mail for it is answered `404 MAILBOX.RECIPIENT_NOT_FOUND`. A mailbox on a connected domain
also loses its routing rule; if Cloudflare refuses to delete the rule, the deletion still succeeds
and mail for the address is answered `404` as above. Deleting a user account does not
touch mailboxes: the organization starter already refuses to delete an account that still belongs
to an organization.

## Access

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
a member sees a mailbox listed but gets `403` when opening its mail.

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

### Read State

Read state is per person. `read` on a message and `unread_count` on a mailbox are the caller's own:
marking a message read or unread with `PUT .../messages/:message_id` needs `view` and changes it
only for the caller. Stars work the same way: `starred` is the caller's own, starring needs `view`,
and nobody else sees it. Placement, folder, labels, and the trash are shared by everyone with access. Outbound messages are always `read: true` for everyone, and marking one changes
nothing. Reading mail with an API key never changes read state. A person who loses access to a
mailbox (removed as a member, handed over as the former owner, or gone from the organization) loses
their read records and stars for it, so access granted again starts with everything unread and
nothing starred.

Only owners and admins create mailboxes. At creation, a personal mailbox needs `owner_user_id` and
no `members`; a shared one needs 1 to 100 `members`, each a different `user_id` with `view`,
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
| `handed_over` | An admin hands the mailbox over | The successor | `{ "former_owner_user_id", "sealed_before" }` |
| `sealed_deleted` | An admin deletes the sealed history | `null` | `{ "deleted_messages", "deleted_broadcasts" }` |
| `webhooks_changed` | The webhook switch changes | `null` | `{ "enabled" }` |

`actor_user_id` is `null` for a change Zmail made itself, such as `owner_left`, `owner_returned`,
and `member_removed` for a departure. `detail` holds ids, permissions, timestamps, and counts only,
never message content. Creating a shared mailbox writes `created` and one `member_set` per first
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
  `pending` with `last_error`, and `POST /v1/mail-domains/:domain_id/setups` starts every step again
  safely. One setup runs per domain at a time; a check while it runs returns the domain unchanged,
  and a setup or receiving request while it runs makes it run once more afterwards, from the domain
  as stored then. A failed zone lookup while connecting is `502 MAILBOX.DOMAIN_PROVIDER_FAILED`.
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
- Removing a domain that a mailbox still uses is `409 MAILBOX.DOMAIN_IN_USE`. Removing forgets the
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
- `subject` is required, at most 998 characters, with no line breaks.
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

A send is never reported as successful before the provider accepts it.

The send limit is split into three lanes. Each lane is counted separately per organization over a
rolling 24 hours by the instants provider calls started, failed attempts count toward it, and a
full lane never consumes another lane's room. Every message belongs to exactly one lane, fixed when
it is stored:

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
body, at least one of which is not empty. Every member reads and edits the organization's
templates; an organization keeps at most 200, and names are unique within it.

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

### Contacts

A contact is one address an organization sends broadcasts to: `email`, `first_name`, `last_name`,
up to 50 string `properties`, a `status`, and the lists it belongs to. An organization keeps at most
100000 contacts, one per address. Every member reads and edits them.

| Field | Rule |
|---|---|
| `email` | A bare address, lowercased. Set when the contact is created and never changed. |
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
- Only owners and admins send, cancel, pause, and resume broadcasts; a member who tries is
  `403 PERMISSION.DENIED`. Sending and resuming also need `send` on the sender mailbox at that
  call; the worker does not check access again, so a broadcast already sending keeps sending when
  that access is later removed. Cancelling and pausing need no access to the sender mailbox, so an
  admin can always stop mail; without `view` on it (or for a sealed broadcast) the answer is
  `204` with no body, so nothing about the broadcast is revealed.

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
| `sent` | No recipient is pending. |
| `canceled` | Stopped; recipients not yet sent became `canceled`. |

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
- If the template does not use `{{unsubscribe_url}}`, a small link with `unsubscribe_label` (or
  `Unsubscribe`) is added at the end of the HTML body and the text body.
- Every message carries `List-Unsubscribe` and `List-Unsubscribe-Post: List-Unsubscribe=One-Click`,
  so mail clients can offer one-click unsubscribe.
- A contact that is no longer `subscribed` when its turn comes is `skipped`
  (`contact_not_subscribed`); a deleted contact is `skipped` (`contact_deleted`).
- Each recipient is sent with an idempotency key of its broadcast and contact, so a worker that stops
  after the provider call never sends that contact a second message.
- When the broadcast lane limit (`daily_broadcast_limit`) is reached, the broadcast keeps `sending`
  with `stop_reason` `send_limit_reached` and continues when the 24-hour window has room. Personal
  and transactional sends never use the broadcast lane's room, and a broadcast never uses theirs.

The broadcast reports `counts` of its recipients by status. `GET
/v1/mail-broadcasts/{id}/recipients` pages them, with an optional `status` filter, each with the
stored `message_id` and a `failure_code`.

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
`complained`. Later broadcasts skip it.

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

## Programmatic Sending

Backend services send with an API key instead of a browser session. Create the key with
`POST /v1/api-keys` (see [`API_KEYS.md`](API_KEYS.md)) and pass it as `X-API-Key`, together with
the `Organization-Id` header. A key belongs to a user, and it acts with that user's current
membership of the named organization: the same header errors apply, and removing the user from the
organization stops the key from working there on the next request. Keys owned by an organization
are not defined.

| Route | Required scope |
|---|---|
| `POST /v1/outbound-mail-messages` | `mail:send` |
| `GET /v1/outbound-mail-messages/:message_id` | `mail:read` |

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
events. Mail events are published only for messages of a shared mailbox whose
[webhook switch](#webhook-switch) is on, and only for messages created at or after its seal
boundary; personal mailboxes never publish. Endpoints, signing, retries, and delivery history are those of [WEBHOOKS.md](WEBHOOKS.md);
this section defines only the mail events.

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

### Event Intake

`POST /v1/mail-delivery-events` carries one Cloudflare Email Sending event
(`cf.email.sending.message.*`, schema version 1) as JSON, at most 64 KiB, with
`X-Mailbox-Signature: t=<unix seconds>,v1=<hex>`. `v1` is HMAC-SHA256, keyed with
`MAILBOX_INBOUND_SECRET`, over `<t>.delivery-event.<body>`. The fixed `delivery-event` scope takes
the place of the recipient in the inbound signature, so a signed inbound message cannot be replayed
as an event. The same five-minute window applies.

| Condition | Response | Mail edge action |
|---|---|---|
| Recorded | `201` `{ "message_id", "type", "duplicate": false }` | Acknowledge |
| Event already recorded | `200` with `duplicate: true` | Acknowledge |
| Missing, stale, or wrong signature | `401 MAILBOX.INBOUND_SIGNATURE_INVALID` | Retry |
| No outbound message has the event's message id | `404 MAILBOX.MESSAGE_NOT_FOUND` | Acknowledge |
| Not a usable Email Sending event | `422 MAILBOX.DELIVERY_EVENT_INVALID` | Acknowledge |
| No inbound secret configured | `503 MAILBOX.INBOUND_DISABLED` | Retry |

An event is matched to a message by the provider's message id, which the send call stores as
`message_id`, and is idempotent by the provider's event id. Opens and clicks are not tracked.
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
| `MAILBOX.ACCESS_CONFLICT` | 409 |
| `MAILBOX.ACCESS_INVALID` | 422 |
| `MAILBOX.ADDRESS_INVALID` | 422 |
| `MAILBOX.ADDRESS_RESERVED` | 422 |
| `MAILBOX.MESSAGE_INVALID` | 422 |
| `MAILBOX.IDEMPOTENCY_CONFLICT` | 409 |
| `MAILBOX.SEND_IN_PROGRESS` | 409 |
| `MAILBOX.IDEMPOTENCY_KEY_INVALID` | 422 |
| `MAILBOX.SEND_LIMIT_REACHED` | 429 |
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
| `MAILBOX.ZONE_NOT_FOUND` | 404 |
| `MAILBOX.ZONE_PENDING` | 409 |
| `MAILBOX.ZONE_IN_USE` | 409 |
| `MAILBOX.ZONE_STILL_DELEGATED` | 409 |
| `MAILBOX.ZONE_LIMIT_REACHED` | 422 |
| `MAILBOX.DNS_RECORD_NOT_FOUND` | 404 |
| `MAILBOX.DNS_RECORD_LOCKED` | 409 |
| `MAILBOX.DNS_RECORD_INVALID` | 422 |
| `MAILBOX.DNS_RECORD_REJECTED` | 422 |
