package internal

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fsnotify/fsnotify"
)

type Watcher struct {
	syncChan chan SyncEvent
	dirName  string
	watcher  *fsnotify.Watcher
}

func NewWatcher(syncChan chan SyncEvent, dirName string) *Watcher {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		// XXX return error
		panic("Error initializing watcher")
	}
	return &Watcher{
		syncChan: syncChan,
		dirName:  dirName,
		watcher:  watcher,
	}
}

func (watcher *Watcher) addRecursively(rootPath string) error {
	return filepath.WalkDir(rootPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() {
			return nil
		}

		if !slices.Contains(watcher.watcher.WatchList(), path) {
			err = watcher.watcher.Add(path)
			if err != nil {
				return fmt.Errorf("failed to watch %s: %w", path, err)
			}
			slog.Debug("Watching path", "path", path)
		}

		return nil
	})
}

func (watcher *Watcher) removeRecursive(path string) {
	for _, watchedPath := range watcher.watcher.WatchList() {
		if watchedPath == path || strings.HasPrefix(watchedPath, path+string(filepath.Separator)) {
			_ = watcher.watcher.Remove(watchedPath)
			slog.Debug("Unwatching path", "path", watchedPath)
		}
	}

}

func (watcher *Watcher) handleEvent(event fsnotify.Event) {
	switch {
	case event.Has(fsnotify.Create):
		slog.Debug("Created/Moved", "name", event.Name)

		fi, err := os.Stat(event.Name)
		if err == nil && fi.IsDir() {
			if err := watcher.addRecursively(event.Name); err != nil {
				slog.Error("Error adding recursive watches to new dir", "error", err)
			}
		}

		watcher.syncChan <- SyncEvent{
			Filename: event.Name,
			Type:     SyncEventCreated,
			Origin:   OriginWatcher,
		}
	case event.Has(fsnotify.Remove), event.Has(fsnotify.Rename):
		isWatched := slices.Contains(watcher.watcher.WatchList(), event.Name)
		if isWatched {
			slog.Debug("Removed/Moved Out", "name", event.Name)
			watcher.removeRecursive(event.Name)
		} else {
			slog.Debug("File changed/removed:", "name", event.Name)
		}

		watcher.syncChan <- SyncEvent{
			Filename: event.Name,
			Type:     SyncEventDeleted,
			Origin:   OriginWatcher,
		}

	case event.Has(fsnotify.Write):
		slog.Debug("Modified file", "name", event.Name)

		watcher.syncChan <- SyncEvent{
			Filename: event.Name,
			Type:     SyncEventModified,
			Origin:   OriginWatcher,
		}

	}
}

func (watcher *Watcher) Run() {
	err := watcher.addRecursively(watcher.dirName)
	if err != nil {
		panic(fmt.Sprintf("Error adding directory '%s': %v", watcher.dirName, err))
	}

	for {
		select {
		case event, ok := <-watcher.watcher.Events:
			if !ok {
				return
			}
			watcher.handleEvent(event)

		case err, ok := <-watcher.watcher.Errors:
			if !ok {
				return
			}
			slog.Error("Watcher error", "error", err)
		}
	}
}
