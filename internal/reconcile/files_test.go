package reconcile

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leoxlin/trium/internal/bundle"
	"github.com/leoxlin/trium/internal/manifest"
)

func testWrite(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}
func testPrepared(t *testing.T, kind string) *Prepared {
	t.Helper()
	root := t.TempDir()
	testWrite(t, filepath.Join(root, "source/skill/SKILL.md"), "---\nname: review\ndescription: Review\n---\ncontent")
	native := "." + kind + "-plugin/plugin.json"
	if kind == "kimi" {
		native = "kimi.plugin.json"
	}
	testWrite(t, filepath.Join(root, "source/plugin", native), `{"name":"review","version":"1.0.0"}`)
	spec, err := manifest.Parse([]byte("version: 1\ntarget: {agent: " + kind + "}\nskills: [{name: review, path: skill}]\nplugins: [{name: review, path: plugin}]"))
	if err != nil {
		t.Fatal(err)
	}
	homes := Homes{User: filepath.Join(root, "user"), Codex: filepath.Join(root, "codex"), Claude: filepath.Join(root, "claude"), Kimi: filepath.Join(root, "kimi")}
	prepared, err := Prepare(context.Background(), spec, filepath.Join(root, "source"), homes)
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

func TestBatchDriftIncludesReadOnlyPolicy(t *testing.T) {
	p := testPrepared(t, "codex")
	target := p.targets[0]
	planned, err := target.native.plan(context.Background(), p.wanted)
	if err != nil {
		t.Fatal(err)
	}
	// The skill destination is unchanged; only policy changes after its observation.
	testWrite(t, filepath.Join(target.home, "config.toml"), "model = 'changed'\n")
	report, err := applyBatch(context.Background(), filepath.Join(target.home, ".trium/backups"), planned.batches[0], func(context.Context) error { return nil })
	var drift *DriftError
	if !errors.As(err, &drift) || drift.Path != filepath.Join(target.home, "config.toml") || len(report.Backups) != 0 {
		t.Fatalf("drift = %#v %v", report, err)
	}
	root := target.native.(*codexNative).skillsRoot
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("drift caused writes: %v", err)
	}
}

func TestPluginRegistryDriftPreventsBundleWrites(t *testing.T) {
	for _, kind := range []string{"codex", "claude", "kimi"} {
		t.Run(kind, func(t *testing.T) {
			p := testPrepared(t, kind)
			target := p.targets[0]
			planned, err := target.native.plan(context.Background(), p.wanted)
			if err != nil {
				t.Fatal(err)
			}
			batch := planned.batches[1]
			registry := batch.reads[0].Path
			testWrite(t, registry, "{}")
			_, err = applyBatch(context.Background(), filepath.Join(target.home, ".trium/backups"), batch, func(context.Context) error { return nil })
			var drift *DriftError
			if !errors.As(err, &drift) {
				t.Fatalf("drift = %v", err)
			}
			if _, err := os.Stat(filepath.Join(target.home, "plugins/managed/review")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("bundle written: %v", err)
			}
		})
	}
}

