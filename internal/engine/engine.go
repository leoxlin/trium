package engine

import (
	"context"
	"errors"
	"fmt"

	agent "github.com/leoxlin/skillets/internal/agents"
	"github.com/leoxlin/skillets/internal/resource"
)

type Plan struct {
	Skills  resource.ChangeSet[resource.Skill]
	Plugins resource.ChangeSet[resource.Plugin]
}

func (p Plan) HasConflicts() bool {
	return p.Skills.HasConflicts() || p.Plugins.HasConflicts()
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
	plan := Plan{Skills: skills}
	if resources.Plugins == nil {
		if len(desired.Plugins) > 0 {
			return Plan{}, errors.New("agent has no plugin adapter")
		}
		return plan, nil
	}
	plan.Plugins, err = resources.Plugins.Plan(ctx, desired.Plugins)
	if err != nil {
		return Plan{}, fmt.Errorf("plan plugins: %w", err)
	}
	return plan, nil
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
	if plan.Skills.HasChanges() {
		report, err = a.Resources().Skills.Apply(ctx, plan.Skills)
		if err != nil {
			return plan, report, fmt.Errorf("apply skills: %w", err)
		}
	}
	if plan.Plugins.HasChanges() {
		pluginReport, applyErr := a.Resources().Plugins.Apply(ctx, plan.Plugins)
		report.Applied = append(report.Applied, pluginReport.Applied...)
		report.Backups = append(report.Backups, pluginReport.Backups...)
		report.Warnings = append(report.Warnings, pluginReport.Warnings...)
		if applyErr != nil {
			return plan, report, fmt.Errorf("apply plugins: %w", applyErr)
		}
	}
	return plan, report, nil
}
