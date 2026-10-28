package internal

import (
	"testing"
)

// TestRunCommand verifies runCommand behavior with valid arguments
func TestRunCommand(t *testing.T) {
	tests := []struct {
		name     string
		program  string
		args     []string
		wantErr  bool
	}{
		{
			name:     "Run echo with single argument",
			program:  "echo",
			args:     []string{"test output"},
			wantErr:  false,
		},
		{
			name:     "Run echo with multiple arguments",
			program:  "echo",
			args:     []string{"arg1", "arg2"},
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// We assume runCommand returns an error on execution failure.
			err := runCommand(tt.program, tt.args)

			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected an error, but got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
			}
		})
	}
}

// TestRunCommand_InvalidCommand tests behavior when the program does not exist
func TestRunCommand_InvalidCommand(t *testing.T) {
	tests := []struct {
		name     string
		program  string
		args     []string
		wantErr  bool
	}{
		{
			name:     "Run non-existent command",
			program:  "nonexistentcommand",
			args:     []string{},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runCommand(tt.program, tt.args)

			if !tt.wantErr && err != nil {
				t.Errorf("Did not expect an error, but got: %v", err)
			}
			if tt.wantErr && err == nil {
				t.Errorf("Expected an error, but got nil")
			}
		})
	}
}
