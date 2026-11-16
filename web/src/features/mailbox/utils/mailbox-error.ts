import { ApiErrorCode, ClientErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';

export type MailboxErrorKey =
  | 'errors.addressInvalid'
  | 'errors.attachmentsInvalid'
  | 'errors.addressReserved'
  | 'errors.addressTaken'
  | 'errors.deliveryFailed'
  | 'errors.generic'
  | 'errors.invalidInput'
  | 'errors.invalidResponse'
  | 'errors.limitReached'
  | 'errors.mailboxNotFound'
  | 'errors.messageInvalid'
  | 'errors.messageNotFound'
  | 'errors.organizationNotFound'
  | 'errors.permissionDenied'
  | 'errors.sendLimitReached'
  | 'errors.senderNotConfigured'
  | 'errors.tooLarge'
  | 'errors.unavailable';

const errorKeys: Partial<Record<string, MailboxErrorKey>> = {
  [ClientErrorCode.INVALID_RESPONSE]: 'errors.invalidResponse',
  [ClientErrorCode.NETWORK_ERROR]: 'errors.unavailable',
  [ClientErrorCode.TIMEOUT]: 'errors.unavailable',
  [ApiErrorCode.COMMON_TIMEOUT]: 'errors.unavailable',
  [ApiErrorCode.COMMON_SERVICE_UNAVAILABLE]: 'errors.unavailable',
  [ApiErrorCode.COMMON_VALIDATION_FAILED]: 'errors.invalidInput',
  [ApiErrorCode.COMMON_INVALID_INPUT]: 'errors.invalidInput',
  [ApiErrorCode.COMMON_REQUEST_TOO_LARGE]: 'errors.tooLarge',
  [ApiErrorCode.PERMISSION_DENIED]: 'errors.permissionDenied',
  [ApiErrorCode.ORGANIZATION_NOT_FOUND]: 'errors.organizationNotFound',
  [ApiErrorCode.MAILBOX_NOT_FOUND]: 'errors.mailboxNotFound',
  [ApiErrorCode.MAILBOX_MESSAGE_NOT_FOUND]: 'errors.messageNotFound',
  [ApiErrorCode.MAILBOX_ADDRESS_TAKEN]: 'errors.addressTaken',
  [ApiErrorCode.MAILBOX_ADDRESS_INVALID]: 'errors.addressInvalid',
  [ApiErrorCode.MAILBOX_ADDRESS_RESERVED]: 'errors.addressReserved',
  [ApiErrorCode.MAILBOX_LIMIT_REACHED]: 'errors.limitReached',
  [ApiErrorCode.MAILBOX_MESSAGE_INVALID]: 'errors.messageInvalid',
  [ApiErrorCode.MAILBOX_SEND_LIMIT_REACHED]: 'errors.sendLimitReached',
  [ApiErrorCode.MAILBOX_DELIVERY_FAILED]: 'errors.deliveryFailed',
  [ApiErrorCode.MAILBOX_SENDER_NOT_CONFIGURED]: 'errors.senderNotConfigured',
};

/** Maps a failure to stable local copy; backend message text is never shown. */
export function resolveMailboxErrorKey(error: unknown): MailboxErrorKey {
  if (!(error instanceof ApiError)) return 'errors.generic';
  return errorKeys[error.errorCode] ?? 'errors.generic';
}

/** True when the API no longer finds the organization for this member: access was lost. */
export function isOrganizationLost(error: unknown): boolean {
  return error instanceof ApiError && error.errorCode === ApiErrorCode.ORGANIZATION_NOT_FOUND;
}

/**
 * True when the API stored the outbound message as `failed` before answering, so the sent folder
 * has a new entry even though the request failed.
 */
export function isStoredSendFailure(error: unknown): boolean {
  return (
    error instanceof ApiError &&
    (error.errorCode === ApiErrorCode.MAILBOX_DELIVERY_FAILED ||
      error.errorCode === ApiErrorCode.MAILBOX_SENDER_NOT_CONFIGURED)
  );
}
