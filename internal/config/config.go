// Package config holds the PUBLIC configuration for the interbank demo: bank
// identities, on-chain addresses, KeyIDs, and service endpoints. It contains no
// private key material — keys live only in the signing-router's keystore,
// mirroring the real platform where deposit-token-svc holds a KeyID, never a key.
package config

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"runtime"

	"github.com/ethereum/go-ethereum/common"
)

// Chain
var (
	RPCURL  = envOr("RPC_URL", "http://localhost:8545")
	ChainID = big.NewInt(1337)
)

// WFUSDDecimals — the deposit token has 6 decimals, like a fiat deposit.
const WFUSDDecimals = 6

// Role distinguishes issuer (mints) from holder (custodies + moves).
type Role string

const (
	RoleIssuer Role = "issuer"
	RoleHolder Role = "holder"
)

// Bank is one participant running its own signing-router (its key) and
// deposit-svc (no key).
type Bank struct {
	ID               string
	Label            string
	Role             Role
	KeyID            string         // opaque handle passed to the signing-router
	Address          common.Address // public address this key controls
	SigningRouterURL string         // this bank's router (holds ONLY this bank's key)
	DepositSvcURL    string
	SigningRouterPort int
	DepositSvcPort    int
}

var (
	BankA = Bank{
		ID:                "bank-a",
		Label:             "Bank A · Issuer",
		Role:              RoleIssuer,
		KeyID:             "key://bank-a/issuer",
		Address:           common.HexToAddress("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266"), // Hardhat #0
		// URLs are env-overridable so the same binaries work locally (localhost)
		// and in Docker/K8s (service DNS names). Ports are the in-container bind ports.
		SigningRouterURL:  envOr("BANKA_SIGNING_ROUTER_URL", "http://localhost:7401"),
		DepositSvcURL:     envOr("BANKA_DEPOSIT_URL", "http://localhost:8401"),
		SigningRouterPort: 7401,
		DepositSvcPort:    8401,
	}
	BankB = Bank{
		ID:                "bank-b",
		Label:             "Bank B · Holder",
		Role:              RoleHolder,
		KeyID:             "key://bank-b/holder",
		Address:           common.HexToAddress("0x70997970C51812dc3A010C7d01b50e0d17dc79C8"), // Hardhat #1
		SigningRouterURL:  envOr("BANKB_SIGNING_ROUTER_URL", "http://localhost:7402"),
		DepositSvcURL:     envOr("BANKB_DEPOSIT_URL", "http://localhost:8402"),
		SigningRouterPort: 7402,
		DepositSvcPort:    8402,
	}
	// Merchant — Bank B's counterparty. Address only (no key).
	Merchant = common.HexToAddress("0x3C44CdDdB6a900fa2b585dd299e03d12FA4293BC") // Hardhat #2
)

// Banks returns the lookup by ID.
func Banks() map[string]Bank { return map[string]Bank{BankA.ID: BankA, BankB.ID: BankB} }

// Deployment mirrors contracts/deployments/besu.json, written by DeployDemo.
type Deployment struct {
	AuthRegistry     common.Address `json:"authRegistry"`
	ReserveLedger    common.Address `json:"reserveLedger"`
	TokenAuthority   common.Address `json:"tokenAuthority"`
	TokenHandler     common.Address `json:"tokenHandler"`
	DepositToken     common.Address `json:"depositToken"`
	TransferPolicyID uint64         `json:"transferPolicyId"`
	RLMintPolicyID   uint64         `json:"rlMintPolicyId"`
	SCMintPolicyID   uint64         `json:"scMintPolicyId"`
	ChainID          uint64         `json:"chainId"`
}

// LoadDeployment reads besu.json. Path is DEPLOYMENT_JSON if set (containers
// mount a shared volume / ConfigMap there), else the in-repo default.
func LoadDeployment() (*Deployment, error) {
	path := envOr("DEPLOYMENT_JSON", filepath.Join(repoRoot(), "contracts", "deployments", "besu.json"))
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w (deploy the contracts first — see README)", path, err)
	}
	var d Deployment
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &d, nil
}

// repoRoot resolves the besu-interbank-demo directory from this source file.
func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
