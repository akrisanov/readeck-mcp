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
	var errRun error
	switch cfg.Transport {
	case "http", "streamable-http":
		logger.Printf("starting MCP HTTP transport on %s%s", cfg.HTTPAddr, cfg.HTTPPath)
		errRun = server.RunHTTP(ctx)
	default:
		logger.Printf("starting MCP stdio transport")
		errRun = server.Run(ctx)
	}
	if errRun != nil {
		logger.Fatalf("server stopped with error: %v", errRun)
	}
}
