package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/kappke/task-tui/internal/bootstrap"
	"github.com/kappke/task-tui/internal/cli"
)

func main() {
	if cli.IsCommand(os.Args[1:]) {
		if err := cli.Run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
			fmt.Fprintln(os.Stderr, bootstrap.SafeErrorText(err))
			os.Exit(1)
		}
		return
	}
	configPath := flag.String("config", "", "path to the TOML configuration file")
	headless := flag.Bool("headless", false, "render the cached view and exit")
	flag.Parse()

	err := bootstrap.Main(context.Background(), bootstrap.Options{
		ConfigPath: *configPath,
		Headless:   *headless,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, bootstrap.SafeErrorText(err))
		os.Exit(1)
	}
}
