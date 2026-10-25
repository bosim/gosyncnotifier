# gosyncnotifier — Agent Protocol for File Change Triggers

## Project Overview

`gosyncnotifier`: A small Go utility that watches one or more directories for file changes and triggers a specified command when activity occurs or a timer elapses.

The project is intended for workflows like:
- Syncing local files to a remote destination with `rsync`, `rclone`, or similar tools
- Running periodic sync jobs even when files are not changing
- Triggering other commands as a result of filesystem events
- Reducing manual sync command usage after frequent file changes

## Repository Layout

- `cmd/gosn/` — CLI entry point for the `gosn` binary
- `internal/` — runtime logic for watchers, timers, runner execution, and notifications
- `README.md` — usage, overview, and examples in HTML format
- `go.mod` / `go.sum` — Go module metadata and dependency lockfile
- `.gitignore` — Git ignore rules

## Build and Validation

From the repository root (or after installation), validate with:

```bash
# For local source
go build ./...

# With test suite
go test ./...
```

After building or installing, update `pkg/` to point to your local path if running from a custom location.

## Architecture Notes

1. **File Watcher**: Monitors specified paths for filesystem events using:
   - Linux/macOS: inotifywait (`fsnotify`)
   - macOS: fswatch

2. **Timer Trigger**: Configurable interval (default 10 minutes) allows periodic execution independent of file activity. Timer uses Go's `time.Ticker` to stay responsive even when system is suspended.

3. **Command Execution**: The runner parses user-provided command as `<program> arg1 arg2...` and executes it.

4. **Notation System**: Desktop notifications use three levels via notify-send:
   - `low`: Debug/debugging information (for developers)
   - `normal`: General notification (for users of the tool)
   - `critical`: Error/failure notification

5. **Event Types**: File activity generates four event types in the `SyncEvent`:
   - `OriginInit`: Initialization signal
   - `SyncEventCreated`: New file detected
   - `SyncEventDeleted`: File removed
   - `SyncEventModified`: File content changed

6. **Execution Model**: Events flow from `syncChannel` through runner, notifying notifier to send desktop messages (if enabled).

## Notification Modes

The notifier supports a three-level notification mechanism:

```go
typeNotifier interface {
    Low(message string)   // "Debug/Development info"
    Normal(message string) // "User-facing notification"
    Critical(message string) // "Error/Failure notification"
}
```

## Usage Patterns

### Basic Configuration

Use the `-watcher-path` flag to specify:
- Where files are monitored
- The sync command to execute automatically

```bash
gosn -watcher-path ~/projects/source -- rsync user@host:/dir/...
```

### Multiple Watchers

One or more watch paths can be configured. Files are watched recursively, including subdirectories.

```bash
gosn -watcher-path /var/log -- notify-send error "File changed in /var/log"
```

### Disabled Watchers (Polling Only)

Configure for periodic execution only, no file change detection:

```bash
gosn -timer-interval 10 -watcher-path "" -- rsync ...
```

Use empty string or omit watcher entirely to poll the command periodically.

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

## CLI Reference

### Commands and Options

| Argument | Default | Description |
|----------|---------|-------------|
| `--timer-interval` (flags) | 10 minutes | Timer triggers; 0 disables timer |
| `--watcher-path` (optional) | empty string | Path to monitor for events |
| `--log-level` (flags) | info | Minimum log level |
| `--notifier-enabled` | true | Enable desktop notifications |
| `--notifier-exec` (default: notify-send) | `notify-send` | Executable name (must be available) |
| `--notifier-post-command` | false | Send follow-up notification |

### CLI Syntax

```bash
gosn [flags] -- <command>
```

Full options are printed with `gosn -help`.

## Key Considerations

- **Watch Scope**: Limit watched paths to avoid excessive syncs on slow environments
- **Suspension Handling**: Use Go ticker (`time.NewTicker`) instead of sleep loops for responsive suspends
- **Notifier Dependency**: Requires executable installed system-wide or in PATH
- **Execution Performance**: Command execution runs regardless of file changes unless explicitly disabled

## Development Workflow

When modifying code:

1. **Validate local** builds and tests first
2. **Confirm model integrity** — event flow matches architecture notes
3. **Test build cleanly** — no compilation errors or warnings
4. **Document usage** — update README.md appropriately
5. **Consider edge cases** — file permissions, network access, race conditions

## License

MIT License - See LICENSE file for details.
