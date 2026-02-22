package server

import (
	"fmt"
	"testing"
	"time"

	beadsv1 "github.com/alfredjeanlab/beads/gen/beads/v1"
	"github.com/alfredjeanlab/beads/internal/model"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestGRPCCreateBead(t *testing.T) {
	srv, ms, ctx := testCtx(t)
	resp, err := srv.CreateBead(ctx, &beadsv1.CreateBeadRequest{
		Title: "Test bead", Type: "task", CreatedBy: "alice",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b := resp.Bead
	if b.Id == "" || b.Title != "Test bead" || b.Status != "open" || b.Kind != "issue" || b.CreatedBy != "alice" {
		t.Fatalf("unexpected bead: id=%q title=%q status=%q kind=%q created_by=%q", b.Id, b.Title, b.Status, b.Kind, b.CreatedBy)
	}
	requireEvent(t, ms, 1, "beads.bead.created")
}

func TestGRPCCreateBead_WithLabels(t *testing.T) {
	srv, ms, ctx := testCtx(t)
	resp, err := srv.CreateBead(ctx, &beadsv1.CreateBeadRequest{
		Title: "Labeled bead", Type: "task", Labels: []string{"urgent", "frontend"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ms.labels[resp.Bead.Id]) != 2 {
		t.Fatalf("expected 2 labels stored, got %d", len(ms.labels[resp.Bead.Id]))
	}
}

func TestGRPCGetBead(t *testing.T) {
	srv, ms, ctx := testCtx(t)
	ms.beads["bd-test1"] = &model.Bead{ID: "bd-test1", Title: "Test bead", Kind: model.KindIssue, Type: model.TypeTask, Status: model.StatusOpen}

	resp, err := srv.GetBead(ctx, &beadsv1.GetBeadRequest{Id: "bd-test1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Bead.Id != "bd-test1" || resp.Bead.Title != "Test bead" {
		t.Fatalf("got id=%q title=%q", resp.Bead.Id, resp.Bead.Title)
	}
}

func TestGRPCListBeads(t *testing.T) {
	srv, ms, ctx := testCtx(t)
	ms.beads["bd-1"] = &model.Bead{ID: "bd-1", Title: "A", Status: model.StatusOpen}
	ms.beads["bd-2"] = &model.Bead{ID: "bd-2", Title: "B", Status: model.StatusOpen}

	resp, err := srv.ListBeads(ctx, &beadsv1.ListBeadsRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Total != 2 || len(resp.Beads) != 2 {
		t.Fatalf("expected 2 beads, got total=%d len=%d", resp.Total, len(resp.Beads))
	}
}

func TestGRPCUpdateBead(t *testing.T) {
	srv, ms, ctx := testCtx(t)
	ms.beads["bd-upd1"] = &model.Bead{ID: "bd-upd1", Title: "Original", Kind: model.KindIssue, Type: model.TypeTask, Status: model.StatusOpen}

	title := "Updated"
	resp, err := srv.UpdateBead(ctx, &beadsv1.UpdateBeadRequest{Id: "bd-upd1", Title: &title})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Bead.Title != "Updated" {
		t.Fatalf("got title=%q", resp.Bead.Title)
	}
	requireEvent(t, ms, 1, "beads.bead.updated")
}

func TestGRPCUpdateBead_StatusClosed(t *testing.T) {
	srv, ms, ctx := testCtx(t)
	ms.beads["bd-upd2"] = &model.Bead{ID: "bd-upd2", Title: "To close", Kind: model.KindIssue, Type: model.TypeTask, Status: model.StatusOpen}

	closed := "closed"
	resp, err := srv.UpdateBead(ctx, &beadsv1.UpdateBeadRequest{Id: "bd-upd2", Status: &closed})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Bead.Status != "closed" || resp.Bead.ClosedAt == nil {
		t.Fatalf("got status=%q closed_at=%v", resp.Bead.Status, resp.Bead.ClosedAt)
	}
}

func TestGRPCCloseBead(t *testing.T) {
	srv, ms, ctx := testCtx(t)
	ms.beads["bd-cls1"] = &model.Bead{ID: "bd-cls1", Title: "To close", Kind: model.KindIssue, Type: model.TypeTask, Status: model.StatusOpen}

	resp, err := srv.CloseBead(ctx, &beadsv1.CloseBeadRequest{Id: "bd-cls1", ClosedBy: "alice"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Bead.Status != "closed" {
		t.Fatalf("got status=%q", resp.Bead.Status)
	}
	requireEvent(t, ms, 1, "beads.bead.closed")
}

func TestGRPCDeleteBead(t *testing.T) {
	srv, ms, ctx := testCtx(t)
	ms.beads["bd-del1"] = &model.Bead{ID: "bd-del1", Title: "Delete me", Status: model.StatusOpen}

	if _, err := srv.DeleteBead(ctx, &beadsv1.DeleteBeadRequest{Id: "bd-del1"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := ms.beads["bd-del1"]; ok {
		t.Fatal("expected bead to be deleted from store")
	}
	requireEvent(t, ms, 1, "beads.bead.deleted")
}

func TestGRPCUpdateBead_ClearDeferUntil(t *testing.T) {
	srv, ms, ctx := testCtx(t)
	future := time.Now().Add(24 * time.Hour)
	ms.beads["bd-def1"] = &model.Bead{
		ID: "bd-def1", Title: "Deferred", Kind: model.KindIssue, Type: model.TypeTask,
		Status: model.StatusDeferred, DeferUntil: &future,
	}

	// Send a zero-time timestamp to clear defer_until.
	zero := timestamppb.New(time.Time{})
	resp, err := srv.UpdateBead(ctx, &beadsv1.UpdateBeadRequest{Id: "bd-def1", DeferUntil: zero})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Bead.DeferUntil != nil {
		t.Fatalf("expected defer_until to be cleared, got %v", resp.Bead.DeferUntil)
	}
}

func TestGRPCUpdateBead_ClearDueAt(t *testing.T) {
	srv, ms, ctx := testCtx(t)
	future := time.Now().Add(24 * time.Hour)
	ms.beads["bd-due1"] = &model.Bead{
		ID: "bd-due1", Title: "With due", Kind: model.KindIssue, Type: model.TypeTask,
		Status: model.StatusOpen, DueAt: &future,
	}

	zero := timestamppb.New(time.Time{})
	resp, err := srv.UpdateBead(ctx, &beadsv1.UpdateBeadRequest{Id: "bd-due1", DueAt: zero})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Bead.DueAt != nil {
		t.Fatalf("expected due_at to be cleared, got %v", resp.Bead.DueAt)
	}
}

func TestGRPCUpdateBead_SetDeferUntilPreserved(t *testing.T) {
	srv, ms, ctx := testCtx(t)
	ms.beads["bd-def2"] = &model.Bead{
		ID: "bd-def2", Title: "Will defer", Kind: model.KindIssue, Type: model.TypeTask,
		Status: model.StatusDeferred,
	}

	future := time.Now().Add(48 * time.Hour).Truncate(time.Microsecond)
	ts := timestamppb.New(future)
	resp, err := srv.UpdateBead(ctx, &beadsv1.UpdateBeadRequest{Id: "bd-def2", DeferUntil: ts})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Bead.DeferUntil == nil {
		t.Fatal("expected defer_until to be set")
	}
	got := resp.Bead.DeferUntil.AsTime().Truncate(time.Microsecond)
	if !got.Equal(future) {
		t.Fatalf("expected defer_until=%v, got %v", future, got)
	}
}

func TestGRPCCloseBead_SetsClosedBy(t *testing.T) {
	srv, ms, ctx := testCtx(t)
	ms.beads["bd-cb1"] = &model.Bead{ID: "bd-cb1", Title: "Close me", Kind: model.KindIssue, Type: model.TypeTask, Status: model.StatusOpen}

	resp, err := srv.CloseBead(ctx, &beadsv1.CloseBeadRequest{Id: "bd-cb1", ClosedBy: "alice"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Bead.Status != "closed" {
		t.Fatalf("expected status=closed, got %q", resp.Bead.Status)
	}
	// Verify closedBy was stored on the model.
	stored := ms.beads["bd-cb1"]
	if stored.ClosedBy != "alice" {
		t.Fatalf("expected stored closedBy=%q, got %q", "alice", stored.ClosedBy)
	}
}

func TestGRPCCloseBead_EmptyClosedBy(t *testing.T) {
	srv, ms, ctx := testCtx(t)
	ms.beads["bd-cb2"] = &model.Bead{ID: "bd-cb2", Title: "Close me", Kind: model.KindIssue, Type: model.TypeTask, Status: model.StatusOpen}

	resp, err := srv.CloseBead(ctx, &beadsv1.CloseBeadRequest{Id: "bd-cb2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Bead.Status != "closed" {
		t.Fatalf("expected status=closed, got %q", resp.Bead.Status)
	}
}

func TestGRPCUpdateBead_LabelsArePersisted(t *testing.T) {
	srv, ms, ctx := testCtx(t)
	ms.beads["bd-lrec"] = &model.Bead{ID: "bd-lrec", Title: "Labeled", Kind: model.KindIssue, Type: model.TypeTask, Status: model.StatusOpen}
	ms.labels["bd-lrec"] = []string{"a", "b"}

	resp, err := srv.UpdateBead(ctx, &beadsv1.UpdateBeadRequest{Id: "bd-lrec", Labels: []string{"b", "c"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = resp

	// Check the store has reconciled labels.
	labelSet := map[string]bool{}
	for _, l := range ms.labels["bd-lrec"] {
		labelSet[l] = true
	}
	if !labelSet["b"] || !labelSet["c"] || labelSet["a"] {
		t.Fatalf("expected labels [b, c], got %v", ms.labels["bd-lrec"])
	}
}

func TestGRPCCreateBead_LabelFailure_ReturnsError(t *testing.T) {
	srv, ms, ctx := testCtx(t)
	ms.addLabelErr = fmt.Errorf("label store down")

	_, err := srv.CreateBead(ctx, &beadsv1.CreateBeadRequest{
		Title: "With labels", Type: "task", Labels: []string{"x"},
	})
	if err == nil {
		t.Fatal("expected error when AddLabel fails")
	}
}
