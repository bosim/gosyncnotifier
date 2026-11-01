package internal

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

const watcherTestTimeout = 2 * time.Second

// newTestWatcher creates a Watcher rooted at a temporary directory and ensures
// the underlying fsnotify watcher is closed when the test finishes.
func newTestWatcher(t *testing.T) (*Watcher, chan SyncEvent, string) {
	t.Helper()

	dir := t.TempDir()
	syncChan := make(chan SyncEvent, 16)

	watcher, err := NewWatcher(syncChan, dir)
	if err != nil {
		t.Fatalf("NewWatcher() error = %v", err)
	}

	t.Cleanup(func() {
		_ = watcher.watcher.Close()
	})

	return watcher, syncChan, dir
}

// waitForWatch waits until path appears in the underlying watch list.
func waitForWatch(t *testing.T, watcher *Watcher, path string) {
	t.Helper()

	deadline := time.Now().Add(watcherTestTimeout)
	for time.Now().Before(deadline) {
		if slices.Contains(watcher.watcher.WatchList(), path) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("timed out waiting for %q to be watched; watch list = %v", path, watcher.watcher.WatchList())
}

// receiveEvent waits for a single SyncEvent or fails the test.
func receiveEvent(t *testing.T, syncChan chan SyncEvent) SyncEvent {
	t.Helper()

	select {
	case event := <-syncChan:
		return event
	case <-time.After(watcherTestTimeout):
		t.Fatal("timed out waiting for sync event")
		return SyncEvent{}
	}
}

func TestNewWatcher(t *testing.T) {
	dir := t.TempDir()
	syncChan := make(chan SyncEvent, 1)

	watcher, err := NewWatcher(syncChan, dir)
	if err != nil {
		t.Fatalf("NewWatcher() error = %v", err)
	}
	t.Cleanup(func() {
		_ = watcher.watcher.Close()
	})

	if watcher.dirName != dir {
		t.Errorf("dirName = %q, want %q", watcher.dirName, dir)
	}
	if watcher.syncChan != syncChan {
		t.Error("syncChan was not stored on the Watcher")
	}
	if watcher.watcher == nil {
		t.Fatal("underlying fsnotify watcher is nil")
	}
}

func TestAddRecursively(t *testing.T) {
	watcher, _, dir := newTestWatcher(t)

	nested := filepath.Join(dir, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	if err := watcher.addRecursively(dir); err != nil {
		t.Fatalf("addRecursively() error = %v", err)
	}

	wantWatched := []string{
		dir,
		filepath.Join(dir, "a"),
		filepath.Join(dir, "a", "b"),
		nested,
	}
	watchList := watcher.watcher.WatchList()
	for _, path := range wantWatched {
		if !slices.Contains(watchList, path) {
			t.Errorf("watch list %v does not contain %q", watchList, path)
		}
	}
}

func TestAddRecursivelyIsIdempotent(t *testing.T) {
	watcher, _, dir := newTestWatcher(t)

	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	if err := watcher.addRecursively(dir); err != nil {
		t.Fatalf("first addRecursively() error = %v", err)
	}
	firstCount := len(watcher.watcher.WatchList())

	if err := watcher.addRecursively(dir); err != nil {
		t.Fatalf("second addRecursively() error = %v", err)
	}
	secondCount := len(watcher.watcher.WatchList())

	if firstCount != secondCount {
		t.Errorf("watch list grew after repeated addRecursively: first=%d second=%d", firstCount, secondCount)
	}
}

func TestAddRecursivelyMissingPath(t *testing.T) {
	watcher, _, dir := newTestWatcher(t)

	missing := filepath.Join(dir, "does-not-exist")
	if err := watcher.addRecursively(missing); err == nil {
		t.Fatal("addRecursively() on a missing path returned nil error")
	}
}

func TestRemoveRecursive(t *testing.T) {
	watcher, _, dir := newTestWatcher(t)

	target := filepath.Join(dir, "target")
	targetNested := filepath.Join(target, "nested")
	sibling := filepath.Join(dir, "sibling")

	for _, path := range []string{targetNested, sibling} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", path, err)
		}
	}

	if err := watcher.addRecursively(dir); err != nil {
		t.Fatalf("addRecursively() error = %v", err)
	}

	watcher.removeRecursive(target)

	watchList := watcher.watcher.WatchList()
	for _, removed := range []string{target, targetNested} {
		if slices.Contains(watchList, removed) {
			t.Errorf("watch list %v still contains removed path %q", watchList, removed)
		}
	}
	for _, kept := range []string{dir, sibling} {
		if !slices.Contains(watchList, kept) {
			t.Errorf("watch list %v does not contain path %q that should be kept", watchList, kept)
		}
	}
}

