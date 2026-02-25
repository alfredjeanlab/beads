package server

// Tests for the decision bead type and gate system:
//
//  1. type:decision is in builtinConfigs — kd decision create no longer returns
//     "unknown bead type decision".
//  2. UpdateBead merges fields instead of replacing them — kd decision respond
//     (which only sends response_text/chosen) no longer fails "prompt: is required".
//  3. Full decision gate flow: hook emit upserts gate, responding to a decision
//     bead satisfies the gate and unblocks the Stop hook.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/groblegark/kbeads/internal/events"
	"github.com/groblegark/kbeads/internal/model"
)

// ── stateful gate mock ─────────────────────────────────────────────────────
//
// The base mockStore stubs all gate methods as no-ops. For decision tests we
// need a store that actually tracks gate state so we can verify satisfaction.

type gateKey struct{ agent, gate string }

type gateState struct {
	satisfied bool
	sessionID string
}

type gatedMockStore struct {
	*mockStore
	gates map[gateKey]*gateState
}

func newGatedStore() *gatedMockStore {
	return &gatedMockStore{
		mockStore: newMockStore(),
		gates:     make(map[gateKey]*gateState),
	}
}

func (g *gatedMockStore) UpsertGate(_ context.Context, agentID, gateID, sessionID string) error {
	k := gateKey{agentID, gateID}
	st, exists := g.gates[k]
	if !exists {
		g.gates[k] = &gateState{satisfied: false, sessionID: sessionID}
		return nil
	}
	// New non-empty session ID: reset to pending.
	if sessionID != "" && st.sessionID != sessionID {
		st.satisfied = false
		st.sessionID = sessionID
	}
	return nil
}

func (g *gatedMockStore) MarkGateSatisfied(_ context.Context, agentID, gateID string) error {
	k := gateKey{agentID, gateID}
	if st, ok := g.gates[k]; ok {
		st.satisfied = true
	}
	return nil
}

func (g *gatedMockStore) ClearGate(_ context.Context, agentID, gateID string) error {
	delete(g.gates, gateKey{agentID, gateID})
	return nil
}

func (g *gatedMockStore) IsGateSatisfied(_ context.Context, agentID, gateID string) (bool, error) {
	if st, ok := g.gates[gateKey{agentID, gateID}]; ok {
		return st.satisfied, nil
	}
	return false, nil
}

func (g *gatedMockStore) ListGates(_ context.Context, _ string) ([]model.GateRow, error) {
	return nil, nil
}

// newGatedTestServer returns a server backed by a stateful gate store.
func newGatedTestServer() (*BeadsServer, *gatedMockStore, http.Handler) {
	gs := newGatedStore()
	s := NewBeadsServer(gs, &events.NoopPublisher{})
	return s, gs, s.NewHTTPHandler()
}

// ── type:decision in builtinConfigs ───────────────────────────────────────

// TestDecisionTypeRegistered verifies that POST /v1/beads with type=decision
// succeeds (i.e. the type config is in builtinConfigs).
func TestDecisionTypeRegistered(t *testing.T) {
	_, _, h := newTestServer()
	rec := doJSON(t, h, "POST", "/v1/beads", map[string]any{
		"title": "approve deploy?",
		"type":  "decision",
		"kind":  "data",
		"fields": map[string]any{
			"prompt": "Should we deploy to prod?",
		},
	})
	requireStatus(t, rec, 201)

	var resp map[string]any
	decodeJSON(t, rec, &resp)
	if resp["type"] != "decision" {
		t.Fatalf("expected type=decision, got %v", resp["type"])
	}
	if resp["id"] == "" {
		t.Fatal("expected non-empty id")
	}
}

// TestDecisionTypeConfig verifies that GET /v1/configs/type:decision returns
// the builtin config with kind=data.
func TestDecisionTypeConfig(t *testing.T) {
	_, _, h := newTestServer()
	rec := doJSON(t, h, "GET", "/v1/configs/type:decision", nil)
	requireStatus(t, rec, 200)

	var cfg struct {
		Key   string          `json:"key"`
		Value json.RawMessage `json:"value"`
	}
	decodeJSON(t, rec, &cfg)
	if cfg.Key != "type:decision" {
		t.Fatalf("expected key=type:decision, got %q", cfg.Key)
	}

	var tc map[string]any
	if err := json.Unmarshal(cfg.Value, &tc); err != nil {
		t.Fatalf("failed to decode type config value: %v", err)
	}
	if tc["kind"] != "data" {
		t.Fatalf("expected kind=data, got %v", tc["kind"])
	}
}

// ── field merge on update ──────────────────────────────────────────────────

