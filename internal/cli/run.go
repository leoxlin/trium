package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/leoxlin/trium/internal/manifest"
	"github.com/leoxlin/trium/internal/reconcile"
)

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	exitCode := 0
	run := func(cmd *cobra.Command, args []string) {
		manifestPath := ""
		if len(args) == 1 {
			manifestPath = args[0]
		}
		exitCode = runManifest(cmd.Context(), manifestPath, cmd.Name() == "apply", stdout, stderr)
	}
	root := &cobra.Command{
		Use:           "trium [manifest]",
		Short:         "Preview changes from an agent manifest",
		Args:          cobra.MaximumNArgs(1),
		Run:           run,
		SilenceErrors: true,
		SilenceUsage:  true,
		CompletionOptions: cobra.CompletionOptions{
			DisableDefaultCmd: true,
		},
	}
	root.AddCommand(&cobra.Command{
		Use:   "apply [manifest]",
		Short: "Apply changes from an agent manifest",
		Args:  cobra.MaximumNArgs(1),
		Run:   run,
	})
	root.SetArgs(append([]string{}, args...))
	root.SetOut(stdout)
	root.SetErr(stderr)
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	return exitCode
}

func runManifest(ctx context.Context, manifestPath string, apply bool, stdout, stderr io.Writer) int {
	if manifestPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(stderr, "resolve home directory: %v\n", err)
			return 1
		}
		manifestPath = filepath.Join(home, ".config", "trium.yaml")
	}
	absolute, err := filepath.Abs(manifestPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	data, err := os.ReadFile(absolute)
	if err != nil {
		fmt.Fprintf(stderr, "read manifest: %v\n", err)
		return 1
	}
	spec, err := manifest.Parse(data)
	if err != nil {
		fmt.Fprintf(stderr, "parse manifest: %v\n", err)
		return 2
	}
	homes, err := resolveHomes()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	prepared, err := reconcile.Prepare(ctx, spec, filepath.Dir(absolute), homes)
	if err != nil {
		fmt.Fprintf(stderr, "prepare: %v\n", err)
		var input *reconcile.InputError
		if errors.As(err, &input) {
			return 2
		}
		return 1
	}
	if apply {
		results, err := prepared.Apply(ctx)
		conflicts := false
		for _, result := range results {
			if err == nil || result.Plan.Changes != nil {
				renderPlan(stdout, absolute, result.Plan)
			}
			renderReport(stdout, result)
			conflicts = conflicts || result.Plan.HasConflicts()
		}
		if err != nil {
			fmt.Fprintf(stderr, "apply: %v\n", err)
			return 1
		}
		if conflicts {
			fmt.Fprintln(stderr, "apply blocked by plan conflicts; no changes applied to conflicted target")
			return 2
		}
		return 0
	}
	plans, err := prepared.Plan(ctx)
	conflicts := false
	for _, plan := range plans {
		renderPlan(stdout, absolute, plan)
		fmt.Fprintln(stdout, "Plan only; no changes applied.")
		conflicts = conflicts || plan.HasConflicts()
	}
	if err != nil {
		fmt.Fprintf(stderr, "plan: %v\n", err)
		return 1
	}
	if conflicts {
		fmt.Fprintln(stderr, "plan contains conflicts")
		return 2
	}
	return 0
}

func resolveHomes() (reconcile.Homes, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return reconcile.Homes{}, err
	}
	home, err = filepath.Abs(home)
	if err != nil {
		return reconcile.Homes{}, err
	}
	homes := reconcile.Homes{User: home}
	for _, target := range []struct {
		env, fallback string
		output        *string
	}{
		{"CODEX_HOME", ".codex", &homes.Codex},
		{"CLAUDE_CONFIG_DIR", ".claude", &homes.Claude},
		{"KIMI_CODE_HOME", ".kimi-code", &homes.Kimi},
	} {
		value := os.Getenv(target.env)
		if value == "" {
			value = filepath.Join(home, target.fallback)
		}
		*target.output, err = filepath.Abs(value)
		if err != nil {
			return reconcile.Homes{}, err
		}
	}
	if os.Getenv("CLAUDE_CONFIG_DIR") != "" {
		homes.ClaudeConfig = filepath.Join(homes.Claude, ".claude.json")
	}
	return homes, nil
}
