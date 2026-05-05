package risk

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIPWhitelistRule_AllowsListedIP(t *testing.T) {
	r, err := NewIPWhitelistRule([]string{"127.0.0.1", "10.0.0.0/8"})
	require.NoError(t, err)
	d, err := r.Check(context.Background(), Input{ClientIP: net.ParseIP("127.0.0.1")})
	require.NoError(t, err)
	assert.True(t, d.Allowed)
}

func TestIPWhitelistRule_AllowsCIDRMatch(t *testing.T) {
	r, err := NewIPWhitelistRule([]string{"10.0.0.0/8"})
	require.NoError(t, err)
	d, err := r.Check(context.Background(), Input{ClientIP: net.ParseIP("10.5.6.7")})
	require.NoError(t, err)
	assert.True(t, d.Allowed)
}

func TestIPWhitelistRule_DeniesUnlisted(t *testing.T) {
	r, err := NewIPWhitelistRule([]string{"127.0.0.1"})
	require.NoError(t, err)
	d, err := r.Check(context.Background(), Input{ClientIP: net.ParseIP("8.8.8.8")})
	require.NoError(t, err)
	assert.False(t, d.Allowed)
	assert.Contains(t, d.Reason, "8.8.8.8")
}

func TestIPWhitelistRule_DeniesMissingIP(t *testing.T) {
	r, err := NewIPWhitelistRule([]string{"127.0.0.1"})
	require.NoError(t, err)
	d, err := r.Check(context.Background(), Input{})
	require.NoError(t, err)
	assert.False(t, d.Allowed)
}

func TestIPWhitelistRule_RejectsBadCIDR(t *testing.T) {
	_, err := NewIPWhitelistRule([]string{"not-an-ip"})
	require.Error(t, err)
}
