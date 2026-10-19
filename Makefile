X := mise exec --
CONTAINER ?= podman
ifeq ($(CONTAINER),podman)
export KIND_EXPERIMENTAL_PROVIDER := podman
export CONTAINER_CONNECTION ?= banda-root
RUNNER_API_URL ?= http://host.containers.internal:8787
else
RUNNER_API_URL ?= http://host.docker.internal:8787
endif
TARGET ?= sandbox
IMAGE ?= localhost/banda-sandbox:dev
# `make api AUTH=dev` skips sign-in. Otherwise the API signs in against Dex (set OIDC_* to use another provider).
ifeq ($(AUTH),dev)
export DEV_AUTH := 1
else
export OIDC_ISSUER ?= http://localhost:5556/dex
export OIDC_CLIENT_ID ?= banda
export OIDC_CLIENT_SECRET ?= banda-dev-secret
export PUBLIC_URL ?= http://localhost:5173
endif

.PHONY: gen test cluster image api web login e2e deploy

gen:
	$(X) buf generate

test:
	$(X) go test ./...

cluster:
	$(X) kind get clusters | grep -qx banda || $(X) kind create cluster --config deploy/kind.yaml
	$(X) kubectl --context kind-banda apply -f https://github.com/kubernetes-sigs/agent-sandbox/releases/download/v1.0.7/sandbox.yaml
	$(X) kubectl --context kind-banda apply -f deploy/postgres.yaml
	$(X) kubectl --context kind-banda rollout status statefulset/postgres -n banda --timeout=300s
	$(X) kubectl --context kind-banda apply -f deploy/dex.yaml
	$(X) kubectl --context kind-banda rollout status deployment/dex -n banda --timeout=300s

image:
	$(CONTAINER) build -f deploy/sandbox.Dockerfile --target $(TARGET) -t $(IMAGE) .
	$(CONTAINER) save $(IMAGE) -o /tmp/banda-sandbox.tar
	$(X) kind load image-archive /tmp/banda-sandbox.tar --name banda

api:
	RUNNER_API_URL=$(RUNNER_API_URL) SANDBOX_IMAGE=$(IMAGE) $(X) go run ./cmd/api

web:
	$(X) pnpm -C web dev

login:
	$(X) kubectl --context kind-banda exec -it -n banda $(SANDBOX) -- claude auth login

e2e:
	deploy/e2e.sh

deploy:
	$(CONTAINER) build -f deploy/api.Dockerfile -t localhost/banda-api:dev .
	$(CONTAINER) save localhost/banda-api:dev -o /tmp/banda-api.tar
	$(X) kind load image-archive /tmp/banda-api.tar --name banda
	$(X) kubectl --context kind-banda apply -f deploy/banda.yaml
	$(X) kubectl --context kind-banda rollout restart -n banda deployment/banda-api
