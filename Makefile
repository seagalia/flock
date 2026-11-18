GINKGO = go run github.com/onsi/ginkgo/v2/ginkgo
# golangci-lint built with the module plugins of .custom-gcl.yml, which the released binary can't load
GOLANGCI_LINT = bin/golangci-lint-custom

# the release binary is built without cgo, so nothing else relies on it either
export CGO_ENABLED = 0

.PHONY: test test-linux lint

# unit tests on the current platform.
# cgo is only enabled here because the race detector needs it on linux.
test:
	CGO_ENABLED=1 $(GINKGO) --randomize-all --race -r

# unit tests on linux, as in production.
# cgo is only enabled here because the race detector needs it on linux.
test-linux:
	docker run --rm \
		-v $(CURDIR):/src/flock \
		-v flock-gomod:/go/pkg/mod \
		-v flock-gocache:/root/.cache/go-build \
		-w /src/flock \
		-e CGO_ENABLED=1 \
		golang:1.27 $(GINKGO) --randomize-all --race -r

# end to end tests, need a linux docker host with ipvs/netfilter kernel modules for the agent suite.
# DOCKER_HOST comes from the environment (.envrc locally).
# testcontainers mounts the docker socket in its helper containers (reaper, host access),
# with colima the socket path inside the vm is /var/run/docker.sock and not the one on the mac.
.PHONY: e2e e2e-controller e2e-agent e2e-agent-f5 e2e-agent-f5-lab e2e-stack e2e-provisioner e2e-waf e2e-dev e2e-load e2e-clean
e2e: e2e-controller e2e-agent e2e-agent-f5 e2e-stack e2e-provisioner e2e-waf e2e-dev

e2e-controller: export TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE ?= /var/run/docker.sock
e2e-controller:
	$(GINKGO) -tags e2e -v --timeout 45m $(E2E_FLAGS) ./e2e/controller/...

e2e-agent: export TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE ?= /var/run/docker.sock
e2e-agent:
	$(GINKGO) -tags e2e -v --timeout 45m $(E2E_FLAGS) ./e2e/agent/starling/...

# the F5 agent on the host against a mock BIG-IP pair, no container; the lab specs skip without F5_LAB_*
e2e-agent-f5:
	$(GINKGO) -tags e2e -v --timeout 15m $(E2E_FLAGS) ./e2e/agent/f5/...

# the lab specs against a real BIG-IP: dry runs of the golden declarations, iRule compilation, one deployment
# (refused when unlicensed). The password is read at run time and never printed.
F5_LAB_URL ?= https://10.17.1.215:8443
F5_LAB_PASSWORD_CMD ?= ssh nuc 'cat ~/homelab/bigip/.admin-password'
e2e-agent-f5-lab:
	F5_LAB_URL=$(F5_LAB_URL) F5_LAB_PASSWORD="$$($(F5_LAB_PASSWORD_CMD))" \
		$(GINKGO) -tags e2e -v --timeout 20m --label-filter lab $(E2E_FLAGS) ./e2e/agent/f5/...

# a real controller with a real starling agent, driven through the cli and the api
e2e-stack: export TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE ?= /var/run/docker.sock
e2e-stack:
	$(GINKGO) -tags e2e -v --timeout 30m $(E2E_FLAGS) ./e2e/stack/...

# the acme provisioner against a real controller and pebble, the test CA of Let's Encrypt, and the docker provisioner
# exposing labelled containers through a real agent
e2e-provisioner: export TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE ?= /var/run/docker.sock
e2e-provisioner:
	$(GINKGO) -tags e2e -v --timeout 20m $(E2E_FLAGS) ./e2e/provisioner/...

# the WAF (agent/starling/waf) behind a real envoy, configs registered through its client
e2e-waf: export TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE ?= /var/run/docker.sock
e2e-waf:
	$(GINKGO) -tags e2e -v --timeout 20m $(E2E_FLAGS) ./e2e/waf/...

