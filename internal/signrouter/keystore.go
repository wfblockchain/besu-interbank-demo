// Package signrouter is the clean-room mimic of the platform's signing-router-svc.
// It knows keys, not contracts: given a KeyID and a 32-byte hash it returns an
// ECDSA signature. In production this fronts an MPC signing cluster; here it
// signs with a local key from this keystore.
package signrouter

import (
	"crypto/ecdsa"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// keystore is the ONLY place private key material exists in this demo.
//
// ⚠️  DEMO-ONLY throwaway keys (well-known Hardhat accounts). Never use on a real
//     network — a counterparty running this would generate their own. Nothing
//     outside this package imports these bytes; the deposit-svc only ever sends
//     a KeyID and a hash.
var keystore = map[string]string{
	"key://bank-a/issuer": "ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80", // Hardhat #0
	"key://bank-b/holder": "59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d", // Hardhat #1
}

type key struct {
	priv    *ecdsa.PrivateKey
	address common.Address
}

func loadKey(keyID string) (*key, error) {
	hex, ok := keystore[keyID]
	if !ok {
		return nil, fmt.Errorf("keystore: no key for keyId %q", keyID)
	}
	priv, err := crypto.HexToECDSA(hex)
	if err != nil {
		return nil, fmt.Errorf("keystore: bad key for %q: %w", keyID, err)
	}
	return &key{priv: priv, address: crypto.PubkeyToAddress(priv.PublicKey)}, nil
}

// signHash produces a 65-byte [R‖S‖V] recoverable signature over the 32-byte
// hash. V is the recovery bit (0/1), matching what tx.WithSignature expects.
func signHash(k *key, hash []byte) ([]byte, error) {
	return crypto.Sign(hash, k.priv)
}
