package organization

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/fastagi/zmail/api/internal/domain"
	auditstarter "github.com/fastagi/zmail/api/internal/modules/audit"
	httphandler "github.com/fastagi/zmail/api/pkg/handler"
	"github.com/fastagi/zmail/api/pkg/response"
)

// OrganizationIDHeader selects the organization for one context-protected request.
const OrganizationIDHeader = "Organization-Id"

var canonicalOrganizationID = regexp.MustCompile(`^[1-9][0-9]*$`)

// ContextResolver validates transport input and binds a current membership to the request.
type ContextResolver struct {
	service Service
}

// NewContextResolver creates the reusable active-organization middleware.
func NewContextResolver(service Service) *ContextResolver {
	return &ContextResolver{service: service}
}

// Middleware resolves Organization-Id after authentication and before tenant-scoped handlers.
func (r *ContextResolver) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		addVary(c.Writer.Header(), OrganizationIDHeader)

		userID, ok := httphandler.GetUserID(c)
		if !ok {
			c.Abort()
			return
		}

		organizationID, err := parseOrganizationIDHeader(c.Request.Header.Values(OrganizationIDHeader))
		organizationID, err = applyAPIKeyBinding(c, organizationID, err)
		if err != nil {
			abortOrganizationContext(c, err)
			return
		}
		if r == nil || r.service == nil {
			abortOrganizationContext(c, domain.ErrServiceUnavailable)
			return
		}

		resolved, err := r.service.ResolveContext(c.Request.Context(), userID, organizationID)
		if err != nil {
			abortOrganizationContext(c, err)
			return
		}
		if resolved == nil || !resolved.IsValid() {
			abortOrganizationContext(c, domain.ErrServiceUnavailable)
			return
		}

		requestContext := domain.WithOrganizationContext(c.Request.Context(), *resolved)
		c.Request = c.Request.WithContext(requestContext)
		auditstarter.RecordChange(requestContext, auditstarter.Change{
			Metadata: map[string]any{
				"organization_id":            resolved.OrganizationID,
				"organization_membership_id": resolved.MembershipID,
				"organization_role":          resolved.Role,
			},
		})
		c.Next()
	}
}

// applyAPIKeyBinding lets an organization-bound API key omit the header and refuses it in any
// other organization. A malformed header stays invalid; sessions and unrestricted keys pass through.
func applyAPIKeyBinding(c *gin.Context, organizationID uint, err error) (uint, error) {
	bound := domain.APIKeyRestrictionFromContext(c.Request.Context()).OrganizationID
	if bound == 0 {
		return organizationID, err
	}
	switch {
	case errors.Is(err, domain.ErrOrganizationContextRequired):
		return bound, nil
	case err != nil:
		return 0, err
	case organizationID != bound:
		return 0, domain.ErrAPIKeyOrganizationMismatch
	}
	return organizationID, nil
}

func parseOrganizationIDHeader(values []string) (uint, error) {
	if len(values) == 0 {
		return 0, domain.ErrOrganizationContextRequired
	}
	if len(values) != 1 {
		return 0, domain.ErrOrganizationContextInvalid
	}

	value := strings.TrimSpace(values[0])
	if !canonicalOrganizationID.MatchString(value) {
		return 0, domain.ErrOrganizationContextInvalid
	}
	parsed, err := strconv.ParseUint(value, 10, strconv.IntSize)
	if err != nil || parsed == 0 {
		return 0, domain.ErrOrganizationContextInvalid
	}
	return uint(parsed), nil
}

func abortOrganizationContext(c *gin.Context, err error) {
	descriptor := response.DefaultErrorMapper.Resolve(err)
	message := "Failed to resolve organization context"
	switch {
	case errors.Is(err, domain.ErrOrganizationContextRequired):
		message = "Organization context required"
	case errors.Is(err, domain.ErrOrganizationContextInvalid):
		message = "Invalid organization context"
	case errors.Is(err, domain.ErrOrganizationNotFound):
		message = "Organization not found"
	case errors.Is(err, domain.ErrAPIKeyOrganizationMismatch):
		message = "API key is bound to another organization"
	}
	response.AbortWithCode(c, descriptor.StatusCode, descriptor.ErrorCode, message)
}

func addVary(header http.Header, field string) {
	for _, value := range header.Values("Vary") {
		for _, existing := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(existing), field) {
				return
			}
		}
	}
	header.Add("Vary", field)
}
