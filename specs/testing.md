# Verification design

Public module interfaces are the primary test surfaces. Tests use temporary directories and local Git repositories.
No test requires external network access or agent executables, and installed resource content is never executed.

## Manifest

`manifest.Parse` tests cover strict fields, required targets, target lists, duplicate targets, invalid names, nulls,
source/ref/path combinations, MCP transport combinations, headers, token references, and legacy field rejection. A valid
declaration can reference nonexistent local or remote content, demonstrating that parsing does not acquire sources.

## Bundle

`ParseReference`, `Acquire`, and `ReadDir` tests cover:

- Supported repository forms using Git URL rewrites into temporary local repositories.
- HEAD, branch, tag, and commit selection; root and nested snapshots.
- Equality across local and Git acquisition and across unrelated file modes and source locations.
- Executable bits, content changes, deterministic order, and defensive copies.
- Missing versus empty directories, unsafe paths, links, and submodules.
- Cancellation, missing refs and paths, and temporary repository cleanup.
- Snapshot independence after source deletion.

## Reconciliation

Public preparation and Plan/Apply tests exercise all three native adapters:

- Add, verify, update, enable-only update, no-op apply, no pruning, and agent instructions.
- Separate skill/plugin namespaces and preservation of unrelated bundles and native settings.
- Codex disabled-skill policy, versioned cache repair, and retention of older cache versions.
- Claude reserved locations and symlinked catalogs.
- External ownership, orphan/missing bundles, duplicate registrations, and unsupported registries.
- Plugin conflicts blocking skill writes, and invalid source metadata blocking every target before writes.
- Fresh apply planning, snapshot reuse, stable ordering, and earlier-target preservation after a later conflict.
- Invalid and ambiguous native JSON documents failing before writes.
- MCP stdio/HTTP mapping, transport replacement, token references, working directories, and input snapshot independence.
- Combined skill/plugin/MCP apply, shared Codex configuration, native option preservation, and no-op MCP applies.
- MCP batch rollback, retained backups, native-file drift, and malformed native configuration.

Private transaction tests cover the internal seam that owns failures. They use real temporary targets and observations,
then inject verification failure or cancellation. Assertions cover registry and read-only policy drift, complete bundle
and registry restoration, removal of failed additions, retained backups, cleanup of stages, rejected overlapping writes,
staging failure before any replacement, and accurate partial reports after a later resource class fails.

Lock tests cover contention across target homes sharing one user lock, preview while the lock is held, unlock, canceled
apply, and OS release after a subprocess exits without explicitly unlocking.

## CLI

CLI acceptance tests call `cli.Run` with temporary homes, manifest files, and captured output. They cover supported
agents, target lists, environment and explicit target overrides, preview without target creation, plan/apply/plan,
plugin-only and MCP output, Claude state paths, and correct installed paths. Tests must set a temporary user home even
when the agent target is explicit: apply uses a per-user lock.

## Completion checks

```sh
go test -race ./...
go vet ./...
gofmt -l cmd internal
git diff --check
```

All formatting output must be empty. Build the command into a temporary destination. Tests must leave no generated
files in the repository or real agent homes. No additional dependencies or external credentials are required.
