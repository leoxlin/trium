package kimi

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	agent "github.com/leoxlin/skillets/internal/agents"
	"github.com/leoxlin/skillets/internal/agents/shared"
	"github.com/leoxlin/skillets/internal/resource"
)

type Target struct {
	Home string
}

type Agent struct {
	target  Target
	plugins *shared.PluginStore
	skills  *shared.SkillStore
}

func New(target Target) *Agent {
	a := &Agent{
		target: target,
		skills: &shared.SkillStore{
			Root:       filepath.Join(target.Home, "skills"),
			BackupRoot: agent.BackupRoot(target.Home),
		},
	}
	a.plugins = &shared.PluginStore{Agent: "kimi", BackupRoot: agent.BackupRoot(target.Home), Register: a.pluginRegistration}
	return a
}

func (a *Agent) Name() string {
	return "kimi"
}

func (a *Agent) Resources() agent.Resources {
	return agent.Resources{Skills: a.skills, Plugins: a.plugins}
}

func (a *Agent) Lock(ctx context.Context) (func() error, error) {
	return agent.AcquireLock(ctx, agent.LockPath(a.target.Home))
}

func (a *Agent) pluginRegistration(desired []resource.Plugin) (shared.PluginRegistration, error) {
	path := filepath.Join(a.target.Home, "plugins", "installed.json")
	doc, target, err := shared.ReadJSON(path, map[string]any{"version": json.Number("1"), "plugins": []any{}})
	if err != nil {
		return shared.PluginRegistration{}, err
	}
	entries, ok := doc["plugins"].([]any)
	if doc["version"] != json.Number("1") || !ok {
		return shared.PluginRegistration{}, fmt.Errorf("unsupported Kimi plugin registry")
	}
	registration := shared.PluginRegistration{Installed: map[string]shared.PluginInstall{}, Paths: map[string]string{}, Targets: []resource.TargetDigest{target}, Conflicts: map[string]string{}}
	byID := map[string]map[string]any{}
	for _, entry := range entries {
		record, ok := entry.(map[string]any)
		if !ok {
			return registration, fmt.Errorf("invalid Kimi plugin record")
		}
		id, ok := record["id"].(string)
		root, rootOK := record["root"].(string)
		enabled, enabledOK := record["enabled"].(bool)
		if !ok || !rootOK || !enabledOK || id == "" || !filepath.IsAbs(root) {
			return registration, fmt.Errorf("invalid Kimi plugin record")
		}
		if byID[id] != nil {
			registration.Conflicts[id] = "multiple plugin registrations"
		}
		byID[id] = record
		registration.Installed[id] = shared.PluginInstall{Path: root, Enabled: enabled, Local: record["source"] == "local-path" && filepath.Clean(root) == filepath.Join(a.target.Home, "plugins", "managed", id)}
	}
	for _, plugin := range desired {
		destination := filepath.Join(a.target.Home, "plugins", "managed", plugin.Name)
		registration.Paths[plugin.Name] = destination
		record := byID[plugin.Name]
		if record == nil {
			record = map[string]any{"id": plugin.Name, "installedAt": time.Now().UTC().Format(time.RFC3339Nano)}
			entries = append(entries, record)
		}
		record["root"], record["source"], record["enabled"] = destination, "local-path", true
		record["originalSource"] = destination
		record["updatedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
	}
	doc["plugins"] = entries
	write, err := shared.JSONWrite(path, doc)
	registration.Writes = []shared.Write{write}
	return registration, err
}
