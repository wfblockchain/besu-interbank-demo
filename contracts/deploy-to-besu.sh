#!/usr/bin/env bash
# deploy-to-besu.sh — one-shot deploy of the interbank stablecoin stack to the
# local Besu QBFT network, then runs the DeployDemo script.
#
# The canonical CREATE2 deployer (0x4e59…56C, Arachnid's
# deterministic-deployment-proxy) that the vendored template deploys its proxies
# through is pre-deployed in genesis (see chain/genesis/genesis.json alloc), so
# no bootstrap is needed here.
#
# Run from inside the Foundry container (the demo scripts do this for you):
#   docker run --rm --network host -v "$PWD":/w -w /w \
#     ghcr.io/foundry-rs/foundry:stable "bash deploy-to-besu.sh"
set -euo pipefail

RPC="${RPC_URL:-http://localhost:8545}"
# DEMO-ONLY Bank A / Hardhat #0 — the issuer & deployer.
BANKA_PK="${BANKA_PK:-0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80}"

CREATE2_DEPLOYER=0x4e59b44847b379578588920cA78FbF26c0B4956C
echo "→ Checking canonical CREATE2 deployer (pre-deployed in genesis)…"
CODE=$(cast code "$CREATE2_DEPLOYER" --rpc-url "$RPC" 2>/dev/null || echo 0x)
[[ "$CODE" != "0x" && -n "$CODE" ]] && echo "  ✓ present at $CREATE2_DEPLOYER" \
  || { echo "  ✗ missing — is the Besu chain up with the demo genesis?"; exit 1; }

# Idempotency: the deploy is deterministic (CREATE2 + fixed salts). If the
# deposit token already has code, a re-run would revert (address occupied), so
# skip and just (re)publish the committed address book. Makes `docker compose up`
# safely re-runnable without a `down -v`.
DEPOSIT_TOKEN=0xA4ADE457d211c4dB066E6e37BdF865Cc06fCF368
DTCODE=$(cast code "$DEPOSIT_TOKEN" --rpc-url "$RPC" 2>/dev/null || echo 0x)
if [[ "$DTCODE" != "0x" && -n "$DTCODE" ]]; then
  echo "→ Contracts already deployed (deposit token has code) — skipping deploy."
  [[ -n "${DEPLOYMENT_OUT:-}" ]] && cp deployments/besu.json "$DEPLOYMENT_OUT" && echo "→ Published besu.json to $DEPLOYMENT_OUT"
  cat deployments/besu.json
  exit 0
fi

echo "→ Deploying interbank stablecoin stack (Bank A = issuer)…"
mkdir -p deployments
# --gas-estimate-multiplier 200: Besu's eth_estimateGas under-provisions the
# CREATE2 factory calls (the EVM 63/64 gas-forwarding rule starves the inner
# CREATE2), so estimates that pass in replay run out of gas when mined. Doubling
# the estimate gives headroom; the chain's block gas limit is effectively
# unbounded for this demo.
forge script scripts/DeployDemo.s.sol:DeployDemo \
  --sig 'deploy()' \
  --rpc-url "$RPC" \
  --private-key "$BANKA_PK" \
  --gas-estimate-multiplier 200 \
  --broadcast --slow

# Publish to a shared location for containerized consumers (compose volume /
# k8s emptyDir), if requested.
if [[ -n "${DEPLOYMENT_OUT:-}" ]]; then
  cp deployments/besu.json "$DEPLOYMENT_OUT"
  echo "→ Copied besu.json to $DEPLOYMENT_OUT"
fi

echo "→ Done. Addresses written to deployments/besu.json:"
cat deployments/besu.json
