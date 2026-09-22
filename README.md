# task-tui

`task-tui` is a keyboard-first terminal task manager for local tasks and configured providers such as ClickUp.

## Build

```bash
go build -o tasktui ./cmd/tasktui
```

The application uses SQLite for local state. Configuration can be supplied with `--config`; otherwise the default user configuration and database paths are used.

## TUI

Start the main interface with:

```bash
./tasktui
```

The TUI displays providers, spaces, lists, and tasks from the local cache. Remote synchronization runs in the background when enabled.

Common controls:

```text
j/k or arrows  move
tab            switch panel
enter          open or select
n              create task
e              edit task
x              complete task
d              delete task
m              move task
r              refresh/sync
q              quit
```

Use `--headless` to render the cached view and exit:

```bash
./tasktui --headless
```

## CLI

The CLI performs one-shot operations without starting the TUI or a continuous synchronization loop. Reads use the local SQLite cache by default.

```bash
./tasktui providers
./tasktui spaces --provider clickup
./tasktui lists --provider clickup --space <space-id>
./tasktui tasks --provider clickup --list <list-id>
./tasktui task get --provider clickup --id <task-id>
./tasktui task add --provider clickup --list <list-id> --title "Fix login"
```

Use `--json` for machine-readable output:

```bash
./tasktui task get --provider clickup --id <task-id> --json
```

Use `--refresh` to synchronize once before a read:

```bash
./tasktui tasks --provider clickup --list <list-id> --refresh
```

Creating a task updates local state immediately. For remote providers, the remote operation is queued durably and the command exits without waiting. Add `--sync` when the command should perform one synchronization cycle before returning:

```bash
./tasktui task add --provider clickup --list <list-id> --title "Fix login" --sync
```

Queued work can also be synchronized explicitly:

```bash
./tasktui sync --provider clickup
```

If synchronization fails, the operation remains in the SQLite queue for a later CLI invocation or TUI session.
