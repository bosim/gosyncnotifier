package internal

import (
	"fmt"
	"log/slog"
	"strings"
	"syscall"
	"unsafe"
)

type Watcher struct {
	syncChan chan SyncEvent
	dirName  string
}

func (watcher *Watcher) Run() {
	fd, err := syscall.InotifyInit()
	if err != nil {
		panic("InotifyInit failed")
	}

	wd, err := syscall.InotifyAddWatch(fd, watcher.dirName,
		syscall.IN_CREATE|syscall.IN_MODIFY|syscall.IN_DELETE|syscall.IN_MOVE)
	if err != nil {
		panic(fmt.Sprintf("AddWatch failed %v", err))
	}

	defer syscall.InotifyRmWatch(fd, uint32(wd))

	var buf [4096]byte

	for {
		n, err := syscall.Read(fd, buf[:])
		if err != nil {
			slog.Error("Error reading from inotify", "err", err)
			continue
		}

		if n <= 0 {
			continue
		}

		offset := 0
		for offset < n {
			event := (*syscall.InotifyEvent)(unsafe.Pointer(&buf[offset]))
			nameBuf := buf[offset+syscall.SizeofInotifyEvent : offset+syscall.SizeofInotifyEvent+int(event.Len)]
			filename := strings.TrimRight(string(nameBuf), "\x00")

			var action SyncEventType
			if event.Mask&syscall.IN_CREATE != 0 {
				action = SyncEventCreated
			} else if event.Mask&syscall.IN_MODIFY != 0 {
				action = SyncEventModified
			} else if event.Mask&syscall.IN_DELETE != 0 {
				action = SyncEventDeleted
			} else if event.Mask&syscall.IN_MOVE != 0 {
				action = SyncEventMoved
			}

			watcher.syncChan <- SyncEvent{
				Filename: filename,
				Type:     action,
				Origin:   OriginWatcher,
			}

			offset += syscall.SizeofInotifyEvent + int(event.Len)
		}
	}
}

func NewWatcher(syncChan chan SyncEvent, dirName string) *Watcher {
	return &Watcher{
		syncChan: syncChan,
		dirName:  dirName,
	}
}
