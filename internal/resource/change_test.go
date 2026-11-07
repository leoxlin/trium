package resource_test

import (
	"testing"

	"github.com/leoxlin/skillets/internal/resource"
)

func TestDiffSortsAndNeverRemoves(t *testing.T) {
	first := resource.NewSkill("z-last", "one", []resource.File{{Path: "SKILL.md", Data: []byte("same")}})
	second := resource.NewSkill("a-first", "two", []resource.File{{Path: "SKILL.md", Data: []byte("new")}})
	current := resource.NewSkill("a-first", "old", []resource.File{{Path: "SKILL.md", Data: []byte("old")}})
	extra := resource.NewSkill("unmanaged", "old", nil)
	changes, err := resource.Diff([]resource.Skill{first, second}, map[string]resource.Skill{
		"a-first": current, "unmanaged": extra,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 || changes[0].Desired.Name != "a-first" || changes[1].Desired.Name != "z-last" {
		t.Fatalf("changes = %#v", changes)
	}
	if changes[0].Action != resource.Update || changes[1].Action != resource.Add {
		t.Fatalf("actions = %s, %s", changes[0].Action, changes[1].Action)
	}
}
