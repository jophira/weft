# Changelog

All notable changes to Weft are documented here. Format follows
[Keep a Changelog](https://keepachangelog.com/); this project is pre-1.0 and the
CLI surface may still change.

Each released section is what the GitHub release notes for that tag are built
from, so keep entries written for someone deciding whether to upgrade.

## [Unreleased]

### Added

- **Rules can load only in interactive sessions** (#322). A rule's `detect`
  predicate can read `interactive`, which is false when `CONDUCTOR_RUN_ID` is
  set. A rule with `detect: interactive` is left out of headless slick-conductor
  steps, so they stop paying for rules written for a person watching.

### Security

- **Conflict backups and the merge work file are written owner-only** (#327).
  Write-back and resolve backups, the `--take merge` work file and the staged
  instruction copies now go through the same atomic owner-only writer as the
  rest of `~/.config/weft`, instead of `0644` files in `0755` directories.

## [0.3.0] - 2026-10-17

Weft now knows which repositories you work in and delivers project rules into
them. Conflicting edits can be merged rather than only picked. Five security
fixes harden self-update, path handling and private state. Read **Upgrading**
before you install: some inputs that used to be accepted are now refused, and
one change asks you to run a command once.

### Upgrading

- **`instruction_glob` patterns that name a directory are refused** (#284). A
  pattern such as `docs/**/*.md` was silently read as `*.md` across the whole
  tree. It now fails with an error, and so does an exact nested path such as
  `docs/CLAUDE.md`. Use `**/*.md` together with `--instruction-exclude` to
  leave directories out.
- **User-defined harnesses in `harnesses.yaml` must use paths relative to
  `$HOME`** (#279). An absolute `config_dir` or `detect_path`, a `..`
  component, a missing or duplicate name, or an entry with neither
  `config_dir` nor `detect_path` is now a load error.
- **Staged instruction copies are read-only** (#261). The per-source copies
  under `~/.config/weft/profiles/<profile>/instructions/` are written `0444`
  with a generated-file header. Edit the source, or the harness file, instead.
- **Source, profile and hook names are checked on every operation** (#277).
  The rule is the one `create` always applied: a lowercase letter first, then
  lowercase letters, digits, `-` or `_`. A YAML file you named by hand outside
  that rule can no longer be read or removed by name. Rename it.
- **Run `weft doctor --fix` once after upgrading if you use project sync**
  (#313). `GEMINI.md` files created by earlier versions are untracked and not
  excluded, so sync now leaves them alone. `--fix` deletes the ones holding only
  weft's block, and the next sync recreates and excludes them. Weft also stops
  editing a `GEMINI.md` it did not create until you run
  `weft config set project-write gemini-cli true`.
- **`~/.config/weft` becomes owner-only** (#280). The directory is tightened to
  `0700` the next time weft saves its config, which puts everything under it out
  of reach of other accounts. Run `weft profile use <name> --no-watch` once, or
  `chmod 700 ~/.config/weft`, to do it straight away.

### Security

- **`weft update` verifies the download before replacing the binary** (#276,
  PR #296). The archive must match its sha256 in the release's
  `checksums.txt`, or the update stops. Downloads have a 5-minute timeout where
  before they had none. This guards against a corrupted or swapped asset, not
  against a compromised release.
- **Harness projection cannot be redirected by a symlink** (#278). A symlink
  inside a harness tree no longer sends writes, backups or pruning outside the
  managed root.
- **Paths in user-defined harnesses are validated on load** (#279). See
  Upgrading.
- **Source, profile and hook names are validated on every read, write and
  delete** (#277). A name with path separators or `..`, including one passed
  through the MCP server, can no longer reach outside weft's storage
  directories.
- **Private state is written atomically and owner-only** (#280). The config,
  manifests, apply-time backups, the audit log and caches are written `0600`
  through a temp file, fsync and rename, so a crash cannot leave a half-written
  manifest that weft would trust on the next run. Write-back and resolve backups
  and the merge work file still use ordinary modes inside the now owner-only
  directory (#327).

### Added

- **Project registry and per-repository delivery** (#263, PR #267). Every weft
  command run inside a repository records it in
  `~/.config/weft/projects.yaml`, and dead or stale entries are pruned on the
  way. Rules then reach those repositories by the cheapest mechanism each
  harness supports: a `SessionStart` hook in a gitignored settings file for
  Claude Code, one import line for Gemini CLI, and inline content only for a
  harness you opt in. What weft writes is added to `.git/info/exclude`, so
  `git status` stays clean. A project's own `CLAUDE.md`, `AGENTS.md`,
  `GEMINI.md` and Cursor rules are read as input, so a rule written for one
  harness reaches the others in that repository. New commands are
  `weft project list`, `weft project status` and `weft project forget`.
- **`weft resolve --take merge`** (#258). It runs a three-way merge of the
  diverged copies against what weft last projected, and opens the result in
  `$EDITOR`. The saved file is the resolution, and quitting without saving
  leaves the conflict held. Conflict markers never reach a harness file. It
  needs a terminal, so `--take <harness>` stays the scripted option.
- **`weft resolve` with no arguments walks every held conflict** (#260). For
  each one it offers merge, a take per harness, a diff against the base, skip
  and quit. Without a terminal it reports what is held and exits non-zero.
- **`weft target list --files`** (#264, PR #269). It shows, per detected
  harness, what weft manages, what it recognises and leaves alone, and a count
  of what it does not recognise (`--all` names them). Credential files are
  never named. `weft project status` answers the same for the current
  repository.
- **`harness_home` and `WEFT_HARNESS_HOME`** (#265, PR #268). Both redirect
  every harness path, so an apply under a throwaway `--config` no longer writes
  to your real `~/.claude`, `~/.codex` and the rest. A `--config` run without
  it now says that harness writes still go to your real home.
- **Structured logging with `--log-level` and `WEFT_LOG_LEVEL`** (#262, PR
  #266). Each run gets an id, and applies, resolves, write-backs and conflicts
  are logged as events with durations. Five log generations are kept instead
  of one. Hints now carry stable codes (`W001`, `W301`, ...), print at most
  three per run on stderr, and are throttled per code.
- **A source can own Claude Code `settings.json` keys** (#307, PR #310). Put the keys in
  `harness/claude-code/settings.json` in a source and every apply sets them in
  `~/.claude/settings.json`, leaving every other key alone. `weft adopt claude-code
  settings.json --into <source>` brings an existing file under a source.

### Fixed

- **Project sync no longer leaves an untracked `GEMINI.md` in your repositories**
  (#313, PR #315). A `GEMINI.md` that weft creates is now added to `.git/info/exclude` at
  once, so it never appears in `git status`. Weft also stops editing a
  `GEMINI.md` that already exists and that it did not create, until you run
  `weft config set project-write gemini-cli true`. Codex and Cursor files are
  excluded the same way when you have enabled them. See Upgrading for the
  one-off `weft doctor --fix`.

- **Projected scripts keep their execute bit** (#309, PR #310). A skill script that is
  executable in its source now arrives executable in the harness, whether it is
  new or its source gained the bit after an earlier apply. Before, it arrived as
  `-rw-r--r--` and failed with `Permission denied` when run by path.

- **Project sync works in a git submodule** (#314, PR #317). Weft now reads and writes the
  submodule's own `info/exclude` and `config`, where it used to touch the
  superproject's, so a file weft created in a submodule is excluded there and its
  `origin` remote is found.

- **Text you write in `GEMINI.md`, `AGENTS.md` or a Cursor rule now reaches the
  other harnesses** (#316, PR #317). Anything outside weft's block in a file weft delivers
  to is read as project input, like text in `CLAUDE.md` already was. Before, weft
  skipped those files whole, so your own lines there never fanned out.

- **Edits Claude Code makes to your `settings.json` now reach your source**
  (#312, PR #317). Adding a rule in `/permissions` to a key a source owns is written back
  to that source's `harness/claude-code/settings.json` at the next apply instead
  of being reverted. If both the source and Claude Code changed the same key,
  weft keeps both values and warns (W205) until you make them match. A `/config`
  change to a key no source owns no longer triggers a "changed outside weft"
  backup. Replacing a value edits only that value in your file; removing a key
  rewrites the file with 2-space indentation. A settings file that has lost
  every key weft wrote (deleted, reset or emptied) is treated as a reset and
  restored from the sources, never written back as deletions.

- **An empty file in a higher-priority source no longer wipes a populated one**
  (#287, PR #289). An empty `CLAUDE.md` or `commands/*.md` in the winning
  source used to replace the lower source's content with an empty block, and
  `weft profile inspect` reported the opposite of what apply did. Empty
  contributions are now skipped.

- **Edits to a harness's instruction block survive watch-mode re-applies**
  (#286). With the watcher running, an edit to weft's block in a single-file
  harness (`AGENTS.md`, `GEMINI.md`, a Cursor rule) was overwritten on the next
  source change, with no write-back and no backup. It is now written back first.

- **Two harnesses editing the same source's instruction section are held as a
  conflict** (#257, PR #273). The second write-back used to discard the first
  silently. Weft now holds that source until you run `weft resolve`. A block
  ending in a newline is also no longer reported as externally edited on every
  apply.

- **Write-back keeps sources portable** (#259, PR #274). An edited line holding
  an expanded `{{weft.root}}`, `{{weft.source:NAME}}`, `{{weft.home}}` or
  `{{weft.docs}}` path is collapsed back to its anchor before it lands in the
  source. `weft resolve` does the same (#285), where before it wrote the
  harness's absolute paths straight into the source.

- **A live watcher in another PID namespace is no longer read as dead** (#300,
  PR #301). Run from a sandbox or container, `weft status` used to report the
  watcher as stopped and delete its state file.

### Changed

- **Auto-sync runs at most 4 sources at a time, each with a 2-minute timeout**
  (#281). A large registry used to start every due sync at once and could run
  out of file descriptors or SSH-agent capacity. One hung remote no longer
  stalls the rest.
- **Built with Go 1.26.9** (#311, PR #310), with `golang.org/x/net` v0.60.0 for a
  `govulncheck` finding. Building from source needs Go 1.26.9 or later.
  Dependency bumps: `mcp-go` 0.57.0 to 1.1.1, `go-git` 5.19.3, `doublestar`
  4.10.2, `x/crypto` 0.57.0, `x/sys` 0.48.0.

### Docs

- **Examples use `~/weft/sources/<name>`** (#323, PR #326), the layout weft
  recommends. The per-project rules section is marked deprecated, and the
  binary install link now points at the releases repository.
- **The `weft-releases` README is published from this repository on every
  release** (#324, PR #326).
- **The v0.0.9 and v0.1.0 changelog sections are complete** (#255, PR #256).
- The documented Go version matches `go.mod` (#282), and `weft_source_push`
  documents its required `confirm` flag (#283).

## [0.2.1] - 2026-08-15

**No code change since v0.2.0.** The binaries are the same build; there is
nothing here to upgrade for. This release exists to put the new release-notes
path through a real tag.

### Docs

- **The changelog moved into the repository** (#252, PR #253). It lived outside
  the repo until now, so a public repo shipped without one and the release notes
  were generated from commit subjects, which drops `docs:`, `test:` and `chore:`
  work and cannot carry a reason or a migration note.

  The history was reconciled into dated `[0.0.9]`, `[0.1.0]` and `[0.2.0]`
  sections, resolved with `git tag --contains` on each entry's merge commit
  rather than by eye, and the entries that had never been written were added.

  From this release on, the notes you are reading are the section of
  `CHANGELOG.md` matching the tag. `scripts/release-notes.sh` extracts it and
  the release workflow hands it to GoReleaser, so the file and this page cannot
  disagree. A missing or empty section fails the release rather than publishing
  a blank page.

  `CONTRIBUTING.md` covers what to write and where. The v0.2.0 notes were
  backfilled from the same file.

## [0.2.0] - 2026-08-15

### Added — conflict detection and `weft resolve` (ADR 0004 D5, PR #247)

- **Two harnesses can no longer overwrite each other's edits** (#213). Write-back
  pushes an edit made inside one harness to the owning source, and the next apply
  fans it out to the rest. That is safe while one copy has moved. When two have
  both changed since the last apply, whichever write-back visited last used to
  win, and the other edit was gone with nothing to recover it from.

  Weft now compares each harness's copy against the hash its manifest recorded at
  the last apply, routing through the class model so `~/.codex/prompts/review.md`
  and `~/.claude/commands/review.md` compare as one canonical file. Two or more
  diverged copies is a conflict: the apply, the startup write-back and the target
  watcher all hold the file, with no write, no backup and no manifest change, so
  the conflict survives the apply that held it.

  ```
  ! conflict: commands/review.md changed in claude-code and codex since 14:02
    → weft resolve commands/review.md --take claude-code|codex
  ```

  `weft resolve <path> --take <harness>` settles one. Losing copies are backed up
  under `~/.config/weft/backups/resolve/<ts>/<harness>/` before being rewritten
  from the winner, every manifest records the agreed hash, and the owning source
  is updated so the next apply keeps the decision. Nothing is deleted.

  `--take` was added to the existing `resolve` command rather than a new one,
  since the ADR's syntax collides with the reverse-lookup form already there.
  Without the flag, behaviour is unchanged.

  There is no `--take merge`. The ADR names it in the example message but
  specifies no algorithm, and guessing at how two prose files combine would fail
  silently, which is the failure this feature exists to prevent.

  Detection covers `commands`, `agents` and `skills`. `instructions` and `mcp`
  are excluded because neither travels as a copied file, so whole-file hashing
  would report a conflict on every apply for content weft never claimed. MCP is
  projection-only today, so that exclusion costs nothing; the instruction case is
  written up in
  `conflict-detection-class-coverage.md`
  and still needs a decision.

### Added — round-trip property tests (ADR 0004 D6, PR #247)

- **Every (class, harness) transform is now pinned by `toCanonical(toNative(x))
  == x`** (#213). The MCP dialects landed with #216 and #219; this covers the
  outstanding pairs. The managed-block envelope is tested on its own, then every
  Tier B harness through the real `ProjectInstruction` path with the harness set
  derived from adapters declaring `StrategyInline` rather than a hard-coded list,
  then Cursor `.mdc` separately for the frontmatter that sits outside the block.
  Eight input shapes each. Every pair round-trips, so no class declaration needed
  downgrading to unsupported.

### Added — adoptable and conflict counts on the status line (ADR 0004)

- **`weft status --short` carries two more numbers** (#213):

  ```
  weft: hybrid · 3 harness · drift:1 · adopt:18 · conflict:2 · watch:on
  ```

  `adopt` is what `weft adopt --scan` would list, `conflict` the number of files
  being held. Apply already computes both, so it records them to a `counts.json`
  sidecar in `internal/runstate` and the status read takes them from there. A
  status line renders once per turn, and recomputing would put a walk of every
  harness root in front of every prompt.

  The cache is deliberately independent of the watcher's own record: applying
  without a watcher running is the common case, and those counts are just as
  true. Counts are omitted when absent or over a day old, so "no scan has run"
  and "a scan found nothing" stay distinguishable. Claude Code reads this through
  its `statusLine` command; Codex has no per-turn shell hook, so it is a
  Claude-family affordance rather than a cross-harness one.

### Added — `weft target detect`

- **Detected harnesses can be written into a profile** (#236, PR #246). Profile
  targets had to be typed by hand, and detection results were discarded: the
  apply path probed every known harness but only when a profile listed no
  targets at all, so a profile carrying `active_target: claude-code` never
  detected anything and installing a second harness changed nothing.

  `weft target detect` lists every known harness, the signal it was found by
  (a config root or a binary on PATH, reported distinctly), and whether the
  active profile targets it. `--add` appends the newly detected names,
  migrating a legacy `active_target` into the `targets` list and keeping the
  existing value first. Nothing is added without the flag, following
  `weft adopt`.

  A target is never removed. A harness that stops being detected is reported
  and kept, since an uninstall on one machine must not silently stop projection
  on another. `weft profile use` now also prints a hint naming installed
  harnesses the profile does not target, suppressed on the watcher's quiet path
  so watch output is unchanged.

### Fixed

- **`weft update` queried a repository with no releases** (#250, PR #251).
  goreleaser publishes to `jophira/weft-releases`; the update path still named
  `jophira/weft`, which carries none. `releases/latest` returned 404, the check
  read that as nothing to report, and the notice never fired. `weft update`
  then 404'd on the download, and the Windows fallback message named an empty
  releases page. The owner and repo now live in `internal/update` with URL
  builders that `cmd/update.go` calls, rather than a second copy of the strings
  that let the two drift. All three URLs are pinned by tests, including the
  archive name against goreleaser's `name_template`.

- **opencode was never detected** (#237, PR #242). Detection matched on
  `$XDG_CONFIG_HOME/opencode`, a directory created on first run rather than at
  install time, so a machine that had installed opencode and not yet started it
  reported nothing. `~/.local/share/opencode` is added as a second candidate,
  behind the config root, which stays first because that is where weft writes.

  `detectSpec.binary` also widens to a slice, probed in order, with
  `DetectedVia` reporting the name that matched. A tool can rename its entry
  point or ship under two names during a transition, and a config root was
  previously the only way to cope. `harnesses.yaml` accepts `detect_binary` as
  a scalar or a sequence, so existing files parse unchanged.

- **Target watchers missed directories an apply created** (#230, PR #240). The
  watch scope was read from the manifest once, when the watchers started. An
  apply that projected the first file into a directory absent from that
  manifest left it unwatched, so external edits there were missed until a
  profile switch rebuilt the watchers. The per-target watchers now record the
  scope they were built from, and the source watcher rebuilds them after every
  apply, only when the scope actually widened.

- **MCP config never reached a harness** (#233, PR #238). Two independent
  defects. `ProjectMCP` looked the dialect up by harness name, and
  `GeminiCLI.Name()` returns `gemini-cli` while the dialect registered under
  `gemini`, so the lookup missed and took the no-support early return, emitting
  nothing and no error. Separately, `managedFilter` seeded its prefix set with
  `CLAUDE.md` and the managed directories only, so a source's root-level
  `mcp.yaml` never reached staging and every dialect rendered a native document
  with its MCP key dropped. Tests now walk the production path for every
  built-in harness.

- **aider was never told to read what weft wrote** (#232, PR #235). Weft wrote
  `~/.aider/CONVENTIONS.md` and nothing pointed aider at it, so an applied
  profile was inert while apply reported success. A new `Wirer` optional
  interface and a `ProjectWiring` apply step cover tools that load no
  well-known path, with aider the only implementation. The merge works on the
  YAML node tree rather than a map round-trip, so the user's other keys, key
  order and comments survive.

- **`weft doctor` named a path it had never checked** (#231, PR #234). Doctor
  printed a harness's config path whenever detection succeeded, whatever signal
  actually matched, and detection often succeeds on a binary found on PATH. Six
  hand-rolled detectors collapse into one shared implementation, which is why
  they had drifted. Adds the binary fallback claude-code, cursor and windsurf
  were missing, and points aider at `~/.aider`, which aider creates, rather
  than `~/.aider.conf.yml`, which it does not.

- **Watching a target root exhausted the watch budget** (#229). A harness
  target root is a live application home; `~/.claude` carries hundreds of
  directories of session state, plugin caches and per-project history, so
  watching it recursively overran the 500-directory ceiling and aborted
  `weft watch` outright. The scope is now built from the manifest: the root,
  every directory holding a weft-managed file, and the ancestors joining them.
  Directories created later inside a managed subtree are still followed; a new
  direct child of the root is treated as harness state and left alone.

### Changed

- **Go toolchain moves to 1.26.6** (#244, PR #245). govulncheck reported five
  standard library advisories against 1.26.5, all fixed in 1.26.6, and every CI
  job resolves its toolchain from `go.mod`.

- Dependency bumps: `mcp-go` 0.56.0 → 0.57.0 (#228), `cel-go` 0.29.2 → 0.31.0
  (#227), `go-git/v5` 5.19.1 → 5.19.2 (#226), `go-toml/v2` 2.2.4 → 2.4.3
  (#225), `testcontainers-go` 0.43.0 → 0.44.0 (#241).

### Docs

- **Community health files** (#221, PR #243). `CONTRIBUTING.md` covering local
  setup, the `make` targets, the three test tiers and the commit convention;
  `SECURITY.md` with GitHub private vulnerability reporting as the disclosure
  route; bug and feature issue forms and a pull request template under
  `.github/`.

- README gains sections on class-aware projection (what each harness receives
  per class, and `harness_sync` for narrowing it), on conflicts and
  `weft resolve`, and on the status line.

### Tests

- **The autostart hand-off flake is now measurable** (#224, PRs #239 and #249).
  A timeout dumps the watcher's process state, its own output, its log and the
  full status, rather than reporting only that a deadline passed. Measuring the
  CI history then showed windows-latest e2e wall time is bimodal, roughly 6s to
  11s against 26s to 34s with nothing between, so the deadline is scaled for
  Windows while the other platforms keep the tighter bound. The observed
  startup time is now logged on success, and CI runs the package with `-v`, so
  the deadline can be set from a distribution rather than a guess.

## [0.1.0] - 2026-07-21

### Fixed — files projected to paths the harness ignores (ADR 0004 D1, D2 and D9, PR #215)

- **Everything but the root instruction file landed in the wrong place**
  (#213). Projection routed files through a rename map keyed on the exact
  relative path, and the only entry any harness declared was the instruction
  file's. So a command staged as `commands/review.md` was written to
  `~/.codex/commands/review.md`, a directory Codex never reads, while Codex
  reads `prompts/`. Agents and skills went to directories no harness reads at
  all.

  The failure was invisible: weft logged those files as applied and tracked
  them in the manifest, so an apply reported success and the manifest agreed.
  Cursor was correct only because its adapter skipped everything but
  `CLAUDE.md` by hand.

  Routing is now by file class, with each harness declaring what it does with
  each one. A class with no native home is skipped and logged rather than
  written somewhere the tool ignores. Classes a harness cannot execute but can
  read on request are advertised instead: weft appends an index to the managed
  instruction block naming what exists and where.

### Added — canonical MCP config (ADR 0004 D4, PRs #216 and #219)

- **One `mcp.yaml` per source, rendered into each harness's own dialect**
  (#213). Weft keeps a canonical MCP document and emits the native form per
  harness: JSON under `mcpServers` for Claude Code and Cursor, TOML
  `[mcp_servers.*]` for Codex, and Gemini CLI's own `settings.json` shape.
  `~/.claude.json` holds unrelated local state, so the write is a keyed merge
  on the MCP entries alone rather than a whole-document rewrite.

  Secrets go in by reference only. A canonical `env` value may hold
  `${env:GITHUB_TOKEN}`; a literal high-entropy value is refused, since sources
  are ordinary git repos that get pushed. Dialect round-trips are pinned by
  property tests.

### Added — per-profile class participation (ADR 0004 D7, PR #217)

- **`harness_sync`** (#213). A profile can narrow what each harness receives,
  per class:

  ```yaml
  harness_sync:
    claude-code: [instructions, commands, agents, skills, mcp]
    codex:       [instructions, commands, mcp]
    cursor:      [instructions]
  ```

  A harness with no entry is unrestricted and gets every class it natively
  supports, so profiles written before `harness_sync` existed project exactly
  what they did before. An explicit empty list projects nothing. Classes
  withheld by config are reported separately from classes the harness cannot
  take, because the two have different fixes.

### Added — opt-in autostart (PR #214)

- **`weft autostart enable/disable/status`** (#212). Runs the watcher at login
  through a systemd user unit, a LaunchAgent or Task Scheduler, depending on
  the platform. `--profile` pins a profile rather than following the active
  one, and `--linger` keeps it alive without a login session. Opt-in, and
  stale-binary detection reports an installed unit pointing at a weft that has
  since moved.

### Added — explicit adoption of harness-native files (ADR 0004 D3)

- **`weft adopt`** (#213). Write-back only ever fired for manifest-owned files, so
  a command, agent or skill authored directly inside a harness belonged to no
  source and never reached the others. `weft adopt --scan` lists the files no
  manifest owns across every harness weft projects to, grouped by harness and
  class; `weft adopt <harness> <path>... --into <source>` copies them into a
  source at the class-correct path and records weft's ownership of the harness
  copy, so the next apply reports `· unchanged` instead of treating the file as
  an external edit.

  Adoption is explicit and one-way by design (once a source owns the file, weft
  overwrites it on every apply), so the guards are enforced in
  `internal/harness/adopt.go` rather than in the cobra handler: a plan is printed
  and confirmed before any write (`ErrConfirmRequired`; `--yes` to skip),
  `--into` is mandatory when the profile has more than one source, an existing
  destination is refused by name unless `--force`, paths that escape the harness
  root are rejected, and any file carrying what looks like a literal credential
  is refused outright via `mcpconfig.LooksSecret` — sources are pushable, so that
  guard is not bypassable by `--force`. Adoptable classes are a conservative
  allowlist (`commands`, `agents`, `skills`, markdown only); harness runtime
  state and weft's own instruction file are never offered. Native class
  directories are resolved from each harness's own `ClassSupport` declaration, so
  Codex `prompts/review.md` adopts into the source's `commands/`.

### Docs

- MIT `LICENSE`, README badges, and a corrected cask description.

### Tests

- **The CLI e2e suite no longer reports a cached pass.** It builds the weft
  binary and drives it through `exec`, so Go's cache key covers the test
  sources and their imports but cannot see through the subprocess. A change
  under `internal/` left those inputs untouched, so a restored cache reported
  `ok` for exactly the class of change the suite exists to catch. CI now runs
  it with `-count=1`.

## [0.0.9] - 2026-07-19

### Added — the two-home split and the work plane (ADR 0003)

- **`~/weft` workbench, `~/.config/weft` engine room** (#189, PR #190). Content
  you edit moves to a workbench at `~/weft`; weft's own machine state stays in
  the config dir. New `weft_home`, `docs_dir` and `audit_dir` config keys, and
  `{{weft.home}}` / `{{weft.docs}}` anchors alongside `{{weft.root}}`. `weft
  init` scaffolds the layout idempotently and `weft migrate` relocates an
  existing setup without destroying anything. A legacy fallback keeps
  pre-migration layouts working.

- **Work plane** (#191, PR #192). `weft rules resolve` appends
  `~/weft/work/projects/<repo>/kb/**/*.md` to the bundle, keyed by repository
  identity, so per-project knowledge reaches a harness without being registered
  as a source. `--no-work` opts out, and a KB error is best-effort so it can
  never break the bundle a hook consumes. `weft ticket new/list` scaffolds
  `~/weft/work/tickets/<ID>/` from templates, never overwriting.

- **Source lifecycle** (#193, PR #194). `weft source relocate` and `weft source
  rename`, plus doctor integrity checks over the registry. This also corrected
  an Epic A design bug found while migrating a real setup: the registry holds
  small pointer files and belongs in the engine room, while source *content*
  belongs in the workbench. Defaulting the registry to the workbench collided
  pointers with content directories and flipped resolution once content landed
  there.

### Added — rules resolver ergonomics

- **`weft doctor` rule-annotation health** (#197). `weft doctor` gains a "Rule
  annotations" section so the convention-driven resolver never silently skips a
  source. Per source it reports rule files missing front-matter (with a suggested
  `label:`/`detect:` inferred from the path via `knownStacks`), sources that
  contribute nothing because none of their rules are annotated, duplicate labels
  (resolver keeps the first, ignores the rest), and dangling `extends:` targets.
  Suggestions are printed for review, never applied — an ambiguous `detect:` is a
  guess. Documentation, `projects/`/`tickets/` trees, and the source's own
  instruction-glob wrapper (`CLAUDE.md`) are excluded, so it never suggests
  turning a ticket or wrapper into a rule. New `auditSourceRules` +
  `suggestRuleFrontMatter`. See
  `onboarding-a-source.md`.

### Deprecated

- **`weft:sources` read-map** (#195/#196). The static sources read-map is
  deprecated in favour of the resolver hook, which injects rule *bodies* directly.
  `weft profile use` prints a one-time migration nudge only when a profile still
  expands the snippet (initial, non-quiet apply). The snippet still works; it
  will be removed in a future release. Mirrors the projects-snippet deprecation.

### Added — watcher usability
### Added — watcher usability

- **Profile hot-swap** (#176). `weft profile use <other>` no longer errors when a
  watcher is already running. Instead it hands the requested profile off to the
  running watcher (by writing `active_profile`), and the watcher — now watching
  `config.yaml` — tears down its source/target watchers and stands up a fresh set
  for the new profile in place, logging `[weft] profile switched → <name>`. No
  stop-and-restart needed to switch profiles. The hand-off resolves the target
  profile first, so a typo fails fast in the invoking process and leaves the
  running watcher untouched. New `watch.DebouncedFile` (single-file watch that
  survives atomic rewrites) and `config.FilePath` / `config.ReadActiveProfile`.
  See `bidirectional-watch.md`.

- **Watcher runstate + observability** (#178/#179/#180). New `internal/runstate`
  sidecar (`watcher.json`) records the live watcher's pid, active profile, config
  dir, and start time (flock proves a watcher runs but exposes no holder pid);
  stale files from a crash/SIGKILL are detected and removed on read.
  - The hand-off message now names the running watcher:
    `Handed "beta" off to the running weft watcher (pid 130915, was serving "alpha")`.
  - `weft status` shows watcher state (`Watcher: running (pid …, profile …, up 2h13m)`);
    `--short` gains `watch:on|off`.
  - New `weft profile current` prints the active profile (fresh from disk), no
    apply and no watcher; `-q` for scripting.

### Fixed — watcher / config isolation

- **`active_profile` honours `--config`** (#181). `SetActiveProfile` /
  `config.FilePath` / `ReadActiveProfile` previously hard-coded the global
  `~/.config/weft/config.yaml`, ignoring `--config` (regressing #164/#165
  isolation and breaking hot-swap under a custom config). A package-level
  override set in `initConfig` routes both read and write to the active config
  file. `weft profile use --no-watch` now also warns when a watcher is running,
  since its one-shot apply would otherwise race with the watcher.

- **`--config` reaches `status`, `target` and `resolve`** (#184, PR #188).
  Those three read the default config directory rather than the active one, so
  under `--config` they reported on state they were not managing.

### Added — portability & mixed-source support

- **`{{weft.root}}` path anchors** (#166, PR #167). Rule/command/agent files
  reference other files with `{{weft.root}}` / `{{weft.source:NAME}}`, expanded
  at projection time to the registered source root. Sources become
  location-independent — relocating one is just re-registering it. New
  `internal/anchor` package; expansion applied to per-source instruction copies
  and to managed files via a new `merge.Merger.WithTransform` hook.
  See `portable-paths.md`.

- **`weft doctor` path linter + `--fix`** (#170, PR #171). Doctor scans sources
  for path references and classifies them (`hardcoded-in-source`,
  `stale-prefix`, `broken-anchor`, `unresolved-anchor`, `external-path`,
  `dead-reference`). `--fix` rewrites the healable ones to anchors, resolving
  even *moved* files via a unique trailing-path match; `--all` shows
  informational refs. New `internal/pathlint` package. Detection is boundary/
  extension/foreign-brace gated and symlink-normalised to bound false positives.
  Windows native `C:\` drive-path detection is out of scope (POSIX forms work
  everywhere).

- **`instruction_exclude`** (#168, PR #169). Root-relative path prefixes
  excluded from instruction assembly on top of the managed dirs, so a broad
  `instruction_glob` can inline only a subset of a mixed-content source.
  `--instruction-exclude` flag on `weft source add`.
  See `hierarchical-sources.md`.

### Fixed

- **`--config` now isolates all state** (#164, PR #165). Previously
  `source add --config <file>` still wrote to the global
  `~/.config/weft/sources`. `initConfig` now roots `sources_dir` / `profiles_dir`
  / `hooks_dir` (and the apply path's staged/manifests, MCP server, and doctor)
  beside the active config file. Explicit config keys still win. `--config` is
  now safe for testing and CI.

- **Manifest ownership survives a file leaving the staged set** (#209, PR
  #210). `applyWithManifest` replaced the manifest file map wholesale on every
  apply, so any path absent from the current staged tree lost its entry.
  Ownership meant "currently staged" rather than "weft wrote this", so a file
  dropped by a profile switch and staged again later came back as a conflict,
  was backed up, and was overwritten. The same lookup gates startup write-back,
  so genuine external edits to such a file were missed too.

- **Write-back refreshes the manifest hash** (PR #211). Write-back copied an
  externally edited target file to its source but left the manifest holding the
  pre-edit hash. The next apply compared the target against that stale hash,
  declared the file externally modified, and backed up a file it had just
  reconciled before writing byte-identical content. The README documents that
  apply as a no-op; it was not one.

### Changed

- **Go directive to 1.26.5** (PR #175), for GO-2026-5856 in `crypto/tls` ECH.

- Dependency bumps: `cel-go` 0.28.1 → 0.29.2 (#174, #205), `mcp-go` 0.55.1 →
  0.56.0 (#204), `golang.org/x/crypto` 0.53.0 → 0.54.0 (#173),
  `golang.org/x/sys` 0.46.0 → 0.47.0 (#172), `actions/setup-go` (#203).

- Apply now logs `unchanged` rather than `skip` for a file that already matches
  what weft wrote, since nothing was skipped.

### Docs

- New guides covering portable paths and onboarding a source, and coverage of
  `instruction_exclude` and `--config` isolation in the hierarchical-sources and
  concepts guides. The guides are maintained outside this repository; the README
  carries what a user needs to get started.
