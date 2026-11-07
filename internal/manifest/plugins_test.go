package manifest_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leoxlin/skillets/internal/manifest"
)

func TestLoadLocalAndGitPlugins(t *testing.T) {
	repo := t.TempDir()
	writeSkill(t, repo, "review", "review", "Standalone")
	plugin := filepath.Join(repo, "plugins/review")
	for _, path := range []string{".codex-plugin/plugin.json", ".claude-plugin/plugin.json", "kimi.plugin.json"} {
		file := filepath.Join(plugin, path)
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(`{"name":"review","version":"1.0.0"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, repo, "init", "-b", "main")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "initial")
	sha := git(t, repo, "rev-parse", "HEAD")
	gitRemote(t, repo)
	for _, source := range []string{"local", "https://example.com/skills.git"} {
		t.Run(source, func(t *testing.T) {
			declaration := fmt.Sprintf("version: 1\ntarget: [{agent: codex}, {agent: claude}, {agent: kimi}]\nskills:\n  - name: review\n    path: skills/review\nplugins:\n  - name: review\n    source: %s\n    path: plugins/review\n", source)
			if source != "local" {
				declaration += "    ref: main\n"
			}
			loaded, err := manifest.Load(context.Background(), writeManifest(t, repo, declaration))
			if err != nil {
				t.Fatal(err)
			}
			if len(loaded.Desired.Skills) != 1 || len(loaded.Desired.Plugins) != 1 {
				t.Fatalf("desired = %#v", loaded.Desired)
			}
			if !loaded.Desired.Plugins[0].Enabled {
				t.Fatal("desired plugin is disabled")
			}
			if source != "local" && !strings.Contains(loaded.Desired.Plugins[0].Source, sha) {
				t.Fatal("Git source did not resolve to a snapshot")
			}
		})
	}
}

func TestPluginValidationIsSeparateFromSkillValidation(t *testing.T) {
	for name, contents := range map[string]string{
		"missing name":  "{}",
		"wrong name":    `{"name":"other"}`,
		"invalid json":  `{`,
		"null manifest": `null`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "plugin/.claude-plugin/plugin.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
				t.Fatal(err)
			}
			declaration := "version: 1\ntarget: {agent: claude}\nplugins:\n  - name: review\n    path: plugin\n"
			_, err := manifest.Load(context.Background(), writeManifest(t, dir, declaration))
			var validation *manifest.ValidationError
			if !errors.As(err, &validation) || !strings.HasPrefix(validation.Field, "plugins[0]") {
				t.Fatalf("error = %v", err)
			}
		})
	}
	for _, fields := range []string{
		"name: review\n    path: ../outside",
		"name: review\n    path: plugin\n    unknown: true",
		"name: ../escape\n    path: plugin",
		"name: review\n    source: local\n    ref: main\n    path: plugin",
		"name: review\n    source: example/repo\n    path: ../escape",
	} {
		dir := t.TempDir()
		_, err := manifest.Load(context.Background(), writeManifest(t, dir, "version: 1\ntarget: {agent: kimi}\nplugins:\n  - "+fields+"\n"))
		var validation *manifest.ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("%s: error = %v", fields, err)
		}
	}
}
