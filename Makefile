X := mise exec --
CONTAINER ?= docker
RUNNER_API_URL ?= http://host.docker.internal:8787
export KIND_EXPERIMENTAL_PROVIDER := $(filter podman,$(CONTAINER))

.PHONY: gen test cluster image api web login

gen:
	$(X) buf generate

test:
	$(X) go test ./...

cluster:
	$(X) kind get clusters | grep -qx banda || $(X) kind create cluster --config deploy/kind.yaml
	$(X) kubectl apply -f https://github.com/kubernetes-sigs/agent-sandbox/releases/download/v1.0.7/sandbox.yaml
	$(X) kubectl apply -f deploy/postgres.yaml
	$(X) kubectl rollout status statefulset/postgres -n banda --timeout=300s

image:
	$(CONTAINER) build -f deploy/sandbox.Dockerfile -t banda-sandbox:dev .
	$(CONTAINER) save banda-sandbox:dev -o /tmp/banda-sandbox.tar
	$(X) kind load image-archive /tmp/banda-sandbox.tar --name banda

api:
	RUNNER_API_URL=$(RUNNER_API_URL) $(X) go run ./cmd/api

web:
	$(X) pnpm -C web dev

login:
	$(X) kubectl exec -it -n banda $(SANDBOX) -- claude auth login
