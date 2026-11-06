# Skillet Architecture

Status: proposed

## Purpose

Skillet is a Go CLI that reconciles selected resources from one versioned manifest into one per-user Codex, Claude Code,
or Kimi Code environment. Its primary seam is Plan/Apply: planning produces a semantic diff without changing the
machine; applying recomputes that diff under a lock and installs the changes.

This document describes the implementation design. Command examples and field definitions are interface contracts, not
end-user documentation.

## Scope and Assumptions

- The initial scope is the current user's agent environments, not project, enterprise-managed, cloud, or remote
  configuration.
- Every manifest targets exactly one agent. An optional target path overrides the selected agent's home; otherwise its
  environment variable and default home are used.
- Skills and plugins come from local files relative to the skillet file or from a directory in a public GitHub
  repository. Context segments remain local. Remote configuration targets are outside version 1.
- MCP declarations support stdio and streamable HTTP. Hook management is outside version 1; existing native hooks remain
  unmanaged. Other native capabilities remain representable in native configuration but are not managed by Skillet.
- A skillet is additive and corrective. It adds or updates named resources but never deletes a resource merely because
  it disappeared from the manifest.
- Each resource adapter chooses whether to use direct native-file operations, the agent's CLI, or a hybrid. The engine
  does not impose an execution strategy. Skillet never directly executes hooks, MCP servers, plugin code, or skill
  scripts.
- Native agent releases can change storage formats and management commands. Each adapter owns its compatibility and
  fails explicitly rather than guessing at an unfamiliar shape or command.

## Approaches considered

1. **Semantic domain model with per-agent adapters — selected.** Each adapter owns native behavior, while the engine
   gets deterministic diffs and a consistent Apply contract.
2. **Invoke each agent's CLI — adapter-private option.** This requires less knowledge of native formats, but dry-run
   support is weak, commands vary by version, and rollback is harder. It is not a global architecture requirement.
3. **Render whole configuration files from templates — rejected.** Generation is simple, but it destroys unmanaged
   settings and makes concurrent ownership unsafe.

Direct files, CLI management commands, and hybrid implementations can coexist across resource classes and agents.
Simple, documented file formats are good candidates for direct writes; registry-backed operations can use a native
management command when that is safer. Whatever the choice, the adapter must preserve the same non-mutating Plan,
target-digest, backup, error-reporting, and post-apply verification contracts.

A second interface choice was to expose `Scan` plus `Apply` from every resource. The selected design instead exposes
`Plan` plus `Apply`: scanning, native decoding, capability checks, canonicalization, and execution strategy are hidden
behind the resource interface. This is a deeper module and keeps agent-specific behavior out of the engine.

## Modules

```text
cmd/skillet
    └── internal/cli
          ├── internal/skillet
          ├── internal/engine
          └── internal/agents/{codex,claude,kimi}
                               └── internal/agent
internal/engine ────────────────┘
internal/skillet ───> internal/resource <─── internal/agent
```

| Module                   | Responsibility                                                                     | Must not know                     |
| ------------------------ | ---------------------------------------------------------------------------------- | --------------------------------- |
| `cmd/skillet`            | Process entry point and exit status                                                | Manifest or native config details |
| `internal/cli`           | Argument dispatch, output rendering, wiring                                        | Reconciliation algorithms         |
| `internal/skillet`       | Strict YAML decoding, local/GitHub acquisition, validation, desired fingerprints   | Agent paths and formats           |
| `internal/resource`      | Resource values, Plan/Apply interfaces, changes, reports, shared semantic differ   | Any specific agent                |
| `internal/agent`         | Agent interface and the set of four resource interfaces                            | CLI or manifest decoding          |
| `internal/engine`        | Deterministic orchestration, apply locking, conflict preflight, result aggregation | Native serialization              |
| `internal/agents/codex`  | Codex discovery, codecs, all four resource adapters                                | Other agents                      |
| `internal/agents/claude` | Claude discovery, codecs, all four resource adapters                               | Other agents                      |
| `internal/agents/kimi`   | Kimi discovery, codecs, all four resource adapters                                 | Other agents                      |

Agent packages may share unexported filesystem and structured-file helpers through an internal package. Resource
semantics must remain in the agent adapter; a shared helper must not turn into a lowest-common-denominator agent model.

The skillet loader is the acquisition seam. Its implementation hides GitHub ref resolution, archive download, safe
extraction, and in-memory snapshots behind the existing manifest load operation. This keeps network policy and bundle
validation in one module instead of duplicating them across three agents and two resource classes.

### Dependency rules

- `resource` has no dependencies on other Skillet modules.
- `skillet` emits `resource.Desired`; it does not import `agent` or an agent implementation. The CLI injects its HTTP
  client, and only this module makes GitHub requests.
- `engine` depends only on `agent` and `resource`.
- Agent implementations depend on `agent` and `resource`, never on `engine`, `cli`, or `skillet`.
- The CLI is the only module that chooses an agent implementation from a name.

## Reconciliation flow

1. The CLI selects `skillet.yaml` or the path supplied by the global `--file` option.
2. The skillet module decodes and statically validates the complete document, including its single `target`. It resolves
   local sources and acquires each declared GitHub source once into memory, then validates bundle content and computes
   semantic fingerprints.
