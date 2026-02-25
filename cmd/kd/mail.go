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

// resolveMailActor returns the actor name for mail operations.
// Priority: --actor flag > KD_ACTOR env > git config user.name.
func resolveMailActor() string {
	if v := os.Getenv("KD_ACTOR"); v != "" {
		return v
	}
	return actor // global from main.go (--actor flag / git config)
}

// ── kd mail (parent) ───────────────────────────────────────────────────

var mailCmd = &cobra.Command{
	Use:        "mail",
	Short:      "Agent-to-agent mail via beads",
	Long:       `Send and receive mail between agents. Mail items are beads with type="mail".`,
	Deprecated: "use 'gb mail' instead (ported to gasboat)",
}

// ── kd mail send ────────────────────────────────────────────────────────

var mailSendCmd = &cobra.Command{
	Use:   "send <recipient>",
	Short: "Send mail to another agent",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		recipient := args[0]
		subject, _ := cmd.Flags().GetString("subject")
		body, _ := cmd.Flags().GetString("body")

		if subject == "" {
			return fmt.Errorf("--subject (-s) is required")
		}

		sender := resolveMailActor()

		req := &client.CreateBeadRequest{
			Title:       subject,
			Type:        "mail",
			Kind:        "data",
			Description: body,
			Assignee:    recipient,
			Labels:      []string{"from:" + sender},
			CreatedBy:   sender,
			Priority:    2,
		}

		bead, err := beadsClient.CreateBead(context.Background(), req)
		if err != nil {
			return fmt.Errorf("sending mail: %w", err)
		}

		if jsonOutput {
			printBeadJSON(bead)
		} else {
			fmt.Printf("Sent: %s → %s (id: %s)\n", sender, recipient, bead.ID)
		}
		return nil
	},
}

// ── kd mail inbox / kd inbox ────────────────────────────────────────────

func runInbox(cmd *cobra.Command, args []string) error {
	me := resolveMailActor()
	limit, _ := cmd.Flags().GetInt("limit")

	req := &client.ListBeadsRequest{
		Type:     []string{"mail"},
		Status:   []string{"open"},
		Assignee: me,
		Sort:     "-created_at",
		Limit:    limit,
	}

	resp, err := beadsClient.ListBeads(context.Background(), req)
	if err != nil {
		return fmt.Errorf("listing inbox: %w", err)
	}

	if jsonOutput {
		printBeadListJSON(resp.Beads)
	} else if len(resp.Beads) == 0 {
		fmt.Println("No mail")
	} else {
		for _, b := range resp.Beads {
			printMailLine(b)
		}
		fmt.Printf("\n%d message(s)\n", len(resp.Beads))
	}
	return nil
}

var mailInboxCmd = &cobra.Command{
	Use:   "inbox",
	Short: "Show your mail inbox",
	RunE:  runInbox,
}

var inboxCmd = &cobra.Command{
	Use:        "inbox",
	Short:      "Show your mail inbox (alias for 'mail inbox')",
	Deprecated: "use 'gb mail inbox' instead (ported to gasboat)",
	RunE:       runInbox,
}

// ── kd mail read ────────────────────────────────────────────────────────

var mailReadCmd = &cobra.Command{
	Use:   "read <id>",
	Short: "Read a mail message",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		bead, err := beadsClient.GetBead(context.Background(), args[0])
		if err != nil {
			return fmt.Errorf("reading mail %s: %w", args[0], err)
		}

		if jsonOutput {
			printBeadJSON(bead)
		} else {
			printMailDetail(bead)
		}
		return nil
	},
}

// ── kd mail list ────────────────────────────────────────────────────────

var mailListCmd = &cobra.Command{
	Use:   "list",
	Short: "List mail with optional filters",
	RunE: func(cmd *cobra.Command, args []string) error {
		status, _ := cmd.Flags().GetStringSlice("status")
		limit, _ := cmd.Flags().GetInt("limit")

		if len(status) == 0 {
			status = []string{"open"}
		}

		me := resolveMailActor()
		req := &client.ListBeadsRequest{
			Type:     []string{"mail"},
			Status:   status,
			Assignee: me,
			Sort:     "-created_at",
			Limit:    limit,
		}

		resp, err := beadsClient.ListBeads(context.Background(), req)
		if err != nil {
			return fmt.Errorf("listing mail: %w", err)
		}

		if jsonOutput {
			printBeadListJSON(resp.Beads)
		} else if len(resp.Beads) == 0 {
			fmt.Println("No mail")
		} else {
			for _, b := range resp.Beads {
				printMailLine(b)
			}
			fmt.Printf("\n%d message(s)\n", len(resp.Beads))
		}
		return nil
	},
}

// ── helpers ─────────────────────────────────────────────────────────────

// senderFromLabels extracts the sender name from a "from:<name>" label.
func senderFromLabels(labels []string) string {
	for _, l := range labels {
		if strings.HasPrefix(l, "from:") {
			return strings.TrimPrefix(l, "from:")
		}
	}
	return ""
}

func printMailLine(b *model.Bead) {
	sender := senderFromLabels(b.Labels)
	if sender == "" {
		sender = b.CreatedBy
	}
	age := formatAge(b.CreatedAt)
	fmt.Printf("  %s  %-12s  %s  (%s)\n", b.ID, sender, b.Title, age)
}

func printMailDetail(b *model.Bead) {
	sender := senderFromLabels(b.Labels)
	if sender == "" {
		sender = b.CreatedBy
	}
	fmt.Printf("From:    %s\n", sender)
	fmt.Printf("To:      %s\n", b.Assignee)
	fmt.Printf("Subject: %s\n", b.Title)
	if !b.CreatedAt.IsZero() {
		fmt.Printf("Date:    %s\n", b.CreatedAt.Format("2006-01-02 15:04:05"))
	}
	fmt.Printf("ID:      %s\n", b.ID)
	if b.Description != "" {
		fmt.Printf("\n%s\n", b.Description)
	}
}

func formatAge(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// ── init ────────────────────────────────────────────────────────────────

func init() {
	// mail send flags
	mailSendCmd.Flags().StringP("subject", "s", "", "mail subject (required)")
	mailSendCmd.Flags().StringP("body", "b", "", "mail body")

	// mail inbox flags
	mailInboxCmd.Flags().Int("limit", 20, "maximum messages to show")

	// mail list flags
	mailListCmd.Flags().StringSlice("status", nil, "filter by status (default: open)")
	mailListCmd.Flags().Int("limit", 20, "maximum messages to show")

	// inbox (top-level alias) flags
	inboxCmd.Flags().Int("limit", 20, "maximum messages to show")

	// wire subcommands
	mailCmd.AddCommand(mailSendCmd)
	mailCmd.AddCommand(mailInboxCmd)
	mailCmd.AddCommand(mailReadCmd)
	mailCmd.AddCommand(mailListCmd)
}
