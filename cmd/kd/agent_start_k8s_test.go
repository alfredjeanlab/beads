package main

import (
	"testing"
)

func TestMockCommandForScenario(t *testing.T) {
	tests := []struct {
		scenario string
		want     string
	}{
		{
			scenario: "basic-echo",
			want:     "claudeless --dangerously-skip-permissions --scenario /scenarios/basic-echo.toml",
		},
		{
			scenario: "work-and-yield",
			want:     "claudeless --dangerously-skip-permissions --scenario /scenarios/work-and-yield.toml",
		},
	}
	for _, tt := range tests {
		got := mockCommandForScenario(tt.scenario)
		if got != tt.want {
			t.Errorf("mockCommandForScenario(%q) = %q, want %q", tt.scenario, got, tt.want)
		}
	}
}

func TestBuildMockCommand_NilClient(t *testing.T) {
	// buildMockCommand fails open when beadsClient is nil.
	saved := beadsClient
	beadsClient = nil
	defer func() { beadsClient = saved }()

	got := buildMockCommand(t.Context(), "mock-tester", "gasboat")
	if got != "" {
		t.Errorf("expected empty string with nil client, got %q", got)
	}
}
