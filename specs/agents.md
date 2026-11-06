# Agents

Codex, Claude Code, and Kimi Code each have one package implementing all four resource interfaces.

## Execution strategy

Every resource adapter independently chooses direct native-file operations, the agent's CLI, or a hybrid. The choice can
differ by resource class, operation, and supported agent version. It is private to the adapter and is not exposed in the
skillet schema, engine, or command syntax.

- **Plan:** only non-mutating operations are allowed. An adapter may use a documented read-only CLI list or inspect
  command, but never an install, update, trust, reload, or resource-execution command. Plan makes no network requests,
  so a CLI query must be known to remain local. GitHub content has already been acquired by the skillet loader.
- **Apply:** an adapter may write native files directly, invoke a native management command, or combine both. It must
  identify affected targets, back them up, invoke commands without a shell, capture and redact output, and verify the
  resulting state through a fresh scan. GitHub snapshots are staged exactly like local bundle content and are never
  downloaded again by an adapter.
- **Selection:** direct writes are suitable for simple documented formats with deterministic replacement. Native
  commands are suitable when an agent owns a complex or unstable registry. Neither strategy is prescribed globally.

An adapter that cannot provide the common Plan/Apply guarantees through one strategy must use another or report a
conflict. It must not silently weaken those guarantees.

## Common discovery and scan scope

The manifest's `target.agent` selects one agent factory. Its optional `target.path` overrides that agent's home
directory; otherwise the factory resolves the process environment and default below, then passes an immutable target
struct to the constructor. A manifest path must be absolute or start with `~/`. Tests construct targets directly.

- Codex: `CODEX_HOME`, defaulting to `~/.codex`.
- Claude: `CLAUDE_CONFIG_DIR`, defaulting to `~/.claude`. User-scope MCP storage remains the separate `~/.claude.json`
  file.
- Kimi: `KIMI_CODE_HOME`, defaulting to `~/.kimi-code`.
- `~` and `HOME` are resolved through the OS user abstraction, not string interpolation in adapters.

Adapters scan writable user-scope sources needed for reconciliation. They do not merge project, admin, built-in, synced,
or plugin-provided catalogs into the result. This prevents a read-only or higher-precedence resource from being mistaken
for a writable standalone installation.

## Native targets

- **Skills**
  - Codex: `~/.agents/skills/<name>/`
  - Claude: `$CLAUDE_CONFIG_DIR/skills/<name>/`
  - Kimi: `$KIMI_CODE_HOME/skills/<name>/`
- **Plugins**
  - Codex: native local managed bundle and install/enabled registry under Codex home.
  - Claude: native local managed bundle and install/enabled registry under Claude config.
  - Kimi: `$KIMI_CODE_HOME/plugins/managed/<name>/` and `$KIMI_CODE_HOME/plugins/installed.json`.
- **MCPs**
  - Codex: `$CODEX_HOME/config.toml`
  - Claude: `~/.claude.json`, top-level `mcpServers`
  - Kimi: `$KIMI_CODE_HOME/mcp.json`, top-level `mcpServers`
- **Context**
  - Codex: `$CODEX_HOME/AGENTS.md`
  - Claude: `$CLAUDE_CONFIG_DIR/CLAUDE.md`
  - Kimi: `$KIMI_CODE_HOME/AGENTS.md`
- **Apply lock**
  - Codex: `$CODEX_HOME/.skillet/apply.lock`
  - Claude: `$CLAUDE_CONFIG_DIR/.skillet/apply.lock`
  - Kimi: `$KIMI_CODE_HOME/.skillet/apply.lock`
- **Backups**
  - Codex: `$CODEX_HOME/.skillet/backups/<run-id>/`
  - Claude: `$CLAUDE_CONFIG_DIR/.skillet/backups/<run-id>/`
  - Kimi: `$KIMI_CODE_HOME/.skillet/backups/<run-id>/`

Codex's shared skill location intentionally does not move with `CODEX_HOME`; this matches Codex's documented user-scope
discovery. Kimi uses its Kimi-specific skill directory rather than the shared `~/.agents/skills` location so relocating
`KIMI_CODE_HOME` isolates the whole Kimi environment.

## Skills

All adapters install directory-form bundles atomically and compare their deterministic content fingerprints.

- **Codex:** read the skill frontmatter but do not add `skills.config` entries. An existing disabled entry for the same
  skill path is a conflict because copying content alone would not make the requested skill available.
- **Claude:** reject the reserved name `synced` and any target inside `.trash` or another Claude-managed catalog. Do not
  modify sync settings or account-managed skills.
- **Kimi:** install only directory form, with `<name>/SKILL.md`. Do not create flat `.md` skills or alter
  `extra_skill_dirs`.

If an installed target is a symlink, non-directory, or contains symlinks/special files, planning reports a conflict
rather than following it. Updating a declared skill replaces the whole named bundle after backup, including removal of
stale files within that bundle.

## Plugins

Plugin adapters privately choose direct registry/bundle writes, native plugin management commands, or a hybrid. A native
command is preferred when the registry is undocumented or unstable; direct writes are suitable when the adapter can
preserve and back up the complete registry safely.

