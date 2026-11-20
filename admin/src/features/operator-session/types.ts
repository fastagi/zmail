import { z } from 'zod';

export const operatorSchema = z.object({
  id: z.number().int().positive(),
  username: z.string(),
  email: z.string(),
  nickname: z.string(),
});

export const operatorCapabilities = [
  'operator:manage',
  'mail_metrics:read',
  'mail_content:read',
] as const;
export type OperatorCapability = (typeof operatorCapabilities)[number];

/** What a grant from an API older than operator scopes allowed: everything except reading bodies. */
const legacyCapabilities: readonly OperatorCapability[] = ['operator:manage', 'mail_metrics:read'];

export const operatorSessionSchema = z.object({
  operator: operatorSchema,
  csrf_token: z.string().min(16).max(128),
  // Unknown capabilities from a newer API are ignored rather than rejecting the session. An API
  // older than operator scopes sends none; every grant it knows is full, and none reads bodies.
  capabilities: z
    .array(z.string())
    .optional()
    .transform((values) =>
      values === undefined
        ? [...legacyCapabilities]
        : values.filter((value): value is OperatorCapability =>
            (operatorCapabilities as readonly string[]).includes(value),
          ),
    ),
});

export type Operator = z.infer<typeof operatorSchema>;
export type OperatorSession = z.infer<typeof operatorSessionSchema>;

export interface SignInInput {
  identifier: string;
  password: string;
}
