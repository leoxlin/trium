package reconcile

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"
)

type kimiNative struct{ home, skillsRoot string }

func (a *kimiNative) plan(ctx context.Context, wanted desired) (targetPlan, error) {
	return planTarget(ctx, wanted, a.skillsRoot, nil, nil, a.pluginRegistration, filepath.Join(a.home, "AGENTS.md"))
}
func (a *kimiNative) validate(wanted desired) error { return validatePlugins(wanted.plugins, "kimi") }

func (a *kimiNative) pluginRegistration(desired []namedBundle) (pluginRegistration, error) {
	path := filepath.Join(a.home, "plugins", "installed.json")
	doc, target, err := readJSON(path, map[string]any{"version": json.Number("1"), "plugins": []any{}})
	if err != nil {
		return pluginRegistration{}, err
	}
	entries, ok := doc["plugins"].([]any)
	if doc["version"] != json.Number("1") || !ok {
		return pluginRegistration{}, fmt.Errorf("unsupported Kimi plugin registry")
	}
	registration := pluginRegistration{targets: []observation{target}}
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
			registration.plugin(id).conflict = "multiple plugin registrations"
		}
		byID[id] = record
		registration.plugin(id).installed = &pluginInstall{Path: root, Enabled: enabled, Local: record["source"] == "local-path" && filepath.Clean(root) == filepath.Join(a.home, "plugins", "managed", id)}
	}
	for _, plugin := range desired {
		destination := filepath.Join(a.home, "plugins", "managed", plugin.name)
		registration.plugin(plugin.name).path = destination
		record := byID[plugin.name]
		if record == nil {
			record = map[string]any{"id": plugin.name, "installedAt": time.Now().UTC().Format(time.RFC3339Nano)}
			entries = append(entries, record)
		}
		record["root"], record["source"], record["enabled"] = destination, "local-path", true
		record["originalSource"] = destination
		record["updatedAt"] = time.Now().UTC().Format(time.RFC3339Nano)
	}
	doc["plugins"] = entries
	write, err := jsonWrite(path, doc)
	registration.writes = []replacement{write}
	return registration, err
}
