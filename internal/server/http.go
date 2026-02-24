package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/groblegark/kbeads/internal/events"
	"github.com/groblegark/kbeads/internal/hooks"
	"github.com/groblegark/kbeads/internal/model"
	"github.com/groblegark/kbeads/internal/presence"
)

// NewHTTPHandler returns an http.Handler with all routes registered.
func (s *BeadsServer) NewHTTPHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/beads", s.handleCreateBead)
	mux.HandleFunc("GET /v1/beads", s.handleListBeads)
	mux.HandleFunc("GET /v1/beads/{id}", s.handleGetBead)
	mux.HandleFunc("PATCH /v1/beads/{id}", s.handleUpdateBead)
	mux.HandleFunc("POST /v1/beads/{id}/close", s.handleCloseBead)
	mux.HandleFunc("DELETE /v1/beads/{id}", s.handleDeleteBead)
	mux.HandleFunc("GET /v1/beads/{id}/dependencies", s.handleGetDependencies)
	mux.HandleFunc("POST /v1/beads/{id}/dependencies", s.handleAddDependency)
	mux.HandleFunc("DELETE /v1/beads/{id}/dependencies", s.handleRemoveDependency)
	mux.HandleFunc("GET /v1/beads/{id}/labels", s.handleGetLabels)
	mux.HandleFunc("POST /v1/beads/{id}/labels", s.handleAddLabel)
	mux.HandleFunc("DELETE /v1/beads/{id}/labels/{label}", s.handleRemoveLabel)
	mux.HandleFunc("GET /v1/beads/{id}/comments", s.handleGetComments)
	mux.HandleFunc("POST /v1/beads/{id}/comments", s.handleAddComment)
	mux.HandleFunc("GET /v1/beads/{id}/events", s.handleGetEvents)
	mux.HandleFunc("PUT /v1/configs/{key...}", s.handleSetConfig)
	mux.HandleFunc("GET /v1/configs/{key...}", s.handleGetConfig)
	mux.HandleFunc("GET /v1/configs", s.handleListConfigs)
	mux.HandleFunc("DELETE /v1/configs/{key...}", s.handleDeleteConfig)
	mux.HandleFunc("GET /v1/graph", s.handleGetGraph)
	mux.HandleFunc("GET /v1/stats", s.handleGetStats)
	mux.HandleFunc("GET /v1/ready", s.handleGetReady)
	mux.HandleFunc("GET /v1/blocked", s.handleGetBlocked)
	mux.HandleFunc("POST /v1/hooks/execute", s.handleExecuteHooks)
	mux.HandleFunc("POST /v1/hooks/emit", s.handleHookEmit)
	mux.HandleFunc("GET /v1/events/stream", s.handleEventStream)
	mux.HandleFunc("GET /v1/decisions/{id}", s.handleGetDecision)
	mux.HandleFunc("GET /v1/decisions", s.handleListDecisions)
	mux.HandleFunc("POST /v1/decisions/{id}/resolve", s.handleResolveDecision)
	mux.HandleFunc("POST /v1/decisions/{id}/cancel", s.handleCancelDecision)
	mux.HandleFunc("GET /v1/health", s.handleHealth)
	mux.HandleFunc("GET /v1/agents/{id}/gates", s.handleListGates)
	mux.HandleFunc("POST /v1/agents/{id}/gates/{gate_id}/satisfy", s.handleSatisfyGate)
	mux.HandleFunc("DELETE /v1/agents/{id}/gates/{gate_id}", s.handleClearGate)
	mux.HandleFunc("GET /v1/agents/roster", s.handleAgentRoster)
	return mux
}

// handleCreateBead handles POST /v1/beads.
func (s *BeadsServer) handleCreateBead(w http.ResponseWriter, r *http.Request) {
	var in createBeadInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	bead, err := s.createBead(r.Context(), in)
	if err != nil {
		var ie inputError
		if errors.As(err, &ie) {
			writeError(w, http.StatusBadRequest, ie.Error())
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	writeJSON(w, http.StatusCreated, bead)
}

// handleListBeads handles GET /v1/beads.
func (s *BeadsServer) handleListBeads(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := model.BeadFilter{
		Assignee: q.Get("assignee"),
		Search:   q.Get("search"),
		Sort:     q.Get("sort"),
	}

	if v := q.Get("status"); v != "" {
		for _, s := range strings.Split(v, ",") {
			filter.Status = append(filter.Status, model.Status(s))
		}
	}
	if v := q.Get("type"); v != "" {
		for _, t := range strings.Split(v, ",") {
			filter.Type = append(filter.Type, model.BeadType(t))
		}
	}
	if v := q.Get("kind"); v != "" {
		for _, k := range strings.Split(v, ",") {
			filter.Kind = append(filter.Kind, model.Kind(k))
		}
	}
	if v := q.Get("labels"); v != "" {
		filter.Labels = strings.Split(v, ",")
	}
	if v := q.Get("priority"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			filter.Priority = &n
		}
	}
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			filter.Limit = n
		}
	}
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			filter.Offset = n
		}
	}

	beads, total, err := s.store.ListBeads(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list beads")
		return
	}

	// Ensure beads is never null in JSON output.
	if beads == nil {
		beads = []*model.Bead{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"beads": beads,
		"total": total,
	})
}

