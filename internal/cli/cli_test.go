package cli_test

import (
	"bytes"
	"context"
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
