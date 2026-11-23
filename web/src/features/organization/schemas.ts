import {
  array,
  boolean,
  email,
  enum as enumSchema,
  int,
  iso,
  literal,
  maxLength,
  maximum,
  minLength,
  nonnegative,
  nullable,
  number,
  object,
  optional,
  pipe,
  positive,
  refine,
  regex,
  strictObject,
  string,
  toLowerCase,
  transform,
  trim,
  unknown,
} from 'zod/mini';

export const organizationRoleSchema = enumSchema(['owner', 'admin', 'member']);
export const organizationInvitationRoleSchema = enumSchema(['admin', 'member']);
export const organizationInvitationStatusSchema = enumSchema([
  'pending',
  'accepted',
  'revoked',
  'expired',
]);
export const invitationEmailSendStatusSchema = enumSchema([
  'accepted_by_provider',
  'failed',
  'not_configured',
]);

/** The console features an organization grants each member, in display order. */
export const memberFeatureSchema = enumSchema(['broadcasts', 'contacts', 'templates', 'api_keys']);

/**
 * What a membership may use. Owners and admins always report every feature. Optional on the
 * views below so a console talking to an API from before grants existed keeps working.
 */
export const memberFeaturesSchema = strictObject({
  broadcasts: boolean(),
  contacts: boolean(),
  templates: boolean(),
  api_keys: boolean(),
});

const safeIdSchema = number().check(
  int(),
  positive(),
  maximum(Number.MAX_SAFE_INTEGER)
);
const timestampSchema = iso.datetime({ offset: true });
const positiveIntegerSchema = number().check(int(), positive());
const nonnegativeIntegerSchema = number().check(int(), nonnegative());
const organizationNameInputSchema = string().check(
  trim(),
  refine(hasValidOrganizationNameLength)
);
const organizationNameSchema = string().check(
  refine(
    (value) => value === value.trim() && hasValidOrganizationNameLength(value)
  )
);
const organizationSlugSchema = string().check(
  regex(/^[a-z0-9](?:[a-z0-9-]{1,48}[a-z0-9])$/)
);
const canonicalInvitationEmailSchema = email().check(
  maxLength(100),
  refine((value) => value === value.trim() && value === value.toLowerCase())
);
const invitationEmailInputSchema = pipe(
  string().check(trim(), toLowerCase(), maxLength(100)),
  email()
);
const invitationTokenSchema = string().check(
  trim(),
  minLength(1),
  maxLength(256)
);

export const organizationSchema = object({
  id: safeIdSchema,
  name: organizationNameSchema,
  slug: organizationSlugSchema,
  role: organizationRoleSchema,
  features: optional(memberFeaturesSchema),
  created_at: timestampSchema,
  updated_at: timestampSchema,
});

export const organizationContextSchema = object({
  organization_id: safeIdSchema,
  organization_name: organizationNameSchema,
  organization_slug: organizationSlugSchema,
  membership_id: safeIdSchema,
  user_id: safeIdSchema,
  role: organizationRoleSchema,
  features: optional(memberFeaturesSchema),
});

export const paginationMetaSchema = object({
  current_page: positiveIntegerSchema,
  per_page: positiveIntegerSchema,
  total: nonnegativeIntegerSchema,
  last_page: positiveIntegerSchema,
  from: nonnegativeIntegerSchema,
  to: nonnegativeIntegerSchema,
});

export const paginationLinksSchema = object({
  first: string(),
  last: string(),
  prev: nullable(string()),
  next: nullable(string()),
});

export const organizationPageEnvelopeSchema = object({
  code: literal(0),
  message: string(),
  data: array(organizationSchema),
  meta: paginationMetaSchema,
  links: paginationLinksSchema,
});

export const organizationMemberSchema = strictObject({
  id: safeIdSchema,
  user_id: safeIdSchema,
  username: string().check(minLength(1), maxLength(50)),
  nickname: optional(string().check(maxLength(50))),
  avatar: optional(string().check(maxLength(255))),
  role: organizationRoleSchema,
  features: optional(memberFeaturesSchema),
  joined_at: timestampSchema,
  updated_at: timestampSchema,
});

