package codex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	agent "github.com/leoxlin/skillets/internal/agents"
	"github.com/leoxlin/skillets/internal/agents/shared"
	nativeplugin "github.com/leoxlin/skillets/internal/plugin"
	"github.com/leoxlin/skillets/internal/resource"
	"github.com/pelletier/go-toml/v2"
)

type Target struct {
	Home       string
	SkillsRoot string
}

type Agent struct {
	target  Target
	plugins *shared.PluginStore
	skills  *shared.SkillStore
}

func New(target Target) *Agent {
	a := &Agent{target: target}
	a.plugins = &shared.PluginStore{Agent: "codex", BackupRoot: agent.BackupRoot(target.Home), Register: a.pluginRegistration}
	a.skills = &shared.SkillStore{
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
	return agent.Resources{Skills: a.skills, Plugins: a.plugins}
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

func (a *Agent) pluginRegistration(desired []resource.Plugin) (shared.PluginRegistration, error) {
	root := filepath.Join(a.target.Home, "plugins")
	configPath := filepath.Join(a.target.Home, "config.toml")
	data, configTarget, err := shared.ReadTarget(configPath)
	if err != nil {
		return shared.PluginRegistration{}, err
	}
	config := map[string]any{}
	if configTarget.Exists {
		if err := toml.Unmarshal(data, &config); err != nil {
			return shared.PluginRegistration{}, err
		}
	}
	plugins, err := shared.ObjectField(config, "plugins")
	if err != nil {
		return shared.PluginRegistration{}, err
	}
	marketplaces, err := shared.ObjectField(config, "marketplaces")
	if err != nil {
		return shared.PluginRegistration{}, err
	}
	catalogPath := filepath.Join(root, ".agents", "plugins", "marketplace.json")
	catalog, catalogTarget, err := shared.ReadJSON(catalogPath, map[string]any{"name": "skillet", "plugins": []any{}})
	if err != nil {
		return shared.PluginRegistration{}, err
	}
	entries, ok := catalog["plugins"].([]any)
	if !ok || catalog["name"] != "skillet" {
		return shared.PluginRegistration{}, fmt.Errorf("unsupported Codex local marketplace")
	}
	registration := shared.PluginRegistration{CopyPaths: map[string][]string{}, Installed: map[string]shared.PluginInstall{}, Paths: map[string]string{}, Targets: []resource.TargetDigest{configTarget, catalogTarget}, Conflicts: map[string]string{}}
	localMarketplace := true
	if value, exists := marketplaces["skillet"]; exists {
		record, ok := value.(map[string]any)
		localMarketplace = ok && record["source_type"] == "local" && record["source"] == root
	}
	byName := map[string]map[string]any{}
	for _, entry := range entries {
		record, ok := entry.(map[string]any)
		if !ok {
			return registration, fmt.Errorf("invalid Codex marketplace entry")
		}
		name, ok := record["name"].(string)
		source, sourceOK := record["source"].(map[string]any)
		if !ok || !sourceOK {
			return registration, fmt.Errorf("invalid Codex marketplace source")
		}
		relative, ok := source["path"].(string)
		if !ok || !filepath.IsLocal(relative) {
			return registration, fmt.Errorf("invalid Codex marketplace path")
		}
		path := filepath.Join(root, relative)
		if byName[name] != nil {
			registration.Conflicts[name] = "multiple plugin registrations"
		}
		byName[name] = record
		entryConfig, hasConfig := plugins[name+"@skillet"].(map[string]any)
		enabled, validEnabled := entryConfig["enabled"].(bool)
		if hasConfig && !validEnabled {
			return registration, fmt.Errorf("invalid Codex plugin enabled state")
		}
		registration.Installed[name] = shared.PluginInstall{Path: path, Enabled: enabled && marketplaces["skillet"] != nil, Local: localMarketplace && source["source"] == "local" && filepath.Clean(path) == filepath.Join(root, "managed", name)}
	}
	for _, plugin := range desired {
		name, key := plugin.Name, plugin.Name+"@skillet"
		version, err := nativeplugin.Version(plugin, "codex")
		if err != nil {
			return registration, err
		}
		if !filepath.IsLocal(version) || strings.ContainsAny(version, "/\\") {
			return registration, fmt.Errorf("unsafe Codex plugin version %q", version)
		}
		destination := filepath.Join(root, "managed", name)
		registration.CopyPaths[name] = []string{filepath.Join(root, "cache", "skillet", name, version)}
		registration.Paths[name] = destination
		if !localMarketplace {
			registration.Conflicts[name] = "skillet marketplace is managed by another source"
		}
		for id := range plugins {
			if strings.SplitN(id, "@", 2)[0] == name && id != key {
				registration.Conflicts[name] = "plugin is managed by another source"
			}
		}
		if _, exists := plugins[key]; exists && byName[name] == nil {
			registration.Conflicts[name] = "plugin registration has no local marketplace entry"
		}
		record := byName[name]
		if record == nil {
			record = map[string]any{"name": name}
			entries = append(entries, record)
		}
		relative, _ := filepath.Rel(root, destination)
		record["source"] = map[string]any{"source": "local", "path": "./" + filepath.ToSlash(relative)}
		record["policy"] = map[string]any{"installation": "AVAILABLE", "authentication": "ON_INSTALL"}
		record["category"] = "Productivity"
		entryConfig, err := shared.ObjectField(plugins, key)
		if err != nil {
			return registration, err
		}
		entryConfig["enabled"] = true
	}
	if localMarketplace {
		marketplace, err := shared.ObjectField(marketplaces, "skillet")
		if err != nil {
			return registration, err
		}
		marketplace["source_type"], marketplace["source"] = "local", root
	}
	catalog["plugins"] = entries
	write, err := shared.JSONWrite(catalogPath, catalog)
	if err != nil {
		return registration, err
	}
	data, err = toml.Marshal(config)
	registration.Writes = []shared.Write{write, {Path: configPath, Data: data}}
	return registration, err
}
