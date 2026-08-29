# Autoresearch lessons — Wavelet / Cordis quality run

Accumulated wisdom across iterations. Read this before forming a hypothesis.
Weight recent lessons higher: the yardstick and codebase change under us.

## Lesson 1 — iterations 0-1
**Pattern**: The project's committed gate (`golangci-lint run` with `.golangci.yml`)
had already been driven to 0 issues by a previous run, so it could no longer
measure anything.
**Why it worked**: Measuring against a pinned snapshot + extra analyzers in
`.auto/lint.ref.yaml` (hash-locked by the Guard) restored headroom and made it
impossible to lower the number by editing the config.
**Conditions**: Any repo whose own lint gate is already green.
**Anti-pattern**: Optimising `tagliatelle` (325 findings) or `wrapcheck` (290).
Those are pure cosmetics — error-message wording and tag naming. A run that
chases them will look productive while shuffling strings.
**Metric delta**: baseline re-established at 102 instead of a dead 0.

## Lesson 2 — iterations 1-4
**Pattern**: Triage every analyzer finding for reality before "fixing" it.
**Why it worked**: Three buckets turned out to be false positives:
`forcetypeassert` in `core/events.go` is guarded by `returnsErr` (the handler's
declared last out really is `error`), and both `exhaustive` switches already
have `default:` arms — `exhaustive` only flags them because
`default-signifies-exhaustive` defaults to false.
**Conditions**: Always, but especially for linters whose defaults assume a
different project convention.
**Anti-pattern**: Adding `if !ok { ... }` branches or empty `case:` arms that
cannot execute. That raises the score and lowers the code.
**Metric delta**: 3 of 16 candidate linters dropped from the plan (0 gained,
real regressions avoided).

## Lesson 3 — iterations 1, 4
**Pattern**: Pair the metric drop with a mechanically provable defect: write the
regression test, commit, then revert *only* the source files and require the
test to fail (`.auto/prove_fix.sh`).
**Why it worked**: It caught a live bug that no counter measures — a
singleflight body capturing the first caller's request context, so one
disconnecting browser poisoned every concurrent request for that image.
Iteration 4 kept debt flat at 93 yet was the most valuable change so far.
**Conditions**: Every behavioural fix. A change that survives its own revert is
not a fix, it is a rename.
**Anti-pattern**: Calling something "hardening" without a test that fails
without it.
**Metric delta**: 0 for the proven bug (kept under the fix gate), 8 for the rest.

## Lesson 4 — iteration 5
**Pattern**: Strengthen the architecture gate; it is a generator of real,
previously invisible debt.
**Why it worked**: `check_cordis_architecture.sh` only grepped `go func(`, so
`go w.run()` — the shape used by four long-lived cleanup loops — passed
silently, each one able to take down the process on a panic. Widening the
pattern surfaced them immediately.
**Conditions**: Whenever a gate has been green for a long time. A green gate
proves the checks exist, not that they cover anything.
**Anti-pattern**: Weakening `.golangci.yml` (blocked outright by the Guard via
`check_gate_weaken.py` + a SHA lock on the yardstick).
**Metric delta**: 4 uncovered crash-on-panic sites hardened.

## Lesson 5 — iteration 3
**Pattern**: Deduplicate by extracting the shared *classification*, not the
shared *response*.
**Why it worked**: Two handlers mapped upload-lookup errors with copy-pasted
blocks that had quietly drifted (different fallback status, different synonym
constant for the same message). `filesrv.AbortUploadRecordError` handles the
200/400 branches, and each endpoint keeps its own fallback it can still
justify. Deleting the orphaned `ErrInvalidUploadID` constant was part of the
change, not extra cleanup.
**Conditions**: Duplicated error-mapping or validation blocks in sibling handlers.
**Anti-pattern**: Silently unifying HTTP status codes across endpoints to make a
helper fit — that is a behaviour change wearing a refactor's clothes.
**Metric delta**: -2.

## Lesson 6 — iterations 15-21
**Pattern**: Delegate a broad read-only audit for what mechanical gates cannot
see (N+1s, locks held over I/O, resource leaks, layering), then re-verify each
claim yourself before touching code.
**Why it worked**: The audit produced the run's best findings — the per-request
CORS database query, the orphan cron dispatching to a task nobody registered,
media temp dirs nothing ever removed. It also produced a wrong one: it asserted
telebot falls back to `http.DefaultClient` with no timeout, when telebot itself
constructs a client with a one minute deadline. Acting on that would have added
a tunable dressed up as a bug fix.
**Conditions**: Whenever the committed gates are green and the easy signal is
exhausted.
**Anti-pattern**: Trusting an audit summary's file:line as evidence. One
referenced file did not exist.
**Metric delta**: 0 for three landed fixes (all kept under the proven-fix gate),
but they were the run's highest-impact changes.

## Lesson 7 — iteration 16
**Pattern**: Prove query-reduction with a functional test double that counts
loader invocations, and assert the counter for both the batch and the looped
form in the same test.
**Why it worked**: Asserting "1 query" alone is vacuous — it also passes when
nothing ran. Asserting batch=1 and per-id=3 in one test makes the instrument
itself checked, so the claim cannot silently degrade.
**Conditions**: Any change whose whole value is doing less I/O.
**Anti-pattern**: Fixing an N+1 by reaching around the contract into another
plugin's repository. The layering was the reason the slow path existed; the
right move was to extend the contract with a batch method.
**Metric delta**: 0 (kept under the proven-fix gate).

