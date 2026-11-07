package claude_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leoxlin/skillets/internal/agents/claude"
	"github.com/leoxlin/skillets/internal/engine"
	"github.com/leoxlin/skillets/internal/resource"
)

func TestProtectedSkills(t *testing.T) {
	for _, kind := range []string{"synced", "trash", "symlinked catalog", "symlinked skill"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			name := "review"
			switch kind {
			case "synced":
				name = "synced"
			case "trash":
				home = filepath.Join(home, ".trash", "old-home")
			case "symlinked catalog", "symlinked skill":
				link := filepath.Join(home, "skills")
				if kind == "symlinked skill" {
					if err := os.Mkdir(link, 0o755); err != nil {
						t.Fatal(err)
					}
					link = filepath.Join(link, name)
				}
				if err := os.Symlink(t.TempDir(), link); err != nil {
					t.Fatal(err)
				}
			}
			a := claude.New(claude.Target{Home: home})
			wanted := resource.NewSkill(name, "source", []resource.File{{Path: "SKILL.md", Data: []byte("content")}})
			desired := resource.Desired{Skills: []resource.Skill{wanted}}
			plan, err := (engine.Engine{}).Plan(context.Background(), a, desired)
			if err != nil || !plan.HasConflicts() {
				t.Fatalf("plan = %#v, %v", plan, err)
			}
			plan, report, err := (engine.Engine{}).Apply(context.Background(), a, desired)
			if err != nil || !plan.HasConflicts() || len(report.Applied) != 0 || len(report.Backups) != 0 {
				t.Fatalf("apply = %#v, %#v, %v", plan, report, err)
			}
		})
	}
}

func TestLockContention(t *testing.T) {
	a := claude.New(claude.Target{Home: t.TempDir()})
	unlock, err := a.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unlock() }()
	if _, _, err := (engine.Engine{}).Apply(context.Background(), a, resource.Desired{}); err == nil {
		t.Fatal("apply acquired an already-held lock")
	}
}

func wantedPlugin(body string) resource.Plugin {
	return resource.NewPlugin("review", "source", []resource.File{
		{Path: ".claude-plugin/plugin.json", Data: []byte(`{"name":"review","version":"1.0.0"}`)},
		{Path: "commands/review.md", Data: []byte(body)},
	})
}

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assertPluginAction(t *testing.T, plan engine.Plan, action resource.Action) {
	t.Helper()
	if len(plan.Plugins.Changes) != 1 || plan.Plugins.Changes[0].Action != action {
		t.Fatalf("plugins = %#v, want %s", plan.Plugins, action)
	}
}

