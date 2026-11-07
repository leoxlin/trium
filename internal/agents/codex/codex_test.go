package codex_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leoxlin/skillets/internal/agents/codex"
	"github.com/leoxlin/skillets/internal/engine"
	"github.com/leoxlin/skillets/internal/resource"
	"github.com/pelletier/go-toml/v2"
)

func TestDisabledSkillConflict(t *testing.T) {
	home := t.TempDir()
	skills := filepath.Join(t.TempDir(), "skills")
	config := "[[skills.config]]\npath = " + quoted(filepath.Join(skills, "review", "SKILL.md")) + "\nenabled = false\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	a := codex.New(codex.Target{Home: home, SkillsRoot: skills})
	wanted := resource.NewSkill("review", "source", []resource.File{{Path: "SKILL.md", Data: []byte("content")}})
	plan, err := a.Resources().Skills.Plan(context.Background(), []resource.Skill{wanted})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.HasConflicts() || plan.Changes[0].Reason != "skill is disabled in Codex config.toml" {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestAgentContract(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	home := filepath.Join(root, "codex-home")
	skillsRoot := filepath.Join(root, "shared", "skills")
	a := codex.New(codex.Target{Home: home, SkillsRoot: skillsRoot})
	wanted := resource.NewSkill("review", "/source/review", []resource.File{{Path: "SKILL.md", Data: []byte("content")}})
	runner := engine.Engine{}

	plan, err := runner.Plan(ctx, a, resource.Desired{Skills: []resource.Skill{wanted}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Skills.Changes) != 1 || plan.Skills.Changes[0].Action != resource.Add {
		t.Fatalf("plan = %#v", plan)
	}
	if _, err := os.Stat(home); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("plan created agent home: %v", err)
	}

	_, _, err = runner.Apply(ctx, a, resource.Desired{Skills: []resource.Skill{wanted}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(skillsRoot, "review", "SKILL.md"))
	if err != nil || string(data) != "content" {
		t.Fatalf("installed SKILL.md = %q, %v", data, err)
	}
}

func TestLockContention(t *testing.T) {
	a := codex.New(codex.Target{Home: t.TempDir(), SkillsRoot: filepath.Join(t.TempDir(), "skills")})
	unlock, err := a.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unlock() }()
	_, _, err = (engine.Engine{}).Apply(context.Background(), a, resource.Desired{})
	if err == nil {
		t.Fatal("apply acquired an already-held lock")
	}
}

func quoted(value string) string {
	return "\"" + value + "\""
}

func wantedPlugin(body string) resource.Plugin {
	return resource.NewPlugin("review", "source", []resource.File{
		{Path: ".codex-plugin/plugin.json", Data: []byte(`{"name":"review","version":"1.0.0"}`)},
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
	agent := codex.New(codex.Target{Home: home, SkillsRoot: filepath.Join(home, "skills")})
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
	settings := filepath.Join(home, "config.toml")

	settingsData := strings.Replace(string(read(t, settings)), "enabled = true", "enabled = false", 1)
	write(t, settings, []byte("model = 'keep'\n"+settingsData))
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
	var doc map[string]any
	if err := toml.Unmarshal(data, &doc); err != nil || doc["model"] != "keep" {
		t.Fatalf("config lost: %s, %v", data, err)
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
	registry := filepath.Join(home, "config.toml")
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
			agent := codex.New(codex.Target{Home: home, SkillsRoot: filepath.Join(home, "skills")})
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
				registry := filepath.Join(home, "config.toml")
				data := read(t, registry)
				switch mode {
				case "external source":
					data = []byte(strings.Replace(string(data), "source_type = 'local'", "source_type = 'git'", 1))
				case "duplicate records":
					catalog := filepath.Join(home, "plugins/.agents/plugins/marketplace.json")
					var doc map[string]any
					_ = json.Unmarshal(read(t, catalog), &doc)
					entries := doc["plugins"].([]any)
					doc["plugins"] = append(entries, entries[0])
					data, _ = json.Marshal(doc)
					registry = catalog
				case "unsupported registry":
					data = []byte("plugins = []\n")
				}
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

func TestCodexPluginCacheAndVersionUpdate(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	agent := codex.New(codex.Target{Home: home, SkillsRoot: filepath.Join(home, "skills")})
	runner := engine.Engine{}
	desired := resource.Desired{Plugins: []resource.Plugin{wantedPlugin("first")}}
	if _, _, err := runner.Apply(ctx, agent, desired); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(home, "plugins/cache/skillet/review/1.0.0")
	write(t, filepath.Join(cache, "commands/review.md"), []byte("drift"))
	plan, err := runner.Plan(ctx, agent, desired)
	if err != nil {
		t.Fatal(err)
	}
	assertPluginAction(t, plan, resource.Update)
	if _, _, err := runner.Apply(ctx, agent, desired); err != nil {
		t.Fatal(err)
	}
	if string(read(t, filepath.Join(cache, "commands/review.md"))) != "first" {
		t.Fatal("cache not repaired")
	}
	plugin := wantedPlugin("second")
	for i := range plugin.Files {
		if plugin.Files[i].Path == ".codex-plugin/plugin.json" {
			plugin.Files[i].Data = []byte(`{"name":"review","version":"2.0.0"}`)
		}
	}
	desired.Plugins = []resource.Plugin{resource.NewPlugin(plugin.Name, plugin.Source, plugin.Files)}
	if _, _, err := runner.Apply(ctx, agent, desired); err != nil {
		t.Fatal(err)
	}
	updated := filepath.Join(home, "plugins/cache/skillet/review/2.0.0/commands/review.md")
	if string(read(t, updated)) != "second" {
		t.Fatal("new version cache missing")
	}
	read(t, filepath.Join(cache, "commands/review.md"))
	plan, err = runner.Plan(ctx, agent, desired)
	if err != nil {
		t.Fatal(err)
	}
	assertPluginAction(t, plan, resource.Unchanged)
}
