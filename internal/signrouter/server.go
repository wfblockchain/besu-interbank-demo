package signrouter

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/ethereum/go-ethereum/common/hexutil"
)

// Server is a signing-router instance provisioned with a fixed set of KeyIDs.
// It signs a 32-byte hash for one of its keys and returns the 65-byte
// [R‖S‖V] signature. Fail-closed: it refuses any KeyID it was not given, so
// Bank A's router physically cannot sign for Bank B.
type Server struct {
	label   string
	allowed map[string]*key
}

// SignRequest / SignResponse are the wire types for POST /sign.
type SignRequest struct {
	KeyID string `json:"keyId"`
	Hash  string `json:"hash"` // 0x-prefixed 32-byte hex
}
type SignResponse struct {
	Signature string `json:"signature"` // 0x-prefixed 65-byte [R‖S‖V]
	Address   string `json:"address"`   // who signed
}

// New builds a router holding exactly the given KeyIDs (must exist in the
// keystore), failing closed if none are provided.
func New(label string, keyIDs []string) (*Server, error) {
	if len(keyIDs) == 0 {
		return nil, fmt.Errorf("signing-router: refusing to start with no keys")
	}
	allowed := make(map[string]*key, len(keyIDs))
	for _, id := range keyIDs {
		k, err := loadKey(id)
		if err != nil {
			return nil, err
		}
		allowed[id] = k
	}
	return &Server{label: label, allowed: allowed}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		ids := make([]string, 0, len(s.allowed))
		for id := range s.allowed {
			ids = append(ids, id)
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "label": s.label, "keyIds": ids})
	})
	mux.HandleFunc("POST /sign", s.handleSign)
	return mux
}

func (s *Server) handleSign(w http.ResponseWriter, r *http.Request) {
	var req SignRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json")
		return
	}
	// Fail closed: only sign for keys this router was provisioned with.
	k, ok := s.allowed[req.KeyID]
	if !ok {
		writeErr(w, http.StatusForbidden, fmt.Sprintf("router %q does not hold key %s", s.label, req.KeyID))
		return
	}
	hash, err := hexutil.Decode(req.Hash)
	if err != nil || len(hash) != 32 {
		writeErr(w, http.StatusBadRequest, "hash must be a 32-byte 0x-hex string")
		return
	}

	// Local-key analog of the MPC sign step: crypto.Sign returns a 65-byte
	// [R‖S‖V] with V already the recovery bit (0/1), which plugs straight into
	// go-ethereum's tx.WithSignature. (The real broadcast-svc adapter instead
	// gets a DER signature from MPC/HSM and recovers V — see AssembleSignedTx.)
	sig, err := signHash(k, hash)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	log.Printf("[%s] signed hash %s… with %s (%s)", s.label, req.Hash[:12], req.KeyID, k.address.Hex())
	writeJSON(w, http.StatusOK, SignResponse{Signature: hexutil.Encode(sig), Address: k.address.Hex()})
}

// Run starts the router HTTP server (blocking).
func Run(port int, label string, keyIDs []string) error {
	srv, err := New(label, keyIDs)
	if err != nil {
		return err
	}
	log.Printf("signing-router %q listening on :%d — holds [%s]", label, port, strings.Join(keyIDs, ", "))
	return http.ListenAndServe(fmt.Sprintf(":%d", port), srv.Handler())
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
