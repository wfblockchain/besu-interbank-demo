// Command signing-router runs one bank's signing-router.
//
//	PORT=7401 LABEL="Bank A router" ROUTER_KEYS="key://bank-a/issuer" signing-router
package main

import (
	"log"
	"os"
	"strconv"
	"strings"

	"besu-interbank-demo/internal/signrouter"
)

func main() {
	port := 7401
	if v := os.Getenv("PORT"); v != "" {
		port, _ = strconv.Atoi(v)
	}
	label := os.Getenv("LABEL")
	if label == "" {
		label = "signing-router"
	}
	var keys []string
	for _, k := range strings.Split(os.Getenv("ROUTER_KEYS"), ",") {
		if k = strings.TrimSpace(k); k != "" {
			keys = append(keys, k)
		}
	}
	if err := signrouter.Run(port, label, keys); err != nil {
		log.Fatalf("signing-router: %v", err)
	}
}
