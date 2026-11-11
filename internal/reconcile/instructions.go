package reconcile

import (
	"crypto/sha256"
)

func planInstructions(wanted string, path string) (writeBatch, error) {
	batch := writeBatch{kind: Instructions}
	data, target, err := readTarget(path)
	if err != nil {
		return batch, err
	}
	batch.reads = append(batch.reads, target)

	current := []byte{}
	if target.Exists {
		current = data
	}
	after := sha256.Sum256([]byte(wanted))
	before := sha256.Sum256(current)

	change := Change{Kind: Instructions, Name: "instructions", Action: Add, After: after, Reason: "not installed"}
	if target.Exists {
		change.Before = before
		change.Action, change.Reason = Update, "content differs"
		if before == after {
			change.Action, change.Reason = Unchanged, ""
		}
	}
	batch.changes = append(batch.changes, change)
	if change.Action == Add || change.Action == Update {
		batch.writes = append(batch.writes, replacement{Path: path, Data: []byte(wanted)})
	}
	return batch, nil
}
