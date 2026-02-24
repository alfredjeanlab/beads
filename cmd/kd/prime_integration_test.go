package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/groblegark/kbeads/internal/client"
	"github.com/groblegark/kbeads/internal/model"
)

// mockBeadsClient implements client.BeadsClient with canned responses
// for testing prime output functions that need a client.
type mockBeadsClient struct {
	listBeadsFunc      func(ctx context.Context, req *client.ListBeadsRequest) (*client.ListBeadsResponse, error)
	getAgentRosterFunc func(ctx context.Context, staleThresholdSecs int) (*client.AgentRosterResponse, error)
	updateBeadFunc     func(ctx context.Context, id string, req *client.UpdateBeadRequest) (*model.Bead, error)

	// Track update calls.
	updateCalls []updateCall
}

type updateCall struct {
	ID  string
	Req *client.UpdateBeadRequest
}

func (m *mockBeadsClient) ListBeads(ctx context.Context, req *client.ListBeadsRequest) (*client.ListBeadsResponse, error) {
	if m.listBeadsFunc != nil {
		return m.listBeadsFunc(ctx, req)
	}
	return &client.ListBeadsResponse{}, nil
}

func (m *mockBeadsClient) GetAgentRoster(ctx context.Context, staleThresholdSecs int) (*client.AgentRosterResponse, error) {
	if m.getAgentRosterFunc != nil {
		return m.getAgentRosterFunc(ctx, staleThresholdSecs)
	}
	return &client.AgentRosterResponse{}, nil
}

func (m *mockBeadsClient) UpdateBead(ctx context.Context, id string, req *client.UpdateBeadRequest) (*model.Bead, error) {
	m.updateCalls = append(m.updateCalls, updateCall{ID: id, Req: req})
	if m.updateBeadFunc != nil {
		return m.updateBeadFunc(ctx, id, req)
	}
	return &model.Bead{ID: id}, nil
}

// Stub methods — not used by prime output functions.
func (m *mockBeadsClient) CreateBead(context.Context, *client.CreateBeadRequest) (*model.Bead, error) {
	return nil, nil
}
func (m *mockBeadsClient) GetBead(context.Context, string) (*model.Bead, error) { return nil, nil }
func (m *mockBeadsClient) CloseBead(context.Context, string, string) (*model.Bead, error) {
	return nil, nil
}
func (m *mockBeadsClient) DeleteBead(context.Context, string) error { return nil }
func (m *mockBeadsClient) AddDependency(context.Context, *client.AddDependencyRequest) (*model.Dependency, error) {
	return nil, nil
}
func (m *mockBeadsClient) RemoveDependency(context.Context, string, string, string) error {
	return nil
}
func (m *mockBeadsClient) GetDependencies(context.Context, string) ([]*model.Dependency, error) {
	return nil, nil
}
func (m *mockBeadsClient) AddLabel(context.Context, string, string) (*model.Bead, error) {
	return nil, nil
}
func (m *mockBeadsClient) RemoveLabel(context.Context, string, string) error   { return nil }
func (m *mockBeadsClient) GetLabels(context.Context, string) ([]string, error) { return nil, nil }
func (m *mockBeadsClient) AddComment(context.Context, string, string, string) (*model.Comment, error) {
	return nil, nil
}
func (m *mockBeadsClient) GetComments(context.Context, string) ([]*model.Comment, error) {
	return nil, nil
}
func (m *mockBeadsClient) GetEvents(context.Context, string) ([]*model.Event, error) {
	return nil, nil
}
func (m *mockBeadsClient) SetConfig(context.Context, string, json.RawMessage) (*model.Config, error) {
	return nil, nil
}
func (m *mockBeadsClient) GetConfig(context.Context, string) (*model.Config, error) { return nil, nil }
func (m *mockBeadsClient) ListConfigs(context.Context, string) ([]*model.Config, error) {
	return nil, nil
}
func (m *mockBeadsClient) DeleteConfig(context.Context, string) error { return nil }
func (m *mockBeadsClient) EmitHook(context.Context, *client.EmitHookRequest) (*client.EmitHookResponse, error) {
	return nil, nil
}
func (m *mockBeadsClient) ListGates(context.Context, string) ([]model.GateRow, error) {
	return nil, nil
}
func (m *mockBeadsClient) SatisfyGate(context.Context, string, string) error { return nil }
func (m *mockBeadsClient) ClearGate(context.Context, string, string) error   { return nil }
func (m *mockBeadsClient) Health(context.Context) (string, error)            { return "ok", nil }
func (m *mockBeadsClient) Close() error                                      { return nil }

