# Self-Serve Domain Acceptance

A person runs this once on a real Zmail instance before self-serve domains are called done. It
proves the path from
[cloudflare-self-serve-domains.zh-CN.md](../plans/cloudflare-self-serve-domains.zh-CN.md) §8 end to
end: a domain whose DNS is not in Cloudflare is connected from the console, its name servers are
changed, Zmail sets it up, and real mail leaves and arrives with SPF, DKIM, and DMARC passing. A
second pass upgrades a send-only domain to receiving.

It sends real mail and changes a real domain's name servers. Automated checks never run it. Public
HTTP behavior is owned by [contracts/MAILBOXES.md](../../contracts/MAILBOXES.md); the worker is
described in [api/docs/DEPLOYMENT.md](../../api/docs/DEPLOYMENT.md#mail-zone-worker).

## What You Need

- A Zmail instance with the `mailbox` starter, `MAILBOX_PROVIDER=cloudflare`, and these settings
  (Docker Compose installs set them in `.env`, see
  [deploy/self-host/README.md](../../deploy/self-host/README.md#organization-domains)):
  - `MAILBOX_CUSTOM_DOMAINS_ENABLED=true`
  - `MAILBOX_CLOUDFLARE_EMAIL_WORKER` naming the deployed Email Worker
  - `MAILBOX_CLOUDFLARE_ACCOUNT_ID` and `MAILBOX_CLOUDFLARE_API_TOKEN` (permissions below)
  - `MAILBOX_MAX_ZONES_PER_ORGANIZATION` left at its default of 3, or set to at most 20
- The `mail:zone:work` worker running next to the API (`mail-zone-worker` in Compose).
- A test domain, called `example.test-domain` below, that:
  - you can change the name servers of at its registrar;
  - is not in any Cloudflare account, so Zmail has to create the zone;
  - has DNSSEC off at the registrar, or you can turn it off (a DS record left at the registrar
    breaks the domain once the name servers change);
  - serves nothing that matters, because its whole DNS moves into the instance's Cloudflare account.
- An owner or admin account in an organization on the instance with no managed zones yet.
- An external Gmail inbox you control, to receive and reply. Do not use an inbox of someone who has
  not agreed to receive the test mail.

### Cloudflare API Token

The token behind `MAILBOX_CLOUDFLARE_API_TOKEN` needs:

| Permission | Why |
|---|---|
| Account → Zone: Edit (new) | Create the zone for a new apex, ask Cloudflare to re-check its name servers, delete it |
| DNS: Edit | Import, read, add, change, and delete DNS records |
| Zone Settings: Edit | Turn on Email Routing for a domain |
| Email Routing Rules: Edit | One routing rule per mailbox |
| Email Sending: Edit | Turn on Email Sending and send |

Zone: Edit includes Zone Read. Zone resources must be **all zones of the account**: a token scoped
to named zones cannot reach the zones Zmail creates later. An instance that must keep organizations
off some zones uses a Cloudflare account that holds only the zones they may use.

Check the account's limits on adding zones before the acceptance; free accounts are rate limited.

## Existing Deployments

Instances that ran Zmail before managed zones, such as fe5, need two changes before the
acceptance. Nothing else changes; the migration runs with the next `up`.

1. **Widen the token.** In the Cloudflare dashboard, edit the token the instance already uses for
   mail domains, add **Account → Zone: Edit**, and set its zone resources to all zones of the
   account. The token value does not change, so `.env` stays as it is. Only an account
   administrator can do this.
2. **Add the zone worker.** Pull the release that has the `mail-zone-worker` service in
   `deploy/self-host/compose.yaml`, then:

   ```bash
   docker compose build
   docker compose up -d --wait
   docker compose logs mail-zone-worker | grep mail_zone.worker_started
   ```

   The log line shows `batch=100` and `poll=10m`. An instance that does not use Compose runs
   `/app/fastbuild mail:zone:work --batch=100 --poll=10m` from the API image as one more
   long-running process with the API's environment.
3. Leave the existing domains alone. Domains in zones the operator added keep `zone: null` and
   behave as before.

## Part A: Send and Receive

1. **Connect.** In the console, open Domain and connect `example.test-domain` (or
   `mail.example.test-domain`) with Send and receive. If the console suggests Send only because
   the name already has another service's MX, use a subdomain such as `mail.example.test-domain`
   instead; Part B covers send-only.
   - Expect: the domain is pending, and the page shows two `*.ns.cloudflare.com` name servers under
     "Change the name servers of example.test-domain", with steps for the registrar the original
     name servers belong to.
   - Expect: the imported DNS records are listed. Compare them with the domain's current DNS and
     add anything missing on the DNS records page now.
2. **Change the name servers.** At the registrar, turn DNSSEC off if it is on, then replace the
   name servers with the two from step 1. Note the original ones; the zone keeps them as
   `original_name_servers` too.
3. **Wait for the zone.** Either wait for the worker (every 10 minutes) or press Check now on the
   domain. "Cloudflare limits re-checks" only means try later. Delegation usually takes minutes to
   hours, at most 48 hours. From a shell, `dig +short NS example.test-domain` shows the change.
   - Expect: the zone becomes Active without anyone pressing anything, and the domain starts
     setting up by itself.
4. **Verify.** When setting up ends, press Check DNS.
   - Expect: every receiving, sending, and DMARC row is `ok`, and the domain is verified. On the
     DNS records page the mail records show as locked and cannot be edited or deleted.
5. **Create a mailbox** such as `accept@example.test-domain`.
6. **Send out.** From that mailbox, send a message to the Gmail inbox. Cloudflare may refuse sends
   for a few minutes after the domain verifies; send again if so.
   - Expect: the message arrives in the inbox, not in spam.
   - In Gmail, open the message, choose Show original, and read `Authentication-Results`. Expect
     `spf=pass`, `dkim=pass`, and `dmarc=pass` for the From domain `example.test-domain` (or the
     subdomain you connected).
7. **Receive.** Reply from Gmail.
   - Expect: the reply appears in the Zmail mailbox.
8. **Reply back** from Zmail to that message.
   - Expect: it arrives in Gmail in the same conversation.
9. **Same domain.** Create a second mailbox on the domain and send between the two.
   - Expect: it arrives.

## Part B: Send Only, Then Receiving

Uses a second name in the same zone, here `notify.example.test-domain`, standing in for a name whose
mail is handled by another service.

1. **Give it another service's MX.** On the zone's DNS records page, add an MX record at `notify`
   pointing at a mail host outside Cloudflare, for example the test domain's original mail host
   or the registrar's mail forwarding host.
2. **Connect** `notify.example.test-domain`.
   - Expect: the console suggests Send only and shows the MX host it found. Keep Send only.
   - Expect: because the zone is already active, setup starts at once and the domain verifies with
     sending and DMARC rows only, and no receiving rows. The MX record stays editable.
3. **Send out.** Create a mailbox such as `hello@notify.example.test-domain`, send from it to
   Gmail, and check Show original as in Part A, step 6.
4. **Start receiving.** Press Start receiving in Zmail.
   - Expect: the confirmation lists the MX record from step 1 as the one being replaced.
   - Expect: after setting up, the receiving MX rows are `conflict` and the domain is pending,
     because the other service's MX is still on the name.
5. **Remove the old MX** on the DNS records page (it is not locked), then press Check DNS.
   - Expect: the domain is verified with receiving rows `ok`.
6. **Receive.** From Gmail, send to the mailbox created in step 3.
   - Expect: it arrives in Zmail. Mailboxes created while the domain was send-only receive too.

## Part C: Guards

1. Try to remove `example.test-domain` while it has mailboxes. Expect a refusal (the domain is in
   use).
2. Delete every mailbox and both domains, then press Remove from Zmail on the zone while the
   registrar still points at Cloudflare. Expect a refusal telling you to change the name servers back
   to the original ones.
3. To finish, change the name servers back at the registrar, wait until
   `dig +short NS example.test-domain` no longer shows `ns.cloudflare.com`, and remove the zone.
   Or keep the zone and domains for later checks.

## Record the Result

Write down, without any token or secret:

- instance, Zmail commit, date, test domain, and registrar;
- the time from changing the name servers to the zone becoming active;
- the `Authentication-Results` line from Part A, step 6 and Part B, step 3;
- whether each expectation above held, and any `last_error` text or console error shown.

## When It Fails

| Symptom | Look at |
|---|---|
| Connecting answers `422 MAILBOX.ZONE_LIMIT_REACHED` | The organization already has `MAILBOX_MAX_ZONES_PER_ORGANIZATION` managed zones |
| Connecting answers `409 MAILBOX.DOMAIN_TAKEN` | Another organization, or another Cloudflare account, holds the apex |
| Connecting answers `502 MAILBOX.DOMAIN_PROVIDER_FAILED` | The token lacks Account → Zone: Edit, or the account cannot add more zones |
| The zone stays pending after `dig` shows the new name servers | `docker compose logs mail-zone-worker`; DNSSEC still on at the registrar |
| The domain stays pending with `last_error` | The step Cloudflare refused; press Set up again after fixing it |
| `dmarc=fail` in Gmail | The DKIM `d=` or the SPF domain does not match the From domain; check the sending rows |
| Mail from Gmail does not arrive | The Email Worker's `MAILBOX_INBOUND_URL` and secret; the mailbox's routing rule in Cloudflare |
