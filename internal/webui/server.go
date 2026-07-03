// Package webui serves a browser UI to trigger and watch the interbank flow,
// plus a lightweight block-explorer API over the Besu RPC.
//
//   - Static UI at  /
//   - Actions        POST /api/{authorize,mint,transfer}  → proxied to the
//                    right bank's deposit-svc (which builds+signs+broadcasts)
//   - State          GET  /api/state    → token info + balances (read on-chain)
//   - Explorer       GET  /api/blocks   → recent blocks with decoded txs
//                    GET  /api/tx?hash= → one tx + receipt
//
// webui holds no keys and never signs — it reads the chain and forwards write
// intents to the deposit-svcs, exactly like a back-office console would.
package webui

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"besu-interbank-demo/internal/chainx"
	"besu-interbank-demo/internal/config"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// decoderABI decodes tx input + receipt events for the explorer. Function and
// event shapes recovered from the vendored template + clean-room AuthRegistry.
var decoderABI = func() abi.ABI {
	const j = `[
	  {"type":"function","name":"mint","inputs":[{"name":"stablecoin","type":"address"},{"name":"to","type":"address"},{"name":"amount","type":"uint256"}]},
	  {"type":"function","name":"burn","inputs":[{"name":"stablecoin","type":"address"},{"name":"amount","type":"uint256"}]},
	  {"type":"function","name":"transfer","inputs":[{"name":"to","type":"address"},{"name":"amount","type":"uint256"}]},
	  {"type":"function","name":"modifyPolicyWhitelist","inputs":[{"name":"policyId","type":"uint64"},{"name":"account","type":"address"},{"name":"allowed","type":"bool"}]},
	  {"type":"event","name":"Transfer","inputs":[{"name":"from","type":"address","indexed":true},{"name":"to","type":"address","indexed":true},{"name":"value","type":"uint256","indexed":false}]},
	  {"type":"event","name":"Approval","inputs":[{"name":"owner","type":"address","indexed":true},{"name":"spender","type":"address","indexed":true},{"name":"value","type":"uint256","indexed":false}]},
	  {"type":"event","name":"Mint","inputs":[{"name":"stablecoin","type":"address","indexed":true},{"name":"to","type":"address","indexed":true},{"name":"amount","type":"uint256","indexed":false}]},
	  {"type":"event","name":"Burn","inputs":[{"name":"stablecoin","type":"address","indexed":true},{"name":"amount","type":"uint256","indexed":false}]},
	  {"type":"event","name":"PolicyMembershipChanged","inputs":[{"name":"policyId","type":"uint64","indexed":true},{"name":"account","type":"address","indexed":true},{"name":"value","type":"bool","indexed":false}]}
	]`
	a, err := abi.JSON(strings.NewReader(j))
	if err != nil {
		panic("webui: bad decoder ABI: " + err.Error())
	}
	return a
}()

// topicValue decodes an indexed event argument from a log topic.
func topicValue(t abi.Type, topic common.Hash) any {
	switch t.T {
	case abi.AddressTy:
		return common.BytesToAddress(topic.Bytes())
	case abi.UintTy, abi.IntTy:
		return new(big.Int).SetBytes(topic.Bytes())
	case abi.BoolTy:
		return new(big.Int).SetBytes(topic.Bytes()).Sign() != 0
	default:
		return topic.Hex()
	}
}

// humanWFUSD renders a 6-decimal base-unit amount with thousands separators.
func humanWFUSD(v *big.Int) string {
	q := new(big.Int).Quo(v, big.NewInt(1_000_000)).String()
	var parts []string
	for len(q) > 3 {
		parts = append([]string{q[len(q)-3:]}, parts...)
		q = q[:len(q)-3]
	}
	parts = append([]string{q}, parts...)
	return strings.Join(parts, ",")
}

type Server struct {
	dep       *config.Deployment
	client    *ethclient.Client
	signer    types.Signer
	http      *http.Client
	staticDir string
	selectors map[string]string
}

func New(ctx context.Context) (*Server, error) {
	dep, err := config.LoadDeployment()
	if err != nil {
		return nil, err
	}
	client, err := ethclient.DialContext(ctx, config.RPCURL)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", config.RPCURL, err)
	}
	sel := func(sig string) string { return "0x" + common.Bytes2Hex(crypto.Keccak256([]byte(sig))[:4]) }
	return &Server{
		dep:       dep,
		client:    client,
		signer:    types.LatestSignerForChainID(config.ChainID),
		http:      &http.Client{Timeout: 30 * time.Second},
		staticDir: envOr("WEBUI_STATIC", defaultStaticDir()),
		selectors: map[string]string{
			sel("mint(address,address,uint256)"):            "mint",
			sel("burn(address,uint256)"):                    "burn",
			sel("transfer(address,uint256)"):                "transfer",
			sel("modifyPolicyWhitelist(uint64,address,bool)"): "authorize",
		},
	}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("GET /api/blocks", s.handleBlocks)
	mux.HandleFunc("GET /api/tx", s.handleTx)
	mux.HandleFunc("POST /api/authorize", s.proxy(config.BankA.DepositSvcURL, "/authorize"))
	mux.HandleFunc("POST /api/mint", s.proxy(config.BankA.DepositSvcURL, "/mint"))
	mux.HandleFunc("POST /api/transfer", s.proxy(config.BankB.DepositSvcURL, "/transfer"))
	mux.Handle("/", http.FileServer(http.Dir(s.staticDir)))
	return mux
}

