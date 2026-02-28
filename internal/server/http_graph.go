package server

import (
	"net/http"
	"strconv"

	"github.com/groblegark/kbeads/internal/model"
)

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