// --- outputJackSection tests ---

func TestOutputJackSection_ActiveAndExpired(t *testing.T) {
	now := time.Now()
	expiredDue := now.Add(-30 * time.Minute)
	activeDue := now.Add(45 * time.Minute)

	mc := &mockBeadsClient{
		listBeadsFunc: func(_ context.Context, req *client.ListBeadsRequest) (*client.ListBeadsResponse, error) {
			// Should be called with type=jack, status=in_progress.
			if len(req.Type) != 1 || req.Type[0] != "jack" {
				t.Errorf("expected type=[jack], got %v", req.Type)
			}
			if len(req.Status) != 1 || req.Status[0] != "in_progress" {
				t.Errorf("expected status=[in_progress], got %v", req.Status)
			}
			return &client.ListBeadsResponse{
				Beads: []*model.Bead{
					{
						ID:       "bd-jack1",
						Type:     "jack",
						Assignee: "wise-newt",
						DueAt:    &expiredDue,
						Fields:   json.RawMessage(`{"jack_target": "pod/bd-daemon-abc"}`),
					},
					{
						ID:        "bd-jack2",
						Type:      "jack",
						CreatedBy: "ripe-elk",
						DueAt:     &activeDue,
						Fields:    json.RawMessage(`{"jack_target": "deployment/api"}`),
					},
				},
			}, nil
		},
	}

	var buf bytes.Buffer
	outputJackSection(&buf, mc)
	out := buf.String()

	if !strings.Contains(out, "Active Jacks (2)") {
		t.Error("missing 'Active Jacks (2)' header")
	}
	if !strings.Contains(out, "EXPIRED") {
		t.Error("missing EXPIRED marker for expired jack")
	}
	if !strings.Contains(out, "bd-jack1") {
		t.Error("missing expired jack ID")
	}
	if !strings.Contains(out, "pod/bd-daemon-abc") {
		t.Error("missing expired jack target")
	}
	if !strings.Contains(out, "bd-jack2") {
		t.Error("missing active jack ID")
	}
	if !strings.Contains(out, "deployment/api") {
		t.Error("missing active jack target")
	}
	if !strings.Contains(out, "remaining") {
		t.Error("missing 'remaining' for active jack")
	}
}

func TestOutputJackSection_NoJacks(t *testing.T) {
	mc := &mockBeadsClient{
		listBeadsFunc: func(_ context.Context, _ *client.ListBeadsRequest) (*client.ListBeadsResponse, error) {
			return &client.ListBeadsResponse{}, nil
		},
	}

	var buf bytes.Buffer
	outputJackSection(&buf, mc)

	if buf.Len() != 0 {
		t.Errorf("expected no output for empty jacks, got %q", buf.String())
	}
}

// --- outputRosterSection tests ---

func TestOutputRosterSection_ActiveAndStale(t *testing.T) {
	mc := &mockBeadsClient{
		getAgentRosterFunc: func(_ context.Context, _ int) (*client.AgentRosterResponse, error) {
			return &client.AgentRosterResponse{
				Actors: []client.RosterEntry{
					{
						Actor:     "wise-newt",
						TaskID:    "bd-task1",
						TaskTitle: "Fix login bug",
						EpicTitle: "Auth epic",
						IdleSecs:  30,
						LastEvent: "PostToolUse",
						ToolName:  "Bash",
					},
					{
						Actor:    "ripe-elk",
						IdleSecs: 5,
						ToolName: "Read",
					},
					{
						Actor:    "crashed-agent",
						IdleSecs: 900,
						Reaped:   true,
					},
				},
				UnclaimedTasks: []client.UnclaimedTask{
					{ID: "bd-orphan", Title: "Unclaimed work", Priority: 1},
				},
			}, nil
		},
	}

	var buf bytes.Buffer
	outputRosterSection(&buf, mc, "wise-newt")
	out := buf.String()

	if !strings.Contains(out, "Active Agents (2)") {
		t.Errorf("missing 'Active Agents' header, got: %s", out)
	}
	if !strings.Contains(out, "You are **wise-newt**") {
		t.Error("missing 'You are' self-identification")
	}
	if !strings.Contains(out, "← you") {
		t.Error("missing '← you' tag for self")
	}
	if !strings.Contains(out, "bd-task1") {
		t.Error("missing task ID in roster")
	}
	if !strings.Contains(out, "Fix login bug") {
		t.Error("missing task title in roster")
	}
	if !strings.Contains(out, "Auth epic") {
		t.Error("missing epic title in roster")
	}
	if !strings.Contains(out, "Crashed") {
		t.Error("missing Crashed section for reaped agent")
	}
	if !strings.Contains(out, "crashed-agent") {
		t.Error("missing crashed agent name")
	}
	if !strings.Contains(out, "Unclaimed in-progress work") {
		t.Error("missing unclaimed tasks section")
	}
	if !strings.Contains(out, "bd-orphan") {
		t.Error("missing unclaimed task ID")
	}
}

