package setting

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fastagi/zmail/api/internal/domain"
)

func TestRepositoryStampsSettingWritesInUTC(t *testing.T) {
	fixture := newSettingTestFixture(t)
	pacific := time.FixedZone("PST", -8*60*60)
	instant := time.Date(2026, time.November, 21, 14, 46, 50, 0, pacific)
	fixture.repository.now = func() time.Time { return instant }
	target := domain.SettingTarget{Scope: domain.SettingScopeUser, SubjectID: fixture.user.ID}
	ctx := context.Background()

	created, changed, err := fixture.repository.Set(ctx, target, "localization.locale", `"zh-Hans"`, 0)
	require.NoError(t, err)
	require.True(t, changed)
	assert.Equal(t, time.UTC, created.UpdatedAt.Location())
	assert.True(t, instant.Equal(created.UpdatedAt))

	updated, changed, err := fixture.repository.Set(ctx, target, "localization.locale", `"en-US"`, 1)
	require.NoError(t, err)
	require.True(t, changed)
	assert.Equal(t, time.UTC, updated.UpdatedAt.Location())
}

func TestSettingResponseSerializesUpdatedAtInUTC(t *testing.T) {
	local := time.Date(2026, time.November, 21, 14, 46, 50, 699150000, time.FixedZone("PST", -8*60*60))
	encoded, err := json.Marshal(toSettingResponse(&domain.Setting{
		Scope:     domain.SettingScopeApp,
		Key:       "branding.display_name",
		Version:   1,
		Source:    domain.SettingSourceOverride,
		UpdatedAt: &local,
	}))
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"updated_at":"2026-11-21T22:46:50.69915Z"`)

	encoded, err = json.Marshal(toSettingResponse(&domain.Setting{Key: "branding.display_name"}))
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"updated_at":null`)
}
