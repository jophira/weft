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
# Register a rule source — remote is inferred from the repo's origin when omitted
weft source add work ~/.rules/work

# Specify the remote explicitly (or override an existing origin)
weft source add work ~/.rules/work --remote git@github.com:you/work-rules.git

# Register a source with a domain hierarchy (Backend/, Frontend/, …)
weft source add work-private ~/.rules/work-private \
  --instruction-glob "**/*.md"

# Pull latest from all remotes
weft source sync

# Combine sources into a named profile
weft profile create hybrid --sources personal,work

# Activate the profile — merges sources, applies to all harnesses, and watches for changes
weft profile use hybrid

# One-shot apply only (no watch — useful in CI/scripts)
weft profile use hybrid --no-watch

# Apply to a specific harness
weft target apply claude-code

# Verify everything is configured correctly
weft doctor
```

## Safe apply — manifest, write-back and backups

Weft keeps a manifest (`~/.config/weft/manifests/<harness>.json`) recording the sha256 hash of
every file it wrote. On startup, before applying, it checks each managed file:

- **File not on disk** — written silently (`✓ wrote`).
- **File unchanged** (hash matches manifest) — skipped (`· skip`).
- **File externally modified** — written back to its source repo first, then apply is a no-op.
- **Unresolvable file** (no owning source) — backed up to
  `~/.config/weft/backups/<harness>/<timestamp>/` with a warning; apply skips it.

Files weft has never touched (e.g. `~/.claude/projects/`) are never modified.

```
[weft] startup write-back: CLAUDE.md → ai-rules-personal-tech
Applying to claude-code...
  · skip   CLAUDE.md
  ✓ wrote  commands/backend/java.md
```

To restore a backup (last-resort cases only):

```bash
weft target revert claude-code                    # restore latest backup
weft target revert claude-code --backup 20260605-143022  # restore a specific one
weft target backups claude-code                   # list all available backups
```

## Commands

| Command | Description |
|---|---|
| `source add <name> <path>` | Register a source; remote inferred from repo origin or set with `--remote` |
| `source list/status/remove` | List, inspect git state, or deregister sources |
| `source sync [name]` | Pull latest from remotes (auto-synced in background; use to force immediately) |
| `source push <name>` | Push commits; aborts if working tree is dirty — use `-m` to commit first |
| `source push <name> -m "msg"` | Stage all changes, commit with message, then push |
| `profile create/list/use/diff/inspect/delete` | Manage named profiles; `--overlay`, `--target`, and `--sources` are validated on create |
| `profile use <name>` | Activate profile: merge sources, apply to all targets, and watch for changes (use `--no-watch` to apply once and exit) |
| `target list/apply/backups/revert` | Manage AI harness targets; inspect and restore backups |
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
