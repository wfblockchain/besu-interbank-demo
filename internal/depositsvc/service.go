// Package depositsvc is the clean-room mimic of deposit-token-svc's role. It
// knows the token/contract semantics but NEVER holds a key. For every write it:
//   1. builds an EIP-1559 transaction and computes the signing hash,
//   2. asks its bank's signing-router to sign that hash by KeyID,
//   3. assembles the signed tx and broadcasts it to Besu.
//
// This is the same build → sign(KeyID, hash) → assemble → broadcast pipeline the
// real deposit-token-svc + broadcast-svc + signing-router-svc use — in fact the
// build/assemble steps mirror broadcast-svc's evm.BuildUnsignedTx /
// AssembleSignedTx (signer.Hash → tx.WithSignature), minus MPC.
package depositsvc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"

	"besu-interbank-demo/internal/chainx"
	"besu-interbank-demo/internal/config"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

// Service is one bank's deposit-svc.
type Service struct {
	bank   config.Bank
	dep    *config.Deployment
	client *ethclient.Client
	signer types.Signer
	http   *http.Client
}

// TxResult summarizes a broadcast transaction for the caller/UI.
type TxResult struct {
	Op          string `json:"op"`
	TxHash      string `json:"txHash"`
	Status      string `json:"status"` // "success" | "reverted"
	BlockNumber string `json:"blockNumber"`
	GasUsed     string `json:"gasUsed"`
	SignedBy    string `json:"signedBy"`
}

// New dials the chain and returns a service for the given bank.
func New(ctx context.Context, bank config.Bank, dep *config.Deployment) (*Service, error) {
	client, err := ethclient.DialContext(ctx, config.RPCURL)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", config.RPCURL, err)
	}
	return &Service{
		bank:   bank,
		dep:    dep,
		client: client,
		signer: types.LatestSignerForChainID(config.ChainID),
		http:   &http.Client{},
	}, nil
}

// ─── Reads ───────────────────────────────────────────────────────────────────

// BalanceOf returns the WFUSD balance (base units) of account.
func (s *Service) BalanceOf(ctx context.Context, account common.Address) (*big.Int, error) {
	return s.readUint(ctx, s.dep.DepositToken, chainx.DepositToken, "balanceOf", account)
}

// TokenInfo is the deposit token's public state plus issuer backing.
type TokenInfo struct {
	Name               string `json:"name"`
	Symbol             string `json:"symbol"`
	Decimals           uint8  `json:"decimals"`
	TotalSupply        string `json:"totalSupply"`
	ReserveLedgerSupply string `json:"reserveLedgerSupply"`
}

func (s *Service) TokenInfo(ctx context.Context) (*TokenInfo, error) {
	name, err := s.readString(ctx, s.dep.DepositToken, chainx.DepositToken, "name")
	if err != nil {
		return nil, err
	}
	symbol, _ := s.readString(ctx, s.dep.DepositToken, chainx.DepositToken, "symbol")
	dec, _ := s.readUint(ctx, s.dep.DepositToken, chainx.DepositToken, "decimals")
	supply, _ := s.readUint(ctx, s.dep.DepositToken, chainx.DepositToken, "totalSupply")
	rl, _ := s.readUint(ctx, s.dep.ReserveLedger, chainx.ReserveLedger, "totalSupply")
	return &TokenInfo{
		Name: name, Symbol: symbol, Decimals: uint8(dec.Uint64()),
		TotalSupply: supply.String(), ReserveLedgerSupply: rl.String(),
	}, nil
}

// ─── Writes ──────────────────────────────────────────────────────────────────

// Mint issues deposit tokens to `to`. Issuer only.
func (s *Service) Mint(ctx context.Context, to common.Address, amount *big.Int) (*TxResult, error) {
	if err := s.requireRole(config.RoleIssuer, "mint"); err != nil {
		return nil, err
	}
	data, err := chainx.TokenAuthority.Pack("mint", s.dep.DepositToken, to, amount)
	if err != nil {
		return nil, err
	}
	return s.buildSignBroadcast(ctx, "mint", s.dep.TokenAuthority, data)
}

// Authorize whitelists a counterparty on the transfer policy (so it can hold/move
// tokens) and the mint-recipient policy (so it can receive mints). Issuer only —
// Bank A is the AuthRegistry policy admin. Mirrors counterparty onboarding.
func (s *Service) Authorize(ctx context.Context, account common.Address) ([]TxResult, error) {
	if err := s.requireRole(config.RoleIssuer, "authorize"); err != nil {
		return nil, err
	}
	steps := []struct {
		policyID uint64
		tag      string
	}{
		{s.dep.TransferPolicyID, "authorize:transfer"},
		{s.dep.SCMintPolicyID, "authorize:mint-recipient"},
	}
	var out []TxResult
	for _, st := range steps {
		data, err := chainx.AuthRegistry.Pack("modifyPolicyWhitelist", st.policyID, account, true)
		if err != nil {
			return nil, err
		}
		res, err := s.buildSignBroadcast(ctx, st.tag, s.dep.AuthRegistry, data)
		if err != nil {
			return nil, err
		}
		out = append(out, *res)
	}
	return out, nil
}

