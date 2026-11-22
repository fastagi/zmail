package apikey_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/fastagi/zmail/api/internal/modules/apikey"
	"github.com/fastagi/zmail/api/internal/modules/mailbox"
)

// Every scope a mail route checks must be one a new key can be created with.
func TestKnownScopesCoverMailRoutes(t *testing.T) {
	for _, scope := range []string{
		mailbox.ScopeMailSend,
		mailbox.ScopeMailRead,
		mailbox.ScopeMailTransactional,
		mailbox.ScopeMailAgent,
	} {
		assert.Contains(t, apikey.KnownScopes(), scope)
	}
}
