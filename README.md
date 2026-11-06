# skillets

<div align="center">
  <img src="skillets.png" alt="skillets" width="250">
  <br>
  An easy way to manage my agent skills — and more.
</div>

## What's inside

A skillet manifest reconciles skills, plugins, MCP servers, and context segments into one agent environment. See
[`skillet.example.yaml`](skillet.example.yaml) for a complete example and
[`skillet.schema.yaml`](skillet.schema.yaml) for the full format.

Each manifest must select one agent:

```yaml
version: 1
target:
  agent: codex # codex, claude, or kimi
```

When `target.path` is omitted, the agent's environment variable and default location are used:

| Agent  | Environment variable | Default       |
| ------ | -------------------- | ------------- |
| Codex  | `CODEX_HOME`         | `~/.codex`    |
| Claude | `CLAUDE_CONFIG_DIR`  | `~/.claude`   |
| Kimi   | `KIMI_CODE_HOME`     | `~/.kimi-code` |

Set `target.path` to override both. The path must be absolute or start with `~/`:

```yaml
target:
  agent: claude
  path: ~/.claude-team
```

Preview the changes in `skillet.yaml`, or apply them:

```bash
skillet
skillet apply
```

Use `--file <path>` to select a different manifest.

## License

[MIT](LICENSE)
