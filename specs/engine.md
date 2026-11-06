# Engine contracts

## Command shape

```text
skillet [--file <path>]
skillet [--file <path>] apply
```

The manifest's required `target.agent` selects exactly one of `codex`, `claude`, or `kimi`. When `--file` is omitted,
the CLI loads `~/.config/skillet.yaml`; it does not fall back to `skillet.yaml` in the current working directory. When
`--file` is provided, the CLI loads exactly that YAML path instead. For either form, local source paths are anchored to
the selected manifest's directory; GitHub paths remain relative to their repository.

The first form is Plan. The second is Apply. Version 1 has no separate `plan` subcommand, saved-plan argument, prune
command, agent argument, or agent auto-detection. Extra positional arguments are invocation errors.

## Plan

Plan loads and validates the entire manifest, acquires each declared GitHub skill or plugin once into memory, discovers
the selected agent's user-scope targets, and produces one semantic diff. It does not create missing configuration,
create the `.skillet` directory, acquire an apply lock, write backups or caches, invoke a mutating command, or execute
resource content. Only manifest loading may use the network, and only when GitHub sources are declared. An adapter may
invoke a documented local, read-only agent CLI query when that is its chosen scan strategy.

### Change meanings

| Action     | Meaning                                                                                                               |
| ---------- | --------------------------------------------------------------------------------------------------------------------- |
| `ADD`      | No current resource with this key exists in the managed target                                                        |
| `UPDATE`   | The key exists but a managed semantic field or bundle fingerprint differs                                             |
| `CONFLICT` | Skillet cannot safely reconcile the requested resource                                                                |
| unchanged  | Included in the summary; unchanged rows are omitted by default                                                        |

There is no `DELETE`. Resources absent from the manifest do not appear in the diff.

### Output

Output is deterministic, plain text, and grouped in skills, plugins, MCPs, context order. Entries are sorted by name
within each group. Paths are absolute after target discovery. Colors, if added later, must not change content and must
be disabled for non-terminal output.

Example shape:

```text
Agent: codex
Manifest: /work/team/skillet.yaml

skills/review-pr          UPDATE  content differs
plugins/team-workflows    ADD     install and enable local plugin
mcps/context7             UPDATE  args changed
mcps/internal-docs        UPDATE  headers changed (values redacted)
context/working-agreements UPDATE /home/me/.codex/AGENTS.md
plugins/legacy            CONFLICT marketplace installation is externally managed

Summary: 1 add, 4 update, 4 unchanged, 1 conflict
Plan only; no changes applied.
```

The summary counts are illustrative and must be computed from actual rows. Context additions and updates include a
unified text diff of the managed segment. MCP changes display field-level before/after values. Skill and plugin changes
display fingerprints and source descriptions. A GitHub description includes repository, bundle path, requested ref, and
resolved commit; a location-only change with equal content remains unchanged. Verbose per-file diffs are outside
version 1.

Values of MCP `env` and `headers` are always rendered as `<redacted>`. Their key names and changed/unchanged state
remain visible. No credential file or bearer environment variable is read to produce a diff.

An empty diff prints `No changes` and the unchanged count. Conflicts are always printed, including when other entries
are unchanged.

## Apply

Apply is not Plan with a writing flag bolted onto a stale result. It performs the following sequence:

1. Load and validate the manifest, acquire each GitHub source once, and compute source fingerprints.
2. Acquire the selected agent's apply lock.
3. Re-scan native state and compute a fresh plan while holding the lock, retaining the source snapshots from step 1.
4. Render that plan.
5. If any conflict exists, release the lock and make no mutations.
6. Preflight each non-empty change set and verify all captured target digests.
7. Apply classes in skills, plugins, MCPs, context order, skipping empty classes.
8. Render applied items, backups, warnings, and any partial failure.
9. Release the lock.

The lock is an exclusive create-lock-file operation containing PID and process start information. A live holder causes
an operational error. A stale lock may be reclaimed only after verifying that its process no longer exists. Plan never
reclaims or creates a lock.

The lock prevents concurrent Skillet applies to one agent. Target digest checks detect unrelated native edits between
planning and commitment. Different agent targets have independent locks, except that shared files such as Codex's
`~/.agents/skills` are not protected against non-Skillet writers; digests remain the final guard.

## Write and recovery guarantees

Each resource adapter privately chooses direct writes, native management commands, or a hybrid. All strategies must:

- Decode and validate every target in the change set before the first mutation for that resource class.
- Verify captured digests immediately before commitment.
- Copy every affected original target into the run backup directory before mutation. Missing targets are recorded as
  absent in backup metadata.
- Verify the resulting semantic state through a fresh scan before reporting success.

Direct file operations additionally:

- Stage new file content in the same filesystem as its destination.
- Replace individual files with rename. Replace bundles by renaming the old bundle to backup and the staged bundle into
  place.
- For a plugin, treat bundle and registry as one logical write and restore both if the second replacement fails.
- Preserve existing file mode where practical; use `0600` for new JSON/TOML state and `0700` for `.skillet` directories.
  Context files use `0644` when newly created.

Native management commands additionally:

- Use a fixed executable and argument vector without a shell; manifest strings are never interpolated into a shell
  command.
- Capture stdout and stderr, redact sensitive values, and convert command failures into operational errors with adapter
  context.
- Declare and back up every target the command can modify. If that cannot be determined safely, the command is not a
  valid Apply strategy.
- Report native side effects and restore affected targets from backup where possible after failure or postcondition
  mismatch.

Direct-file atomicity is one file replacement where supported by the OS. A native command has only the atomicity it
provides. A resource class can contain several mutations, and an apply contains several resource classes; neither is
globally atomic.

If a resource fails, its adapter attempts to restore that resource's already-changed targets from backup. The engine
stops subsequent classes and returns all previously successful reports. It does not silently roll back earlier resource
classes. The error output must say exactly which classes succeeded, which failed, whether restoration succeeded, which
native side effects could not be reversed, and where backups are stored.

A successful apply retains its backup directory. Version 1 does not automatically delete backups or provide a restore
command.

## Post-apply result

A successful apply means the requested mutations completed and passed a fresh verification scan; it does not prove that
an agent loaded them or that external resource executables work.

Warnings identify required native follow-up when known:

- Plugins and some MCP changes can require reload or a new agent session.
- Skillet does not validate MCP connectivity, credentials, or plugin runtime compatibility.

A second plan immediately after a successful apply must report no changes for the same manifest and unchanged native
state, except for explicitly reported external normalization conflicts. Such a conflict is a bug in semantic
canonicalization, not an expected warning.

## Exit status

- **Status `0`:** Plan produced a valid, conflict-free plan with or without changes. Apply completed all requested
  changes or had nothing to do.
- **Status `1`:** Plan hit a native scan, permission, I/O, or internal failure. Apply hit a lock, drift, write, or
  other operational failure; partial changes are reported if present.
- **Status `2`:** Invocation or the manifest was invalid, or the plan had a conflict. Apply makes no mutations for an
  invalid invocation, manifest, or conflicted fresh plan.

The CLI prints validation and conflict diagnostics to stderr when they prevent success. Normal diffs and apply reports
go to stdout. Operational errors go to stderr, while any partial apply report still goes to stdout.

## Cancellation

Context cancellation stops further work at the next filesystem or orchestration checkpoint. An adapter must finish or
restore an in-progress replacement before returning; cancellation must not intentionally leave a half-written JSON/TOML
file. The apply report distinguishes completed resources from unattempted resources.
