package main

// kd hook — Claude Code agent hook subcommands.
//
// Replaces the shell scripts that implement Claude Code hook behaviour:
//   - check-mail.sh + drain-queue.sh  →  kd hook check-mail
//   - prime.sh                         →  kd hook prime
//   - stop-gate.sh                     →  kd hook stop-gate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/groblegark/kbeads/internal/client"
	"github.com/spf13/cobra"
)

var hookCmd = &cobra.Command{
	Use:        "hook",
	Short:      "Agent hook subcommands (replaces shell hook scripts)",
	Deprecated: "use 'gb hook' instead (ported to gasboat)",
}

// ── kd hook check-mail ────────────────────────────────────────────────────
//
// Replaces check-mail.sh + drain-queue.sh.
// Queries for open mail/message beads assigned to this agent and outputs
// them as a <system-reminder> tag so Claude Code picks them up inline.
// No intermediate file is required.

var hookCheckMailCmd = &cobra.Command{
	Use:   "check-mail",
	Short: "Inject unread mail as a system-reminder (replaces check-mail.sh + drain-queue.sh)",
	RunE: func(cmd *cobra.Command, args []string) error {
		me := resolveMailActor()
		if me == "" || me == "unknown" {
			return nil
		}

		resp, err := beadsClient.ListBeads(context.Background(), &client.ListBeadsRequest{
			Type:     []string{"mail", "message"},
			Status:   []string{"open"},
			Assignee: me,
			Sort:     "-created_at",
			Limit:    20,
		})
		if err != nil || len(resp.Beads) == 0 {
			return nil
		}

		var sb strings.Builder
		sb.WriteString("## Inbox\n\n")
		for _, b := range resp.Beads {
			sender := senderFromLabels(b.Labels)
			if sender == "" {
				sender = b.CreatedBy
			}
			sb.WriteString(fmt.Sprintf("- %s | %s | %s\n", b.ID, b.Title, sender))
		}
		fmt.Printf("<system-reminder>\n%s</system-reminder>\n", sb.String())
		return nil
	},
}

// ── kd hook prime ─────────────────────────────────────────────────────────
//
// Replaces prime.sh (used by SessionStart and PreCompact hooks).
// Outputs the full kd prime context wrapped in a system-reminder tag.

var hookPrimeCmd = &cobra.Command{
	Use:   "prime",
	Short: "Output workflow context as system-reminder (replaces prime.sh)",
	RunE: func(cmd *cobra.Command, args []string) error {
		agentID := resolvePrimeAgentFromEnv(actor)
		outputPrimeForHook(os.Stdout, agentID)
		return nil
	},
}

// ── kd hook stop-gate ─────────────────────────────────────────────────────
//
// Replaces stop-gate.sh (used by the Stop hook).
// Reads the Claude Code hook event from stdin, emits Stop to the server,
// and if blocked writes the block JSON to stderr and exits 2.
//
// The noisy-poll problem described in kd-iNrd817B6q is resolved by running
// kd yield in the foreground (blocking the Claude turn). When yield is
// running, the Stop hook does not fire. When yield returns (decision
// resolved), the Stop hook fires once — and since no pending decision
// exists, the gate passes immediately.

var hookStopGateCmd = &cobra.Command{
	Use:   "stop-gate",
	Short: "Emit Stop hook event and handle gate block (replaces stop-gate.sh)",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Read Claude Code hook event JSON from stdin.
		var stdinEvent map[string]any
		if err := json.NewDecoder(os.Stdin).Decode(&stdinEvent); err != nil {
			stdinEvent = map[string]any{}
		}

		claudeSessionID, _ := stdinEvent["session_id"].(string)
		cwd, _ := stdinEvent["cwd"].(string)
		if cwd == "" {
			cwd, _ = os.Getwd()
		}

		agentBeadID := os.Getenv("KD_AGENT_ID")
		if agentBeadID == "" {
			agentBeadID = resolveAgentByActor(context.Background(), actor, claudeSessionID)
		}

		resp, err := beadsClient.EmitHook(context.Background(), &client.EmitHookRequest{
			AgentBeadID:     agentBeadID,
			HookType:        "Stop",
			ClaudeSessionID: claudeSessionID,
			CWD:             cwd,
			Actor:           actor,
		})
		if err != nil {
			// Fail open — don't block the agent on server errors.
			fmt.Fprintf(os.Stderr, "kd hook stop-gate: server error (failing open): %v\n", err)
			return nil
		}

		for _, w := range resp.Warnings {
			fmt.Printf("<system-reminder>%s</system-reminder>\n", w)
		}
		if resp.Inject != "" {
			fmt.Print(resp.Inject)
		}

		if resp.Block {
			blockJSON, _ := json.Marshal(map[string]string{
				"decision": "block",
				"reason":   resp.Reason,
			})
			fmt.Fprintf(os.Stderr, "%s\n", blockJSON)
			os.Exit(2)
		}

		return nil
	},
}

func init() {
	hookCmd.AddCommand(hookCheckMailCmd)
	hookCmd.AddCommand(hookPrimeCmd)
	hookCmd.AddCommand(hookStopGateCmd)
}
