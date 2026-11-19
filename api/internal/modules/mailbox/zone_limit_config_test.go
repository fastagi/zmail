package mailbox

import (
	"testing"

	"github.com/fastagi/zmail/api/internal/domain"
	"github.com/fastagi/zmail/api/internal/infra/config"
)

func TestZoneLimitCeilingMatchesConfig(t *testing.T) {
	if config.MaxMailboxZonesPerOrganization != domain.MaxMailZonesPerOrganization {
		t.Fatalf("config ceiling %d != domain ceiling %d", config.MaxMailboxZonesPerOrganization, domain.MaxMailZonesPerOrganization)
	}
}
