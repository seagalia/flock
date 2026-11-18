<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/logo-dark.svg">
  <img alt="flock" src="assets/logo.svg">
</picture>

# flock

> [!WARNING]
> flock is a work in progress. The API, the configuration and the resources still change without notice and with no
> migration path, and no release is fit for production yet.

flock is a flexible load balancer orchestrator. One binary, `flock`, holds both programs:

- **controller**: implements the [API](api) over gRPC/mTLS,
  stores resources and pushes them to agents with an xDS-style delta protocol.
- **starling agent**: the original agent implementation. It announces VIPs with ARP/NDP or BGP, load balances udp and tcp
  with [ipvs](http://www.linuxvirtualserver.org/software/ipvs.html) and tls over tcp and http/https with
  [envoy](https://www.envoyproxy.io/).

The Go SDK both of them are built on (generated API code, agent SDK, storers) lives in `sdk/`.

The documentation lives on [flock.seagalia.io](https://flock.seagalia.io), built from its own repository. Start with
the [quickstart](https://flock.seagalia.io/start/quickstart/).

## Usage

```bash
flock controller serve -c ./config.yml       # run the controller
flock agent starling start -c ./config.yml   # run the starling agent
flock agent starling clean -c ./config.yml   # remove everything the agent created on the host
flock agent f5 start -c ./config.yml         # run the F5 BIG-IP bridge agent
flock version                                # print the version, commit and build date
```

`-c/--config-path` defaults to `./config.yml`. Sample configurations:
[controller](controller/config.sample.yml), [starling agent](agent/starling/config.sample.yml),
[F5 agent](agent/f5/config.sample.yml).
Configuration references: [controller](https://flock.seagalia.io/reference/controller-configuration/),
[starling agent](https://flock.seagalia.io/agents/starling/reference/configuration/),
[F5 agent](https://flock.seagalia.io/agents/f5/reference/configuration/).

## Layout

```
main.go             the flock command tree
cli/                the command line of the controller (resources, login, TUIs, dashboard proxy)
api/                protobuf/gRPC definitions of the API (buf module)
controller/         the controller
agent/starling/     the starling agent
agent/f5/           the F5 BIG-IP agent (AS3)
provisioners/       the provisioners: acme certificates, docker, kubernetes
webui/              the dashboard (Angular), embedded in the binary
devenv/             flock dev, a local controller and agent with no config
sdk/                generated API code (sdk/flock) and shared libraries
e2e/                end-to-end suites (controller, starling agent, stack, provisioner, ...)
assets/             packaging files (systemd, logrotate, sysctl, sample configs)
docs/               architecture diagrams and the example manifests the cli specs read
```

## API

The API is defined in protobuf under [`api/`](api):

- `api/flock/api/config/<domain>/v1`: resources (virtual servers, backends, vips, routes, certificates, …);
- `api/flock/services/<domain>/v1`: the gRPC services of the controller, `services/agent/*` the ones of the agents;
- `api/flock/extensions/<area>/<impl>/v1`: implementation-specific options, packed into the `typed_options`
  `google.protobuf.Any` fields of the resources, so the core models stay implementation agnostic.

After changing a `.proto`, regenerate and commit the result with it:

```bash
make generate    # Go code in sdk/flock, webui TypeScript, cli/protoset
make api-lint    # buf lint, not enforced yet
```

`make generate` needs [buf](https://buf.build/docs/installation). CI fails when the generated code is out of date.

Docs: [protobuf reference](https://flock.seagalia.io/reference/proto/), generated from the proto comments by the
documentation site, architecture diagrams in [`docs/api`](docs/api).

Each agent implementation names its extension packages after itself (`flock.extensions.<area>.<agent>_<kind>.v1`), so
the starling agent of `agent/starling/` has `starling_*` packages.

## Development

```bash
go build .
make test          # unit tests (Ginkgo), on the current platform
make test-linux    # unit tests in a linux container, as in production
make lint          # golangci-lint
make e2e           # both e2e suites, needs colima as the docker context
make e2e-controller E2E_FLAGS='--focus "some describe text"'
make e2e-agent
# keep the containers for 10 minutes when a spec fails, to inspect them with docker exec
E2E_PAUSE_ON_FAILURE=10m make e2e-controller E2E_FLAGS='--focus "some describe text"'
```

The agent e2e suite needs a linux docker host with the ipvs/netfilter kernel modules.

Commits follow [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/). We use
[pre-commit](https://pre-commit.com/) to run the linters before each commit:

```bash
pip install pre-commit
pre-commit install
```
