# Interfaces and module contracts

The design uses small Go interfaces at real seams: three resource classes vary independently of three agent
implementations. Planning and applying behavior, rather than native file access, is the public resource interface.

## Resource values

`internal/resource` defines `Skill`, `Plugin`, `MCP`, and `Desired`.

```go
type Desired struct {
    Skills  []Skill
    Plugins []Plugin
    MCPs    []MCP
}

type Digest [32]byte

type Item interface {
    Key() string
    Fingerprint() Digest
}
```

Each resource value implements `Item`. `Key` is the manifest name. `Fingerprint` is the canonical semantic digest
defined in [skillet.schema.yaml](../skillet.schema.yaml), not a digest of native serialization.

Desired file-backed values retain a bundle source so an adapter can stage content. A local source contains its resolved
path. A GitHub source contains an immutable in-memory snapshot of regular file paths, bytes, and executable bits, plus
its repository, requested ref, and resolved commit for diagnostics. Both forms use the same bundle fingerprint and
staging behavior. Source locations and resolved commits never participate in fingerprints.

The skillet loader acquires every declared GitHub bundle once, before agent discovery. It resolves the ref, downloads
and validates the archive, selects the requested directory, and returns the snapshot as part of `resource.Desired`.
The CLI injects an HTTP client into the loader; tests supply a fake transport. Agent adapters receive neither an HTTP
client nor repository declarations, and never acquire source content themselves. Apply therefore uses the same remote
snapshot in its fresh plan and writes, even if the upstream ref moves during that invocation.

`MCP` uses typed fields matching the manifest schema rather than `map[string]any`. Agent-only fields are decoded into
adapter-private types and do not leak into these values.

## Resource interfaces

```go
type Resource[T Item] interface {
    Plan(context.Context, []T) (ChangeSet[T], error)
    Apply(context.Context, ChangeSet[T]) (ApplyReport, error)
}

type Skills interface {
    Resource[Skill]
}

type Plugins interface {
    Resource[Plugin]
}

type MCPs interface {
    Resource[MCP]
}
```

The three named interfaces make constructor requirements explicit and prevent accidentally passing changes to the wrong
adapter. They share behavior without sharing agent-specific implementation.

### `Plan`

`Plan` is a deep operation: it scans, decodes, canonicalizes, validates capabilities, and diffs desired resources.

- It makes no mutations and performs no network requests. An adapter may invoke a documented local, read-only agent CLI
  query, but never a management or resource-execution command.
- An empty desired slice returns an empty change set. Adapters may skip scanning in that case.
- A missing target is valid empty state. Invalid native syntax, failed CLI queries, and unexpected I/O failures return
  an error.
- Expected incompatibilities are returned as `Conflict` changes, not operational errors.
- It captures a digest for every target that `Apply` could modify, including registry files.
- Results are sorted by resource key and are independent of map iteration and native array ordering.

A shared generic `Diff[T Item]` helper implements add/update/unchanged comparison. Adapters remain responsible for
native conflicts and capability checks. The helper never emits removals.

### `Apply`

`Apply` executes one previously planned resource class.

- It rejects a change set containing conflicts before its first write or management command.
- It rechecks all captured target digests before mutation and reports concurrent modification on mismatch.
- It ignores unchanged entries.
- It preserves unrelated native values and backs up every target the chosen strategy can modify.
- The adapter privately chooses direct file writes, an agent management command, or a hybrid. Direct writes use staging
  and atomic replacement where possible. CLI invocations use a fixed executable and argument vector without a shell,
  capture and redact output, and are followed by a fresh scan that verifies the requested state.
- It returns successfully applied keys, backup locations, and native side-effect warnings. It does not retry an
  operational failure automatically.
- It never directly invokes a hook, MCP server, plugin executable, or skill script. Any side effects of a native
  management command must be documented and reported.

Callers must not mutate a change set between `Plan` and `Apply`. The apply command creates a fresh plan under lock
rather than accepting a serialized plan. Execution strategy is not part of this interface.

## Changes and reports