3. The CLI selects the agent factory named by `target.agent`. The factory resolves the optional `target.path` override,
   then the agent-home environment variable and default, and discovers concrete per-user targets. Environment discovery
   occurs only in this factory; adapters receive explicit paths.
4. The engine calls `Plan` for every resource class in fixed order: skills, plugins, MCPs, context.
5. Each resource adapter scans its writable user-scope targets, decodes native state, validates requested capabilities,
   and returns semantic changes plus target digests. Resource planning performs no acquisition or network requests.
6. The plan command renders changes and conflicts. It creates no directories, lock files, backups, caches, or native
   configuration. A manifest with GitHub sources requires network access during step 2; a local-only plan does not.
7. The apply command acquires the agent lock before repeating steps 4–5 with the already acquired sources. Any conflict
   aborts before writes. Otherwise the engine applies each non-empty change set in the same fixed order and reports the
   result.

There is no saved plan file or remote-source cache in version 1. Apply always plans against current native state, so a
stale diff cannot be submitted for execution, and it stages the same GitHub snapshots used to render that diff.

## Ownership model

Skillet manages only the intersection of the manifest and each agent's writable user scope.

- **Skills and plugins:** a declared name owns the complete installed bundle. Updates may remove stale files inside that
  bundle. Sibling bundles are untouched.
- **MCPs:** a declared name owns the portable fields defined by the schema. Unrecognized native fields on the same entry
  are preserved when the agent format permits them.
- **Context:** a declared name owns only the bytes between its marker pair. Text outside markers is preserved exactly.
- **Absent manifest entries:** no action. Version 1 has no prune operation.

An existing resource with the same name is compared by semantic fingerprint, not source path, modification time, or
native file formatting. Native entries from plugins, project configuration, system policy, or built-in catalogs are not
imported into standalone resource state.

## Integrity and failure behavior

- A missing target is empty state; a malformed or unreadable existing target is an error.
- Invalid skillet input fails before agent scanning.
- Expected incompatibilities—unsupported native capabilities, unsafe target types, or externally managed plugins—are
  plan conflicts and block all mutations.
- Every `Apply` verifies the digests captured by its `Plan` before modifying a target. A mismatch means concurrent drift
  and aborts that resource.
- Direct writes use a temporary file in the target directory followed by rename. Delegated native commands have their
  native atomicity and must be followed by a fresh verification scan. Bundle replacement, multi-file registry updates,
  and management commands are rollback-capable only to the extent the adapter can guarantee; none is globally atomic.
- Each target the chosen strategy can change is backed up before commitment. An operational failure stops subsequent
  resource classes and reports earlier successful changes and irreversible native side effects; there is no claim of a
  cross-resource transaction.
- Unrelated settings are preserved semantically. Byte-for-byte formatting and comments in rewritten JSON/TOML files are
  not guaranteed; original bytes remain in the backup. Context text outside markers is the exception and is preserved
  byte-for-byte.

See [engine.md](engine.md) for command and failure contracts.

## Security constraints

- Local source paths cannot escape the manifest directory, GitHub paths cannot escape their repository, and source
  symlinks are rejected.
- GitHub acquisition uses fixed anonymous HTTPS GitHub endpoints, standard TLS verification, bounded response and
  extracted sizes, file-count limits, timeouts, and context cancellation. Redirects may only go to approved GitHub
  hosts. Repository fields cannot select another host, and refs are URL-path escaped rather than passed to a shell.
- Archives are extracted into memory after stripping GitHub's single archive root. Absolute paths, parent traversal,
  links, and special files in the selected bundle are rejected; unrelated archive entries are ignored. Skillet does not
  run `git`, clone hooks, submodule commands, credential helpers, or any acquired content. Plan retains no temporary
  extraction files.
- Target paths come only from the manifest's explicit `target.path`, trusted agent-home environment variables, and
  explicit constructor configuration. Manifest overrides must be absolute or start with `~/`; relative paths, empty
  paths, and control characters are rejected during manifest validation.
- Plan makes no mutations. Only the loader may make direct network requests, only for declared GitHub sources, and
  before agent discovery; resource planning and local-only commands make none. An Apply adapter may invoke a fixed
  native management executable and argument vector without a shell; it must capture and redact diagnostics and report
  native side effects.
- MCP environment and header values are always redacted in human-readable diffs and captured command diagnostics.
  Skillet does not resolve secret references or read credential files. GitHub acquisition is anonymous and does not read
  GitHub tokens or other credentials.
- New files that can contain credentials use owner-only permissions; existing permissions are preserved where practical.
- Applying an agent configuration can enable code to run in a later agent session. The plan must identify that effect
  even though Skillet itself does not execute it.

## Related design documents

- [skillet.schema.yaml](../skillet.schema.yaml): version 1 manifest and canonical resource semantics
- [interfaces.md](interfaces.md): Go interfaces and invariants
- [agents.md](agents.md): native targets, conversion rules, and adapter execution strategies
- [engine.md](engine.md): CLI, diff, locking, backup, and exit behavior
- [testing.md](testing.md): verification strategy and acceptance coverage
