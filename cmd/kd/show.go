package main

import (
	"context"
	"fmt"
	"os"

	beadsv1 "github.com/groblegark/kbeads/gen/beads/v1"
	"github.com/spf13/cobra"
)

var showCmd = &cobra.Command{
	Use:     "show <id>",
	Short:   "Show details of a bead",
	GroupID: "beads",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]

		resp, err := client.GetBead(context.Background(), &beadsv1.GetBeadRequest{
			Id: id,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		bead := resp.GetBead()
		if jsonOutput {
			printBeadJSON(bead)
		} else {
			printBeadTable(bead)
			printComments(bead.GetComments())
		}
		return nil
	},
}
