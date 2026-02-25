package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/groblegark/kbeads/internal/client"
	"github.com/groblegark/kbeads/internal/model"
	"github.com/spf13/cobra"
)

var yieldCmd = &cobra.Command{
	Use:        "yield",
	Short:      "Block until a pending decision is resolved or mail arrives",
	Deprecated: "use 'gb yield' instead (ported to gasboat)",
	Long: `Blocks the agent until one of the following events occurs:
  - A pending decision bead (type=decision, status=open) is closed/resolved
  - A mail/message bead targeting this agent is created
  - The timeout expires (default 24h)

Uses HTTP SSE (GET /v1/events/stream) for real-time notification,
with 2-second polling as fallback if SSE is unavailable.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		timeout, _ := cmd.Flags().GetDuration("timeout")

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()

		if timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}

		// Find the most recent pending decision by this actor.
		resp, err := beadsClient.ListBeads(ctx, &client.ListBeadsRequest{
			Status: []string{"open"},
			Type:   []string{"decision"},
			Sort:   "-created_at",
			Limit:  1,
		})
		if err != nil {
			return fmt.Errorf("listing decisions: %w", err)
		}

		if len(resp.Beads) == 0 {
			fmt.Println("No pending decisions found, waiting for any event...")
		} else {
			d := resp.Beads[0]
			prompt := decisionField(d, "prompt")
			if prompt == "" {
				prompt = d.Title
			}
			fmt.Fprintf(os.Stderr, "Yielding on decision %s: %s\n", d.ID, prompt)
		}

		return yieldSSE(ctx, resp.Beads)
	},
}

// yieldSSE connects to the HTTP SSE endpoint and blocks until a relevant event
// arrives or the context expires. Falls back to yieldPoll on connection error.
func yieldSSE(ctx context.Context, pending []*model.Bead) error {
	pendingIDs := make(map[string]bool, len(pending))
	for _, b := range pending {
		pendingIDs[b.ID] = true
	}

	sseURL := httpURL + "/v1/events/stream?topics=beads.%3E"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sseURL, nil)
	if err != nil {
		return yieldPoll(ctx, pending)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return yieldPoll(ctx, pending)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return yieldPoll(ctx, pending)
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var dataLine string
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "data:"):
			dataLine = strings.TrimPrefix(line, "data:")
		case line == "" && dataLine != "":
			if done, doneErr := processYieldEvent(dataLine, pendingIDs); done {
				return doneErr
			}
			dataLine = ""
		}
	}

	// Scanner ended — check why.
	if ctx.Err() == context.DeadlineExceeded {
		fmt.Println("Yield timed out")
		return nil
	}
	if ctx.Err() != nil {
		return nil
	}

	// Server closed the connection; fall back to poll.
	return yieldPoll(ctx, pending)
}

// processYieldEvent parses a single SSE data line. Returns (true, err) if the
// yield condition was satisfied, (false, nil) to continue waiting.
func processYieldEvent(data string, pendingIDs map[string]bool) (bool, error) {
	var evt map[string]any
	if err := json.Unmarshal([]byte(data), &evt); err != nil {
		return false, nil
	}
	beadID, _ := evt["bead_id"].(string)
	if pendingIDs[beadID] {
		return true, printYieldResult(beadID)
	}
	beadType, _ := evt["type"].(string)
	if beadType == "message" || beadType == "mail" {
		fmt.Printf("Mail received: %s\n", beadID)
		return true, nil
	}
	return false, nil
}

func yieldPoll(ctx context.Context, pending []*model.Bead) error {
	pendingIDs := make(map[string]bool, len(pending))
	for _, b := range pending {
		pendingIDs[b.ID] = true
	}

	for {
		select {
		case <-ctx.Done():
			if ctx.Err() == context.DeadlineExceeded {
				fmt.Println("Yield timed out")
			}
			return nil
		case <-time.After(2 * time.Second):
		}

		// Check if any pending decision was resolved.
		for id := range pendingIDs {
			bead, err := beadsClient.GetBead(ctx, id)
			if err != nil {
				continue
			}
			if bead.Status == model.StatusClosed {
				return printYieldResult(id)
			}
			chosen := decisionField(bead, "chosen")
			responseText := decisionField(bead, "response_text")
			if chosen != "" || responseText != "" {
				return printYieldResult(id)
			}
		}

		// If no decisions, check for any new mail.
		if len(pendingIDs) == 0 {
			msgs, err := beadsClient.ListBeads(ctx, &client.ListBeadsRequest{
				Status: []string{"open"},
				Type:   []string{"message", "mail"},
				Limit:  1,
				Sort:   "-created_at",
			})
			if err == nil && len(msgs.Beads) > 0 {
				fmt.Printf("Mail received: %s\n", msgs.Beads[0].ID)
				return nil
			}
		}
	}
}

func printYieldResult(id string) error {
	bead, err := beadsClient.GetBead(context.Background(), id)
	if err != nil {
		return err
	}
	chosen := decisionField(bead, "chosen")
	responseText := decisionField(bead, "response_text")
	if chosen != "" {
		fmt.Printf("Decision %s resolved: %s\n", id, chosen)
	} else if responseText != "" {
		fmt.Printf("Decision %s resolved: %s\n", id, responseText)
	} else {
		fmt.Printf("Decision %s closed\n", id)
	}
	return nil
}

func init() {
	yieldCmd.Flags().Duration("timeout", 24*time.Hour, "maximum time to wait")
}
