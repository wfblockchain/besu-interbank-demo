# Besu Interbank Deposit-Token Demo

A **self-contained** demo of two banks transacting deposit tokens on
[Hyperledger Besu](https://www.hyperledger.org/projects/besu), using
[Bridge's ERC-20 stablecoin template](https://github.com/withbridge/erc20-stablecoin-template)
for the on-chain contracts.

> **The story.** Bank A (the *issuer*) holds the ledger and mints deposit tokens
> straight into a wallet custodied by Bank B (the *holder*). Bank B can then move
> those tokens in real time — here, to a merchant — settling in a single Besu
> block. **Each bank owns its own signing key; neither key ever leaves its
> owner.**

Everything here is clean-room and shareable: **no private dependencies, no MPC,
and no code from the custody platform.** The client is a faithful *mimic* of the
platform's two-part split — a deposit-token service and a signing router — so a
counterparty can see the shape of the architecture without any proprietary
material.

Open **[docs/key-custody.html](docs/key-custody.html)** in a browser for the
one-page trust model.

---

## Architecture

```
  Bank A domain (Issuer)                                Bank B domain (Holder)
 ┌───────────────────────────┐                        ┌───────────────────────────┐
 │ deposit-svc  (:8401)       │                        │ deposit-svc  (:8402)       │
 │   builds mint / authorize  │                        │   builds transfer          │
 │   tx  →  computes sighash   │                        │   tx  →  computes sighash   │
 │            │                │                        │            │                │
 │            ▼ sign(keyId,hash)                        │            ▼ sign(keyId,hash)
 │ signing-router (:7401)      │                        │ signing-router (:7402)      │
 │   🔐 KEY A  (never leaves)  │                        │   🔐 KEY B  (never leaves)  │
 └────────────┬──────────────┘                         └────────────┬──────────────┘
              │ signed tx                                            │ signed tx
              └───────────────────────►  Besu QBFT  ◄───────────────┘
                                    (chainId 1337, ~2s)
                     TokenAuthority → ReserveLedger → WFUSD (deposit token)
```

- **deposit-svc** knows the contracts but **never holds a key**. For every write
  it: builds an EIP-1559 transaction → computes the signing hash → asks its
  signing-router to sign that hash by `KeyID` → assembles and broadcasts. This is
  the same `build → sign(KeyID, hash) → assemble → broadcast` pipeline the real
  `deposit-token-svc` + `broadcast-svc` + `signing-router-svc` use, minus MPC.
- **signing-router** knows keys but **nothing about contracts**. It signs a
  32-byte hash for a `KeyID` and returns the signature. Each instance is
  provisioned with only its bank's key, so Bank A's router *physically cannot*
  sign for Bank B.
- The two are separated by the **sign-a-hash boundary** — exactly like the
  production stack. Local keys stand in for MPC.

---

## Layout

| Path | What it is |
|------|-----------|
| `chain/` | Besu QBFT dev network — `docker-compose.yml`, `genesis/`, validator key |
| `contracts/` | Vendored Bridge template + **clean-room `lib/auth-registry/`** + `deploy-to-besu.sh` |
| `internal/signrouter/` | Mimic: `Sign(keyId, hash) → signature`, local keystore (the only place keys live) |
| `internal/depositsvc/` | Mimic: `mint` / `transfer` / `authorize` / balance — builds & broadcasts tx |
| `internal/demo/` | Orchestrator — brings up both banks' services and drives the scenario |
| `cmd/{signing-router,deposit-svc,demo}/` | The three Go binaries (one per service, mirroring the platform's `cmd/` layout) |
| `docs/key-custody.html` | The key-custody trust-model visual |

The client is **Go**, built on [`go-ethereum`](https://github.com/ethereum/go-ethereum) —
the same library your `broadcast-svc` uses. The deposit-svc's
`build → sign(KeyID, hash) → assemble` steps mirror `broadcast-svc`'s
`evm.BuildUnsignedTx` / `AssembleSignedTx` (`signer.Hash(tx)` → `tx.WithSignature`),
so the demo reflects the real service shapes, not a toy.

---

## Prerequisites

- **Docker** (runs Besu, the contract deploy, and the whole stack)
- **Go ≥ 1.23** (only for the local/host path)
- **kubectl** (only for the Kubernetes path)

## Run it — three ways

### A. Docker (full stack + web console) — recommended

```bash
make up          # build images + start chain, deploy contracts, run all services
# → open http://localhost:8080   (the web console: trigger the flow + block explorer)
make demo        # optional: run the one-shot CLI scenario against the running stack
make down        # tear everything down
```

`make up` is just `docker compose up --build -d`. Bring-up order is enforced by
healthchecks: **besu → deployer → routers + deposit-svcs → webui**.

### B. Kubernetes

```bash
make k8s-up      # build + (kind) load images, then apply manifests
# → http://localhost:30080  (NodePort)   or:
#   kubectl -n besu-interbank port-forward svc/webui 8080:8080
make k8s-down
```

Manifests live in [`deploy/k8s/`](deploy/k8s/) (see its README). Contract
addresses are deterministic (CREATE2 + fixed salts), so the address book ships
as the `deployment-addresses` ConfigMap; a `deployer` Job puts the code on-chain
and deposit-svcs wait (initContainer) until it exists.

### C. Local (host binaries, no containers for the services)

```bash
docker compose -f chain/docker-compose.yml up -d          # chain only
(cd contracts && docker run --rm --network host -v "$PWD":/w -w /w \
   ghcr.io/foundry-rs/foundry:stable "bash deploy-to-besu.sh")   # deploy
./run.sh          # = go build -o bin/ ./cmd/...  &&  ./bin/demo  (spawns the 4 services)
```

## Web console & block explorer

The `webui` service (`http://localhost:8080`) is a back-office-style console:

- **Trigger** — buttons for *Authorize*, *Mint*, *Transfer*, or *Run full flow*.
  It never holds a key; it forwards write intents to the deposit-svcs.
- **Visualize** — a live GSAP diagram animates each signed transaction through
  `TokenAuthority → ReserveLedger → Deposit Token`, and on-chain balances update.
- **Explore** — a built-in block explorer reads Besu directly: a live transaction
  feed with decoded methods (`mint` / `transfer` / `authorize`) and friendly
  address labels (Bank A/B, Merchant, contracts), recent blocks, and per-tx detail.

Each binary is also runnable standalone, e.g.
`curl -XPOST localhost:8401/mint -d '{"to":"0x7099…79C8","amount":"1000000000000"}'`.

---

## What the demo proves

```
② Bank A mints 1,000,000 WFUSD → Bank B's wallet
    ✓ mint                   blk#309  0x8dc9dfb5e5b6eac7…  signed by 0xf39Fd6e5…   (Key A)

③ Bank B transfers 250,000 WFUSD → Merchant (real-time)
    ✓ transfer               blk#311  0x0de744d09e1f8646…  signed by 0x70997970…   (Key B)

④ Reconcile & key-isolation checks
    ✓ ReserveLedger backing 1000000 WFUSD == deposit-token supply 1000000 WFUSD
    ✓ Bank B cannot mint (holder, not issuer)
    ✓ Bank A's router refuses to sign with Bank B's key (403 — key not held)
```

- **Two keys, two owners.** Every mint/authorize is signed by Bank A's router
  with Key A; the transfer is signed by Bank B's router with Key B. The output
  names the signer of each transaction.
- **Isolation is enforced, not assumed.** Bank B's deposit-svc refuses to mint
  (wrong role), and Bank A's signing-router returns `403` when asked to sign with
  Bank B's key — it simply does not hold it.
- **Backed 1:1.** The issuer's `ReserveLedger` supply always equals the
  circulating deposit-token supply.

---

## Clean-room notes

- **`contracts/lib/auth-registry/`** is a ~90-line clean-room reconstruction of
  the policy registry the Bridge template calls into (runtime surface is a single
  `isAuthorized(policyId, account)`; management surface recovered from the
  template's own tests). It replaces the **private** `withbridge/auth-registry`
  git dependency so this bundle has **no private dependencies**. The unrelated
  Tempo-chain variant (`src/v3/tempo/`, `tempo-std`) was dropped.
- The vendored contract suite passes **135/135 tests** against this AuthRegistry
  (`cd contracts && docker run --rm -v "$PWD":/w -w /w ghcr.io/foundry-rs/foundry:stable "forge test"`).
- The Besu genesis pre-deploys the canonical CREATE2 factory
  (`0x4e59…56C`) and enables **Shanghai + Cancun** (the template uses transient
  storage), and runs with `zeroBaseFee` so no faucet is needed.
- **All keys are DEMO-ONLY** well-known Hardhat accounts, prefunded in genesis.
  Never reuse them on a real network. A counterparty running this would generate
  their own.
```
Bank A (Issuer)  0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266   Hardhat #0
Bank B (Holder)  0x70997970C51812dc3A010C7d01b50e0d17dc79C8   Hardhat #1
Merchant         0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC   Hardhat #2
```

The token contracts are Bridge's ERC-20 stablecoin template — used here as
**deposit tokens** — under the MIT License (`contracts/LICENSE`).
