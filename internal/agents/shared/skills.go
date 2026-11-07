package shared

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/leoxlin/skillets/internal/resource"
)

type SkillConflictCheck func(context.Context, resource.Skill, string) (string, error)

type SkillStore struct {
	Root       string
	BackupRoot string
	Check      SkillConflictCheck
}

func (s *SkillStore) Plan(ctx context.Context, desired []resource.Skill) (resource.ChangeSet[resource.Skill], error) {
	if len(desired) == 0 {
		return resource.ChangeSet[resource.Skill]{}, nil
	}
	current := make(map[string]resource.Skill, len(desired))
	conflicts := make(map[string]string, len(desired))
	targets := make([]resource.TargetDigest, 0, len(desired))
	for _, wanted := range desired {
		if err := ctx.Err(); err != nil {
			return resource.ChangeSet[resource.Skill]{}, err
		}
		path := filepath.Join(s.Root, wanted.Name)
		existing, target, reason, err := scanSkill(path, wanted.Name)
		if err != nil {
			return resource.ChangeSet[resource.Skill]{}, err
		}
		targets = append(targets, target)
		if existing != nil {
			current[wanted.Name] = *existing
		}
		if reason == "" && s.Check != nil {
			reason, err = s.Check(ctx, wanted, path)
			if err != nil {
				return resource.ChangeSet[resource.Skill]{}, err
			}
		}
		if reason != "" {
			conflicts[wanted.Name] = reason
		}
	}
	changes, err := resource.Diff(desired, current)
	if err != nil {
		return resource.ChangeSet[resource.Skill]{}, err
	}
	for i := range changes {
		if reason := conflicts[changes[i].Desired.Name]; reason != "" {
			changes[i].Action = resource.Conflict
			changes[i].Reason = reason
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].Path < targets[j].Path })
	return resource.ChangeSet[resource.Skill]{Changes: changes, Targets: targets}, nil
}

func (s *SkillStore) Apply(ctx context.Context, changes resource.ChangeSet[resource.Skill]) (resource.ApplyReport, error) {
	var report resource.ApplyReport
	if changes.HasConflicts() {
		return report, errors.New("cannot apply a change set with conflicts")
	}
	if !changes.HasChanges() {
		return report, nil
	}
	for _, target := range changes.Targets {
		current, err := digestTarget(target, true)
		if err != nil || current != target {
			return report, &resource.DriftError{Path: target.Path}
		}
	}
	var writes []Write
	var desired []resource.Skill
	for _, change := range changes.Changes {
		if change.Action != resource.Add && change.Action != resource.Update {
			continue
		}
		desired = append(desired, change.Desired)
		writes = append(writes, Write{Path: filepath.Join(s.Root, change.Desired.Name), Files: change.Desired.Files, Directory: true})
	}
	report, err := applyWrites(ctx, s.BackupRoot, writes, func() error {
		verification, err := s.Plan(ctx, desired)
		if err != nil {
			return err
		}
		for _, change := range verification.Changes {
			if change.Action != resource.Unchanged {
				return fmt.Errorf("verify installed skill %s: %s", change.Desired.Name, change.Reason)
			}
		}
		return nil
	})
	if err != nil {
		return report, err
	}
	for _, change := range changes.Changes {
		if change.Action == resource.Add || change.Action == resource.Update {
			report.Applied = append(report.Applied, resource.AppliedItem{Kind: "skills", Key: change.Desired.Key(), Action: change.Action})
		}
	}
	return report, nil
}

func scanSkill(path, name string) (*resource.Skill, resource.TargetDigest, string, error) {
	files, target, reason, err := scanBundle(path)
	if err != nil || reason != "" || !target.Exists {
		return nil, target, reason, err
	}
	skill := resource.NewSkill(name, path, files)
	return &skill, target, "", nil
}
