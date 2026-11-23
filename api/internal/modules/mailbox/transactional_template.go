package mailbox

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
)

const maxTemplateFromName = 100

// templateKeyPattern is the form of a transactional template's API key.
var templateKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// validateTransactionalTemplate checks the fields only a transactional template has: its key,
// sender, and variable declarations, which must match the placeholders exactly. Every
// placeholder is declared and every declaration used; a secret variable is required; a required
// variable never has a fallback, and every placeholder of an optional one has one. It cleans the
// sender in place. Whether the sender's domain is verified is checked by the service.
func validateTransactionalTemplate(template *domain.MailTemplate, parsed *parsedTemplate) error {
	if !templateKeyPattern.MatchString(template.Key) {
		return fmt.Errorf("%w: key", ErrMailTemplateInvalid)
	}
	address, ok := normalizeRecipient(template.FromAddress)
	if !ok {
		return fmt.Errorf("%w: from_address", ErrMailTemplateInvalid)
	}
	template.FromAddress = address
	fromName := strings.Join(strings.Fields(template.FromName), " ")
	if utf8.RuneCountInString(fromName) > maxTemplateFromName || strings.ContainsAny(fromName, "<>\"") ||
		strings.IndexFunc(fromName, unicode.IsControl) >= 0 {
		return fmt.Errorf("%w: from_name", ErrMailTemplateInvalid)
	}
	template.FromName = fromName
	if len(template.Variables) > maxTemplateVariables {
		return fmt.Errorf("%w: variable_definitions", ErrMailTemplateInvalid)
	}
	declared := make(map[string]domain.TransactionalVariable, len(template.Variables))
	for _, variable := range template.Variables {
		if !validVariableName(variable.Name) {
			return fmt.Errorf("%w: variable_definitions name %q", ErrMailTemplateInvalid, variable.Name)
		}
		if _, duplicate := declared[variable.Name]; duplicate {
			return fmt.Errorf("%w: variable_definitions declares %s twice", ErrMailTemplateInvalid, variable.Name)
		}
		if variable.Secret && !variable.Required {
			return fmt.Errorf("%w: variable_definitions secret %s must be required", ErrMailTemplateInvalid, variable.Name)
		}
		declared[variable.Name] = variable
	}
	used := map[string]struct{}{}
	for _, parts := range [][]templatePart{parsed.subject, parsed.html, parsed.text} {
		for _, part := range parts {
			if part.variable == "" {
				continue
			}
			variable, ok := declared[part.variable]
			switch {
			case !ok:
				return fmt.Errorf("%w: placeholder %s is not declared", ErrMailTemplateInvalid, part.variable)
			case variable.Required && part.hasFallback:
				return fmt.Errorf("%w: required variable %s has a fallback", ErrMailTemplateInvalid, part.variable)
			case !variable.Required && !part.hasFallback:
				return fmt.Errorf("%w: optional variable %s needs a fallback", ErrMailTemplateInvalid, part.variable)
			}
			used[part.variable] = struct{}{}
		}
	}
	for name := range declared {
		if _, ok := used[name]; !ok {
			return fmt.Errorf("%w: variable %s is declared but not used", ErrMailTemplateInvalid, name)
		}
	}
	return nil
}

// requireTemplateSender accepts a transactional template's sender when its domain is a verified
// domain of the organization, or, with the log provider, the instance's own domain so local
// development works without one. Any other domain, including another organization's, is
// ErrMailDomainNotVerified.
func (s *service) requireTemplateSender(ctx context.Context, organizationID uint, address string) error {
	name := addressDomain(address)
	if s.settings.Provider == config.MailboxProviderLog && name == s.settings.Domain {
		return nil
	}
	if s.domains == nil {
		return ErrMailDomainNotVerified
	}
	mailDomain, err := s.domains.FindDomainByName(ctx, name)
	switch {
	case err == nil && mailDomain.OrganizationID == organizationID && mailDomain.Status == domain.MailDomainStatusVerified:
		return nil
	case err == nil || errors.Is(err, ErrMailDomainNotFound):
		return ErrMailDomainNotVerified
	default:
		return err
	}
}

// requireTemplateWriter decides who may write a template with this kind and sender: a standard
// template needs the templates grant, a transactional one an owner, an admin, or a developer whose
// grant covers the sender's domain.
func (s *service) requireTemplateWriter(
	ctx context.Context,
	viewer domain.OrganizationContext,
	kind domain.MailTemplateKind,
	fromAddress string,
) error {
	if kind != domain.MailTemplateTransactional {
		if !viewer.CanUse(domain.MemberFeatureTemplates) {
			return domain.ErrOrganizationFeatureNotGranted
		}
		return nil
	}
	if err := s.requireDeveloperSender(ctx, viewer, fromAddress); err != nil {
		if errors.Is(err, domain.ErrOrganizationFeatureNotGranted) {
			return domain.ErrPermissionDenied
		}
		return err
	}
	return nil
}

// requireStandardTemplate refuses a transactional template where only standard templates serve:
// mailbox sends, template tests, and broadcasts.
func requireStandardTemplate(template *domain.MailTemplate) error {
	if template.Kind == domain.MailTemplateTransactional {
		return ErrMailTemplateKindMismatch
	}
	return nil
}
