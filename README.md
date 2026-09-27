# gosyncnotifier

🚧 This program is still in very early stage, work in progress 🚧

This is a small utility for invoking a sync executable such as `rsync`, `rclone`, or similar tools based on timer and file modification events.

It is based on the idea described in [this blog post](https://book.rymcg.tech/blog/linux/rclone_sync/), just written into a general tool in golang. 

## Overview

`gosyncnotifier` watches one or more directories for changes and triggers a sync action when:
- a file changes
- a timer interval elapses

This is useful for:
- keeping a remote destination in sync
- running a sync step after local file updates
- reducing the need for manual sync commands during frequent file changes
- can also be used to run other tasks like compiling on file changes.

## Features

- watches file system activity
- triggers a configurable sync command
- supports time-based sync intervals
- works with common sync tools such as `rsync`, `rclone`, and similar executables
- provide desktop notifications using `notify-send`
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


Typical workflow:
1. choose a directory to watch
2. configure the sync command to run when changes are detected
3. optionally set a timer-based trigger to run periodic syncs even when files have not changed

## Example

```bash
gosn -timer-interval 10 -watcher-path /home/user/test -- rsync -avz -e ssh /home/user/test user@server:/dir
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
