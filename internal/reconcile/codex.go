package reconcile

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type codexNative struct{ home, skillsRoot string }

func (a *codexNative) plan(ctx context.Context, wanted desired) (targetPlan, error) {
	var reads []observation
	disabled := map[string]bool{}
	if len(wanted.skills) > 0 {
		configPath := filepath.Join(a.home, "config.toml")
		data, target, err := readTarget(configPath)
		if err != nil {
			return targetPlan{}, err
		}
		reads = append(reads, target)
		var config struct {
			Skills struct {
				Config []struct {
					Path    string
					Enabled *bool
				}
			}
		}
		if target.Exists {
			if err := toml.Unmarshal(data, &config); err != nil {
				return targetPlan{}, fmt.Errorf("decode %s: %w", configPath, err)
			}
		}
		for _, entry := range config.Skills.Config {
			if entry.Enabled != nil && !*entry.Enabled {
				disabled[filepath.Clean(entry.Path)] = true
			}
		}
	}
	return planTarget(ctx, wanted, a.skillsRoot, reads, func(path string) (string, error) {
		if disabled[filepath.Join(path, "SKILL.md")] {
			return "skill is disabled in Codex config.toml", nil
		}
		return "", nil
	}, a.pluginRegistration)
}
func (a *codexNative) validate(wanted desired) error { return validatePlugins(wanted.plugins, "codex") }

func (a *codexNative) pluginRegistration(desired []namedBundle) (pluginRegistration, error) {
	root := filepath.Join(a.home, "plugins")
	configPath := filepath.Join(a.home, "config.toml")
	data, configTarget, err := readTarget(configPath)
	if err != nil {
		return pluginRegistration{}, err
	}
	config := map[string]any{}
	if configTarget.Exists {
		if err := toml.Unmarshal(data, &config); err != nil {
			return pluginRegistration{}, err
		}
	}
	plugins, err := objectField(config, "plugins")
	if err != nil {
		return pluginRegistration{}, err
	}
	marketplaces, err := objectField(config, "marketplaces")
	if err != nil {
		return pluginRegistration{}, err
	}
	catalogPath := filepath.Join(root, ".agents", "plugins", "marketplace.json")
	catalog, catalogTarget, err := readJSON(catalogPath, map[string]any{"name": "skillet", "plugins": []any{}})
	if err != nil {
		return pluginRegistration{}, err
	}
	entries, ok := catalog["plugins"].([]any)
	if !ok || catalog["name"] != "skillet" {
		return pluginRegistration{}, fmt.Errorf("unsupported Codex local marketplace")
	}
	registration := pluginRegistration{targets: []observation{configTarget, catalogTarget}}
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
			registration.plugin(name).conflict = "multiple plugin registrations"
		}
		byName[name] = record
		entryConfig, hasConfig := plugins[name+"@skillet"].(map[string]any)
		enabled, validEnabled := entryConfig["enabled"].(bool)
		if hasConfig && !validEnabled {
			return registration, fmt.Errorf("invalid Codex plugin enabled state")
		}
		registration.plugin(name).installed = &pluginInstall{Path: path, Enabled: enabled && marketplaces["skillet"] != nil, Local: localMarketplace && source["source"] == "local" && filepath.Clean(path) == filepath.Join(root, "managed", name)}
	}
	for _, plugin := range desired {
		name, key := plugin.name, plugin.name+"@skillet"
		version, err := pluginVersion(plugin, "codex")
		if err != nil {
			return registration, err
		}
		if !filepath.IsLocal(version) || strings.ContainsAny(version, "/\\") {
			return registration, fmt.Errorf("unsafe Codex plugin version %q", version)
		}
		destination := filepath.Join(root, "managed", name)
		registration.plugin(name).copies = []string{filepath.Join(root, "cache", "skillet", name, version)}
		registration.plugin(name).path = destination
		if !localMarketplace {
			registration.plugin(name).conflict = "skillet marketplace is managed by another source"
		}
		for id := range plugins {
			if strings.SplitN(id, "@", 2)[0] == name && id != key {
				registration.plugin(name).conflict = "plugin is managed by another source"
			}
		}
		if _, exists := plugins[key]; exists && byName[name] == nil {
			registration.plugin(name).conflict = "plugin registration has no local marketplace entry"
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
		entryConfig, err := objectField(plugins, key)
		if err != nil {
			return registration, err
		}
		entryConfig["enabled"] = true
	}
	if localMarketplace {
		marketplace, err := objectField(marketplaces, "skillet")
		if err != nil {
			return registration, err
		}
		marketplace["source_type"], marketplace["source"] = "local", root
	}
	catalog["plugins"] = entries
	write, err := jsonWrite(catalogPath, catalog)
	if err != nil {
		return registration, err
	}
	data, err = toml.Marshal(config)
	registration.writes = []replacement{write, {Path: configPath, Data: data}}
	return registration, err
}
