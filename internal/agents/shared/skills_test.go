package shared_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/leoxlin/skillets/internal/agents/shared"
	"github.com/leoxlin/skillets/internal/resource"
)

func TestStoreSkillLifecycle(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "skills")
	backupRoot := filepath.Join(t.TempDir(), "backups")
	store := &shared.SkillStore{Root: root, BackupRoot: backupRoot}
	wanted := storeSkill("review", "first")

	plan, err := store.Plan(ctx, []resource.Skill{wanted})
	if err != nil {
		t.Fatal(err)
	}
	assertStoreAction(t, plan, resource.Add)
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("plan created skill root: %v", err)
	}
	if _, err := os.Stat(backupRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("plan created backup root: %v", err)
	}

	report, err := store.Apply(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Applied) != 1 || len(report.Backups) != 1 {
		t.Fatalf("unexpected apply report: %#v", report)
	}
	data, err := os.ReadFile(filepath.Join(root, "review", "SKILL.md"))
	if err != nil || string(data) != "first" {
		t.Fatalf("installed SKILL.md = %q, %v", data, err)
	}

	plan, err = store.Plan(ctx, []resource.Skill{wanted})
	if err != nil {
		t.Fatal(err)
	}
	assertStoreAction(t, plan, resource.Unchanged)
	report, err = store.Apply(ctx, plan)
	if err != nil || len(report.Backups) != 0 {
		t.Fatalf("second apply was not a no-op: %#v, %v", report, err)
	}

	updated := storeSkill("review", "second")
	unrelated := filepath.Join(root, "unrelated", "keep.txt")
	if err := os.MkdirAll(filepath.Dir(unrelated), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unrelated, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err = store.Plan(ctx, []resource.Skill{updated})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Apply(ctx, plan); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(filepath.Join(root, "review", "SKILL.md"))
	if string(data) != "second" {
		t.Fatalf("updated SKILL.md = %q", data)
	}
	data, _ = os.ReadFile(unrelated)
	if string(data) != "keep" {
		t.Fatalf("unrelated resource changed: %q", data)
	}

	plan, err = store.Plan(ctx, nil)
	if err != nil || len(plan.Changes) != 0 {
		t.Fatalf("empty desired set changed resources: %#v, %v", plan, err)
	}
	if _, err := os.Stat(filepath.Join(root, "review")); err != nil {
		t.Fatalf("empty desired set deleted skill: %v", err)
	}
}

func TestStoreRejectsConflictsAndDrift(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "skills")
	store := &shared.SkillStore{Root: root, BackupRoot: filepath.Join(t.TempDir(), "backups")}
	wanted := storeSkill("review", "content")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "elsewhere"), filepath.Join(root, "review")); err != nil {
		t.Fatal(err)
	}
	plan, err := store.Plan(ctx, []resource.Skill{wanted})
	if err != nil || !plan.HasConflicts() {
		t.Fatalf("symlink plan = %#v, %v", plan, err)
	}
	if _, err := store.Apply(ctx, plan); err == nil {
		t.Fatal("apply accepted conflict")
	}
	if err := os.Remove(filepath.Join(root, "review")); err != nil {
		t.Fatal(err)
	}

	plan, err = store.Plan(ctx, []resource.Skill{wanted})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "review"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = store.Apply(ctx, plan)
	var drift *resource.DriftError
	if !errors.As(err, &drift) {
		t.Fatalf("apply after drift = %v", err)
	}
}

func storeSkill(name, body string) resource.Skill {
	return resource.NewSkill(name, "/source/"+name, []resource.File{{Path: "SKILL.md", Data: []byte(body)}})
}

func assertStoreAction(t *testing.T, plan resource.ChangeSet[resource.Skill], action resource.Action) {
	t.Helper()
	if len(plan.Changes) != 1 || plan.Changes[0].Action != action {
		t.Fatalf("plan = %#v, want %s", plan, action)
	}
}
