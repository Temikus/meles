# meles design notes (2026-10-14)

Status: design agreed, no code yet. Prototype screens live in the Paper
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
2. A control repo (`<owner>/meles` or the `.github` repo) holds
   `meles.yml`, templates and shapes.
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
means. `on_close` is set per policy, with a global default:

| `on_close` | behaviour                                                        |
|------------|------------------------------------------------------------------|
| `accept`   | the close is a quiet exemption for that finding. meles posts one comment saying how to make it permanent in `.meles.yml`, and only re-opens if the drift changes shape (new file, new constraint). Default. |
| `reopen`   | the finding is re-planned on the next run and the PR comes back. For policies that must not be waved away, such as security and actions pinning. |

One PR can carry findings from policies with different `on_close`
values, so a close is handled per finding, not per PR. The `accept`
findings become exemptions and the single comment lists them. The
`reopen` findings come back in a fresh PR on the next run.

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
- required files from templates: LICENSE, SECURITY.md, CODEOWNERS,
  CODE_OF_CONDUCT.md; README has required badges
- `governance.files` checks existence only. A file counts as present in any
  location GitHub reads it from (root, `.github/`, `docs/`); a missing file
  is created at the root unless `path:` says otherwise. Content is never
  compared and files carry no ownership header, which could break parsers.
  Later: record a checksum of what meles wrote, so meles can tell its own
  stale copy from a file the repo owns.
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
  exemption in `.meles.yml` references the kind.
- Selectors are evaluated against facts, so `files: [go.mod]` is a
  marker-file match, not a language guess from GitHub's linguist.
- Selectors exclude archived repos unless they say `archived: true`.
  Archived repos reject every write.
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
   ETag cache, policy interface, three policies (`deps.updater`,
   `security.features`, `governance.files`), `meles plan` table and
   `--json`.
3. **Apply**: `FileChange` batched into one PR per repo on the stable
   branch, updated in place; `SettingChange` applied for `enforce`;
   `.meles.yml` exemptions with expiry; `on_close` handling per finding.
4. **Run from the control repo**: GitHub App auth via `ghinstallation`,
   a reusable workflow that mints the token and runs `meles apply` on
   cron, job summary report.
5. **Shapes and the rest**: `ci.shape` with the three ownership levels,
   `actions.pinning`, `governance.settings` with rulesets,
   `release.tooling`.
6. **Later**: hosted webhook mode, release notes v2, dashboard.
