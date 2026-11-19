package main

import (
	"os"

	"github.com/Zubrium/cdn/main/internal/command"
)

func main() {
	os.Exit(command.Run(os.Args[1:], os.Stdout, os.Stderr))
}
