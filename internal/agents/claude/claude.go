package claude

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	agent "github.com/leoxlin/skillets/internal/agents"
	"github.com/leoxlin/skillets/internal/agents/shared"
	"github.com/leoxlin/skillets/internal/resource"
)

type Target struct {
	Home string
}

type Agent struct {
	target Target
	skills *skills
}

func New(target Target) *Agent {
	return &Agent{
		target: target,
		skills: &skills{Store: &shared.Store{
			Root:       filepath.Join(target.Home, "skills"),
			BackupRoot: agent.BackupRoot(target.Home),
		}},
	}
}

func (a *Agent) Name() string { return "claude" }

func (a *Agent) Resources() agent.Resources { return agent.Resources{Skills: a.skills} }

func (a *Agent) Lock(ctx context.Context) (func() error, error) {
	return agent.AcquireLock(ctx, agent.LockPath(a.target.Home))
}

type skills struct {
	*shared.Store
}

func (s *skills) Plan(ctx context.Context, desired []resource.Skill) (resource.ChangeSet[resource.Skill], error) {
	changes, err := resource.Diff(desired, map[string]resource.Skill{})
	if err != nil {
		return resource.ChangeSet[resource.Skill]{}, err
	}
	var plan resource.ChangeSet[resource.Skill]
	for _, change := range changes {
		if err := ctx.Err(); err != nil {
			return resource.ChangeSet[resource.Skill]{}, err
		}
		reason, err := protectedPath(filepath.Join(s.Root, change.Desired.Name))
		if err != nil {
			return resource.ChangeSet[resource.Skill]{}, err
		}
		if reason != "" {
			change.Action, change.Reason = resource.Conflict, reason
			plan.Changes = append(plan.Changes, change)
			continue
		}
		item, err := s.Store.Plan(ctx, []resource.Skill{change.Desired})
		if err != nil {
			return resource.ChangeSet[resource.Skill]{}, err
		}
		plan.Changes = append(plan.Changes, item.Changes...)
		plan.Targets = append(plan.Targets, item.Targets...)
	}
	return plan, nil
}

func protectedPath(path string) (string, error) {
	var ancestors []string
	for current := path; ; current = filepath.Dir(current) {
		if name := filepath.Base(current); name == "synced" || name == ".trash" {
			return "skill target is reserved for Claude-managed skills", nil
		}
		ancestors = append(ancestors, current)
		if filepath.Dir(current) == current {
			break
		}
	}
	for i := len(ancestors) - 1; i >= 0; i-- {
		current := ancestors[i]
		info, err := os.Lstat(current)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("inspect Claude skill path %s: %w", current, err)
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return "skill target must not traverse symlinked catalogs", nil
		}
	}
	return "", nil
}