func TestBatchRollbackRetainsBackups(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(map[bool]string{false: "verification", true: "cancellation"}[cancel], func(t *testing.T) {
			root := t.TempDir()
			directory := filepath.Join(root, "bundle")
			registry := filepath.Join(root, "registry.json")
			added := filepath.Join(root, "added.json")
			testWrite(t, filepath.Join(directory, "file"), "old bundle")
			testWrite(t, registry, "old registry")
			_, dirRead, _, err := scanBundle(context.Background(), directory)
			if err != nil {
				t.Fatal(err)
			}
			_, registryRead, err := readTarget(registry)
			if err != nil {
				t.Fatal(err)
			}
			_, addedRead, err := readTarget(added)
			if err != nil {
				t.Fatal(err)
			}
			batch := writeBatch{reads: []observation{dirRead, registryRead, addedRead}, writes: []replacement{
				{Path: directory, Directory: true, Files: []bundle.File{{Path: "file", Data: []byte("new bundle")}}},
				{Path: registry, Data: []byte("new registry")}, {Path: added, Data: []byte("new")},
			}}
			ctx, stop := context.WithCancel(context.Background())
			defer stop()
			sentinel := errors.New("verification failed")
			report, err := applyBatch(ctx, filepath.Join(root, "backups"), batch, func(ctx context.Context) error {
				data, _ := os.ReadFile(registry)
				if string(data) != "new registry" {
					t.Fatal("verification ran before installation")
				}
				if cancel {
					stop()
					return ctx.Err()
				}
				return sentinel
			})
			if err == nil || len(report.Backups) != 1 || len(report.Warnings) != 0 {
				t.Fatalf("report = %#v %v", report, err)
			}
			if cancel && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation lost: %v", err)
			}
			for path, want := range map[string]string{filepath.Join(directory, "file"): "old bundle", registry: "old registry", filepath.Join(report.Backups[0], "0/file"): "old bundle", filepath.Join(report.Backups[0], "1"): "old registry"} {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != want {
					t.Fatalf("%s = %q %v", path, data, err)
				}
			}
			if _, err := os.Stat(added); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("addition survived rollback: %v", err)
			}
			stages, err := filepath.Glob(filepath.Join(root, ".trium-stage-*"))
			if err != nil || len(stages) != 0 {
				t.Fatalf("stages leaked: %v %v", stages, err)
			}
		})
	}
}

type failingNative struct {
	native
	calls int
}

func (a *failingNative) plan(ctx context.Context, wanted desired) (targetPlan, error) {
	a.calls++
	if a.calls == 3 {
		return targetPlan{}, errors.New("injected plugin verification failure")
	}
	return a.native.plan(ctx, wanted)
}
func TestPartialReportAfterPluginFailure(t *testing.T) {
	p := testPrepared(t, "kimi")
	p.targets[0].native = &failingNative{native: p.targets[0].native}
	results, err := p.Apply(context.Background())
	if err == nil || len(results) != 1 || len(results[0].Applied) != 1 || results[0].Applied[0].Kind != Skills {
		t.Fatalf("partial = %#v %v", results, err)
	}
	home := p.targets[0].home
	if _, err := os.Stat(filepath.Join(home, "skills/review/SKILL.md")); err != nil {
		t.Fatal("earlier class was rolled back")
	}
	if _, err := os.Stat(filepath.Join(home, "plugins/managed/review")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed class retained: %v", err)
	}
}

