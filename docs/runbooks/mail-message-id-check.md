# Mail Message-ID Check

Confirms that the mail provider delivers outbound mail with the `Message-ID` Zmail generated for
it. Threads depend on it: a reply names that value in `In-Reply-To`, and Zmail joins the reply to
the thread only when it matches the stored `rfc_message_id` (see `contracts/MAILBOXES.md`,
Threads). If the provider replaced the header, every reply to Zmail mail would start a new thread.

**This check sends real mail, so a person runs it.** Automated tests and agents never do: the
acceptance flow `api/tests/kest/mailbox_organize.flow.md` uses the `log` provider and proves the
threading logic, not what the provider puts on the wire.

## When To Run

- Before the first release of mailbox organize to an environment with a real provider.
- After changing the mail provider, its account, or the outbound transport code
  (`api/internal/infra/mailtransport`).

## Prerequisites

- A staging deployment with `MAILBOX_PROVIDER=cloudflare` and a mailbox you may send from.
- An external inbox you control whose client shows raw headers (for example "Show original").
- Read-only SQL access to the staging database. The API does not return the generated Message-ID:
  the detail's `message_id` of an outbound message is the provider's identifier, which may differ.

## Steps

1. In the staging Web console, send one plain message from the mailbox to your external inbox.
   Use a subject nobody else will use, for example `Message-ID check <date> <random word>`.
2. Look the message up by its mailbox and subject, newest first, and read its stored values:

   ```sql
   SELECT m.id, m.rfc_message_id, m.message_id AS provider_message_id, m.status, m.created_at
   FROM mail_messages m
   JOIN mailboxes b ON b.id = m.mailbox_id
   WHERE lower(b.address) = lower('<mailbox address>')
     AND m.direction = 'outbound'
     AND m.subject = '<the unique subject>'
   ORDER BY m.id DESC
   LIMIT 1;
   ```

3. Confirm that `status` is `sent` and `rfc_message_id` is not empty.
4. In the external inbox, open the message's raw headers and find `Message-ID`.
5. Confirm that the header equals `<` + `rfc_message_id` + `>` exactly.
6. Optional round trip: reply from the external inbox, wait for the reply to arrive in the Zmail
   mailbox, and confirm in the console that it opens in the same conversation as the sent message.
   In SQL, the reply's `in_reply_to` equals the sent message's `rfc_message_id` and both share the
   same `thread_id`.

## Result

- **Pass:** the header matches. Record the date, environment, and provider in the release notes
  or the pull request; do not record mailbox or inbox addresses.
- **Fail:** the provider rewrote or dropped the header. Do not release threading as working. Open an
  issue with the stored `rfc_message_id`, the received header, and the provider's identifier, and
  check whether the provider supports a caller-supplied `Message-ID`.

## History

- 2026-11-19: a manual round trip on a development domain (Zmail to an external mailbox, then a
  reply back to Zmail) threaded correctly, with `in_reply_to` set on the reply.
