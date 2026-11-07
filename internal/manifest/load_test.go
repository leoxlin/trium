package manifest_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/leoxlin/skillets/internal/manifest"
)

func TestLoadLocalSkills(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "review", "review", "Review code")
	path := writeManifest(t, dir, "version: 1\ntarget:\n  agent: kimi\nskills:\n  - name: review\n    source: skills/review\n")
	loaded, err := manifest.Load(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Target.Agent != "kimi" || len(loaded.Desired.Skills) != 1 {
		t.Fatalf("manifest = %#v", loaded)
	}
	if loaded.Desired.Skills[0].Source != filepath.Join(dir, "skills", "review") {
		t.Fatalf("source = %q", loaded.Desired.Skills[0].Source)
	}
}

func TestLoadRejectsInvalidInput(t *testing.T) {
	tests := map[string]string{
		"claude":          "version: 1\ntarget:\n  agent: claude\n",
		"plugins":         "version: 1\ntarget:\n  agent: kimi\nplugins:\n  - name: nope\n    source: nope\n",
		"remote":          "version: 1\ntarget:\n  agent: codex\nskills:\n  - name: review\n    github: owner/repo\n",
		"unknown field":   "version: 1\ntarget:\n  agent: kimi\nunknown: true\n",
		"null":            "version: 1\ntarget:\n  agent: kimi\nskills: null\n",
		"relative target": "version: 1\ntarget:\n  agent: kimi\n  path: relative\n",
	}
	for name, contents := range tests {
		t.Run(name, func(t *testing.T) {
			path := writeManifest(t, t.TempDir(), contents)
			_, err := manifest.Load(context.Background(), path)
			var validation *manifest.ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestLoadValidatesSkillMetadata(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "review", "other", "Review code")
	path := writeManifest(t, dir, "version: 1\ntarget:\n  agent: codex\nskills:\n  - name: review\n    source: skills/review\n")
	_, err := manifest.Load(context.Background(), path)
	var validation *manifest.ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("error = %v", err)
	}
}

func writeSkill(t *testing.T, root, directory, name, description string) {
	t.Helper()
	path := filepath.Join(root, "skills", directory)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	contents := "---\nname: " + name + "\ndescription: " + description + "\n---\nInstructions\n"
	if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeManifest(t *testing.T, dir, contents string) string {
	t.Helper()
	path := filepath.Join(dir, "skillet.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
