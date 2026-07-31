package main

import (
	"context"
	"fmt"
	"os"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/cli"
)

func main() {
	root, err := cli.ResolveDataRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(4)
	}
	app := cli.NewApp(root, os.Stdout, os.Stderr, nil)
	if workingDirectory, err := os.Getwd(); err == nil {
		app.WorkingDirectory = workingDirectory
	}
	if err := app.EnsureInitialized(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(4)
	}
	os.Exit(app.Run(os.Args[1:]))
}
