package mailbox

import (
	"time"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
	"github.com/fastagi/zmail/api/internal/modules/mailbox/triage"
)

const (
	triageBodyLimit    = triage.BodyLimit
	triageExampleLimit = triage.ExampleLimit
	draftThreadLimit   = triage.DraftThreadLimit
	draftMessageLimit  = triage.DraftMessageLimit
	draftTemplateLimit = triage.DraftTemplateLimit
	maxDraftBodyLength = triage.MaxDraftBodyLength
)

var (
	// ErrTriageUnavailable means AI triage is not offered: no provider, or a switch is off.
	ErrTriageUnavailable = triage.ErrUnavailable
	// ErrTriageModelFailed means the model call failed or returned nothing usable.
	ErrTriageModelFailed = triage.ErrModelFailed
)

type (
	TriageExample = triage.Example
	TriageInput   = triage.Input
	TriageOutput  = triage.Output
	DraftMessage  = triage.DraftMessage
	DraftTemplate = triage.DraftTemplate
	DraftInput    = triage.DraftInput
	DraftBasis    = triage.DraftBasis
	DraftOutput   = triage.DraftOutput
	TriageModel         = triage.Model
	disabledTriageModel = triage.DisabledModel
)

const triageCallTimeout = triage.CallTimeout

func parseTriageDue(raw string, now time.Time) (time.Time, bool) {
	return triage.ParseDue(raw, now)
}

type mailboxSecretMasker struct{}

func (mailboxSecretMasker) MaskInbound(message *domain.MailMessage) triage.MaskedContent {
	masked := maskInboundSecrets(message)
	return triage.MaskedContent{
		Subject:  masked.Subject,
		TextBody: masked.TextBody,
		HTMLBody: masked.HTMLBody,
	}
}

func (mailboxSecretMasker) MaskText(text string) (string, bool) {
	return maskSecretsInText(text)
}

func newTriageModel(cfg config.AITriageConfig) TriageModel {
	return triage.NewModel(cfg)
}

func buildTriageInput(message *domain.MailMessage, roleName string, examples []domain.MailTriageExample) TriageInput {
	return triage.BuildInput(message, roleName, examples, mailboxSecretMasker{})
}

func buildDraftInput(thread []*domain.MailMessage, templates []*domain.MailTemplate, roleName string) DraftInput {
	return triage.BuildDraftInput(thread, templates, roleName, mailboxSecretMasker{})
}

func validateTriageOutput(output TriageOutput, now time.Time) (domain.MailTriageCategory, string, *time.Time) {
	return triage.ValidateOutput(output, now, mailboxSecretMasker{})
}

func validateDraftOutput(output DraftOutput, input DraftInput) (DraftOutput, error) {
	return triage.ValidateDraftOutput(output, input, mailboxSecretMasker{})
}