func Run(ctx context.Context, port int) error {
	srv, err := New(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("webui on :%d — static %s — RPC %s\n", port, srv.staticDir, config.RPCURL)
	return http.ListenAndServe(fmt.Sprintf(":%d", port), srv.Handler())
}

// ─── State (read on-chain) ─────────────────────────────────────────────────────

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name, _ := s.readStr(ctx, s.dep.DepositToken, "name")
	symbol, _ := s.readStr(ctx, s.dep.DepositToken, "symbol")
	supply, _ := s.readUint(ctx, s.dep.DepositToken, chainx.DepositToken, "totalSupply")
	rl, _ := s.readUint(ctx, s.dep.ReserveLedger, chainx.ReserveLedger, "totalSupply")
	bankB, _ := s.balanceOf(ctx, config.BankB.Address)
	merch, _ := s.balanceOf(ctx, config.Merchant)
	head, _ := s.client.BlockNumber(ctx)
	writeJSON(w, 200, map[string]any{
		"token":  map[string]any{"name": name, "symbol": symbol, "totalSupply": supply.String(), "reserveLedgerSupply": rl.String()},
		"blockNumber": head,
		"addresses": map[string]string{
			"depositToken": s.dep.DepositToken.Hex(), "tokenAuthority": s.dep.TokenAuthority.Hex(),
			"reserveLedger": s.dep.ReserveLedger.Hex(), "authRegistry": s.dep.AuthRegistry.Hex(),
			"bankA": config.BankA.Address.Hex(), "bankB": config.BankB.Address.Hex(), "merchant": config.Merchant.Hex(),
		},
		"balances": map[string]string{"bankB": bankB.String(), "merchant": merch.String()},
	})
}

// ─── Explorer ──────────────────────────────────────────────────────────────────

type exTx struct {
	Hash   string `json:"hash"`
	From   string `json:"from"`
	To     string `json:"to"`
	Method string `json:"method"`
	Status string `json:"status"`
	Block  uint64 `json:"block"`
	Value  string `json:"value"`
}
type exBlock struct {
	Number uint64 `json:"number"`
	Hash   string `json:"hash"`
	Time   uint64 `json:"time"`
	TxN    int    `json:"txCount"`
	Txs    []exTx `json:"txs"`
}

func (s *Server) handleBlocks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	n := 12
	if q := r.URL.Query().Get("n"); q != "" {
		if v, err := strconv.Atoi(q); err == nil && v > 0 && v <= 50 {
			n = v
		}
	}
	head, err := s.client.BlockNumber(ctx)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	var out []exBlock
	for i := 0; i < n && int64(head)-int64(i) >= 0; i++ {
		bn := new(big.Int).SetUint64(head - uint64(i))
		blk, err := s.client.BlockByNumber(ctx, bn)
		if err != nil {
			break
		}
		eb := exBlock{Number: blk.NumberU64(), Hash: blk.Hash().Hex(), Time: blk.Time(), TxN: len(blk.Transactions())}
		for _, tx := range blk.Transactions() {
			eb.Txs = append(eb.Txs, s.decodeTx(ctx, tx, blk.NumberU64()))
		}
		out = append(out, eb)
	}
	writeJSON(w, 200, out)
}

// param is one decoded input/event argument.
type param struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value string `json:"value"`
	Human string `json:"human,omitempty"` // e.g. token amount in WFUSD
	Label string `json:"label,omitempty"` // friendly name for known addresses
}

// logEntry is one decoded receipt event.
type logEntry struct {
	Address string  `json:"address"`
	Event   string  `json:"event"`
	Params  []param `json:"params,omitempty"`
	Topic0  string  `json:"topic0,omitempty"`
	Raw     bool    `json:"raw,omitempty"`
}

