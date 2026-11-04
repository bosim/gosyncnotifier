package internal

import (
	"fmt"
	"testing"
)

// TestSyncEventTypeString verifies every event type has a stable, human-readable
// name and that out-of-range values fall back to "unknown".
func TestSyncEventTypeString(t *testing.T) {
	tests := []struct {
		name      string
		eventType SyncEventType
		want      string
	}{
		{name: "none", eventType: SyncEventNone, want: "none"},
		{name: "created", eventType: SyncEventCreated, want: "created"},
		{name: "modified", eventType: SyncEventModified, want: "modified"},
		{name: "deleted", eventType: SyncEventDeleted, want: "deleted"},
		{name: "unknown positive", eventType: SyncEventType(99), want: "unknown"},
		{name: "unknown negative", eventType: SyncEventType(-1), want: "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.eventType.String(); got != tt.want {
				t.Errorf("SyncEventType(%d).String() = %q, want %q", tt.eventType, got, tt.want)
			}
		})
	}
}

// TestOriginTypeString verifies every origin has a stable, human-readable name
// and that out-of-range values fall back to "unknown".
func TestOriginTypeString(t *testing.T) {
	tests := []struct {
		name   string
		origin OriginType
		want   string
	}{
		{name: "none", origin: OriginNone, want: "none"},
		{name: "init", origin: OriginInit, want: "init"},
		{name: "timer", origin: OriginTimer, want: "timer"},
		{name: "watcher", origin: OriginWatcher, want: "watcher"},
		{name: "unknown positive", origin: OriginType(99), want: "unknown"},
		{name: "unknown negative", origin: OriginType(-1), want: "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.origin.String(); got != tt.want {
				t.Errorf("OriginType(%d).String() = %q, want %q", tt.origin, got, tt.want)
			}
		})
	}
}

// TestSyncEventZeroValue documents the defaults of a freshly allocated event:
// no file, no event type, and no origin.
func TestSyncEventZeroValue(t *testing.T) {
	var event SyncEvent

	if event.Filename != "" {
		t.Errorf("Filename = %q, want empty", event.Filename)
	}
	if event.Type != SyncEventNone {
		t.Errorf("Type = %v, want %v", event.Type, SyncEventNone)
	}
	if event.Origin != OriginNone {
		t.Errorf("Origin = %v, want %v", event.Origin, OriginNone)
	}
	if event.Events != nil {
		t.Errorf("Events = %v, want nil", event.Events)
	}
}

// TestEventTypesAreFormatters ensures the types can be used anywhere a
// fmt.Stringer is expected, matching how logging and notifications consume them.
func TestEventTypesAreFormatters(t *testing.T) {
	var _ fmt.Stringer = SyncEventType(0)
	var _ fmt.Stringer = OriginType(0)

	if got, want := fmt.Sprint(SyncEventCreated), "created"; got != want {
		t.Errorf("fmt.Sprint(SyncEventCreated) = %q, want %q", got, want)
	}
	if got, want := fmt.Sprintf("%v", OriginTimer), "timer"; got != want {
		t.Errorf("fmt.Sprintf(OriginTimer) = %q, want %q", got, want)
	}
}
