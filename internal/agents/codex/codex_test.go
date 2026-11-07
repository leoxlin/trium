package codex_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/leoxlin/skillets/internal/agents/codex"
	"github.com/leoxlin/skillets/internal/engine"
	"github.com/leoxlin/skillets/internal/resource"
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
