package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"

	agent "github.com/leoxlin/skillets/internal/agents"
	"github.com/leoxlin/skillets/internal/agents/shared"
	"github.com/leoxlin/skillets/internal/resource"
)

type Target struct {
	Home       string
	SkillsRoot string
}

type Agent struct {
	target Target
	skills *shared.Store
}

func New(target Target) *Agent {
	a := &Agent{target: target}
	a.skills = &shared.Store{
		Root:       target.SkillsRoot,
		BackupRoot: agent.BackupRoot(target.Home),
		Check:      a.disabledSkill,
	}
	return a
}

func (a *Agent) Name() string {
	return "codex"
}

func (a *Agent) Resources() agent.Resources {
	return agent.Resources{Skills: a.skills}
}

func (a *Agent) Lock(ctx context.Context) (func() error, error) {
	return agent.AcquireLock(ctx, agent.LockPath(a.target.Home))
}

func (a *Agent) disabledSkill(_ context.Context, _ resource.Skill, target string) (string, error) {
	path := filepath.Join(a.target.Home, "config.toml")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read Codex config %s: %w", path, err)
	}
	var config struct {
		Skills struct {
			Config []struct {
				Path    string `toml:"path"`
				Enabled *bool  `toml:"enabled"`
			} `toml:"config"`
		} `toml:"skills"`
	}
	if err := toml.Unmarshal(data, &config); err != nil {
		return "", fmt.Errorf("decode Codex config %s: %w", path, err)
	}
	skillFile := filepath.Clean(filepath.Join(target, "SKILL.md"))
	for _, entry := range config.Skills.Config {
		if entry.Enabled != nil && !*entry.Enabled && filepath.Clean(entry.Path) == skillFile {
			return "skill is disabled in Codex config.toml", nil
		}
	}
	return "", nil
}
