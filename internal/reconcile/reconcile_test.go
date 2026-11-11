package reconcile_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leoxlin/trium/internal/manifest"
	"github.com/leoxlin/trium/internal/reconcile"
	"github.com/pelletier/go-toml/v2"
)

type fixture struct {
	t                                         *testing.T
	kind, root, source, home, skills, version string
	homes                                     reconcile.Homes
}

func newFixture(t *testing.T, kind string) *fixture {
	t.Helper()
	root := t.TempDir()
	homes := reconcile.Homes{User: filepath.Join(root, "user"), Codex: filepath.Join(root, "codex"), Claude: filepath.Join(root, "claude"), Kimi: filepath.Join(root, "kimi")}
	home := map[string]string{"codex": homes.Codex, "claude": homes.Claude, "kimi": homes.Kimi}[kind]
	skills := filepath.Join(home, "skills")
	if kind == "codex" {
		skills = filepath.Join(homes.User, ".agents", "skills")
	}
	return &fixture{t: t, kind: kind, root: root, source: filepath.Join(root, "source"), home: home, skills: skills, homes: homes, version: "1.0.0"}
}
func (f *fixture) prepare(skill, plugin string) *reconcile.Prepared {
	f.t.Helper()
	document := "version: 1\ntarget: {agent: " + f.kind + "}\n"
	if skill != "" {
		write(f.t, filepath.Join(f.source, "skill/SKILL.md"), []byte("---\nname: review\ndescription: Review code\n---\n"+skill))
		document += "skills: [{name: review, path: skill}]\n"
	}
	if plugin != "" {
		native := "." + f.kind + "-plugin/plugin.json"
		if f.kind == "kimi" {
			native = "kimi.plugin.json"
		}
		write(f.t, filepath.Join(f.source, "plugin", native), []byte(fmt.Sprintf(`{"name":"review","version":%q}`, f.version)))
		write(f.t, filepath.Join(f.source, "plugin/commands/review.md"), []byte(plugin))
		document += "plugins: [{name: review, path: plugin}]\n"
	}
	spec, err := manifest.Parse([]byte(document))
	if err != nil {
		f.t.Fatal(err)
	}
	prepared, err := reconcile.Prepare(context.Background(), spec, f.source, f.homes)
	if err != nil {
		f.t.Fatal(err)
	}
	return prepared
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
func plan(t *testing.T, p *reconcile.Prepared) reconcile.TargetPlan {
	t.Helper()
	plans, err := p.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 {
		t.Fatalf("plans = %#v", plans)
	}
	return plans[0]
}
func apply(t *testing.T, p *reconcile.Prepared) reconcile.TargetResult {
	t.Helper()
	results, err := p.Apply(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Plan.HasConflicts() {
		t.Fatalf("results = %#v", results)
	}
	return results[0]
}
func action(t *testing.T, p reconcile.TargetPlan, kind reconcile.Kind, want reconcile.Action) {
	t.Helper()
	for _, change := range p.Changes {
		if change.Kind == kind {
			if change.Action != want {
				t.Fatalf("change = %#v, want %s", change, want)
			}
			return
		}
	}
	t.Fatalf("missing %s change: %#v", kind, p)
}
func (f *fixture) registry() string {
	switch f.kind {
	case "codex":
		return filepath.Join(f.home, "config.toml")
	case "claude":
		return filepath.Join(f.home, "plugins/installed_plugins.json")
	default:
		return filepath.Join(f.home, "plugins/installed.json")
	}
}
func (f *fixture) disable() {
	if f.kind == "codex" {
		path := f.registry()
		write(f.t, path, []byte("model = 'keep'\n"+strings.Replace(string(read(f.t, path)), "enabled = true", "enabled = false", 1)))
		return
	}
	path := f.registry()
	if f.kind == "claude" {
		path = filepath.Join(f.home, "settings.json")
	}
	var doc map[string]any
	if err := json.Unmarshal(read(f.t, path), &doc); err != nil {
		f.t.Fatal(err)
	}
	doc["unrelated"] = map[string]any{"keep": true}
	if f.kind == "claude" {
		doc["enabledPlugins"].(map[string]any)["review@trium"] = false
	} else {
		doc["plugins"].([]any)[0].(map[string]any)["enabled"] = false
	}
	data, err := json.Marshal(doc)
	if err != nil {
		f.t.Fatal(err)
	}
	write(f.t, path, data)
}

func TestNativeLifecycle(t *testing.T) {
	for _, kind := range []string{"codex", "claude", "kimi"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t, kind)
			prepared := f.prepare("standalone", "first")
			initial := plan(t, prepared)
			action(t, initial, reconcile.Skills, reconcile.Add)
			action(t, initial, reconcile.Plugins, reconcile.Add)
			for _, path := range []string{f.home, f.homes.User} {
				if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("prepare/plan wrote %s: %v", path, err)
				}
			}
			// Editing the public report cannot change what Apply installs.
			initial.Changes[0].Name = "injected"
			result := apply(t, prepared)
			if len(result.Applied) != 2 || result.Applied[1].Kind != reconcile.Plugins {
				t.Fatalf("report = %#v", result)
			}
			installed := filepath.Join(f.home, "plugins/managed/review")
			if string(read(t, filepath.Join(installed, "commands/review.md"))) != "first" {
				t.Fatal("plugin content missing")
			}
			if !strings.HasSuffix(string(read(t, filepath.Join(f.skills, "review/SKILL.md"))), "standalone") {
				t.Fatal("standalone skill missing")
			}
			if result := apply(t, prepared); len(result.Backups) != 0 || len(result.Applied) != 0 {
				t.Fatalf("second apply = %#v", result)
			}
			f.disable()
			action(t, plan(t, prepared), reconcile.Plugins, reconcile.Update)
			apply(t, prepared)
			action(t, plan(t, prepared), reconcile.Plugins, reconcile.Unchanged)
			write(t, filepath.Join(installed, "stale"), []byte("old"))
			unrelated := filepath.Join(f.home, "plugins/unrelated/keep")
			write(t, unrelated, []byte("keep"))
			prepared = f.prepare("standalone", "second")
			apply(t, prepared)
			if _, err := os.Stat(filepath.Join(installed, "stale")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("stale retained: %v", err)
			}
			if string(read(t, unrelated)) != "keep" {
				t.Fatal("unrelated content changed")
			}
			settings := f.registry()
			if kind == "claude" {
				settings = filepath.Join(f.home, "settings.json")
			}
			data := read(t, settings)
			if kind == "codex" {
				var doc map[string]any
				if err := toml.Unmarshal(data, &doc); err != nil || doc["model"] != "keep" {
					t.Fatalf("config preservation: %s %v", data, err)
				}
			} else if !strings.Contains(string(data), "unrelated") {
				t.Fatalf("settings lost: %s", data)
			}
			action(t, plan(t, prepared), reconcile.Skills, reconcile.Unchanged)
			action(t, plan(t, prepared), reconcile.Plugins, reconcile.Unchanged)
			apply(t, f.prepare("", ""))
			read(t, filepath.Join(installed, "commands/review.md"))
			read(t, filepath.Join(f.skills, "review/SKILL.md"))
		})
	}
}

