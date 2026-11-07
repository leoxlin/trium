package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	agent "github.com/leoxlin/skillets/internal/agents"
	"github.com/leoxlin/skillets/internal/agents/claude"
	"github.com/leoxlin/skillets/internal/agents/codex"
	"github.com/leoxlin/skillets/internal/agents/kimi"
	"github.com/leoxlin/skillets/internal/engine"
	"github.com/leoxlin/skillets/internal/manifest"
)

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("skillet", flag.ContinueOnError)
	flags.SetOutput(stderr)
	manifestPath := flags.String("file", "", "manifest path")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Usage: skillet [--file <path>] [apply]")
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	apply := false
	switch flags.NArg() {
	case 0:
	case 1:
		if flags.Arg(0) != "apply" {
			fmt.Fprintf(stderr, "unknown command %q\n", flags.Arg(0))
			return 2
		}
		apply = true
	default:
		fmt.Fprintln(stderr, "too many arguments")
		return 2
	}

	if *manifestPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(stderr, "resolve home directory: %v\n", err)
			return 1
		}
		*manifestPath = filepath.Join(home, ".config", "skillet.yaml")
	}
	loaded, err := manifest.Load(ctx, *manifestPath)
	if err != nil {
		fmt.Fprintf(stderr, "load manifest: %v\n", err)
		var validation *manifest.ValidationError
		if errors.As(err, &validation) {
			return 2
		}
		return 1
	}
	for _, target := range loaded.Targets {
		selected, err := discover(target)
		if err != nil {
			fmt.Fprintf(stderr, "discover %s: %v\n", target.Agent, err)
			return 1
		}
		if code := runTarget(ctx, selected, loaded, apply, stdout, stderr); code != 0 {
			return code
		}
	}
	return 0
}

func runTarget(ctx context.Context, selected agent.Agent, loaded manifest.Manifest, apply bool, stdout, stderr io.Writer) int {
	runner := engine.Engine{}
	if apply {
		plan, report, err := runner.Apply(ctx, selected, loaded.Desired)
		if plan.Skills.Changes != nil || plan.Plugins.Changes != nil {
			renderPlan(stdout, selected.Name(), loaded.Path, plan)
		}
		if len(report.Applied) != 0 || len(report.Backups) != 0 || len(report.Warnings) != 0 {
			renderReport(stdout, report)
		}
		if err != nil {
			fmt.Fprintf(stderr, "apply: %v\n", err)
			return 1
		}
		if plan.HasConflicts() {
			fmt.Fprintln(stderr, "apply blocked by plan conflicts; no changes applied")
			return 2
		}
		return 0
	}

	plan, err := runner.Plan(ctx, selected, loaded.Desired)
	if err != nil {
		fmt.Fprintf(stderr, "plan: %v\n", err)
		return 1
	}
	renderPlan(stdout, selected.Name(), loaded.Path, plan)
	fmt.Fprintln(stdout, "Plan only; no changes applied.")
	if plan.HasConflicts() {
		fmt.Fprintln(stderr, "plan contains conflicts")
		return 2
	}
	return 0
}

func discover(target manifest.Target) (agent.Agent, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	targetHome := target.Path
	if strings.HasPrefix(targetHome, "~/") {
		targetHome = filepath.Join(home, strings.TrimPrefix(targetHome, "~/"))
	}
	if targetHome == "" {
		switch target.Agent {
		case "codex":
			targetHome = os.Getenv("CODEX_HOME")
			if targetHome == "" {
				targetHome = filepath.Join(home, ".codex")
			}
		case "claude":
			targetHome = os.Getenv("CLAUDE_CONFIG_DIR")
			if targetHome == "" {
				targetHome = filepath.Join(home, ".claude")
			}
		case "kimi":
			targetHome = os.Getenv("KIMI_CODE_HOME")
			if targetHome == "" {
				targetHome = filepath.Join(home, ".kimi-code")
			}
		}
	}
	targetHome, err = filepath.Abs(targetHome)
	if err != nil {
		return nil, err
	}
	switch target.Agent {
	case "codex":
		return codex.New(codex.Target{
			Home:       targetHome,
			SkillsRoot: filepath.Join(home, ".agents", "skills"),
		}), nil
	case "claude":
		return claude.New(claude.Target{Home: targetHome}), nil
	case "kimi":
		return kimi.New(kimi.Target{Home: targetHome}), nil
	default:
		return nil, fmt.Errorf("unsupported agent %q", target.Agent)
	}
}
