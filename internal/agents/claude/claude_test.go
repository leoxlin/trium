package claude_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/leoxlin/skillets/internal/agents/claude"
	"github.com/leoxlin/skillets/internal/engine"
	"github.com/leoxlin/skillets/internal/resource"
)

func TestProtectedSkills(t *testing.T) {
	for _, kind := range []string{"synced", "trash", "symlinked catalog", "symlinked skill"} {
		t.Run(kind, func(t *testing.T) {
			home := t.TempDir()
			name := "review"
			switch kind {
			case "synced":
				name = "synced"
			case "trash":
				home = filepath.Join(home, ".trash", "old-home")
			case "symlinked catalog", "symlinked skill":
				link := filepath.Join(home, "skills")
				if kind == "symlinked skill" {
					if err := os.Mkdir(link, 0o755); err != nil {
						t.Fatal(err)
					}
					link = filepath.Join(link, name)
				}
				if err := os.Symlink(t.TempDir(), link); err != nil {
					t.Fatal(err)
				}
			}
			a := claude.New(claude.Target{Home: home})
			wanted := resource.NewSkill(name, "source", []resource.File{{Path: "SKILL.md", Data: []byte("content")}})
			desired := resource.Desired{Skills: []resource.Skill{wanted}}
			plan, err := (engine.Engine{}).Plan(context.Background(), a, desired)
			if err != nil || !plan.HasConflicts() {
				t.Fatalf("plan = %#v, %v", plan, err)
			}
			plan, report, err := (engine.Engine{}).Apply(context.Background(), a, desired)
			if err != nil || !plan.HasConflicts() || len(report.Applied) != 0 || len(report.Backups) != 0 {
				t.Fatalf("apply = %#v, %#v, %v", plan, report, err)
			}
		})
	}
}

func TestLockContention(t *testing.T) {
	a := claude.New(claude.Target{Home: t.TempDir()})
	unlock, err := a.Lock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unlock() }()
	if _, _, err := (engine.Engine{}).Apply(context.Background(), a, resource.Desired{}); err == nil {
		t.Fatal("apply acquired an already-held lock")
	}
}
