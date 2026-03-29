package main

import (
	"context"
	"fmt"
	"os"

	"github.com/outgate-ai/og-cli/cmd"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := cmd.NewCLI().ExecuteContext(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
