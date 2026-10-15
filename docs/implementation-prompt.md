# Implementation prompt for meles

Paste the block below into a fresh agent session started in this directory.

---

Implement meles, a GitHub housekeeping tool. The design is agreed and
lives in this repo. Read these before anything else, in order:

1. `docs/DESIGN.md`: the model, pipeline, modes, `on_close`, policy
   packs, config semantics, decisions, and the implementation plan.
2. `examples/meles.yml`, `examples/shapes/go.yml`, `examples/.meles.yml`:
   the config surface you are implementing. Treat them as the spec and
   keep them valid as you go.

Work through the implementation plan in `docs/DESIGN.md` one milestone
at a time. Before writing code for each milestone, enter plan mode,
outline the packages and interfaces, and get my sign-off. Do not start
the next milestone until I have said the current one is good.

Constraints:

- Go, single module, single binary. Use `go-github` for the API and
  `ghinstallation` for App auth. No framework.
- The policy interface is the heart of it. A policy evaluates facts into
  findings with no I/O, then turns a finding into a `FileChange` or a
  `SettingChange`. Keep evaluation pure so it is table-testable.
- `meles plan` is the default and must never write. `meles apply` is
  the only path that mutates GitHub. Every apply must be idempotent: a
  second run with no drift makes zero write calls, and a test proves it.
- Files always go through one PR per repo on the `meles/housekeeping`
  branch, updated in place. Settings are applied directly only when a
  policy's mode is `enforce`.
- Implement `on_close` per finding, not per PR, as described in the
  Closed PRs section of the design.
- Milestone 1 is the scaffold: `justfile` with `build`, `test`, `lint`,
  `run`; GitHub Actions CI; README with build and license
  badges; Apache-2.0 LICENSE.
- Tests: write the failing test first for policy evaluation and the PR
  executor. Do not stub `go-github` to the point of testing the stub.
  For the executor and facts cache, write an integration test against a
  real throwaway repo, gated behind an env var, since we rely on API
  behaviour that is poorly documented (ETags, PR update semantics,
  `workflows: write`).
- Comments are terse, four lines or fewer, and only for hazards or
  invariants the code cannot show.
- Commit per milestone with a conventional-commit message. Do not push
  and do not open PRs unless I ask.

Known hazards from the design, do not rediscover them:

- Touching `.github/workflows/*` in a PR needs the App's
  `workflows: write` permission, or `workflow` scope on a PAT.
- Rate limit is 5000 requests per hour per installation. Cache facts
  with ETags from milestone 2, not later.
- Validate generated `renovate.json` with `renovate-config-validator`
  before it goes into a PR.

Start by reading the two documents above, then propose the package
layout for milestone 1 and 2 together so the policy interface is
settled early.
