# Contributing to Flock

Thank you for your interest in Flock. This guide covers code contributions to the controller, the starling agent, the
command line, the API and the dashboard, and documentation changes in this repository.

Before you contribute, read the [code of conduct](CODE_OF_CONDUCT.md). It applies to every issue, pull request, and
discussion of the project.

The user documentation lives on [flock.seagalia.io](https://flock.seagalia.io), built from its own repository.

## Flock overview

Flock is a flexible load balancer orchestrator in one Go module, `github.com/seagalia/flock`, and one binary, `flock`.
The [README](README.md) lists what each directory holds. In short:

| Directory         | What it holds                                                                                  |
|-------------------|------------------------------------------------------------------------------------------------|
| `cli/`            | The command line of the controller, whose commands sit at the root of the `flock` command tree |
| `controller/`     | The controller, which implements the API and pushes resources to the agents                    |
| `agent/starling/` | The starling agent, one per load-balancer node (IPVS, envoy, ARP/NDP or BGP)                   |
| `webui/`          | The dashboard (Angular), embedded in the binary and served at `/dashboard/`                    |
| `api/`            | The protobuf definitions of the API, a buf module                                              |
| `sdk/`            | The shared Go SDK, including the code generated from `api/` in `sdk/flock/`                    |
| `e2e/`            | The end-to-end suites                                                                          |

## Before you start

Install these tools before you build or test Flock:

- Go, at the version of the `go` line of `go.mod`.
- Node.js 24 (`.nvmrc`), only for the dashboard.
- [buf](https://buf.build/docs/installation), only to run `make generate` after a change in `api/`.
- Docker, to run the unit tests in a Linux container and the end-to-end suites. On macOS the project uses colima as the
  Docker context.
- golangci-lint v2 and [pre-commit](https://pre-commit.com/).

The controller and the starling agent run on Linux only. Their packages still compile on macOS, because the Linux-only
code lives in `*_linux.go` files with no-op stubs in `*_others.go`. Run `make test-linux` for the tests that matter on
Linux, especially the agent ones.

## Environment setup

1. Clone the repository.

    ```bash
    git clone https://github.com/seagalia/flock.git
    cd flock
    ```

2. Install the pre-commit hooks, which run golangci-lint and the YAML and whitespace fixers before each commit.

    ```bash
    pip install pre-commit
    pre-commit install
    ```

3. Build the binary.

    ```bash
    go build .
    ```

4. If you need the dashboard, then build it and build the binary again, so the controller serves it at `/dashboard/`.

    ```bash
    make webui
    go build .
    ```

   Without this step `/dashboard/` answers `503`. Only `webui/dist/.gitkeep` is committed, so the binary builds without
   Node.js.

5. Run a local controller and agent with no configuration.

    ```bash
    ./flock dev
    ```

   `flock dev` keeps a SQLite store in `~/.flock/dev/store.db` and logs the command line in as `admin`. While it runs,
   the commands of another terminal use its session, so `./flock vips` works at once.

6. If you change the dashboard, then start its development server in a second terminal while `flock dev` runs.

    ```bash
    make webui-dev
    ```

   It serves the dashboard on `http://localhost:4200/dashboard/` and proxies the API to the `flock dev` controller.

### Troubleshoot

If colima fails with the error `failed to run attach disk`, unlock its disk:

```bash
LIMA_HOME=$HOME/.colima/_lima limactl disk unlock colima
```

If an end-to-end suite can't reach Docker, set `DOCKER_HOST` to the colima socket, which is
`unix://${HOME}/.colima/default/docker.sock` for the default colima profile.

## Test your change

The unit tests use Ginkgo v2 and Gomega. Each package has a `*_suite_test.go`.

| Command               | What it runs                                                                                                |
|-----------------------|-------------------------------------------------------------------------------------------------------------|
| `make test`           | Every unit test on the current platform, with the race detector                                             |
| `make test-linux`     | The same tests in a `golang:1.27` container, as in production                                               |
| `make lint`           | golangci-lint, with the configuration CI uses                                                               |
| `make webui-test`     | The lint and prettier checks of the dashboard, then its vitest tests                                        |
| `make e2e`            | The controller, agent and stack end-to-end suites                                                           |
| `make e2e-controller` | A real controller with SQLite, PostgreSQL or MySQL, NATS, an OAuth 2.0 mock, and OpenLDAP, with fake agents |
| `make e2e-agent`      | A real agent and envoy in a privileged container, fed by a fake controller                                  |
| `make e2e-stack`      | The command line and the API against a real controller with a real agent                                    |
| `make e2e-load`       | The memory of a real controller and agent under load, kept out of `make e2e`                                |
| `make e2e-clean`      | Removes every end-to-end image, so the next run rebuilds them                                               |

To run one package or one spec, call Ginkgo directly:

```bash
go run github.com/onsi/ginkgo/v2/ginkgo -r ./controller/serves
go run github.com/onsi/ginkgo/v2/ginkgo --focus "some describe text" ./agent/starling/backendctl
make e2e-controller E2E_FLAGS='--focus "some describe text"'
```

To keep the containers of a failed end-to-end spec for 10 minutes, so you can inspect them with `docker exec`, set
`E2E_PAUSE_ON_FAILURE`:

```bash
E2E_PAUSE_ON_FAILURE=10m make e2e-agent E2E_FLAGS='--focus "TLS"'
```

The agent suite needs a Linux Docker host with the IPVS and netfilter kernel modules.

Everything builds with `CGO_ENABLED=0`, and the Makefile exports it. Only the unit tests turn cgo on, because the race
detector needs it on Linux. Never add a dependency that needs cgo. SQLite comes from the pure Go `modernc.org/sqlite`.

## Change the API

The API is defined in protobuf under `api/`. After you change a `.proto`, regenerate everything derived from it and
commit the result with the proto change:

```bash
make generate
make api-lint
```

`make generate` writes the Go code in `sdk/flock/`, the TypeScript of the dashboard in `webui/src/gen/` and the
descriptor set `cli/protoset/protoset.bin`. The protobuf reference of the documentation site is built from the proto
comments. CI runs it again and fails on any difference, and it also runs `make api-lint`.

Follow these rules when you change the API:

- Never edit a generated file by hand. That covers `*.pb.go`, `webui/src/gen/` and `cli/protoset/protoset.bin`. The
  diagrams in `docs/api/` are hand-authored.
- Never add a field specific to one agent implementation to `flock/api/config/*`. Define a message under
  `flock/extensions/<area>/<impl>/v1` and name it in the comment of the `google.protobuf.Any typed_options` field it's
  packed into. An option every implementation shares is a real field of the config model.
- Write validation rules as protovalidate options. The RPC server applies the defaults and validates every request
  before a handler sees it, so handlers neither default nor validate their request.
- Add a new version as a sibling directory, `v2/` next to `v1/`. Never edit `v1/` into a new version.
- Breaking wire changes are allowed for now, because nothing deployed needs to stay compatible.

`buf lint` runs with the STANDARD rules. The naming rules users would see a rename of are turned off in `api/buf.yaml`,
so follow the existing style rather than those rules.

## Naming rules

- Each agent implementation has its own name, and its extension packages are named after it:
  `flock.extensions.<area>.<agent>_<kind>.v1`. The starling agent (`agent/starling/`) has
  `flock.extensions.*.starling_*.v1`, with messages such as `StarlingAnnounce` or `HttpOptions`.

## Code conventions

- Log with zerolog. Get a component logger with `logging.Component("<name>")` from `sdk/logging`, and assign it to a
  variable before you call `.Info()`. Every log event ends with `Msg`, `Msgf` or `Send`, which `zerologlint` checks. The
  command line prints its user output through `github.com/ArthurHlt/messages` instead.
- Return errors that cross the RPC boundary as `connect.Error` values. Store errors go through
  `errors.ToConnectError(err)` from `sdk/errors`.
- Use the connect-go v2 API (`connectrpc.com/connect/v2`). grpc-go is allowed only where a library needs it: the agent's
  envoy xDS server.
- Regenerate the gomock mocks with `go generate ./...` after you change a mocked interface.
- If you change a command or a flag named in `cli/skill/SKILL.md`, then update that skill too.

## Contribution workflow

### Report issues and bugs

Open an issue in the [GitHub issues of Flock](https://github.com/seagalia/flock/issues). Give the version
(`flock version`), the command or configuration that fails, and what you expected.

<!-- TODO: the repository has no issue template in .github/. Add one, or describe here what a bug report must contain. -->

### Commit messages

Commits follow [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/), for example `feat(agent): …`,
`fix(controller): …` or `docs: …`. Mark a breaking change with `!` after the type and a `BREAKING CHANGE:` footer.

### Pull requests

1. Fork the repository and create a branch from `main`.
2. Make your change, and add its tests and its documentation.
3. If you changed a `.proto`, then run `make generate` and commit the generated files in the same commit.
4. Run `make lint` and `make test`, and `make test-linux` for agent code.
5. Open a pull request against `main`.

CI runs the lint, the unit tests, the dashboard checks and the generated-code check on every pull request. The
end-to-end workflow runs on pull requests that change the Go code, the end-to-end suites or the build files.

<!-- TODO: there is no pull request template and no branch naming rule in the repository. Add them here once they exist. -->

### Releases

A tag `v*` pushed to the repository runs goreleaser, which publishes the binaries for Linux, macOS and Windows on amd64
and arm64 to the GitHub releases, with `checksums.txt`. `flock self-update` and `bin/install.sh` download those
archives, so keep their names (`flock_<version>_<os>_<arch>.tar.gz`, `.zip` on Windows).

## Documentation

The user documentation, configuration references and internals included, lives on the documentation site
([flock.seagalia.io](https://flock.seagalia.io)), built from its own repository. A change of a configuration key, a
flag or a component goes with a change of its page there. The protobuf reference of the site is generated from the
proto comments, so fix it in `api/`. This repository only keeps the architecture diagrams of `docs/api/` and the example
manifests of `docs/cli/examples/`, which the cli specs read.