// handleGetBead handles GET /v1/beads/{id}.
func (s *BeadsServer) handleGetBead(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}

	bead, err := s.store.GetBead(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "bead not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get bead")
		return
	}
	if bead == nil {
		writeError(w, http.StatusNotFound, "bead not found")
		return
	}

	writeJSON(w, http.StatusOK, bead)
}

// handleDeleteBead handles DELETE /v1/beads/{id}.
func (s *BeadsServer) handleDeleteBead(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}

	if err := s.store.DeleteBead(r.Context(), id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "bead not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete bead")
		return
	}

	s.recordAndPublish(r.Context(), events.TopicBeadDeleted, id, "", events.BeadDeleted{BeadID: id})

	w.WriteHeader(http.StatusNoContent)
}

// handleUpdateBead handles PATCH /v1/beads/{id}.
func (s *BeadsServer) handleUpdateBead(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}

	var in updateBeadInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	// For HTTP/JSON, DueAt/DeferUntil/Labels presence is inferred from non-nil/non-empty.
	if in.DueAt != nil {
		in.dueAtSet = true
	}
	if in.DeferUntil != nil {
		in.deferUntilSet = true
	}
	if in.Labels != nil {
		in.labelsSet = true
	}

	bead, err := s.updateBead(r.Context(), id, in)
	if err != nil {
		var ie inputError
		if errors.As(err, &ie) {
			writeError(w, http.StatusBadRequest, ie.Error())
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "bead not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, bead)
}

// handleCloseBead handles POST /v1/beads/{id}/close.
// Accepts optional JSON body with "closed_by" and any extra fields to merge
// into the bead's fields before closing (e.g., "chosen", "rationale" for decisions).
func (s *BeadsServer) handleCloseBead(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}

	// Decode body as generic map to capture both closed_by and extra fields.
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)

	closedBy, _ := body["closed_by"].(string)

	// Merge extra fields (anything except closed_by) into the bead before closing.
	extraFields := make(map[string]any)
	for k, v := range body {
		if k != "closed_by" {
			extraFields[k] = v
		}
	}
	if len(extraFields) > 0 {
		if err := s.mergeBeadFields(r.Context(), id, extraFields); err != nil {
			// Non-fatal — log and continue with close.
			slog.Warn("failed to merge close fields", "bead", id, "error", err)
		}
	}

	bead, err := s.store.CloseBead(r.Context(), id, closedBy)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "bead not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to close bead")
		return
	}
	if bead == nil {
		writeError(w, http.StatusNotFound, "bead not found")
		return
	}

	s.recordAndPublish(r.Context(), events.TopicBeadClosed, bead.ID, closedBy, events.BeadClosed{
		Bead:     bead,
		ClosedBy: closedBy,
	})

	// If closing a decision bead, satisfy the requesting agent's gate.
	if bead.Type == model.TypeDecision {
		if agentID := decisionFieldStr(bead.Fields, "requesting_agent_bead_id"); agentID != "" {
			if err := s.store.MarkGateSatisfied(r.Context(), agentID, "decision"); err != nil {
				slog.Warn("failed to satisfy decision gate on close", "agent", agentID, "err", err)
			}
		}
	}

	writeJSON(w, http.StatusOK, bead)
}

