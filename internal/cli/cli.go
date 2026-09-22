package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/kappke/task-tui/internal/app"
	"github.com/kappke/task-tui/internal/bootstrap"
	"github.com/kappke/task-tui/internal/domain"
)

// IsCommand reports whether args select the one-shot CLI rather than the TUI.
func IsCommand(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "providers", "spaces", "lists", "tasks", "task", "sync":
			return true
		}
	}
	return false
}

// Run executes one CLI command. It deliberately does not call Runtime.Start,
// so no continuous synchronization goroutines are created.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	configPath, timeout, command, commandArgs, err := globalArgs(args)
	if err != nil {
		return err
	}
	runtime, err := bootstrap.Build(ctx, bootstrap.Options{ConfigPath: configPath, Headless: true, Output: io.Discard})
	if err != nil {
		return err
	}
	defer func() { _ = runtime.Shutdown(context.Background()) }()

	commandCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		commandCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	return execute(commandCtx, runtime, command, commandArgs, stdout, stderr)
}

func globalArgs(args []string) (string, time.Duration, string, []string, error) {
	var configPath string
	var timeout time.Duration
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		if args[0] == "--config" || args[0] == "-config" {
			if len(args) < 2 {
				return "", 0, "", nil, errors.New("--config requires a path")
			}
			configPath, args = args[1], args[2:]
			continue
		}
		if args[0] == "--timeout" || args[0] == "-timeout" {
			if len(args) < 2 {
				return "", 0, "", nil, errors.New("--timeout requires a duration")
			}
			parsed, err := time.ParseDuration(args[1])
			if err != nil || parsed <= 0 {
				return "", 0, "", nil, fmt.Errorf("invalid --timeout %q", args[1])
			}
			timeout, args = parsed, args[2:]
			continue
		}
		break
	}
	if len(args) == 0 {
		return "", 0, "", nil, errors.New("a CLI command is required")
	}
	return configPath, timeout, args[0], args[1:], nil
}

func execute(ctx context.Context, runtime *bootstrap.Runtime, command string, args []string, stdout, stderr io.Writer) error {
	switch command {
	case "providers":
		return listProviders(ctx, runtime, args, stdout, stderr)
	case "spaces":
		return listSpaces(ctx, runtime, args, stdout, stderr)
	case "lists":
		return listLists(ctx, runtime, args, stdout, stderr)
	case "tasks":
		return listTasks(ctx, runtime, args, stdout, stderr)
	case "task":
		if len(args) == 0 {
			return errors.New("task requires get or add")
		}
		switch args[0] {
		case "get":
			return getTask(ctx, runtime, args[1:], stdout, stderr)
		case "add":
			return addTask(ctx, runtime, args[1:], stdout, stderr)
		default:
			return fmt.Errorf("unknown task command %q", args[0])
		}
	case "sync":
		return syncOnce(ctx, runtime, args, stdout)
	default:
		return fmt.Errorf("unknown CLI command %q", command)
	}
}

type commonFlags struct {
	provider string
	json     bool
}

func newFlags(name string, args []string, common *commonFlags, stderr io.Writer) (*flag.FlagSet, error) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&common.provider, "provider", "", "provider instance ID")
	flags.BoolVar(&common.json, "json", false, "write JSON output")
	return flags, nil
}

func listProviders(ctx context.Context, runtime *bootstrap.Runtime, args []string, stdout, stderr io.Writer) error {
	common := commonFlags{}
	flags, err := newFlags("providers", args, &common, stderr)
	if err != nil {
		return err
	}
	if err := flags.Parse(args); err != nil {
		return err
	}
	providers, err := runtime.ListProviders(ctx)
	if err != nil {
		return err
	}
	if common.json {
		return writeJSON(stdout, providers)
	}
	w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tTYPE\tNAME\tENABLED")
	for _, provider := range providers {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", provider.ID, provider.Type, provider.Name, strconv.FormatBool(provider.Enabled))
	}
	return w.Flush()
}

func listSpaces(ctx context.Context, runtime *bootstrap.Runtime, args []string, stdout, stderr io.Writer) error {
	common := commonFlags{}
	flags, err := newFlags("spaces", args, &common, stderr)
	if err != nil {
		return err
	}
	refresh := flags.Bool("refresh", false, "synchronize the provider before reading")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := refreshProvider(ctx, runtime, common.provider, *refresh); err != nil {
		return err
	}
	spaces, err := runtime.ListSpaces(ctx, domain.ProviderID(common.provider))
	if err != nil {
		return err
	}
	if common.json {
		return writeJSON(stdout, spaces)
	}
	w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tREMOTE ID\tNAME\tSYNC")
	for _, space := range spaces {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", space.ID, optional(space.RemoteID), space.Name, space.SyncState)
	}
	return w.Flush()
}

