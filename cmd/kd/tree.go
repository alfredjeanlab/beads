package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	beadsv1 "github.com/groblegark/kbeads/gen/beads/v1"
	"github.com/spf13/cobra"
)

var treeCmd = &cobra.Command{
	Use:     "tree <bead-id>",
	Short:   "Show dependency tree (or flat list) for a bead",
	GroupID: "views",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		beadID := args[0]
		depth, _ := cmd.Flags().GetInt("depth")
		flat, _ := cmd.Flags().GetBool("flat")
		filterType, _ := cmd.Flags().GetString("type")

		if flat {
			return runTreeFlat(beadID, filterType)
		}
		return runTreeGraph(beadID, depth, filterType)
	},
}

func runTreeGraph(beadID string, depth int, filterType string) error {
	resp, err := client.GetBead(context.Background(), &beadsv1.GetBeadRequest{
		Id: beadID,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	bead := resp.GetBead()
	fmt.Printf("%s [%s] %s\n", bead.GetId(), bead.GetStatus(), bead.GetTitle())

	deps := bead.GetDependencies()
	if filterType != "" {
		deps = filterDepsByType(deps, []string{filterType})
	}
	printDepTree(deps, "", depth-1)
	return nil
}

func runTreeFlat(beadID string, filterType string) error {
	var types []string
	if filterType != "" {
		types = []string{filterType}
	}

	deps, err := fetchAndResolveDeps(context.Background(), client, beadID, types)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(deps) == 0 {
		fmt.Println("No dependencies found.")
		return nil
	}

	if jsonOutput {
		type jsonChild struct {
			DependsOnID string `json:"depends_on_id"`
			Type        string `json:"type"`
			Status      string `json:"status,omitempty"`
			Title       string `json:"title,omitempty"`
		}
		var out []jsonChild
		for _, rd := range deps {
			jc := jsonChild{
				DependsOnID: rd.Dep.GetDependsOnId(),
				Type:        rd.Dep.GetType(),
			}
			if rd.Bead != nil {
				jc.Status = rd.Bead.GetStatus()
				jc.Title = rd.Bead.GetTitle()
			}
			out = append(out, jc)
		}
		data, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error marshaling JSON: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(data))
	} else {
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "DEPENDS_ON\tTYPE\tSTATUS\tTITLE")
		for _, rd := range deps {
			status := "(unknown)"
			title := "(error fetching)"
			if rd.Bead != nil {
				status = rd.Bead.GetStatus()
				title = rd.Bead.GetTitle()
				if len(title) > 50 {
					title = title[:47] + "..."
				}
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
				rd.Dep.GetDependsOnId(),
				rd.Dep.GetType(),
				status,
				title,
			)
		}
		w.Flush()
	}
	return nil
}

func printDepTree(deps []*beadsv1.Dependency, prefix string, remainingDepth int) {
	for i, dep := range deps {
		isLast := i == len(deps)-1

		connector := "├── "
		childPrefix := prefix + "│   "
		if isLast {
			connector = "└── "
			childPrefix = prefix + "    "
		}

		depResp, err := client.GetBead(context.Background(), &beadsv1.GetBeadRequest{
			Id: dep.GetDependsOnId(),
		})
		if err != nil {
			fmt.Printf("%s%s%s: %s (error fetching)\n", prefix, connector, dep.GetType(), dep.GetDependsOnId())
			continue
		}

		depBead := depResp.GetBead()
		fmt.Printf("%s%s%s: %s [%s] %s\n",
			prefix, connector,
			dep.GetType(),
			depBead.GetId(),
			depBead.GetStatus(),
			depBead.GetTitle(),
		)

		if remainingDepth > 0 {
			childDeps := depBead.GetDependencies()
			if len(childDeps) > 0 {
				printDepTree(childDeps, childPrefix, remainingDepth-1)
			}
		}
	}
}

func init() {
	treeCmd.Flags().Int("depth", 3, "maximum depth to traverse")
	treeCmd.Flags().Bool("flat", false, "flat table instead of ASCII tree")
	treeCmd.Flags().StringP("type", "t", "", "filter by dependency type (e.g. parent-child, blocks)")
}
