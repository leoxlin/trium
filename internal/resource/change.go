package resource

import (
	"fmt"
	"sort"
)

type Action string

const (
	Add       Action = "add"
	Update    Action = "update"
	Unchanged Action = "unchanged"
	Conflict  Action = "conflict"
)

type TargetDigest struct {
	Path   string
	Exists bool
	Digest Digest
}

type Change[T Item] struct {
	Action  Action
	Desired T
	Current *T
	Reason  string
}

type ChangeSet[T Item] struct {
	Changes []Change[T]
	Targets []TargetDigest
}

func (c ChangeSet[T]) HasConflicts() bool {
	for _, change := range c.Changes {
		if change.Action == Conflict {
			return true
		}
	}
	return false
}

func (c ChangeSet[T]) HasChanges() bool {
	for _, change := range c.Changes {
		if change.Action == Add || change.Action == Update {
			return true
		}
	}
	return false
}

type ApplyReport struct {
	Applied  []AppliedItem
	Backups  []string
	Warnings []string
}

type AppliedItem struct {
	Key    string
	Action Action
}

func Diff[T Item](desired []T, current map[string]T) ([]Change[T], error) {
	changes := make([]Change[T], 0, len(desired))
	seen := make(map[string]struct{}, len(desired))
	for _, wanted := range desired {
		key := wanted.Key()
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("duplicate resource %q", key)
		}
		seen[key] = struct{}{}
		change := Change[T]{Action: Add, Desired: wanted, Reason: "not installed"}
		if existing, ok := current[key]; ok {
			change.Current = &existing
			if existing.Fingerprint() == wanted.Fingerprint() {
				change.Action = Unchanged
				change.Reason = ""
			} else {
				change.Action = Update
				change.Reason = "content differs"
			}
		}
		changes = append(changes, change)
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Desired.Key() < changes[j].Desired.Key() })
	return changes, nil
}

type DriftError struct {
	Path string
}

func (e *DriftError) Error() string {
	return fmt.Sprintf("target changed after planning: %s", e.Path)
}
