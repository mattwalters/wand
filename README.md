# wand

An agent toolkit for repositories: it sets up and maintains a repo's agent
machinery — its covenant, and the blessing path work travels along.

> **Status: early.** `init` bootstraps a Linear team to the covenant and
> installs the guard hook; `guard` enforces the covenant's authorization
> rules; `queue` and `ticket` are the read layer; the lifecycle verbs
> (`claim`, `handback`, `abandon`, `file`) are the write layer; `doctor`
> reports a team's drift from the covenant; `ui` opens home, where a
> human blesses work; `plan` is the first orchestrator — a cold scout that
> turns one blessed-for-research ticket into a plan a human can bless for
> building; `run` drives one blessed ticket through the implement → CI →
> review → revise loop; `dispatch` is the selector over that loop — it
> picks the one ticket to run next and runs it, with `--watch` to poll
> continuously; and `sweep` is everything that happens after a run exits.

## Install

```bash
brew install mattwalters/wand/wand
```

Or with a shell script, on macOS or Linux — this is the one to put on a
Linux box where the cask doesn't reach:

```bash
curl -fsSL https://wandcli.com/install.sh | sh
```

It downloads the release archive matching your OS and arch, verifies it
against the published checksums, and installs to `/usr/local/bin` (falling
back to `~/.local/bin` if that isn't writable — override either with
`WAND_INSTALL_DIR`). Pin a version with `WAND_VERSION=v0.1.0`.

Or with Go:

```bash
go install github.com/mattwalters/wand@latest
```