func TestOutputRosterSection_EmptyRoster(t *testing.T) {
	mc := &mockBeadsClient{
		getAgentRosterFunc: func(_ context.Context, _ int) (*client.AgentRosterResponse, error) {
			return &client.AgentRosterResponse{}, nil
		},
	}

	var buf bytes.Buffer
	outputRosterSection(&buf, mc, "test-agent")

	if buf.Len() != 0 {
		t.Errorf("expected no output for empty roster, got %q", buf.String())
	}
}

func TestOutputRosterSection_StoppedAgentsExcluded(t *testing.T) {
	mc := &mockBeadsClient{
		getAgentRosterFunc: func(_ context.Context, _ int) (*client.AgentRosterResponse, error) {
			return &client.AgentRosterResponse{
				Actors: []client.RosterEntry{
					{Actor: "active-agent", IdleSecs: 10, LastEvent: "PostToolUse"},
					{Actor: "stopped-agent", IdleSecs: 120, LastEvent: "Stop"},
				},
			}, nil
		},
	}

	var buf bytes.Buffer
	outputRosterSection(&buf, mc, "active-agent")
	out := buf.String()

	if !strings.Contains(out, "Active Agents (1)") {
		t.Errorf("expected 1 active agent (stopped excluded), got: %s", out)
	}
	if strings.Contains(out, "stopped-agent") {
		t.Error("stopped agent should be excluded from output")
	}
}

// --- outputAutoAssign tests ---

func TestOutputAutoAssign_AssignsIdleAgent(t *testing.T) {
	callIdx := 0
	mc := &mockBeadsClient{
		listBeadsFunc: func(_ context.Context, req *client.ListBeadsRequest) (*client.ListBeadsResponse, error) {
			callIdx++
			if callIdx == 1 {
				// First call: check for in_progress work (should be empty).
				if req.Assignee != "test-agent" {
					t.Errorf("first ListBeads call should filter by assignee, got %q", req.Assignee)
				}
				return &client.ListBeadsResponse{}, nil
			}
			// Second call: fetch ready tasks.
			return &client.ListBeadsResponse{
				Beads: []*model.Bead{
					{ID: "bd-ready1", Title: "Ready task", Priority: 1},
				},
			}, nil
		},
	}

	var buf bytes.Buffer
	outputAutoAssign(&buf, mc, "test-agent")
	out := buf.String()

	if !strings.Contains(out, "Auto-assigned bead bd-ready1") {
		t.Errorf("expected auto-assign message, got: %s", out)
	}
	if !strings.Contains(out, "Ready task") {
		t.Error("missing task title in auto-assign output")
	}
	if len(mc.updateCalls) != 1 {
		t.Fatalf("expected 1 update call, got %d", len(mc.updateCalls))
	}
	if mc.updateCalls[0].ID != "bd-ready1" {
		t.Errorf("update call ID = %q, want bd-ready1", mc.updateCalls[0].ID)
	}
	if *mc.updateCalls[0].Req.Assignee != "test-agent" {
		t.Errorf("update assignee = %q, want test-agent", *mc.updateCalls[0].Req.Assignee)
	}
	if *mc.updateCalls[0].Req.Status != "in_progress" {
		t.Errorf("update status = %q, want in_progress", *mc.updateCalls[0].Req.Status)
	}
}