export const organizationMemberPageEnvelopeSchema = strictObject({
  code: literal(0),
  message: string(),
  data: array(organizationMemberSchema),
  meta: paginationMetaSchema,
  links: paginationLinksSchema,
});

export const organizationInvitationSchema = strictObject({
  id: safeIdSchema,
  organization_id: safeIdSchema,
  email: canonicalInvitationEmailSchema,
  role: organizationInvitationRoleSchema,
  status: organizationInvitationStatusSchema,
  expires_at: timestampSchema,
  email_send_status: nullable(invitationEmailSendStatusSchema),
  email_attempted_at: nullable(timestampSchema),
  created_at: timestampSchema,
  updated_at: timestampSchema,
});

export const organizationInvitationPageEnvelopeSchema = strictObject({
  code: literal(0),
  message: string(),
  data: array(organizationInvitationSchema),
  meta: paginationMetaSchema,
  links: paginationLinksSchema,
});

/**
 * The one-time accept link of a token minted by a create or resend call, in the shape invitation
 * emails use: `<console origin>/invite#<token>`, where the console origin (MAILBOX_PUBLIC_URL) may
 * carry a path. A bearer secret: keep it out of caches and logs.
 */
export const invitationUrlSchema = string().check(
  maxLength(2048),
  refine(isInvitationUrl)
);

/**
 * The create and resend result. The invitation view inside stays strict, but the wrapper ignores
 * fields it does not know, so an additive API change cannot turn a saved invitation into an error.
 * `invitation_url` is read loosely here and validated on its own: an unusable link is dropped.
 */
export const organizationInvitationCreateResultSchema = object({
  invitation: organizationInvitationSchema,
  email_send_status: invitationEmailSendStatusSchema,
  invitation_url: optional(unknown()),
});

export const organizationOwnershipTransferSchema = strictObject({
  previous_owner: organizationMemberSchema,
  new_owner: organizationMemberSchema,
});

export const createOrganizationSchema = object({
  name: organizationNameInputSchema,
  slug: optional(organizationSlugSchema),
});

export const updateOrganizationSchema = object({
  name: organizationNameInputSchema,
});

export const createOrganizationInvitationSchema = strictObject({
  email: invitationEmailInputSchema,
  role: organizationInvitationRoleSchema,
});

export const acceptOrganizationInvitationSchema = strictObject({
  token: invitationTokenSchema,
});

export const updateOrganizationMemberSchema = strictObject({
  role: organizationInvitationRoleSchema,
});

export const updateOrganizationMemberFeaturesSchema = strictObject({
  broadcasts: optional(boolean()),
  contacts: optional(boolean()),
  templates: optional(boolean()),
  api_keys: optional(boolean()),
}).check(
  refine((value) => Object.values(value).some((granted) => granted !== undefined), {
    message: 'Name at least one feature',
  })
);

export const transferOrganizationOwnershipSchema = strictObject({
  new_owner_member_id: safeIdSchema,
});

export const organizationRouteIdSchema = pipe(
  pipe(
    string().check(regex(/^[1-9]\d*$/)),
    transform(Number)
  ),
  safeIdSchema
);

function hasValidOrganizationNameLength(value: string): boolean {
  const length = Array.from(value).length;
  return length >= 2 && length <= 100;
}

const LOCAL_HOSTNAMES = new Set(['localhost', '127.0.0.1', '[::1]']);

function isInvitationUrl(value: string): boolean {
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    return false;
  }
  const secure =
    url.protocol === 'https:' ||
    (url.protocol === 'http:' && LOCAL_HOSTNAMES.has(url.hostname));
  return (
    secure &&
    url.username === '' &&
    url.password === '' &&
    url.search === '' &&
    url.pathname.endsWith('/invite') &&
    url.hash.length > 1
  );
}