```go
type Action string

const (
    Add       Action = "add"
    Update    Action = "update"
    Unchanged Action = "unchanged"
    Conflict  Action = "conflict"
)

type TargetDigest struct {
    Path   string
    Exists bool
    Digest Digest
}

type Change[T Item] struct {
    Action  Action
    Desired T
    Current *T
    Reason  string
}

type ChangeSet[T Item] struct {
    Changes []Change[T]
    Targets []TargetDigest
}

func (c ChangeSet[T]) HasConflicts() bool
func (c ChangeSet[T]) HasChanges() bool

type AppliedItem struct {
    Key    string
    Action Action
}

type ApplyReport struct {
    Applied  []AppliedItem
    Backups  []string
    Warnings []string
}
```

`Current` is nil for additions. `Reason` is required for conflicts and may explain an update. Display rendering compares
canonical before/after values; it does not infer operations from native bytes.

The engine's aggregate plan has explicit typed fields, avoiding an untyped heterogeneous collection:

```go
type Plan struct {
    Skills  resource.ChangeSet[resource.Skill]
    Plugins resource.ChangeSet[resource.Plugin]
    MCPs    resource.ChangeSet[resource.MCP]
}
```

The CLI converts these values into display-only rows. The engine does not format text.

## Agent interface

`internal/agent` exposes one agent and its resource set.

```go
type Resources struct {
    Skills  resource.Skills
    Plugins resource.Plugins
    MCPs    resource.MCPs
}

type Agent interface {
    Name() string
    Resources() Resources
    Lock(context.Context) (unlock func() error, err error)
}
```

All three fields are required for Codex, Claude, and Kimi. A nil field is a construction error, not an implicit
capability skip. Future agents with genuinely unsupported resource classes should return conflict-producing adapters so
requested resources are never silently ignored.

`Lock` is per agent target and is used only for apply. It must be reentrant-unsafe, fail if another live process holds
it, and remove the lock on unlock. Plan does not create or inspect the lock file.

Agent constructors accept an explicit target struct containing every native path. CLI-backed adapters also accept an
explicit executable path and private command-runner seam. The CLI selects one factory using the manifest's
`target.agent`. That factory resolves an optional absolute or home-relative `target.path` override, then `CODEX_HOME`,
`CLAUDE_CONFIG_DIR`, or `KIMI_CODE_HOME`, then the corresponding home-relative default, plus any native executable
selected by the adapter. This keeps tests hermetic and environment lookup out of reconciliation. The engine receives
neither paths nor command runners.

## Engine interface

```go
type Engine struct {
    // No native agent or CLI dependencies.
}

func (Engine) Plan(
    ctx context.Context,
    a agent.Agent,
    desired resource.Desired,
) (Plan, error)

func (Engine) Apply(
    ctx context.Context,
    a agent.Agent,
    desired resource.Desired,
) (Plan, ApplyReport, error)
```

`Engine.Plan` calls each resource interface in fixed order and aggregates the complete plan. A hard error aborts;
conflicts are accumulated so the CLI can show all actionable problems.

`Engine.Apply`:

1. Acquires and defers release of the agent lock.
2. Recomputes the complete plan under that lock.
3. Returns the plan without writes if any class contains a conflict.
4. Applies non-empty classes in skills, plugins, MCPs order.
5. Stops on the first operational error and returns the plan and partial report alongside the error.

The aggregate report includes the plan that was actually applied. There is no interface for applying a stale or
externally supplied plan.

## Error taxonomy

- **Invalid invocation, manifest, or acquired bundle:** a typed validation error with a field path; exit 2 before
  scanning. This includes invalid GitHub declarations, unsafe archive paths, and invalid skill content.
- **Requested/native incompatibility:** a `Conflict` change with key and reason; complete diff, exit 2, no mutations.
- **GitHub HTTP, native parse, CLI query, permission, or I/O failure:** a wrapped operational error with source, target,
  or command context; exit 1. HTTP status, timeout, cancellation, response-limit, and malformed-archive failures happen
  before agent discovery or native writes.
- **Concurrent target modification:** a typed drift error naming the target; exit 1 without mutating that resource.
- **Apply failure:** a wrapped error plus partial `ApplyReport`; exit 1 with explicit partial-result output.

Errors wrap with `%w` so tests can use `errors.Is`/`errors.As`. User-facing rendering adds context once at the CLI seam;
lower modules do not print.

## Interface test surface

Each agent adapter runs the same resource contract suite against temporary native targets. Tests cross `Plan` and
`Apply`, not private codec helpers. Codec fixtures supplement those contract tests for native compatibility. The generic
differ is tested independently for identity, ordering, fingerprints, and no-removal behavior.