## Lesson 8 — iterations 17-22
**Pattern**: Strengthen a gate only alongside the code that satisfies it, and
never rewrite history in a shared worktree.
**Why it worked**: Deleting 24 dead lint suppressions paid off exactly as the
self-correcting design predicted: two of them were load-bearing under the
project's own gate even though the analyzer called them unused, the Guard
vetoed, and their removal surfaced two verified `contextcheck` false positives
worth documenting instead of silently swallowing. Meanwhile a concurrent
session was committing plan documents in the same tree, so `git add -A` swept
one of its in-flight edits into my commit — unfixable by rebase without
destroying their work, so the repair was to stage explicit paths from then on.
**Conditions**: Always, in this repo. Assume another agent is editing `docs/`
and `backend/core` concurrently.
**Anti-pattern**: `git add -A` outside the first setup commit. Also: trusting
"unused directive" as "safe to delete" — check the strictest config, not just
the pinned yardstick.
**Metric delta**: -25 in one iteration.

## Lesson 9 — iterations 23-24
**Pattern**: Cross-check every service a plugin's `Apply` reads out of the
container against what that plugin's `Inject()` declares. `Inject()` is the only
thing `App.reconcileLocked` gates on, so anything consumed as a *value* at Apply
time but left undeclared is resolved from a container that may not hold it yet.
**Why it worked**: It found the run's worst defect, invisible to every
mechanical gate: `user` declared only `DBService` while capturing
`contracts.AuthService` to build its route guard, and `cmd/app.go` lists `user`
before `auth`. Because user's dep set is a strict subset of auth's and it sits
earlier in the slice, user *always* mounts first — deterministically, not a
race — so `loginMW` fell back to a `c.Next()` closure and
`/api/v1/user/{change-password,profile,access-tokens}` mounted unguarded. The
same lookups in `admin` read a package global that its own `OnDispose` nils, so
in-flight requests fail open during dispose.
**Conditions**: Any Cordis plugin whose Apply assigns a contract result to a
variable used later (middleware, handler closures). Services bound through
`core.When` late binding are exempt — that is the correct pattern for genuinely
late deps, so do not blanket-declare everything.
**Anti-pattern**: Assuming a checked `x, ok :=` assertion is safe. All three
plugins used the checked form and all three failed *open* — checked syntax,
unchecked semantics.
**Metric delta**: 0 across both iterations (kept under the proven-fix gate),
but this is the run's highest-severity finding. `RouterRegistry` records each
route's `Handlers`/`Middlewares`, which makes "is this route actually guarded?"
directly assertable from the route table — the cheapest available oracle for
security properties here.

## Lesson 10 — iteration 23 review
**Pattern**: When the remaining metric is dominated by a positional or
taste-based analyzer, say so and refuse to spend iterations on it.
**Why it worked**: `funcorder` was 21 of 54 findings (39%) — pure function
*ordering within a file*. Reordering private helpers to the bottom of a file
moves the number and changes nothing a reader or the machine cares about, which
is Lesson 1's "looks productive while shuffling strings" with a different label.
Skipping it kept the loop honest. Triage also cleared 12 of 13
`forcetypeassert` (guarded by construction) and 2 of 3 `unparam` (deliberate
constructor symmetry behind one factory switch).
**Conditions**: Whenever one linter dominates a shrinking total, break the count
down per linter *before* picking a hypothesis.
**Anti-pattern**: Treating a large single-linter share as an easy win. Real
headroom at this point is ~10 findings, so a plateau in `debt` no longer means a
stalled loop.
**Metric delta**: 0 spent, ~21 findings deliberately left in place.

## Lesson 11 — iterations 24-25
**Pattern**: Run the Guard after every single commit, and confirm which commit a
proof script is actually reverting against.
**Why it worked**: Four `staticcheck ST1023` findings from iteration 24 shipped
straight through `go build ./...` and a green 47-package `go test ./...` —
neither runs the project linter, so only `checks.sh` section 3 catches them.
Separately, `prove_fix.sh` reverts to `HEAD^`; appending the iteration-23 log
commit shifted `HEAD^` to the *fixed* state and reported "PROVE FAILED: tests
still pass without the fix" on a genuinely load-bearing fix. Re-checking against
the explicit pre-fix commit (`git checkout <sha> -- <files>`) showed the real
answer. A false negative here is worse than no proof: it reads like the fix was
cosmetic.
**Conditions**: Always. Also note zsh does not word-split unquoted variables, so
`git checkout $FILES` passes one bogus pathspec and silently reverts nothing —
the command still exits 0.
**Anti-pattern**: Batch-verifying at ship time. And any shell loop built on the
bash word-splitting habit in this environment.
**Metric delta**: -0, 1 wrong verdict corrected.

## Standing notes
- Upstream moves fast in this repo: `origin/main` gained 11 commits mid-run
  (Cordis config extension point — `ctx.Config().Bind`, `DeclareConfig()`,
  `core.ConfigGatedPlugin`), which raised measured `debt` 54 -> 64 and
  `nolint_dirs` 72 -> 73 on its own. Rebase early and re-run the Guard after;
  a clean rebase does not mean a green one.
- `cmd.TestNewWaveletAppWithRedisEnabled` needs a live Redis on
  `127.0.0.1:6379` and fails without one. Pre-existing on `origin/main`, so
  `tests_passed` 46 vs 47 is environmental, not a regression. Confirm against a
  scratch `git worktree` of `origin/main` before blaming a change for it.
- Repo facts: backend module rooted at `backend/`, gofumpt orders a single
  import group as `Wavelet/...` before stdlib (uppercase sorts first); new Go
  files need the Apache license header or `scripts/update_go_license.sh --check`
  fails the Guard.
- Handler edits require `make swagger` (cheap: it regenerates identical docs
  when only bodies change).
- Dead suppressions are tracked by the `nolint_dirs` counter; removing one that
  is still needed re-raises the original finding, so the metric self-corrects.
  24 were removed in iteration 22; 72 remain, each still doing work (73 after
  the upstream rebase).

