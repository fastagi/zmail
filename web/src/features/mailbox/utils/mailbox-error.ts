import { ApiErrorCode, ClientErrorCode } from '@/http/codes';
import { ApiError } from '@/http/request';

export type MailboxErrorKey =
  | 'errors.accessConflict'
  | 'errors.accessInvalid'
  | 'errors.addressInvalid'
  | 'errors.attachmentsInvalid'
  | 'errors.broadcastDisabled'
  | 'errors.broadcastInvalid'
  | 'errors.broadcastNoRecipients'
  | 'errors.broadcastNotEditable'
  | 'errors.broadcastNotFound'
  | 'errors.contactExists'
  | 'errors.contactImportInvalid'
  | 'errors.contactInvalid'
  | 'errors.contactLimitReached'
  | 'errors.contactListLimitReached'
  | 'errors.contactListNameTaken'
  | 'errors.contactListNotFound'
  | 'errors.contactNotFound'
  | 'errors.addressReserved'
  | 'errors.addressTaken'
  | 'errors.deliveryFailed'
  | 'errors.domainInUse'
  | 'errors.domainInvalid'
  | 'errors.domainLimitReached'
  | 'errors.domainNotFound'
  | 'errors.domainNotVerified'
  | 'errors.domainProviderFailed'
  | 'errors.domainTaken'
  | 'errors.domainZoneNotFound'
  | 'errors.domainsDisabled'
  | 'errors.dnsRecordInvalid'
  | 'errors.dnsRecordLocked'
  | 'errors.dnsRecordNotFound'
  | 'errors.dnsRecordRejected'
  | 'errors.generic'
  | 'errors.importDuplicateRow'
  | 'errors.invalidInput'
  | 'errors.invalidResponse'
  | 'errors.limitReached'
  | 'errors.mailboxNotFound'
  | 'errors.messageInvalid'
  | 'errors.messageNotFound'
  | 'errors.organizationNotFound'
  | 'errors.createDenied'
  | 'errors.permissionDenied'
  | 'errors.platformAccountNotFound'
  | 'errors.platformAccountTaken'
  | 'errors.sendLimitReached'
  | 'errors.senderNotConfigured'
  | 'errors.templateInvalid'
  | 'errors.templateKeyTaken'
  | 'errors.templateKindImmutable'
  | 'errors.templateKindMismatch'
  | 'errors.templateLimitReached'
  | 'errors.templateNameTaken'
  | 'errors.templateNotFound'
  | 'errors.templateVariablesMissing'
  | 'errors.tooLarge'
  | 'errors.unavailable'
  | 'errors.folderNotFound'
  | 'errors.labelNotFound'
  | 'errors.folderNameTaken'
  | 'errors.labelNameTaken'
  | 'errors.folderLimitReached'
  | 'errors.labelLimitReached'
  | 'errors.searchTooBroad'
  | 'errors.roleNotFound'
  | 'errors.roleNameTaken'
  | 'errors.roleLimitReached'
  | 'errors.zoneInUse'
  | 'errors.zoneLimitReached'
  | 'errors.zoneNotFound'
  | 'errors.zonePending'
  | 'errors.zoneStillDelegated';

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
  [ApiErrorCode.MAILBOX_ACCESS_INVALID]: 'errors.accessInvalid',
  [ApiErrorCode.MAILBOX_ACCESS_CONFLICT]: 'errors.accessConflict',
  [ApiErrorCode.MAILBOX_IMPORT_DUPLICATE_ROW]: 'errors.importDuplicateRow',
  [ApiErrorCode.MAILBOX_MESSAGE_INVALID]: 'errors.messageInvalid',
  [ApiErrorCode.MAILBOX_SEND_LIMIT_REACHED]: 'errors.sendLimitReached',
  [ApiErrorCode.MAILBOX_DELIVERY_FAILED]: 'errors.deliveryFailed',
  [ApiErrorCode.MAILBOX_SENDER_NOT_CONFIGURED]: 'errors.senderNotConfigured',
  [ApiErrorCode.MAILBOX_BROADCAST_NOT_FOUND]: 'errors.broadcastNotFound',
  [ApiErrorCode.MAILBOX_BROADCAST_INVALID]: 'errors.broadcastInvalid',
  [ApiErrorCode.MAILBOX_BROADCAST_NOT_EDITABLE]: 'errors.broadcastNotEditable',
  [ApiErrorCode.MAILBOX_BROADCAST_NO_RECIPIENTS]: 'errors.broadcastNoRecipients',
  [ApiErrorCode.MAILBOX_BROADCAST_DISABLED]: 'errors.broadcastDisabled',
  [ApiErrorCode.MAILBOX_CONTACT_NOT_FOUND]: 'errors.contactNotFound',
  [ApiErrorCode.MAILBOX_CONTACT_EXISTS]: 'errors.contactExists',
  [ApiErrorCode.MAILBOX_CONTACT_INVALID]: 'errors.contactInvalid',
  [ApiErrorCode.MAILBOX_CONTACT_LIMIT_REACHED]: 'errors.contactLimitReached',
  [ApiErrorCode.MAILBOX_CONTACT_LIST_NOT_FOUND]: 'errors.contactListNotFound',
  [ApiErrorCode.MAILBOX_CONTACT_LIST_NAME_TAKEN]: 'errors.contactListNameTaken',
  [ApiErrorCode.MAILBOX_CONTACT_LIST_LIMIT_REACHED]: 'errors.contactListLimitReached',
  [ApiErrorCode.MAILBOX_CONTACT_IMPORT_INVALID]: 'errors.contactImportInvalid',
  [ApiErrorCode.MAILBOX_TEMPLATE_NOT_FOUND]: 'errors.templateNotFound',
  [ApiErrorCode.MAILBOX_TEMPLATE_INVALID]: 'errors.templateInvalid',
  [ApiErrorCode.MAILBOX_TEMPLATE_NAME_TAKEN]: 'errors.templateNameTaken',
  [ApiErrorCode.MAILBOX_TEMPLATE_KEY_TAKEN]: 'errors.templateKeyTaken',
  [ApiErrorCode.MAILBOX_TEMPLATE_KIND_IMMUTABLE]: 'errors.templateKindImmutable',
  [ApiErrorCode.MAILBOX_TEMPLATE_KIND_MISMATCH]: 'errors.templateKindMismatch',
  [ApiErrorCode.MAILBOX_TEMPLATE_LIMIT_REACHED]: 'errors.templateLimitReached',
  [ApiErrorCode.MAILBOX_TEMPLATE_VARIABLES_MISSING]: 'errors.templateVariablesMissing',
  [ApiErrorCode.MAILBOX_DOMAIN_NOT_FOUND]: 'errors.domainNotFound',
  [ApiErrorCode.MAILBOX_DOMAIN_INVALID]: 'errors.domainInvalid',
  [ApiErrorCode.MAILBOX_DOMAIN_TAKEN]: 'errors.domainTaken',
  [ApiErrorCode.MAILBOX_DOMAIN_LIMIT_REACHED]: 'errors.domainLimitReached',
  [ApiErrorCode.MAILBOX_DOMAIN_ZONE_NOT_FOUND]: 'errors.domainZoneNotFound',
  [ApiErrorCode.MAILBOX_DOMAIN_IN_USE]: 'errors.domainInUse',
  [ApiErrorCode.MAILBOX_DOMAIN_NOT_VERIFIED]: 'errors.domainNotVerified',
  [ApiErrorCode.MAILBOX_DOMAINS_DISABLED]: 'errors.domainsDisabled',
  [ApiErrorCode.MAILBOX_DOMAIN_PROVIDER_FAILED]: 'errors.domainProviderFailed',
  [ApiErrorCode.MAILBOX_FOLDER_NOT_FOUND]: 'errors.folderNotFound',
  [ApiErrorCode.MAILBOX_LABEL_NOT_FOUND]: 'errors.labelNotFound',
  [ApiErrorCode.MAILBOX_FOLDER_NAME_TAKEN]: 'errors.folderNameTaken',
  [ApiErrorCode.MAILBOX_LABEL_NAME_TAKEN]: 'errors.labelNameTaken',
  [ApiErrorCode.MAILBOX_FOLDER_LIMIT_REACHED]: 'errors.folderLimitReached',
  [ApiErrorCode.MAILBOX_LABEL_LIMIT_REACHED]: 'errors.labelLimitReached',
  [ApiErrorCode.MAILBOX_SEARCH_TOO_BROAD]: 'errors.searchTooBroad',
  [ApiErrorCode.MAILBOX_ROLE_NOT_FOUND]: 'errors.roleNotFound',
  [ApiErrorCode.MAILBOX_ROLE_NAME_TAKEN]: 'errors.roleNameTaken',
  [ApiErrorCode.MAILBOX_ROLE_LIMIT_REACHED]: 'errors.roleLimitReached',
  [ApiErrorCode.MAILBOX_PLATFORM_ACCOUNT_NOT_FOUND]: 'errors.platformAccountNotFound',
  [ApiErrorCode.MAILBOX_PLATFORM_ACCOUNT_TAKEN]: 'errors.platformAccountTaken',
  [ApiErrorCode.MAILBOX_ZONE_NOT_FOUND]: 'errors.zoneNotFound',
  [ApiErrorCode.MAILBOX_ZONE_PENDING]: 'errors.zonePending',
  [ApiErrorCode.MAILBOX_ZONE_IN_USE]: 'errors.zoneInUse',
  [ApiErrorCode.MAILBOX_ZONE_STILL_DELEGATED]: 'errors.zoneStillDelegated',
  [ApiErrorCode.MAILBOX_ZONE_LIMIT_REACHED]: 'errors.zoneLimitReached',
  [ApiErrorCode.MAILBOX_DNS_RECORD_NOT_FOUND]: 'errors.dnsRecordNotFound',
  [ApiErrorCode.MAILBOX_DNS_RECORD_INVALID]: 'errors.dnsRecordInvalid',
  [ApiErrorCode.MAILBOX_DNS_RECORD_LOCKED]: 'errors.dnsRecordLocked',
  [ApiErrorCode.MAILBOX_DNS_RECORD_REJECTED]: 'errors.dnsRecordRejected',
};

