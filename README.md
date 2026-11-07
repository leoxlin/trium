# skillets

<div align="center">
  <img src="skillets.png" alt="skillets" width="250">
  <br>
  An easy way to manage my agent skills — and more.
</div>

## Tracer bullet

The current implementation reconciles local directory-form skills for Codex and Kimi Code. Claude, plugins, MCP
servers, and GitHub sources remain in the design but are not implemented.

Build the CLI:

```bash
go build ./cmd/skillet
```

Create `~/.config/skillet.yaml`:

```yaml
version: 1
target:
  agent: codex # codex or kimi
skills:
  - name: code-review
    source: skills/code-review
```

The source is relative to the manifest and must contain `SKILL.md` with matching `name` and a non-empty `description`
in its YAML frontmatter. Codex installs into `~/.agents/skills`; Kimi installs into `$KIMI_CODE_HOME/skills`.

When `target.path` is omitted, the agent's environment variable and default home are used:

| Agent | Environment variable | Default        |
| ----- | -------------------- | -------------- |
| Codex | `CODEX_HOME`         | `~/.codex`     |
| Kimi  | `KIMI_CODE_HOME`     | `~/.kimi-code` |

Set `target.path` to an absolute path or one starting with `~/` to override the agent home. Codex's shared skill
location remains under the real home directory.

Preview or apply the default manifest, or select another file:

```bash
skillet
skillet apply
skillet --file /path/to/skillet.yaml
```

Planning makes no filesystem changes. Applying takes a lock, rechecks the plan, retains backups under the selected
agent's `.skillet/backups` directory, and replaces only declared skill bundles. Undeclared skills are left alone.

The planned full manifest format is documented in [`skillet.schema.yaml`](skillet.schema.yaml).

## License

[MIT](LICENSE)
