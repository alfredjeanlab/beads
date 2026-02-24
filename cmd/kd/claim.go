package main

import (
	"context"
	"fmt"

	"github.com/groblegark/kbeads/internal/client"
	"github.com/spf13/cobra"
)

var claimCmd = &cobra.Command{
	Use:     "claim <id>",
	Short:   "Claim a bead by assigning it to yourself",
	GroupID: "workflow",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		inProgress := "in_progress"

		bead, err := beadsClient.UpdateBead(context.Background(), id, &client.UpdateBeadRequest{
			Assignee: &actor,
			Status:   &inProgress,
		})
		if err != nil {
			return fmt.Errorf("claiming bead %s: %w", id, err)
		}

		if jsonOutput {
			printBeadJSON(bead)
		} else {
			printBeadTable(bead)
		}
		return nil
	},
}
