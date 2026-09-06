package main

import (
	"context"
	"fmt"
	"go-rag/app"
	"go-rag/config"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := app.Run(ctx, config.Load()); err != nil {
		fmt.Fprint(os.Stderr, err)
		os.Exit(1)
	}
}
