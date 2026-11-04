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

type SyncEvent struct {
	Filename string
	Type     SyncEventType
	Origin   OriginType
	// Count is the number of file events coalesced into this event by the
	// debouncer (1 for a single forwarded event). Events that never pass
	// through the debouncer (timer, init, debouncing disabled) leave it 0.
	Count int
}
