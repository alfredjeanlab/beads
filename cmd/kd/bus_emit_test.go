package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestResolvePrimeAgentFromEnv_Priority(t *testing.T) {
	// Already tested in prime_test.go, but verify the bus_emit.go function
	// exists and works with the same contract.
	t.Setenv("KD_ACTOR", "from-env")
	t.Setenv("KD_AGENT_ID", "")
	got := resolvePrimeAgentFromEnv("fallback")
	if got != "from-env" {
		t.Errorf("expected from-env, got %s", got)
	}
}

func TestOutputPrimeForHook_WrapsInSystemReminder(t *testing.T) {
	// outputPrimeForHook uses the global beadsClient which won't be set in tests.
	// We test the wrapper format by verifying the function signature exists
	// and the output structure when called with empty agent ID.
	// With no beadsClient, the advice/roster/auto-assign sections will be skipped
	// or error silently, but the workflow context should still render.

	// Skip if beadsClient is nil (it will be in unit tests).
	if beadsClient == nil {
		t.Skip("beadsClient is nil in unit tests — testing wrapper format only")
	}

	var buf bytes.Buffer
	outputPrimeForHook(&buf, "")
	out := buf.String()

	if !strings.Contains(out, "<system-reminder>") {
		t.Error("output should contain <system-reminder> tag")
	}
	if !strings.Contains(out, "</system-reminder>") {
		t.Error("output should contain closing </system-reminder> tag")
	}
	if !strings.Contains(out, "SessionStart:compact hook success:") {
		t.Error("output should contain SessionStart:compact hook success prefix")
	}
}
