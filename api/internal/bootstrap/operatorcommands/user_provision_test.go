package operatorcommands

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fastagi/zmail/api/internal/domain"
)

type fakeUserProvisioner struct {
	got     domain.UserProvisionRequest
	created bool
	err     error
}

func (f *fakeUserProvisioner) ProvisionUser(_ context.Context, req domain.UserProvisionRequest) (*domain.User, bool, error) {
	f.got = req
	if f.err != nil {
		return nil, false, f.err
	}
	return &domain.User{ID: 7, Email: req.Email, Username: "z-zmail", Status: 1}, f.created, nil
}

type provisionAuditRecorder struct{ logs []*domain.AuditLog }

func (f *provisionAuditRecorder) Record(_ context.Context, log *domain.AuditLog) error {
	f.logs = append(f.logs, log)
	return nil
}

func TestParseUserProvisionArguments(t *testing.T) {
	parsed, err := parseUserProvisionArguments([]string{"z@zmail.im", "--username=ops"})
	require.NoError(t, err)
	assert.Equal(t, userProvisionArguments{email: "z@zmail.im", username: "ops"}, parsed)

	for _, args := range [][]string{nil, {"a@b.c", "secret-in-args"}, {"a@b.c", "--password=x"}} {
		_, err := parseUserProvisionArguments(args)
		assert.Error(t, err, "%v", args)
	}
}

func TestReadProvisionPassword(t *testing.T) {
	password, err := readProvisionPassword(strings.NewReader("p@ss word-123\r\nignored"))
	require.NoError(t, err)
	assert.Equal(t, "p@ss word-123", password)

	_, err = readProvisionPassword(strings.NewReader(""))
	assert.Error(t, err)
	_, err = readProvisionPassword(strings.NewReader(strings.Repeat("x", maxProvisionPasswordLine+5)))
	assert.Error(t, err)
}

func TestRunUserProvisionReportsWithoutThePassword(t *testing.T) {
	const password = "never-print-this-secret"
	for _, created := range []bool{true, false} {
		provisioner := &fakeUserProvisioner{created: created}
		audit := &provisionAuditRecorder{}
		var out bytes.Buffer

		err := runUserProvision(context.Background(), provisioner, audit, userProvisionArguments{email: "z@zmail.im"}, password, &out)
		require.NoError(t, err)
		assert.Equal(t, password, provisioner.got.Password)
		assert.NotContains(t, out.String(), password)
		assert.Contains(t, out.String(), "z@zmail.im")
		require.Len(t, audit.logs, 1)
		assert.Equal(t, "7", audit.logs[0].TargetID)
		assert.NotContains(t, audit.logs[0].Metadata, "password")
	}
}

func TestRunUserProvisionWrapsFailures(t *testing.T) {
	provisioner := &fakeUserProvisioner{err: domain.ErrUsernameAlreadyExists}
	err := runUserProvision(context.Background(), provisioner, nil, userProvisionArguments{email: "z@zmail.im"}, "long-enough-secret", &bytes.Buffer{})
	assert.True(t, errors.Is(err, domain.ErrUsernameAlreadyExists))
}
