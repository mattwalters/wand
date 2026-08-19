# wand

An agent toolkit for repositories: it sets up and maintains a repo's agent
machinery — its covenant, and the blessing path work travels along.

> **Status: early.** `init` bootstraps a Linear team to the covenant and
> installs the guard hook; `guard` enforces the covenant's authorization
> rules; `queue` and `ticket` are the read layer; `doctor` reports a
> team's drift from the covenant. The `covenant` and `bless` verbs are
> stubs.

## Install

```bash
brew install mattwalters/wand/wand
```

Or with Go:

```bash
go install github.com/mattwalters/wand@latest
```

That puts `wand` in `$(go env GOPATH)/bin`, which needs to be on your `PATH`.
Prebuilt binaries for macOS, Linux and Windows (amd64 and arm64) are on the
[releases page](https://github.com/mattwalters/wand/releases); the `v1` tag
always points at the latest `v1.x.y`.

## Usage

```bash
wand                        # help
wand ui                     # the interactive interface
wand queue --team-key WND   # the ranked, vetted Todo queue
wand ticket WND-3           # one ticket whole, for a cold reader
wand claim WND-3            # take a blessed issue: In Progress + assignee, first
wand handback WND-3 -m "…"  # park it on a human: question first, Needs Input second
wand abandon WND-3 -m "…"   # return it to Backlog with the evidence that undid it
wand file "…" --team-key WND  # file a finding into Triage, duplicates searched first
wand doctor --team-key WND  # report the team's drift from the covenant
wand version                # build info, and the covenant schema this binary speaks
```

`wand version` reports the covenant schema version alongside the build:
a repo's covenant file declares the schema it was written against, and
comparing the two is how you learn whether a given binary can read it.

## The covenant file

The state graph — Triage → Backlog → Scoping → Todo → Needs Input →
In Progress → In Review → Done — is wand's opinion, gofmt-style. What a repo
customizes are the parameters of the machine, never its shape: a checked-in
`wand.toml` at the repo root carries status *names* over the fixed semantics,
caps (review rounds, CI attempts, worker timeouts), the estimate scale,
toggles, the three pluggable commands (verify, provision, run agent), ticket
templates, and a schema version so topology upgrades ship centrally as wand
releases. TOML, so the rationale for a value survives as a comment next to
the value it justifies:

```toml
schema = 1

[statuses]
# Our board predates wand and the team reads "Ready" as blessed-to-build.
todo = "Ready"

[caps]
review_rounds = 5

[commands]
verify = "make check"
```

`wand init` and `wand doctor` read it when present and fall back to the stock
covenant when absent. Validation refuses unknown keys loudly — a misspelled cap silently
defaulting is the failure mode — and an invalid file is an error, never
quietly the defaults.

The file must never contain a secret, a machine path, or a harness name:
those are machine config, not covenant. The test for the split: if two
clones could legitimately differ, it is config; if a difference means two
different processes, it is covenant.

## The doctor

`wand doctor --team-key WAND` reads the team's statuses, labels, PR
automations and settings, diffs them against the covenant, and reports the
drift. It writes nothing — `wand init` is the verb that repairs — and it
exits diff-style, so scripts and CI can hold a team to the covenant
continuously instead of a human verifying the settings pages once:

- `0` — the team satisfies the covenant
- `1` — drift found (each finding on its own `drift:` line)
- `2` — the check could not run (no API key, no such team, API failure)

The covenant, not Linear's settings pages, is the source of truth — the API
exposes everything doctor needs, git automations included. Extra statuses
outside the machine's path (a team's own "Design" column upstream of
Backlog), extra labels, and automations on events the covenant does not
mention are tolerated strangers, not drift. Renamed or missing covenant
statuses, repointed automations, missing labels, and changed team settings
are drift.

## The guard

Some ticket transitions hand out authorization an agent does not have:
promoting to **Todo** blesses building, promoting to **Scoping** blesses
research, and **Done**, **Canceled** and **Duplicate** close a ticket. Those
are a human's call, so `wand guard` refuses them — by status name or by
Linear state type — while leaving every legitimate agent move alone
(In Progress, In Review, Needs Input, Backlog, Triage).

It speaks the Claude Code and Codex PreToolUse hook protocol: the pending tool
call arrives as JSON on stdin, and exit code 2 blocks it with the reason on
stderr. Input that does not parse passes — a broken guard must never wedge a
session.

`wand init` installs the hook for both harnesses: entries in
`.claude/settings.json` and `.codex/hooks.json` route every Linear
`save_issue` through `wand guard`. Those entries are build artifacts —
regenerated by `init`, never hand-edited — and assume `wand` is on your
`PATH`. Codex asks you to trust a repo-local command hook before it runs.
Codex workers themselves disable hooks and ignore user configuration; that
excludes user-configured MCP servers, though installed plugins can still
contribute their own servers.

## The read layer

`wand queue` prints a team's Todo issues in start order: priority ascending
with "No priority" last, oldest first within a priority, identifier as the
final tiebreak so two racing readers agree. Issues an agent may not start
are vetted out and printed with the reason — labeled `human-only`, or
blocked by an issue not yet completed or canceled (a started blocker still
blocks). Nothing is dropped silently: a queue that quietly comes up short
reads as a queue in order.

`wand ticket WND-3` renders one issue for someone with no context — a worker
being prompted, or a human catching up. The description is passed whole;
comments follow oldest-first, every page of them, each held to a per-comment
budget so a long early comment cannot crowd out the short answer a human
left last.

