package server

// Tests for the decision bead type and gate system:
//
//  1. type:decision is in builtinConfigs — kd decision create no longer returns
//     "unknown bead type decision".
//  2. UpdateBead merges fields instead of replacing them — kd decision respond
//     (which only sends response_text/chosen) no longer fails "prompt: is required".
//  3. Full decision gate flow: hook emit upserts gate, gb yield satisfies the
//     gate (via POST /v1/agents/{id}/gates/decision/satisfy), which unblocks Stop.
//  4. Gate is consumed on allow: after Stop is allowed, the next Stop blocks again.

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

func (g *gatedMockStore) UpsertGate(_ context.Context, agentID, gateID, _ string) error {
	k := gateKey{agentID, gateID}
	if _, exists := g.gates[k]; !exists {
		g.gates[k] = &gateState{satisfied: false}
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
//  3. POST /v1/decisions/{id}/resolve → closes decision (gate NOT yet satisfied)
//  4. POST /v1/agents/{id}/gates/decision/satisfy → gate satisfied (simulates gb yield)
//  5. POST /v1/hooks/emit with hook_type=Stop → now unblocked (gate consumed)
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

	// Step 3: Resolve the decision. Gate is NOT satisfied by resolve — that is
	// now gb yield's responsibility (via POST .../satisfy).
	resolveRec := doJSON(t, h, "POST", "/v1/decisions/"+decisionID+"/resolve", map[string]any{
		"selected_option": "y",
		"responded_by":    "test-agent",
	})
	requireStatus(t, resolveRec, 200)

	// Gate should still be pending after resolve.
	if st := gs.gates[gateKey{agentID, "decision"}]; st != nil && st.satisfied {
		t.Fatal("gate should not be satisfied by resolve alone")
	}

	// Step 4: gb yield calls the satisfy endpoint after detecting the decision
	// was resolved.
	satisfyRec := doJSON(t, h, "POST", "/v1/agents/"+agentID+"/gates/decision/satisfy", nil)
	requireStatus(t, satisfyRec, 200)

	if st := gs.gates[gateKey{agentID, "decision"}]; st == nil || !st.satisfied {
		t.Fatal("gate should be satisfied after satisfy endpoint called")
	}

	// Step 5: Emit Stop hook again → gate is now satisfied → unblocked (and gate consumed).
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
		t.Fatalf("expected unblocked on Stop after gate satisfied, got %v", stopResp2)
	}
}

// TestDecisionGateNotSatisfiedByClose verifies that closing a decision bead
// alone does NOT satisfy the gate. Gate satisfaction is now gb yield's
// responsibility (via POST /v1/agents/{id}/gates/{gate}/satisfy).
func TestDecisionGateNotSatisfiedByClose(t *testing.T) {
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

	// Gate must still be pending after close — satisfaction is gb yield's job.
	if st := gs.gates[gateKey{agentID, "decision"}]; st != nil && st.satisfied {
		t.Fatal("gate must not be satisfied by close alone; gb yield must call the satisfy endpoint")
	}
}

// TestDecisionGateConsumed verifies the one-shot consumption model: after a
// satisfied gate allows a Stop, the gate is reset to pending so the next Stop
// blocks again. A new decision+yield cycle is required for every Stop.
func TestDecisionGateConsumed(t *testing.T) {
	_, _, h := newGatedTestServer()

	const agentID = "kd-agent-consumed"

	// Step 1: First Stop → gate pending → blocked.
	stop1 := doJSON(t, h, "POST", "/v1/hooks/emit", map[string]any{
		"agent_bead_id": agentID,
		"hook_type":     "Stop",
		"actor":         "test-agent",
	})
	requireStatus(t, stop1, 200)
	var r1 map[string]any
	decodeJSON(t, stop1, &r1)
	if r1["block"] != true {
		t.Fatalf("step 1: expected block=true, got %v", r1)
	}

	// Step 2: gb yield calls satisfy endpoint → gate satisfied.
	satisfyRec := doJSON(t, h, "POST", "/v1/agents/"+agentID+"/gates/decision/satisfy", nil)
	requireStatus(t, satisfyRec, 200)

	// Step 3: Second Stop → gate satisfied → allowed, gate consumed (reset to pending).
	stop2 := doJSON(t, h, "POST", "/v1/hooks/emit", map[string]any{
		"agent_bead_id": agentID,
		"hook_type":     "Stop",
		"actor":         "test-agent",
	})
	requireStatus(t, stop2, 200)
	var r2 map[string]any
	decodeJSON(t, stop2, &r2)
	if r2["block"] == true {
		t.Fatalf("step 3: expected unblocked after gate satisfied, got %v", r2)
	}

	// Step 4: Third Stop → gate was consumed/reset → blocked again.
	stop3 := doJSON(t, h, "POST", "/v1/hooks/emit", map[string]any{
		"agent_bead_id": agentID,
		"hook_type":     "Stop",
		"actor":         "test-agent",
	})
	requireStatus(t, stop3, 200)
	var r3 map[string]any
	decodeJSON(t, stop3, &r3)
	if r3["block"] != true {
		t.Fatalf("step 4: expected block=true after gate was consumed, got %v", r3)
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
