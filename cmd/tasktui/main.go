package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/kappke/task-tui/internal/bootstrap"
)

func main() {
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
