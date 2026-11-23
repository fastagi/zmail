package mailbox

import (
	"errors"
	"net/http"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/pkg/response"
)

// mailboxErrorCodes is the one table of this starter's domain errors with their public status
// and error code. HTTP errors and import row results both read it.
var mailboxErrorCodes = []struct {
	err    error
	status int
	code   string
}{
	{ErrMailboxNotFound, http.StatusNotFound, domain.CodeMailboxNotFound},
	{ErrMailboxAddressTaken, http.StatusConflict, domain.CodeMailboxAddressTaken},
	{ErrMailboxAddressInvalid, http.StatusUnprocessableEntity, domain.CodeMailboxAddressInvalid},
	{ErrMailboxAddressReserved, http.StatusUnprocessableEntity, domain.CodeMailboxAddressReserved},
	{ErrMailboxLimitReached, http.StatusConflict, domain.CodeMailboxLimitReached},
	{ErrMailboxAccessInvalid, http.StatusUnprocessableEntity, domain.CodeMailboxAccessInvalid},
	{ErrMailboxAccessConflict, http.StatusConflict, domain.CodeMailboxAccessConflict},
	{ErrMailMessageNotFound, http.StatusNotFound, domain.CodeMailMessageNotFound},
	{ErrMailMessageInvalid, http.StatusUnprocessableEntity, domain.CodeMailMessageInvalid},
	{ErrMailSendLimitReached, http.StatusTooManyRequests, domain.CodeMailSendLimitReached},
	{ErrMailSendingPaused, http.StatusForbidden, domain.CodeMailSendingPaused},
	{ErrMailDeliveryFailed, http.StatusBadGateway, domain.CodeMailDeliveryFailed},
	{ErrMailIdempotencyConflict, http.StatusConflict, domain.CodeMailIdempotencyConflict},
	{ErrMailIdempotencyKeyInvalid, http.StatusUnprocessableEntity, domain.CodeMailIdempotencyKeyInvalid},
	{ErrMailSendInProgress, http.StatusConflict, domain.CodeMailSendInProgress},
	{ErrMailSenderNotConfigured, http.StatusServiceUnavailable, domain.CodeMailSenderNotConfigured},
	{ErrMailInboundDisabled, http.StatusServiceUnavailable, domain.CodeMailInboundDisabled},
	{ErrMailInboundSignature, http.StatusUnauthorized, domain.CodeMailInboundSignature},
	{ErrMailInboundUnparseable, http.StatusUnprocessableEntity, domain.CodeMailInboundUnparseable},
	{ErrMailInboundTooLarge, http.StatusRequestEntityTooLarge, domain.CodeMailInboundTooLarge},
	{ErrMailDeliveryEventInvalid, http.StatusUnprocessableEntity, domain.CodeMailDeliveryEventInvalid},
	{ErrMailRecipientNotFound, http.StatusNotFound, domain.CodeMailRecipientNotFound},
	{ErrMailTemplateNotFound, http.StatusNotFound, domain.CodeMailTemplateNotFound},
	{ErrMailDraftNotFound, http.StatusNotFound, domain.CodeMailDraftNotFound},
	{ErrMailDraftLimitReached, http.StatusConflict, domain.CodeMailDraftLimitReached},
	{ErrMailThreadAssignmentUnavailable, http.StatusConflict, domain.CodeMailThreadAssignmentUnavailable},
	{ErrMailThreadNoteNotFound, http.StatusNotFound, domain.CodeMailThreadNoteNotFound},
	{ErrMailThreadNoteLimitReached, http.StatusConflict, domain.CodeMailThreadNoteLimitReached},
	{ErrMailTemplateInvalid, http.StatusUnprocessableEntity, domain.CodeMailTemplateInvalid},
	{ErrMailTemplateNameTaken, http.StatusConflict, domain.CodeMailTemplateNameTaken},
	{ErrMailTemplateLimitReached, http.StatusConflict, domain.CodeMailTemplateLimitReached},
	{ErrMailTemplateKeyTaken, http.StatusConflict, domain.CodeMailTemplateKeyTaken},
	{ErrMailTemplateKindImmutable, http.StatusConflict, domain.CodeMailTemplateKindImmutable},
	{ErrMailTemplateKindMismatch, http.StatusUnprocessableEntity, domain.CodeMailTemplateKindMismatch},
	{ErrMailContactNotFound, http.StatusNotFound, domain.CodeMailContactNotFound},
	{ErrMailContactExists, http.StatusConflict, domain.CodeMailContactExists},
	{ErrMailContactInvalid, http.StatusUnprocessableEntity, domain.CodeMailContactInvalid},
	{ErrMailContactLimitReached, http.StatusConflict, domain.CodeMailContactLimitReached},
	{ErrMailContactListNotFound, http.StatusNotFound, domain.CodeMailContactListNotFound},
	{ErrMailContactListNameTaken, http.StatusConflict, domain.CodeMailContactListNameTaken},
	{ErrMailContactListLimit, http.StatusConflict, domain.CodeMailContactListLimitReached},
	{ErrMailContactImportInvalid, http.StatusUnprocessableEntity, domain.CodeMailContactImportInvalid},
	{ErrMailBroadcastNotFound, http.StatusNotFound, domain.CodeMailBroadcastNotFound},
	{ErrMailBroadcastInvalid, http.StatusUnprocessableEntity, domain.CodeMailBroadcastInvalid},
	{ErrMailBroadcastNotEditable, http.StatusConflict, domain.CodeMailBroadcastNotEditable},
	{ErrMailBroadcastNoRecipients, http.StatusUnprocessableEntity, domain.CodeMailBroadcastNoRecipients},
	{ErrMailBroadcastDisabled, http.StatusServiceUnavailable, domain.CodeMailBroadcastDisabled},
	{ErrMailUnsubscribeInvalid, http.StatusNotFound, domain.CodeMailUnsubscribeInvalid},
	{ErrMailDomainNotFound, http.StatusNotFound, domain.CodeMailDomainNotFound},
	{ErrMailDomainInvalid, http.StatusUnprocessableEntity, domain.CodeMailDomainInvalid},
	{ErrMailDomainTaken, http.StatusConflict, domain.CodeMailDomainTaken},
	{ErrMailDomainLimitReached, http.StatusUnprocessableEntity, domain.CodeMailDomainLimitReached},
	{ErrMailDomainZoneNotFound, http.StatusUnprocessableEntity, domain.CodeMailDomainZoneNotFound},
	{ErrMailDomainInUse, http.StatusConflict, domain.CodeMailDomainInUse},
	{ErrMailDomainNotVerified, http.StatusUnprocessableEntity, domain.CodeMailDomainNotVerified},
	{ErrMailDomainsDisabled, http.StatusNotFound, domain.CodeMailDomainsDisabled},
	{ErrMailDomainProviderFailed, http.StatusBadGateway, domain.CodeMailDomainProviderFailed},
	{ErrMailZoneNotFound, http.StatusNotFound, domain.CodeMailZoneNotFound},
	{ErrMailZonePending, http.StatusConflict, domain.CodeMailZonePending},
	{ErrMailZoneInUse, http.StatusConflict, domain.CodeMailZoneInUse},
	{ErrMailZoneStillDelegated, http.StatusConflict, domain.CodeMailZoneStillDelegated},
	{ErrMailZoneLimitReached, http.StatusUnprocessableEntity, domain.CodeMailZoneLimitReached},
	{ErrMailDNSRecordNotFound, http.StatusNotFound, domain.CodeMailDNSRecordNotFound},
	{ErrMailDNSRecordInvalid, http.StatusUnprocessableEntity, domain.CodeMailDNSRecordInvalid},
	{ErrMailDNSRecordLocked, http.StatusConflict, domain.CodeMailDNSRecordLocked},
	{ErrMailDNSRecordRejected, http.StatusUnprocessableEntity, domain.CodeMailDNSRecordRejected},
	{ErrMailTemplateVariablesMissing, http.StatusUnprocessableEntity, domain.CodeMailTemplateVariablesMissing},
	{ErrMailFolderNotFound, http.StatusNotFound, domain.CodeMailFolderNotFound},
	{ErrMailLabelNotFound, http.StatusNotFound, domain.CodeMailLabelNotFound},
	{ErrMailFolderNameTaken, http.StatusConflict, domain.CodeMailFolderNameTaken},
	{ErrMailLabelNameTaken, http.StatusConflict, domain.CodeMailLabelNameTaken},
	{ErrMailFolderLimitReached, http.StatusUnprocessableEntity, domain.CodeMailFolderLimitReached},
	{ErrMailLabelLimitReached, http.StatusUnprocessableEntity, domain.CodeMailLabelLimitReached},
	{ErrMailRoleNotFound, http.StatusNotFound, domain.CodeMailRoleNotFound},
	{ErrMailRoleNameTaken, http.StatusConflict, domain.CodeMailRoleNameTaken},
	{ErrPlatformAccountNotFound, http.StatusNotFound, domain.CodePlatformAccountNotFound},
	{ErrPlatformAccountTaken, http.StatusConflict, domain.CodePlatformAccountTaken},
	{ErrTriageUnavailable, http.StatusConflict, domain.CodeMailTriageUnavailable},
	{ErrTriageModelFailed, http.StatusBadGateway, domain.CodeMailTriageModelFailed},
	{ErrExternalMailConnectionNotFound, http.StatusNotFound, domain.CodeExternalMailConnectionNotFound},
	{ErrExternalMailAuthorizationNotFound, http.StatusNotFound, domain.CodeExternalMailAuthorizationNotFound},
	{ErrExternalMailAuthorizationInvalid, http.StatusUnprocessableEntity, domain.CodeExternalMailAuthorizationInvalid},
	{ErrExternalMailAuthorizationFailed, http.StatusUnprocessableEntity, domain.CodeExternalMailAuthorizationFailed},
	{ErrExternalMailAccountMismatch, http.StatusConflict, domain.CodeExternalMailAccountMismatch},
	{ErrExternalMailAccountTaken, http.StatusConflict, domain.CodeExternalMailAccountTaken},
	{ErrExternalMailConnectionLimit, http.StatusConflict, domain.CodeExternalMailConnectionLimit},
	{ErrExternalMailProviderUnavailable, http.StatusNotFound, domain.CodeExternalMailProviderUnavailable},
	{ErrExternalMailPushUnauthenticated, http.StatusUnauthorized, response.ErrorCodeUnauthorized},
	{ErrExternalMailPushRateLimited, http.StatusTooManyRequests, domain.CodeRateLimited},
	{domain.ErrExternalMailPushBusy, http.StatusServiceUnavailable, domain.CodeServiceUnavailable},
	{ErrExternalMailServerUnreachable, http.StatusUnprocessableEntity, domain.CodeExternalMailServerUnreachable},
	{ErrExternalMailCredentialsRateLimited, http.StatusTooManyRequests, domain.CodeRateLimited},
	{ErrExternalMailConnectionNotRevoked, http.StatusConflict, domain.CodeExternalMailConnectionNotRevoked},
	// Tenants reuse the connection codes: a tenant is the organization's connection to a whole
	// directory, and each new code costs every Web route bundle bytes.
	{ErrExternalMailTenantNotFound, http.StatusNotFound, domain.CodeExternalMailConnectionNotFound},
	{ErrExternalMailTenantInactive, http.StatusConflict, domain.CodeMailboxAccessConflict},
	{ErrExternalMailDirectoryNotFound, http.StatusConflict, domain.CodeExternalMailAccountMismatch},
	{ErrMailAgentGrantNotFound, http.StatusNotFound, domain.CodeMailAgentGrantNotFound},
	{ErrMailAgentGrantInvalid, http.StatusUnprocessableEntity, domain.CodeMailAgentGrantInvalid},
	{ErrMailAgentGrantLimit, http.StatusConflict, domain.CodeMailAgentGrantLimit},
	{ErrMailAgentGrantExists, http.StatusConflict, domain.CodeMailAgentGrantExists},
	{ErrMailAgentRateLimited, http.StatusTooManyRequests, domain.CodeRateLimited},
	{ErrMailRoleLimitReached, http.StatusUnprocessableEntity, domain.CodeMailRoleLimitReached},
	{ErrTransactionalMessageNotFound, http.StatusNotFound, domain.CodeMailTransactionalMessageNotFound},
	{ErrTransactionalDisabled, http.StatusServiceUnavailable, domain.CodeMailTransactionalDisabled},
	{ErrMailRecipientRateLimited, http.StatusTooManyRequests, domain.CodeMailRecipientRateLimited},
	{ErrTransactionalRateLimited, http.StatusTooManyRequests, domain.CodeRateLimited},
	{ErrMailSearchTooBroad, http.StatusUnprocessableEntity, domain.CodeMailSearchTooBroad},
	{ErrMailboxImportDuplicateRow, http.StatusUnprocessableEntity, domain.CodeMailboxImportDuplicateRow},
	{ErrMailboxImportMemberNotFound, http.StatusUnprocessableEntity, domain.CodeMailboxImportMemberNotFound},
	{ErrMemberImportEmailInvalid, http.StatusUnprocessableEntity, domain.CodeMemberImportEmailInvalid},
	{ErrMemberImportRoleInvalid, http.StatusUnprocessableEntity, domain.CodeMemberImportRoleInvalid},
	{ErrMemberImportDomainRequired, http.StatusUnprocessableEntity, domain.CodeMemberImportDomainRequired},
	{ErrMailAppPasswordNotFound, http.StatusNotFound, domain.CodeMailAppPasswordNotFound},
	{ErrMailAppPasswordLimitReached, http.StatusConflict, domain.CodeMailAppPasswordLimitReached},
	{ErrMailClientAccessDisabled, http.StatusForbidden, domain.CodeMailClientAccessDisabled},
	{ErrMailAppPasswordPepperMissing, http.StatusServiceUnavailable, domain.CodeServiceUnavailable},
	{ErrMailAppPasswordAttemptsLimited, http.StatusTooManyRequests, domain.CodeRateLimited},
}

// RegisterErrorMappings maps this starter's own domain errors to public status and error codes.
func (h *Handler) RegisterErrorMappings(mapper *response.ErrorMapper) {
	for _, entry := range mailboxErrorCodes {
		mapper.Register(entry.err, entry.status, entry.code)
	}
}

// importErrorCode is the public error code of an import row's failure; an error outside the
// table is COMMON.INTERNAL.
func importErrorCode(err error) string {
	for _, entry := range mailboxErrorCodes {
		if errors.Is(err, entry.err) {
			return entry.code
		}
	}
	return response.ErrorCodeInternal
}
