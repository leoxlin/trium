package engine

import (
	"context"
	"errors"
	"fmt"

	agent "github.com/leoxlin/skillets/internal/agents"
	"github.com/leoxlin/skillets/internal/resource"
)

type Plan struct {
	Skills resource.ChangeSet[resource.Skill]
}

func (p Plan) HasConflicts() bool {
	return p.Skills.HasConflicts()
}

type Engine struct{}

func (Engine) Plan(ctx context.Context, a agent.Agent, desired resource.Desired) (Plan, error) {
	resources := a.Resources()
	if resources.Skills == nil {
		return Plan{}, errors.New("agent has no skill adapter")
	}
	skills, err := resources.Skills.Plan(ctx, desired.Skills)
	if err != nil {
		return Plan{}, fmt.Errorf("plan skills: %w", err)
	}
	return Plan{Skills: skills}, nil
}

func (e Engine) Apply(ctx context.Context, a agent.Agent, desired resource.Desired) (plan Plan, report resource.ApplyReport, err error) {
	unlock, err := a.Lock(ctx)
	if err != nil {
		return Plan{}, resource.ApplyReport{}, fmt.Errorf("lock agent: %w", err)
	}
	defer func() {
		err = errors.Join(err, unlock())
	}()

	plan, err = e.Plan(ctx, a, desired)
	if err != nil || plan.HasConflicts() {
		return plan, resource.ApplyReport{}, err
	}
	if !plan.Skills.HasChanges() {
		return plan, resource.ApplyReport{}, nil
	}
	report, err = a.Resources().Skills.Apply(ctx, plan.Skills)
	if err != nil {
		return plan, report, fmt.Errorf("apply skills: %w", err)
	}
	return plan, report, nil
}