func TestHandleEventCreateFile(t *testing.T) {
	watcher, syncChan, dir := newTestWatcher(t)

	file := filepath.Join(dir, "created.txt")
	if err := os.WriteFile(file, []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	watcher.handleEvent(fsnotify.Event{Name: file, Op: fsnotify.Create})

	event := receiveEvent(t, syncChan)
	if event.Filename != file {
		t.Errorf("Filename = %q, want %q", event.Filename, file)
	}
	if event.Type != SyncEventCreated {
		t.Errorf("Type = %v, want %v", event.Type, SyncEventCreated)
	}
	if event.Origin != OriginWatcher {
		t.Errorf("Origin = %v, want %v", event.Origin, OriginWatcher)
	}
}

func TestHandleEventCreateDirectoryAddsRecursiveWatches(t *testing.T) {
	watcher, syncChan, dir := newTestWatcher(t)

	newDir := filepath.Join(dir, "new", "nested")
	if err := os.MkdirAll(newDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	watcher.handleEvent(fsnotify.Event{Name: filepath.Join(dir, "new"), Op: fsnotify.Create})

	event := receiveEvent(t, syncChan)
	if event.Type != SyncEventCreated {
		t.Errorf("Type = %v, want %v", event.Type, SyncEventCreated)
	}

	watchList := watcher.watcher.WatchList()
	for _, path := range []string{filepath.Join(dir, "new"), newDir} {
		if !slices.Contains(watchList, path) {
			t.Errorf("watch list %v does not contain newly created directory %q", watchList, path)
		}
	}
}

func TestHandleEventCreateMissingPathStillEmitsEvent(t *testing.T) {
	watcher, syncChan, dir := newTestWatcher(t)

	missing := filepath.Join(dir, "vanished")
	watcher.handleEvent(fsnotify.Event{Name: missing, Op: fsnotify.Create})

	event := receiveEvent(t, syncChan)
	if event.Filename != missing {
		t.Errorf("Filename = %q, want %q", event.Filename, missing)
	}
	if event.Type != SyncEventCreated {
		t.Errorf("Type = %v, want %v", event.Type, SyncEventCreated)
	}
}

func TestHandleEventRemoveWatchedDirectory(t *testing.T) {
	watcher, syncChan, dir := newTestWatcher(t)

	sub := filepath.Join(dir, "sub")
	nested := filepath.Join(sub, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := watcher.addRecursively(dir); err != nil {
		t.Fatalf("addRecursively() error = %v", err)
	}

	watcher.handleEvent(fsnotify.Event{Name: sub, Op: fsnotify.Remove})

	event := receiveEvent(t, syncChan)
	if event.Filename != sub || event.Type != SyncEventDeleted || event.Origin != OriginWatcher {
		t.Errorf("unexpected event: %+v", event)
	}

	watchList := watcher.watcher.WatchList()
	for _, removed := range []string{sub, nested} {
		if slices.Contains(watchList, removed) {
			t.Errorf("watch list %v still contains removed directory %q", watchList, removed)
		}
	}
}

func TestHandleEventRenameWatchedDirectory(t *testing.T) {
	watcher, syncChan, dir := newTestWatcher(t)

	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	if err := watcher.addRecursively(dir); err != nil {
		t.Fatalf("addRecursively() error = %v", err)
	}

	watcher.handleEvent(fsnotify.Event{Name: sub, Op: fsnotify.Rename})

	event := receiveEvent(t, syncChan)
	if event.Filename != sub || event.Type != SyncEventDeleted || event.Origin != OriginWatcher {
		t.Errorf("unexpected event: %+v", event)
	}
	if slices.Contains(watcher.watcher.WatchList(), sub) {
		t.Errorf("watch list still contains renamed directory %q", sub)
	}
}

func TestHandleEventRemoveUnwatchedFile(t *testing.T) {
	watcher, syncChan, dir := newTestWatcher(t)

	file := filepath.Join(dir, "unwatched.txt")
	watcher.handleEvent(fsnotify.Event{Name: file, Op: fsnotify.Remove})

	event := receiveEvent(t, syncChan)
	if event.Filename != file || event.Type != SyncEventDeleted || event.Origin != OriginWatcher {
		t.Errorf("unexpected event: %+v", event)
	}
}

func TestHandleEventWrite(t *testing.T) {
	watcher, syncChan, dir := newTestWatcher(t)

	file := filepath.Join(dir, "written.txt")
	watcher.handleEvent(fsnotify.Event{Name: file, Op: fsnotify.Write})

	event := receiveEvent(t, syncChan)
	if event.Filename != file || event.Type != SyncEventModified || event.Origin != OriginWatcher {
		t.Errorf("unexpected event: %+v", event)
	}
}

func TestHandleEventChmodProducesNoEvent(t *testing.T) {
	watcher, syncChan, dir := newTestWatcher(t)

	watcher.handleEvent(fsnotify.Event{Name: filepath.Join(dir, "file.txt"), Op: fsnotify.Chmod})

	select {
	case event := <-syncChan:
		t.Errorf("unexpected event for chmod: %+v", event)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestRunStopsOnContextCancellation(t *testing.T) {
	watcher, _, dir := newTestWatcher(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		watcher.Run(ctx)
	}()

	waitForWatch(t, watcher, dir)
	cancel()

	select {
	case <-done:
	case <-time.After(watcherTestTimeout):
		t.Fatal("Run did not return after context cancellation")
	}
}

func TestRunReturnsForInvalidDirectory(t *testing.T) {
	watcher, _, dir := newTestWatcher(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		watcher.dirName = filepath.Join(dir, "missing")
		watcher.Run(ctx)
	}()

	select {
	case <-done:
	case <-time.After(watcherTestTimeout):
		t.Fatal("Run did not return after failing to watch an invalid directory")
	}
}

func TestRunStopsWhenUnderlyingWatcherCloses(t *testing.T) {
	watcher, _, dir := newTestWatcher(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		watcher.Run(ctx)
	}()

	waitForWatch(t, watcher, dir)
	if err := watcher.watcher.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	select {
	case <-done:
	case <-time.After(watcherTestTimeout):
		t.Fatal("Run did not return after the underlying watcher was closed")
	}
}

// TestRunDetectsFileChanges exercises the full event pipeline with real
// filesystem notifications.
func TestRunDetectsFileChanges(t *testing.T) {
	watcher, syncChan, dir := newTestWatcher(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		watcher.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(watcherTestTimeout):
			t.Error("Run did not stop during cleanup")
		}
	})

	waitForWatch(t, watcher, dir)

	file := filepath.Join(dir, "watched.txt")
	if err := os.WriteFile(file, []byte("data"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	deadline := time.After(watcherTestTimeout)
	for {
		select {
		case event := <-syncChan:
			if event.Filename != file {
				continue
			}
			if event.Origin != OriginWatcher {
				t.Errorf("Origin = %v, want %v", event.Origin, OriginWatcher)
			}
			if event.Type != SyncEventCreated && event.Type != SyncEventModified {
				t.Errorf("Type = %v, want %v or %v", event.Type, SyncEventCreated, SyncEventModified)
			}
			return
		case <-deadline:
			t.Fatalf("timed out waiting for an event for %q", file)
		}
	}
}

// TestRunDetectsNewDirectory exercises recursive watching of a directory that
// is created after the watcher starts.
func TestRunDetectsNewDirectory(t *testing.T) {
	watcher, syncChan, dir := newTestWatcher(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		watcher.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(watcherTestTimeout):
			t.Error("Run did not stop during cleanup")
		}
	})

	waitForWatch(t, watcher, dir)

	newDir := filepath.Join(dir, "newdir")
	if err := os.Mkdir(newDir, 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	// Wait for the create event and the recursive watch registration.
	deadline := time.After(watcherTestTimeout)
	for {
		select {
		case event := <-syncChan:
			if event.Filename == newDir && event.Type == SyncEventCreated {
				waitForWatch(t, watcher, newDir)
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for creation event for %q", newDir)
		}
	}
}
