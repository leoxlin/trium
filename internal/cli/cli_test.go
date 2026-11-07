package cli_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leoxlin/skillets/internal/cli"
)

func TestKimiPlanApplyPlan(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KIMI_CODE_HOME", filepath.Join(home, "relocated-kimi"))
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	contents := "---\nname: review\ndescription: Review code\n---\nReview carefully.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := "version: 1\ntarget:\n  agent: kimi\nskills:\n  - name: review\n    path: skill\n"
	manifestPath := filepath.Join(dir, "skillet.yaml")
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := cli.Run(context.Background(), []string{"--file", manifestPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("plan exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "skills/review") || !strings.Contains(stdout.String(), "ADD") {
		t.Fatalf("plan output:\n%s", stdout.String())
	}
	if _, err := os.Stat(os.Getenv("KIMI_CODE_HOME")); !os.IsNotExist(err) {
		t.Fatalf("plan created target: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := cli.Run(context.Background(), []string{"--file", manifestPath, "apply"}, &stdout, &stderr); code != 0 {
		t.Fatalf("apply exit %d: %s", code, stderr.String())
	}
	installed, err := os.ReadFile(filepath.Join(os.Getenv("KIMI_CODE_HOME"), "skills", "review", "SKILL.md"))
	if err != nil || string(installed) != contents {
		t.Fatalf("installed skill = %q, %v", installed, err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := cli.Run(context.Background(), []string{"--file", manifestPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("second plan exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "No changes (1 unchanged).") {
		t.Fatalf("second plan output:\n%s", stdout.String())
	}
}

func TestCodexUsesSharedSkillRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "relocated-codex"))
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	contents := "---\nname: review\ndescription: Review code\n---\nReview carefully.\n"
	if err := os.WriteFile(filepath.Join(dir, "skill", "SKILL.md"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := "version: 1\ntarget:\n  agent: codex\nskills:\n  - name: review\n    path: skill\n"
	path := filepath.Join(dir, "skillet.yaml")
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := cli.Run(context.Background(), []string{"--file", path, "apply"}, &stdout, &stderr); code != 0 {
		t.Fatalf("apply exit %d: %s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "review", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

func TestMultipleTargetsPlanApplyPlan(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KIMI_CODE_HOME", filepath.Join(home, "relocated-kimi"))
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	contents := "---\nname: review\ndescription: Review code\n---\nReview carefully.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := "version: 1\ntarget:\n  - agent: codex\n  - agent: kimi\nskills:\n  - name: review\n    path: skill\n"
	manifestPath := filepath.Join(dir, "skillet.yaml")
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := cli.Run(context.Background(), []string{"--file", manifestPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("plan exit %d: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "skills/review") || !strings.Contains(stdout.String(), "ADD") {
		t.Fatalf("plan output:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Agent: codex") || !strings.Contains(stdout.String(), "Agent: kimi") {
		t.Fatalf("missing agent plans: %s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".agents")); !os.IsNotExist(err) {
		t.Fatalf("plan created Codex target: %v", err)
	}
	if _, err := os.Stat(os.Getenv("KIMI_CODE_HOME")); !os.IsNotExist(err) {
		t.Fatalf("plan created target: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := cli.Run(context.Background(), []string{"--file", manifestPath, "apply"}, &stdout, &stderr); code != 0 {
		t.Fatalf("apply exit %d: %s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "review", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
	installed, err := os.ReadFile(filepath.Join(os.Getenv("KIMI_CODE_HOME"), "skills", "review", "SKILL.md"))
	if err != nil || string(installed) != contents {
		t.Fatalf("installed skill = %q, %v", installed, err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := cli.Run(context.Background(), []string{"--file", manifestPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("second plan exit %d: %s", code, stderr.String())
	}
	if strings.Count(stdout.String(), "No changes (1 unchanged).") != 2 {
		t.Fatalf("second plan output:\n%s", stdout.String())
	}
}

func TestClaudePlanApplyPlan(t *testing.T) {
	for _, mode := range []string{"default", "environment", "absolute override", "home override"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			target := filepath.Join(home, ".claude")
			override := ""
			if mode != "default" {
				target = filepath.Join(home, "relocated-claude")
				t.Setenv("CLAUDE_CONFIG_DIR", target)
			}
			if mode == "absolute override" || mode == "home override" {
				target = filepath.Join(home, "override")
				override = target
				if mode == "home override" {
					override = "~/override"
				}
			}
			dir := t.TempDir()
			skill := filepath.Join(dir, "skill")
			if err := os.Mkdir(skill, 0o755); err != nil {
				t.Fatal(err)
			}
			contents := "---\nname: review\ndescription: Review code\n---\nReview carefully.\n"
			if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte(contents), 0o644); err != nil {
				t.Fatal(err)
			}
			declaration := "version: 1\ntarget:\n  agent: claude\n"
			if override != "" {
				declaration += fmt.Sprintf("  path: %q\n", override)
			}
			declaration += "skills:\n  - name: review\n    path: skill\n"
			path := filepath.Join(dir, "skillet.yaml")
			if err := os.WriteFile(path, []byte(declaration), 0o600); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			run := func(apply bool) string {
				t.Helper()
				stdout.Reset()
				stderr.Reset()
				args := []string{"--file", path}
				if apply {
					args = append(args, "apply")
				}
				if code := cli.Run(context.Background(), args, &stdout, &stderr); code != 0 {
					t.Fatalf("exit %d: %s", code, stderr.String())
				}
				return stdout.String()
			}
			if output := run(false); !strings.Contains(output, "ADD") {
				t.Fatalf("plan: %s", output)
			}
			if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Fatalf("plan created target: %v", err)
			}
			run(true)
			installed, err := os.ReadFile(filepath.Join(target, "skills", "review", "SKILL.md"))
			if err != nil || string(installed) != contents {
				t.Fatalf("installed = %q, %v", installed, err)
			}
			if output := run(false); !strings.Contains(output, "No changes (1 unchanged)") {
				t.Fatalf("second plan: %s", output)
			}
			if output := run(true); strings.Contains(output, "Backup:") {
				t.Fatalf("second apply: %s", output)
			}
		})
	}
}

func TestPluginOnlyCLI(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "agent")
	plugin := filepath.Join(dir, "plugin")
	if err := os.Mkdir(plugin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugin, "kimi.plugin.json"), []byte(`{"name":"tools"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "skillet.yaml")
	declaration := fmt.Sprintf("version: 1\ntarget: {agent: kimi, path: %q}\nplugins:\n  - name: tools\n    path: plugin\n", home)
	if err := os.WriteFile(path, []byte(declaration), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"plan", "apply", "unchanged"} {
		var stdout, stderr bytes.Buffer
		args := []string{"--file", path}
		if command == "apply" {
			args = append(args, "apply")
		}
		if code := cli.Run(context.Background(), args, &stdout, &stderr); code != 0 {
			t.Fatalf("%s exit %d: %s", command, code, stderr.String())
		}
		if command == "unchanged" {
			if !strings.Contains(stdout.String(), "No changes (1 unchanged)") {
				t.Fatalf("plan = %s", stdout.String())
			}
		} else if !strings.Contains(stdout.String(), "plugins/tools") || strings.Contains(stdout.String(), "skills/tools") {
			t.Fatalf("output = %s", stdout.String())
		}
		if command == "apply" && !strings.Contains(stdout.String(), "Applied: plugins/tools") {
			t.Fatalf("report = %s", stdout.String())
		}
	}
}
