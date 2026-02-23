package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	beadsv1 "github.com/alfredjeanlab/beads/gen/beads/v1"
)

func printBeadJSON(bead *beadsv1.Bead) {
	data, err := json.MarshalIndent(bead, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshaling JSON: %v\n", err)
		return
	}
	fmt.Println(string(data))
}

func printBeadTable(bead *beadsv1.Bead) {
	fmt.Printf("ID:          %s\n", bead.GetId())
	fmt.Printf("Slug:        %s\n", bead.GetSlug())
	fmt.Printf("Title:       %s\n", bead.GetTitle())
	fmt.Printf("Type:        %s\n", bead.GetType())
	fmt.Printf("Kind:        %s\n", bead.GetKind())
	fmt.Printf("Status:      %s\n", bead.GetStatus())
	fmt.Printf("Priority:    %d\n", bead.GetPriority())
	fmt.Printf("Assignee:    %s\n", bead.GetAssignee())
	fmt.Printf("Owner:       %s\n", bead.GetOwner())
	if bead.GetDescription() != "" {
		fmt.Printf("Description: %s\n", bead.GetDescription())
	}
	if len(bead.GetLabels()) > 0 {
		fmt.Printf("Labels:      %s\n", strings.Join(bead.GetLabels(), ", "))
	}
	fmt.Printf("Created By:  %s\n", bead.GetCreatedBy())
	if bead.GetCreatedAt() != nil {
		fmt.Printf("Created At:  %s\n", bead.GetCreatedAt().AsTime().Format("2006-01-02 15:04:05"))
	}
	if bead.GetUpdatedAt() != nil {
		fmt.Printf("Updated At:  %s\n", bead.GetUpdatedAt().AsTime().Format("2006-01-02 15:04:05"))
	}
}

func printBeadListJSON(beads []*beadsv1.Bead) {
	data, err := json.MarshalIndent(beads, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshaling JSON: %v\n", err)
		return
	}
	fmt.Println(string(data))
}

func printBeadListTable(beads []*beadsv1.Bead, total int32) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tTYPE\tPRIORITY\tTITLE\tASSIGNEE")
	for _, b := range beads {
		title := b.GetTitle()
		if len(title) > 50 {
			title = title[:47] + "..."
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\n",
			b.GetId(),
			b.GetStatus(),
			b.GetType(),
			b.GetPriority(),
			title,
			b.GetAssignee(),
		)
	}
	w.Flush()
	fmt.Printf("\n%d beads (%d total)\n", len(beads), total)
}

// resolvedDep pairs a dependency with its optionally-resolved target bead.
type resolvedDep struct {
	Dep  *beadsv1.Dependency
	Bead *beadsv1.Bead // nil if fetch failed
}

// fetchAndResolveDeps fetches dependencies for a bead and resolves each target.
// If types is non-empty, only dependencies matching one of the given types are included.
func fetchAndResolveDeps(ctx context.Context, c beadsv1.BeadsServiceClient, beadID string, types []string) ([]resolvedDep, error) {
	resp, err := c.GetDependencies(ctx, &beadsv1.GetDependenciesRequest{
		BeadId: beadID,
	})
	if err != nil {
		return nil, err
	}
	return resolveBeadDeps(ctx, c, resp.GetDependencies(), types), nil
}

// resolveBeadDeps takes an existing dependency slice, filters by type, and resolves each target bead.
func resolveBeadDeps(ctx context.Context, c beadsv1.BeadsServiceClient, deps []*beadsv1.Dependency, types []string) []resolvedDep {
	typeSet := make(map[string]bool, len(types))
	for _, t := range types {
		typeSet[t] = true
	}

	var resolved []resolvedDep
	for _, d := range deps {
		if len(typeSet) > 0 && !typeSet[d.GetType()] {
			continue
		}
		rd := resolvedDep{Dep: d}
		beadResp, err := c.GetBead(ctx, &beadsv1.GetBeadRequest{Id: d.GetDependsOnId()})
		if err == nil {
			rd.Bead = beadResp.GetBead()
		}
		resolved = append(resolved, rd)
	}
	return resolved
}

// printDepSubSection prints resolved dependencies as indented lines.
func printDepSubSection(deps []resolvedDep, fields []string) {
	if len(deps) == 0 {
		return
	}
	if len(fields) == 0 {
		fields = []string{"id", "title", "status"}
	}
	for _, rd := range deps {
		if rd.Bead != nil {
			vals := make([]string, len(fields))
			for i, f := range fields {
				vals[i] = beadField(rd.Bead, f)
			}
			fmt.Printf("    %s: %s\n", rd.Dep.GetType(), strings.Join(vals, " | "))
		} else {
			fmt.Printf("    %s: %s (unresolved)\n", rd.Dep.GetType(), rd.Dep.GetDependsOnId())
		}
	}
}

// printBeadTableFiltered prints bead detail fields, restricted to the given whitelist.
// If fields is nil or empty, all fields are printed (delegates to printBeadTable).
func printBeadTableFiltered(bead *beadsv1.Bead, fields []string) {
	if len(fields) == 0 {
		printBeadTable(bead)
		return
	}
	fieldSet := make(map[string]bool, len(fields))
	for _, f := range fields {
		fieldSet[strings.ToLower(f)] = true
	}
	type fieldRow struct {
		label string
		key   string
		value string
	}
	rows := []fieldRow{
		{"ID", "id", bead.GetId()},
		{"Slug", "slug", bead.GetSlug()},
		{"Title", "title", bead.GetTitle()},
		{"Type", "type", bead.GetType()},
		{"Kind", "kind", bead.GetKind()},
		{"Status", "status", bead.GetStatus()},
		{"Priority", "priority", fmt.Sprintf("%d", bead.GetPriority())},
		{"Assignee", "assignee", bead.GetAssignee()},
		{"Owner", "owner", bead.GetOwner()},
	}
	for _, r := range rows {
		if fieldSet[r.key] {
			fmt.Printf("%-13s%s\n", r.label+":", r.value)
		}
	}
	if fieldSet["description"] && bead.GetDescription() != "" {
		fmt.Printf("%-13s%s\n", "Description:", bead.GetDescription())
	}
	if fieldSet["labels"] && len(bead.GetLabels()) > 0 {
		fmt.Printf("%-13s%s\n", "Labels:", strings.Join(bead.GetLabels(), ", "))
	}
	if fieldSet["created_by"] {
		fmt.Printf("%-13s%s\n", "Created By:", bead.GetCreatedBy())
	}
	if fieldSet["created_at"] && bead.GetCreatedAt() != nil {
		fmt.Printf("%-13s%s\n", "Created At:", bead.GetCreatedAt().AsTime().Format("2006-01-02 15:04:05"))
	}
	if fieldSet["updated_at"] && bead.GetUpdatedAt() != nil {
		fmt.Printf("%-13s%s\n", "Updated At:", bead.GetUpdatedAt().AsTime().Format("2006-01-02 15:04:05"))
	}
}

// printComments prints bead comments in a standard format.
func printComments(comments []*beadsv1.Comment) {
	if len(comments) == 0 {
		return
	}
	fmt.Println()
	fmt.Println("Comments:")
	for _, c := range comments {
		ts := ""
		if c.GetCreatedAt() != nil {
			ts = c.GetCreatedAt().AsTime().Format("2006-01-02 15:04:05")
		}
		fmt.Printf("  [%s] %s: %s\n", ts, c.GetAuthor(), c.GetText())
	}
}
