package main

import (
	"log"

	"github.com/merev/mcp-kubernetes-server/internal/server"
)

func main() {
	if err := server.Run(); err != nil {
		log.Fatal(err)
	}
}