/**
 * Maps an import row's error code to local copy; an unknown code is the generic failure. The copy
 * of `MAILBOX.IMPORT_MEMBER_NOT_FOUND` names the missing emails (`{emails}`).
 */
export function importRowErrorKey(
  code: string | null
): MailboxErrorKey | 'admin.importDialog.memberNotFound' {
  if (code === ApiErrorCode.MAILBOX_IMPORT_MEMBER_NOT_FOUND) {
    return 'admin.importDialog.memberNotFound';
  }
  return (code === null ? undefined : errorKeys[code]) ?? 'errors.generic';
}

/** Maps a failure to stable local copy; backend message text is never shown. */
export function resolveMailboxErrorKey(error: unknown): MailboxErrorKey {
  if (!(error instanceof ApiError)) return 'errors.generic';
  return errorKeys[error.errorCode] ?? 'errors.generic';
}

/** The API's own prefix in front of Cloudflare's reason; the page has its own wording for it. */
const rejectionPrefix = /^the dns provider rejected the record:\s*/iu;

/**
 * Cloudflare's reason for refusing a DNS record, the one backend text the page shows: the adapter
 * forwards it for `MAILBOX.DNS_RECORD_REJECTED` only. Empty when there is none.
 */
export function dnsRecordRejectionReason(error: unknown): string {
  if (!(error instanceof ApiError)) return '';
  if (error.errorCode !== ApiErrorCode.MAILBOX_DNS_RECORD_REJECTED) return '';
  return error.message.replace(rejectionPrefix, '').trim();
}

/** True when the mailbox's access changed since the console loaded it: a refresh shows it. */
export function isAccessConflict(error: unknown): boolean {
  return error instanceof ApiError && error.errorCode === ApiErrorCode.MAILBOX_ACCESS_CONFLICT;
}

/** True when the caller lacks the capability, such as access lost since the page loaded. */
export function isPermissionDenied(error: unknown): boolean {
  return error instanceof ApiError && error.errorCode === ApiErrorCode.PERMISSION_DENIED;
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
