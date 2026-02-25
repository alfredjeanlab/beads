package main

// kd bus emit --hook=Stop
// Reads Claude Code hook event JSON from stdin, resolves agent identity,
// calls POST /v1/hooks/emit, and exits with appropriate code.
//
// Exit codes:
//
//	0 — allow
//	2 — block (stderr: {"decision":"block","reason":"..."})

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/groblegark/kbeads/internal/client"
	"github.com/spf13/cobra"
)

// busCmd is the parent command for event bus operations.
// Deprecated: use 'gb bus' instead (ported to gasboat).
var busCmd = &cobra.Command{
	Use:        "bus",
	Short:      "Event bus operations",
	Deprecated: "use 'gb bus' instead (ported to gasboat)",
}

// busEmitCmd emits a hook event to the server.
var busEmitCmd = &cobra.Command{
	Use:   "emit",
	Short: "Emit a hook event",
	Long: `Reads a Claude Code hook event JSON from stdin, resolves agent identity,
and calls POST /v1/hooks/emit on the kbeads server.

Exit codes:
  0 — allow (or no gates to check)
  2 — block

Warnings are written to stdout as <system-reminder> tags for Claude Code.
Block reason is written to stderr as {"decision":"block","reason":"..."}.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		hookType, _ := cmd.Flags().GetString("hook")
		if hookType == "" {
			return fmt.Errorf("--hook is required (e.g. Stop, PreToolUse, UserPromptSubmit, PreCompact)")
		}

		cwdFlag, _ := cmd.Flags().GetString("cwd")

		// Read JSON from stdin (Claude Code hook event format).
		var stdinEvent map[string]any
		decoder := json.NewDecoder(os.Stdin)
		if err := decoder.Decode(&stdinEvent); err != nil {
			// stdin may be empty or non-JSON (e.g. called manually).
			// Treat as empty event — proceed with env-based resolution.
			stdinEvent = map[string]any{}
		}

		// Resolve CWD: flag > stdin cwd field > os.Getwd().
		cwd := cwdFlag
		if cwd == "" {
			if v, ok := stdinEvent["cwd"].(string); ok && v != "" {
				cwd = v
			}
		}
		if cwd == "" {
			if wd, err := os.Getwd(); err == nil {
				cwd = wd
			}
		}

		// Extract claude_session_id from stdin JSON.
		claudeSessionID, _ := stdinEvent["session_id"].(string)

		// Extract tool_name for presence tracking (from PreToolUse/PostToolUse events).
		toolName, _ := stdinEvent["tool_name"].(string)

		// Resolve agent_bead_id in priority order:
		//   1. KD_AGENT_ID env var
		//   2. Query by KD_ACTOR name (assignee search)
		//   3. Empty string (no gates to check)
		agentBeadID := os.Getenv("KD_AGENT_ID")
		if agentBeadID == "" {
			agentBeadID = resolveAgentByActor(cmd.Context(), actor, claudeSessionID)
		}

		req := &client.EmitHookRequest{
			AgentBeadID:     agentBeadID,
			HookType:        hookType,
			ClaudeSessionID: claudeSessionID,
			CWD:             cwd,
			Actor:           actor,
			ToolName:        toolName,
		}

		resp, err := beadsClient.EmitHook(cmd.Context(), req)
		if err != nil {
			// On server error, allow (fail open) — don't block the agent.
			fmt.Fprintf(os.Stderr, "kd bus emit: server error (failing open): %v\n", err)
			return nil
		}

		// On SessionStart, inject the full kd prime context.
		// We do this client-side because the prime output functions live in this binary
		// and the server is remote (HTTP API) — no subprocess needed.
		if hookType == "SessionStart" {
			agentID := resolvePrimeAgentFromEnv(actor)
			outputPrimeForHook(os.Stdout, agentID)
		}

		// Write warnings as system-reminder tags to stdout (Claude Code reads these).
		for _, w := range resp.Warnings {
			fmt.Printf("<system-reminder>%s</system-reminder>\n", w)
		}

		// Write inject content to stdout if present.
		if resp.Inject != "" {
			fmt.Print(resp.Inject)
		}

		// Block: write to stderr and exit 2.
		// Exit code 2 is required by the Claude Code hook protocol to signal "block".
		// Cobra cannot express non-1 exit codes via error returns.
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

// resolveAgentByActor looks up an open agent bead by the actor's assignee name.
// Returns empty string if not found or on error.
func resolveAgentByActor(ctx context.Context, actorName, _ string) string {
	if actorName == "" || actorName == "unknown" {
		return ""
	}

	resp, err := beadsClient.ListBeads(ctx, &client.ListBeadsRequest{
		Type:     []string{"agent"},
		Assignee: actorName,
		Status:   []string{"open", "in_progress"},
		Sort:     "-created_at",
		Limit:    1,
	})
	if err != nil {
		return ""
	}
	if len(resp.Beads) == 0 {
		return ""
	}
	return resp.Beads[0].ID
}

func init() {
	busCmd.AddCommand(busEmitCmd)

	busEmitCmd.Flags().String("hook", "", "hook type: Stop|PreToolUse|UserPromptSubmit|PreCompact (required)")
	busEmitCmd.Flags().String("cwd", "", "working directory (default: current dir)")

	// Mark --hook as required so cobra prints a helpful error if omitted.
	_ = busEmitCmd.MarkFlagRequired("hook")
}

// resolvePrimeAgentFromEnv resolves agent identity from env vars and the global actor.
// Used by bus emit when --for flag is not available.
func resolvePrimeAgentFromEnv(globalActor string) string {
	if v := os.Getenv("KD_ACTOR"); v != "" {
		return v
	}
	if v := os.Getenv("KD_AGENT_ID"); v != "" {
		return v
	}
	if globalActor != "" && globalActor != "unknown" {
		return globalActor
	}
	return ""
}

// outputPrimeForHook generates full kd prime output wrapped in a system-reminder tag.
// This reuses the same output functions as `kd prime` but wraps them for hook injection.
func outputPrimeForHook(w io.Writer, agentID string) {
	var buf strings.Builder

	// 1. Workflow context.
	outputWorkflowContext(&buf)

	// 2. Advice.
	if agentID != "" {
		outputAdvice(&buf, beadsClient, agentID)
	}

	// 3. Jacks.
	outputJackSection(&buf, beadsClient)

	// 4. Roster.
	outputRosterSection(&buf, beadsClient, agentID)

	// 5. Auto-assign.
	if agentID != "" {
		outputAutoAssign(&buf, beadsClient, agentID)
	}

	content := buf.String()
	if content != "" {
		fmt.Fprintf(w, "<system-reminder>\nSessionStart:compact hook success: %s</system-reminder>\n", content)
	}
}
