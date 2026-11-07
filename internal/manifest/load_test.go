package manifest_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leoxlin/skillets/internal/agents/kimi"
	"github.com/leoxlin/skillets/internal/engine"
	"github.com/leoxlin/skillets/internal/manifest"
	"github.com/leoxlin/skillets/internal/resource"
)

func TestLoadLocalSkills(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "review", "review", "Review code")
	path := writeManifest(t, dir, "version: 1\ntarget:\n  agent: kimi\nskills:\n  - name: review\n    path: skills/review\n")
	loaded, err := manifest.Load(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Targets) != 1 || loaded.Targets[0].Agent != "kimi" || len(loaded.Desired.Skills) != 1 {
		t.Fatalf("manifest = %#v", loaded)
	}
	if loaded.Desired.Skills[0].Source != filepath.Join(dir, "skills", "review") {
		t.Fatalf("source = %q", loaded.Desired.Skills[0].Source)
	}
	explicit, err := manifest.Load(context.Background(), writeManifest(t, dir,
		"version: 1\ntarget:\n  agent: kimi\nskills:\n  - name: review\n    source: local\n    path: skills/review\n"))
	if err != nil {
		t.Fatal(err)
	}
	if explicit.Desired.Skills[0].Source != loaded.Desired.Skills[0].Source ||
		explicit.Desired.Skills[0].Hash != loaded.Desired.Skills[0].Hash {
		t.Fatal("source: local differs from omitted source")
	}
	_, err = manifest.Load(context.Background(), writeManifest(t, dir,
		"version: 1\ntarget:\n  agent: kimi\nskills:\n  - name: review\n    source: ./skills/review\n"))
	var validation *manifest.ValidationError
	if !errors.As(err, &validation) || validation.Field != "skills[0].source" {
		t.Fatalf("local source error = %v", err)
	}
}

