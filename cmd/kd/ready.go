package main

import (
	"context"
	"fmt"

	"github.com/groblegark/kbeads/internal/client"
	"github.com/spf13/cobra"
)

var readyCmd = &cobra.Command{
	Use:   "ready",
	Short: "Show beads ready to work on (open, not blocked)",
	RunE: func(cmd *cobra.Command, args []string) error {
		beadType, _ := cmd.Flags().GetStringSlice("type")
		assignee, _ := cmd.Flags().GetString("assignee")
		limit, _ := cmd.Flags().GetInt("limit")

		req := &client.ListBeadsRequest{
			Status: []string{"open"},
			Type:   beadType,
			Limit:  limit,
			Sort:   "priority",
		}
		if assignee != "" {
			req.Assignee = assignee
		}

		resp, err := beadsClient.ListBeads(context.Background(), req)
		if err != nil {
			return fmt.Errorf("listing ready beads: %w", err)
		}

		if jsonOutput {
			printBeadListJSON(resp.Beads)
		} else {
			printBeadListTable(resp.Beads, resp.Total)
		}
		return nil
	},
}

func init() {
	readyCmd.Flags().StringSliceP("type", "t", nil, "filter by type (repeatable)")
	readyCmd.Flags().String("assignee", "", "filter by assignee")
	readyCmd.Flags().Int("limit", 20, "maximum number of results")
}
