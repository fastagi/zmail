# External Mail In Development

Development notes for connected mailboxes (`EXTERNAL_MAIL_PROVIDERS`). The public behavior is in
[`contracts/MAILBOXES.md`](../../contracts/MAILBOXES.md) (External Mailboxes); nothing here is part
of the contract.

## The Fake Connector

`EXTERNAL_MAIL_PROVIDERS=fake` stands in for Gmail and Outlook with an in-memory source, so the
connect, sync, reconnect, and disconnect flows run without provider credentials. Configuration
refuses `fake` with `APP_ENV=production` and refuses combining it with a real provider.

- Sign-in: the fake provider page is never opened. Finish a sign-in with any code; the code `<code>`
  binds the account `<code>@external.invalid`, and the code `refuse` is refused.
- Mail: the source lives in the memory of each process. The API process and the separate
  `mail:external:sync` worker (started by `make dev` when `EXTERNAL_MAIL_PROVIDERS` is set) each have
  their own, empty source, so the worker records sync runs but never fetches mail.

## Delivering Mail To A Fake Account

While `fake` is the active source, and only then, the API registers one more route:

```http
POST /v1/external-mail-connections/:connection_id/fake-deliveries
Authorization: Bearer <session of the connection's owner, or of a manager of a shared connection>
Organization-Id: <organization>
Content-Type: application/json

{ "from": "carol@sender.test", "subject": "Invoice", "text": "Body",
  "html": "<p>Body</p>", "message_id": "inv-1@sender.test" }
```

It accepts the caller's own connection, or a shared connection they manage (every one for an
`owner` or `admin`, those of mailboxes held with `manage` otherwise). It appends the message to the connection's account in the API process's fake source and syncs that
connection at once in the API process, through the same code as the worker, so the message is stored
once in the connected mailbox with the connection's provider as its `source`, and goes through the
same post-store steps (triage queue, agent feed, webhooks) as synced mail.

| Field | Rule |
|---|---|
| `from` | Required email address, at most 320 characters |
| `subject` | Optional, at most 998 characters |
| `text` | Required, at most 100 KB |
| `html` | Optional, at most 100 KB |
| `message_id` | Optional provider message id, at most 255 characters, angle brackets dropped; empty generates `dev-…@external.invalid`. Delivering the same id again stores no second copy |

| Case | Answer |
|---|---|
| Delivered and synced | `201` `{ "message_id": "inv-1@sender.test" }` |
| Missing or invalid field | `422 COMMON.VALIDATION_FAILED` |
| A connection the caller may not manage (another member's own one, or a shared one they do not manage), a revoked one, or an unknown id | `404 MAILBOX.EXTERNAL_CONNECTION_NOT_FOUND` |
| Real connectors, or external mail off | `404`: the route does not exist |

The route is not in `contracts/openapi.yaml`: the route contract check assembles the routes without
`fake`, and `contracts/scripts/check-api-routes.mjs` lists it as intentionally undocumented in case
the check runs with `EXTERNAL_MAIL_PROVIDERS=fake` in its environment.

Example against `make dev` (session cookie handling omitted; the Web BFF does not forward this path):

```bash
curl -sS -X POST "http://127.0.0.1:8025/v1/external-mail-connections/3/fake-deliveries" \
  -H "Authorization: Bearer $SESSION" -H "Organization-Id: 1" -H "Content-Type: application/json" \
  -d '{"from":"carol@sender.test","subject":"Hello","text":"First synced message"}'
```
