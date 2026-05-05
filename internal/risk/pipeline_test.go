package risk

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubRule struct {
	name    string
	dec     Decision
	calledP *bool
	err     error
}

func (s *stubRule) Name() string { return s.name }
func (s *stubRule) Check(ctx context.Context, in Input) (Decision, error) {
	if s.calledP != nil {
		*s.calledP = true
	}
	return s.dec, s.err
}

func TestPipeline_AllAllow(t *testing.T) {
	p := NewPipeline(
		&stubRule{name: "a", dec: Allow()},
		&stubRule{name: "b", dec: Allow()},
	)
	d, err := p.Run(context.Background(), Input{})
	require.NoError(t, err)
	assert.True(t, d.Allowed)
}

func TestPipeline_ShortCircuitsOnDeny(t *testing.T) {
	calledThird := false
	p := NewPipeline(
		&stubRule{name: "a", dec: Allow()},
		&stubRule{name: "b", dec: Deny("rule b denied")},
		&stubRule{name: "c", dec: Allow(), calledP: &calledThird},
	)
	d, err := p.Run(context.Background(), Input{})
	require.NoError(t, err)
	assert.False(t, d.Allowed)
	assert.Equal(t, "b", d.RuleName)
	assert.Equal(t, "rule b denied", d.Reason)
	assert.False(t, calledThird, "third rule should not be called after deny")
}

func TestPipeline_RulesErrorPropagates(t *testing.T) {
	p := NewPipeline(
		&stubRule{name: "a", err: errors.New("boom")},
	)
	_, err := p.Run(context.Background(), Input{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rule a")
}
