import {
  array,
  boolean,
  int,
  literal,
  nullable,
  number,
  optional,
  strictObject,
  string,
  union,
} from 'zod/mini';

import {
  nonnegativeIntegerSchema,
  positiveIntegerSchema,
  safeIdSchema,
  timestampSchema,
} from './shared-schemas';

export const MAIL_DOMAIN_NAME_MAX_LENGTH = 253;
export const MAIL_DOMAIN_MAX_PER_ORGANIZATION = 10;
export const MAIL_ZONE_MAX_PER_ORGANIZATION = 3;
export const MAIL_DNS_RECORD_NAME_MAX_LENGTH = 253;
export const MAIL_DNS_RECORD_CONTENT_MAX_LENGTH = 4_096;
export const MAIL_DNS_TXT_CONTENT_MAX_LENGTH = 2_048;
/** Cloudflare's DNS record identifier, as the record routes accept it. */
export const MAIL_DNS_RECORD_ID_PATTERN = /^[A-Za-z0-9]{1,64}$/u;

export const mailboxDomainSchema = strictObject({
  domain: string(),
  // `shared` is the instance domain; `organization` a verified domain the organization connected.
  kind: union([literal('shared'), literal('organization')]),
  // True on the one domain new mailbox forms preselect. Optional so an older API still parses.
  default: optional(boolean()),
  max_mailboxes: number().check(int()),
  // `cloudflare` delivers; `log` only records and is for development.
  provider: union([literal('cloudflare'), literal('log')]),
  inbound_enabled: boolean(),
  // Per-organization lane limits over a rolling 24 hours: personal, broadcast, transactional.
  daily_send_limit: positiveIntegerSchema,
  daily_broadcast_limit: positiveIntegerSchema,
  daily_transactional_limit: positiveIntegerSchema,
  // Broadcasts need the inbound secret and MAILBOX_PUBLIC_URL for their unsubscribe links.
  broadcasts_enabled: boolean(),
  // Instance-wide: whether owners and admins may connect their own domains.
  custom_domains_enabled: boolean(),
  // Instance-wide transactional mail settings: the switch, the two rate limits, and the domain whose
  // addresses are simulated instead of sent.
  transactional_enabled: boolean(),
  transactional_recipient_limit_per_minute: positiveIntegerSchema,
  transactional_key_limit_per_second: positiveIntegerSchema,
  simulator_domain: string(),
});
export const mailboxDomainListSchema = array(mailboxDomainSchema);

export const mailDomainRecordSchema = strictObject({
  purpose: union([literal('receiving'), literal('sending'), literal('dmarc')]),
  type: string(),
  name: string(),
  content: string(),
  priority: optional(nonnegativeIntegerSchema),
  status: union([literal('ok'), literal('missing'), literal('conflict')]),
  found: optional(string()),
});

/** `send_only` leaves the domain's MX with another mail service. */
export const mailDomainCapabilitySchema = union([literal('send_receive'), literal('send_only')]);

/** A Cloudflare zone Zmail created for an organization's apex. */
export const mailZoneSchema = strictObject({
  id: safeIdSchema,
  name: string(),
  // `pending` until the registrar delegates the apex to `name_servers`.
  status: union([literal('pending'), literal('active')]),
  name_servers: array(string()),
  original_name_servers: array(string()),
  activated_at: nullable(timestampSchema),
  last_checked_at: nullable(timestampSchema),
  created_at: timestampSchema,
});
export const mailZoneListSchema = array(mailZoneSchema);

export const mailDomainSchema = strictObject({
  id: safeIdSchema,
  name: string(),
  zone_name: string(),
  capability: mailDomainCapabilitySchema,
  // Null in a zone the operator manages.
  zone: nullable(mailZoneSchema),
  status: union([literal('pending'), literal('verified')]),
  records: array(mailDomainRecordSchema),
  // A setup runs in the background; read the domain again until this is false.
  setting_up: boolean(),
  // Cloudflare refused to look for the pending zone's name servers now; check again later.
  zone_check_limited: boolean(),
  // The organization's default domain, and its live mailboxes. Optional so an older API still parses.
  default: optional(boolean()),
  mailbox_count: optional(nonnegativeIntegerSchema),
  last_error: optional(string()),
  // Stable class of last_error; an unknown code reads like domain.setup_failed.
  last_error_code: optional(string()),
  checked_at: nullable(timestampSchema),
  verified_at: nullable(timestampSchema),
  created_at: timestampSchema,
});
export const mailDomainListSchema = array(mailDomainSchema);

export const mailDomainInputSchema = strictObject({
  name: string(),
  capability: optional(mailDomainCapabilitySchema),
});

export const mailDomainPreviewInputSchema = strictObject({ name: string() });
/** What connecting a domain would find; changes nothing. */
export const mailDomainPreviewSchema = strictObject({
  name: string(),
  apex: string(),
  existing_mx: array(string()),
  suggested_capability: mailDomainCapabilitySchema,
});

/** The record types the DNS editor writes. */
export const mailDnsRecordTypeSchema = union([
  literal('A'),
  literal('AAAA'),
  literal('CNAME'),
  literal('TXT'),
  literal('MX'),
]);
export const mailDnsLockedReasonSchema = union([
  literal('mail_receiving'),
  literal('mail_spf'),
  literal('mail_dmarc'),
  literal('mail_dkim'),
  literal('mail_sending'),
]);
export const mailDnsRecordSchema = strictObject({
  id: string(),
  // A zone may hold types the editor does not write.
  type: string(),
  name: string(),
  content: string(),
  // `1` is automatic.
  ttl: positiveIntegerSchema,
  priority: optional(nonnegativeIntegerSchema),
  proxied: boolean(),
  // Zmail mail relies on the record, so it cannot be changed or deleted.
  locked: boolean(),
  locked_reason: optional(mailDnsLockedReasonSchema),
});
export const mailDnsRecordListSchema = array(mailDnsRecordSchema);

export const mailDnsRecordInputSchema = strictObject({
  type: mailDnsRecordTypeSchema,
  name: string(),
  content: string(),
  ttl: optional(number()),
  priority: optional(number()),
  // The contract defaults it to false; the client always says.
  proxied: boolean(),
});
/** Any field of a record; an omitted field keeps its current value. */
export const mailDnsRecordPatchSchema = strictObject({
  type: optional(mailDnsRecordTypeSchema),
  name: optional(string()),
  content: optional(string()),
  ttl: optional(number()),
  priority: optional(number()),
  proxied: optional(boolean()),
});