// mergeBeadFields merges extra fields into an existing bead's fields JSON.
func (s *BeadsServer) mergeBeadFields(ctx context.Context, id string, extra map[string]any) error {
	bead, err := s.store.GetBead(ctx, id)
	if err != nil {
		return fmt.Errorf("get bead: %w", err)
	}

	// Parse existing fields.
	existing := make(map[string]any)
	if len(bead.Fields) > 0 {
		_ = json.Unmarshal(bead.Fields, &existing)
	}

	// Merge extra fields.
	for k, v := range extra {
		existing[k] = v
	}

	// Marshal back.
	merged, err := json.Marshal(existing)
	if err != nil {
		return fmt.Errorf("marshal fields: %w", err)
	}
	bead.Fields = merged

	return s.store.UpdateBead(ctx, bead)
}

// handleGetDependencies handles GET /v1/beads/{id}/dependencies.
func (s *BeadsServer) handleGetDependencies(w http.ResponseWriter, r *http.Request) {
	beadID := r.PathValue("id")
	if beadID == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}

	deps, err := s.store.GetDependencies(r.Context(), beadID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get dependencies")
		return
	}

	if deps == nil {
		deps = []*model.Dependency{}
	}

	writeJSON(w, http.StatusOK, map[string]any{"dependencies": deps})
}

// addDependencyRequest is the JSON body for POST /v1/beads/{id}/dependencies.
type addDependencyRequest struct {
	DependsOnID string `json:"depends_on_id"`
	Type        string `json:"type"`
	CreatedBy   string `json:"created_by"`
}

// handleAddDependency handles POST /v1/beads/{id}/dependencies.
func (s *BeadsServer) handleAddDependency(w http.ResponseWriter, r *http.Request) {
	beadID := r.PathValue("id")
	if beadID == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}

	var req addDependencyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.DependsOnID == "" {
		writeError(w, http.StatusBadRequest, "depends_on_id is required")
		return
	}

	now := time.Now().UTC()
	dep := &model.Dependency{
		BeadID:      beadID,
		DependsOnID: req.DependsOnID,
		Type:        model.DependencyType(req.Type),
		CreatedAt:   now,
		CreatedBy:   req.CreatedBy,
	}

	if err := s.store.AddDependency(r.Context(), dep); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to add dependency")
		return
	}

	s.recordAndPublish(r.Context(), events.TopicDependencyAdded, dep.BeadID, dep.CreatedBy, events.DependencyAdded{Dependency: dep})

	writeJSON(w, http.StatusCreated, dep)
}

// handleRemoveDependency handles DELETE /v1/beads/{id}/dependencies.
// depends_on_id and type are taken from query parameters.
func (s *BeadsServer) handleRemoveDependency(w http.ResponseWriter, r *http.Request) {
	beadID := r.PathValue("id")
	if beadID == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}

	q := r.URL.Query()
	dependsOnID := q.Get("depends_on_id")
	if dependsOnID == "" {
		writeError(w, http.StatusBadRequest, "depends_on_id query parameter is required")
		return
	}
	depType := q.Get("type")

	if err := s.store.RemoveDependency(r.Context(), beadID, dependsOnID, model.DependencyType(depType)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to remove dependency")
		return
	}

	s.recordAndPublish(r.Context(), events.TopicDependencyRemoved, beadID, "", events.DependencyRemoved{
		BeadID:      beadID,
		DependsOnID: dependsOnID,
		Type:        depType,
	})

	w.WriteHeader(http.StatusNoContent)
}

// handleGetLabels handles GET /v1/beads/{id}/labels.
func (s *BeadsServer) handleGetLabels(w http.ResponseWriter, r *http.Request) {
	beadID := r.PathValue("id")
	if beadID == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}

	labels, err := s.store.GetLabels(r.Context(), beadID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get labels")
		return
	}

	if labels == nil {
		labels = []string{}
	}

	writeJSON(w, http.StatusOK, map[string]any{"labels": labels})
}

// addLabelRequest is the JSON body for POST /v1/beads/{id}/labels.
type addLabelRequest struct {
	Label string `json:"label"`
}

// handleAddLabel handles POST /v1/beads/{id}/labels.
func (s *BeadsServer) handleAddLabel(w http.ResponseWriter, r *http.Request) {
	beadID := r.PathValue("id")
	if beadID == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}

	var req addLabelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Label == "" {
		writeError(w, http.StatusBadRequest, "label is required")
		return
	}

	if err := s.store.AddLabel(r.Context(), beadID, req.Label); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to add label")
		return
	}

	s.recordAndPublish(r.Context(), events.TopicLabelAdded, beadID, "", events.LabelAdded{
		BeadID: beadID,
		Label:  req.Label,
	})

	// Fetch the updated bead to return.
	bead, err := s.store.GetBead(r.Context(), beadID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get bead after adding label")
		return
	}
	if bead == nil {
		writeError(w, http.StatusNotFound, "bead not found")
		return
	}

	writeJSON(w, http.StatusCreated, bead)
}

