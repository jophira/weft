# weft

Composable AI rules manager — manage, layer, and sync AI rule sources across teams and harnesses.

Maintain separate rule repositories (personal, team, company) and compose them into a single layered profile applied to whichever AI coding tool you're using. Weft normalises across harnesses automatically — the same source writes `CLAUDE.md` for Claude Code, `AGENTS.md` for Codex, `GEMINI.md` for Gemini CLI, and a `.mdc` rule for Cursor.

Sources can use a flat `CLAUDE.md` or a full domain hierarchy (`Backend/BACKEND.md`, `Backend/Java/JAVA.md`, …). Set `instruction_glob: "**/*.md"` in the source config and Weft assembles every matching file — parent directories before children — before merging and applying.

## Install

**macOS / Linux — Homebrew**
```bash
brew install jophira/tap/weft
```

**Linux / macOS — binary**
```bash
curl -sSL https://github.com/jophira/weft/releases/latest/download/weft_linux_amd64.tar.gz | tar xz
sudo mv weft /usr/local/bin/
```

Replace `linux_amd64` with your platform: `linux_arm64`, `darwin_amd64`, `darwin_arm64`.

**Windows**

Download `weft_windows_amd64.zip` from the [releases page](https://github.com/jophira/weft/releases/latest), extract, and add to your `PATH`.

**Build from source** (requires Go 1.24+)
```bash
git clone https://github.com/jophira/weft.git && cd weft
make build        # binary at ./bin/weft
```

## Quick start

```bash
# Register a rule source (local path + git remote)
weft source add work ~/.rules/work git@github.com:you/work-rules.git

# Pull latest from all remotes
weft source sync

# Combine sources into a named profile
weft profile create hybrid --sources personal,work

# Activate the profile (merges sources, writes to harness config)
weft profile use hybrid

# Keep the profile live — re-applies automatically when any source file changes
weft profile use hybrid --watch

# Apply to a specific harness
weft target apply claude-code

# Verify everything is configured correctly
weft doctor
```

## Commands

| Command | Description |
|---|---|
| `source add/list/sync/push/status/remove` | Manage rule sources |
| `profile create/list/use/diff/delete` | Manage named profiles (`use --watch` for live reload) |
| `target list/apply` | Manage AI harness targets |
| `hook add/list/run/remove` | Manage lifecycle hooks |
| `doctor` | Health check — shows discovered harnesses and config issues |
| `version` | Print version, commit, and build date |

## Supported harnesses

| Harness | Written as |
|---|---|
| Claude Code | `~/.claude/CLAUDE.md` |
| OpenAI Codex | `~/.codex/AGENTS.md` |
| Cursor | `~/.cursor/rules/weft.mdc` |
| Windsurf | `~/.codeium/windsurf/global_rules.md` |
| Gemini CLI | `~/.gemini/GEMINI.md` |
| Warp | `~/.warp/workflows/*.yaml` |
| Aider | `~/.aider/CONVENTIONS.md` |

Additional harnesses (Goose, OpenCode, Hermes, Antigravity) are supported via
plain directory copy. New harnesses can be added to `~/.config/weft/harnesses.yaml`
without recompiling.

## Configuration

Config file: `~/.config/weft/config.yaml`

Override with `--config <path>` on any command.

## License

MIT