// TestUpdateDecisionFieldsMerged verifies that PATCH /v1/beads/{id} merges
// the incoming fields into existing ones rather than replacing them.
// This is the fix for kd decision respond failing "prompt: is required".
func TestUpdateDecisionFieldsMerged(t *testing.T) {
	_, ms, h := newTestServer()

	// Create a decision bead with prompt and options already set.
	createRec := doJSON(t, h, "POST", "/v1/beads", map[string]any{
		"title": "deploy?",
		"type":  "decision",
		"kind":  "data",
		"fields": map[string]any{
			"prompt":  "Deploy to production?",
			"options": []map[string]any{{"id": "y", "label": "Yes"}, {"id": "n", "label": "No"}},
		},
	})
	requireStatus(t, createRec, 201)

	var created map[string]any
	decodeJSON(t, createRec, &created)
	id := created["id"].(string)

	// Now update with only the response fields (simulating kd decision respond).
	updateRec := doJSON(t, h, "PATCH", "/v1/beads/"+id, map[string]any{
		"fields": map[string]any{
			"chosen":       "y",
			"responded_by": "alice",
		},
	})
	requireStatus(t, updateRec, 200)

	// Verify the updated bead still has the original prompt AND the new response.
	bead := ms.beads[id]
	if bead == nil {
		t.Fatalf("bead %s not found in store", id)
	}
	var fields map[string]any
	if err := json.Unmarshal(bead.Fields, &fields); err != nil {
		t.Fatalf("failed to decode bead fields: %v", err)
	}
	if fields["prompt"] != "Deploy to production?" {
		t.Errorf("prompt field overwritten; got %v", fields["prompt"])
	}
	if fields["chosen"] != "y" {
		t.Errorf("chosen field not set; got %v", fields["chosen"])
	}
	if fields["responded_by"] != "alice" {
		t.Errorf("responded_by field not set; got %v", fields["responded_by"])
	}
}

// ── decision gate: full flow ───────────────────────────────────────────────

// TestDecisionGateFlow exercises the complete decision gate lifecycle:
//  1. POST /v1/hooks/emit with hook_type=Stop → gate is pending → blocked
//  2. POST /v1/beads (decision) sets requesting_agent_bead_id
//  3. POST /v1/decisions/{id}/resolve → gate is satisfied
//  4. POST /v1/hooks/emit with hook_type=Stop → now unblocked
func TestDecisionGateFlow(t *testing.T) {
	_, gs, h := newGatedTestServer()

	const agentID = "kd-agent-test"

	// Step 1: Emit Stop hook → gate is pending → response should be blocked.
	stopRec := doJSON(t, h, "POST", "/v1/hooks/emit", map[string]any{
		"agent_bead_id":     agentID,
		"hook_type":         "Stop",
		"claude_session_id": "sess-abc",
		"cwd":               "/workspace",
		"actor":             "test-agent",
	})
	requireStatus(t, stopRec, 200)
	var stopResp1 map[string]any
	decodeJSON(t, stopRec, &stopResp1)
	if stopResp1["block"] != true {
		t.Fatalf("expected block=true on first Stop, got %v", stopResp1)
	}

	// Step 2: Create a decision bead referencing the agent.
	decisionRec := doJSON(t, h, "POST", "/v1/beads", map[string]any{
		"title": "approve shutdown?",
		"type":  "decision",
		"kind":  "data",
		"fields": map[string]any{
			"prompt":                   "Can the agent shut down?",
			"requesting_agent_bead_id": agentID,
		},
	})
	requireStatus(t, decisionRec, 201)
	var decisionBead map[string]any
	decodeJSON(t, decisionRec, &decisionBead)
	decisionID := decisionBead["id"].(string)

	// Gate should still be pending (decision not yet resolved).
	if st := gs.gates[gateKey{agentID, "decision"}]; st != nil && st.satisfied {
		t.Fatal("gate should not be satisfied yet")
	}

	// Step 3: Resolve the decision → gate should be satisfied.
	resolveRec := doJSON(t, h, "POST", "/v1/decisions/"+decisionID+"/resolve", map[string]any{
		"selected_option": "y",
		"responded_by":    "test-agent",
	})
	requireStatus(t, resolveRec, 200)

	if st := gs.gates[gateKey{agentID, "decision"}]; st == nil || !st.satisfied {
		t.Fatal("gate should be satisfied after decision resolved")
	}

	// Step 4: Emit Stop hook again → gate is now satisfied → unblocked.
	stopRec2 := doJSON(t, h, "POST", "/v1/hooks/emit", map[string]any{
		"agent_bead_id":     agentID,
		"hook_type":         "Stop",
		"claude_session_id": "sess-abc",
		"cwd":               "/workspace",
		"actor":             "test-agent",
	})
	requireStatus(t, stopRec2, 200)
	var stopResp2 map[string]any
	decodeJSON(t, stopRec2, &stopResp2)
	if stopResp2["block"] == true {
		t.Fatalf("expected unblocked on second Stop after decision resolved, got %v", stopResp2)
	}
}

