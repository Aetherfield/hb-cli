package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"hb/internal/cmd"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := cmd.NewRoot().ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "hb:", err)
		os.Exit(1)
	}
}
