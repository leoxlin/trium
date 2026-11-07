# Reconciliation and failure contracts

## Preparation and preview

The CLI reads one manifest file. Parsing validates declarations without acquiring content. Preparation resolves target
homes, acquires immutable snapshots, and validates their contents for every selected agent.

Source acquisition may use the network and temporary storage. It completes before the apply lock or any native target
scan. Planning and applying never reacquire sources. Preview does not mutate agent state, acquire a lock, or create
backups. Output is deterministic by target order, resource class, and resource name.

## Apply

1. Acquire the per-user advisory lock at `<user-home>/.skillet/apply.lock`.
2. For each target, compute a fresh private plan from the prepared snapshots and current native state.
3. If planning fails or any class has conflicts, stop before resource writes for that target.
4. Apply nonempty batches in skills/plugins order.
5. Return completed changes, backup paths, and warnings, including partial results on failure.
6. Release the lock on every return path.

The Unix advisory lock is nonblocking. A held lock fails apply. Its file persists, while ownership ends when the
process closes its descriptor or exits. Preview does not interact with it. A shared user lock also covers Codex skills
used by different Codex homes. It coordinates Skillet processes using the same user home; external writers remain
outside that coordination.

## Write batches

A batch owns the complete read set that influenced its plan, including policy files it does not modify. It also owns
bundle and native-file replacements. An unchanged observed target can still invalidate a batch.

Before writing, the transaction implementation:

- Checks observations and target safety.
- Rejects unobserved, overlapping, and backup-overlapping write destinations.
- Stages all new content beside its destination and copies all originals into a unique backup directory.
- Records original existence and backup paths in `metadata.json`.
- Rechecks the read set before commitment.

Commit moves existing targets into temporary storage on their own filesystem, then renames staged replacements into
place. Backup copies can reside on a different filesystem. Registry modes are retained when replacing existing files;
new native files use mode `0600`. Bundle files use mode `0644`, or `0755` for executable files.

A fresh native plan verifies the completed resource class. Any remaining add, update, or conflict in that class fails
verification. Failed writes or verification restore the affected targets in reverse order. Cancellation stops further
work and restores an in-progress batch before returning. Recovery itself is not canceled.

Successful backups remain under `<agent-home>/.skillet/backups/run-*`. Incomplete restoration retains temporary recovery
files and names them in warnings. Stage-cleanup failures are also reported. Earlier successful resource classes and
targets are not rolled back.

These operations provide rollback for detected failures, not global atomicity or automatic process-crash recovery.
A failed operation can leave administrative directories and backups. Native agent processes do not honor Skillet's
lock; observation checks reduce drift risk without eliminating every race with external writers.

## Ownership and idempotence

Only declared resources are reconciled. A complete named bundle is replaced, so stale files inside it are removed.
Sibling bundles and unrelated configuration are preserved. Plugins from other sources or scopes conflict rather than
being taken over. Skills and plugins with the same name remain separate.

After a successful apply, another plan against the same prepared snapshots and unchanged native state reports no
changes. A second apply creates no new backups. Plugin runtime behavior and successful agent loading are outside this
installation guarantee; Skillet never runs plugin code or skill scripts.

## Exit status

| Status | Meaning |
| --- | --- |
| `0` | Conflict-free preview or verified apply, including no-op operations |
| `1` | Acquisition, native parsing, locking, drift, write, or other operational failure |
| `2` | Invalid invocation/declaration/acquired content, or a conflicted plan |

Normal plans and reports go to stdout. Blocking diagnostics go to stderr. Reports distinguish completed changes and
failed restoration; an error identifies the failed target and operation. A conflict on a later target does not undo
an earlier target's successful apply.
