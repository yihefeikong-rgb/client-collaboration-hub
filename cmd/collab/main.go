package main

import (
	"os"

	"github.com/yihefeikong-rgb/client-collaboration-hub/internal/cli"
)

func main() {
	os.Exit(cli.NewApp(".", os.Stdout, os.Stderr, nil).Run(os.Args[1:]))
}
