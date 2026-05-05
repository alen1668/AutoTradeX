package risk

import (
	"context"
	"fmt"
)

type Pipeline struct {
	rules []Rule
}

func NewPipeline(rules ...Rule) *Pipeline { return &Pipeline{rules: rules} }

func (p *Pipeline) Run(ctx context.Context, in Input) (Decision, error) {
	for _, r := range p.rules {
		d, err := r.Check(ctx, in)
		if err != nil {
			return Decision{}, fmt.Errorf("rule %s: %w", r.Name(), err)
		}
		if !d.Allowed {
			d.RuleName = r.Name()
			return d, nil
		}
	}
	return Allow(), nil
}