func TestLoadRejectsInvalidInput(t *testing.T) {
	tests := map[string]string{
		"empty targets":         "version: 1\ntarget: []\n",
		"missing target":        "version: 1\n",
		"invalid list agent":    "version: 1\ntarget:\n  - agent: codex\n  - agent: nope\n",
		"unknown target field":  "version: 1\ntarget:\n  - agent: kimi\n    typo: true\n",
		"duplicate targets":     "version: 1\ntarget:\n  - agent: kimi\n  - agent: kimi\n",
		"scalar target":         "version: 1\ntarget: kimi\n",
		"null list target":      "version: 1\ntarget: [null]\n",
		"relative list path":    "version: 1\ntarget:\n  - agent: kimi\n    path: relative\n",
		"duplicate target keys": "version: 1\ntarget:\n  - agent: kimi\n    agent: codex\n",

		"claude":          "version: 1\ntarget:\n  agent: claude\n",
		"plugins":         "version: 1\ntarget:\n  agent: kimi\nplugins:\n  - name: nope\n    source: nope\n",
		"legacy github":   "version: 1\ntarget:\n  agent: codex\nskills:\n  - name: review\n    github: owner/repo\n",
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
	path := writeManifest(t, dir, "version: 1\ntarget:\n  agent: codex\nskills:\n  - name: review\n    path: skills/review\n")
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

func TestLoadMultipleTargets(t *testing.T) {
	path := writeManifest(t, t.TempDir(), "version: 1\ntarget:\n  - agent: codex\n  - agent: kimi\n    path: ~/custom-kimi\n")
	loaded, err := manifest.Load(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Targets) != 2 || loaded.Targets[0].Agent != "codex" ||
		loaded.Targets[1] != (manifest.Target{Agent: "kimi", Path: "~/custom-kimi"}) {
		t.Fatalf("targets = %#v", loaded.Targets)
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.com",
		"-c", "commit.gpgsign=false", "-c", "core.hooksPath=" + os.DevNull}, args...)...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func gitRemote(t *testing.T, repo string) {
	t.Helper()
	config := filepath.Join(t.TempDir(), "gitconfig")
	git(t, repo, "config", "--file", config, "url."+repo+".insteadOf", "https://example.com/skills.git")
	t.Setenv("GIT_CONFIG_GLOBAL", config)
}

func TestLoadGitSkills(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "repository")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "init", "-b", "main")
	gitRemote(t, repo)
	writeSkill(t, repo, "review", "review", "Review code")
	script := filepath.Join(repo, "skills", "review", "run.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "initial")
	sha := git(t, repo, "rev-parse", "HEAD")
	git(t, repo, "tag", "v1")
	git(t, repo, "checkout", "-b", "other")
	writeSkill(t, repo, "review", "review", "Other version")
	git(t, repo, "commit", "-am", "other")
	git(t, repo, "checkout", "main")

	localFiles, err := resource.Snapshot(filepath.Join(repo, "skills", "review"))
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"", "main", "v1", sha, "other"} {
		t.Run("ref="+ref, func(t *testing.T) {
			contents := "version: 1\ntarget:\n  agent: kimi\nskills:\n  - name: review\n    source: https://example.com/skills.git\n    path: skills/review\n"
			if ref != "" {
				contents += "    ref: " + ref + "\n"
			}
			loaded, err := manifest.Load(context.Background(), writeManifest(t, dir, contents))
			if err != nil {
				t.Fatal(err)
			}
			skill := loaded.Desired.Skills[0]
			if ref != "other" && (skill.Hash != resource.HashFiles(localFiles) || !strings.Contains(skill.Source, sha)) {
				t.Fatalf("snapshot differs from local skill: %#v", skill)
			}
			if ref == "other" && skill.Hash == resource.HashFiles(localFiles) {
				t.Fatal("branch was not selected")
			}
			runner := engine.Engine{}
			target := kimi.New(kimi.Target{Home: t.TempDir()})
			plan, _, err := runner.Apply(context.Background(), target, loaded.Desired)
			if err != nil || plan.HasConflicts() {
				t.Fatalf("apply: %v, %#v", err, plan)
			}
			plan, err = runner.Plan(context.Background(), target, loaded.Desired)
			if err != nil || plan.Skills.HasChanges() {
				t.Fatalf("plan after apply: %v, %#v", err, plan)
			}
		})
	}

	// A repository whose root is a bundle must not install Git's own metadata.
	writeSkill(t, dir, "root", "review", "Root skill")
	root := filepath.Join(dir, "skills", "root")
	git(t, root, "init", "-b", "main")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "root")
	gitRemote(t, root)
	loaded, err := manifest.Load(context.Background(), writeManifest(t, dir,
		"version: 1\ntarget:\n  agent: codex\nskills:\n  - name: review\n    source: https://example.com/skills.git\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Desired.Skills[0].Files) != 1 {
		t.Fatalf("root files = %#v", loaded.Desired.Skills[0].Files)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	// Installation uses the acquired bytes even when the original repository disappears.
	_, _, err = (engine.Engine{}).Apply(context.Background(), kimi.New(kimi.Target{Home: t.TempDir()}), loaded.Desired)
	if err != nil {
		t.Fatalf("apply acquired snapshot: %v", err)
	}
}

func TestLoadSourceForms(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "review", "review", "Review code")
	repo := filepath.Join(dir, "skills", "review")
	git(t, repo, "init", "-b", "main")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "initial")
	sha := git(t, repo, "rev-parse", "HEAD")
	// Git rewrites remote URLs to a local repository so no test needs network access.
	config := filepath.Join(dir, "gitconfig")
	for _, remote := range []string{
		"https://github.com/example/skills", "https://github.com/example/skills.git",
		"https://example.com/skills.git", "ssh://git@example.com/skills.git",
		"git@example.com:skills.git", "git://example.com/skills.git",
	} {
		git(t, dir, "config", "--file", config, "--add", "url."+repo+".insteadOf", remote)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	for _, source := range []string{
		"example/skills", "github:example/skills", "github.com/example/skills",
		"https://github.com/example/skills", "https://github.com/example/skills.git", "https://example.com/skills.git",
		"git:https://example.com/skills.git", "git+https://example.com/skills.git",
		"ssh://git@example.com/skills.git", "git+ssh://git@example.com/skills.git",
		"git@example.com:skills.git", "git://example.com/skills.git",
	} {
		t.Run(source, func(t *testing.T) {
			contents := fmt.Sprintf("version: 1\ntarget:\n  agent: kimi\nskills:\n  - name: review\n    source: %q\n", source)
			loaded, err := manifest.Load(context.Background(), writeManifest(t, dir, contents))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(loaded.Desired.Skills[0].Source, sha) {
				t.Fatalf("source did not resolve to commit: %q", loaded.Desired.Skills[0].Source)
			}
		})
	}
}

