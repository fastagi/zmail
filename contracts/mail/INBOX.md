# Mail Inbox Contract

Part of the [Mailbox Contract](../MAILBOXES.md). Defines mailboxes, access control, mail roles, messages, threads, folders/labels, attachments, unified inbox, drafts, signatures, idempotency, masked secrets, and member feature grants. See [`../MAILBOXES.md`](../MAILBOXES.md) for activation, ownership, route overview, and the master error catalog.

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
  `simulator_domain` (see [Transactional Mail](DELIVERY.md#transactional-mail)), `broadcasts_enabled`, and
  `custom_domains_enabled`.
- Exactly one item has `default: true`: the organization's default domain (see Domains), or the
  shared domain while the organization has no verified domain. Forms that create mailboxes
  preselect it; it never changes what an empty `domain` means.
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
mailbox. `ai_triage_enabled` is the mailbox's [AI triage](TRIAGE_AND_AGENTS.md#ai-triage) switch. `role_id` is the
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

## Access

A mailbox has two independent dimensions. Its `type` says who may use it, and its `origin` says
where its mail comes from:

- `origin` `zmail` mailboxes receive and send at a Zmail address; every mailbox created through
  `POST /v1/mailboxes` is one.
- `origin` `connected` mailboxes hold copies of an outside account (see
  [External Mailboxes](EXTERNAL_MAIL.md#external-mailboxes)). Whatever the type and permission, nobody gets
  `organize`, and nobody sends from them unless the account was bound with its SMTP server (see
  [Sending From A Connected Mailbox](EXTERNAL_MAIL.md#sending-from-a-connected-mailbox)); their address never takes delivery, never blocks a Zmail address, and
  does not count toward the organization's mailbox limit. They are never created through
  `POST /v1/mailboxes`. A connected `personal` mailbox is its owner's own account: only the owner
  sees it, with `view` and `see_history`, and the owner and admin roles get nothing on it, so it is
  absent from their mailbox lists, the directory and its `mailbox_count`, the export, the unified
  inbox and its counts, and every route of it answers them `404`. Owners and admins see connected
  accounts only in the [connection directory](EXTERNAL_MAIL.md#connection-directory), with the address masked.
  Domain receiving and domain deletion ignore connected mailboxes too: an address on an
  organization domain gets no routing rule because of one, and never keeps the domain from being
  deleted. Operator mailbox counts count Zmail mailboxes only.
- A connected `personal` mailbox **assigned from the organization's tenant** (see
  [Tenant Connections](EXTERNAL_MAIL.md#tenant-connections)) is at the organization's own address, so the owner and
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
| `owner_assigned` | The person a [roster import](GATEWAY.md#roster-import) reserved the mailbox for joins | The new owner | `{}` |
| `handed_over` | An admin hands the mailbox over | The successor | `{ "former_owner_user_id", "sealed_before" }` |
| `sealed_deleted` | An admin deletes the sealed history | `null` | `{ "deleted_messages", "deleted_broadcasts" }` |
| `webhooks_changed` | The webhook switch changes | `null` | `{ "enabled" }` |
| `renamed` | An admin changes the display name | `null` | `{ "from", "to" }` |
| `connection_bound` | A member binds an outside account into a new connected mailbox | The owner | `{ "connection_id", "provider" }` |
| `connection_reconnected` | The owner signs in to the account again | The owner | `{ "connection_id", "provider" }` |
| `connection_revoked` | The owner unbinds the account | The owner | `{ "connection_id", "provider" }` |
| `connection_force_disconnected` | An owner or admin disconnects another member's account | The owner | `{ "connection_id", "provider" }` |
| `connection_revoked_on_leave` | The owner leaves the organization | The owner | `{ "connection_id", "provider" }` |
| `agent_granted` | An [agent grant](TRIAGE_AND_AGENTS.md#agent-grants) is issued | The key's owner | `{ "grant_id", "api_key_id", "capabilities", "reveal_secrets", "expires_at", "sender_filters", "label_filters" }` (filter counts) |
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

A personal mailbox [assigned from a tenant](EXTERNAL_MAIL.md#tenant-connections) follows these rules too, because its
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
[Tenant Connections](EXTERNAL_MAIL.md#tenant-connections)).

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
[webhooks](DELIVERY.md#webhooks) for a shared mailbox on or off and answers `200` with the mailbox. It needs
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
| `bcc` | Blind copies of an outbound message, only on the detail the sending member reads in the console (`GET` of one message or its thread). Absent for everyone else, in listings, on API-key and agent routes, and in mail app sources. |
| `read` | Whether the caller has read the message; see [Read State](#read-state). Outbound messages are always read. |
| `failure_code` | Stable provider or transport reason when `status` is `failed`, otherwise empty |
| `last_event` | Summary of the delivery events of an outbound message; see [Delivery Events](DELIVERY.md#delivery-events). Empty when none are recorded. |
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
  `text`, and `html`, which must then be absent. See [Templates](DELIVERY.md#templates).
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
| Broadcast | Each broadcast recipient | `daily_broadcast_limit` | The broadcast pauses (see [Broadcasts](DELIVERY.md#broadcasts)) |
| Transactional | Live, non-simulated transactional messages | `daily_transactional_limit` | The message is queued |

A `202` message waits; nothing about the limit's usage is in the answer, and the Web console tells
everyone the same thing: the limit is full and the message is queued. The `mail:broadcast:work`
worker sends waiting messages oldest first, per organization, as the rolling window gains room,
and records each outcome exactly like a direct send (`sent`, or `failed` with its `failure_code`,
and the same webhooks). No webhook is published when a message starts waiting. A waiting message
moved to the trash is never sent (see [Placements](#placements)); deleting its mailbox deletes it too. One
that a handover seals is not sent: it becomes `failed` with `failure_code` `send.canceled`. Broadcasts
never wait this way; they pause as described in [Broadcasts](DELIVERY.md#broadcasts). This applies to both
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
| `triage` | Comma-separated distinct [triage](TRIAGE_AND_AGENTS.md#ai-triage) categories, such as `reply,review`; only with `view` `all` or `today`, and never with `group` or `q`. Matches nothing while the organization's AI triage is off. |
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
caller's own. `triage` is the message's [triage result](TRIAGE_AND_AGENTS.md#ai-triage) or `null`. There is no body,
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

An organization grants each `member` six features (see
[`ORGANIZATIONS.md`](../ORGANIZATIONS.md#member-feature-grants)); owners and admins always have all of
them. A route that needs a feature the active membership lacks returns
`403 ORGANIZATION.FEATURE_NOT_GRANTED` before anything else is checked.

| Feature | Routes |
|---|---|
| `broadcasts` | Every `/v1/mail-broadcasts` route |
| `contacts` | Every `/v1/mail-contacts` route, `POST /v1/mail-contact-imports`, and writes to `/v1/mail-contact-lists` |
| `contacts` or `broadcasts` | `GET /v1/mail-contact-lists` |
| `templates` | `POST /v1/mail-templates/:template_id/tests` |
| `templates` or `developer` | `POST`, `PUT`, and `DELETE` on `/v1/mail-templates` (a developer writes only transactional templates inside the scope; see [Developer access](DELIVERY.md#developer-access)) |
| `templates`, `broadcasts`, or `api_keys` | `GET /v1/mail-templates`, `GET /v1/mail-templates/:template_id`, `POST /v1/mail-template-previews` |
| `api_keys` | Every route authenticated by `X-API-Key` under [Programmatic Sending](DELIVERY.md#programmatic-sending), [Agent Grants](TRIAGE_AND_AGENTS.md#agent-grants), and [Transactional Mail](DELIVERY.md#transactional-mail) |
| `developer` | Also grants `api_keys`; adds [Developer access](DELIVERY.md#developer-access) on top of the routes above |
| `mail_clients` | `POST /v1/mail-app-passwords`, and signing in from a mail app (see [Mail Client Access](GATEWAY.md#mail-client-access)) |
