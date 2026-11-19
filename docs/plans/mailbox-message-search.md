# Mailbox Message Search (Reduced Plan)

## Problem

`GET /v1/mailboxes/{mailbox_id}/messages` filters only by `direction` and pages newest first.
Members reading a busy inbox or sent folder cannot search within a mailbox or narrow to unread
messages without leaving the mailbox for the cross-mailbox log (`GET /v1/mail-messages`), which
lacks an unread filter.

## Decision

- Add optional `q` and `unread` query parameters to `GET /v1/mailboxes/{mailbox_id}/messages`.
- `q` matches `GET /v1/mail-messages`: trimmed, at most 200 characters, no control characters, and
  case-insensitive substring matching on `subject`, `from_address`, `to_addresses`, and
  `cc_addresses` with `%`, `_`, and `\` escaped literally. Whitespace-only `q` is treated as
  omitted.
- `unread` is an optional boolean on the caller's own read state, as `read` reports it: `true`
  keeps inbound messages the caller has not read (no `mail_message_reads` row of the caller's
  membership), `false` keeps outbound mail and inbound mail the caller has read, and omitting
  `unread` returns both. Another member opening a message never changes it for the caller.
- Both filters compose with `direction` and offset pagination (`page`, `per_page`).

## Contract

`GET /v1/mailboxes/{mailbox_id}/messages` accepts optional `q` (`string`, `maxLength: 200`) and
`unread` (`boolean`). Matching `GET /v1/mail-messages`, an invalid `direction`, a non-boolean or
repeated `unread`, or an over-long or control-character `q` returns `400 COMMON.INVALID_INPUT`.

## Out Of Scope

Full-text search on message bodies (`text_body`, `html_body`), attachment file names, date-range
filters, and saved searches.

## Verification

Contract lint, generated TypeScript client sync (`web` and `admin`), and route check; service unit
tests for trimming, validation, and filter forwarding; handler tests for `q` and `unread` parsing
and `400 COMMON.INVALID_INPUT` errors; and PostgreSQL repository tests for escaped `ILIKE`
matching, `unread` filtering, and pagination totals.