func TestPluginConflictsBlockSkillWrites(t *testing.T) {
	for _, kind := range []string{"codex", "claude", "kimi"} {
		for _, mode := range []string{"orphan bundle", "missing bundle", "symlink ancestor", "external source", "duplicate records", "unsupported registry"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				f := newFixture(t, kind)
				prepared := f.prepare("", "first")
				installed := filepath.Join(f.home, "plugins/managed/review")
				switch mode {
				case "orphan bundle":
					write(t, filepath.Join(installed, "orphan"), []byte("orphan"))
				case "symlink ancestor":
					if err := os.MkdirAll(f.home, 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(t.TempDir(), filepath.Join(f.home, "plugins")); err != nil {
						t.Fatal(err)
					}
				default:
					apply(t, prepared)
					if mode == "missing bundle" {
						if err := os.RemoveAll(installed); err != nil {
							t.Fatal(err)
						}
						break
					}
					registry := f.registry()
					data := read(t, registry)
					if kind == "codex" {
						switch mode {
						case "external source":
							data = []byte(strings.Replace(string(data), "source_type = 'local'", "source_type = 'git'", 1))
						case "unsupported registry":
							data = []byte("plugins = []\n")
						case "duplicate records":
							registry = filepath.Join(f.home, "plugins/.agents/plugins/marketplace.json")
							var doc map[string]any
							if err := json.Unmarshal(read(t, registry), &doc); err != nil {
								t.Fatal(err)
							}
							entries := doc["plugins"].([]any)
							doc["plugins"] = append(entries, entries[0])
							data, _ = json.Marshal(doc)
						}
					} else {
						var doc map[string]any
						if err := json.Unmarshal(data, &doc); err != nil {
							t.Fatal(err)
						}
						switch mode {
						case "unsupported registry":
							doc["version"] = 99
						case "external source":
							if kind == "claude" {
								records := doc["plugins"].(map[string]any)
								records["review@marketplace"] = records["review@trium"]
								delete(records, "review@trium")
							} else {
								doc["plugins"].([]any)[0].(map[string]any)["source"] = "github"
							}
						case "duplicate records":
							if kind == "claude" {
								records := doc["plugins"].(map[string]any)
								entries := records["review@trium"].([]any)
								records["review@trium"] = append(entries, entries[0])
							} else {
								entries := doc["plugins"].([]any)
								doc["plugins"] = append(entries, entries[0])
							}
						}
						data, _ = json.Marshal(doc)
					}
					write(t, registry, data)
				}
				prepared = f.prepare("new", "first")
				results, err := prepared.Apply(context.Background())
				if err == nil && (len(results) == 0 || !results[0].Plan.HasConflicts()) {
					t.Fatalf("accepted conflict: %#v", results)
				}
				if _, err := os.Stat(filepath.Join(f.skills, "review")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("skill written before conflict preflight: %v", err)
				}
			})
		}
	}
}

