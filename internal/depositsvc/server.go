package depositsvc

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"

	"besu-interbank-demo/internal/config"

	"github.com/ethereum/go-ethereum/common"
)

// Server exposes one bank's deposit-svc over HTTP. Amounts are base units
// (6-decimals): 1 WFUSD = "1000000".
type Server struct{ svc *Service }

func NewServer(svc *Service) *Server { return &Server{svc: svc} }

func (h *Server) Handler() http.Handler {
	b := h.svc.Bank()
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"ok": true, "bank": b.Label, "address": b.Address.Hex(), "keyId": b.KeyID})
	})

	mux.HandleFunc("GET /info", func(w http.ResponseWriter, r *http.Request) {
		info, err := h.svc.TokenInfo(r.Context())
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		bal, _ := h.svc.BalanceOf(r.Context(), b.Address)
		writeJSON(w, 200, map[string]any{"bank": b.Label, "address": b.Address.Hex(), "token": info, "balance": bal.String()})
	})

	mux.HandleFunc("GET /balance", func(w http.ResponseWriter, r *http.Request) {
		acct := b.Address
		if q := r.URL.Query().Get("account"); q != "" {
			acct = common.HexToAddress(q)
		}
		bal, err := h.svc.BalanceOf(r.Context(), acct)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, 200, map[string]string{"account": acct.Hex(), "raw": bal.String()})
	})

	mux.HandleFunc("POST /mint", func(w http.ResponseWriter, r *http.Request) {
		to, amount, err := decodeToAmount(r)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		res, err := h.svc.Mint(r.Context(), to, amount)
		respond(w, res, err)
	})

	mux.HandleFunc("POST /authorize", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Account string `json:"account"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, 400, "bad json")
			return
		}
		res, err := h.svc.Authorize(r.Context(), common.HexToAddress(body.Account))
		respond(w, res, err)
	})

	mux.HandleFunc("POST /transfer", func(w http.ResponseWriter, r *http.Request) {
		to, amount, err := decodeToAmount(r)
		if err != nil {
			writeErr(w, 400, err.Error())
			return
		}
		res, err := h.svc.Transfer(r.Context(), to, amount)
		respond(w, res, err)
	})

	return mux
}

// Run builds a service for BANK_ID and serves it (blocking).
func Run(ctx context.Context, bankID string, port int) error {
	bank, ok := config.Banks()[bankID]
	if !ok {
		return fmt.Errorf("unknown bank %q", bankID)
	}
	dep, err := config.LoadDeployment()
	if err != nil {
		return err
	}
	svc, err := New(ctx, bank, dep)
	if err != nil {
		return err
	}
	log.Printf("deposit-svc %q on :%d — key %s via %s", bank.Label, port, bank.KeyID, bank.SigningRouterURL)
	return http.ListenAndServe(fmt.Sprintf(":%d", port), NewServer(svc).Handler())
}

func decodeToAmount(r *http.Request) (common.Address, *big.Int, error) {
	var body struct {
		To     string `json:"to"`
		Amount string `json:"amount"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return common.Address{}, nil, fmt.Errorf("bad json")
	}
	amount, ok := new(big.Int).SetString(body.Amount, 10)
	if !ok {
		return common.Address{}, nil, fmt.Errorf("bad amount %q", body.Amount)
	}
	return common.HexToAddress(body.To), amount, nil
}

func respond(w http.ResponseWriter, res any, err error) {
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, res)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
