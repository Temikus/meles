# meles

[![CI](https://github.com/temikus/meles/actions/workflows/ci.yml/badge.svg)](https://github.com/temikus/meles/actions/workflows/ci.yml)
[![License](https://img.shields.io/github/license/temikus/meles)](LICENSE)

Badgers keep the sett tidy. meles keeps GitHub repos tidy.

meles is config management for GitHub repos. You declare the desired state
once in a control repo, and meles converges every repo toward it: files go
through one pull request per repo, settings are applied directly when you
allow it. `meles plan` is the default and never writes.

Status: early development. See [docs/DESIGN.md](docs/DESIGN.md) for the
model and the implementation plan, and [examples/](examples/) for the
config surface.

## Install

```sh
go install github.com/temikus/meles/cmd/meles@latest
```

## Usage

meles reads `meles.yml` from your control repo. See
[examples/meles.yml](examples/meles.yml) for the full surface.

```sh
meles validate -c meles.yml          # check the config, no API calls
meles plan -c meles.yml              # what meles would change; never writes
meles plan --owner my-org --json     # another owner, machine-readable
meles apply -c meles.yml --repo 'me/sandbox'   # make the changes, here for one repo
meles apply --yes                    # no approval prompt, for CI
meles version
```

`apply` puts every file change for a repo into one PR on
`meles/housekeeping` and updates it in place. Settings change directly,
but only for policies with `mode: enforce`. `apply` shows what it will do
and asks for `yes` first, unless you pass `--yes`. A second run with no
drift makes no writes. A repo opts out in its own `.meles.yml`; see
[examples/.meles.yml](examples/.meles.yml).

`plan` is the default command. The token comes from `GH_TOKEN`,
`GITHUB_TOKEN`, or `gh auth token`. With `MELES_APP_ID` and
`MELES_APP_PRIVATE_KEY` (the PEM contents) set, meles signs in as that
GitHub App instead. Responses are cached by ETag under
your user cache directory, so repeat runs cost little rate limit.

Implemented policies: `deps.updater`, `security.features`,
`governance.standards`, `governance.files`. Other kinds in the example are accepted and skipped
with a warning until their milestone lands.

## Development

Requires Go (see `go.mod`), [just](https://github.com/casey/just) and
[golangci-lint](https://golangci-lint.run) v2. With
[mise](https://mise.jdx.dev), `mise install` sets up the pinned versions.

```sh
just build              # bin/meles
just test               # unit tests, race detector, coverage
just lint
just run version        # go run with arguments
just test-integration   # real GitHub API against a throwaway repo (default Temikus/meles-sandbox)
```

## License

[Apache-2.0](LICENSE)
