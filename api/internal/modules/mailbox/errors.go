package mailbox

import (
	"errors"
	"fmt"

	"github.com/fastagi/zmail/api/internal/domain"
)

// Domain errors owned by the mailbox starter.
var (
	ErrMailboxNotFound        = errors.New("mailbox not found")
	ErrMailboxAddressTaken    = errors.New("mailbox address is already taken")
	ErrMailboxAddressInvalid  = errors.New("mailbox address is invalid")
	ErrMailboxAddressReserved = errors.New("mailbox address is reserved")
	ErrMailboxLimitReached    = errors.New("mailbox limit reached")
	// ErrMailboxImportDuplicateRow is an import row whose address an earlier row of the file names.
	ErrMailboxImportDuplicateRow = errors.New("mailbox import row repeats an earlier address")
	// ErrMailboxImportMemberNotFound is an import row naming an email of no organization member.
	ErrMailboxImportMemberNotFound = errors.New("mailbox import member is not in the organization")
	// ErrMemberImportEmailInvalid is a roster row whose email is not one address of at most 100 characters.
	ErrMemberImportEmailInvalid = errors.New("member import email is invalid")
	// ErrMemberImportRoleInvalid is a roster row whose role is neither member nor admin.
	ErrMemberImportRoleInvalid = errors.New("member import role must be member or admin")
	// ErrMemberImportDomainRequired is a roster row whose mailbox would be on the shared domain
	// rather than on a verified domain of the organization.
	ErrMemberImportDomainRequired = errors.New("member import mailbox needs a verified organization domain")
	// ErrMailboxAccessInvalid rejects a mailbox type, owner, member, or permission that cannot be applied.
	ErrMailboxAccessInvalid = errors.New("mailbox access is invalid")
	// ErrMailboxAccessConflict rejects an access change the mailbox's current type does not allow.
	ErrMailboxAccessConflict = errors.New("mailbox access conflicts with its type")
	ErrMailMessageNotFound   = errors.New("mail message not found")
	// ErrMailMessageSourceNotFound is a message stored without its raw source; mail apps get a
	// rebuilt message instead.
	ErrMailMessageSourceNotFound = errors.New("mail message source not found")
	// ErrMailAppPasswordNotFound is a mail client password outside the caller's reach or already revoked.
	ErrMailAppPasswordNotFound = errors.New("mail client password not found")
	// ErrMailAppPasswordLimitReached means the member already holds the most mail client passwords.
	ErrMailAppPasswordLimitReached = errors.New("mail client password limit reached")
	// ErrMailClientAccessDisabled means the organization switched mail app sign-in off.
	ErrMailClientAccessDisabled = errors.New("mail client access is disabled for this organization")
	// ErrMailAppPasswordPepperMissing means the instance has no MAIL_APP_PASSWORD_PEPPER.
	ErrMailAppPasswordPepperMissing = errors.New("mail client passwords are not configured")
	// ErrMailAppPasswordAttemptsLimited means too many failed credential checks in the window.
	ErrMailAppPasswordAttemptsLimited = errors.New("too many mail client password attempts")
	// ErrTransactionalMessageNotFound means no transactional message matched the lookup.
	ErrTransactionalMessageNotFound = domain.ErrTransactionalMessageNotFound
	ErrMailMessageInvalid           = domain.ErrMailMessageInvalid
	ErrMailSendLimitReached            = errors.New("mail send limit reached")
	ErrMailSendingPaused               = errors.New("mail sending paused by bounce or complaint rate")
	ErrMailDeliveryFailed              = errors.New("mail delivery failed")
	ErrMailIdempotencyConflict         = errors.New("idempotency key was used with a different mail message")
	ErrMailIdempotencyKeyInvalid       = errors.New("idempotency key is invalid")
	ErrMailSendInProgress              = errors.New("mail message with this idempotency key is still being sent")
	ErrMailSenderNotConfigured         = errors.New("outbound mail is not configured")
	ErrMailInboundDisabled             = errors.New("inbound mail is not configured")
	ErrMailInboundSignature            = errors.New("inbound mail signature is invalid")
	ErrMailInboundUnparseable          = errors.New("inbound mail could not be parsed")
	ErrMailInboundTooLarge             = errors.New("inbound mail is too large")
	ErrMailDeliveryEventInvalid        = errors.New("mail delivery event is invalid")
	ErrMailRecipientNotFound           = errors.New("inbound mail recipient not found")
	ErrMailTemplateNotFound            = errors.New("mail template not found")
	ErrMailDraftNotFound               = errors.New("mail draft not found")
	ErrMailDraftLimitReached           = errors.New("mail draft limit reached")
	ErrMailThreadAssignmentUnavailable = errors.New("only a shared Zmail mailbox assigns its threads")
	ErrMailThreadNoteNotFound          = errors.New("mail thread note not found")
	ErrMailThreadNoteLimitReached      = errors.New("mail thread note limit reached")
	ErrMailTemplateInvalid             = errors.New("mail template is invalid")
	ErrMailTemplateNameTaken           = errors.New("mail template name is already taken")
	ErrMailTemplateLimitReached        = errors.New("mail template limit reached")
	ErrMailTemplateKeyTaken            = errors.New("mail template key is already taken")
	// ErrMailTemplateKindImmutable means a replace tried to change a template's kind.
	ErrMailTemplateKindImmutable = errors.New("mail template kind cannot change")
	// ErrMailTemplateKindMismatch means a send named a template of the wrong kind, such as a
	// transactional template in a mailbox send or a broadcast.
	ErrMailTemplateKindMismatch  = errors.New("mail template kind does not fit this send")
	ErrMailContactNotFound      = domain.ErrMailContactNotFound
	ErrMailContactExists        = domain.ErrMailContactExists
	ErrMailContactInvalid       = domain.ErrMailContactInvalid
	ErrMailContactLimitReached  = domain.ErrMailContactLimitReached
	ErrMailContactListNotFound  = domain.ErrMailContactListNotFound
	ErrMailContactListNameTaken = domain.ErrMailContactListNameTaken
	ErrMailContactListLimit     = domain.ErrMailContactListLimit
	ErrMailContactImportInvalid = domain.ErrMailContactImportInvalid
	ErrMailBroadcastNotFound     = errors.New("mail broadcast not found")
	ErrMailBroadcastInvalid      = errors.New("mail broadcast is invalid")
	ErrMailBroadcastNotEditable  = errors.New("mail broadcast cannot be changed in its current state")
	ErrMailBroadcastNoRecipients = errors.New("mail broadcast has no subscribed recipients")
	ErrMailBroadcastDisabled     = errors.New("mail broadcasts are not configured")
	ErrMailUnsubscribeInvalid    = errors.New("unsubscribe link is invalid")
	ErrMailDomainNotFound        = errors.New("mail domain not found")
	ErrMailDomainInvalid         = errors.New("mail domain name is invalid")
	ErrMailDomainTaken           = errors.New("mail domain is already connected")
	ErrMailDomainLimitReached    = errors.New("mail domain limit reached")
	ErrMailDomainZoneNotFound    = errors.New("no manageable DNS zone contains the mail domain")
	ErrMailDomainInUse           = errors.New("mail domain still has mailboxes")
	ErrMailDomainNotVerified     = errors.New("mail domain is not verified")
	ErrMailDomainsDisabled       = errors.New("organization mail domains are not enabled")
	ErrMailDomainProviderFailed  = errors.New("the DNS provider rejected the request")
	ErrMailFolderNotFound        = errors.New("mail folder not found")
	ErrMailFolderNameTaken       = errors.New("mail folder name is already taken")
	ErrMailFolderLimitReached    = errors.New("mail folder limit reached")
	ErrMailLabelNotFound         = errors.New("mail label not found")
	ErrMailLabelNameTaken        = errors.New("mail label name is already taken")
	ErrMailLabelLimitReached     = errors.New("mail label limit reached")
	ErrMailRoleNotFound          = errors.New("mail role not found")
	ErrMailRoleNameTaken         = errors.New("mail role name is already taken")
	ErrMailRoleLimitReached      = errors.New("mail role limit reached")
	// ErrMailSearchTooBroad is a search the database could not finish within its time limit.
	ErrMailSearchTooBroad     = errors.New("mail search is too broad")
	ErrMailZoneNotFound       = errors.New("mail zone not found")
	ErrMailZoneLimitReached   = errors.New("mail zone limit reached")
	ErrMailZoneInUse          = errors.New("mail zone still has mail domains")
	ErrMailZoneStillDelegated = errors.New("mail zone is still delegated to its assigned name servers")
	ErrMailZonePending        = errors.New("mail zone is not active yet")
	ErrMailZoneCheckLimited   = errors.New("mail zone was checked too recently")
	ErrMailDNSRecordNotFound  = errors.New("DNS record not found")
	ErrMailDNSRecordInvalid   = errors.New("DNS record is invalid")
	ErrMailDNSRecordLocked    = errors.New("DNS record is used by Zmail mail")
	ErrMailDNSRecordRejected  = errors.New("the DNS provider rejected the record")
)

// Access errors that answer with an existing public code.
var (
	// ErrMailboxMemberNotFound is a member row that does not exist; it answers like a missing mailbox.
	ErrMailboxMemberNotFound = fmt.Errorf("%w: member", ErrMailboxNotFound)
	// ErrMailboxMemberLimit refuses a member beyond domain.MaxMailboxMembers.
	ErrMailboxMemberLimit = fmt.Errorf("%w: a shared mailbox has at most 100 members", ErrMailboxAccessInvalid)
)
