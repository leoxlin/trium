# Module interfaces and contracts

## Manifest

```go
func Parse(data []byte) (Manifest, error)

type Manifest struct {
    Targets []Target
    Skills  []Declaration
    Plugins []Declaration
    MCPs    []MCP
}

type Target struct { Agent, Path string }
type Declaration struct {
    Name   string
    Source bundle.Reference
}

type ValidationError struct { Field, Message string }
```

`Parse` accepts exactly one version-1 YAML document. It rejects unknown fields, nulls, duplicate declarations, invalid
names, unsupported agents, invalid target paths, and incompatible source fields. Target order is preserved. Skill,
plugin, and MCP names are independent. MCP declarations require exactly one stdio command or HTTP URL, with
transport-specific fields.

Parsing performs no filesystem access, environment lookup, or network activity. It calls the pure source-reference
parser and adds declaration context to its errors. A valid reference to absent content can be parsed successfully.

## Bundle

```go
func ParseReference(source, path, ref string) (Reference, error)
func Acquire(ctx context.Context, baseDir string, source Reference) (Snapshot, error)
func ReadDir(ctx context.Context, path string) (Snapshot, error)

type Reference struct { /* private normalized fields */ }
type Snapshot struct { /* private files, digest, source description */ }
type Digest [32]byte

type File struct {
    Path       string
    Data       []byte
    Executable bool
}

func (s Snapshot) Files() []File
func (s Snapshot) Digest() Digest
func (s Snapshot) Source() string
func (d Digest) String() string
```

`Reference` values come from `ParseReference`; the zero value is invalid. Local references require a path and forbid a
ref. Repository references normalize supported URLs and GitHub shorthand, default their ref to HEAD, and reject unsafe
refs and escaping paths. Reference errors identify the offending source field.

`Acquire` requires an absolute base directory. Local paths stay within that directory. Repository paths stay within the
selected Git tree. Acquisition owns its temporary repository, timeout, subprocesses, and archive decoding. It disables
terminal credential prompts and Git hooks, rejects submodules, and never executes acquired content. Git archive
attributes apply. No temporary resource is transferred to the caller.

`ReadDir` reads an explicitly selected real directory, including installed bundles. It has no manifest-relative source
semantics. Both source and installed snapshots reject symlinked ancestors, links, and special files. Missing directories
return an error distinguishable with `errors.Is(err, os.ErrNotExist)`; existing empty directories yield valid snapshots.

Snapshots contain sorted, normalized file paths, bytes, and executable bits. Their digest excludes source location,
revision labels, timestamps, and unrelated mode bits. `Files` returns a deep copy. Snapshots remain usable after their
source disappears. `UnsafePathError` describes unsafe content or paths; transport and filesystem failures retain their
underlying errors.

## Reconciliation

```go
type Homes struct {
    User, Codex, Claude, Kimi string
    ClaudeConfig string // Optional explicit user-state path
}
type Prepared struct { /* private snapshots and native adapters */ }

func Prepare(ctx context.Context, spec manifest.Manifest,
    baseDir string, homes Homes) (*Prepared, error)
func (p *Prepared) Plan(ctx context.Context) ([]TargetPlan, error)
func (p *Prepared) Apply(ctx context.Context) ([]TargetResult, error)
```

All homes and the manifest base directory must be absolute. Reconciliation reads no process environment. A manifest `~/`
target override is resolved against `Homes.User`; a target without an override uses its supplied agent home. Duplicate
resolved targets are rejected. `Homes.ClaudeConfig`, when supplied, must be absolute and selects the user-state file for
a Claude target without a path override. Otherwise a default Claude home uses `<user-home>/.claude.json`, and a
relocated home uses `<agent-home>/.claude.json`.

Preparation acquires every declaration once, sorts resources by name, validates skill frontmatter, and validates native
plugin metadata for all selected agents. MCP declarations are copied and sorted; relative working directories resolve
against the manifest directory. Claude rejects MCP working directories before target I/O. Native validation performs no
target I/O. An invalid later target's plugin metadata therefore fails before any earlier target is applied.
Acquired-content failures use `InputError{Field, Err}`; I/O failures remain operational errors with declaration context.

`Plan` returns targets in declaration order and changes in skills/plugins/mcps order, sorted by name. It never creates
target files, directories, locks, or backups. It stops on an operational error and can return earlier target reports.

`Apply` takes the per-user lock and computes its own private plan against current native state. Public reports cannot be
submitted for execution. All resource classes must pass conflict preflight before a target is written. Apply stops at
the first conflict or operational error, retaining earlier successes and returning partial results.

```go
type Kind string   // Skills, Plugins, MCPs
type Action string // Add, Update, Unchanged, Conflict

type Change struct {
    Kind   Kind
    Name   string
    Action Action
    Before bundle.Digest
    After  bundle.Digest
    Reason string
}

type TargetPlan struct {
    Agent   string
    Home    string
    Changes []Change
}
func (p TargetPlan) HasConflicts() bool

type TargetResult struct {
    Plan     TargetPlan
    Applied  []Change
    Backups  []string
    Warnings []string
}
```

Digests describe bundle content or canonical JSON for a native MCP entry. A plugin can require an update with equal
before/after digests because its enabled state or native cache differs. Reasons explain those changes. The before digest
is meaningful only for existing content.

A conflicted plan is returned without an operational error; callers inspect `HasConflicts`. On an apply error, results
include earlier targets and the attempted target when planning began. Only verified resource classes appear in
`Applied`. Errors identify the failed target and operation. Warnings identify incomplete restoration or stage cleanup.

Native adapters implement a private `validate(desired)`/`plan(context.Context, desired)` interface. Private plans
contain
observations and replacement batches. No native registry maps, target digests, execution instructions, or rollback state
are exported. `DriftError` identifies a changed native target.

## CLI

```go
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int
```

Cobra handles command selection, positional argument validation, and help. `trium [manifest]` previews changes;
`trium apply [manifest]` applies them. An omitted manifest defaults to `~/.config/trium.yaml`.

The CLI owns arguments, process environment, home defaults, manifest file reading, formatting, and exit codes. It calls
`manifest.Parse`, `reconcile.Prepare`, and either `Plan` or `Apply`. It renders partial results before reporting an
operational error. Resource content and native formats are handled by the other modules.