// handleRemoveLabel handles DELETE /v1/beads/{id}/labels/{label}.
func (s *BeadsServer) handleRemoveLabel(w http.ResponseWriter, r *http.Request) {
	beadID := r.PathValue("id")
	if beadID == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	label := r.PathValue("label")
	if label == "" {
		writeError(w, http.StatusBadRequest, "label is required")
		return
	}

	if err := s.store.RemoveLabel(r.Context(), beadID, label); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to remove label")
		return
	}

	s.recordAndPublish(r.Context(), events.TopicLabelRemoved, beadID, "", events.LabelRemoved{
		BeadID: beadID,
		Label:  label,
	})

	w.WriteHeader(http.StatusNoContent)
}

// handleGetComments handles GET /v1/beads/{id}/comments.
func (s *BeadsServer) handleGetComments(w http.ResponseWriter, r *http.Request) {
	beadID := r.PathValue("id")
	if beadID == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}

	comments, err := s.store.GetComments(r.Context(), beadID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get comments")
		return
	}

	if comments == nil {
		comments = []*model.Comment{}
	}

	writeJSON(w, http.StatusOK, map[string]any{"comments": comments})
}

// addCommentRequest is the JSON body for POST /v1/beads/{id}/comments.
type addCommentRequest struct {
	Author string `json:"author"`
	Text   string `json:"text"`
}

// handleAddComment handles POST /v1/beads/{id}/comments.
func (s *BeadsServer) handleAddComment(w http.ResponseWriter, r *http.Request) {
	beadID := r.PathValue("id")
	if beadID == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}

	var req addCommentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Text == "" {
		writeError(w, http.StatusBadRequest, "text is required")
		return
	}

	now := time.Now().UTC()
	comment := &model.Comment{
		BeadID:    beadID,
		Author:    req.Author,
		Text:      req.Text,
		CreatedAt: now,
	}

	if err := s.store.AddComment(r.Context(), comment); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to add comment")
		return
	}

	s.recordAndPublish(r.Context(), events.TopicCommentAdded, comment.BeadID, comment.Author, events.CommentAdded{Comment: comment})

	writeJSON(w, http.StatusCreated, comment)
}

// handleGetEvents handles GET /v1/beads/{id}/events.
func (s *BeadsServer) handleGetEvents(w http.ResponseWriter, r *http.Request) {
	beadID := r.PathValue("id")
	if beadID == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}

	evts, err := s.store.GetEvents(r.Context(), beadID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get events")
		return
	}

	if evts == nil {
		evts = []*model.Event{}
	}

	writeJSON(w, http.StatusOK, map[string]any{"events": evts})
}

// setConfigRequest is the JSON body for PUT /v1/configs/{key}.
type setConfigRequest struct {
	Value json.RawMessage `json:"value"`
}

// handleSetConfig handles PUT /v1/configs/{key}.
func (s *BeadsServer) handleSetConfig(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if key == "" {
		writeError(w, http.StatusBadRequest, "key is required")
		return
	}

	var req setConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	config := &model.Config{
		Key:   key,
		Value: req.Value,
	}

	if err := s.store.SetConfig(r.Context(), config); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to set config")
		return
	}

	writeJSON(w, http.StatusOK, config)
}

// handleGetConfig handles GET /v1/configs/{key}.
func (s *BeadsServer) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if key == "" {
		writeError(w, http.StatusBadRequest, "key is required")
		return
	}

	config, err := s.store.GetConfig(r.Context(), key)
	if errors.Is(err, sql.ErrNoRows) {
		if builtin, ok := builtinConfigs[key]; ok {
			writeJSON(w, http.StatusOK, builtin)
			return
		}
		writeError(w, http.StatusNotFound, "config not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get config")
		return
	}

	writeJSON(w, http.StatusOK, config)
}

// handleListConfigs handles GET /v1/configs?namespace=...
func (s *BeadsServer) handleListConfigs(w http.ResponseWriter, r *http.Request) {
	namespace := r.URL.Query().Get("namespace")
	if namespace == "" {
		writeError(w, http.StatusBadRequest, "namespace query parameter is required")
		return
	}

	configs, err := s.listConfigsWithBuiltins(r.Context(), namespace)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list configs")
		return
	}

	if configs == nil {
		configs = []*model.Config{}
	}

	writeJSON(w, http.StatusOK, map[string]any{"configs": configs})
}

