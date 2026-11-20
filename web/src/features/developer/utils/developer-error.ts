'use client';

import { resolveMailboxErrorKey } from '@/features/mailbox/utils/mailbox-error';
import { ApiErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';
import { useT } from '@/i18n';

type TransactionalErrorKey =
  | 'transactional.disabled'
  | 'transactional.rateLimited'
  | 'transactional.recipientRateLimited'
  | 'transactional.messageNotFound'
  | 'transactional.idempotencyConflict'
  | 'transactional.idempotencyKeyInvalid';

const transactionalKeys: Partial<Record<string, TransactionalErrorKey>> = {
  [ApiErrorCode.MAILBOX_TRANSACTIONAL_DISABLED]: 'transactional.disabled',
  [ApiErrorCode.COMMON_RATE_LIMITED]: 'transactional.rateLimited',
  [ApiErrorCode.MAILBOX_RECIPIENT_RATE_LIMITED]: 'transactional.recipientRateLimited',
  [ApiErrorCode.MAILBOX_TRANSACTIONAL_MESSAGE_NOT_FOUND]: 'transactional.messageNotFound',
  [ApiErrorCode.MAILBOX_IDEMPOTENCY_CONFLICT]: 'transactional.idempotencyConflict',
  [ApiErrorCode.MAILBOX_IDEMPOTENCY_KEY_INVALID]: 'transactional.idempotencyKeyInvalid',
};

const rateLimitKeys: ReadonlySet<TransactionalErrorKey> = new Set([
  'transactional.rateLimited',
  'transactional.recipientRateLimited',
]);

/**
 * Local copy for a developer console failure, branching on `error_code` only. Transactional codes
 * use the `errors` module; a rate limit adds the seconds of `Retry-After` when the API sent it. Every
 * other code falls back to the mailbox copy.
 */
export function useDeveloperErrorMessage(): (error: unknown) => string {
  const te = useT('errors');
  const tm = useT('mailbox');
  return error => {
    const key = error instanceof ApiError ? transactionalKeys[error.errorCode] : undefined;
    if (!key) return tm(resolveMailboxErrorKey(error));
    const message = te(key);
    if (!rateLimitKeys.has(key)) return message;
    const seconds = error instanceof ApiError ? error.retryAfterSeconds : undefined;
    const retry =
      seconds === undefined
        ? te('transactional.retryLater')
        : te('transactional.retryIn', { seconds });
    return `${message} ${retry}`;
  };
}
