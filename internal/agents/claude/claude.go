package claude

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	agent "github.com/leoxlin/skillets/internal/agents"
	"github.com/leoxlin/skillets/internal/agents/shared"
	nativeplugin "github.com/leoxlin/skillets/internal/plugin"
	"github.com/leoxlin/skillets/internal/resource"
)

type Target struct {
	Home string
}

type Agent struct {
	target  Target
	plugins *shared.PluginStore
	skills  *skills
}

func New(target Target) *Agent {
	a := &Agent{
		target: target,
		skills: &skills{SkillStore: &shared.SkillStore{
			Root:       filepath.Join(target.Home, "skills"),
			BackupRoot: agent.BackupRoot(target.Home),
		}},
	}
	a.plugins = &shared.PluginStore{Agent: "claude", BackupRoot: agent.BackupRoot(target.Home), Register: a.pluginRegistration}
	return a
}

func (a *Agent) Name() string { return "claude" }

func (a *Agent) Resources() agent.Resources {
	return agent.Resources{Skills: a.skills, Plugins: a.plugins}
}

func (a *Agent) Lock(ctx context.Context) (func() error, error) {
	return agent.AcquireLock(ctx, agent.LockPath(a.target.Home))
}

type skills struct {
	*shared.SkillStore
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
		item, err := s.SkillStore.Plan(ctx, []resource.Skill{change.Desired})
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

func (a *Agent) pluginRegistration(desired []resource.Plugin) (shared.PluginRegistration, error) {
	root := filepath.Join(a.target.Home, "plugins", "managed")
	registryPath := filepath.Join(a.target.Home, "plugins", "installed_plugins.json")
	registry, registryTarget, err := shared.ReadJSON(registryPath, map[string]any{"version": json.Number("2"), "plugins": map[string]any{}})
	if err != nil {
		return shared.PluginRegistration{}, err
	}
	if registry["version"] != json.Number("2") {
		return shared.PluginRegistration{}, fmt.Errorf("unsupported Claude plugin registry version")
	}
	records, err := shared.ObjectField(registry, "plugins")
	if err != nil {
		return shared.PluginRegistration{}, err
	}
	settingsPath := filepath.Join(a.target.Home, "settings.json")
	settings, settingsTarget, err := shared.ReadJSON(settingsPath, map[string]any{})
	if err != nil {
		return shared.PluginRegistration{}, err
	}
	enabled, err := shared.ObjectField(settings, "enabledPlugins")
	if err != nil {
		return shared.PluginRegistration{}, err
	}
	marketplacePath := filepath.Join(a.target.Home, "plugins", "known_marketplaces.json")
	marketplaces, marketplaceTarget, err := shared.ReadJSON(marketplacePath, map[string]any{})
	if err != nil {
		return shared.PluginRegistration{}, err
	}
	catalogPath := filepath.Join(root, ".claude-plugin", "marketplace.json")
	catalog, catalogTarget, err := shared.ReadJSON(catalogPath, map[string]any{"name": "skillet", "owner": map[string]any{"name": "skillet"}, "plugins": []any{}})
	if err != nil {
		return shared.PluginRegistration{}, err
	}
	entries, ok := catalog["plugins"].([]any)
	if !ok || catalog["name"] != "skillet" {
		return shared.PluginRegistration{}, fmt.Errorf("unsupported Claude local marketplace")
	}
	localMarketplace := true
	if value, exists := marketplaces["skillet"]; exists {
		marketplace, ok := value.(map[string]any)
		if !ok {
			return shared.PluginRegistration{}, fmt.Errorf("invalid Claude marketplace record")
		}
		source, ok := marketplace["source"].(map[string]any)
		localMarketplace = ok && source["source"] == "directory" && source["path"] == root && marketplace["installLocation"] == root
	}
	catalogEntries := map[string]map[string]any{}
	for _, entry := range entries {
		record, ok := entry.(map[string]any)
		if !ok {
			return shared.PluginRegistration{}, fmt.Errorf("invalid Claude marketplace entry")
		}
		name, ok := record["name"].(string)
		if !ok || catalogEntries[name] != nil {
			return shared.PluginRegistration{}, fmt.Errorf("invalid or duplicate Claude marketplace entry")
		}
		catalogEntries[name] = record
	}
	registration := shared.PluginRegistration{Installed: map[string]shared.PluginInstall{}, Paths: map[string]string{}, Targets: []resource.TargetDigest{registryTarget, settingsTarget, marketplaceTarget, catalogTarget}, Conflicts: map[string]string{}}
	for _, plugin := range desired {
		name, key := plugin.Name, plugin.Name+"@skillet"
		version, err := nativeplugin.Version(plugin, "claude")
		if err != nil {
			return registration, err
		}
		if !localMarketplace {
			registration.Conflicts[name] = "skillet marketplace is managed by another source"
		}
		catalogEntry := catalogEntries[name]
		if catalogEntry == nil {
			catalogEntry = map[string]any{"name": name}
			entries = append(entries, catalogEntry)
		}
		catalogEntry["source"] = "./" + name
		catalogEntry["version"] = version
		destination := filepath.Join(root, name)
		registration.Paths[name] = destination
		for id, value := range records {
			if strings.SplitN(id, "@", 2)[0] != name {
				continue
			}
			entries, ok := value.([]any)
			if !ok || len(entries) != 1 {
				registration.Conflicts[name] = "multiple or invalid plugin registrations"
				continue
			}
			record, ok := entries[0].(map[string]any)
			if !ok {
				return registration, fmt.Errorf("invalid Claude plugin record %s", id)
			}
			path, ok := record["installPath"].(string)
			if !ok || !filepath.IsAbs(path) {
				return registration, fmt.Errorf("invalid Claude plugin install path %s", id)
			}
			isEnabled := true
			if value, exists := enabled[id]; exists {
				isEnabled, ok = value.(bool)
				if !ok {
					return registration, fmt.Errorf("invalid Claude enabledPlugins entry %s", id)
				}
			}
			if _, exists := registration.Installed[name]; exists {
				registration.Conflicts[name] = "multiple plugin registrations"
			}
			registration.Installed[name] = shared.PluginInstall{Path: path, Enabled: isEnabled && marketplaces["skillet"] != nil && catalogEntries[name] != nil, Local: localMarketplace && id == key && record["scope"] == "user" && filepath.Clean(path) == destination}
		}
		now := time.Now().UTC().Format(time.RFC3339Nano)
		record := map[string]any{"scope": "user", "installPath": destination, "installedAt": now}
		if existing, ok := records[key].([]any); ok && len(existing) == 1 {
			if previous, ok := existing[0].(map[string]any); ok {
				record = previous
			}
		}
		record["lastUpdated"] = now
		record["version"] = version
		records[key] = []any{record}
		enabled[key] = true
	}
	catalog["plugins"] = entries
	if localMarketplace {
		marketplace, err := shared.ObjectField(marketplaces, "skillet")
		if err != nil {
			return registration, err
		}
		marketplace["source"] = map[string]any{"source": "directory", "path": root}
		marketplace["installLocation"] = root
		marketplace["lastUpdated"] = time.Now().UTC().Format(time.RFC3339Nano)
	}
	for _, document := range []struct {
		path  string
		value any
	}{{registryPath, registry}, {settingsPath, settings}, {catalogPath, catalog}, {marketplacePath, marketplaces}} {
		path, value := document.path, document.value
		write, err := shared.JSONWrite(path, value)
		if err != nil {
			return registration, err
		}
		registration.Writes = append(registration.Writes, write)
	}
	return registration, nil
}
