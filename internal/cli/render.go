package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/leoxlin/skillets/internal/engine"
	"github.com/leoxlin/skillets/internal/resource"
)

func renderPlan(w io.Writer, agentName, manifestPath string, plan engine.Plan) {
	fmt.Fprintf(w, "Agent: %s\n", agentName)
	fmt.Fprintf(w, "Manifest: %s\n\n", manifestPath)
	counts := map[resource.Action]int{}
	visible := 0
	visible += renderChanges(w, "skills", plan.Skills.Changes, counts)
	visible += renderChanges(w, "plugins", plan.Plugins.Changes, counts)
	if visible == 0 {
		fmt.Fprintf(w, "No changes (%d unchanged).\n", counts[resource.Unchanged])
	}
	fmt.Fprintf(w, "\nSummary: %d add, %d update, %d unchanged, %d conflict\n",
		counts[resource.Add], counts[resource.Update], counts[resource.Unchanged], counts[resource.Conflict])
}

func renderChanges[T resource.Item](w io.Writer, kind string, changes []resource.Change[T], counts map[resource.Action]int) int {
	visible := 0
	for _, change := range changes {
		counts[change.Action]++
		if change.Action == resource.Unchanged {
			continue
		}
		visible++
		action := strings.ToUpper(string(change.Action))
		if change.Action == resource.Update && change.Current != nil {
			fmt.Fprintf(w, "%s/%-24s %-8s %s -> %s  %s\n", kind, change.Desired.Key(), action,
				shortDigest((*change.Current).Fingerprint()), shortDigest(change.Desired.Fingerprint()), change.Reason)
		} else {
			fmt.Fprintf(w, "%s/%-24s %-8s %s  %s\n", kind, change.Desired.Key(), action,
				shortDigest(change.Desired.Fingerprint()), change.Reason)
		}
	}
	return visible
}

func renderReport(w io.Writer, report resource.ApplyReport) {
	for _, applied := range report.Applied {
		kind := applied.Kind
		if kind == "" {
			kind = "skills"
		}
		fmt.Fprintf(w, "Applied: %s/%s (%s)\n", kind, applied.Key, applied.Action)
	}
	for _, backup := range report.Backups {
		fmt.Fprintf(w, "Backup: %s\n", backup)
	}
	for _, warning := range report.Warnings {
		fmt.Fprintf(w, "Warning: %s\n", warning)
	}
}

func shortDigest(digest resource.Digest) string {
	return digest.String()[:12]
}