func TestOutputAutoAssign_SkipsWhenBusy(t *testing.T) {
	mc := &mockBeadsClient{
		listBeadsFunc: func(_ context.Context, _ *client.ListBeadsRequest) (*client.ListBeadsResponse, error) {
			// Return in_progress bead — agent is busy.
			return &client.ListBeadsResponse{
				Beads: []*model.Bead{
					{ID: "bd-current", Title: "Current work", Status: model.StatusInProgress},
				},
			}, nil
		},
	}

	var buf bytes.Buffer
	outputAutoAssign(&buf, mc, "busy-agent")

	if buf.Len() != 0 {
		t.Errorf("expected no output when agent has work, got %q", buf.String())
	}
	if len(mc.updateCalls) != 0 {
		t.Error("should not call UpdateBead when agent is busy")
	}
}

func TestOutputAutoAssign_NoReadyTasks(t *testing.T) {
	callIdx := 0
	mc := &mockBeadsClient{
		listBeadsFunc: func(_ context.Context, _ *client.ListBeadsRequest) (*client.ListBeadsResponse, error) {
			callIdx++
			// Both calls return empty.
			return &client.ListBeadsResponse{}, nil
		},
	}

	var buf bytes.Buffer
	outputAutoAssign(&buf, mc, "idle-agent")

	if buf.Len() != 0 {
		t.Errorf("expected no output when no ready tasks, got %q", buf.String())
	}
}

// --- outputAdvice tests ---

func TestOutputAdvice_GroupedByScope(t *testing.T) {
	// Agent ID "beads/crew/test-agent" generates subscriptions:
	// global, agent:beads/crew/test-agent, rig:beads, role:crew
	mc := &mockBeadsClient{
		listBeadsFunc: func(_ context.Context, req *client.ListBeadsRequest) (*client.ListBeadsResponse, error) {
			if len(req.Type) == 1 && req.Type[0] == "advice" {
				return &client.ListBeadsResponse{
					Beads: []*model.Bead{
						{
							ID:          "bd-adv1",
							Title:       "Global advice",
							Description: "Applies to everyone",
							Labels:      []string{"global"},
						},
						{
							ID:          "bd-adv2",
							Title:       "Rig advice",
							Description: "For beads rig",
							Labels:      []string{"rig:beads"},
						},
						{
							ID:          "bd-adv3",
							Title:       "Role advice",
							Description: "For crew role",
							Labels:      []string{"role:crew"},
						},
						{
							ID:          "bd-adv4",
							Title:       "Agent advice",
							Description: "For test-agent only",
							Labels:      []string{"agent:beads/crew/test-agent"},
						},
						{
							ID:          "bd-adv5",
							Title:       "Unmatched advice",
							Description: "For other-agent only",
							Labels:      []string{"agent:other-agent"},
						},
					},
				}, nil
			}
			return &client.ListBeadsResponse{}, nil
		},
	}

	var buf bytes.Buffer
	outputAdvice(&buf, mc, "beads/crew/test-agent")
	out := buf.String()

	// Should include matching advice (global, rig:beads, role:crew, agent:beads/crew/test-agent).
	if !strings.Contains(out, "Advice (4 items)") {
		t.Errorf("expected 4 matched advice items, got: %s", out)
	}
	if !strings.Contains(out, "Global advice") {
		t.Error("missing global advice")
	}
	if !strings.Contains(out, "[Global]") {
		t.Error("missing [Global] header")
	}
	if !strings.Contains(out, "Rig advice") {
		t.Error("missing rig advice")
	}
	if !strings.Contains(out, "[Rig: beads]") {
		t.Error("missing [Rig: beads] header")
	}
	if !strings.Contains(out, "Role advice") {
		t.Error("missing role advice")
	}
	if !strings.Contains(out, "[Role: crew]") {
		t.Error("missing [Role: crew] header")
	}
	if !strings.Contains(out, "Agent advice") {
		t.Error("missing agent-specific advice")
	}

	// Should NOT include unmatched advice.
	if strings.Contains(out, "Unmatched advice") {
		t.Error("should not include advice for other-agent")
	}
}

func TestOutputAdvice_NoAdvice(t *testing.T) {
	mc := &mockBeadsClient{
		listBeadsFunc: func(_ context.Context, _ *client.ListBeadsRequest) (*client.ListBeadsResponse, error) {
			return &client.ListBeadsResponse{}, nil
		},
	}

	var buf bytes.Buffer
	outputAdvice(&buf, mc, "test-agent")

	if buf.Len() != 0 {
		t.Errorf("expected no output for empty advice, got %q", buf.String())
	}
}