That puts `wand` in `$(go env GOPATH)/bin`, which needs to be on your `PATH`.
Prebuilt binaries for macOS, Linux and Windows (amd64 and arm64) are on the
[releases page](https://github.com/mattwalters/wand/releases); the `v1` tag
always points at the latest `v1.x.y`.

## Usage

`--team-key` below is shown explicitly, but it is only required outside any
repo, or to run against a team other than the one the repo is bound to. A
repo with `[team] key = "WND"` in its `wand.toml` needs it on none of these:

```bash
wand                        # home: everything waiting on a human
wand help                   # help
wand ui --team-key WND      # home, spelled explicitly
wand queue --team-key WND   # the ranked, vetted Todo queue
wand ticket WND-3           # one ticket whole, for a cold reader
wand claim WND-3            # take a blessed issue: In Progress + assignee, first
wand handback WND-3 -m "…"  # park it on a human: question first, Needs Input second
wand abandon WND-3 -m "…"   # return it to Backlog with the evidence that undid it
wand file "…" --team-key WND  # file a finding into Triage, duplicates searched first
wand plan WND-3             # research one To Plan ticket into a plan, ending at Plan Review
wand run WND-3              # own one blessed ticket: implement → CI → review → revise
wand dispatch --team-key WND  # pick the one ticket to run next, and run it
wand sweep --team-key WND   # act on one thing left over after a run ended
wand doctor --team-key WND  # report the team's drift from the covenant
wand version                # build info, and the covenant schema this binary speaks
```

## Home

`wand ui` opens on a landing screen naming three views — one job each,
rather than one flat list of everything waiting on you:

```
 wand · WND                                                     7 waiting on you

  1  Decide   2 waiting  judge the pool, rank it, promote it
  2  Review   3 waiting  bless plans, answer questions, merge PRs
  3  Unblock  2 waiting  clear parks, resolve stuck lanes

  1 Decide • 2 Review • 3 Unblock • r refresh • q quit
```

**Decide** (`1`) is Triage: judge what agents filed, rank it, promote it.
**Review** (`2`) is Plan Review, Needs Input and Ready for human together:
bless plans, answer questions, merge PRs — judgment on work that already
exists, not on what to start. **Unblock** (`3`) is Stalled: clear parks,
resolve runs no process is driving any more — a repair of the tooling, not
a judgment about the work. A number key opens a view; `esc` returns here.

Five queues behind the three views, and nothing else. Each is one that
nothing drains on its own, and each is invisible until something puts it on
screen. Backlog is deliberately absent: a Backlog ticket is not waiting on
you, it is the pool, and browsing a pool is Linear's job.

**Plan Review, in the Review view, is where a plan gets judged.** A ticket
lands there with the plan `wand plan` wrote in its description, and opening
the row shows that plan in place — nothing else in the description, and no
comment. Judging it has two answers: bless it into Todo, the same way
Triage does, or send it back to Backlog with the reasoning as a comment, so
the next plan run over the ticket starts from why the last plan did not
land instead of guessing.

**Blessing lives in Decide.** Promoting a ticket to Todo or To Plan is the
transition [the guard](#the-guard) refuses everywhere else, because it hands
out authorization an agent does not have. Home is the one place a
person grants it, and it is a deliberate moment rather than a keystroke:

```
  ✦ Blessing

    WND-41  guard: a raw state UUID is not matched
    Triage → Todo

    Todo is the gate between written down and a bot may act on this unattended.
    Blessing it lets an agent claim the ticket, branch, write code and open a
    pull request without asking you again.

    WND-41 has no priority, and No priority sorts last in the queue. Blessing it
    authorizes the work without scheduling it.

  enter bless WND-41 → Todo • esc back
```

Judging a Triage ticket has four other answers — Backlog with a priority,
Backlog unranked, Duplicate of another issue, or Canceled with a reason.
The last two write their evidence *before* the status moves, for the same
reason `handback` posts its question first: the status write is what ends
the ticket's visibility, so anything a reader will later need has to already
be on it.

`wand ui --sample` opens the same interface against a built-in board, so you
can walk it — landing screen, all three views, blessing included — without
an API key or a team.

Press `e` to **engage**: home then polls Todo and To Plan on an
interval, spawning a winner as a detached process, and also drains sweep's
four conditions — a dead lease, a re-plan or re-review hand-back, an
unresolved PR thread on a converged ticket — the same pass `wand sweep`
runs standalone. It is `wand dispatch --watch`'s own mechanics, run from
inside home instead of a standalone process. It is always a
deliberate key press, never a default: bare `wand` opens home, never
pre-engaged, because opening a dashboard must not start spending money. See
the engage mode section of [the `ui` command
docs](https://wandcli.com/docs/commands/ui/) for the safety story across
multiple engaged homes.

`wand version` reports the covenant schema version alongside the build:
a repo's covenant file declares the schema it was written against, and
comparing the two is how you learn whether a given binary can read it.

## The covenant file

The state graph — Triage → Backlog → To Plan → In Planning → Plan Review →
Todo → Needs Input → In Progress → In Review → Done — is wand's opinion, gofmt-style. What a repo
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

wand ships one of its own: [`wand.toml`](./wand.toml) at the root of this
repo sets `commands.verify` and nothing else. That is the shape to copy —
what a repo leaves unset stays the stock covenant, and a key restating a
default is a key that will one day disagree with it.

The file must never contain a secret, a machine path, or a harness name:
those are machine config, not covenant. The test for the split: if two
clones could legitimately differ, it is config; if a difference means two
different processes, it is covenant.

## The plan orchestrator

`wand plan WND-3` claims the ticket into In Planning — mirroring the way
`wand run` claims Todo into In Progress, before anything else happens —
then sends a cold, read-only scout over the repository to research it,
validates what it hands back, and writes the result: the plan into a
marker-fenced region of the description, the approaches and their
trade-offs as a comment, the estimate, then Plan Review. Promoting the
result to Todo is yours — an agent does not bless its own plan.

Two rules carry most of the weight. **A handoff that fails validation
writes nothing at all**: one to three approaches each with a trade-off, a
recommendation naming one of them, at least one file cited as `path:line`,
an estimate on the covenant's scale, and a plan with ordered steps and a
test story. Structural defects are fatal, cosmetic ones are not — a
citation missing its line number is dropped and named in the plan rather
than costing the whole research pass, because discarding a scout's work
over one malformed field is a loss out of all proportion to the mistake.
Half a plan reads like a whole one, and a human blesses it on the strength
of the argument beside it. And **each deliverable lands before the
transition that advertises it** — Plan Review says "there is a finished
plan here to judge", so it is written last, and anything that fails before
it leaves the ticket in In Planning rather than claiming to be finished. A scout
that finds the ticket's premise wrong takes the other ending instead: no
plan, its account on the ticket, and Needs Input — reserved for exactly
that, a blocking question, never a plan awaiting review.

The plan region is the only thing a plan run writes into the description.
The ticket's goals, problem statement and title are the human's and are
checked *against* by the plan, never rewritten by it — the fence is
absolute, on a first plan and a re-plan alike. A scout that judges the
ticket itself wrong takes the wrong-premise ending instead: no plan, its
account on the ticket, and a human decides.

There is no worktree: the scout reads your checkout and may not change it,
and a run whose worker touched the tree parks with the change left in front
of you. `--interactive` grills you over the draft first — questions ordered
worst-consequence first, each quoting the draft — and hands your answers to
a second, fresh session, because a session that has just argued for an
approach defends it. A cold critic (`toggles.plan_critic`, on by default)
runs ahead of that: what it resolves is named in the options comment as
what was challenged and what changed, and what it cannot resolve becomes an
open question there for you to judge.

Exit codes are a scheduler contract: `0` planned, `2` handed back (the scout
judged the ticket's premise wrong), `3` parked, `1` never started.

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
promoting to **Todo** blesses building, promoting to **To Plan** blesses
research, and **Done**, **Canceled** and **Duplicate** close a ticket. Those
are a human's call, so `wand guard` refuses them — by status name or by
Linear state type — while leaving every legitimate agent move alone
(In Progress, In Review, Needs Input, In Planning, Plan Review, Backlog, Triage).

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
are vetted out and printed with the reason — labeled `human-only`, labeled
`parked` (a run already stopped on it; clearing the label is how you ask
for another), or blocked by an issue not yet completed or canceled (a
started blocker still blocks). Nothing is dropped silently: a queue that
quietly comes up short reads as a queue in order.

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

## The run loop

`wand run WND-3` owns one blessed ticket from claim to a terminal state:
implement → CI → review → revise, a cold worker per phase, in a run-private
worktree, with phases and caps from the covenant (`caps.review_rounds`,
`caps.ci_attempts`, `caps.worker_timeout_minutes`, `caps.phase_timeout_minutes`,
`caps.worker_retries`; `commands.verify` is required). Workers commit and are mute — their environments carry no Linear
or GitHub credentials, proven per harness by the isolation conformance
suite — while the orchestrator makes every external write: it runs verify,
pushes, opens and titles the PR (`[WND-3] …`, written at open and repaired
in code), applies the handoff's description corrections to the ticket, and
moves the status.

The ticket's description is the specification handed to the implement
worker; its comments are the record of how it got there — reasoning, open
questions, answers, approaches raised and rejected — never themselves
authoritative, and the prompt says so: a worker that read a rejected
approach and the accepted one as equally binding could build the wrong one.

Every run ends in exactly one journaled terminal state, and the exit code
is a contract a scheduler can read:

- **converged** (exit 0) — the reviewer approved on positive evidence and
  no human PR thread stands unresolved (outdated included: outdated is not
  answered); the ticket is In Review with `ready-for-human`, and the PR
  awaits a human. A PR a human merged before the run could finish converges
  too — the work landed — with the status left to the merge automation
  rather than moved back to In Review.
- **handed back** (exit 2) — Needs Input, comment first: a worker's own
  verbatim account of what blocked it, or a cap that ran out saying so.
  Convergence never happens by exhaustion; a spent cap is a hand-back that
  quotes the final round's findings whole.
- **parked** (exit 3) — stopped without deciding (interrupt, unparseable
  handoff, dirty tree); worktree preserved. The journal is written first
  and is the run's real ending, so a park is reachable even when Linear is
  what failed; the ticket then gets the same sentence as a comment and the
  `parked` label, best-effort. A park keeps the ticket's place in the
  lifecycle — it reports that the machine stopped, not that the work was
  judged — so clearing the label is all it takes to let `wand dispatch`
  pick it up again. A reviewer that leaves no parseable handoff parks
  rather than converges: anything else turns reviewer crashes into clean
  passes.

One failure does not park: a worker whose harness reported the failure as
infrastructure rather than as anything about the work — a provider error, a
host that suspended mid-response. That phase respawns, up to
`caps.worker_retries` times (one by default; `0` turns it off), at the same
round, so a closed laptop lid never spends review or CI budget. Only the
harness's own verdict counts, through an optional adapter interface that
fails soft: a harness that cannot say, or output nothing recognized, means
"not transient" and the run parks as before. Timeouts are excluded on
purpose — a timeout cannot distinguish a wedged worker from a job bigger
than the cap, and a retry costs another whole one — and an interrupt is
never retried, whatever the harness printed on the way down. A retry only
happens into a clean tree: uncommitted work is work at risk, and a second
worker must not write over the first one's half-finished edits.

The run journal makes all of it crash-only: every phase is journaled before
its worker spawns, and a run killed outright is provably dead and cheap to
re-enter.

## Docs

Everything is at [wandcli.com/docs/](https://wandcli.com/docs/): the
doctrine — the *why* behind every covenant state and rule, which
deliberately does not live in the covenant file — and the command
reference, every implemented command one page each with its flags and its
exit codes, at
[wandcli.com/docs/commands/](https://wandcli.com/docs/commands/).

**The root is the latest build.** `wandcli.com/` is the project's homepage
and `wandcli.com/docs/` the current documentation, both tracking `main` —
which is what lets a doc fix ship without cutting a release, and matches
this repo's rule that docs change with the code in the same PR. Every
release additionally freezes a copy at `wandcli.com/vX.Y.Z/`, so a repo
pinned to wand vX.Y.Z reads the doctrine matching its covenant schema; pick
the version in the site header. `/latest/` is the root build published a
second time, so links written before this layout still resolve.

The site lives in [docs/](docs/) — plain Markdown built by Hugo (a single
Go binary, keeping with the no-Node rule) with a hand-rolled minimal theme.
`make docs-serve` previews it locally.

Publishing is two parts: the docs workflow commits the built site to the
`gh-pages` branch — on every push to `main` and again on every tag — and a
one-time repo setting (Settings → Pages → deploy from the `gh-pages`
branch, with `wandcli.com` as the verified custom domain) tells GitHub to
serve it. The deploy keeps existing files, so a frozen version directory is
never touched again after the tag that wrote it.

## Run from a clone

You do not need to install anything to try it:

```bash
go run . ui --sample
```

`make run` does the same. To get a binary instead, `make build` writes
`bin/wand` (gitignored), and `make install` puts `wand` on your `PATH`.

### Which wand am I running?

`make install` writes to Go's bin directory, which on a typical machine
precedes Homebrew's on `PATH`. So a local build **shadows** an installed
release: after `make install`, a bare `wand` is your working tree, even with
the cask installed. That is deliberate. Every repo's `PreToolUse` hook shells
out to a bare `wand guard`, so if the dev build did not own the name, the
guard would be the one surface never dogfooded. A shell alias cannot stand in
for this — hooks are exec'd by the harness, not by an interactive shell, so
aliases are invisible to them.

The cost is that a broken build breaks Linear writes in every repo, not just
this one. `make install` therefore builds, smokes the binary — `version` must
answer and `guard` must still block a promotion to Todo — and only then moves
it onto your `PATH`. A build that fails the smoke never lands.

These gestures cover everything:

| Intent | Move |
|---|---|
| Does my change work, right now? | `make build && ./bin/wand …` — no install, no `PATH` |
| Live on the tip, in every repo | `make install` |
| Back to what ships | `make uninstall` |
| Pin an old release | `make install-release VERSION=v0.1.0` |
| What do users actually get? | install the cask, run `/opt/homebrew/bin/wand` |
| Installed via the curl script | `curl -fsSL https://wandcli.com/install.sh \| sh`, run by absolute path (it prints where it landed) |

That last-but-one row is the one worth remembering: because Go's bin
directory wins, a bare `wand` after `brew install` still runs your dev
build. Comparing against a release means the absolute path, every time. The
curl script is a fourth copy in the same story — it does not touch the
cask's `/opt/homebrew/bin/wand` or your Go bin directory, but it warns if
its own install directory shadows or is shadowed by one of them, rather
than silently changing which `wand` a bare invocation runs.

Local builds stamp themselves from `git describe`, so `wand version` tells you
which one you have — `v0.1.0-5-g22923b1-dirty` is five commits past the tag
with an unclean tree, where a real release reads exactly `v0.1.0`.
`make install-release` builds from source at a tag, so it is not byte-identical
to the published artifact; to exercise what users get, use the cask. Either
copy can be checked in place with `make smoke BIN=/opt/homebrew/bin/wand`.

## Testing a TUI

Terminal UIs are awkward to test and nearly impossible for an AI agent to
review, because the thing under test is a picture. wand renders screens to
plain text instead:

```console
$ wand ui --script "j,t" --dump-screen
  ✦ Blessing

    WND-41  guard: a raw state UUID is not matched
    Triage → Todo

    Todo is the gate between written down and a bot may act on this unattended.
    Blessing it lets an agent claim the ticket, branch, write code and open a
    pull request without asking you again.

    WND-41 has no priority, and No priority sorts last in the queue. Blessing it
    authorizes the work without scheduling it.

  read-only: this is the sample board. Every key here walks the screen; none of
  them write.
  esc back
```

`--script` applies a key sequence first, so any screen is reachable from one
command, and no terminal is needed. From a clone that is
`go run . ui --script "j,t" --dump-screen`, or `make screen SCRIPT=j,t`.

Note the last two lines. `--dump-screen` renders the built-in sample board
with **no writer behind it**, so an agent can walk to every screen — the
blessing screen included, which is the one most worth being able to inspect
— and reach none of the transitions the guard forbids it. The refusal is
structural, not a check: that code path constructs the model without a
backend, so there is nothing for the confirm key to call.

The test suite renders through the same code path, so those snapshots are
stored as golden files that read as pictures of the UI:

```go
tuitest.AssertScreen(t, "bless-todo", model, "t")
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
go run . ui --sample # run home from source, against the sample board
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
