# gosyncnotifier

🚧 This program is still in very early stage, work in progress 🚧

This is a small utility for invoking a sync executable such as `rsync`, `rclone`, or similar tools based on timer and file modification events.

It is based on the idea described in [this blog post](https://book.rymcg.tech/blog/linux/rclone_sync/), just written into a general tool in golang.

## Overview

`gosyncnotifier`/`gosn` watches a directory for changes and triggers a command when:

- a file is created, modified, or deleted
- a timer interval elapses

This is useful for:

- keeping a remote destination in sync
- running a sync step after local file updates
- reducing the need for manual sync commands during frequent file changes
- running other tasks on file changes, such as compiling

## Features

- watches file system activity recursively (create, modify, delete)
- triggers a configurable sync command, run as `<program> arg1 arg2 ...`
- supports time-based sync intervals, which reset whenever file activity is observed
- works with common sync tools such as `rsync`, `rclone`, and similar executables
- provide desktop notifications via D-Bus, with an optional `notify-send`-compatible command backend
- configurable log level (`debug`, `info`, `warn`, `error`)
- graceful shutdown on `SIGINT` (Ctrl-C) and `SIGTERM`
- lightweight Go-based implementation

## Installation

Requires Go 1.26 or later.

From source:

```bash
git clone https://github.com/bosim/gosyncnotifier.git
cd gosyncnotifier
go build -o gosn ./cmd/gosn
```

Or install the binary with Go:

```bash
go install github.com/bosim/gosyncnotifier/cmd/gosn@latest
```

The binary is installed into `$(go env GOPATH)/bin` (usually `~/go/bin`), which
must be on your `$PATH` to invoke `gosn` directly.

Run the test suite with:

```bash
go test ./...
```

## Usage

Run `gosn --help` to get a full description of options:

```
gosn - run commands based on timer or file activity

Basic syntax:
  gosn <flags> -- <command>

The command is run when the timer triggers or file activity occurs

The following flags are available:

  -log-level string
    	Notification backend: "dbus" or "command" (default "dbus")
  -notifier-backend string
    	The minimum log level (debug, info, warn, error) (default "info")
  -notifier-enabled
    	Whether desktop notifications are sent or not (default true)
  -notifier-exec string
    	Name or location of a notify-send compatible executable (command backend only) (default "notify-send")
  -notifier-post-command
    	Send a low priority desktop notification after the command has completed
  -timer-interval int
    	Specifies the interval (in minutes) on how often the timer part should trigger, 0 is disabled (default 10)
  -watcher-path string
    	Directory to watch for file changes, empty disables the watcher

Example:
  gosn -timer-interval 0 -watcher-path /home/user/test -- rsync -avz -e ssh /home/user/test user@server:/dir
```

### Notification backends

By default, notifications are sent directly through the freedesktop.org D-Bus
notification service:

```bash
gosn -notifier-backend=dbus ...
```

If you prefer to use a `notify-send`-compatible executable instead, select the
command backend and optionally point it at a different executable:

```bash
gosn -notifier-backend=command -notifier-exec=notify-send ...
```

The command backend is useful if you already depend on a specific
`notify-send`-compatible wrapper or want to keep the previous command-based
behavior.

Typical workflow:

1. choose a directory to watch
2. configure the sync command to run when changes are detected
3. optionally set a timer-based trigger to run periodic syncs even when files have not changed

### Example

```bash
gosn -timer-interval 10 -watcher-path /home/user/test -- rsync -avz -e ssh /home/user/test user@server:/dir
```

This will:

- watch `/home/user/test` (recursively) for file changes and run `rsync` when something changes
- run `rsync` once immediately at startup
- run `rsync` at least every 10 minutes of inactivity; the interval restarts whenever file activity is observed
- send a desktop notification if `rsync` fails

### Timer only (polling without file watching)

Omit `-watcher-path`, or pass an empty string, to disable the watcher and run the command on the interval only:

```bash
gosn -watcher-path "" -timer-interval 30 -- rclone sync /home/user/data remote:bucket
```

At least one of the watcher or the timer must be active, otherwise `gosn` exits with an error.

### Logging

```bash
gosn -log-level debug ...    # verbose: watched paths and every file event
gosn -log-level warn ...     # warnings and errors only
gosn -log-level error ...    # errors only
```

### Notifications

Desktop notifications require a `notify-send` compatible executable (default
`notify-send`, available on most Linux desktops).

- a critical notification is sent when the command fails
- a low priority notification is sent after a successful run when `-notifier-post-command` is set
- `-notifier-enabled=false` disables notifications entirely
- `-notifier-exec` points to a different executable

If the executable cannot be found, `gosn` logs a warning at startup and continues without sending notifications.

### Graceful shutdown

`gosn` shuts down gracefully when it receives `SIGINT` (Ctrl-C) or `SIGTERM`:

- the file watcher and the timer stop immediately
- a command that is currently running is allowed to finish before `gosn` exits
- pressing Ctrl-C a second time force-quits immediately, e.g. if the command hangs

Note: when run interactively, Ctrl-C is delivered by the terminal to the whole
foreground process group, so the command itself may still receive `SIGINT`
directly. `SIGTERM` (e.g. `kill`, systemd) is only handled by `gosn`, which then
waits for the command to complete.

## Behaviour notes

- **Startup run**: the command is executed once immediately at startup, before the first file event or timer tick.
- **Recursive watching**: the whole tree below `-watcher-path` is watched; directories created while running are picked up automatically, removed ones are unwatched.
- **Timer reset**: file activity resets the interval timer, so `-timer-interval` is the maximum idle time between periodic runs rather than a fixed schedule.
- **One command at a time**: commands run serially and further events wait until the running command finishes. There is no debounce, so a burst of changes can trigger several consecutive runs.
- **Suspend**: the timer is based on the wall clock and fires shortly after the system wakes up. File changes made while the system was suspended are not reported by the file watcher, so keep the timer enabled to catch up after waking.

## Notes

- Use a sync command that matches your environment and destination.
- Keep the watched directory scope narrow to avoid unnecessary syncs.
- If your sync tool is expensive or slow, consider batching changes or setting a reasonable interval.
- The tool should cope with laptops being suspended which the basic `inotifywait` cannot cope with.
- Direct D-Bus notifications require `DBUS_SESSION_BUS_ADDRESS` to point at a session bus. Use `-notifier-backend=command` if you prefer a `notify-send`-compatible wrapper.
- Very high event rates can overflow the underlying inotify queue and drop events; the timer acts as a safety net for those cases.

## License

This project is licensed under the MIT License. See the `LICENSE` file for details.
