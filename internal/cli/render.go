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
	for _, change := range plan.Skills.Changes {
		counts[change.Action]++
		if change.Action == resource.Unchanged {
			continue
		}
		visible++
		action := strings.ToUpper(string(change.Action))
		if change.Action == resource.Update && change.Current != nil {
			fmt.Fprintf(w, "skills/%-24s %-8s %s -> %s  %s\n", change.Desired.Name, action,
				shortDigest(change.Current.Fingerprint()), shortDigest(change.Desired.Fingerprint()), change.Reason)
		} else {
			fmt.Fprintf(w, "skills/%-24s %-8s %s  %s\n", change.Desired.Name, action,
				shortDigest(change.Desired.Fingerprint()), change.Reason)
		}
	}
	if visible == 0 {
		fmt.Fprintf(w, "No changes (%d unchanged).\n", counts[resource.Unchanged])
	}
	fmt.Fprintf(w, "\nSummary: %d add, %d update, %d unchanged, %d conflict\n",
		counts[resource.Add], counts[resource.Update], counts[resource.Unchanged], counts[resource.Conflict])
}

func renderReport(w io.Writer, report resource.ApplyReport) {
	for _, applied := range report.Applied {
		fmt.Fprintf(w, "Applied: skills/%s (%s)\n", applied.Key, applied.Action)
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