# flock dev in an unprivileged container, the quickstart served through its envoy
e2e-dev: export TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE ?= /var/run/docker.sock
e2e-dev:
	$(GINKGO) -tags e2e -v --timeout 15m $(E2E_FLAGS) ./e2e/dev/...

# memory of a real controller and a real agent under load, report in e2e/.build/load-report.md (knobs: LOAD_SCALE,
# LOAD_*); its own build tag keeps it out of make e2e
e2e-load: export TESTCONTAINERS_DOCKER_SOCKET_OVERRIDE ?= /var/run/docker.sock
e2e-load:
	$(GINKGO) -tags e2e,load -v --timeout 90m $(E2E_FLAGS) ./e2e/load/

# removes every e2e image (label flock-e2e), the next run rebuilds them
e2e-clean:
	ids=$$(docker images -q --filter label=flock-e2e=true); [ -z "$$ids" ] || docker image rm -f $$ids
	rm -rf e2e/.build

$(GOLANGCI_LINT): .custom-gcl.yml
	golangci-lint custom --destination bin --name golangci-lint-custom

lint: $(GOLANGCI_LINT)
	$(GOLANGCI_LINT) run ./...

# the dashboard served by the controller at /dashboard, embedded from webui/dist by webui/embed.go.
# Without it the binary still builds, /dashboard then tells to run make webui.
.PHONY: webui webui-dev webui-test docker docker-slim docker-waf
# scripts and styles are kept gzipped only, which takes 3 MB off the binary, webui.Mount serves them as they are
webui:
	cd webui && npm ci && npm run build
	find webui/dist/app/browser -type f \( -name '*.js' -o -name '*.css' \) -exec gzip -9 -n {} +

# the flock image: the binary on top of envoy, runs flock dev by default
docker:
	docker build -f flock.Dockerfile --build-arg VERSION=$$(git describe --tags 2>/dev/null || echo 0.0.0-dev) --build-arg COMMIT=$$(git rev-parse HEAD) --build-arg DATE=$$(date -u +%Y-%m-%dT%H:%M:%SZ) -t flock:dev .

# the flock image without envoy, for the controller, the provisioner and the F5 agent
docker-slim:
	docker build -f flock.Dockerfile --target slim --build-arg VERSION=$$(git describe --tags 2>/dev/null || echo 0.0.0-dev) --build-arg COMMIT=$$(git rev-parse HEAD) --build-arg DATE=$$(date -u +%Y-%m-%dT%H:%M:%SZ) -t flock:dev-slim .

# the standalone WAF, built with cgo and libre2 (tag re2_cgo)
docker-waf:
	docker build -f starling-waf.Dockerfile --build-arg VERSION=$$(git describe --tags --always) --build-arg COMMIT=$$(git rev-parse HEAD) --build-arg DATE=$$(date -u +%Y-%m-%dT%H:%M:%SZ) -t starling-waf:dev .

# angular dev server on http://localhost:4200/dashboard/, the api goes to the controller of flock dev
webui-dev:
	cd webui && npm start

webui-test:
	cd webui && npm run lint && npm test -- --watch=false

# regenerate everything derived from the protos in api/
.PHONY: generate api-lint
# outputs are removed first, otherwise those of a deleted proto would survive unnoticed
generate:
	find sdk/flock \( -name '*.pb.go' -o -name '*.connect.go' \) -delete
	rm -rf cli/protoset/protoset.bin webui/src/gen
	cd api && buf generate
	# explain reads proto comments, which only the protoset carries
	buf build api -o cli/protoset/protoset.bin
	# protobuf-es strips comments, the webui reads the field docs and extension kinds from this file
	go run ./webui/protodocs webui/src/app/core/proto-docs.gen.ts
	# protoc-gen-crd compiles the sdk generated above, so it runs from its own template once that code is written
	rm -f provisioners/kubernetes/api/v1alpha1/zz_generated.*.spec.go provisioners/kubernetes/api/v1alpha1/zz_generated.*.convert.go
	cd api && buf generate --template buf.gen.crd.yaml
	$(MAKE) -C provisioners/kubernetes generate manifests

api-lint:
	cd api && buf lint

