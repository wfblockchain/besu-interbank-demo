// Package chainx holds the minimal contract ABIs the deposit-svc uses and small
// helpers for packing/unpacking calls. Only the functions the demo actually
// invokes on the vendored Bridge template + the clean-room AuthRegistry.
package chainx

import (
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
)

const (
	tokenAuthorityJSON = `[
      {"type":"function","name":"mint","stateMutability":"nonpayable",
       "inputs":[{"name":"stablecoin","type":"address"},{"name":"to","type":"address"},{"name":"amount","type":"uint256"}],"outputs":[]},
      {"type":"function","name":"burn","stateMutability":"nonpayable",
       "inputs":[{"name":"stablecoin","type":"address"},{"name":"amount","type":"uint256"}],"outputs":[]}
    ]`

	depositTokenJSON = `[
      {"type":"function","name":"transfer","stateMutability":"nonpayable",
       "inputs":[{"name":"to","type":"address"},{"name":"amount","type":"uint256"}],"outputs":[{"name":"","type":"bool"}]},
      {"type":"function","name":"balanceOf","stateMutability":"view",
       "inputs":[{"name":"account","type":"address"}],"outputs":[{"name":"","type":"uint256"}]},
      {"type":"function","name":"totalSupply","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"uint256"}]},
      {"type":"function","name":"name","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"string"}]},
      {"type":"function","name":"symbol","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"string"}]},
      {"type":"function","name":"decimals","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"uint8"}]}
    ]`

	reserveLedgerJSON = `[
      {"type":"function","name":"totalSupply","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"uint256"}]}
    ]`

	authRegistryJSON = `[
      {"type":"function","name":"modifyPolicyWhitelist","stateMutability":"nonpayable",
       "inputs":[{"name":"policyId","type":"uint64"},{"name":"account","type":"address"},{"name":"allowed","type":"bool"}],"outputs":[]},
      {"type":"function","name":"isAuthorized","stateMutability":"view",
       "inputs":[{"name":"policyId","type":"uint64"},{"name":"account","type":"address"}],"outputs":[{"name":"","type":"bool"}]}
    ]`
)

// Parsed ABIs, ready for Pack/Unpack. Panics at init on a malformed literal.
var (
	TokenAuthority = mustABI(tokenAuthorityJSON)
	DepositToken   = mustABI(depositTokenJSON)
	ReserveLedger  = mustABI(reserveLedgerJSON)
	AuthRegistry   = mustABI(authRegistryJSON)
)

func mustABI(s string) abi.ABI {
	parsed, err := abi.JSON(strings.NewReader(s))
	if err != nil {
		panic("chainx: bad ABI literal: " + err.Error())
	}
	return parsed
}
