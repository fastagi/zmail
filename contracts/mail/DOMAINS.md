# Mail Domains Contract

Part of the [Mailbox Contract](../MAILBOXES.md). Defines custom mail domains, managed zones, DNS records, and organization Cloudflare discovery and connection setup. See [`../MAILBOXES.md`](../MAILBOXES.md) for activation, ownership, route overview, and the master error catalog.

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
- Every domain carries `mailbox_count`, the organization's live mailboxes with an address on it,
  and `default`. The default domain is the one an owner or admin chose with
  `PUT /v1/mail-domains/:domain_id/default` while it is `verified`; with none chosen, or while the
  chosen one is `pending`, it is the verified domain connected first. At most one domain is the
  default, and none while no domain is verified. Choosing a `pending` domain is
  `422 MAILBOX.DOMAIN_NOT_VERIFIED`; choosing the default again changes nothing. The answer is the
  domain with `default: true`. Removing the chosen domain makes the verified domain connected first
  the default again.
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
    "default": true,
    "mailbox_count": 4,
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

### Organization Cloudflare discovery

`POST /v1/mail-cloudflare-previews` accepts an organization manager's candidate `api_token` and returns the active zones visible to that token, with each zone's `id`, `name`, `account_id`, and `account_name`. The operation is read-only and the credential is never stored or returned. An empty list is valid. A successful response verifies visibility only; email routing, DNS writes and sending require their own checks.

Organization owners and administrators can read the masked state with `GET /v1/mail-cloudflare-connection` and bind one zone with `POST /v1/mail-cloudflare-connection` (`api_token`, `zone_id`). The response returns the signing secret once, a generated Worker name and a callback path. The operator's public HTTPS API origin completes the URL. Enable Email Routing on the exact receiving domain and deploy the Worker in its own account. The daily token needs the selected zone's DNS Edit, Zone Read and Email Routing Rules Edit plus the corresponding account's Email Sending Edit.

`POST /v1/mail-cloudflare-connection/{connection_id}/domains` accepts a name and capability. A domain with foreign MX is refused for receiving. DNS setup and checks, exact-address rules and sending use the connection of that domain. The Worker posts signed mail to `/v1/cloudflare-connections/{connection_id}/inbound-mail-messages`. Its organization and the recipient's domain must match the stored mailbox. `DELETE /v1/mail-cloudflare-connection/{connection_id}` clears the credentials and stops later sends and callbacks through this connection. The organization's existing messages and its Cloudflare resources are retained.
