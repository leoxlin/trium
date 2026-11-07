package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/leoxlin/skillets/internal/bundle"
	"github.com/leoxlin/skillets/internal/reconcile"
)

func renderPlan(w io.Writer, manifestPath string, plan reconcile.TargetPlan) {
	fmt.Fprintf(w, "Agent: %s\n", plan.Agent)
	fmt.Fprintf(w, "Manifest: %s\n\n", manifestPath)
	counts := map[reconcile.Action]int{}
	visible := 0
	visible += renderChanges(w, plan.Changes, counts)
	if visible == 0 {
		fmt.Fprintf(w, "No changes (%d unchanged).\n", counts[reconcile.Unchanged])
	}
	fmt.Fprintf(w, "\nSummary: %d add, %d update, %d unchanged, %d conflict\n",
		counts[reconcile.Add], counts[reconcile.Update], counts[reconcile.Unchanged], counts[reconcile.Conflict])
}

func renderChanges(w io.Writer, changes []reconcile.Change, counts map[reconcile.Action]int) int {
	visible := 0
	for _, change := range changes {
		counts[change.Action]++
		if change.Action == reconcile.Unchanged {
			continue
		}
		visible++
		action := strings.ToUpper(string(change.Action))
		if change.Action == reconcile.Update {
			fmt.Fprintf(w, "%s/%-24s %-8s %s -> %s  %s\n", change.Kind, change.Name, action,
				shortDigest(change.Before), shortDigest(change.After), change.Reason)
		} else {
			fmt.Fprintf(w, "%s/%-24s %-8s %s  %s\n", change.Kind, change.Name, action,
				shortDigest(change.After), change.Reason)
		}
	}
	return visible
}

func renderReport(w io.Writer, report reconcile.TargetResult) {
	for _, applied := range report.Applied {
		kind := applied.Kind
		if kind == "" {
			kind = "skills"
		}
		fmt.Fprintf(w, "Applied: %s/%s (%s)\n", kind, applied.Name, applied.Action)
	}
	for _, backup := range report.Backups {
		fmt.Fprintf(w, "Backup: %s\n", backup)
	}
	for _, warning := range report.Warnings {
		fmt.Fprintf(w, "Warning: %s\n", warning)
	}
}

func shortDigest(digest bundle.Digest) string {
	return digest.String()[:12]
}
