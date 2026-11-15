package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"hb/internal/cmd"
	"hb/internal/ui"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := cmd.NewRoot().ExecuteContext(ctx); err != nil {
		u := ui.For(os.Stderr)
		fmt.Fprintln(os.Stderr, u.Bad("错误：")+err.Error())
		os.Exit(1)
	}
}
