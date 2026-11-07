# gosyncnotifier — Agent Protocol for File Change Triggers

## Project Overview

`gosyncnotifier`: A small Go utility that watches a directory for file changes and triggers a specified command when activity occurs or a timer elapses.

The project is intended for workflows like:
- Syncing local files to a remote destination with `rsync`, `rclone`, or similar tools
- Running periodic sync jobs even when files are not changing
- Triggering other commands as a result of filesystem events
- Reducing manual sync command usage after frequent file changes

## Repository Layout

- `cmd/gosn/` — CLI entry point for the `gosn` binary
- `cmd/gosnctl/` — CLI entry point for the `gosnctl` status and control binary (queries `gosn` over the control socket)
- `internal/` — runtime logic for watchers, event debouncing, timers, runner execution, and notifications
- `internal/*_test.go` — unit tests for the watcher, debouncer, timer, runner, syncevent, notifier, and util packages
- `README.md` — usage, overview, and examples in Markdown format
- `go.mod` / `go.sum` — Go module metadata and dependency lockfile
- `.github/workflows/go.yml` — CI workflow running `go build -v ./...` and `go test -v ./...`
- `.gitignore` — Git ignore rules

## Build and Validation

From the repository root (or after installation), validate with:

```bash
# For local source
go build ./...

# With test suite
go test ./...
```

CI (`.github/workflows/go.yml`) runs `go build -v ./...` and
`go test -v -race ./...` on Go 1.26 (`-race` needs cgo, so a local C compiler
is required to reproduce that exact command).

## Architecture Notes

1. **File Watcher**: Monitors a single directory recursively for filesystem
   events using `fsnotify` (inotify on Linux, kqueue/FSEvents on macOS). No
   external watcher programs (`inotifywait`, `fswatch`) are used. Directories
   created while running are watched automatically, removed ones are unwatched.

2. **Timer Trigger**: Configurable interval (default 10 minutes) allows periodic execution independent of file activity. Timer uses Go's `time.Ticker` to stay responsive even when system is suspended.

3. **Command Execution**: The runner parses user-provided command as `<program> arg1 arg2...` and executes it.

4. **Notification System**: Desktop notifications use three levels via the freedesktop.org D-Bus notification service, or optionally via a `notify-send`-compatible executable:
   - `low`: Debug/debugging information (for developers)
   - `normal`: General notification (for users of the tool)
   - `critical`: Error/failure notification

5. **Event Types**: A `SyncEvent` carries an event type and an origin, which are
   two separate enums:

   `SyncEventType`:
   - `SyncEventNone`: No file activity (e.g. timer or init trigger)
   - `SyncEventCreated`: New file detected
   - `SyncEventModified`: File content changed
   - `SyncEventDeleted`: File removed

   `OriginType`:
   - `OriginNone`: Unset
   - `OriginInit`: Initialization signal (runs the command once at startup)
   - `OriginTimer`: Timer interval elapsed
   - `OriginWatcher`: Filesystem event observed
   - `OriginControl`: Explicit run requested via `gosnctl trigger`

   A `SyncEvent` additionally carries `Events` (the affected files as
   `FileEvent{Filename, Type}`, deduplicated in first-appearance order with
   the latest type per file; nil when the event never passed through the
   debouncer).

6. **Execution Model**: Events flow from `syncChannel` through runner, notifying notifier to send desktop messages (if enabled). Each run optionally executes a `-precmd` shell command (`sh -c`) first — a non-zero exit skips the main command and the `-postcmd` without recording a sync in the control status — and a `-postcmd` shell command after the main command, which runs even when the main command failed; a failing `-postcmd` sends a critical notification with its captured output. The command runs once immediately at startup (`OriginInit`), file activity resets the timer interval, and commands run serially (one at a time, further events wait); watcher event bursts are debounced before reaching `syncChannel` (see Event Debouncing).