func TestApplyLockIsSharedAcrossTargetHomes(t *testing.T) {
	p := testPrepared(t, "codex")
	other := *p
	original := p.targets[0].native.(*codexNative)
	other.targets = []selectedTarget{{agent: "codex", home: filepath.Join(t.TempDir(), "other"), native: &codexNative{home: filepath.Join(t.TempDir(), "other"), skillsRoot: original.skillsRoot}}}
	unlock, err := acquireLock(context.Background(), p.lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Apply(context.Background()); err == nil {
		t.Fatal("second target bypassed user lock")
	}
	if _, err := p.Plan(context.Background()); err != nil {
		t.Fatalf("preview requires lock: %v", err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Apply(context.Background()); err != nil {
		t.Fatalf("lock not released: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Apply(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled apply = %v", err)
	}
}

func TestLockReleasedOnProcessExit(t *testing.T) {
	if path := os.Getenv("TRIUM_TEST_LOCK"); path != "" {
		if _, err := acquireLock(context.Background(), path); err != nil {
			t.Fatal(err)
		}
		os.Exit(0)
	}
	path := filepath.Join(t.TempDir(), "apply.lock")
	command := exec.Command(os.Args[0], "-test.run=^TestLockReleasedOnProcessExit$")
	command.Env = append(os.Environ(), "TRIUM_TEST_LOCK="+path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("child: %s %v", output, err)
	}
	unlock, err := acquireLock(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
}

func TestBatchRejectsOverlappingOrUnobservedWrites(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "target")
	for _, writes := range [][]replacement{
		{{Path: path, Data: []byte("new")}},
		{{Path: path, Directory: true}, {Path: filepath.Join(path, "nested")}},
	} {
		batch := writeBatch{writes: writes}
		if len(writes) == 2 {
			batch.reads = []observation{{Path: path, Directory: true}, {Path: filepath.Join(path, "nested")}}
		}
		_, err := applyBatch(context.Background(), filepath.Join(root, "backups"), batch, func(context.Context) error { return nil })
		if err == nil {
			t.Fatal("invalid writes accepted")
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("invalid batch mutated target")
		}
	}
}

func TestPlanOrderingAndNoPrune(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"z-last", "a-first"} {
		testWrite(t, filepath.Join(root, name, "SKILL.md"), "---\nname: "+name+"\ndescription: Test\n---\n")
	}
	spec, err := manifest.Parse([]byte("version: 1\ntarget: {agent: kimi}\nskills: [{name: z-last, path: z-last}, {name: a-first, path: a-first}]"))
	if err != nil {
		t.Fatal(err)
	}
	homes := Homes{User: root, Codex: filepath.Join(root, "codex"), Claude: filepath.Join(root, "claude"), Kimi: filepath.Join(root, "kimi")}
	prepared, err := Prepare(context.Background(), spec, root, homes)
	if err != nil {
		t.Fatal(err)
	}
	plans, err := prepared.Plan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if plans[0].Changes[0].Name != "a-first" || plans[0].Changes[1].Name != "z-last" {
		t.Fatalf("order = %#v", plans)
	}
	if _, err := prepared.Apply(context.Background()); err != nil {
		t.Fatal(err)
	}
	spec.Skills = nil
	prepared, err = Prepare(context.Background(), spec, root, homes)
	if err != nil {
		t.Fatal(err)
	}
	results, err := prepared.Apply(context.Background())
	if err != nil || len(results[0].Applied) != 0 {
		t.Fatalf("empty desired = %#v %v", results, err)
	}
	data, err := os.ReadFile(filepath.Join(homes.Kimi, "skills/a-first/SKILL.md"))
	if err != nil || !strings.Contains(string(data), "a-first") {
		t.Fatal("undeclared resource pruned")
	}
}

func TestStagingFailureDoesNotReplaceEarlierTargets(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first")
	second := filepath.Join(root, "second")
	testWrite(t, first, "original")
	_, before, err := readTarget(first)
	if err != nil {
		t.Fatal(err)
	}
	batch := writeBatch{reads: []observation{before, {Path: second, Directory: true}}, writes: []replacement{
		{Path: first, Data: []byte("replacement")},
		{Path: second, Directory: true, Files: []bundle.File{{Path: "../escape", Data: []byte("unsafe")}}},
	}}
	verified := false
	_, err = applyBatch(context.Background(), filepath.Join(root, "backups"), batch, func(context.Context) error { verified = true; return nil })
	if err == nil || verified {
		t.Fatalf("staging failure = %v, verified=%v", err, verified)
	}
	data, err := os.ReadFile(first)
	if err != nil || string(data) != "original" {
		t.Fatalf("earlier target replaced: %q %v", data, err)
	}
	stages, _ := filepath.Glob(filepath.Join(root, ".trium-stage-*"))
	if len(stages) != 0 {
		t.Fatalf("staging files retained: %v", stages)
	}
}

func TestMCPBatchRollbackAndDrift(t *testing.T) {
	for _, agent := range []string{"codex", "claude", "kimi"} {
		t.Run(agent, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "config")
			original := "{\"unrelated\":true}\n"
			if agent == "codex" {
				original = "unrelated = true\n"
			}
			testWrite(t, path, original)
			servers := []manifest.MCP{{Name: "server", Command: "server"}}
			batch, err := planMCPs(context.Background(), servers, agent, path)
			if err != nil {
				t.Fatal(err)
			}
			sentinel := errors.New("verification failed")
			report, err := applyBatch(context.Background(), filepath.Join(root, "backups"), batch, func(context.Context) error { return sentinel })
			if !errors.Is(err, sentinel) || len(report.Backups) != 1 || len(report.Warnings) != 0 {
				t.Fatalf("rollback: %#v %v", report, err)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != original {
				t.Fatalf("restoration: %s %v", data, err)
			}
			testWrite(t, path, original+" ")
			_, err = applyBatch(context.Background(), filepath.Join(root, "backups"), batch, func(context.Context) error { return nil })
			var drift *DriftError
			if !errors.As(err, &drift) {
				t.Fatalf("drift: %v", err)
			}
		})
	}
}
