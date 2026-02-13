package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/akrisanov/readeck-mcp/internal/config"
	"github.com/akrisanov/readeck-mcp/internal/mcp"
	"github.com/akrisanov/readeck-mcp/internal/readeck"
)

func main() {
	logger := log.New(os.Stderr, "", log.LstdFlags|log.Lmicroseconds)

	cfg, err := config.Load()
	if err != nil {
		logger.Fatalf("config error: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client := readeck.NewClient(cfg, logger)
	server := mcp.NewServer(cfg, client, logger)
	if err := server.Run(ctx); err != nil {
		logger.Fatalf("server stopped with error: %v", err)
	}
}
