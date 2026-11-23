import {
  _default,
  array,
  boolean,
  int,
  iso,
  literal,
  maximum,
  maxLength,
  minLength,
  nonnegative,
  nullable,
  number,
  optional,
  positive,
  refine,
  regex,
  strictObject,
  string,
} from 'zod/mini';

const safeIdSchema = number().check(int(), positive(), maximum(Number.MAX_SAFE_INTEGER));
const nonnegativeIntegerSchema = number().check(
  int(),
  nonnegative(),
  maximum(Number.MAX_SAFE_INTEGER)
);
const positiveIntegerSchema = number().check(
  int(),
  positive(),
  maximum(Number.MAX_SAFE_INTEGER)
);
const timestampSchema = iso.datetime({ offset: true });
const kindSchema = string().check(
  maxLength(100),
  regex(/^[a-z][a-z0-9_]*(?:\.[a-z][a-z0-9_]*)+$/)
);
const titleSchema = string().check(refine(value => hasBoundedText(value, 160)));
const bodySchema = string().check(refine(value => hasBoundedText(value, 4_000)));
const actionUrlSchema = string().check(maxLength(2_048), refine(isSafeActionUrl));

export const notificationSchema = strictObject({
  id: safeIdSchema,
  kind: kindSchema,
  title: titleSchema,
  body: bodySchema,
  action_url: optional(actionUrlSchema),
  is_read: boolean(),
  read_at: nullable(timestampSchema),
  created_at: timestampSchema,
});

const paginationMetaSchema = strictObject({
  current_page: positiveIntegerSchema,
  per_page: positiveIntegerSchema,
  total: nonnegativeIntegerSchema,
  last_page: positiveIntegerSchema,
  from: nonnegativeIntegerSchema,
  to: nonnegativeIntegerSchema,
});

const paginationLinksSchema = strictObject({
  first: string(),
  last: string(),
  prev: nullable(string()),
  next: nullable(string()),
});

export const notificationPageEnvelopeSchema = strictObject({
  code: literal(0),
  message: string(),
  data: array(notificationSchema),
  meta: paginationMetaSchema,
  links: paginationLinksSchema,
});

export const notificationStatusSchema = strictObject({
  unread_count: nonnegativeIntegerSchema,
});

export const notificationReadStateResultSchema = strictObject({
  updated_count: nonnegativeIntegerSchema,
  unread_count: nonnegativeIntegerSchema,
});

// `web_push_enabled` defaults to true so responses from an API that predates web push still parse.
export const notificationPreferenceSchema = strictObject({
  in_app_enabled: boolean(),
  email_enabled: boolean(),
  web_push_enabled: _default(boolean(), true),
});

// Omitting `web_push_enabled` keeps the stored value, so older clients cannot turn push back on.
export const replaceNotificationPreferenceSchema = strictObject({
  in_app_enabled: boolean(),
  email_enabled: boolean(),
  web_push_enabled: optional(boolean()),
});

export const webPushConfigSchema = strictObject({
  enabled: boolean(),
  public_key: string().check(maxLength(256), regex(/^[A-Za-z0-9_-]*$/)),
});

export const webPushSubscriptionSchema = strictObject({
  id: safeIdSchema,
  device_label: string().check(maxLength(480)),
  created_at: timestampSchema,
  last_success_at: nullable(timestampSchema),
});

export const webPushSubscriptionListSchema = array(webPushSubscriptionSchema);

// The BFF checks only the shape; the API owns the push-service allowlist and key validation.
export const registerWebPushSubscriptionSchema = strictObject({
  endpoint: string().check(minLength(1), maxLength(2_048)),
  keys: strictObject({
    p256dh: string().check(minLength(1), maxLength(256)),
    auth: string().check(minLength(1), maxLength(64)),
  }),
  device_label: optional(string().check(maxLength(480))),
});

export const replaceNotificationReadStateSchema = strictObject({
  is_read: boolean(),
});

export const markNotificationsReadSchema = strictObject({
  through_id: safeIdSchema,
});

export const notificationRouteIdSchema = string().check(regex(/^[1-9]\d*$/));

function hasBoundedText(value: string, maxLength: number): boolean {
  const length = Array.from(value).length;
  return value === value.trim() && length >= 1 && length <= maxLength;
}

function isSafeActionUrl(value: string): boolean {
  if (
    !value.startsWith('/') ||
    value.startsWith('//') ||
    value.includes('\\') ||
    containsControl(value)
  ) {
    return false;
  }
  try {
    const parsed = new URL(value, 'https://fastbuild.invalid');
    const decodedPath = decodeURIComponent(parsed.pathname);
    return (
      parsed.origin === 'https://fastbuild.invalid' &&
      !decodedPath.startsWith('//') &&
      !decodedPath.includes('\\') &&
      !containsControl(decodedPath)
    );
  } catch {
    return false;
  }
}

function containsControl(value: string): boolean {
  return /[\u0000-\u001F\u007F]/u.test(value);
}
