package mailbox

import "errors"

// Domain errors owned by the mailbox starter.
var (
	ErrMailboxNotFound         = errors.New("mailbox not found")
	ErrMailboxAddressTaken     = errors.New("mailbox address is already taken")
	ErrMailboxAddressInvalid   = errors.New("mailbox address is invalid")
	ErrMailboxAddressReserved  = errors.New("mailbox address is reserved")
	ErrMailboxLimitReached     = errors.New("mailbox limit reached")
	ErrMailMessageNotFound     = errors.New("mail message not found")
	ErrMailMessageInvalid      = errors.New("mail message is invalid")
	ErrMailSendLimitReached    = errors.New("mail send limit reached")
	ErrMailDeliveryFailed      = errors.New("mail delivery failed")
	ErrMailSenderNotConfigured = errors.New("outbound mail is not configured")
	ErrMailInboundDisabled     = errors.New("inbound mail is not configured")
	ErrMailInboundSignature    = errors.New("inbound mail signature is invalid")
	ErrMailInboundUnparseable  = errors.New("inbound mail could not be parsed")
	ErrMailInboundTooLarge     = errors.New("inbound mail is too large")
	ErrMailRecipientNotFound   = errors.New("inbound mail recipient not found")
)