// TestDecisionGateSatisfiedByClose verifies that closing a decision bead
// (rather than using the /resolve endpoint) also satisfies the gate.
func TestDecisionGateSatisfiedByClose(t *testing.T) {
	_, gs, h := newGatedTestServer()

	const agentID = "kd-agent-close-test"

	// Emit Stop to register the gate.
	stopRec := doJSON(t, h, "POST", "/v1/hooks/emit", map[string]any{
		"agent_bead_id": agentID,
		"hook_type":     "Stop",
		"actor":         "test-agent",
	})
	requireStatus(t, stopRec, 200)
	var stopResp map[string]any
	decodeJSON(t, stopRec, &stopResp)
	if stopResp["block"] != true {
		t.Fatalf("expected block=true, got %v", stopResp)
	}

	// Create and close a decision bead for this agent.
	decisionRec := doJSON(t, h, "POST", "/v1/beads", map[string]any{
		"title": "ok to close?",
		"type":  "decision",
		"kind":  "data",
		"fields": map[string]any{
			"prompt":                   "Proceed?",
			"requesting_agent_bead_id": agentID,
		},
	})
	requireStatus(t, decisionRec, 201)
	var decisionBead map[string]any
	decodeJSON(t, decisionRec, &decisionBead)
	decisionID := decisionBead["id"].(string)

	// Close via POST /v1/beads/{id}/close.
	closeRec := doJSON(t, h, "POST", "/v1/beads/"+decisionID+"/close", map[string]any{
		"closed_by": "test-agent",
	})
	requireStatus(t, closeRec, 200)

	if st := gs.gates[gateKey{agentID, "decision"}]; st == nil || !st.satisfied {
		t.Fatal("gate should be satisfied after closing decision bead")
	}
}

// TestDecisionGateCrossSessionReset verifies that a satisfied gate is reset to
// pending when a new Claude session ID arrives on the Stop hook.
//
// This is the bug: UpsertGate previously used WHERE status='pending' on the
// DO UPDATE, so a satisfied gate was never reset for a new session, causing
// the decision checkpoint to never fire again after the first session.
func TestDecisionGateCrossSessionReset(t *testing.T) {
	_, gs, h := newGatedTestServer()

	const agentID = "kd-agent-cross-session"

	// Session 1: first Stop → gate created pending → blocked.
	stopRec1 := doJSON(t, h, "POST", "/v1/hooks/emit", map[string]any{
		"agent_bead_id":     agentID,
		"hook_type":         "Stop",
		"claude_session_id": "sess-1",
		"actor":             "test-agent",
	})
	requireStatus(t, stopRec1, 200)
	var resp1 map[string]any
	decodeJSON(t, stopRec1, &resp1)
	if resp1["block"] != true {
		t.Fatalf("session 1: expected block=true, got %v", resp1)
	}

	// Session 1: resolve decision → gate satisfied.
	decisionRec := doJSON(t, h, "POST", "/v1/beads", map[string]any{
		"title": "session 1 decision",
		"type":  "decision",
		"kind":  "data",
		"fields": map[string]any{
			"prompt":                   "Proceed?",
			"requesting_agent_bead_id": agentID,
		},
	})
	requireStatus(t, decisionRec, 201)
	var decisionBead map[string]any
	decodeJSON(t, decisionRec, &decisionBead)

	resolveRec := doJSON(t, h, "POST", "/v1/decisions/"+decisionBead["id"].(string)+"/resolve", map[string]any{
		"selected_option": "continue",
		"responded_by":    "human",
	})
	requireStatus(t, resolveRec, 200)

	// Session 1: second Stop → gate is satisfied → allowed.
	stopRec2 := doJSON(t, h, "POST", "/v1/hooks/emit", map[string]any{
		"agent_bead_id":     agentID,
		"hook_type":         "Stop",
		"claude_session_id": "sess-1",
		"actor":             "test-agent",
	})
	requireStatus(t, stopRec2, 200)
	var resp2 map[string]any
	decodeJSON(t, stopRec2, &resp2)
	if resp2["block"] == true {
		t.Fatalf("session 1 second stop: expected unblocked, got %v", resp2)
	}

	// Session 2: new session ID → gate should reset to pending → blocked again.
	stopRec3 := doJSON(t, h, "POST", "/v1/hooks/emit", map[string]any{
		"agent_bead_id":     agentID,
		"hook_type":         "Stop",
		"claude_session_id": "sess-2",
		"actor":             "test-agent",
	})
	requireStatus(t, stopRec3, 200)
	var resp3 map[string]any
	decodeJSON(t, stopRec3, &resp3)
	if resp3["block"] != true {
		t.Fatalf("session 2: expected block=true after session reset, got %v", resp3)
	}
}

// TestDecisionCreateUnknownTypeGone verifies the old "unknown bead type" error
// no longer occurs — regression test for the original bug.
func TestDecisionCreateUnknownTypeGone(t *testing.T) {
	_, _, h := newTestServer()
	rec := doJSON(t, h, "POST", "/v1/beads", map[string]any{
		"title":  "should this work?",
		"type":   "decision",
		"kind":   "data",
		"fields": map[string]any{"prompt": "yes?"},
	})
	// Must NOT be 400 "unknown bead type decision".
	if rec.Code == http.StatusBadRequest {
		t.Fatalf("got 400 (unknown bead type); regression: %s", rec.Body.String())
	}
	requireStatus(t, rec, 201)
}
