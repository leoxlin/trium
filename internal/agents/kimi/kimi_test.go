package kimi_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/leoxlin/skillets/internal/agents/kimi"
	"github.com/leoxlin/skillets/internal/engine"
	"github.com/leoxlin/skillets/internal/resource"
)

func TestAgentContract(t *testing.T) {
	ctx := context.Background()
	home := filepath.Join(t.TempDir(), "kimi-home")
	a := kimi.New(kimi.Target{Home: home})
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
	data, err := os.ReadFile(filepath.Join(home, "skills", "review", "SKILL.md"))
	if err != nil || string(data) != "content" {
		t.Fatalf("installed SKILL.md = %q, %v", data, err)
	}
}

func TestLockContention(t *testing.T) {
	a := kimi.New(kimi.Target{Home: t.TempDir()})
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