func TestLoadGitRejectsInvalidDeclarations(t *testing.T) {
	for name, fields := range map[string]string{
		"duplicate source": "source: example/skills\n    source: local", "missing": "ref: main",
		"escape": "source: example/skills\n    path: ../outside", "absolute": "source: example/skills\n    path: /outside",
		"backslash": `source: example/skills` + "\n    path: 'a\\b'", "ref option": "source: example/skills\n    ref: --all",
		"ref whitespace": "source: example/skills\n    ref: 'bad ref'", "url option": "source: git:--upload-pack=bad",
		"refspec": "source: example/skills\n    ref: 'main:refs/heads/other'", "wildcard": "source: example/skills\n    ref: '*'",
		"legacy git": "git: repo", "empty git": "source: 'git:'", "empty source": "source: ''",
		"bad shorthand": "source: github:owner", "escaping shorthand": "source: github:../repo",
		"bad url": "source: https://", "unsupported scheme": "source: ext::command",
		"local ref": "path: skill\n    ref: main", "local source": "source: ./skill\n    path: nested",
		"absolute source": "source: /tmp/skill", "git local source": "source: git:./skill",
		"file source":        "source: file:///tmp/skill",
		"local missing path": "source: local", "explicit local ref": "source: local\n    path: skill\n    ref: main",
	} {
		t.Run(name, func(t *testing.T) {
			contents := "version: 1\ntarget:\n  agent: kimi\nskills:\n  - name: review\n    " + fields + "\n"
			_, err := manifest.Load(context.Background(), writeManifest(t, t.TempDir(), contents))
			var validation *manifest.ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestLoadGitFailures(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-b", "main")
	gitRemote(t, dir)
	writeSkill(t, dir, "review", "review", "Review code")
	writeSkill(t, dir, "wrong", "other", "Review code")
	writeSkill(t, dir, "submodule", "review", "Review code")
	if err := os.Symlink("SKILL.md", filepath.Join(dir, "skills", "review", "link")); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-m", "symlink")
	sha := git(t, dir, "rev-parse", "HEAD")
	git(t, dir, "update-index", "--add", "--cacheinfo", "160000,"+sha+",skills/submodule/child")
	git(t, dir, "commit", "-m", "submodule")
	for _, fields := range []string{
		"path: skills/review", "path: missing", "ref: missing", "path: .", "path: skills/wrong", "path: skills/submodule",
	} {
		t.Run(fields, func(t *testing.T) {
			contents := fmt.Sprintf("version: 1\ntarget:\n  agent: kimi\nskills:\n  - name: review\n    source: %q\n    %s\n", "https://example.com/skills.git", fields)
			if _, err := manifest.Load(context.Background(), writeManifest(t, t.TempDir(), contents)); err == nil {
				t.Fatal("expected acquisition or bundle validation failure")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := manifest.Load(ctx, writeManifest(t, t.TempDir(),
		"version: 1\ntarget:\n  agent: kimi\nskills:\n  - name: review\n    source: example/missing\n"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
}
