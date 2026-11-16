# FastMail Email Worker

The mail edge of the `mailbox` starter. Cloudflare Email Routing hands every message for the
mailbox domain to this Worker; the Worker signs it and posts the raw message to the FastMail API.
The API owns mailboxes and decides whether the recipient exists. Behavior is defined in
[`contracts/MAILBOXES.md`](../../../contracts/MAILBOXES.md).

## Requirements

- The mailbox domain uses Cloudflare DNS, with Email Routing enabled.
- The FastMail API is reachable over public HTTPS and runs with `OPTIONAL_STARTERS` containing
  `mailbox`, `MAILBOX_DOMAIN` set to the domain, and a `MAILBOX_INBOUND_SECRET` of at least 32
  characters.

## Deploy

1. Set `MAILBOX_INBOUND_URL` in `wrangler.jsonc` to `https://<api host>/v1/inbound-mail-messages`.
2. Deploy the Worker from this directory:

   ```bash
   npx wrangler deploy
   ```

3. Store the shared secret, using the same value as the API's `MAILBOX_INBOUND_SECRET`:

   ```bash
   npx wrangler secret put MAILBOX_INBOUND_SECRET
   ```

4. In the Cloudflare dashboard, open the domain's Email Routing rules and point the catch-all
   address at the `fastmail-email-worker` Worker. The catch-all sends every address on the domain
   to FastMail; mail for an address that is not a mailbox is bounced.

## Outbound Mail

Sending does not go through this Worker. The API calls the Cloudflare Email Sending REST API
directly. Onboard the domain under Email Service > Email Sending, create an API token that may send
email, and set `MAILBOX_PROVIDER=cloudflare`, `MAILBOX_CLOUDFLARE_ACCOUNT_ID`, and
`MAILBOX_CLOUDFLARE_API_TOKEN` on the API.

## What Happens To A Message

| API answer | Worker action | Sender sees |
|---|---|---|
| `200` or `201` | Accept | Delivered |
| `404` | Reject with "Mailbox does not exist" | Bounce |
| `413`, or the raw message exceeds 5 MiB | Reject with "Message too large" | Bounce |
| `422` | Reject with "Message could not be processed" | Bounce |
| Anything else, or a network error | Throw | Temporary failure; the sending server retries |

A temporary failure never bounces the message, so an API outage or a wrong secret delays mail
instead of losing it.

## Test

```bash
node --test deploy/cloudflare/email-worker/worker.test.mjs
```

The signature test asserts the same vector as `TestSignInboundMatchesTheEdgeVector` in
`api/internal/modules/mailbox`, so the Worker and the API cannot drift apart unnoticed.
