# meles design notes (2026-10-14)

Status: milestones 1 and 2 implemented (read-only `meles plan`). Prototype screens live in the Paper
file "meles · workflow prototypes" (five artboards: how it works, fleet
dashboard, repo plan to PR, CLI plan, release notes).

Badgers keep the sett tidy. meles keeps GitHub repos tidy.

## One-line model

**Config management for GitHub repos.** Declare desired state once, meles
converges every repo toward it, continuously. Puppet/Ansible, but the
"hosts" are repos and the "resources" are files, settings and rulesets.

## Delivery: GitHub App for identity, Actions for compute

Goal: "install it, write policies, it works" with nothing to host.

1. A GitHub App gives identity and scoped permissions. Install it on the
   account or org. (App Manifest flow lets a new user create their own
   App in one click; no shared multi-tenant service needed.)
2. A control repo holds `meles.yml`, templates and shapes: the owner's
   `.github` repo, or a dedicated `.meles` repo to keep it separate. meles
   never assumes `<owner>/meles`.
3. A workflow in the control repo runs on cron + `workflow_dispatch`,
   mints an installation token with `actions/create-github-app-token`,
   and runs the `meles` binary.

Later, optional: hosted mode that receives webhooks (needed for
event-driven things like release notes on `release.published`).
Same binary, different entry point.

## Pipeline

```
inventory  ->  facts  ->  evaluate  ->  plan  ->  apply  ->  report
```

- **inventory**: repos visible to the installation, filtered by selectors
  (topic, language, visibility, name glob, marker files, archived/fork).
- **facts**: one snapshot per repo. Settings, rulesets, security
  features, file tree + content of interesting paths, workflows, latest
  release, installed apps. Cached with ETags.
- **evaluate**: each policy returns findings against facts. Pure, no I/O.
- **plan**: findings -> changes. Two change kinds:
  - `FileChange` (path, content). Batched per repo into **one PR** on a
    stable branch (`meles/housekeeping`), force-updated in place. Never
    opens a second PR while the first is open. What happens after a human
    closes it is a per-policy setting, see "Closed PRs" below.
  - `SettingChange` (API call). Applied directly when mode=`enforce`,
    otherwise reported.
- **apply**: rate-limited, idempotent. Re-running with no drift is a no-op.
- **report**: job summary, dashboard issue in the control repo, JSON
  export. `meles plan` is the dry run and is the default.

## Modes

Every policy has a mode, with a global default:

| mode      | behaviour                                   |
|-----------|---------------------------------------------|
| `observe` | report only                                 |
| `pr`      | open/update a PR with the fix               |
| `enforce` | apply directly (settings only; files are always PRs) |

Repo opt-out via `.meles.yml` in the repo, per policy, with a reason and
an expiry date so exemptions do not rot silently.

## Closed PRs

A human closing a meles PR is a signal, and policies differ on what it
means. `on_close` is set per policy, with a global default. Policies with
entries (`governance.standards`, `governance.files`) also take it per
entry, so `reopen` on the security policy does not pin CODEOWNERS too:

| `on_close` | behaviour                                                        |
|------------|------------------------------------------------------------------|
| `accept`   | the close is a quiet exemption for that finding for one year. meles posts one comment saying how to record it in `.meles.yml`, and re-opens early if the drift changes shape (new file, new constraint). Default. |
| `reopen`   | the finding is re-planned on the next run and the PR comes back. For policies that must not be waved away, such as security and actions pinning. |

One PR can carry findings from policies with different `on_close`
values, so a close is handled per finding, not per PR. The `accept`
findings become exemptions and the single comment lists them. The
`reopen` findings come back in a fresh PR on the next run.

meles keeps no state. Each PR body ends with a hidden marker listing the
key, shape and `on_close` of every finding it carries. A finding is
accepted while a closed, unmerged meles PR lists the same key and shape,
the policy still says `accept`, and the close is less than a year old.
Like a `.meles.yml` exemption, it cannot hide a finding for ever. Reopening
the PR, or setting `on_close: reopen`, undoes it early. A PR meles closes
itself is marked `closed_by: meles` and never counts.

## Policy packs