func listLists(ctx context.Context, runtime *bootstrap.Runtime, args []string, stdout, stderr io.Writer) error {
	common := commonFlags{}
	flags, err := newFlags("lists", args, &common, stderr)
	if err != nil {
		return err
	}
	space := flags.String("space", "", "space ID or remote ID")
	refresh := flags.Bool("refresh", false, "synchronize the provider before reading")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := refreshProvider(ctx, runtime, common.provider, *refresh); err != nil {
		return err
	}
	lists, err := runtime.ListLists(ctx, domain.ProviderID(common.provider), domain.SpaceID(*space))
	if err != nil {
		return err
	}
	if common.json {
		return writeJSON(stdout, lists)
	}
	w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tREMOTE ID\tSPACE\tNAME\tSYNC")
	for _, list := range lists {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", list.ID, optional(list.RemoteID), list.SpaceID, list.Name, list.SyncState)
	}
	return w.Flush()
}

func listTasks(ctx context.Context, runtime *bootstrap.Runtime, args []string, stdout, stderr io.Writer) error {
	common := commonFlags{}
	flags, err := newFlags("tasks", args, &common, stderr)
	if err != nil {
		return err
	}
	list := flags.String("list", "", "list ID or remote ID")
	refresh := flags.Bool("refresh", false, "synchronize the provider before reading")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*list) == "" {
		return errors.New("--list is required")
	}
	if err := refreshProvider(ctx, runtime, common.provider, *refresh); err != nil {
		return err
	}
	tasks, err := runtime.ListTasks(ctx, domain.ProviderID(common.provider), domain.ListID(*list))
	if err != nil {
		return err
	}
	if common.json {
		return writeJSON(stdout, tasks)
	}
	w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tREMOTE ID\tSTATUS\tTITLE\tSYNC")
	for _, task := range tasks {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", task.ID, optional(task.RemoteID), task.Status, task.Title, task.SyncState)
	}
	return w.Flush()
}

func getTask(ctx context.Context, runtime *bootstrap.Runtime, args []string, stdout, stderr io.Writer) error {
	common := commonFlags{}
	flags, err := newFlags("task get", args, &common, stderr)
	if err != nil {
		return err
	}
	id := flags.String("id", "", "task ID or remote ID")
	refresh := flags.Bool("refresh", false, "synchronize the provider before reading")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*id) == "" {
		return errors.New("--id is required")
	}
	if err := refreshProvider(ctx, runtime, common.provider, *refresh); err != nil {
		return err
	}
	task, err := runtime.GetTask(ctx, domain.ProviderID(common.provider), *id)
	if err != nil {
		return err
	}
	if common.json {
		return writeJSON(stdout, task)
	}
	fmt.Fprintf(stdout, "id: %s\nprovider: %s\nlist: %s\nremote_id: %s\ntitle: %s\nstatus: %s\npriority: %s\nsync: %s\n", task.ID, task.ProviderID, task.ListID, optional(task.RemoteID), task.Title, task.Status, task.Priority, task.SyncState)
	if task.Description != "" {
		fmt.Fprintf(stdout, "description: %s\n", task.Description)
	}
	return nil
}

func addTask(ctx context.Context, runtime *bootstrap.Runtime, args []string, stdout, stderr io.Writer) error {
	common := commonFlags{}
	flags, err := newFlags("task add", args, &common, stderr)
	if err != nil {
		return err
	}
	list := flags.String("list", "", "list ID or remote ID")
	title := flags.String("title", "", "task title")
	description := flags.String("description", "", "task description")
	status := flags.String("status", "", "task status")
	priority := flags.String("priority", "", "task priority")
	doSync := flags.Bool("sync", false, "perform one synchronization cycle before returning")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*list) == "" || strings.TrimSpace(*title) == "" {
		return errors.New("--list and --title are required")
	}
	task, err := runtime.CreateTask(ctx, app.CreateTaskInput{
		ProviderID:  domain.ProviderID(common.provider),
		ListID:      domain.ListID(*list),
		Title:       *title,
		Description: *description,
		Status:      *status,
		Priority:    domain.Priority(*priority),
	})
	if err != nil {
		return err
	}
	if *doSync {
		if err := runtime.SyncOnce(ctx, task.ProviderID); err != nil {
			return fmt.Errorf("task created locally, synchronization failed: %w", err)
		}
		task, err = runtime.GetTask(ctx, task.ProviderID, string(task.ID))
		if err != nil {
			return err
		}
	}
	if common.json {
		return writeJSON(stdout, task)
	}
	fmt.Fprintf(stdout, "created task %s\nprovider: %s\nsync: %s\n", task.ID, task.ProviderID, task.SyncState)
	if task.RemoteID != nil {
		fmt.Fprintf(stdout, "remote_id: %s\n", *task.RemoteID)
	}
	return nil
}

func syncOnce(ctx context.Context, runtime *bootstrap.Runtime, args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("sync", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	provider := flags.String("provider", "", "provider instance ID")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := runtime.SyncOnce(ctx, domain.ProviderID(*provider)); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "synchronization completed")
	return nil
}

func refreshProvider(ctx context.Context, runtime *bootstrap.Runtime, provider string, refresh bool) error {
	if !refresh {
		return nil
	}
	return runtime.SyncOnce(ctx, domain.ProviderID(provider))
}

func optional(value *string) string {
	if value == nil {
		return "-"
	}
	return *value
}

func writeJSON(w io.Writer, value any) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
