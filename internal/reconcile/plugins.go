package reconcile

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/leoxlin/trium/internal/bundle"
)

type pluginInstall struct {
	Path           string
	Enabled, Local bool
}
type pluginState struct {
	path      string
	copies    []string
	installed *pluginInstall
	conflict  string
}
type pluginRegistration struct {
	plugins map[string]*pluginState
	targets []observation
	writes  []replacement
}

func (r *pluginRegistration) plugin(name string) *pluginState {
	if r.plugins == nil {
		r.plugins = map[string]*pluginState{}
	}
	if r.plugins[name] == nil {
		r.plugins[name] = &pluginState{}
	}
	return r.plugins[name]
}

func validatePlugins(plugins []namedBundle, agent string) error {
	for _, wanted := range plugins {
		if err := validatePlugin(wanted.name, agent, wanted.content); err != nil {
			return &InputError{Field: wanted.field, Err: err}
		}
		if agent == "codex" || agent == "claude" {
			version, err := pluginVersion(wanted, agent)
			if err != nil {
				return &InputError{Field: wanted.field, Err: err}
			}
			if agent == "codex" && (!filepath.IsLocal(version) || strings.ContainsAny(version, "/\\")) {
				return &InputError{Field: wanted.field, Err: fmt.Errorf("unsafe Codex plugin version %q", version)}
			}
		}
	}
	return nil
}

func planPlugins(ctx context.Context, desired []namedBundle, register func([]namedBundle) (pluginRegistration, error)) (writeBatch, error) {
	batch := writeBatch{kind: Plugins}
	if len(desired) == 0 {
		return batch, nil
	}
	registration, err := register(desired)
	if err != nil {
		return batch, err
	}
	batch.reads = append(batch.reads, registration.targets...)
	for _, wanted := range desired {
		if err := ctx.Err(); err != nil {
			return batch, err
		}
		state := registration.plugin(wanted.name)
		path := state.path
		reason := state.conflict
		if state.installed != nil {
			if !state.installed.Local {
				reason = "plugin is managed by another source"
			} else {
				path = state.installed.Path
			}
		}
		change := compare(Plugins, wanted, bundle.Digest{}, false)
		if reason == "" {
			_, target, unsafe, err := scanBundle(ctx, path)
			if err != nil {
				return batch, err
			}
			batch.reads = append(batch.reads, target)
			switch {
			case unsafe != "":
				reason = unsafe
			case state.installed != nil && !target.Exists:
				reason = "plugin registration has no bundle"
			case state.installed == nil && target.Exists:
				reason = "plugin bundle has no registration"
			default:
				change = compare(Plugins, wanted, target.Digest, target.Exists)
				if change.Action == Unchanged && !state.installed.Enabled {
					change.Action, change.Reason = Update, "plugin is disabled"
				}
			}
			for _, copyPath := range state.copies {
				_, target, unsafe, err := scanBundle(ctx, copyPath)
				if err != nil {
					return batch, err
				}
				batch.reads = append(batch.reads, target)
				if unsafe != "" {
					reason = unsafe
				}
				if change.Action == Unchanged && (!target.Exists || target.Digest != wanted.content.Digest()) {
					change.Action, change.Reason = Update, "native plugin cache differs"
				}
			}
		}
		if reason != "" {
			change.Action, change.Reason = Conflict, reason
		}
		if change.Action == Add || change.Action == Update {
			change.Reason += "; enables plugin code in future agent sessions"
			for _, destination := range append([]string{state.path}, state.copies...) {
				batch.writes = append(batch.writes, replacement{Path: destination, Files: wanted.content.Files(), Directory: true})
			}
		}
		batch.changes = append(batch.changes, change)
	}
	if len(batch.writes) > 0 {
		batch.writes = append(batch.writes, registration.writes...)
	}
	return batch, nil
}

func validatePlugin(name, agent string, content bundle.Snapshot) error {
	paths := map[string][]string{
		"codex":  {".codex-plugin/plugin.json"},
		"claude": {".claude-plugin/plugin.json"},
		"kimi":   {"kimi.plugin.json", ".kimi-plugin/plugin.json"},
	}
	for _, path := range paths[agent] {
		file, ok := findFile(content, path)
		if !ok {
			continue
		}
		metadata, err := decodeObject(file.Data)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		identity, _ := metadata["name"].(string)
		if agent == "kimi" {
			id, _ := metadata["id"].(string)
			if id != "" {
				identity = id
			}
		}
		if identity != name {
			return fmt.Errorf("%s: plugin name or ID %q does not match manifest name %q", path, identity, name)
		}
		return nil
	}
	return fmt.Errorf("%s plugin must contain %v", agent, paths[agent])
}

func pluginVersion(plugin namedBundle, agent string) (string, error) {
	file, _ := findFile(plugin.content, "."+agent+"-plugin/plugin.json")
	metadata, err := decodeObject(file.Data)
	if err != nil {
		return "", err
	}
	value, exists := metadata["version"]
	if !exists {
		return "0.1.0", nil
	}
	version, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("plugin version must be a string")
	}
	if version == "" {
		return "0.1.0", nil
	}
	return version, nil
}
