import {
  array,
  int,
  iso,
  literal,
  maxLength,
  maximum,
  minLength,
  nonnegative,
  nullable,
  number,
  optional,
  pipe,
  positive,
  refine,
  regex,
  strictObject,
  enum as zEnum,
  string,
  toLowerCase,
  transform,
  trim,
} from 'zod/mini';

import { CREATABLE_API_KEY_SCOPES } from '@/features/api-key/scopes';

const apiKeyScopePattern = /^(?:\*|[a-z][a-z0-9_-]{0,31}:[a-z][a-z0-9_-]{0,31})$/;
const safeIdSchema = number().check(int(), positive(), maximum(Number.MAX_SAFE_INTEGER));
const timestampSchema = iso.datetime({ offset: true });
const canonicalApiKeyNameSchema = string().check(
  refine(value => value === value.trim() && hasValidNameLength(value))
);
const apiKeyNameInputSchema = string().check(trim(), refine(hasValidNameLength));
const canonicalApiKeyScopeSchema = string().check(regex(apiKeyScopePattern));
// A new key carries only scopes some route checks: Zmail's mail scopes, or `*`.
const apiKeyScopeInputSchema = pipe(
  string().check(trim(), toLowerCase()),
  zEnum(CREATABLE_API_KEY_SCOPES)
);
const positiveIntegerSchema = number().check(int(), positive());
const nonnegativeIntegerSchema = number().check(int(), nonnegative());
const apiKeyModeSchema = zEnum(['live', 'test']);

export const apiKeySchema = strictObject({
  id: safeIdSchema,
  user_id: safeIdSchema,
  name: canonicalApiKeyNameSchema,
  // Keys issued before a prefix rename keep their original lowercase prefix.
  key_prefix: string().check(regex(/^[a-z][a-z0-9]*_[a-z0-9_-]+$/)),
  mode: apiKeyModeSchema,
  scopes: array(canonicalApiKeyScopeSchema).check(maxLength(32)),
  last_used_at: optional(timestampSchema),
  expires_at: optional(timestampSchema),
  revoked_at: optional(timestampSchema),
  created_at: timestampSchema,
  updated_at: timestampSchema,
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

export const apiKeyPageEnvelopeSchema = strictObject({
  code: literal(0),
  message: string(),
  data: array(apiKeySchema),
  meta: paginationMetaSchema,
  links: paginationLinksSchema,
});

export const createApiKeyResultSchema = strictObject({
  api_key: apiKeySchema,
  plaintext_key: string().check(
    minLength(16),
    maxLength(512),
    refine(value => value === value.trim())
  ),
});

export const createApiKeySchema = strictObject({
  name: apiKeyNameInputSchema,
  scopes: array(apiKeyScopeInputSchema).check(minLength(1), maxLength(32)),
  expires_at: optional(timestampSchema),
  mode: optional(apiKeyModeSchema),
});

export const apiKeyRouteIdSchema = pipe(
  pipe(string().check(regex(/^[1-9]\d*$/)), transform(Number)),
  safeIdSchema
);

function hasValidNameLength(value: string): boolean {
  const length = Array.from(value).length;
  return length >= 1 && length <= 100;
}