### deps (Renovate / Dependabot)
- exactly one updater configured (flag both, flag neither)
- `renovate.json` extends the shared preset
  (`github>owner/renovate-config`); drift in the preset propagates
  without touching repos
- Renovate app actually installed on the repo (installation API)
- Dependabot alerts enabled regardless of updater choice

### security + governance
- security features: secret scanning, push protection, dependabot
  alerts, private vulnerability reporting
- `governance.standards`: GitHub's community standards (license, security
  policy, code of conduct, contributing, CODEOWNERS, README with badges).
  Keys name the standard, not a file, and GitHub decides whether it is met.
  That covers any location, unusual names, and files inherited from the
  owner's public `.github` repo. A missing one is created from a template
  under GitHub's usual name at the root, unless `create_at:` says otherwise.
- `governance.files`: keys are exact repo paths, for files GitHub does not
  track. Only that path counts.
- Both check existence only. Content is never compared and files carry no
  ownership header, which could break parsers. Later: record a checksum of
  what meles wrote, so meles can tell its own stale copy from a file the
  repo owns.
- repo settings: squash-only, delete branch on merge, no wiki/projects
- ruleset: default branch protected, required checks, signed commits
- `actions.pinning` is its own policy, not a shape constraint: it applies
  to every workflow in every repo, shaped or not. Pins by SHA with a
  version comment, and can be `reopen` so it is never waved away.
- Orgs: prefer org-level rulesets and security configurations. meles sets
  those once and only checks repos. Personal accounts lack these, so the
  per-repo path must exist.

### ci (shapes)
A *shape* is a workflow template plus constraints, keyed by stack
(detected from `go.mod`, `Cargo.toml`, `pyproject.toml`, `package.json`,
`justfile`...). Three ownership levels per workflow file:

| level         | who owns the file | meles does                              |
|---------------|-------------------|-----------------------------------------|
| `full`        | meles             | overwrite on drift via PR               |
| `caller`      | meles             | keep a thin `uses: owner/.github/...` caller in sync |
| `constraints` | repo              | lint only: `permissions`, `timeout-minutes`, `concurrency` |

`caller` is the sweet spot: reusable workflows carry the logic, repos
hold ten lines, meles keeps the ten lines honest.

### release
Convergence does not fit event-driven work, so v1 is "ensure the
release tooling exists": release-please or release-drafter config
matching a template, plus a workflow caller. v2 (hosted mode): meles
writes the notes itself from merged PRs since the last tag, grouped by
conventional-commit type, with an LLM pass for the summary paragraph.

## Config semantics

- `id` names the policy kind. A kind may appear more than once with
  disjoint selectors (two `ci.shape` entries for go and python). An
  exemption in `.meles.yml` references the kind, and optionally one
  `subject` (as `meles plan` prints it). `reason` and `until` are required;
  `until` is inclusive. Exempt findings stay in the plan as `skip`.
- Selectors are evaluated against facts, so `files: [go.mod]` is a
  marker-file match, not a language guess from GitHub's linguist.
- Selectors exclude archived repos unless they say `archived: true`.
  Archived repos reject every write.
- Selector fields combine with AND; items in a list combine with OR, so
  `files: [go.mod, go.work]` matches either. `files` entries are tree
  paths or `path.Match` globs; `name` is a glob.
- Templates and shapes are paths relative to the control repo. The PR
  body cites the control-repo commit they were rendered from.

## Surfaces

The PR body is the primary UI. Most people meet meles through a PR, not
a dashboard, so each PR section names the policy, says why, and cites
the template commit. The CLI table and the Actions job summary carry the
same data. A web dashboard is optional and not in v1. When it exists,
its default filter is "has findings": a matrix of 42 green rows is noise.

## Prior art and where meles sits

- **OSSF Allstar**: closest. App-based policy enforcement, but mostly
  opens issues and is security-only.
- **GitHub safe-settings**: settings-as-code for orgs, Probot. No file
  content, no CI shapes.
- **multi-gitter**: bulk PRs from a script. No policy model, no
  convergence.
- **repolinter / scorecard**: checks, no remediation.

meles = Allstar's install model + safe-settings' config + multi-gitter's
PR remediation + stack-aware CI shapes + release hygiene.

