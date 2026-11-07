package reconcile

import (
	"fmt"

	"github.com/leoxlin/skillets/internal/bundle"
)

type Kind string

const (
	Skills  Kind = "skills"
	Plugins Kind = "plugins"
)

type Action string

const (
	Add       Action = "add"
	Update    Action = "update"
	Unchanged Action = "unchanged"
	Conflict  Action = "conflict"
)

type Change struct {
	Kind   Kind
	Name   string
	Action Action
	Before bundle.Digest
	After  bundle.Digest
	Reason string
}

type TargetPlan struct {
	Agent   string
	Home    string
	Changes []Change
}

func (p TargetPlan) HasConflicts() bool {
	for _, c := range p.Changes {
		if c.Action == Conflict {
			return true
		}
	}
	return false
}

type TargetResult struct {
	Plan     TargetPlan
	Applied  []Change
	Backups  []string
	Warnings []string
}

// InputError identifies acquired content that does not satisfy its declaration.
type InputError struct {
	Field string
	Err   error
}

func (e *InputError) Error() string { return fmt.Sprintf("%s: %v", e.Field, e.Err) }
func (e *InputError) Unwrap() error { return e.Err }

type DriftError struct{ Path string }

func (e *DriftError) Error() string { return "target changed after planning: " + e.Path }

type namedBundle struct {
	name, field string
	content     bundle.Snapshot
}
type desired struct{ skills, plugins []namedBundle }

type observation struct {
	Path      string
	Exists    bool
	Directory bool
	Digest    bundle.Digest
}

type replacement struct {
	Path      string
	Files     []bundle.File
	Data      []byte
	Directory bool
}

type writeBatch struct {
	kind    Kind
	changes []Change
	reads   []observation
	writes  []replacement
}

type targetPlan struct{ batches []writeBatch }

func (p targetPlan) changes() []Change {
	var changes []Change
	for _, b := range p.batches {
		changes = append(changes, b.changes...)
	}
	return changes
}

type batchReport struct{ Backups, Warnings []string }

func compare(kind Kind, wanted namedBundle, current bundle.Digest, exists bool) Change {
	c := Change{Kind: kind, Name: wanted.name, Action: Add, After: wanted.content.Digest(), Reason: "not installed"}
	if exists {
		c.Before = current
		c.Action, c.Reason = Update, "content differs"
		if c.Before == c.After {
			c.Action, c.Reason = Unchanged, ""
		}
	}
	return c
}