// Transfer moves deposit tokens to another wallet. Any holder can do this.
func (s *Service) Transfer(ctx context.Context, to common.Address, amount *big.Int) (*TxResult, error) {
	data, err := chainx.DepositToken.Pack("transfer", to, amount)
	if err != nil {
		return nil, err
	}
	return s.buildSignBroadcast(ctx, "transfer", s.dep.DepositToken, data)
}

// ─── build → sign(KeyID,hash) → assemble → broadcast ─────────────────────────

func (s *Service) buildSignBroadcast(ctx context.Context, op string, to common.Address, data []byte) (*TxResult, error) {
	from := s.bank.Address

	// 1. Build the unsigned EIP-1559 transaction.
	nonce, err := s.client.PendingNonceAt(ctx, from)
	if err != nil {
		return nil, fmt.Errorf("nonce: %w", err)
	}
	head, err := s.client.HeaderByNumber(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("head: %w", err)
	}
	baseFee := head.BaseFee
	if baseFee == nil {
		baseFee = big.NewInt(0) // zeroBaseFee chain
	}
	gas, err := s.client.EstimateGas(ctx, ethereum.CallMsg{From: from, To: &to, Data: data})
	if err != nil {
		gas = 800_000
	}
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   config.ChainID,
		Nonce:     nonce,
		To:        &to,
		Value:     big.NewInt(0),
		Data:      data,
		GasTipCap: big.NewInt(0),
		GasFeeCap: new(big.Int).Set(baseFee),
		// ×2 headroom: nested calls (mint → handler → ledger+token) and the EVM
		// 63/64 gas-forwarding rule can leave a tight estimate short when mined.
		Gas: gas * 2,
	})

	// 2. Compute the signing hash — ALL the signing-router will ever see.
	sighash := s.signer.Hash(tx)

	// 3. Ask this bank's signing-router to sign the hash by KeyID.
	sig, signer, err := s.routerSign(ctx, sighash.Bytes())
	if err != nil {
		return nil, err
	}

	// 4. Assemble the signed tx and broadcast.
	signedTx, err := tx.WithSignature(s.signer, sig)
	if err != nil {
		return nil, fmt.Errorf("assemble: %w", err)
	}
	if err := s.client.SendTransaction(ctx, signedTx); err != nil {
		return nil, fmt.Errorf("broadcast: %w", err)
	}
	receipt, err := bind.WaitMined(ctx, s.client, signedTx)
	if err != nil {
		return nil, fmt.Errorf("wait mined: %w", err)
	}

	status := "reverted"
	if receipt.Status == types.ReceiptStatusSuccessful {
		status = "success"
	}
	return &TxResult{
		Op:          op,
		TxHash:      signedTx.Hash().Hex(),
		Status:      status,
		BlockNumber: receipt.BlockNumber.String(),
		GasUsed:     fmt.Sprintf("%d", receipt.GasUsed),
		SignedBy:    signer,
	}, nil
}

// routerSign posts {keyId, hash} to this bank's signing-router and returns the
// 65-byte signature + the signer address.
func (s *Service) routerSign(ctx context.Context, hash []byte) ([]byte, string, error) {
	body, _ := json.Marshal(map[string]string{"keyId": s.bank.KeyID, "hash": hexutil.Encode(hash)})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, s.bank.SigningRouterURL+"/sign", bytes.NewReader(body))
	req.Header.Set("content-type", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("signing-router: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		Signature string `json:"signature"`
		Address   string `json:"address"`
		Error     string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, "", fmt.Errorf("signing-router decode: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("signing-router %s refused: %d %s", s.bank.SigningRouterURL, resp.StatusCode, out.Error)
	}
	sig, err := hexutil.Decode(out.Signature)
	if err != nil {
		return nil, "", fmt.Errorf("signing-router bad signature: %w", err)
	}
	return sig, out.Address, nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func (s *Service) requireRole(want config.Role, op string) error {
	if s.bank.Role != want {
		return fmt.Errorf("%s (%s) may not perform %q — requires %s", s.bank.Label, s.bank.Role, op, want)
	}
	return nil
}

func (s *Service) call(ctx context.Context, to common.Address, a abiPacker, method string, args ...any) ([]any, error) {
	data, err := a.Pack(method, args...)
	if err != nil {
		return nil, err
	}
	out, err := s.client.CallContract(ctx, ethereum.CallMsg{To: &to, Data: data}, nil)
	if err != nil {
		return nil, err
	}
	return a.Unpack(method, out)
}

func (s *Service) readUint(ctx context.Context, to common.Address, a abiPacker, method string, args ...any) (*big.Int, error) {
	vals, err := s.call(ctx, to, a, method, args...)
	if err != nil {
		return nil, err
	}
	switch v := vals[0].(type) {
	case *big.Int:
		return v, nil
	case uint8:
		return big.NewInt(int64(v)), nil
	default:
		return nil, fmt.Errorf("readUint: unexpected type %T", v)
	}
}

func (s *Service) readString(ctx context.Context, to common.Address, a abiPacker, method string) (string, error) {
	vals, err := s.call(ctx, to, a, method)
	if err != nil {
		return "", err
	}
	str, _ := vals[0].(string)
	return str, nil
}

// abiPacker is the subset of abi.ABI we use (Pack/Unpack), so helpers stay small.
type abiPacker interface {
	Pack(name string, args ...any) ([]byte, error)
	Unpack(name string, data []byte) ([]any, error)
}

// Bank exposes this service's bank config (for the HTTP layer).
func (s *Service) Bank() config.Bank { return s.bank }
