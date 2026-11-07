package kimi

import (
	"context"
	"path/filepath"

	agent "github.com/leoxlin/skillets/internal/agents"
	"github.com/leoxlin/skillets/internal/agents/shared"
)

type Target struct {
	Home string
}

type Agent struct {
	target Target
	skills *shared.Store
}

func New(target Target) *Agent {
	return &Agent{
		target: target,
		skills: &shared.Store{
			Root:       filepath.Join(target.Home, "skills"),
			BackupRoot: agent.BackupRoot(target.Home),
		},
	}
}

func (a *Agent) Name() string {
	return "kimi"
}

func (a *Agent) Resources() agent.Resources {
	return agent.Resources{Skills: a.skills}
}

func (a *Agent) Lock(ctx context.Context) (func() error, error) {
	return agent.AcquireLock(ctx, agent.LockPath(a.target.Home))
}
