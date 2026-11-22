package webhook

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/fastagi/zmail/api/internal/domain"
)

type staticIPResolver struct {
	addresses map[string][]net.IPAddr
	err       error
}

func (r staticIPResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.addresses[host], nil
}

type recordingDialer struct {
	address string
}

func (d *recordingDialer) DialContext(_ context.Context, _, address string) (net.Conn, error) {
	d.address = address
	left, right := net.Pipe()
	_ = right.Close()
	return left, nil
}

func TestTargetPolicyNormalizesAndRequiresEveryResolvedAddressToBePublic(t *testing.T) {
	policy := NewTargetPolicy(nil)
	policy.resolver = staticIPResolver{addresses: map[string][]net.IPAddr{
		"hooks.example.com": {{IP: net.ParseIP("8.8.8.8")}},
	}}

	normalized, hash, err := policy.Normalize(context.Background(), "https://HOOKS.example.com:443/deliver")
	require.NoError(t, err)
	assert.Equal(t, "https://hooks.example.com/deliver", normalized)
	assert.Len(t, hash, 64)

	policy.resolver = staticIPResolver{addresses: map[string][]net.IPAddr{
		"hooks.example.com": {
			{IP: net.ParseIP("8.8.8.8")},
			{IP: net.ParseIP("127.0.0.1")},
		},
	}}
	_, _, err = policy.Normalize(context.Background(), "https://hooks.example.com/deliver")
	assert.ErrorIs(t, err, domain.ErrWebhookInvalidTarget)
}

func TestTargetPolicyRejectsUnsafeURLFormsAndNetworks(t *testing.T) {
	policy := NewTargetPolicy(nil)
	unsafe := []string{
		"http://8.8.8.8/hook",
		"https://user:secret@8.8.8.8/hook",
		"https://8.8.8.8/hook?token=x",
		"https://8.8.8.8/hook#fragment",
		"https://127.0.0.1/hook",
		"https://169.254.169.254/latest/meta-data",
		"https://100.64.0.1/hook",
		"https://203.0.113.1/hook",
		" https://8.8.8.8/hook",
		"https://8.8.8.8\\@example.com/hook",
		"https://[64:ff9b::a00:1]/hook",
		"https://[64:ff9b:1::a00:1]/hook",
		"https://[2002:a00:1::1]/hook",
		"https://[2001:0:a00:1::1]/hook",
		"https://[::10.0.0.1]/hook",
		"https://[::ffff:10.0.0.1]/hook",
	}
	for _, value := range unsafe {
		_, _, err := policy.Normalize(context.Background(), value)
		assert.ErrorIs(t, err, domain.ErrWebhookInvalidTarget, value)
	}
}

func TestTargetPolicyDialsOnlyTheVerifiedAddress(t *testing.T) {
	dialer := &recordingDialer{}
	policy := NewTargetPolicy(nil)
	policy.resolver = staticIPResolver{addresses: map[string][]net.IPAddr{
		"hooks.example.com": {{IP: net.ParseIP("8.8.4.4")}},
	}}
	policy.dialer = dialer

	connection, err := policy.DialContext(context.Background(), "tcp", "hooks.example.com:443")
	require.NoError(t, err)
	require.NoError(t, connection.Close())
	assert.Equal(t, "8.8.4.4:443", dialer.address)
}

func TestTargetPolicyMayAllowPrivateTargetsOnlyWhenExplicitlyConfigured(t *testing.T) {
	policy := NewTargetPolicy(nil)
	policy.allowPrivateTarget = true
	policy.allowInsecureHTTP = true

	normalized, _, err := policy.Normalize(context.Background(), "http://127.0.0.1:8080/hook")
	require.NoError(t, err)
	assert.Equal(t, "http://127.0.0.1:8080/hook", normalized)

	policy.resolver = staticIPResolver{err: errors.New("dns unavailable")}
	_, _, err = policy.Normalize(context.Background(), "http://hooks.example.com/hook")
	assert.Error(t, err)
}

func TestTargetPolicySetupRejectsNamesThatDoNotResolve(t *testing.T) {
	policy := NewTargetPolicy(nil)
	policy.resolver = staticIPResolver{err: &net.DNSError{Err: "no such host", Name: "2130706433", IsNotFound: true}}
	_, _, err := policy.Normalize(context.Background(), "https://2130706433/hook")
	assert.ErrorIs(t, err, domain.ErrWebhookInvalidTarget)

	policy.resolver = staticIPResolver{}
	_, _, err = policy.Normalize(context.Background(), "https://hooks.example.com/hook")
	assert.ErrorIs(t, err, domain.ErrWebhookInvalidTarget)

	policy.resolver = staticIPResolver{err: &net.DNSError{Err: "i/o timeout", Name: "hooks.example.com", IsTimeout: true}}
	_, _, err = policy.Normalize(context.Background(), "https://hooks.example.com/hook")
	assert.ErrorIs(t, err, domain.ErrServiceUnavailable)
}

func TestTargetPolicyRejectsNamesOtherParsersReadAsIPv4(t *testing.T) {
	policy := NewTargetPolicy(nil)
	public := []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}
	numeric := []string{"2130706433", "0x7f000001", "0X7F000001", "127.1", "0177.0.0.1", "hooks.example.0x", "hooks.example.42"}
	addresses := map[string][]net.IPAddr{"hooks.example.42a": public}
	for _, name := range numeric {
		addresses[strings.ToLower(name)] = public
	}
	policy.resolver = staticIPResolver{addresses: addresses}

	for _, name := range numeric {
		_, _, err := policy.Normalize(context.Background(), "https://"+name+"/hook")
		assert.ErrorIs(t, err, domain.ErrWebhookInvalidTarget, name)
	}
	_, _, err := policy.Normalize(context.Background(), "https://hooks.example.42a/hook")
	assert.NoError(t, err)
}
