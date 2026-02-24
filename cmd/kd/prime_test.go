package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"zero", 0, "0s"},
		{"seconds", 45 * time.Second, "45s"},
		{"one_minute", 60 * time.Second, "1m"},
		{"minutes", 150 * time.Second, "2m"},
		{"one_hour", time.Hour, "1h"},
		{"hours_and_minutes", time.Hour + 30*time.Minute, "1h30m"},
		{"exact_two_hours", 2 * time.Hour, "2h"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatDuration(tt.d)
			if got != tt.want {
				t.Errorf("formatDuration(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}

func TestFormatIdleDur(t *testing.T) {
	tests := []struct {
		name string
		secs float64
		want string
	}{
		{"zero", 0, "0s"},
		{"under_minute", 30, "30s"},
		{"one_minute", 60, "1m0s"},
		{"minutes_seconds", 125, "2m5s"},
		{"one_hour", 3600, "1h0m"},
		{"hours_minutes", 3720, "1h2m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatIdleDur(tt.secs)
			if got != tt.want {
				t.Errorf("formatIdleDur(%f) = %q, want %q", tt.secs, got, tt.want)
			}
		})
	}
}

func TestOutputWorkflowContext(t *testing.T) {
	var buf bytes.Buffer
	outputWorkflowContext(&buf)
	out := buf.String()

	required := []string{
		"# Beads Workflow Context",
		"SESSION CLOSE PROTOCOL",
		"kd prime",
		"kd ready",
		"kd claim",
		"kd close",
		"kd news",
		"kd create",
		"kd show",
		"kd decision create",
		"git push",
	}
	for _, s := range required {
		if !strings.Contains(out, s) {
			t.Errorf("outputWorkflowContext missing %q", s)
		}
	}

	// Should NOT contain bd commands
	bdCmds := []string{"bd ready", "bd claim", "bd close", "bd create"}
	for _, s := range bdCmds {
		if strings.Contains(out, s) {
			t.Errorf("outputWorkflowContext should not contain beads command %q", s)
		}
	}
}

func TestCategorizeScope(t *testing.T) {
	tests := []struct {
		name       string
		labels     []string
		wantScope  string
		wantTarget string
	}{
		{"global", []string{"global"}, "global", ""},
		{"agent", []string{"agent:foo"}, "agent", "foo"},
		{"role", []string{"role:crew"}, "role", "crew"},
		{"rig", []string{"rig:beads"}, "rig", "beads"},
		{"agent_overrides_global", []string{"global", "agent:bar"}, "agent", "bar"},
		{"role_overrides_rig", []string{"rig:x", "role:y"}, "role", "y"},
		{"empty_defaults_global", []string{}, "global", ""},
		{"advice_prefix_not_stripped", []string{"advice:agent:test"}, "global", ""},
		{"g0_prefix_stripped", []string{"g0:agent:test"}, "agent", "test"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scope, target := categorizeScope(tt.labels)
			if scope != tt.wantScope || target != tt.wantTarget {
				t.Errorf("categorizeScope(%v) = (%q, %q), want (%q, %q)",
					tt.labels, scope, target, tt.wantScope, tt.wantTarget)
			}
		})
	}
}

func TestBuildHeader(t *testing.T) {
	tests := []struct {
		scope, target, want string
	}{
		{"global", "", "Global"},
		{"rig", "beads", "Rig: beads"},
		{"role", "crew", "Role: crew"},
		{"agent", "foo", "Agent: foo"},
		{"unknown", "", "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.scope+"_"+tt.target, func(t *testing.T) {
			got := buildHeader(tt.scope, tt.target)
			if got != tt.want {
				t.Errorf("buildHeader(%q, %q) = %q, want %q", tt.scope, tt.target, got, tt.want)
			}
		})
	}
}

func TestGroupSortKey_Ordering(t *testing.T) {
	// Global < rig < role < agent
	keys := []struct {
		scope, target string
	}{
		{"global", ""},
		{"rig", "a"},
		{"role", "b"},
		{"agent", "c"},
	}
	for i := 0; i < len(keys)-1; i++ {
		a := groupSortKey(keys[i].scope, keys[i].target)
		b := groupSortKey(keys[i+1].scope, keys[i+1].target)
		if a >= b {
			t.Errorf("groupSortKey(%q) >= groupSortKey(%q): %q >= %q",
				keys[i].scope, keys[i+1].scope, a, b)
		}
	}
}

func TestResolvePrimeAgentFromEnv(t *testing.T) {
	t.Run("KD_ACTOR_wins", func(t *testing.T) {
		t.Setenv("KD_ACTOR", "env-actor")
		t.Setenv("KD_AGENT_ID", "env-id")
		got := resolvePrimeAgentFromEnv("global-actor")
		if got != "env-actor" {
			t.Errorf("expected env-actor, got %s", got)
		}
	})

	t.Run("KD_AGENT_ID_fallback", func(t *testing.T) {
		t.Setenv("KD_ACTOR", "")
		t.Setenv("KD_AGENT_ID", "agent-id-123")
		got := resolvePrimeAgentFromEnv("global-actor")
		if got != "agent-id-123" {
			t.Errorf("expected agent-id-123, got %s", got)
		}
	})

	t.Run("global_actor_fallback", func(t *testing.T) {
		t.Setenv("KD_ACTOR", "")
		t.Setenv("KD_AGENT_ID", "")
		got := resolvePrimeAgentFromEnv("my-agent")
		if got != "my-agent" {
			t.Errorf("expected my-agent, got %s", got)
		}
	})

	t.Run("unknown_returns_empty", func(t *testing.T) {
		t.Setenv("KD_ACTOR", "")
		t.Setenv("KD_AGENT_ID", "")
		got := resolvePrimeAgentFromEnv("unknown")
		if got != "" {
			t.Errorf("expected empty, got %q", got)
		}
	})

	t.Run("all_empty", func(t *testing.T) {
		t.Setenv("KD_ACTOR", "")
		t.Setenv("KD_AGENT_ID", "")
		got := resolvePrimeAgentFromEnv("")
		if got != "" {
			t.Errorf("expected empty, got %q", got)
		}
	})
}