func TestCodexPolicyAndCache(t *testing.T) {
	f := newFixture(t, "codex")
	prepared := f.prepare("content", "")
	config := "[[skills.config]]\npath = " + fmt.Sprintf("%q", filepath.Join(f.skills, "review/SKILL.md")) + "\nenabled = false\n"
	write(t, f.registry(), []byte(config))
	action(t, plan(t, prepared), reconcile.Skills, reconcile.Conflict)
	// Apply replans after a preview; changed native policy is respected.
	if err := os.Remove(f.registry()); err != nil {
		t.Fatal(err)
	}
	action(t, plan(t, prepared), reconcile.Skills, reconcile.Add)
	write(t, f.registry(), []byte(config))
	results, err := prepared.Apply(context.Background())
	if err != nil || !results[0].Plan.HasConflicts() {
		t.Fatalf("fresh plan = %#v %v", results, err)
	}
	if err := os.Remove(f.registry()); err != nil {
		t.Fatal(err)
	}
	prepared = f.prepare("", "first")
	apply(t, prepared)
	cache := filepath.Join(f.home, "plugins/cache/trium/review/1.0.0/commands/review.md")
	write(t, cache, []byte("drift"))
	action(t, plan(t, prepared), reconcile.Plugins, reconcile.Update)
	apply(t, prepared)
	if string(read(t, cache)) != "first" {
		t.Fatal("cache not repaired")
	}
	f.version = "2.0.0"
	prepared = f.prepare("", "second")
	apply(t, prepared)
	if string(read(t, filepath.Join(f.home, "plugins/cache/trium/review/2.0.0/commands/review.md"))) != "second" {
		t.Fatal("new cache missing")
	}
	read(t, cache)
	action(t, plan(t, prepared), reconcile.Plugins, reconcile.Unchanged)
}

func TestClaudeProtectedSkills(t *testing.T) {
	for _, mode := range []string{"synced", "trash", "symlinked catalog", "symlinked skill"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t, "claude")
			name := "review"
			if mode == "synced" {
				name = "synced"
			}
			if mode == "trash" {
				f.homes.Claude = filepath.Join(f.home, ".trash", "old")
				f.home = f.homes.Claude
				f.skills = filepath.Join(f.home, "skills")
			}
			if strings.HasPrefix(mode, "symlinked") {
				path := f.skills
				if mode == "symlinked skill" {
					path = filepath.Join(path, name)
				}
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), path); err != nil {
					t.Fatal(err)
				}
			}
			write(t, filepath.Join(f.source, "skill/SKILL.md"), []byte("---\nname: "+name+"\ndescription: Review\n---\ncontent"))
			spec, err := manifest.Parse([]byte("version: 1\ntarget: {agent: claude}\nskills: [{name: " + name + ", path: skill}]"))
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := reconcile.Prepare(context.Background(), spec, f.source, f.homes)
			if err != nil {
				t.Fatal(err)
			}
			action(t, plan(t, prepared), reconcile.Skills, reconcile.Conflict)
			results, err := prepared.Apply(context.Background())
			if err != nil || !results[0].Plan.HasConflicts() || len(results[0].Applied) != 0 || len(results[0].Backups) != 0 {
				t.Fatalf("conflict apply: %#v %v", results, err)
			}
		})
	}
}

