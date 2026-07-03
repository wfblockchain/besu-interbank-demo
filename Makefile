# Besu interbank demo — build / run helpers.
.PHONY: help build local up down demo logs k8s-up k8s-down k8s-render test

help:
	@echo "Local (host binaries):"
	@echo "  make local        build Go binaries and run the CLI demo (needs chain up + contracts deployed)"
	@echo "Docker (full stack):"
	@echo "  make up           build images + start chain, contracts, services, web console  (http://localhost:8080)"
	@echo "  make demo         run the one-shot CLI scenario against the running stack"
	@echo "  make logs         tail all service logs"
	@echo "  make down         stop and remove everything (incl. volumes)"
	@echo "Kubernetes:"
	@echo "  make k8s-render   render manifests to stdout"
	@echo "  make k8s-up       build + load images, then apply manifests"
	@echo "  make k8s-down     delete the namespace"
	@echo "Contracts:"
	@echo "  make test         forge test the contracts (135 tests)"

# ── Local ──────────────────────────────────────────────────────────────────────
local:
	go build -o bin/ ./cmd/...
	./bin/demo

# ── Docker ─────────────────────────────────────────────────────────────────────
up:
	docker compose up --build -d
	@echo "→ Web console:  http://localhost:8080    (RPC http://localhost:8545)"

demo:
	docker compose --profile cli run --rm demo

logs:
	docker compose logs -f --tail=40

down:
	docker compose down -v

# ── Kubernetes ─────────────────────────────────────────────────────────────────
# `..` file refs in the kustomization require LoadRestrictionsNone.
KUSTOMIZE = kubectl kustomize --load-restrictor LoadRestrictionsNone deploy/k8s

k8s-render:
	$(KUSTOMIZE)

# Builds the two images and (for kind) loads them into the cluster before applying.
# For minikube use `eval $$(minikube docker-env)` before `make k8s-up`; for a
# remote cluster, push the images and edit deploy/k8s/kustomization.yaml.
k8s-up:
	docker build -t besu-interbank-svc:latest -f Dockerfile .
	docker build -t besu-interbank-deployer:latest -f Dockerfile.deployer .
	- kind load docker-image besu-interbank-svc:latest besu-interbank-deployer:latest 2>/dev/null
	$(KUSTOMIZE) | kubectl apply -f -
	@echo "→ Web console:  http://localhost:30080   (NodePort)  or  kubectl -n besu-interbank port-forward svc/webui 8080:8080"

k8s-down:
	kubectl delete namespace besu-interbank --ignore-not-found

# ── Contracts ──────────────────────────────────────────────────────────────────
test:
	cd contracts && docker run --rm -v "$$PWD":/w -w /w ghcr.io/foundry-rs/foundry:stable "forge test"
