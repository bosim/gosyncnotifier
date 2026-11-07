package main

import (
	"errors"
	"strings"
	"testing"
)

// TestPostFailureMessage verifies the critical notification rendered for a
// failed postcmd: error only when no output was captured, otherwise error
// plus the (trimmed, truncated) output.
func TestPostFailureMessage(t *testing.T) {
	err := errors.New("exit status 1")

	t.Run("without output", func(t *testing.T) {
		got := postFailureMessage(err, "")
		if want := "postcmd failed: exit status 1"; got != want {
			t.Errorf("postFailureMessage() = %q, want %q", got, want)
		}
	})

	t.Run("with output", func(t *testing.T) {
		got := postFailureMessage(err, "  conflict-detail \n")
		want := "postcmd failed: exit status 1\nconflict-detail"
		if got != want {
			t.Errorf("postFailureMessage() = %q, want %q", got, want)
		}
	})

	t.Run("long output is truncated", func(t *testing.T) {
		got := postFailureMessage(err, strings.Repeat("x", postOutputLimit+100))

		if !strings.HasPrefix(got, "postcmd failed: exit status 1\n") {
			t.Errorf("postFailureMessage() = %q, want the error as prefix", got)
		}
		if !strings.HasSuffix(got, "...") {
			t.Errorf("postFailureMessage() = %q, want it to end with an ellipsis marker", got)
		}
		if runes := []rune(got); len(runes) > len("postcmd failed: exit status 1\n")+postOutputLimit+len("...") {
			t.Errorf("message length = %d, want at most %d", len(runes), len("postcmd failed: exit status 1\n")+postOutputLimit+len("..."))
		}
	})
}