## Hazards found while thinking

- PRs that touch `.github/workflows/*` need the App's `workflows: write`
  permission (or `workflow` scope on a PAT). Easy to miss.
- PR storms. Cap PRs per run, one branch per repo. Dependabot and
  Renovate both drew complaints for re-opening closed PRs, which is why
  `on_close` defaults to `accept`.
- Renovate config validation: run `renovate-config-validator` on the
  generated file before PRing it.
- Rate limits: 5000 req/h per installation. Fine for hundreds of repos
  with ETag caching; thousands need sharding across runs.

Found while building milestone 2 (verified against the live API):

- "Renovate app installed" cannot be read. `GET /user/installations` is
  403 for `gh` OAuth tokens, and an App token only sees its own App. Needs
  a heuristic (e.g. `renovate[bot]` PRs) if it is wanted.
- `GET vulnerability-alerts` answers 204 with no ETag, so it costs one
  request per repo per run. Every other facts call revalidates for free.
- `GET /rate_limit` reported 0 used for a `gh` OAuth token while the
  `X-RateLimit-Used` response header counted correctly. Trust the header.
- Recursive `git/trees` on larger repos returns 500 intermittently; GETs
  retry twice on 5xx.
- `security_and_analysis` is null for an admin on a private repo without
  GHAS. Treated as unavailable, not drift, so enforce cannot loop on it.
- A stale `renovate-config-validator` rejects newer options already in a
  repo. Validation blocks a change only if the current file passes and the
  changed one fails.
- Community standards come from one GraphQL query per repo (POST, so no
  ETag; it uses the separate GraphQL quota). `codeowners` without `refName`
  returned null for a fork that has a CODEOWNERS file, so the default branch
  is passed explicitly. `GET /community/profile` 404s for forks.
- A missing branch: `git/ref` answers 404, which is never cached and costs
  a request every run. `git/matching-refs` answers `200 []` with an ETag.
- `GET commits/<sha>` reports each changed file's blob SHA, which equals
  `git hash-object` of the content, so "is the branch current?" needs no
  content download.

Found while building milestone 3:

- `vulnerability-alerts` reads flap between the old and new value for
  several seconds after a write. A run straight after a toggle may see the
  old state and write again; the next run settles it.
- `apply` asks for approval like `tofu apply`: only `yes` counts, and
  `--yes` skips the prompt for CI. A stray `--repo` cannot write unseen.

## Decisions

- **Language**: Go. One static binary runs as CLI, GitHub Action and
  webhook server. `go-github` and `ghinstallation` cover App auth.
  `just` for tasks, Apache-2.0.
- **Scale target**: tens to low hundreds of repos across a personal
  account and a few orgs. No sharding in v1.
- **Audience**: the author first, OSS-ready from the start. The App
  Manifest flow lets anyone create their own App; no multi-tenant
  service.
- **Default is dry run**: `meles plan` prints, `meles apply` changes.
- **Files via PR, settings direct**: enforce never writes to a branch.

## Implementation plan

Each milestone ends with tests green and something demoable.

1. **Scaffold**: Go module, `justfile` (`build`, `test`, `lint`,
   `run`), GitHub Actions CI, README with badges, Apache-2.0 LICENSE,
   `meles version`.
2. **Plan, read-only**: config loader and validator for `meles.yml`,
   selectors, inventory via a token from `gh auth token`, facts with
   ETag cache, policy interface, policies (`deps.updater`,
   `security.features`, `governance.standards`, `governance.files`),
   `meles plan` table and `--json`.
3. **Apply**: `FileChange` batched into one PR per repo on the stable
   branch, updated in place; `SettingChange` applied for `enforce`;
   `.meles.yml` exemptions with expiry; `on_close` handling per finding,
   settable per entry.
4. **Run from the control repo**: GitHub App auth via `ghinstallation`,
   a reusable workflow that mints the token and runs `meles apply` on
   cron, job summary report.
5. **Shapes and the rest**: `ci.shape` with the three ownership levels,
   `actions.pinning`, `governance.settings` with rulesets,
   `release.tooling`.
6. **Later**: hosted webhook mode, release notes v2, dashboard.
