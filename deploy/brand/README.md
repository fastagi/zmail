# Zmail production mail identity and templates

This directory contains product-specific operational content. It does not change
framework defaults or automatically replace password-reset/invitation email content.

## Intended identities

| Purpose | Identity |
|---|---|
| Platform notifications and replies | `hello@notify.zmail.im (localized Zmail team display name)` |
| Shared trial sending | Per-user addresses on `send.zmail.im` |
| Established owner login and mailbox | `owner@zmail.im` (retained) |
| Customer business sending | Customer-owned verified domains, subsequent onboarding work |

Provision the notify domain for the ZGI organization through the existing Domains
workflow. Create the hello mailbox there so replies have a real inbox. Change the
shared domain only after its DNS and literal inbound routes are ready; a subdomain
has no apex catch-all. The domain-management credential was explicitly approved and installed. Never copy the local Wrangler OAuth token to the server.

## Brand templates

The four JSON files are request bodies for the existing organization-scoped
`POST /v1/mail-templates` endpoint:

- `welcome-zh.json` and `welcome-en.json`: greeting and onboarding.
- `notification-zh.json` and `notification-en.json`: a service-related title and message.

All have equivalent HTML/plain text, a text wordmark, a single first-party console
link, reply contact and an explanation of why the recipient received the message.
There are no remote images, tracking pixels, short URLs, scripts or promotional claims.
Use these for requested service mail, not unsolicited campaigns. Marketing/broadcast
content needs its own consent explanation and the existing unsubscribe mechanism.

Saved in ZGI on 2026-11-17: English notification ID 1, Chinese notification ID 2,
English welcome ID 3, Chinese welcome ID 4. Preview API returned no missing variables
for all four. Sample variables affect previews/test sends only. Real sends must supply
required values. The service escapes substituted HTML values.

Use the Templates page to preview and edit. Select the intended mailbox when sending;
the template itself does not set the sender. Do not overwrite existing templates by
name without comparing their current content. Saved message content stays immutable
when templates are edited.

## Authentication verification

Export required records from Cloudflare's sending-subdomain DNS endpoint into a local
JSON file. It contains public DNS data, not credentials. Then run:

```sh
python3 deploy/brand/check-authentication.py \
  --domain notify.zmail.im --records /path/to/required-records.json
```

The checker compares public DNS with the provider's required records and checks DMARC
enforcement. Reports are point-in-time evidence, not guarantees about future sends.
Actual receiver authentication requires Authentication-Results from a received message:
SPF must check the envelope sender, DKIM the signing domain/selector, and DMARC the visible
From domain alignment. Do not interpret an apex SPF record alone as sending verification.
The authorized recipient's already signed-in Gmail browser account was used to inspect
the branded test message and its original headers. Never infer Gmail PASS or inbox
placement from DNS/provider acceptance alone.

## Delivery feedback

A separate `zmail-fe5-delivery-events` queue feeds `zmail-fe5-email-worker`, which signs
and sends provider events to `https://zmail.im/v1/mail-delivery-events`. Its consumer
uses batch size 10, timeout 5 seconds, retry delay 60 seconds, 5 retries, and
`zmail-fe5-delivery-dead-letter` for exhausted retries. Both queues use the existing
Cloudflare account/plan. No paid plan upgrade was made. These resources share the
account's included usage and billing; this is not a zero-cost guarantee at scale.

Use Wrangler 4.164.0 or later: the previously installed 4.46.0 CLI does not expose
email.sending subscription creation flags. For each enabled sending domain:

```sh
npx wrangler@4.164.0 queues subscription create zmail-fe5-delivery-events \
  --source email.sending --zone-id YOUR_ZONE_ID --domain notify.zmail.im \
  --events message.delivered,message.deferred,message.bounced,message.failed,message.rejected,message.complained \
  --name zmail-fe5-notify
```

List subscriptions before creating them to avoid duplicates. Existing unrelated
`zmail-email-events` queue and its old Worker remain unchanged.

Provider `accepted` means it took the send request. `delivered` means the receiving
mail server accepted the message, not that it entered the inbox or was read. The
message detail timeline exposes provider outcomes; no open tracking is added.
Repeated events deduplicate by provider event ID. Invalid 413/422 and unknown-message
404 events are acknowledged by the existing Worker; network/auth/server failures retry.

After sending changes or API outages, inspect the queue metrics and dead-letter
backlog. The default retention is four days on this account. Investigate any dead-letter
message before expiry; correct the cause and replay the event, never resend the original
email as a substitute for event replay. There is currently no external alert integration.

## Checks performed

- Worker suite: 25 tests passed.
- Brand template API creation, rendered preview, and live Web preview passed.
- Apex zmail.im required DNS all matched; DMARC p=reject.
- New queue deployed; apex/notify/send subscriptions active.
- notify/send domains verified in the application and against public DNS; DMARC p=reject.
- Official sender and system MAIL_FROM configured; shared domain switched to send.zmail.im.
- Branded Gmail test message 4 has a real delivered event at 2026-11-17T19:33:43.288Z.
- New shared address checks@send.zmail.im received real SMTP mail via the new Worker.
- Its real reply reached hello@notify.zmail.im; both internal sends show delivered.
- API regression tests and full make check passed; fixed API image deployed.
- Gmail browser verification: branded message 4 is in Inbox; Show original reports
  SPF PASS, DKIM PASS (notify.zmail.im), DMARC PASS, and delivery after 3 seconds.
  This verifies this message only; the send/apex domains were not tested this way.

The scoped runtime token can manage DNS and literal routes but the Email Routing DNS
activation endpoint rejected it. The operator enabled notify/send routing using the
existing local OAuth login; no OAuth token was placed on the host. Future new domains
may need that initial operator step. DNS rechecks and per-mailbox routes work with the
installed token. The root-only env/Compose snapshots support rollback without deleting
mailboxes or events.