func TestPrepareValidatesAllTargetsBeforeWrites(t *testing.T) {
	f := newFixture(t, "kimi")
	f.prepare("content", "plugin")
	spec, err := manifest.Parse([]byte("version: 1\ntarget: [{agent: kimi}, {agent: claude}]\nskills: [{name: review, path: skill}]\nplugins: [{name: review, path: plugin}]"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = reconcile.Prepare(context.Background(), spec, f.source, f.homes)
	var input *reconcile.InputError
	if !errors.As(err, &input) || input.Field != "plugins[0]" {
		t.Fatalf("validation = %v", err)
	}
	for _, home := range []string{f.home, f.homes.Claude, f.homes.User} {
		if _, err := os.Stat(home); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("prepare wrote home: %v", err)
		}
	}
	for _, contents := range []string{"{}", `{"name":"other"}`, "{", "null"} {
		write(t, filepath.Join(f.source, "plugin/.claude-plugin/plugin.json"), []byte(contents))
		if _, err := reconcile.Prepare(context.Background(), spec, f.source, f.homes); !errors.As(err, &input) {
			t.Fatalf("plugin accepted: %s %v", contents, err)
		}
	}
	write(t, filepath.Join(f.source, "plugin/.claude-plugin/plugin.json"), []byte(`{"name":"review"}`))
	write(t, filepath.Join(f.source, "skill/SKILL.md"), []byte("---\nname: wrong\ndescription: Review\n---\n"))
	if _, err := reconcile.Prepare(context.Background(), spec, f.source, f.homes); !errors.As(err, &input) || !strings.HasPrefix(input.Field, "skills[0]") {
		t.Fatalf("skill validation: %v", err)
	}
}

func TestPreparedRetainsSnapshots(t *testing.T) {
	f := newFixture(t, "kimi")
	prepared := f.prepare("content", "plugin")
	if err := os.RemoveAll(f.source); err != nil {
		t.Fatal(err)
	}
	apply(t, prepared)
	action(t, plan(t, prepared), reconcile.Skills, reconcile.Unchanged)
	action(t, plan(t, prepared), reconcile.Plugins, reconcile.Unchanged)
}

func TestLaterTargetConflictKeepsEarlierSuccess(t *testing.T) {
	f := newFixture(t, "kimi")
	f.prepare("content", "")
	spec, err := manifest.Parse([]byte("version: 1\ntarget: [{agent: kimi}, {agent: claude}, {agent: codex}]\nskills: [{name: review, path: skill}]"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(f.homes.Claude, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(f.homes.Claude, "skills")); err != nil {
		t.Fatal(err)
	}
	prepared, err := reconcile.Prepare(context.Background(), spec, f.source, f.homes)
	if err != nil {
		t.Fatal(err)
	}
	results, err := prepared.Apply(context.Background())
	if err != nil || len(results) != 2 || len(results[0].Applied) != 1 || !results[1].Plan.HasConflicts() {
		t.Fatalf("results = %#v %v", results, err)
	}
	read(t, filepath.Join(f.homes.Kimi, "skills/review/SKILL.md"))
	if _, err := os.Stat(filepath.Join(f.homes.User, ".agents")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("later target was attempted: %v", err)
	}
}

func TestNativeJSONRejectsAmbiguousDocuments(t *testing.T) {
	for _, contents := range []string{`{"version":1,"version":1,"plugins":[]}`, `{"version":1,"plugins":[],"unknown":{"same":1,"same":2}}`, `{} {}`, `null`, `[]`, `{`} {
		t.Run(contents, func(t *testing.T) {
			f := newFixture(t, "kimi")
			prepared := f.prepare("content", "plugin")
			write(t, f.registry(), []byte(contents))
			if _, err := prepared.Apply(context.Background()); err == nil {
				t.Fatal("ambiguous registry accepted")
			}
			if _, err := os.Stat(filepath.Join(f.skills, "review")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("skill written before decoding: %v", err)
			}
		})
	}
}
