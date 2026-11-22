/**
 * The scopes a new key may carry (`contracts/API_KEYS.md`), in the order the create dialog offers
 * them. `mail:send` is the one most developers need, so a new key starts with it.
 */
export const API_KEY_SCOPES = ['mail:send', 'mail:read', 'mail:transactional', 'mail:agent'] as const;

export type ApiKeyScope = (typeof API_KEY_SCOPES)[number];

export const DEFAULT_API_KEY_SCOPES: readonly ApiKeyScope[] = ['mail:send'];

/** The message key of each scope's label and description. */
export const API_KEY_SCOPE_MESSAGE_KEYS = {
  'mail:send': 'send',
  'mail:read': 'read',
  'mail:transactional': 'transactional',
  'mail:agent': 'agent',
} as const satisfies Record<ApiKeyScope, string>;

/** What the API accepts on create: a named scope, or the explicit `*` wildcard. */
export const CREATABLE_API_KEY_SCOPES = [...API_KEY_SCOPES, '*'] as const;