// txDetail is the full Etherscan-style view of a transaction.
type txDetail struct {
	Hash      string     `json:"hash"`
	Status    string     `json:"status"`
	Method    string     `json:"method"`
	Block     uint64     `json:"block"`
	BlockHash string     `json:"blockHash"`
	Timestamp string     `json:"timestamp"`
	From      string     `json:"from"`
	FromAddr  string     `json:"fromAddr"`
	To        string     `json:"to"`
	ToAddr    string     `json:"toAddr"`
	Value     string     `json:"value"`
	Nonce     uint64     `json:"nonce"`
	TxType    uint8      `json:"txType"`
	GasLimit  uint64     `json:"gasLimit"`
	GasUsed   uint64     `json:"gasUsed"`
	GasPrice  string     `json:"gasPrice"`
	Input     string     `json:"input"`
	Decoded   []param    `json:"decoded"`
	Logs      []logEntry `json:"logs"`
}

func (s *Server) handleTx(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	hash := common.HexToHash(r.URL.Query().Get("hash"))
	tx, pending, err := s.client.TransactionByHash(ctx, hash)
	if err != nil {
		writeErr(w, 404, "tx not found")
		return
	}
	from := ""
	if f, err := types.Sender(s.signer, tx); err == nil {
		from = f.Hex()
	}
	to, toAddr := "", ""
	if tx.To() != nil {
		toAddr = tx.To().Hex()
		to = s.label(*tx.To())
	}
	d := txDetail{
		Hash:     hash.Hex(),
		Status:   "pending",
		Method:   s.methodName(tx.Data()),
		From:     s.label(common.HexToAddress(from)),
		FromAddr: from,
		To:       to,
		ToAddr:   toAddr,
		Value:    tx.Value().String(),
		Nonce:    tx.Nonce(),
		TxType:   tx.Type(),
		GasLimit: tx.Gas(),
		Input:    "0x" + common.Bytes2Hex(tx.Data()),
		Decoded:  s.decodeInput(tx.Data()),
	}
	if !pending {
		if rcpt, err := s.client.TransactionReceipt(ctx, hash); err == nil && rcpt != nil {
			d.Status = "reverted"
			if rcpt.Status == types.ReceiptStatusSuccessful {
				d.Status = "success"
			}
			d.Block = rcpt.BlockNumber.Uint64()
			d.BlockHash = rcpt.BlockHash.Hex()
			d.GasUsed = rcpt.GasUsed
			if rcpt.EffectiveGasPrice != nil {
				d.GasPrice = rcpt.EffectiveGasPrice.String()
			}
			d.Logs = s.decodeLogs(rcpt.Logs)
			if hd, err := s.client.HeaderByHash(ctx, rcpt.BlockHash); err == nil && hd != nil {
				d.Timestamp = fmt.Sprintf("%d", hd.Time)
			}
		}
	}
	writeJSON(w, 200, d)
}

// methodName resolves the 4-byte selector to a friendly name (else the selector).
func (s *Server) methodName(data []byte) string {
	if len(data) < 4 {
		return "transfer(ETH)"
	}
	if m, ok := s.selectors["0x"+common.Bytes2Hex(data[:4])]; ok {
		return m
	}
	return "0x" + common.Bytes2Hex(data[:4])
}

// decodeInput unpacks the calldata of a known method into named params.
func (s *Server) decodeInput(data []byte) []param {
	if len(data) < 4 {
		return nil
	}
	m, err := decoderABI.MethodById(data[:4])
	if err != nil {
		return nil
	}
	vals, err := m.Inputs.Unpack(data[4:])
	if err != nil {
		return nil
	}
	out := make([]param, 0, len(m.Inputs))
	for i, in := range m.Inputs {
		out = append(out, s.fmtParam(in.Name, in.Type.String(), vals[i]))
	}
	return out
}

// decodeLogs decodes receipt events (Transfer, Mint, PolicyMembershipChanged, …).
func (s *Server) decodeLogs(logs []*types.Log) []logEntry {
	out := make([]logEntry, 0, len(logs))
	for _, lg := range logs {
		if len(lg.Topics) == 0 {
			continue
		}
		ev, err := decoderABI.EventByID(lg.Topics[0])
		if err != nil {
			out = append(out, logEntry{Address: s.label(lg.Address), Event: "Unknown", Topic0: lg.Topics[0].Hex(), Raw: true})
			continue
		}
		entry := logEntry{Address: s.label(lg.Address), Event: ev.Name}
		// non-indexed args come from data (in order); indexed come from topics.
		nonIdx := abi.Arguments{}
		for _, in := range ev.Inputs {
			if !in.Indexed {
				nonIdx = append(nonIdx, in)
			}
		}
		dataVals, _ := nonIdx.Unpack(lg.Data)
		ti, di := 1, 0
		for _, in := range ev.Inputs {
			var v any
			if in.Indexed {
				if ti < len(lg.Topics) {
					v = topicValue(in.Type, lg.Topics[ti])
				}
				ti++
			} else if di < len(dataVals) {
				v = dataVals[di]
				di++
			}
			entry.Params = append(entry.Params, s.fmtParam(in.Name, in.Type.String(), v))
		}
		out = append(out, entry)
	}
	return out
}

