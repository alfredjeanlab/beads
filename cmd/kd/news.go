package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/groblegark/kbeads/internal/client"
	"github.com/groblegark/kbeads/internal/model"
	"github.com/spf13/cobra"
)

var newsCmd = &cobra.Command{
	Use:        "news",
	Short:      "Show in-progress work by others",
	Deprecated: "use 'gb news' instead (ported to gasboat)",
	Long: `Show what other agents are actively working on. Use this before starting
work to avoid conflicts and get situational awareness.

Shows:
- In-progress beads by other agents (potential conflicts)
- Recently closed beads (context on recent progress)

Issues by the current actor are excluded unless --all is specified.

Examples:
  kd news              # Show in-progress work by others
  kd news --all        # Include your own activity
  kd news --window 4h  # Look back 4 hours for recent closures`,
	RunE: runNews,
}

func init() {
	newsCmd.Flags().Bool("all", false, "include your own activity")
	newsCmd.Flags().String("window", "2h", "lookback window for recently closed")
	newsCmd.Flags().IntP("limit", "n", 50, "maximum beads per section")
}

func runNews(cmd *cobra.Command, args []string) error {
	showAll, _ := cmd.Flags().GetBool("all")
	windowStr, _ := cmd.Flags().GetString("window")
	limit, _ := cmd.Flags().GetInt("limit")

	window, err := time.ParseDuration(windowStr)
	if err != nil {
		return fmt.Errorf("invalid --window duration %q (use e.g. 2h, 30m): %w", windowStr, err)
	}

	ctx := context.Background()
	currentActor := actor

	// Fetch in-progress beads.
	inProgress, err := beadsClient.ListBeads(ctx, &client.ListBeadsRequest{
		Status: []string{"in_progress"},
		Limit:  limit,
	})
	if err != nil {
		return fmt.Errorf("fetching in-progress: %w", err)
	}

	// Fetch recently closed beads (using updated_at as proxy — kbeads doesn't have closed_after filter).
	recentlyClosed, err := beadsClient.ListBeads(ctx, &client.ListBeadsRequest{
		Status: []string{"closed"},
		Sort:   "-updated_at",
		Limit:  limit,
	})
	if err != nil {
		return fmt.Errorf("fetching recently closed: %w", err)
	}

	// Filter out current actor's work unless --all.
	ipBeads := inProgress.Beads
	closedBeads := filterRecentlyClosed(recentlyClosed.Beads, window)
	if !showAll && currentActor != "" && currentActor != "unknown" {
		ipBeads = filterOutAssignee(ipBeads, currentActor)
		closedBeads = filterOutAssignee(closedBeads, currentActor)
	}

	// Filter out noise types (decisions, gates, config, advice).
	ipBeads = filterOutNoiseTypes(ipBeads)
	closedBeads = filterOutNoiseTypes(closedBeads)

	if len(ipBeads) == 0 && len(closedBeads) == 0 {
		fmt.Fprintf(os.Stdout, "\nNo recent activity (last %s)\n\n", windowStr)
		return nil
	}

	if len(ipBeads) > 0 {
		fmt.Fprintf(os.Stdout, "\nIn-progress by others (%d):\n\n", len(ipBeads))
		for _, b := range ipBeads {
			printNewsBead(b)
		}
	}

	if len(closedBeads) > 0 {
		if len(ipBeads) > 0 {
			fmt.Fprintln(os.Stdout)
		}
		fmt.Fprintf(os.Stdout, "Closed in last %s (%d):\n\n", windowStr, len(closedBeads))
		for _, b := range closedBeads {
			printNewsBead(b)
		}
	}

	fmt.Fprintln(os.Stdout)
	return nil
}

func printNewsBead(b *model.Bead) {
	assignee := "unassigned"
	if b.Assignee != "" {
		assignee = "@" + b.Assignee
	}

	age := time.Since(b.UpdatedAt)
	ageStr := formatDuration(age)

	typeStr := ""
	if string(b.Type) != "" {
		typeStr = fmt.Sprintf("[%s] ", b.Type)
	}

	fmt.Fprintf(os.Stdout, "  %s %s%s  %s  %s ago\n",
		b.ID, typeStr, b.Title, assignee, ageStr)
}

func filterOutAssignee(beads []*model.Bead, actor string) []*model.Bead {
	actorLower := strings.ToLower(actor)
	var filtered []*model.Bead
	for _, b := range beads {
		assigneeLower := strings.ToLower(b.Assignee)
		if assigneeLower == actorLower {
			continue
		}
		if strings.HasSuffix(assigneeLower, "/"+actorLower) {
			continue
		}
		filtered = append(filtered, b)
	}
	return filtered
}

func filterRecentlyClosed(beads []*model.Bead, window time.Duration) []*model.Bead {
	cutoff := time.Now().Add(-window)
	var filtered []*model.Bead
	for _, b := range beads {
		if b.UpdatedAt.After(cutoff) {
			filtered = append(filtered, b)
		}
	}
	return filtered
}

func filterOutNoiseTypes(beads []*model.Bead) []*model.Bead {
	noise := map[model.BeadType]bool{
		"decision": true,
		"gate":     true,
		"config":   true,
		"advice":   true,
		"message":  true,
		"formula":  true,
		"molecule": true,
		"runbook":  true,
	}
	var filtered []*model.Bead
	for _, b := range beads {
		if noise[b.Type] {
			continue
		}
		filtered = append(filtered, b)
	}
	return filtered
}