7. **Event Debouncing**: A burst of filesystem events (an editor save, a copy
   into the watched tree) is coalesced by the `Debouncer`
   (`internal/debouncer.go`) into a single `SyncEvent`, forwarded only after
   the filesystem has been quiet for `-debounce-interval` (default 1s; 0
   disables debouncing and the watcher writes to `syncChannel` directly).
   Flow: `watcher → debouncer → syncChannel`; timer and init events go to
   `syncChannel` directly and are never delayed. Watch registration side
   effects (adding/removing recursive watches) stay in the watcher and remain
   immediate. The forwarded event describes the whole burst: `SyncEvent.Events`
   lists the distinct affected files in first-appearance order with the latest
   event type per file (nil when the debouncer was not involved). For a
   burst touching more than one file, the main loop logs the number of
   affected files as `files` and prints one debug line per file.

8. **Control Socket**: `gosn` serves a Unix domain socket (default
   `$XDG_RUNTIME_DIR/gosn.sock`, i.e. `/run/user/<uid>/gosn.sock`, permissions
   `0600`; `-control-socket` moves it, an empty value disables it) with
   one-line JSON request/response pairs for `gosnctl`: `status`, `pause`
   (optionally with a Go duration for auto-resume), `resume` and `trigger`.
   While paused the event loop records events and keeps the timer schedule but
   skips command runs; `resume` and the expiry of a timed pause queue a
   catch-up event with `OriginInit`, while `trigger` is rejected until
   resumed. A live instance's socket is never stolen (a second instance logs a
   warning and disables its control server), a stale socket file is removed on
   startup, and the file is unlinked on shutdown.

9. **Shutdown**: A shared `context.Context` from `signal.NotifyContext` covers
   SIGINT (Ctrl-C) and SIGTERM. On cancellation the watcher, debouncer, and
   timer stop immediately, a pending debounced burst is dropped, an
   in-flight command is allowed to finish, and no further commands are
   started. Failure and post-command notifications are suppressed
   during shutdown. After the first signal the default disposition is restored,
   so a second Ctrl-C force-quits even if a command hangs.

## Notification Modes

The notifier supports a three-level notification mechanism:

```go
type Notifier interface {
    Low(message string) error      // "Debug/Development info"
    Normal(message string) error   // "User-facing notification"
    Critical(message string) error // "Error/Failure notification"
    Close() error
}
```

The default `dbus` backend connects to the session bus once and reconnects if a
notification fails. A failed startup connection is logged as a warning and is
not fatal; later sends retry the connection. An unknown `-notifier-backend`
value exits with an error at startup. The `command` backend runs a
`notify-send`-compatible executable and is selected with
`-notifier-backend=command`; if the executable is missing, notifications are
disabled with a warning and the program keeps running.

## Usage Patterns

### Basic Configuration

Use the `-watcher-path` flag to specify the directory to monitor for file
changes. The command to execute is passed after `--`:

```bash
gosn -watcher-path ~/projects/source -- rsync user@host:/dir/...
```

### Recursive Watching

Only one watch path can be configured. Files are watched recursively,
including subdirectories:

```bash
gosn -watcher-path /var/log -- notify-send error "File changed in /var/log"
```

### Disabled Watchers (Polling Only)

Configure for periodic execution only, no file change detection:

```bash
gosn -timer-interval 10 -watcher-path "" -- rsync ...
```

Use empty string or omit watcher entirely to poll the command periodically.

### Debouncing File Events

Coalesce a burst of filesystem events into a single command run once the
watched directory has been quiet for the configured period (default 1s):

```bash
gosn -debounce-interval 2s ...   # wait for 2s of quiet before running
gosn -debounce-interval 0 ...    # disable: run once per file event
```

### Silent Operation (Full Polling Without Notifications)

Disable notifications while still running:

```bash
gosn -notifier-enabled=false -watcher-path /test -- rsync user@host:/dir...
```

### Custom Log Levels

Set log level for filtering output:

```bash
gosn -log-level=debug ...    # Detailed logs
gosn -log-level=warn ...     # Warning messages and above
gosn -log-level=error ...    # Error messages and above (no info)
```

### Post-Command Notification

Send a secondary low-priority notification when command completes:

```bash
gosn -notifier-post-command=true ...
```

### Pre and Post Commands

Gate the run on a shell check (non-zero exit skips the run) and hook a
reporting script after it (non-zero exit notifies with its output):

