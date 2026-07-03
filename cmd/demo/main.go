// Command demo runs the full interbank scenario. It spawns the signing-router
// and deposit-svc binaries built alongside it (same directory), so build all
// three first:
//
//	go build -o bin/ ./cmd/...
//	./bin/demo
package main

import (
	"context"
	"log"
	"os"
	"os/signal"

	"besu-interbank-demo/internal/demo"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if err := demo.Run(ctx); err != nil {
		log.Fatalf("\n  ✗ %v\n", err)
	}
}
