package cli_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leoxlin/trium/internal/cli"
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
	manifestPath := filepath.Join(dir, "trium.yaml")
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := cli.Run(context.Background(), []string{manifestPath}, &stdout, &stderr); code != 0 {
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
	if code := cli.Run(context.Background(), []string{"apply", manifestPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("apply exit %d: %s", code, stderr.String())
	}
	installed, err := os.ReadFile(filepath.Join(os.Getenv("KIMI_CODE_HOME"), "skills", "review", "SKILL.md"))
	if err != nil || string(installed) != contents {
		t.Fatalf("installed skill = %q, %v", installed, err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := cli.Run(context.Background(), []string{manifestPath}, &stdout, &stderr); code != 0 {
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
	path := filepath.Join(dir, "trium.yaml")
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := cli.Run(context.Background(), []string{"apply", path}, &stdout, &stderr); code != 0 {
		t.Fatalf("apply exit %d: %s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".agents", "skills", "review", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

func TestMultipleTargetsPlanApplyPlan(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
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
	manifestPath := filepath.Join(dir, "trium.yaml")
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := cli.Run(context.Background(), []string{manifestPath}, &stdout, &stderr); code != 0 {
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
	if code := cli.Run(context.Background(), []string{"apply", manifestPath}, &stdout, &stderr); code != 0 {
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
	if code := cli.Run(context.Background(), []string{manifestPath}, &stdout, &stderr); code != 0 {
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
			path := filepath.Join(dir, "trium.yaml")
			if err := os.WriteFile(path, []byte(declaration), 0o600); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			run := func(apply bool) string {
				t.Helper()
				stdout.Reset()
				stderr.Reset()
				args := []string{path}
				if apply {
					args = append([]string{"apply"}, args...)
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
	t.Setenv("HOME", filepath.Join(dir, "user"))
	home := filepath.Join(dir, "agent")
	plugin := filepath.Join(dir, "plugin")
	if err := os.Mkdir(plugin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plugin, "kimi.plugin.json"), []byte(`{"name":"tools"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "trium.yaml")
	declaration := fmt.Sprintf("version: 1\ntarget: {agent: kimi, path: %q}\nplugins:\n  - name: tools\n    path: plugin\n", home)
	if err := os.WriteFile(path, []byte(declaration), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"plan", "apply", "unchanged"} {
		var stdout, stderr bytes.Buffer
		args := []string{path}
		if command == "apply" {
			args = append([]string{"apply"}, args...)
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

func TestMCPCLI(t *testing.T) {
	for _, scenario := range []string{"defaults", "environment", "explicit-default-claude", "target"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			for _, env := range []string{"CODEX_HOME", "CLAUDE_CONFIG_DIR", "KIMI_CODE_HOME"} {
				t.Setenv(env, "")
			}
			codex, claude, kimi := filepath.Join(home, ".codex"), filepath.Join(home, ".claude"), filepath.Join(home, ".kimi-code")
			claudeConfig := filepath.Join(home, ".claude.json")
			if scenario == "environment" {
				codex, claude, kimi = filepath.Join(home, "codex"), filepath.Join(home, "claude"), filepath.Join(home, "kimi")
				t.Setenv("CODEX_HOME", codex)
				t.Setenv("CLAUDE_CONFIG_DIR", claude)
				t.Setenv("KIMI_CODE_HOME", kimi)
				claudeConfig = filepath.Join(claude, ".claude.json")
			}
			if scenario == "explicit-default-claude" {
				t.Setenv("CLAUDE_CONFIG_DIR", claude)
				claudeConfig = filepath.Join(claude, ".claude.json")
			}
			targets := "target: [{agent: codex}, {agent: claude}, {agent: kimi}]\n"
			if scenario == "target" {
				claude = filepath.Join(home, "override")
				targets = fmt.Sprintf("target: [{agent: claude, path: %q}]\n", claude)
				claudeConfig = filepath.Join(claude, ".claude.json")
			}
			dir := t.TempDir()
			path := filepath.Join(dir, "trium.yaml")
			if err := os.WriteFile(path, []byte("version: 1\n"+targets+"mcps: [{name: docs, url: https://example.com/mcp, bearer_token_env: MCP_TOKEN}]\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			if code := cli.Run(context.Background(), []string{path}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "mcps/docs") {
				t.Fatalf("preview: %d %s %s", code, &stdout, &stderr)
			}
			if _, err := os.Stat(claudeConfig); !os.IsNotExist(err) {
				t.Fatal("preview wrote config")
			}
			stdout.Reset()
			if code := cli.Run(context.Background(), []string{"apply", path}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "Applied: mcps/docs") {
				t.Fatalf("apply: %d %s %s", code, &stdout, &stderr)
			}
			paths := []string{claudeConfig}
			if scenario != "target" {
				paths = append(paths, filepath.Join(codex, "config.toml"), filepath.Join(kimi, "mcp.json"))
			}
			for _, installed := range paths {
				info, err := os.Stat(installed)
				if err != nil || info.Mode().Perm() != 0o600 {
					t.Fatalf("native file: %s %v %v", installed, info, err)
				}
			}
			stdout.Reset()
			if code := cli.Run(context.Background(), []string{"apply", path}, &stdout, &stderr); code != 0 || strings.Contains(stdout.String(), "Backup:") || !strings.Contains(stdout.String(), "No changes (1 unchanged).") {
				t.Fatalf("no-op: %d %s %s", code, &stdout, &stderr)
			}
		})
	}
}

func TestInstructionsCLI(t *testing.T) {
	for _, kind := range []string{"codex", "claude", "kimi"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
			t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
			t.Setenv("KIMI_CODE_HOME", filepath.Join(home, ".kimi-code"))
			dir := t.TempDir()
			manifest := "version: 1\ntarget: {agent: " + kind + "}\ninstructions: |\n  Be concise.\n"
			path := filepath.Join(dir, "trium.yaml")
			if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			if code := cli.Run(context.Background(), []string{"apply", path}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "instructions/instructions") {
				t.Fatalf("apply: %d %s %s", code, stdout.String(), stderr.String())
			}
			name := "AGENTS.md"
			if kind == "claude" {
				name = "CLAUDE.md"
			}
			agentHome := filepath.Join(home, "."+kind)
			if kind == "kimi" {
				agentHome = filepath.Join(home, ".kimi-code")
			}
			got, err := os.ReadFile(filepath.Join(agentHome, name))
			if err != nil || string(got) != "Be concise.\n" {
				t.Fatalf("instructions = %q, %v", got, err)
			}
		})
	}
}

func TestCommandArguments(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		code int
		out  string
		err  string
	}{
		{name: "root help", args: []string{"--help"}, out: "trium [manifest]"},
		{name: "apply help", args: []string{"apply", "--help"}, out: "trium apply [manifest]"},
		{name: "help command", args: []string{"help", "apply"}, out: "trium apply [manifest]"},
		{name: "extra root argument", args: []string{"one.yaml", "two.yaml"}, code: 2, err: "accepts at most 1 arg(s), received 2"},
		{name: "extra apply argument", args: []string{"apply", "one.yaml", "two.yaml"}, code: 2, err: "accepts at most 1 arg(s), received 2"},
		{name: "unknown flag", args: []string{"--unknown"}, code: 2, err: "unknown flag"},
		{name: "unknown apply flag", args: []string{"apply", "--unknown"}, code: 2, err: "unknown flag"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			var stdout, stderr bytes.Buffer
			if code := cli.Run(context.Background(), test.args, &stdout, &stderr); code != test.code {
				t.Fatalf("exit = %d, want %d; stderr: %s", code, test.code, stderr.String())
			}
			if test.out == "" && stdout.Len() != 0 || !strings.Contains(stdout.String(), test.out) {
				t.Fatalf("stdout = %q, want %q", stdout.String(), test.out)
			}
			if test.err == "" && stderr.Len() != 0 || !strings.Contains(stderr.String(), test.err) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), test.err)
			}
		})
	}
}

func TestManifestDefaultsAndExitCodes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("KIMI_CODE_HOME", filepath.Join(home, "kimi"))
	config := filepath.Join(home, ".config")
	if err := os.Mkdir(config, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config, "trium.yaml")
	for _, test := range []struct {
		name     string
		manifest string
		code     int
	}{
		{name: "missing manifest", code: 1},
		{name: "invalid manifest", manifest: "version: 999\n", code: 2},
		{name: "valid manifest", manifest: "version: 1\ntarget: {agent: kimi}\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.manifest != "" {
				if err := os.WriteFile(path, []byte(test.manifest), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			for _, args := range [][]string{nil, {"apply"}} {
				var stdout, stderr bytes.Buffer
				if code := cli.Run(context.Background(), args, &stdout, &stderr); code != test.code {
					t.Fatalf("args %v: exit = %d, want %d; stderr: %s", args, code, test.code, stderr.String())
				}
				if test.code == 0 && !strings.Contains(stdout.String(), path) {
					t.Fatalf("args %v: output does not identify default manifest: %s", args, stdout.String())
				}
			}
		})
	}
}