func TestPluginLifecycleKeepsSkillsSeparate(t *testing.T) {
	ctx := context.Background()
	home := filepath.Join(t.TempDir(), "agent")
	agent := claude.New(claude.Target{Home: home})
	runner := engine.Engine{}
	plugin := wantedPlugin("first")
	skill := resource.NewSkill("review", "source", []resource.File{{Path: "SKILL.md", Data: []byte("standalone")}})
	desired := resource.Desired{Skills: []resource.Skill{skill}, Plugins: []resource.Plugin{plugin}}
	plan, err := runner.Plan(ctx, agent, desired)
	if err != nil {
		t.Fatal(err)
	}
	assertPluginAction(t, plan, resource.Add)
	if _, err := os.Stat(home); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("plan mutated home: %v", err)
	}
	_, report, err := runner.Apply(ctx, agent, desired)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Applied) != 2 || report.Applied[1].Kind != "plugins" {
		t.Fatalf("report = %#v", report)
	}
	if string(read(t, filepath.Join(home, "skills/review/SKILL.md"))) != "standalone" {
		t.Fatal("skill changed")
	}
	path := filepath.Join(home, "plugins/managed/review")
	if string(read(t, filepath.Join(path, "commands/review.md"))) != "first" {
		t.Fatal("plugin missing")
	}
	_, report, err = runner.Apply(ctx, agent, desired)
	if err != nil || len(report.Backups) != 0 {
		t.Fatalf("second apply = %#v, %v", report, err)
	}
	write(t, filepath.Join(path, "stale"), []byte("old"))
	unrelated := filepath.Join(home, "plugins/unrelated/keep")
	write(t, unrelated, []byte("keep"))
	// Disable the native registration and add an unrelated native setting.
	settings := filepath.Join(home, "settings.json")
	var doc map[string]any
	if err := json.Unmarshal(read(t, settings), &doc); err != nil {
		t.Fatal(err)
	}
	doc["unrelated"] = map[string]any{"keep": true}
	doc["enabledPlugins"].(map[string]any)["review@skillet"] = false
	settingsData, _ := json.Marshal(doc)
	write(t, settings, settingsData)
	// The same bytes still require an update when the plugin is disabled.
	plan, err = runner.Plan(ctx, agent, desired)
	if err != nil {
		t.Fatal(err)
	}
	assertPluginAction(t, plan, resource.Update)
	// Remove content drift to exercise enable-only reconciliation.
	if err := os.Remove(filepath.Join(path, "stale")); err != nil {
		t.Fatal(err)
	}
	plan, err = runner.Plan(ctx, agent, desired)
	if err != nil {
		t.Fatal(err)
	}
	assertPluginAction(t, plan, resource.Update)
	write(t, filepath.Join(path, "stale"), []byte("old"))
	desired.Plugins = []resource.Plugin{wantedPlugin("second")}
	_, report, err = runner.Apply(ctx, agent, desired)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(path, "stale")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale file retained: %v", err)
	}
	if string(read(t, unrelated)) != "keep" || string(read(t, filepath.Join(home, "skills/review/SKILL.md"))) != "standalone" {
		t.Fatal("unrelated content changed")
	}
	data := read(t, settings)
	if !strings.Contains(string(data), "unrelated") {
		t.Fatalf("settings lost: %s", data)
	}
	plan, err = runner.Plan(ctx, agent, desired)
	if err != nil {
		t.Fatal(err)
	}
	assertPluginAction(t, plan, resource.Unchanged)
	// Registry drift aborts before replacing a bundle.
	desired.Plugins = []resource.Plugin{wantedPlugin("third")}
	plan, err = runner.Plan(ctx, agent, desired)
	if err != nil {
		t.Fatal(err)
	}
	registry := filepath.Join(home, "plugins/installed_plugins.json")
	write(t, registry, append(read(t, registry), '\n'))
	_, err = agent.Resources().Plugins.Apply(ctx, plan.Plugins)
	var drift *resource.DriftError
	if !errors.As(err, &drift) {
		t.Fatalf("drift error = %v", err)
	}
	if string(read(t, filepath.Join(path, "commands/review.md"))) != "second" {
		t.Fatal("drift changed bundle")
	}
	// A failed verification restores both the native registry and bundle.
	before := read(t, registry)
	broken := wantedPlugin("third")
	broken.Hash[0] ^= 0xff
	desired.Plugins = []resource.Plugin{broken}
	_, _, err = runner.Apply(ctx, agent, desired)
	if err == nil {
		t.Fatal("expected verification failure")
	}
	if string(read(t, registry)) != string(before) || string(read(t, filepath.Join(path, "commands/review.md"))) != "second" {
		t.Fatal("rollback failed")
	}
	// Removing declarations is additive: no plugin is pruned.
	if _, _, err := runner.Apply(ctx, agent, resource.Desired{}); err != nil {
		t.Fatal(err)
	}
	read(t, filepath.Join(path, "commands/review.md"))
}

func TestPluginConflictsBlockSkillWrites(t *testing.T) {
	for _, mode := range []string{"orphan bundle", "missing bundle", "symlink ancestor", "external source", "duplicate records", "unsupported registry"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			home := filepath.Join(t.TempDir(), "agent")
			agent := claude.New(claude.Target{Home: home})
			runner := engine.Engine{}
			desired := resource.Desired{Plugins: []resource.Plugin{wantedPlugin("first")}}
			path := filepath.Join(home, "plugins/managed/review")
			switch mode {
			case "orphan bundle":
				write(t, filepath.Join(path, "orphan"), []byte("orphan"))
			case "symlink ancestor":
				if err := os.MkdirAll(home, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), filepath.Join(home, "plugins")); err != nil {
					t.Fatal(err)
				}
			default:
				if _, _, err := runner.Apply(ctx, agent, desired); err != nil {
					t.Fatal(err)
				}
				if mode == "missing bundle" {
					if err := os.RemoveAll(path); err != nil {
						t.Fatal(err)
					}
					break
				}
				registry := filepath.Join(home, "plugins/installed_plugins.json")
				data := read(t, registry)
				var doc map[string]any
				_ = json.Unmarshal(data, &doc)
				switch mode {
				case "external source":
					records := doc["plugins"].(map[string]any)
					records["review@marketplace"] = records["review@skillet"]
					delete(records, "review@skillet")
				case "duplicate records":
					entries := doc["plugins"].(map[string]any)["review@skillet"].([]any)
					doc["plugins"].(map[string]any)["review@skillet"] = append(entries, entries[0])
				case "unsupported registry":
					doc["version"] = 99
				}
				data, _ = json.Marshal(doc)
				write(t, registry, data)
			}
			desired.Skills = []resource.Skill{resource.NewSkill("new-skill", "source", []resource.File{{Path: "SKILL.md", Data: []byte("new")}})}
			plan, _, err := runner.Apply(ctx, agent, desired)
			if err == nil && !plan.HasConflicts() {
				t.Fatalf("accepted %s: %#v", mode, plan)
			}
			if _, err := os.Stat(filepath.Join(home, "skills/new-skill")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("skill written before plugin preflight: %v", err)
			}
		})
	}
}