// handleDeleteConfig handles DELETE /v1/configs/{key}.
func (s *BeadsServer) handleDeleteConfig(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if key == "" {
		writeError(w, http.StatusBadRequest, "key is required")
		return
	}

	if err := s.store.DeleteConfig(r.Context(), key); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "config not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to delete config")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleGetGraph handles GET /v1/graph.
// Returns all beads as nodes, all dependencies as edges, and aggregate stats
// for 3D graph visualization.
func (s *BeadsServer) handleGetGraph(w http.ResponseWriter, r *http.Request) {
	limit := 500
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	graph, err := s.store.GetGraph(r.Context(), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get graph")
		return
	}

	writeJSON(w, http.StatusOK, graph)
}

// handleGetStats handles GET /v1/stats.
func (s *BeadsServer) handleGetStats(w http.ResponseWriter, r *http.Request) {
	stats, err := s.store.GetStats(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get stats")
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

// handleGetReady handles GET /v1/ready.
// Returns beads that are open and have no unsatisfied blocking dependencies.
func (s *BeadsServer) handleGetReady(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	beads, _, err := s.store.ListBeads(r.Context(), model.BeadFilter{
		Status: []model.Status{model.StatusOpen},
		Sort:   "priority",
		Limit:  limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list beads")
		return
	}

	// Filter out beads that have unsatisfied blocking dependencies.
	var ready []*model.Bead
	for _, b := range beads {
		deps, err := s.store.GetDependencies(r.Context(), b.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to get dependencies")
			return
		}
		blocked := false
		for _, d := range deps {
			if d.Type == model.DepBlocks {
				// Check if the blocking bead is still open.
				blocker, err := s.store.GetBead(r.Context(), d.DependsOnID)
				if err != nil {
					continue
				}
				if blocker != nil && blocker.Status != model.StatusClosed {
					blocked = true
					break
				}
			}
		}
		if !blocked {
			ready = append(ready, b)
		}
	}

	if ready == nil {
		ready = []*model.Bead{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"beads": ready,
		"total": len(ready),
	})
}

// handleGetBlocked handles GET /v1/blocked.
// Returns beads with status=blocked, enriched with blocked_by dependency info.
func (s *BeadsServer) handleGetBlocked(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	beads, _, err := s.store.ListBeads(r.Context(), model.BeadFilter{
		Status: []model.Status{model.StatusBlocked},
		Sort:   "priority",
		Limit:  limit,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list beads")
		return
	}

	// Enrich each bead with its dependencies.
	for _, b := range beads {
		deps, err := s.store.GetDependencies(r.Context(), b.ID)
		if err != nil {
			continue
		}
		b.Dependencies = deps
	}

	if beads == nil {
		beads = []*model.Bead{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"beads": beads,
		"total": len(beads),
	})
}

// hookEmitRequest is the JSON body for POST /v1/hooks/emit.
type hookEmitRequest struct {
	AgentBeadID     string `json:"agent_bead_id"`
	HookType        string `json:"hook_type"`        // "Stop", "PreToolUse", etc.
	ClaudeSessionID string `json:"claude_session_id"`
	CWD             string `json:"cwd"`
	Actor           string `json:"actor"`
	ToolName        string `json:"tool_name,omitempty"` // tool name from Claude Code (e.g. "Bash", "Read")
}

// hookEmitResponse is the JSON response from POST /v1/hooks/emit.
type hookEmitResponse struct {
	Block    bool     `json:"block,omitempty"`
	Reason   string   `json:"reason,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
	Inject   string   `json:"inject,omitempty"`
}

// handleHookEmit handles POST /v1/hooks/emit.
// It evaluates gate state and soft auto-checks, returning block/warn/inject signals.
func (s *BeadsServer) handleHookEmit(w http.ResponseWriter, r *http.Request) {
	var req hookEmitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	// Record presence for agent roster tracking.
	if s.Presence != nil && req.Actor != "" {
		s.Presence.RecordHookEvent(presence.HookEvent{
			Actor:     req.Actor,
			HookType:  req.HookType,
			ToolName:  req.ToolName,
			SessionID: req.ClaudeSessionID,
			CWD:       req.CWD,
		})
	}

	ctx := r.Context()
	var resp hookEmitResponse

	// If no agent_bead_id, there are no gates to check — allow.
	if req.AgentBeadID == "" {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	// Evaluate strict gates for Stop hook.
	if req.HookType == "Stop" {
		// Upsert the decision gate for this agent (creates pending row if not exists).
		if err := s.store.UpsertGate(ctx, req.AgentBeadID, "decision", req.ClaudeSessionID); err != nil {
			slog.Warn("hookEmit: failed to upsert decision gate", "agent", req.AgentBeadID, "err", err)
		}

		satisfied, err := s.store.IsGateSatisfied(ctx, req.AgentBeadID, "decision")
		if err != nil {
			slog.Warn("hookEmit: failed to check decision gate", "agent", req.AgentBeadID, "err", err)
		}
		if !satisfied {
			resp.Block = true
			resp.Reason = "decision: decision point offered before session end"
			writeJSON(w, http.StatusOK, resp)
			return
		}
	}

	// Soft gate AutoChecks — always warn, never block.
	if warning := hooks.CheckCommitPush(req.CWD); warning != "" {
		resp.Warnings = append(resp.Warnings, warning)
	}

	// TODO: bead-update soft check — requires checking KD_HOOK_BEAD env var from the
	// client side. Skip server-side check for now; the CLI can handle this in future.

	// Run existing advice hook logic for session-end trigger on Stop hook.
	if req.HookType == "Stop" {
		agentID := req.AgentBeadID
		if req.Actor != "" {
			agentID = req.Actor
		}
		hookResp := s.hooksHandler.HandleSessionEvent(ctx, hooks.SessionEvent{
			AgentID: agentID,
			Trigger: hooks.TriggerSessionEnd,
			CWD:     req.CWD,
		})
		if hookResp.Block && !resp.Block {
			resp.Block = true
			resp.Reason = hookResp.Reason
		}
		resp.Warnings = append(resp.Warnings, hookResp.Warnings...)
	}

	writeJSON(w, http.StatusOK, resp)
}

// executeHooksRequest is the JSON body for POST /v1/hooks/execute.
type executeHooksRequest struct {
	AgentID string `json:"agent_id"`
	Trigger string `json:"trigger"`
	CWD     string `json:"cwd,omitempty"`
}

// handleExecuteHooks handles POST /v1/hooks/execute.
// Agents call this to evaluate advice hooks for a given lifecycle trigger.
func (s *BeadsServer) handleExecuteHooks(w http.ResponseWriter, r *http.Request) {
	var req executeHooksRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.AgentID == "" {
		writeError(w, http.StatusBadRequest, "agent_id is required")
		return
	}
	if req.Trigger == "" {
		writeError(w, http.StatusBadRequest, "trigger is required")
		return
	}

	resp := s.hooksHandler.HandleSessionEvent(r.Context(), hooks.SessionEvent{
		AgentID: req.AgentID,
		Trigger: req.Trigger,
		CWD:     req.CWD,
	})

	writeJSON(w, http.StatusOK, resp)
}

// handleGetDecision handles GET /v1/decisions/{id}.
// Returns the bead as both a decision fields object and the full issue data,
// matching the shape expected by the kbeads3d frontend.
func (s *BeadsServer) handleGetDecision(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}

	bead, err := s.store.GetBead(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) || bead == nil {
		writeError(w, http.StatusNotFound, "decision not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get decision")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"decision": extractDecisionFields(bead),
		"issue":    bead,
	})
}

// handleListDecisions handles GET /v1/decisions.
// Lists beads of type=decision, with optional status filter.
func (s *BeadsServer) handleListDecisions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := model.BeadFilter{
		Type: []model.BeadType{model.TypeDecision},
		Sort: "-created_at",
	}
	if v := q.Get("status"); v != "" {
		for _, st := range strings.Split(v, ",") {
			filter.Status = append(filter.Status, model.Status(st))
		}
	} else {
		filter.Status = []model.Status{model.StatusOpen, model.StatusInProgress}
	}
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			filter.Limit = n
		}
	}
	if filter.Limit == 0 {
		filter.Limit = 50
	}

	beads, _, err := s.store.ListBeads(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list decisions")
		return
	}

	decisions := make([]map[string]any, 0, len(beads))
	for _, b := range beads {
		decisions = append(decisions, map[string]any{
			"decision": extractDecisionFields(b),
			"issue":    b,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{"decisions": decisions})
}

// resolveDecisionRequest is the JSON body for POST /v1/decisions/{id}/resolve.
type resolveDecisionRequest struct {
	SelectedOption string `json:"selected_option"`
	ResponseText   string `json:"response_text"`
	RespondedBy    string `json:"responded_by"`
}

// handleResolveDecision handles POST /v1/decisions/{id}/resolve.
// Merges response fields into the bead, then closes it.
func (s *BeadsServer) handleResolveDecision(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}

	var req resolveDecisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if req.SelectedOption == "" && req.ResponseText == "" {
		writeError(w, http.StatusBadRequest, "selected_option or response_text is required")
		return
	}

	// Merge resolution fields.
	extra := map[string]any{
		"responded_at": time.Now().UTC().Format(time.RFC3339),
	}
	if req.SelectedOption != "" {
		extra["chosen"] = req.SelectedOption
	}
	if req.ResponseText != "" {
		extra["response_text"] = req.ResponseText
	}
	if req.RespondedBy != "" {
		extra["responded_by"] = req.RespondedBy
	}

	if err := s.mergeBeadFields(r.Context(), id, extra); err != nil {
		slog.Warn("failed to merge decision fields", "bead", id, "error", err)
	}

	closedBy := req.RespondedBy
	bead, err := s.store.CloseBead(r.Context(), id, closedBy)
	if errors.Is(err, sql.ErrNoRows) || bead == nil {
		writeError(w, http.StatusNotFound, "decision not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to close decision")
		return
	}

	s.recordAndPublish(r.Context(), events.TopicBeadClosed, bead.ID, closedBy, events.BeadClosed{
		Bead:     bead,
		ClosedBy: closedBy,
	})

	// Satisfy the requesting agent's decision gate when the decision is resolved.
	if agentID := decisionFieldStr(bead.Fields, "requesting_agent_bead_id"); agentID != "" {
		if err := s.store.MarkGateSatisfied(r.Context(), agentID, "decision"); err != nil {
			slog.Warn("failed to satisfy decision gate on resolve", "agent", agentID, "err", err)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"decision": extractDecisionFields(bead),
		"issue":    bead,
	})
}

// handleCancelDecision handles POST /v1/decisions/{id}/cancel.
func (s *BeadsServer) handleCancelDecision(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}

	var body struct {
		Reason    string `json:"reason"`
		CanceledBy string `json:"canceled_by"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	extra := map[string]any{
		"canceled": true,
	}
	if body.Reason != "" {
		extra["cancel_reason"] = body.Reason
	}
	if err := s.mergeBeadFields(r.Context(), id, extra); err != nil {
		slog.Warn("failed to merge cancel fields", "bead", id, "error", err)
	}

	closedBy := body.CanceledBy
	bead, err := s.store.CloseBead(r.Context(), id, closedBy)
	if errors.Is(err, sql.ErrNoRows) || bead == nil {
		writeError(w, http.StatusNotFound, "decision not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel decision")
		return
	}

	s.recordAndPublish(r.Context(), events.TopicBeadClosed, bead.ID, closedBy, events.BeadClosed{
		Bead:     bead,
		ClosedBy: closedBy,
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"decision": extractDecisionFields(bead),
		"issue":    bead,
	})
}

// extractDecisionFields extracts decision-specific fields from a bead's Fields JSON.
// Returns a map with keys like prompt, options, context, selected_option (mapped from chosen),
// requested_by, responded_by, responded_at, response_text, urgency, iteration, max_iterations.
func extractDecisionFields(b *model.Bead) map[string]any {
	dec := make(map[string]any)
	if len(b.Fields) == 0 {
		return dec
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b.Fields, &fields); err != nil {
		return dec
	}

	// String fields — extract and assign.
	stringKeys := []string{"prompt", "context", "requested_by", "responded_by", "responded_at", "response_text", "urgency"}
	for _, k := range stringKeys {
		if raw, ok := fields[k]; ok {
			var s string
			if json.Unmarshal(raw, &s) == nil {
				dec[k] = s
			} else {
				dec[k] = strings.TrimSpace(string(raw))
			}
		}
	}

	// "chosen" in storage maps to "selected_option" in the frontend.
	if raw, ok := fields["chosen"]; ok {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			dec["selected_option"] = s
		}
	}

	// "options" — pass through as-is (may be JSON array string or raw array).
	if raw, ok := fields["options"]; ok {
		dec["options"] = json.RawMessage(raw)
	}

	// Numeric fields.
	numKeys := []string{"iteration", "max_iterations"}
	for _, k := range numKeys {
		if raw, ok := fields[k]; ok {
			var n float64
			if json.Unmarshal(raw, &n) == nil {
				dec[k] = int(n)
			}
		}
	}

	return dec
}

// handleListGates handles GET /v1/agents/{id}/gates.
// Returns all gate rows for an agent bead.
func (s *BeadsServer) handleListGates(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	if agentID == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	gates, err := s.store.ListGates(r.Context(), agentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if gates == nil {
		gates = []model.GateRow{}
	}
	writeJSON(w, http.StatusOK, gates)
}

// handleSatisfyGate handles POST /v1/agents/{id}/gates/{gate_id}/satisfy.
// Manually satisfies a gate for an agent bead.
func (s *BeadsServer) handleSatisfyGate(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	gateID := r.PathValue("gate_id")
	if agentID == "" || gateID == "" {
		writeError(w, http.StatusBadRequest, "id and gate_id are required")
		return
	}
	// Upsert first to ensure row exists.
	if err := s.store.UpsertGate(r.Context(), agentID, gateID, ""); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.store.MarkGateSatisfied(r.Context(), agentID, gateID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "satisfied"})
}

// handleClearGate handles DELETE /v1/agents/{id}/gates/{gate_id}.
// Resets a gate back to pending status.
func (s *BeadsServer) handleClearGate(w http.ResponseWriter, r *http.Request) {
	agentID := r.PathValue("id")
	gateID := r.PathValue("gate_id")
	if agentID == "" || gateID == "" {
		writeError(w, http.StatusBadRequest, "id and gate_id are required")
		return
	}
	if err := s.store.ClearGate(r.Context(), agentID, gateID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "pending"})
}

// handleHealth handles GET /v1/health.
func (s *BeadsServer) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleAgentRoster handles GET /v1/agents/roster.
// Returns the live agent roster from the presence tracker.
func (s *BeadsServer) handleAgentRoster(w http.ResponseWriter, r *http.Request) {
	if s.Presence == nil {
		writeJSON(w, http.StatusOK, map[string]any{"actors": []any{}, "unclaimed_tasks": []any{}})
		return
	}

	// Parse optional stale_threshold_secs query param (default: 30 min).
	staleThreshold := 30 * time.Minute
	if v := r.URL.Query().Get("stale_threshold_secs"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
			staleThreshold = time.Duration(secs) * time.Second
		}
	}

	entries := s.Presence.Roster(staleThreshold)

	// Enrich with task data from the store.
	type rosterEntry struct {
		Actor     string  `json:"actor"`
		TaskID    string  `json:"task_id,omitempty"`
		TaskTitle string  `json:"task_title,omitempty"`
		EpicID    string  `json:"epic_id,omitempty"`
		EpicTitle string  `json:"epic_title,omitempty"`
		IdleSecs  float64 `json:"idle_secs"`
		LastEvent string  `json:"last_event,omitempty"`
		ToolName  string  `json:"tool_name,omitempty"`
		SessionID string  `json:"session_id,omitempty"`
		CWD       string  `json:"cwd,omitempty"`
		Reaped    bool    `json:"reaped,omitempty"`
	}

	actors := make([]rosterEntry, 0, len(entries))
	for _, e := range entries {
		re := rosterEntry{
			Actor:     e.Actor,
			IdleSecs:  e.IdleSecs,
			LastEvent: e.LastEvent,
			ToolName:  e.ToolName,
			SessionID: e.SessionID,
			CWD:       e.CWD,
			Reaped:    e.Reaped,
		}

		// Look up the agent's current in_progress task.
		ctx := r.Context()
		beads, _, err := s.store.ListBeads(ctx, model.BeadFilter{
			Assignee: e.Actor,
			Status:   []model.Status{model.StatusInProgress},
			Limit:    1,
		})
		if err == nil && len(beads) > 0 {
			re.TaskID = beads[0].ID
			re.TaskTitle = beads[0].Title
		}

		actors = append(actors, re)
	}

	// Find unclaimed in_progress beads (no assignee).
	type unclaimedTask struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Priority int    `json:"priority"`
	}

	var unclaimed []unclaimedTask
	allInProgress, _, err := s.store.ListBeads(r.Context(), model.BeadFilter{
		Status: []model.Status{model.StatusInProgress},
		Limit:  50,
	})
	if err == nil {
		for _, b := range allInProgress {
			if b.Assignee == "" {
				unclaimed = append(unclaimed, unclaimedTask{
					ID:       b.ID,
					Title:    b.Title,
					Priority: b.Priority,
				})
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"actors":          actors,
		"unclaimed_tasks": unclaimed,
	})
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