Both need `LINEAR_API_KEY` in the environment, and both respect a
`wand.toml`'s status renames — a board whose blessed column is called
"Ready" queues from "Ready".

## The lifecycle verbs

The four writes an interactive session performs, as verbs instead of raw
Linear calls — each encodes an ordering rule that used to live in prose,
where it was followed only sometimes:

- `wand claim WND-3` vets the issue exactly as queue does (in Todo, not
  `human-only`, no unresolved blockers), then sets In Progress and the
  assignee in a single write. Claim **before** anything touches the
  filesystem: the status move is the cheapest place to lose a race.
- `wand handback WND-3 -m "…"` posts the question — what you need, the
  options, your pick — as a comment **first**, then moves to Needs Input,
  so a failure between the two never leaves a Needs Input ticket with no
  question on it. That ticket parks forever.
- `wand abandon WND-3 -m "…" --replace "old wording" --with "corrected"`
  posts the evidence, then in one write corrects the description, moves to
  Backlog and unassigns — the body stops asserting the false premise in the
  same act that demotes it, and the old wording is quoted into the comment.
  The `--replace` anchor must match the description exactly once; anything
  else refuses rather than guesses in someone else's prose. Never Canceled,
  Done or Duplicate: closing is a human's call, and the guard enforces it.
- `wand file "title" --team-key WND` searches the team for near-duplicates
  first — candidates are printed and nothing is filed until they are ruled
  out (`--force`) — then files into Triage with the `agent-filed` label, no
  priority and no assignee. An agent never promotes what it filed.

Like the reads, the verbs need `LINEAR_API_KEY` in the environment and
respect a `wand.toml`'s status renames (found by walking up from the
working directory, so a verb run from a subdirectory sees the repo's file).
Every status they write passes through the same verdict function as the
guard hook, so wand's own write path cannot drift from what the guard
promises — and the respect runs both ways: `handback` and `abandon` refuse
a ticket a human already closed, because reopening a close is as much a
human's call as making one.

## Docs

The doctrine — the *why* behind every covenant state and rule, which
deliberately does not live in the covenant file — is at
[wandcli.com](https://wandcli.com/). The docs
are versioned with the tool: every release publishes a frozen copy under its
own tag next to the moving `latest`, so a repo pinned to wand vX.Y.Z reads
the doctrine matching its covenant schema.

The site lives in [docs/](docs/) — plain Markdown built by Hugo (a single
Go binary, keeping with the no-Node rule) with a hand-rolled minimal theme.
`make docs-serve` previews it locally.

Publishing is two parts: the release workflow commits the built site to the
`gh-pages` branch on every tag, and a one-time repo setting (Settings →
Pages → deploy from the `gh-pages` branch, with `wandcli.com` as the
verified custom domain) tells GitHub to serve it — after that, every
release publishes on its own.

## Run from a clone

You do not need to install anything to try it:

```bash
go run . ui
```

`make run` does the same. To get a binary instead, `make build` writes
`bin/wand` (gitignored), and `make install` puts `wand` on your `PATH`.

## Testing a TUI

Terminal UIs are awkward to test and nearly impossible for an AI agent to
review, because the thing under test is a picture. wand renders screens to
plain text instead:

```console
$ wand ui --script "j,enter" --dump-screen
 wand covenant

  Read and edit the repository covenant

  "covenant" is not implemented yet.

  esc back • q quit
```

`--script` applies a key sequence first, so any screen is reachable from one
command, and no terminal is needed. From a clone that is
`go run . ui --script "j,enter" --dump-screen`, or `make screen SCRIPT=j,enter`.

The test suite renders through the same code path, so those snapshots are
stored as golden files that read as pictures of the UI:

```go
tuitest.AssertScreen(t, "detail", tui.New(80, 24), "j,enter")
```

When a screen changes, the failure is a unified diff of the screen itself
rather than a wall of escape codes. Under the hood a real `tea.Program` runs
with its output captured, and those ANSI bytes are replayed through a virtual
terminal emulator to recover the grid of characters a user would see.

Four tiers cover the app: pure `Update` tests, runtime wiring tests, golden
screens, and one end-to-end test that drives the compiled binary through a real
pseudo-terminal. Off to the side sits the worker isolation conformance suite:
its structural half runs with `make test`, and its live half
(`make test-conformance`) spawns each installed real harness and proves a worker
instructed to write Linear or GitHub fails — it spends a real model call, so
it is run deliberately, never in CI. See [CLAUDE.md](CLAUDE.md) for the
details and the rules that keep it deterministic.

## Development

```bash
go run . ui          # run the TUI from source
make test            # fast suite
make test-e2e        # pty smoke test
make test-conformance # worker isolation against the real harness (spends a model call)
make check           # gofmt + vet + test
make update-goldens  # regenerate screens (then read the diff)
make docs            # build the docs site into docs/public (needs hugo: brew install hugo)
make docs-serve      # serve the docs at http://localhost:1313/ with live reload
make release VERSION=v0.1.0  # tag and push a release (CI does the rest)
make help            # every target
```

Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea),
[Lip Gloss](https://github.com/charmbracelet/lipgloss),
[Bubbles](https://github.com/charmbracelet/bubbles) and
[fang](https://github.com/charmbracelet/fang).

## License

MIT — see [LICENSE](LICENSE).
