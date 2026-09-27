# gosyncnotifier

Small utility for invoking a sync executable such as `rsync`, `rclone`, or similar tools based on timer and file modification events.

## Overview

`gosyncnotifier` watches one or more directories for changes and triggers a sync action when:
- a file changes
- a timer interval elapses
- the configured conditions are met

This is useful for:
- keeping a remote destination in sync
- running a sync step after local file updates
- reducing the need for manual sync commands during frequent file changes

## Features

- watches file system activity
- triggers a configurable sync command
- supports time-based sync intervals
- works with common sync tools such as `rsync`, `rclone`, and similar executables
- lightweight Go-based implementation

## Installation

From source:

```bash
git clone https://github.com/bosim/gosyncnotifier.git
cd gosyncnotifier
go build ./...
```

Or install the binary with Go:

```bash
go install github.com/bosim/gosyncnotifier@latest
```

## Usage

Run the notifier with your watch directory and sync command. The exact flags may vary based on your local build, but the general pattern is:

```bash
gosyncnotifier --watch /path/to/watch --sync "rsync -av /path/to/watch/ remote:/destination/" --interval 60
```

Typical workflow:
1. choose a directory to watch
2. configure the sync command to run when changes are detected
3. optionally set a timer-based trigger to run periodic syncs even when files have not changed

## Example

```bash
gosyncnotifier \
  --watch /home/user/data \
  --sync "rsync -av /home/user/data/ backup-host:/backup/data/" \
  --interval 300
```

This will:
- watch the directory for file changes
- trigger the sync command after relevant updates
- also run the sync command on the configured interval

## Notes

- Use a sync command that matches your environment and destination.
- Keep the watched directory scope narrow to avoid unnecessary syncs.
- If your sync tool is expensive or slow, consider batching changes or setting a reasonable interval.

## License

This project is licensed under the MIT License. See the `LICENSE` file for details.