// fmtParam renders one decoded value with a friendly label / human amount.
func (s *Server) fmtParam(name, typ string, v any) param {
	p := param{Name: name, Type: typ}
	switch val := v.(type) {
	case common.Address:
		p.Value = val.Hex()
		if lbl := s.label(val); lbl != val.Hex() {
			p.Label = lbl
		}
	case *big.Int:
		p.Value = val.String()
		if name == "amount" || name == "value" {
			p.Human = humanWFUSD(val) + " WFUSD"
		}
	case bool:
		p.Value = fmt.Sprintf("%t", val)
	case uint64:
		p.Value = fmt.Sprintf("%d", val)
	default:
		p.Value = fmt.Sprintf("%v", val)
	}
	return p
}

func (s *Server) decodeTx(ctx context.Context, tx *types.Transaction, block uint64) exTx {
	to := ""
	if tx.To() != nil {
		to = s.label(*tx.To())
	}
	from := ""
	if f, err := types.Sender(s.signer, tx); err == nil {
		from = s.label(f)
	}
	method := "transfer(ETH)"
	if len(tx.Data()) >= 4 {
		sel := "0x" + common.Bytes2Hex(tx.Data()[:4])
		if m, ok := s.selectors[sel]; ok {
			method = m
		} else {
			method = sel
		}
	}
	status := "pending"
	if rcpt, err := s.client.TransactionReceipt(ctx, tx.Hash()); err == nil && rcpt != nil {
		if rcpt.Status == types.ReceiptStatusSuccessful {
			status = "success"
		} else {
			status = "reverted"
		}
	}
	return exTx{Hash: tx.Hash().Hex(), From: from, To: to, Method: method, Status: status, Block: block, Value: tx.Value().String()}
}

// label maps known addresses to friendly names for the explorer.
func (s *Server) label(a common.Address) string {
	switch a {
	case config.BankA.Address:
		return "Bank A"
	case config.BankB.Address:
		return "Bank B"
	case config.Merchant:
		return "Merchant"
	case s.dep.DepositToken:
		return "DepositToken"
	case s.dep.TokenAuthority:
		return "TokenAuthority"
	case s.dep.ReserveLedger:
		return "ReserveLedger"
	case s.dep.AuthRegistry:
		return "AuthRegistry"
	case s.dep.TokenHandler:
		return "TokenHandler"
	}
	return a.Hex()
}

// ─── proxy write intents to a deposit-svc ──────────────────────────────────────

func (s *Server) proxy(base, path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, base+path, bytes.NewReader(body))
		req.Header.Set("content-type", "application/json")
		resp, err := s.http.Do(req)
		if err != nil {
			writeErr(w, 502, err.Error())
			return
		}
		defer resp.Body.Close()
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}
}

// ─── small chain readers ───────────────────────────────────────────────────────

func (s *Server) balanceOf(ctx context.Context, a common.Address) (*big.Int, error) {
	return s.readUint(ctx, s.dep.DepositToken, chainx.DepositToken, "balanceOf", a)
}

func (s *Server) readUint(ctx context.Context, to common.Address, a interface {
	Pack(string, ...any) ([]byte, error)
	Unpack(string, []byte) ([]any, error)
}, method string, args ...any) (*big.Int, error) {
	data, err := a.Pack(method, args...)
	if err != nil {
		return big.NewInt(0), err
	}
	out, err := s.client.CallContract(ctx, ethereum.CallMsg{To: &to, Data: data}, nil)
	if err != nil {
		return big.NewInt(0), err
	}
	vals, err := a.Unpack(method, out)
	if err != nil || len(vals) == 0 {
		return big.NewInt(0), err
	}
	if v, ok := vals[0].(*big.Int); ok {
		return v, nil
	}
	return big.NewInt(0), nil
}

func (s *Server) readStr(ctx context.Context, to common.Address, method string) (string, error) {
	data, err := chainx.DepositToken.Pack(method)
	if err != nil {
		return "", err
	}
	out, err := s.client.CallContract(ctx, ethereum.CallMsg{To: &to, Data: data}, nil)
	if err != nil {
		return "", err
	}
	vals, err := chainx.DepositToken.Unpack(method, out)
	if err != nil || len(vals) == 0 {
		return "", err
	}
	str, _ := vals[0].(string)
	return str, nil
}

// ─── helpers ───────────────────────────────────────────────────────────────────

func defaultStaticDir() string {
	if _, err := os.Stat("/app/webui/static"); err == nil {
		return "/app/webui/static"
	}
	return filepath.Join("webui", "static")
}
func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeErr(w http.ResponseWriter, status int, msg string) { writeJSON(w, status, map[string]string{"error": msg}) }
