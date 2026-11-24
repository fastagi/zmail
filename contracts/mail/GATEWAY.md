# Mail Gateway And Directory Contract

Part of the [Mailbox Contract](../MAILBOXES.md). Defines IMAP/SMTP mail client access, app passwords, client settings, mailbox directory, mailbox import/export, roster import, member welcome mail, and platform accounts. See [`../MAILBOXES.md`](../MAILBOXES.md) for activation, ownership, route overview, and the master error catalog.

## Mail Client Access

Members can read their mail in a desktop or phone mail app (Apple Mail, Outlook, Thunderbird, and
others) over IMAP and send from it over SMTP submission. A mail app never signs in with the account password: each device gets its own
mail client password, which the member creates in the console, sees once, and can revoke on its
own. These are unrelated to the outside provider's app password used under
[Binding With An App Password](EXTERNAL_MAIL.md#binding-with-an-app-password).

### Mail Client Passwords

`POST /v1/mail-app-passwords` (session) creates one for the caller in the active organization:

```json
{ "name": "Alice's iPhone", "scopes": ["imap"], "current_password": "...", "totp_code": "123456" }
```

| Field | Rule |
|---|---|
| `name` | Required, 1 to 64 characters after trimming, the device's name |
| `scopes` | Required, no repeats: `imap` to read, `smtp` to send (see [SMTP Submission](#smtp-submission)). The console offers both by default |
| `current_password` | Required, the caller's account password |
| `totp_code` | Required when the account has two-factor sign-in on: an authenticator or recovery code |

A mail client password bypasses two-factor sign-in, so creating one asks for the account
credentials again. A wrong or missing password or code is `422 COMMON.VALIDATION_FAILED` with an
error on `current_password` or `totp_code` (never `401`, which would read as an ended session).
These checks share a budget of 5 failures per user per 15 minutes; past it the route answers
`429 COMMON.RATE_LIMITED` with `Retry-After`. The body is at most 2 KiB.

It answers `201` with the password and, only in this response, its `secret`:

```json
{
  "id": 12, "name": "Alice's iPhone", "scopes": ["imap"], "hint": "mnop",
  "created_at": "2026-12-24T09:00:00Z", "last_used_at": null, "last_used_protocol": null,
  "secret": "abcd efgh ijkl mnop"
}
```

- `secret` is 16 random lowercase letters in four groups. Mail apps may send it with or without the
  spaces and in any case. The API keeps only a keyed hash of it (HMAC-SHA256 with the instance's
  `MAIL_APP_PASSWORD_PEPPER`); nobody can read it again, and no other response carries it.
  Without a configured pepper creating answers `503 COMMON.SERVICE_UNAVAILABLE`.
- `hint` is the secret's last four letters, to tell devices apart.
- A member holds at most 20 passwords that are not revoked per organization; the next is
  `409 MAILBOX.APP_PASSWORD_LIMIT_REACHED`.
- Creating needs the `mail_clients` feature (`403 ORGANIZATION.FEATURE_NOT_GRANTED`) and the
  organization switch on (`403 MAILBOX.MAIL_CLIENT_ACCESS_DISABLED`), checked in that order before
  the credentials.

`GET /v1/mail-app-passwords` lists the caller's own passwords that are not revoked, newest first,
without secrets, and tells the console whether mail apps can sign in and where:

```json
{
  "items": [{ "id": 12, "name": "Alice's iPhone", "scopes": ["imap"], "hint": "mnop",
    "created_at": "2026-12-24T09:00:00Z", "last_used_at": "2026-12-24T10:00:00Z", "last_used_protocol": "imap" }],
  "limit": 20,
  "access": { "enabled": false, "reason": "organization_disabled" },
  "server": { "imap_host": "imap.zmail.im", "imap_port": 993, "imap_security": "ssl_tls",
    "smtp_host": "smtp.zmail.im", "smtp_port": 465, "smtp_security": "ssl_tls", "smtp_starttls_port": 587 }
}
```

`access.reason` is `unavailable` when the instance has no pepper configured,
`organization_disabled` when the organization switch is off, `feature_not_granted` when the
membership lacks `mail_clients`, and `null` while `enabled` is `true`, checked in that order. Listing and revoking work without the feature, so a member can always remove a device.
`server` comes from `MAIL_GATEWAY_PUBLIC_IMAP_HOST`, `MAIL_GATEWAY_PUBLIC_IMAP_PORT`,
`MAIL_GATEWAY_PUBLIC_SMTP_HOST`, `MAIL_GATEWAY_PUBLIC_SMTP_PORT`, and
`MAIL_GATEWAY_PUBLIC_SUBMISSION_PORT`.

`DELETE /v1/mail-app-passwords/:password_id` revokes one of the caller's own passwords at once and
answers `204`; anything else, including another member's password or one already revoked, is
`404 MAILBOX.APP_PASSWORD_NOT_FOUND`.

Owners and admins see every password of the organization with `GET /v1/mail-app-password-directory`
(`page`, `per_page` up to 100), newest first. Each item adds the owner's `membership_id`, `user_id`,
`user_email`, and `user_name`, plus `revoked_at` and `revoke_reason` (`user` or `admin`) for
passwords revoked in the last 90 days. It never carries a secret or anything about mail.
`DELETE /v1/mail-app-password-directory/:password_id` revokes a member's password with
`revoke_reason` `admin` and answers `204`; an id outside the organization or already revoked is
`404 MAILBOX.APP_PASSWORD_NOT_FOUND`. Members get `403 PERMISSION.DENIED` on both.

Creating and revoking write a user audit change (see [`AUDIT.md`](../AUDIT.md)) with resource
`mail_app_passwords`, action `create` or `delete`, the password id as target, and metadata holding
only `name_length`, `scopes`, and `by_admin`. Revoked passwords are deleted 90 days
after revocation; leaving the organization deletes the member's passwords with the membership.

### Organization Switch

`GET /v1/mail-client-settings` answers `{ "access_enabled": true, "updated_at": null }` to any
member; `updated_at` is `null` until an owner or admin saves the switch, and an organization that
never saved it has mail apps allowed. `PUT /v1/mail-client-settings` with
`{ "access_enabled": false }` replaces it (owners and admins; `403 PERMISSION.DENIED` otherwise).
Turning it off keeps every password but none can sign in; turning it on again restores them.

### Signing In

A mail app signs in with a full mailbox address as the user name (or the account email) and a mail
client password. Sign-in succeeds only when the password is not revoked, has the protocol's scope,
the organization switch is on, the membership still exists and has `mail_clients`, and the address
names a mailbox the member may `view` (see [Access](INBOX.md#access)). Every failure looks the same to the
mail app. Revoking a password, turning the switch off, withdrawing the feature, or removing the
member ends that device's open sessions within 60 seconds.

### IMAP Folders

After signing in, a mail app sees the mailboxes the member may `view`, read-only in this release.
The mailbox named by the user name is the root; every other shared mailbox is under
`Shared/<address>/`. Connected mailbox copies (`origin` `connected`) are not offered.

| IMAP folder | Messages |
|---|---|
| `INBOX` | Inbound messages in the `default` placement |
| `Sent` (`\Sent`) | Outbound messages in the `default` placement that did not fail |
| `Archive` (`\Archive`) | The `archive` placement |
| `Trash` (`\Trash`) | The `trash` placement |
| The folder's name | The `folder` placement of that folder; `/` in a name shows as `∕`, and a name that collides with another folder gets ` (1)` |

Each message has a UID that grows in the order it entered the folder; moving a message out and
back gives it a new UID. A folder's UIDVALIDITY changes only when the folder is created again. A
mail app can set and clear `\Seen` and `\Flagged`, which are the reader's own read and starred
state from [Read State](INBOX.md#read-state). Creating, renaming, or deleting folders, appending, copying,
moving, and expunging are refused.

### Message Sources

A mail app downloads each message as RFC 5322. Inbound mail delivered after this release keeps its
raw message byte for byte, at most 5 MiB, deleted with the message. Older mail and outbound mail
are rebuilt from the stored fields: `From`, `To`, `Cc`, `Subject`, `Date` (when Zmail stored it),
`Message-ID`, `In-Reply-To`, `References`, the text and HTML bodies, and the attached files, with
the header `X-Zmail-Reconstructed: 1`; `Reply-To`, the original `Date`, signatures, and inline image
references are lost. Mail apps receive the source unmasked: one-time codes and sign-in links are
not masked as in [Masked Secrets](INBOX.md#masked-secrets). An organization that must not let codes reach
devices turns the switch off.

### SMTP Submission

A mail app sends over SMTP submission: port 465 with TLS from the start, or port 587 with
`STARTTLS` (authentication is neither offered nor accepted before TLS). It signs in with `AUTH
PLAIN` or `AUTH LOGIN`, a full mailbox address or the account email, and a mail client password
with the `smtp` scope; sign-in failures share their budget and locks with IMAP. A connection that
has not signed in within 60 seconds, or sends nothing for 5 minutes, is closed. The password is
checked again before each message; a revoked one gets `421 4.7.0` and the connection ends.

- **Sender.** The `From` header picks the mailbox: a Zmail mailbox of the organization the member
  may `send` from (see [Access](INBOX.md#access)). `MAIL FROM` must be such a mailbox too. Anything else,
  a missing or multi-address `From`, `MAIL FROM:<>`, and connected mailboxes are refused with
  `550 5.7.1`. The message goes out with the mailbox's own display name, whatever name the app wrote.
- **Recipients.** The envelope decides who receives it. `To` and `Cc` keep the header addresses the
  envelope names; every other envelope recipient is a blind copy, delivered and never written into a
  header, and stored as `bcc`. At most 50 different recipients (`452 4.5.3` from the 51st);
  repeating one counts once; an address that is not valid is `501 5.1.3`. A message with no `To`
  recipient (only `Cc` or blind copies) is `554 5.6.0`.
- **Content.** At most 8 MiB (`SIZE 8388608`; larger is `552 5.3.4`). The limits of
  [Send](INBOX.md#send) apply and are refused, never cut: a subject over 998 characters, a text or
  HTML body over 256 KiB, more than 10 attachments, or attachments over 4 MiB in total answer
  `552 5.3.4` naming the limit. A message without a subject is sent as `(no subject)`; one with only
  files gets a blank body. Images of a `multipart/related` body keep their `Content-ID` and stay
  inline. Zmail gives the message its own `Message-ID` and does not keep the submitted bytes.
- **Retries.** A message is identified by its mailbox and `Message-ID` (by its exact bytes when it
  has none). Submitting it again answers the first outcome without sending twice; a retry after a
  failed attempt sends it again, and a different message with the same `Message-ID` is `554 5.6.0`.
- **Send limit.** It counts in the personal lane with console sends. Past the limit the message
  waits like a console send and the app hears `250 2.0.0 Queued`; when the waiting queue is full,
  `452 4.3.1`.

| Outcome | Reply |
|---|---|
| Sent | `250 2.0.0 Ok: sent, id <message id>` |
| Waiting for the send limit | `250 2.0.0 Queued: ...` |
| Send limit and queue full | `452 4.3.1` |
| Sending paused by the bounce or complaint rate | `550 5.7.1` |
| Provider refused or unreachable | `451 4.4.1` |
| An earlier attempt still in flight | `451 4.4.1` |
| No outbound provider configured | `451 4.3.5` |
| Invalid message | `554 5.6.0` with the reason |
| Server error | `451 4.3.0` |

## Mailbox Directory

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
included: the same predicate as the personal lane of [Lane Usage](DELIVERY.md#lane-usage). In `meta`,
`daily_send_limit` is that lane's limit and `sent_last_24h` the organization's attempts in it, the
figure the limit is checked against. Deleting sent mail for good, one message, the trash, sealed
history, or a whole mailbox, frees no room: its attempts still count in both figures until they
leave the window, and so do the bounces and complaints they drew in the sending-health check. A
deleted mailbox has no row, so the rows of every page together add up to `sent_last_24h` less the
attempts of mailboxes deleted in the window. `mailbox_count` counts the
organization's live mailboxes and `max_mailboxes` is `MAILBOX_MAX_PER_ORGANIZATION`. Entries carry no
message content, unread count, or access.

## Import And Export

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

## Roster Import

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
- `features.developer`, or a row's `developer: true` (the roster's `developer=yes` column), gives a
  `member` row developer access on joining, scoped to the organization's default mail domain at
  accept time (see [Developer access](DELIVERY.md#developer-access)); it is ignored for an `admin` row.
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
    "welcome_mails_queued": 1, "welcome_mails_failed": 0,
    "rows": [
      { "row": 1, "email": "alice@gmail.com", "address": "alice@company.com", "status": "invited", "skip_reason": null, "error_code": null, "mailbox_id": 51, "invitation_id": 12, "email_send_status": "accepted_by_provider", "welcome_mail_status": "queued", "welcome_mail_error_code": null },
      { "row": 2, "email": "sam@gmail.com", "address": "sales@company.com", "status": "skipped", "skip_reason": "mailbox_taken", "error_code": null, "mailbox_id": null, "invitation_id": null, "email_send_status": null, "welcome_mail_status": null, "welcome_mail_error_code": null },
      { "row": 3, "email": "not-an-email", "address": "", "status": "failed", "skip_reason": null, "error_code": "MAILBOX.IMPORT_EMAIL_INVALID", "mailbox_id": null, "invitation_id": null, "email_send_status": null, "welcome_mail_status": null, "welcome_mail_error_code": null }
    ]
  }
}
```

`status` is `valid` (dry run only), `invited`, `created`, `skipped` (with `skip_reason`
`already_member`, `already_invited`, or `mailbox_taken`), or `failed` (with `error_code`).
`email_send_status` is the invitation email outcome (`accepted_by_provider`, `failed`, or
`not_configured`) when the row sent one. The invitation link is never returned here; a manager
shares one through a resend. `welcome_mail_status` is the row's [welcome mail](#member-welcome-mail)
outcome, set only outside a dry run on a row that is `invited`, `created`, or skipped as
`already_invited` (finishing a welcome an earlier upload could not send); otherwise null.
`welcome_mail_error_code` is set only when it is `failed`. A welcome mail never changes a row's
`status` or its counts: `welcome_mails_queued` counts rows whose welcome was queued by this
request and `welcome_mails_failed` rows whose welcome is `failed` or `suppressed`. Row error codes:

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

## Member Welcome Mail

When a member's personal mailbox is provisioned on a verified organization domain, Zmail sends that
person one welcome mail: how to sign in and how to set up a mail app, with the public mail app
server (`MAIL_GATEWAY_PUBLIC_IMAP_HOST`/`PORT`, `MAIL_GATEWAY_PUBLIC_SMTP_HOST`/`PORT`, and
`MAIL_GATEWAY_PUBLIC_SUBMISSION_PORT`) and a link to the console's mail app settings. It never
contains a password, a mail app password, an invitation token, or any other secret.

| Provisioned by | Recipient |
|---|---|
| A roster import row that is `invited`, `created`, or skipped as `already_invited` | The row's `email`: the invitation address the person reads until they first sign in |
| An owner or admin creating a personal mailbox for another member (`POST /v1/mailboxes`) | That member's account email |

The new mailbox itself is never the recipient: the person cannot read it before signing in. A
manager creating a mailbox for themselves, a mailbox on the shared domain, and a connected mailbox
send no welcome mail.

Each mailbox is welcomed automatically at most once, ever: the welcome is recorded with the mailbox
in the same transaction that stores the message, so uploading a roster again or retrying a row
answers `already_sent` and sends nothing. Only an owner or admin sends one again, explicitly (see
[Resending A Welcome Mail](#resending-a-welcome-mail)). Welcome mails are [transactional messages](DELIVERY.md#transactional-mail)
created by Zmail itself (no API key, `mode` `live`), sent from `no-reply@<mailbox domain>` under the
organization's name: they are stored first and sent off the request path with the usual retries,
count in the transactional lane, honor the suppression list and the simulator domain, and appear in
the [send log](DELIVERY.md#send-log) with `template` `member-welcome` and `idempotency_key`
`member-welcome:<mailbox id>`. The `MAILBOX_TRANSACTIONAL_ENABLED` switch governs only API sends;
welcome mails need `MAILBOX_SECRET_ENCRYPTION_KEY` and `MAILBOX_PUBLIC_URL`, and without either the
outcome is `not_configured`.

| Outcome (`welcome_mail_status`) | Meaning |
|---|---|
| `queued` | Stored and sent, or waiting for the lane; see the send log for delivery |
| `suppressed` | Stored as `suppressed`: the recipient is on the suppression list |
| `already_sent` | This mailbox was welcomed before; nothing new was stored |
| `disabled` | The organization turned welcome mails off |
| `not_configured` | This instance cannot send welcome mails |
| `failed` | Nothing was stored; `welcome_mail_error_code` says why (for example `MAILBOX.DOMAIN_NOT_VERIFIED`, `MAILBOX.SEND_LIMIT_REACHED`, `COMMON.SERVICE_UNAVAILABLE`), and a later upload of the row tries again |

### Welcome Mail Setting

Only owners and admins read and change the setting (`403 PERMISSION.DENIED` otherwise).
`GET /v1/member-welcome-mail-setting` answers:

```json
{
  "enabled": true,
  "language": "en-US",
  "subject": "Welcome to {{organization_name}} on Zmail",
  "body": "Hi {{member_name}},\n\n…",
  "customized": false,
  "variables": ["member_name", "mailbox_address", "organization_name", "sign_in_url"],
  "defaults": {
    "en-US": { "subject": "Welcome to {{organization_name}} on Zmail", "body": "Hi {{member_name}},\n\n…" },
    "zh-Hans": { "subject": "…", "body": "…" }
  },
  "updated_at": null
}
```

An organization that never saved the setting has welcome mails on, in `zh-Hans`, with the standard
text. `subject` and `body` are the copy in use: the saved copy, or the standard text of `language`
where none was saved (`customized` says whether any was). `defaults` is the standard text of each
language. `PUT` takes `{ "enabled", "language", "subject", "body" }`:

- `enabled` (boolean) and `language` (`en-US` or `zh-Hans`) are required.
- `subject` is one line of at most 200 characters, `body` plain text of at most 5000; both are
  trimmed, and an empty one, or one equal to the standard text of `language`, means the standard
  text, so it follows a later language change.
- Both may use `{{variable}}` and `{{variable|fallback}}` with only the variables listed:
  `member_name` (the roster name or mailbox display name, else the mailbox address),
  `mailbox_address`, `organization_name`, and `sign_in_url` (`<MAILBOX_PUBLIC_URL>/login`).
  An unknown variable, a malformed placeholder, or a value over a limit is
  `422 COMMON.VALIDATION_FAILED` with `errors.subject` or `errors.body`.

Every welcome mail ends with a fixed mail app section in `language`, after the editable body; it
cannot be removed, so the server names are always right. The HTML part is the plain text escaped,
with `sign_in_url` and the settings link made clickable. Saving is audited as
`member_welcome_settings.update` with `enabled`, `language`, and `customized`.

`POST /v1/member-welcome-mail-previews` takes `{ "language", "subject", "body" }` with the same rules
and answers `{ "subject", "text", "html" }`: the whole mail, fixed section included, rendered with
sample values and the organization's name. It stores and sends nothing.

### Welcome Mail Status

`GET /v1/member-welcome-mails` lists, for owners and admins only (`403 PERMISSION.DENIED`
otherwise), every mailbox that can be welcomed today, oldest first, with its latest welcome. A
mailbox can be welcomed when it is a Zmail `personal` mailbox on an organization domain (not the
shared domain) that is either reserved for an invitee or owned by a current member other than the
caller; a mailbox awaiting handover, a connected mailbox, and the caller's own mailbox are absent.
The list holds at most the organization's mailbox ceiling and is not paginated.

```json
[
  {
    "mailbox_id": 51,
    "address": "alice@acme.example",
    "display_name": "Alice",
    "owner_user_id": 902,
    "invitation_pending": false,
    "status": "failed",
    "failure_code": "transport.permanent_bounce",
    "queued_at": "2026-11-23T08:00:00Z",
    "sent_at": null,
    "resend_count": 1
  }
]
```

| `status` | Meaning |
|---|---|
| `not_sent` | No welcome mail was ever stored for the mailbox: it predates welcome mails, they were off or not configured, or every try failed before storing |
| `queued` | The latest welcome mail waits to be sent |
| `sent` | The provider accepted the latest welcome mail at `sent_at` |
| `failed` | Sending the latest welcome mail failed for good; `failure_code` is its send log failure code, such as `transport.permanent_bounce` or `send.sender_unverified` |
| `suppressed` | The latest welcome mail was not sent: its recipient is on the suppression list |
| `expired` | The latest welcome mail went out, but its send log entry is past retention |

`invitation_pending` is true for a mailbox reserved for an invitee who has not joined yet
(`owner_user_id` is then null). `queued_at` is when the latest welcome mail was stored, null for
`not_sent`; `sent_at` is set only for `sent`; `failure_code` only for `failed`. `resend_count`
counts the explicit resends. The list never carries a recipient address.

### Resending A Welcome Mail

`POST /v1/member-welcome-mails` with `{ "mailbox_id": 51 }` sends that mailbox's welcome mail
again, with the current setting's copy and language. It answers `201` with the mailbox's entry as
in the list above, whose `status` is `queued` (or `suppressed` when the recipient is on the
suppression list; nothing is then sent). The recipient follows the rule of the first welcome: the
invitation address while the mailbox is reserved for an invitee, otherwise the owner's account
email. A mailbox never welcomed before may be resent too: that is how an admin completes a welcome
that failed.

Each resend is a new [transactional message](DELIVERY.md#transactional-mail), like the first welcome, with
`idempotency_key` `member-welcome:<mailbox id>:<n>`, where `n` is the new `resend_count`. The record
of the mailbox stays one row and now points at the latest welcome, so a later roster upload still
answers `already_sent`. Resending is audited as `member_welcome_mails.resend` with `mailbox_id`,
`resend_count`, and `status`.

| Error | When |
|---|---|
| `403 PERMISSION.DENIED` | The caller is not an owner or admin |
| `404 MAILBOX.NOT_FOUND` | No mailbox with that id in the organization |
| `409 MAILBOX.WELCOME_NOT_ELIGIBLE` | The mailbox cannot be welcomed (see the list above) |
| `409 MAILBOX.WELCOME_DISABLED` | The organization turned welcome mails off |
| `422 COMMON.VALIDATION_FAILED` | `mailbox_id` is missing or not a positive integer |
| `422 MAILBOX.DOMAIN_NOT_VERIFIED` | The mailbox's domain is not verified, so `no-reply@` cannot send |
| `429 MAILBOX.WELCOME_RESEND_LIMITED` | A welcome mail of this mailbox was stored less than a minute ago, or it was resent 5 times in the current 24-hour window; `Retry-After` gives the seconds to wait |
| `429 MAILBOX.SEND_LIMIT_REACHED` | The transactional lane is full; nothing is stored |
| `503 COMMON.SERVICE_UNAVAILABLE` | This instance cannot send welcome mails (`not_configured`) |

The 24-hour window starts at the first resend after the previous window ended. A refused resend
stores nothing and counts toward no limit.

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
