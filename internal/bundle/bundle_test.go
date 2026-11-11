package bundle_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leoxlin/trium/internal/bundle"
)

func write(t *testing.T, path, data string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), mode); err != nil {
		t.Fatal(err)
	}
}
func acquire(t *testing.T, base, source, path, ref string) bundle.Snapshot {
	t.Helper()
	reference, err := bundle.ParseReference(source, path, ref)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := bundle.Acquire(context.Background(), base, reference)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestSnapshotsAreCanonicalAndIndependent(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a/z"), "last", 0o644)
	write(t, filepath.Join(root, "a/a"), "first", 0o755)
	first := acquire(t, root, "local", "a", "")
	write(t, filepath.Join(root, "b/a"), "first", 0o711)
	write(t, filepath.Join(root, "b/z"), "last", 0o600)
	second := acquire(t, root, "", "b", "")
	if first.Digest() != second.Digest() {
		t.Fatal("location or unrelated modes affected digest")
	}
	files := first.Files()
	if files[0].Path != "a" || !files[0].Executable {
		t.Fatalf("files = %#v", files)
	}
	files[0].Path = "changed"
	files[0].Data[0] = 'X'
	if first.Files()[0].Path != "a" || string(first.Files()[0].Data) != "first" {
		t.Fatal("snapshot was mutable")
	}
	if err := os.Chmod(filepath.Join(root, "b/a"), 0o644); err != nil {
		t.Fatal(err)
	}
	third := acquire(t, root, "local", "b", "")
	if third.Digest() == first.Digest() {
		t.Fatal("executable bit ignored")
	}
	write(t, filepath.Join(root, "b/a"), "different", 0o755)
	if acquire(t, root, "local", "b", "").Digest() == first.Digest() {
		t.Fatal("content ignored")
	}
}

func TestReadDirRejectsUnsafePathsAndDistinguishesMissing(t *testing.T) {
	root := t.TempDir()
	if _, err := bundle.ReadDir(context.Background(), filepath.Join(root, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing: %v", err)
	}
	if _, err := bundle.ReadDir(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "file"), "data", 0o644)
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{root, filepath.Join(root, "file"), filepath.Join(root, "link", "missing")} {
		_, err := bundle.ReadDir(context.Background(), path)
		var unsafe *bundle.UnsafePathError
		if !errors.As(err, &unsafe) {
			t.Fatalf("%s: %v", path, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := bundle.ReadDir(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}

func TestAcquireGit(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init", "-b", "main")
	write(t, filepath.Join(repo, "skills/review/SKILL.md"), "first", 0o644)
	write(t, filepath.Join(repo, "skills/review/run.sh"), "#!/bin/sh\nexit 0\n", 0o755)
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "initial")
	sha := git(t, repo, "rev-parse", "HEAD")
	git(t, repo, "tag", "v1")
	git(t, repo, "checkout", "-b", "other")
	write(t, filepath.Join(repo, "skills/review/SKILL.md"), "second", 0o644)
	git(t, repo, "commit", "-am", "other")
	git(t, repo, "checkout", "main")
	gitRemote(t, repo)
	local := acquire(t, repo, "local", "skills/review", "")
	temp := t.TempDir()
	t.Setenv("TMPDIR", temp)
	for _, ref := range []string{"", "main", "v1", sha, "other"} {
		remote := acquire(t, repo, "https://example.com/skills.git", "skills/review", ref)
		if ref != "other" && (remote.Digest() != local.Digest() || !strings.Contains(remote.Source(), sha)) {
			t.Fatal("Git and local differ")
		}
		if ref == "other" && remote.Digest() == local.Digest() {
			t.Fatal("branch ignored")
		}
	}
	root := acquire(t, repo, "https://example.com/skills.git", "", "")
	if len(root.Files()) != 2 {
		t.Fatalf("Git metadata included: %#v", root.Files())
	}
	for _, fields := range [][2]string{{"missing", "main"}, {".", "missing"}} {
		reference, _ := bundle.ParseReference("https://example.com/skills.git", fields[0], fields[1])
		if _, err := bundle.Acquire(context.Background(), repo, reference); err == nil {
			t.Fatal("accepted missing path/ref")
		}
	}
	entries, err := os.ReadDir(temp)
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary acquisition resources leaked: %v, %v", entries, err)
	}
	if err := os.RemoveAll(repo); err != nil {
		t.Fatal(err)
	}
	if string(local.Files()[0].Data) != "first" || len(root.Files()) != 2 {
		t.Fatal("snapshot retained source dependency")
	}
}

func TestGitUnsafeContent(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init", "-b", "main")
	write(t, filepath.Join(repo, "content/file"), "data", 0o644)
	if err := os.Symlink("file", filepath.Join(repo, "content/link")); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "link")
	sha := git(t, repo, "rev-parse", "HEAD")
	git(t, repo, "update-index", "--add", "--cacheinfo", "160000,"+sha+",submodule/child")
	git(t, repo, "commit", "-m", "submodule")
	gitRemote(t, repo)
	for _, path := range []string{"content", "submodule"} {
		reference, _ := bundle.ParseReference("https://example.com/skills.git", path, "")
		_, err := bundle.Acquire(context.Background(), repo, reference)
		var unsafe *bundle.UnsafePathError
		if !errors.As(err, &unsafe) {
			t.Fatalf("%s: %v", path, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reference, _ := bundle.ParseReference("https://example.com/skills.git", "", "")
	if _, err := bundle.Acquire(ctx, repo, reference); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}

func TestCancellationDuringGitCleansTemporaryRepository(t *testing.T) {
	root := t.TempDir()
	temporary := filepath.Join(root, "temporary")
	if err := os.Mkdir(temporary, 0o700); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "bin")
	ready := filepath.Join(root, "ready")
	write(t, filepath.Join(bin, "git"), "#!/bin/sh\n: > \"$TRIUM_TEST_READY\"\nexec sleep 30\n", 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TRIUM_TEST_READY", ready)
	t.Setenv("TMPDIR", temporary)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			if _, err := os.Stat(ready); err == nil {
				cancel()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	reference, err := bundle.ParseReference("example/skills", "", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = bundle.Acquire(ctx, root, reference)
	<-done
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("in-flight cancellation: %v", err)
	}
	if _, err := os.Stat(ready); err != nil {
		t.Fatalf("Git did not start: %v", err)
	}
	entries, err := os.ReadDir(temporary)
	if err != nil || len(entries) != 0 {
		t.Fatalf("cancellation leaked acquisition resources: %v, %v", entries, err)
	}
}

func TestSourceForms(t *testing.T) {
	repo := t.TempDir()
	git(t, repo, "init", "-b", "main")
	write(t, filepath.Join(repo, "file"), "data", 0o644)
	git(t, repo, "add", ".")
	git(t, repo, "commit", "-m", "initial")
	config := filepath.Join(t.TempDir(), "gitconfig")
	for _, remote := range []string{"https://github.com/example/skills", "https://github.com/example/skills.git", "https://example.com/skills.git", "ssh://git@example.com/skills.git", "git@example.com:skills.git", "git://example.com/skills.git"} {
		git(t, repo, "config", "--file", config, "--add", "url."+repo+".insteadOf", remote)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	for _, source := range []string{"example/skills", "github:example/skills", "github.com/example/skills", "https://github.com/example/skills", "https://github.com/example/skills.git", "https://example.com/skills.git", "git:https://example.com/skills.git", "git+https://example.com/skills.git", "ssh://git@example.com/skills.git", "git+ssh://git@example.com/skills.git", "git@example.com:skills.git", "git://example.com/skills.git"} {
		if len(acquire(t, repo, source, "", "").Files()) != 1 {
			t.Fatalf("source: %s", source)
		}
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.com",
		"-c", "commit.gpgsign=false", "-c", "core.hooksPath=" + os.DevNull}, args...)...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func gitRemote(t *testing.T, repo string) {
	t.Helper()
	config := filepath.Join(t.TempDir(), "gitconfig")
	git(t, repo, "config", "--file", config, "url."+repo+".insteadOf", "https://example.com/skills.git")
	t.Setenv("GIT_CONFIG_GLOBAL", config)
}