```bash
gosn -precmd 'test "$(nmcli -t -f METERED general get)" = no' \
     -postcmd '/usr/local/bin/report-bisync-conflicts' -- rsync ...
```

### Status and Pause (gosnctl)

Query and control the running instance over the control socket:

```bash
gosnctl status            # sync state, last sync, next timer run
gosnctl -json status      # same as JSON, for scripting
gosnctl pause 30m         # pause with automatic resume after 30 minutes
gosnctl resume            # resume and queue a catch-up run
gosnctl trigger           # queue a sync run now (rejected while paused)
gosnctl -socket /run/user/1000/gosn-b.sock status   # non-default socket (multi-instance)
```

### Notification Backend

Use the session D-Bus notification service (default):

```bash
gosn -notifier-backend=dbus ...
```

Use a `notify-send`-compatible executable instead:

```bash
gosn -notifier-backend=command -notifier-exec=notify-send ...
```

## CLI Reference

### Commands and Options

| Argument | Default | Description |
|----------|---------|-------------|
| `--timer-interval` (flags) | 10 minutes | Timer triggers; 0 disables timer |
| `--debounce-interval` (flags) | 1s | Quiet period for coalescing file events before running the command; 0 disables debouncing |
| `--watcher-path` (optional) | empty string | Path to monitor for events |
| `--control-socket` (flags) | `$XDG_RUNTIME_DIR/gosn.sock` | Control socket for `gosnctl`; empty disables it, only one instance per socket |
| `--log-level` (flags) | info | Minimum log level |
| `--notifier-enabled` | true | Enable desktop notifications |
| `--notifier-backend` | `dbus` | Notification backend: `dbus` or `command` |
| `--notifier-exec` (default: notify-send) | `notify-send` | Executable name for the `command` backend (must be available) |
| `--notifier-post-command` | false | Send follow-up notification |
| `--precmd` (optional) | empty string | Shell command (`sh -c`) run before each command run; a non-zero exit skips the run |
| `--postcmd` (optional) | empty string | Shell command (`sh -c`) run after each command run; a non-zero exit sends a critical notification with its output |

### CLI Syntax

```bash
gosn [flags] -- <command>
```

Full options are printed with `gosn -help`.

### gosnctl Commands

```bash
gosnctl [flags] <command> [arguments]
```

| Command | Description |
|---------|-------------|
| `status` (`-json` for JSON) | Sync state, last sync, last event, next timer run and configuration |
| `pause [duration]` | Pause syncs until resume; optional Go duration (e.g. `30m`) auto-resumes after the period |
| `resume` | Resume syncs and queue a catch-up run |
| `trigger` | Queue a sync run now; rejected while paused |

Flags: `-socket` selects a non-default control socket path (default matches
`gosn`'s `-control-socket`), `-json` prints `status` as JSON. Full options are
printed with `gosnctl -help`.

Multi-instance example — each `gosn` gets its own socket, `gosnctl -socket`
selects which one to control:

```bash
gosn -watcher-path ~/photos -control-socket /run/user/1000/gosn-photos.sock -- rsync ...
gosnctl -socket /run/user/1000/gosn-photos.sock status
```

## Key Considerations

- **Watch Scope**: Limit watched paths to avoid excessive syncs on slow environments
- **Suspension Handling**: Use Go ticker (`time.NewTicker`) instead of sleep loops for responsive suspends
- **Notifier Dependency**: The `dbus` backend requires a reachable session bus (`DBUS_SESSION_BUS_ADDRESS`); the `command` backend requires a `notify-send`-compatible executable in PATH
- **Execution Performance**: Command execution runs regardless of file changes unless explicitly disabled
- **Control Socket**: The default path is per-user under `$XDG_RUNTIME_DIR`; one `gosn` instance owns a socket at a time, and `gosnctl -socket` must match a custom `-control-socket` path

## Development Workflow

When modifying code:

1. **Validate local** builds and tests first
2. **Confirm model integrity** — event flow matches architecture notes
3. **Test build cleanly** — no compilation errors or warnings
4. **Document usage** — update README.md appropriately
5. **Consider edge cases** — file permissions, network access, race conditions

## License

MIT License - See LICENSE file for details.
