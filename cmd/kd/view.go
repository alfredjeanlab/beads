package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	beadsv1 "github.com/groblegark/kbeads/gen/beads/v1"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// depConfig controls optional dependency sub-sections displayed below beads.
type depConfig struct {
	Types  []string `json:"types,omitempty"`  // dep types to include; empty = all
	Fields []string `json:"fields,omitempty"` // fields of resolved target bead; default: id,title,status
}

// viewConfig is the client-side interpretation of a view:{name} config value.
type viewConfig struct {
	Filter  viewFilter `json:"filter"`
	Sort    string     `json:"sort"`
	Columns []string   `json:"columns"`
	Limit   int32      `json:"limit"`
	Deps    *depConfig `json:"deps,omitempty"`
}

type viewFilter struct {
	Status   []string          `json:"status"`
	Type     []string          `json:"type"`
	Kind     []string          `json:"kind"`
	Labels   []string          `json:"labels"`
	Assignee string            `json:"assignee"`
	Search   string            `json:"search"`
	Priority *int32            `json:"priority"`
	Fields   map[string]string `json:"fields,omitempty"`
}

var viewCmd = &cobra.Command{
	Use:     "view <name>",
	Short:   "Run a saved view (named query)",
	GroupID: "views",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		limitOverride, _ := cmd.Flags().GetInt32("limit")

		// 1. Fetch the view config.
		resp, err := client.GetConfig(context.Background(), &beadsv1.GetConfigRequest{
			Key: "view:" + name,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		var vc viewConfig
		if err := json.Unmarshal(resp.GetConfig().GetValue(), &vc); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing view config: %v\n", err)
			os.Exit(1)
		}

		// 2. Build the ListBeads request.
		req := &beadsv1.ListBeadsRequest{
			Status:   vc.Filter.Status,
			Type:     vc.Filter.Type,
			Kind:     vc.Filter.Kind,
			Labels:   vc.Filter.Labels,
			Assignee: expandVar(vc.Filter.Assignee),
			Search:   vc.Filter.Search,
			Sort:     vc.Sort,
			Limit:    vc.Limit,
		}
		if vc.Filter.Priority != nil {
			req.Priority = wrapperspb.Int32(*vc.Filter.Priority)
		}
		if len(vc.Filter.Fields) > 0 {
			req.FieldFilters = vc.Filter.Fields
		}
		if limitOverride > 0 {
			req.Limit = limitOverride
		}

		// 3. Call ListBeads.
		listResp, err := client.ListBeads(context.Background(), req)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		// 4. Display results.
		if jsonOutput {
			printBeadListJSON(listResp.GetBeads())
		} else if len(vc.Columns) > 0 {
			printBeadListColumns(listResp.GetBeads(), listResp.GetTotal(), vc.Columns)
		} else {
			printBeadListTable(listResp.GetBeads(), listResp.GetTotal())
		}

		// 5. Optional dependency sub-sections.
		if !jsonOutput && vc.Deps != nil && len(listResp.GetBeads()) > 0 {
			printViewDeps(listResp.GetBeads(), vc.Deps)
		}
		return nil
	},
}

// expandVar replaces well-known variables in filter values.
func expandVar(s string) string {
	s = strings.ReplaceAll(s, "$BEADS_ACTOR", actor)
	return s
}

// printBeadListColumns prints beads using a custom set of columns.
func printBeadListColumns(beads []*beadsv1.Bead, total int32, columns []string) {
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	// Header
	headers := make([]string, len(columns))
	for i, c := range columns {
		headers[i] = strings.ToUpper(c)
	}
	fmt.Fprintln(tw, strings.Join(headers, "\t"))

	// Rows
	for _, b := range beads {
		vals := make([]string, len(columns))
		for i, col := range columns {
			vals[i] = beadField(b, col)
		}
		fmt.Fprintln(tw, strings.Join(vals, "\t"))
	}
	tw.Flush()
	fmt.Printf("\n%d beads (%d total)\n", len(beads), total)
}

// beadField returns the string value of a bead field by column name.
func beadField(b *beadsv1.Bead, col string) string {
	switch strings.ToLower(col) {
	case "id":
		return b.GetId()
	case "title":
		title := b.GetTitle()
		if len(title) > 50 {
			title = title[:47] + "..."
		}
		return title
	case "status":
		return b.GetStatus()
	case "type":
		return b.GetType()
	case "kind":
		return b.GetKind()
	case "priority":
		return fmt.Sprintf("%d", b.GetPriority())
	case "assignee":
		return b.GetAssignee()
	case "owner":
		return b.GetOwner()
	case "created_by":
		return b.GetCreatedBy()
	case "labels":
		return strings.Join(b.GetLabels(), ",")
	default:
		return ""
	}
}

// printViewDeps prints dependency sub-sections for each bead in the list.
func printViewDeps(beads []*beadsv1.Bead, dc *depConfig) {
	fmt.Println()
	for _, b := range beads {
		deps, err := fetchAndResolveDeps(context.Background(), client, b.GetId(), dc.Types)
		if err != nil || len(deps) == 0 {
			continue
		}
		fmt.Printf("  %s dependencies:\n", b.GetId())
		printDepSubSection(deps, dc.Fields)
		fmt.Println()
	}
}

func init() {
	viewCmd.Flags().Int32("limit", 0, "override the view's limit")
}
