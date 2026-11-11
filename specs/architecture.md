# trium architecture

Status: implemented for skills, plugins, and MCP server configurations.

## Modules

trium has four internal modules. Manifest parsing produces bundle and MCP declarations. Bundle acquisition produces
immutable snapshots. Reconciliation turns those snapshots into verified native state. The CLI supplies process
configuration and renders results.

| Module | Files | Responsibility |
| --- | --- | --- |
| `cli` | `run.go`, `render.go` | Arguments, environment, manifest file reading, output, exit status |
| `manifest` | `manifest.go` | Strict YAML decoding and declaration validation |
| `bundle` | `reference.go`, `snapshot.go`, `local.go`, `git.go` | Source references, safe acquisition, file snapshots, fingerprints |
| `reconcile` | `run.go`, `model.go` | Preparation, target ordering, public reports, Plan/Apply |
| `reconcile` | `skills.go`, `plugins.go`, `mcps.go` | Shared resource validation and planning |
| `reconcile` | `codex.go`, `claude.go`, `kimi.go` | Private native adapters and registry codecs |
| `reconcile` | `files.go`, `lock.go` | Observations, replacements, backups, rollback, apply locking |

`cmd/trium/main.go` supplies cancellation and process exit. Tests sit beside their implementation.

## Dependencies

- `bundle` uses the standard library and the Git executable for repository acquisition.
- `manifest` uses YAML and the pure `bundle.ParseReference` operation. It performs no I/O or environment discovery.
- `reconcile` accepts manifest declarations and bundle snapshots. Native adapters remain private to this module.
- `cli` reads the manifest file, resolves the process environment, and calls the other modules.

There is no exported resource execution hierarchy, native registration protocol, or generic filesystem interface.
The three native adapters share a private `validate`/`plan` seam. Common direct-file execution stays inside
reconciliation.

## Operation lifecycle

1. The CLI reads exactly the requested manifest file, or the default `~/.config/trium.yaml`.
2. `manifest.Parse` returns targets and source references. Referenced directories and repositories need not exist yet.
3. `reconcile.Prepare` resolves target overrides from explicit homes and acquires each declared bundle once.
4. Preparation validates skill and plugin metadata and MCP capabilities for every target before inspecting native state.
5. `Prepared.Plan` inspects current native state and returns displayable reports without writing agent state.
6. `Prepared.Apply` takes one per-user lock and computes a fresh private plan for each target before writing it.

A prepared value owns only in-memory state. Temporary repositories are removed during acquisition. It has no cleanup
method. Reusing it retains the same source snapshots; preparing again refreshes source content.

## Ownership and scope

- A declared skill or plugin owns its complete installed bundle. Replacement removes stale files inside that bundle.
- Native plugin registration and enabled state are reconciled with the bundle.
- Undeclared resources and unrelated native configuration are preserved.
- Skills, plugins, and MCPs have separate namespaces, even when they share a name.
- Declared MCPs own connection fields; unrelated native server policy and undeclared entries remain.
- Native paths, ownership rules, and codecs belong to the corresponding private adapter.
- Expected ownership conflicts block all resource writes for that target. Invalid native documents are errors.

Targets are processed in declaration order; resource classes run in skills/plugins/mcps order. A failed or conflicted
target stops apply. Earlier verified targets and resource classes remain applied. There is no transaction across
targets.

## Filesystem and concurrency

The bundle module reads source and installed directories into the same canonical snapshot representation. It rejects
unsafe entries and paths. Reconciliation separately owns target observations and mutation lifecycles.

Every private write batch contains its read set and write set. The read set includes files that influence policy even
when the batch does not write them, such as Codex's disabled-skill configuration. Reconciliation verifies observations
before staging and again before commitment.

`Prepared.Apply` owns one advisory lock at `<user-home>/.trium/apply.lock`. This serializes trium applies across
relocated agent homes and their shared skill destinations. The persistent lock file is not deleted; the OS releases its
lock when the descriptor closes or the process exits. Preview never takes the lock.

The transaction implementation stages replacements beside destinations and copies original targets into per-agent
backups. It then replaces targets and verifies the result. Detected failures restore affected targets in reverse order.
Successful backups remain available. Failed restoration retains recovery files and emits warnings.

Individual file renames are atomic where supported. Multi-file operations are not globally atomic. Administrative
parent directories and backup directories may remain after failure. Process-crash recovery is not automatic, and
non-trium writers do not participate in the advisory lock.

See [interfaces.md](interfaces.md), [reconciliation.md](reconciliation.md), [agents.md](agents.md), and
[testing.md](testing.md) for the interface, execution, native-format, and verification contracts.
