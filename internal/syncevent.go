package internal

type SyncEventType int

const (
	SyncEventNone SyncEventType = iota
	SyncEventCreated
	SyncEventModified
	SyncEventDeleted
)

func (t SyncEventType) String() string {
	switch t {
	case SyncEventNone:
		return "none"
	case SyncEventCreated:
		return "created"
	case SyncEventModified:
		return "modified"
	case SyncEventDeleted:
		return "deleted"
	}

	return "unknown"
}

type OriginType int

const (
	OriginNone OriginType = iota
	OriginInit
	OriginTimer
	OriginWatcher
)

func (o OriginType) String() string {
	switch o {
	case OriginNone:
		return "none"
	case OriginInit:
		return "init"
	case OriginTimer:
		return "timer"
	case OriginWatcher:
		return "watcher"
	}

	return "unknown"
}

// FileEvent describes what happened to one file during a debounced burst.
type FileEvent struct {
	Filename string
	Type     SyncEventType
}

type SyncEvent struct {
	Filename string
	Type     SyncEventType
	Origin   OriginType
	// Events lists the distinct files affected by a debounced burst, in
	// first-appearance order with the latest event type per file. It is nil
	// when the event never passed through the debouncer.
	Events []FileEvent
}
