package smtpgateway

import (
	"context"
	"errors"
	"strconv"

	"github.com/emersion/go-smtp"

	"github.com/fastagi/zmail/api/internal/domain"
)

// outcomeError is the metric outcome of a submission the store could not finish.
const outcomeError = "error"

func reply(code int, class, subject, detail int, message string) *smtp.SMTPError {
	return &smtp.SMTPError{Code: code, EnhancedCode: smtp.EnhancedCode{class, subject, detail}, Message: message}
}

// Fixed replies. None names an address, a subject, or anything else a mail app sent.
var (
	replyAuthFailed        = reply(535, 5, 7, 8, "Authentication credentials invalid")
	replyAuthLocked        = reply(454, 4, 7, 0, "Too many sign-in attempts, try again later")
	replyAuthUnavailable   = reply(454, 4, 7, 0, "Temporary authentication failure")
	replyAuthRequired      = reply(530, 5, 7, 0, "Authentication required")
	replyRevoked           = reply(421, 4, 7, 0, "This app password is no longer valid")
	replySenderNotAllowed  = reply(550, 5, 7, 1, "You may not send from this address")
	replyBadRecipient      = reply(501, 5, 1, 3, "Bad recipient address syntax")
	replyTooManyRecipients = reply(452, 4, 5, 3, "Too many recipients, at most 50 per message")
	replyTemporary         = reply(451, 4, 3, 0, "Temporary server error, try again later")
)

func replyTooLarge(reason string) *smtp.SMTPError { return reply(552, 5, 3, 4, reason) }

// replyAccepted is the 250 that ends DATA; go-smtp writes the code of an *SMTPError as it is.
func replyAccepted(result *domain.MailClientSubmissionResult) *smtp.SMTPError {
	if result.Status == domain.MailClientSubmissionQueued {
		return reply(250, 2, 0, 0, "Queued: sent when the daily limit allows, id "+strconv.FormatUint(uint64(result.MessageID), 10))
	}
	return reply(250, 2, 0, 0, "Ok: sent, id "+strconv.FormatUint(uint64(result.MessageID), 10))
}

// replyFor turns a refused submission into its SMTP reply (plan R6) and names the outcome a
// metric counts. An error the store did not classify is temporary.
func replyFor(err error) (*smtp.SMTPError, string) {
	var refused *domain.MailClientSubmitError
	if !errors.As(err, &refused) {
		return replyTemporary, outcomeError
	}
	outcome := string(refused.Kind)
	switch refused.Kind {
	case domain.MailClientSubmitSenderNotAllowed:
		return replySenderNotAllowed, outcome
	case domain.MailClientSubmitTooLarge:
		if refused.Reason == "More than 50 recipients" {
			return replyTooManyRecipients, outcome
		}
		return replyTooLarge(reasonOr(refused, "The message is too large")), outcome
	case domain.MailClientSubmitInvalid:
		return reply(554, 5, 6, 0, reasonOr(refused, "The message cannot be sent as it is")), outcome
	case domain.MailClientSubmitConflict:
		return reply(554, 5, 6, 0, reasonOr(refused, "A different message was already sent with this Message-ID")), outcome
	case domain.MailClientSubmitLimitReached:
		return reply(452, 4, 3, 1, "Daily send limit reached, try later"), outcome
	case domain.MailClientSubmitPaused:
		return reply(550, 5, 7, 1, "Sending paused for this organization"), outcome
	case domain.MailClientSubmitDeliveryFailed:
		return reply(451, 4, 4, 1, reasonOr(refused, "The mail provider did not accept the message, try later")), outcome
	case domain.MailClientSubmitInProgress:
		return reply(451, 4, 4, 1, "An earlier attempt of this message is still being sent"), outcome
	case domain.MailClientSubmitNotConfigured:
		return reply(451, 4, 3, 5, "Sending is not set up on this server"), outcome
	}
	return replyTemporary, outcomeError
}

func reasonOr(refused *domain.MailClientSubmitError, fallback string) string {
	if refused.Reason != "" {
		return refused.Reason
	}
	return fallback
}

// errorKind names an error for a log line without its text, which could quote a value.
func errorKind(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, domain.ErrServiceUnavailable):
		return "unavailable"
	}
	return "internal"
}
