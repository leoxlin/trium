# Verification design

The public resource interface is the primary test surface. Tests exercise real Plan/Apply behavior against temporary
native targets, not mocks of the interface being tested.

## Unit Coverage

Implemented Git-source tests use temporary local repositories and require the Git executable, without external network
access. They cover default HEAD, branch, tag, and commit selection, root and nested bundles, executable bits, equality
with local fingerprints, apply idempotence, invalid declarations, missing revisions and paths, links, and cancellation.

### Manifest module

Use table-driven tests for:

- supported version and every resource class;
- a required target mapping or non-empty list, all supported agents, absolute and `~/` overrides, invalid paths,
  duplicate targets, and strict field validation for every list entry;
- unknown fields, duplicate YAML keys, duplicate names, invalid names, and nulls;
- stdio/HTTP field exclusivity and transport-specific fields;
- missing, absolute, escaping, and symlinked local paths;
- required local `path` when `source` is `local` or omitted; Git URLs and GitHub shorthand for repository sources;
- repository syntax, default and explicit refs, root and nested bundle paths, and rejection of `ref` for local skills;
- rejection of local filesystem paths and `file://` URLs in `source`, including existing local skill directories;
- skill frontmatter validation and name agreement;
- deterministic bundle hashes despite file order, timestamps, non-executable mode changes, and source location;
- executable-bit and file-content hash changes.

Run the loader twice with differently ordered filesystem entries and require identical desired values and fingerprints.
An identical local directory and Git archive must produce the same desired bundle and fingerprint.

### Git acquisition

Use temporary local repositories and Git URL rewrites; no test contacts a remote host. Verify URL and shorthand
normalization through the public loader, local `path` selection without `source`, default HEAD, branch, tag, and full
commit
selection, root and nested bundles, executable bits, cancellation, missing refs and paths, symlinks, and submodules.
Assert that acquired bytes remain installable after the original repository disappears. Reject legacy `git` and
`github` fields, malformed source forms, unsafe paths, and `ref` on local directory declarations.

### Shared differ

Verify add, update, unchanged, mixed classes, deterministic ordering, duplicate defense, and no removals when desired
entries disappear. Confirm that equal semantic values with different native representations remain unchanged.

### Plan and rendering

Golden tests cover stable grouping and sorting, all actions, empty diffs, conflict summaries, field-level changes, and
redaction of every MCP environment/header value. Tests assert that Plan leaves a complete temporary filesystem
byte-for-byte unchanged, including absence of lock, backup, and newly created parent directories.

## Adapter contract suites

Shared resource behavior tests belong with their implementation, such as `internal/agents/shared/store_test.go`.
Each agent has its own backend test file with explicit temporary targets for discovery, wiring, and lock integration.
The suites collectively cover these behavioral cases:

1. Empty targets produce additions for every resource class.
2. Apply installs native state that the same adapter reads back as unchanged.
3. A second Plan reports no changes and a second Apply is a no-op.
4. Changed desired content produces updates without touching unrelated resources.
5. Removing a manifest entry produces no deletion.
6. Existing unmanaged native settings and resources survive apply semantically.
7. Missing files and valid empty files are distinguished from malformed files.
8. Unsupported native capabilities produce conflicts and zero writes.
9. Symlink and wrong-type targets produce conflicts without traversal.
10. Concurrent target modification invalidates captured digests and prevents the write.
11. Cancellation and injected write failures do not leave truncated files.
12. Lock contention blocks a second apply; unlock permits a later apply.

Adapter-specific cases add:

- **Codex:** shared skill root, disabled skill conflict, and HTTP/TOML field mapping.
- **Claude:** reserved/synced skill protection, explicit HTTP `type`, top-level user MCP mapping, and settings
  preservation.
- **Kimi:** relocated home isolation, camelCase MCP fields, and plugin `installed.json` preservation.

## Native fixtures

Keep small agent-owned JSON/TOML/plugin registry fixtures representing supported native releases. Fixture tests verify:

- decode → semantic model produces expected values;
- update preserves unknown settings and unrelated records;
- transport changes remove only obsolete managed fields;
- malformed shapes and duplicate keys fail with path context;
- plugin bundle/registry disagreement and external ownership become conflicts.

Fixture tests may cover private codecs, but they supplement rather than replace the public contract suite.

## Failure injection

Filesystem and command-runner failures are injected at private adapter seams, not by changing the public resource
interface. Cover failure while staging, backing up, replacing a bundle, writing a plugin registry, starting a native
management command, and after a native command has partially changed state.

Assertions distinguish:

- no mutations before conflict preflight;
- per-resource restoration after a failed multi-file write or management command;
- verification of post-command state through a fresh scan;
- explicit reporting of native side effects that cannot be rolled back;
- earlier successful classes after a later class fails;
- accurate partial reports and backup paths;
- no automatic retry or hidden continuation.

## CLI acceptance tests

Build the actual `skillet` binary and run it with a temporary `HOME` and agent-home environment variables. For each
agent:

1. Create `$HOME/.config/skillet.yaml`, selecting that agent and containing all three resource classes and minimal valid
   native bundles.
2. From a working directory without `skillet.yaml`, run `skillet`; verify that it loads the default manifest, produces
   the semantic diff, makes zero filesystem mutations, and exits 0.
3. Run `skillet apply`; verify exit 0 and decode every expected native target or verify the expected management command
   and postcondition.
4. Run Plan again; verify no changes.
5. Add a conflict and verify both commands exit 2 and Apply performs no mutations.
6. Introduce malformed native JSON/TOML and verify exit 1 with the native path in the error.
7. Verify an unrelated project directory and its `AGENTS.md` remain untouched.

Also verify `--file <path>` loads that manifest instead of the default and anchors local sources to its directory,
`target.path` takes precedence over the agent-home environment variable, missing or unknown target agents and extra
arguments exit 2, MCP secrets never appear in captured output or forwarded command diagnostics, and partial apply
failures include their report.

CLI wiring tests add Git-sourced skills and plugins through local repositories and URL rewrites for every agent. Verify
that Plan makes
no filesystem changes, Apply acquires before the lock and fetches only once, and
the bytes staged for installation are the bytes used for the rendered plan. A repeated invocation serving the same
archive must report no changes. Local and remote forms of every skill and plugin adapter contract case must pass.

Direct-file acceptance tests require no agent binaries. CLI-backed adapter tests use a hermetic fake executable with
recorded arguments and programmable output, side effects, and failures; real-agent integration tests are optional,
explicitly enabled, and pinned to supported releases. No test requires external network access or credentials, and no
test executes installed resource content.

## Completion checks

Before implementation is considered complete:

- `go test ./...` passes with the race detector enabled.
- All three adapter contract suites and all CLI acceptance scenarios pass.
- `go vet ./...` and the project's formatting check pass.
- A repeated apply is idempotent for every resource class and agent.
- Tests create artifacts only inside their temporary directories; repository test runs leave no generated files behind.

## Implemented plugin checks

- Local and Git plugin acquisition, native identity validation for all selected targets, and independent skill names.
- Plan/apply/plan, enable-only updates, complete bundle replacement, and preservation of unrelated native settings.
- Native registry and bundle drift, duplicate or external registrations, missing bundles, and symlinked ancestors.
- Plugin preflight blocks skill writes; failed plugin verification restores its bundles and native registry bytes.
- Plugin-only CLI plans and reports use the `plugins/` resource prefix.