1. Validate the native manifest and require its name or ID to equal the skillet name.
2. Scan the managed bundle and installation/enabled state through native files or a read-only CLI query. If both are
   absent, the plugin is an addition. A bundle without a registry record, a registry record without a bundle, or
   multiple records for one ID is a conflict.
3. Classify the existing source. Only a native local installation is eligible for update. Marketplace, account-synced,
   enterprise-managed, and session-only plugins are conflicts.
4. Compare bundle content and enabled state. A local bundle with equal content but disabled state produces an update.
5. On apply, back up every affected target and install and enable the plugin through the adapter's chosen strategy.
   Direct writes stage the complete bundle and update the local-install record. CLI-backed writes use fixed arguments
   and no shell. Both strategies capture target digests and verify the result through a fresh scan.

Native manifest locations are adapter concerns:

- Codex: `.codex-plugin/plugin.json`.
- Claude: `.claude-plugin/plugin.json`.
- Kimi: `kimi.plugin.json`, with `.kimi-plugin/plugin.json` accepted as the documented alternative.

Direct registry codecs must preserve records and settings for other plugins and reject a schema they cannot represent.
CLI-backed adapters must identify the command's affected targets and report native side effects. Skillet never directly
runs a plugin installation hook or manifest-declared command.

## MCP conversion

### Codex

Stdio entries map to `[mcp_servers.<name>]` with `command`, `args`, `env`, and `cwd`. HTTP entries map to `url`,
`http_headers`, and `bearer_token_env_var`. The adapter removes obsolete transport keys if the transport changes and
preserves unrelated native MCP and top-level settings.

### Claude

Entries map into top-level `mcpServers` in `~/.claude.json`. Stdio uses the native command form. HTTP explicitly writes
`type: "http"` because Claude otherwise treats a URL entry as invalid. Headers map to `headers`; the bearer environment
reference maps to the native bearer-token environment field. Project-nested MCP declarations and enable/disable lists
are not modified.

### Kimi

Entries map into top-level `mcpServers` in `$KIMI_CODE_HOME/mcp.json`. Stdio uses `command`, `args`, `env`, and `cwd`.
HTTP uses `url`, `headers`, and `bearerTokenEnvVar`; no `transport` field is needed for HTTP. Other server options are
preserved.

For all agents, the scanner distinguishes missing portable fields from native fields outside Skillet's ownership.
Formatting, key order, and explicit empty defaults do not create changes. Existing malformed entries return an
operational parse error; valid entries with unsupported requested semantics produce conflicts.

## Context segments

All adapters use the marker algorithm in the schema document and preserve bytes outside managed segments.

- Codex writes `AGENTS.md`. If a non-empty `AGENTS.override.md` exists in Codex home, planning reports a conflict
  because Codex would read the override instead of the modified base file.
- Claude writes user-scope `CLAUDE.md`, not `AGENTS.md`; Claude's AGENTS.md discovery is project-scoped.
- Kimi writes its global `AGENTS.md` under `KIMI_CODE_HOME`, not the generic `~/.agents/AGENTS.md` file.

Missing instruction files are empty state. A target that is a symlink, directory, or non-regular file is a conflict.
Adapter-specific instruction size limits are validated during planning when known; exceeding one is a conflict rather
than a partially installed segment.

## Structured-file compatibility

Native JSON and TOML codecs decode into typed known fields plus preserved unknown fields. They must:

- reject duplicate keys where the native format disallows them;
- preserve unknown settings semantically;
- avoid coercing numbers, booleans, or strings into different types;
- distinguish a missing file from an empty or malformed file;
- include the native path and structural location in errors;
- write deterministic output and retain existing file permissions when replacing a file.

Semantic preservation does not guarantee retention of TOML/JSON whitespace or comments. Context files are handled
separately and retain all unmanaged bytes.

## Official format references

The initial mappings are based on these agent-owned references:

- Codex: [skills](https://developers.openai.com/codex/skills), [MCP](https://developers.openai.com/codex/mcp),
  [AGENTS.md](https://developers.openai.com/codex/guides/agents-md),
  [plugins](https://developers.openai.com/codex/plugins).
- Claude: [skills](https://code.claude.com/docs/en/skills), [MCP](https://code.claude.com/docs/en/mcp),
  [settings](https://code.claude.com/docs/en/settings), [plugins](https://code.claude.com/docs/en/plugins),
  [memory](https://code.claude.com/docs/en/memory).
- Kimi: [data locations](https://www.kimi.com/code/docs/en/kimi-code-cli/configuration/data-locations.html),
  [skills](https://www.kimi.com/code/docs/en/kimi-code-cli/customization/skills.html),
  [MCP](https://www.kimi.com/code/docs/en/kimi-code-cli/customization/mcp.html),
  [plugins](https://www.kimi.com/code/docs/en/kimi-code-cli/customization/plugins.html).

Adapter fixtures must pin the native shapes used by supported releases. A native format change requires an adapter
change and contract-test coverage, not a domain schema change by default.
