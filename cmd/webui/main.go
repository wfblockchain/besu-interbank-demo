// Command webui serves the browser UI + block-explorer API.
//
//	PORT=8080 webui
package main

import (
	"context"
	"log"
	"os"
	"strconv"

	"besu-interbank-demo/internal/webui"
)

func main() {
	port := 8080
	if v := os.Getenv("PORT"); v != "" {
		port, _ = strconv.Atoi(v)
	}
	if err := webui.Run(context.Background(), port); err != nil {
		log.Fatalf("webui: %v", err)
	}
}
