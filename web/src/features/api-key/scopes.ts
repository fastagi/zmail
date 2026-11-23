/**
 * The scopes a new key may carry (`contracts/API_KEYS.md`), in the order the create dialog offers
 * them. `mail:send` is the one most developers need, so a new key starts with it.
 */
export const API_KEY_SCOPES = [
  'mail:send',
  'mail:read',
  'mail:transactional',
  'mail:agent',
  'templates:create',
] as const;

export type ApiKeyScope = (typeof API_KEY_SCOPES)[number];

export const DEFAULT_API_KEY_SCOPES: readonly ApiKeyScope[] = ['mail:send'];

/** The message key of each scope's label and description. */
export const API_KEY_SCOPE_MESSAGE_KEYS = {
  'mail:send': 'send',
  'mail:read': 'read',
  'mail:transactional': 'transactional',
  'mail:agent': 'agent',
  'templates:create': 'templatesCreate',
} as const satisfies Record<ApiKeyScope, string>;

/** What the API accepts on create: a named scope, or the explicit `*` wildcard. */
export const CREATABLE_API_KEY_SCOPES = [...API_KEY_SCOPES, '*'] as const;

/**
 * What a new key is for. The presets cover the common first keys; `custom` picks scopes one by one
 * from {@link API_KEY_SCOPES}.
 */
export const API_KEY_PURPOSES = ['send', 'sendRead', 'custom'] as const;

export type ApiKeyPurpose = (typeof API_KEY_PURPOSES)[number];

export const API_KEY_PURPOSE_SCOPES = {
  send: ['mail:send'],
  // `mail:read` looks up sent messages and their delivery status.
  sendRead: ['mail:send', 'mail:read'],
} as const satisfies Record<Exclude<ApiKeyPurpose, 'custom'>, readonly ApiKeyScope[]>;

/** How long a new key lives: a preset, no expiry, or a chosen date. */
export const API_KEY_EXPIRY_CHOICES = ['d30', 'd90', 'never', 'custom'] as const;

export type ApiKeyExpiryChoice = (typeof API_KEY_EXPIRY_CHOICES)[number];

export const API_KEY_EXPIRY_PRESET_DAYS = { d30: 30, d90: 90 } as const;
