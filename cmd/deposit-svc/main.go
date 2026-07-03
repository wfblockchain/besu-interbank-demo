// Command deposit-svc runs one bank's deposit-token service.
//
//	BANK_ID=bank-a PORT=8401 deposit-svc
package main

import (
	"context"
	"log"
	"os"
	"strconv"

	"besu-interbank-demo/internal/config"
	"besu-interbank-demo/internal/depositsvc"
)

func main() {
	bankID := os.Getenv("BANK_ID")
	bank, ok := config.Banks()[bankID]
	if !ok {
		log.Fatalf("deposit-svc: set BANK_ID to one of bank-a, bank-b (got %q)", bankID)
	}
	port := bank.DepositSvcPort
	if v := os.Getenv("PORT"); v != "" {
		port, _ = strconv.Atoi(v)
	}
	if err := depositsvc.Run(context.Background(), bankID, port); err != nil {
		log.Fatalf("deposit-svc: %v", err)
	}
}
