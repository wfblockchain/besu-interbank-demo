# Kubernetes manifests — Besu interbank demo

Deploys the whole demo to a cluster: Besu (QBFT), a one-shot contract-deployer
Job, both banks' signing-routers and deposit-svcs, and the web console.

## Layout

| File | Objects |
|------|---------|
| `00-namespace.yaml` | `besu-interbank` namespace |
| `10-besu.yaml` | Besu Deployment + Service (genesis via ConfigMap, validator key via Secret) |
| `20-deployer-job.yaml` | Job that deploys the contracts on-chain (waits for Besu first) |
| `30-routers.yaml` | `router-a` / `router-b` Deployments + Services (each holds only its bank's key) |
| `40-deposit.yaml` | `deposit-a` / `deposit-b` Deployments + Services (initContainer waits for contract code) |
| `50-webui.yaml` | Web console Deployment + NodePort Service (`:30080`) |
| `kustomization.yaml` | Generates the genesis / key / address-book from the repo files |

The chain genesis, validator key, and the **deterministic** address book
(`contracts/deployments/besu.json`) are pulled straight from the repo via
kustomize generators, so they never drift from the rest of the demo.

## Quick start (kind / minikube)

```bash
# from the repo root
make k8s-up          # builds images, (kind) loads them, applies the manifests
# → http://localhost:30080          (NodePort)
#   or: kubectl -n besu-interbank port-forward svc/webui 8080:8080
make k8s-down
```

Manually, without the Makefile:

```bash
docker build -t besu-interbank-svc:latest -f Dockerfile .
docker build -t besu-interbank-deployer:latest -f Dockerfile.deployer .
kind load docker-image besu-interbank-svc:latest besu-interbank-deployer:latest   # kind only
# minikube instead: eval $(minikube docker-env) before the builds

# `..` file refs in the kustomization require LoadRestrictionsNone:
kubectl kustomize --load-restrictor LoadRestrictionsNone deploy/k8s | kubectl apply -f -
```

## Notes

- **Images are built locally** and referenced by tag (`imagePullPolicy: IfNotPresent`).
  For a remote cluster, push them to a registry and edit `images:` in
  `kustomization.yaml`.
- **Besu storage is ephemeral** (`emptyDir`) — this is a throwaway demo chain.
- **All keys are DEMO-ONLY** (well-known Hardhat accounts). The validator key is a
  Secret only for hygiene; it is not sensitive here.
- Ordering is handled without cross-object `depends_on`: the deployer Job and the
  deposit-svc/webui pods use an initContainer that polls Besu until the deposit
  token has code on-chain.
