# meles

[![CI](https://github.com/temikus/meles/actions/workflows/ci.yml/badge.svg)](https://github.com/temikus/meles/actions/workflows/ci.yml)
[![License](https://img.shields.io/github/license/temikus/meles)](LICENSE)
[![codecov](https://codecov.io/gh/temikus/meles/graph/badge.svg)](https://codecov.io/gh/temikus/meles)

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

```sh
meles version
```

## Development

Requires Go (see `go.mod`), [just](https://github.com/casey/just) and
[golangci-lint](https://golangci-lint.run) v2. With
[mise](https://mise.jdx.dev), `mise install` sets up the pinned versions.

```sh
just build              # bin/meles
just test               # unit tests, race detector, coverage
just lint
just run version        # go run with arguments
just test-integration   # real GitHub API, needs MELES_IT_REPO and a token
```

## License

[Apache-2.0](LICENSE)
