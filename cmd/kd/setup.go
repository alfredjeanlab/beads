package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Setup commands for agent environment",
}

var setupClaudeCmd = &cobra.Command{
	Use:   "claude",
	Short: "Materialize Claude Code hooks from config beads",
	Long: `Fetches claude-hooks config beads from the daemon, merges them by
specificity (global → role → agent), and writes .claude/settings.json
in the workspace directory.

Config bead keys (checked in order, later overrides earlier):
  claude-hooks:global   — base hooks for all agents
  claude-hooks:<role>   — role-specific overrides (e.g. claude-hooks:crew)

Merge rules:
  - Top-level keys: more specific layer wins (replace)
  - hooks.<hookType> arrays: append (more specific adds to existing)

Environment variables used:
  BOAT_ROLE / KD_ROLE  — agent role for role-specific config lookup
  KD_WORKSPACE         — workspace directory (default: current directory)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		workspace, _ := cmd.Flags().GetString("workspace")
		if workspace == "" {
			workspace, _ = os.Getwd()
		}

		role, _ := cmd.Flags().GetString("role")
		if role == "" {
			role = os.Getenv("BOAT_ROLE")
		}
		if role == "" {
			role = os.Getenv("KD_ROLE")
		}

		return runSetupClaude(cmd.Context(), workspace, role)
	},
}

func init() {
	setupClaudeCmd.Flags().String("workspace", os.Getenv("KD_WORKSPACE"), "workspace directory")
	setupClaudeCmd.Flags().String("role", "", "agent role (default: $BOAT_ROLE or $KD_ROLE)")
	setupCmd.AddCommand(setupClaudeCmd)
}

func runSetupClaude(ctx context.Context, workspace, role string) error {
	// Fetch config layers in specificity order (least → most specific).
	var layers []json.RawMessage

	// Layer 1: global hooks (applies to all agents).
	if cfg, err := beadsClient.GetConfig(ctx, "claude-hooks:global"); err == nil && cfg != nil {
		layers = append(layers, cfg.Value)
		fmt.Fprintf(os.Stderr, "[setup] loaded claude-hooks:global\n")
	}

	// Layer 2: role-specific hooks (e.g. claude-hooks:crew).
	if role != "" {
		if cfg, err := beadsClient.GetConfig(ctx, "claude-hooks:"+role); err == nil && cfg != nil {
			layers = append(layers, cfg.Value)
			fmt.Fprintf(os.Stderr, "[setup] loaded claude-hooks:%s\n", role)
		}
	}

	if len(layers) == 0 {
		return fmt.Errorf("no claude-hooks config beads found")
	}

	// Merge layers: later overrides earlier, hooks arrays append.
	merged := mergeHookLayers(layers)

	// Pretty-print and write to .claude/settings.json.
	outDir := filepath.Join(workspace, ".claude")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return fmt.Errorf("creating .claude dir: %w", err)
	}

	outPath := filepath.Join(outDir, "settings.json")
	data, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling settings: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(outPath, data, 0600); err != nil {
		return fmt.Errorf("writing settings: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[setup] wrote %s\n", outPath)
	return nil
}

// mergeHookLayers merges multiple settings JSON layers.
// Top-level keys: later wins (replace). hooks.<type> arrays: append.
func mergeHookLayers(layers []json.RawMessage) map[string]any {
	result := make(map[string]any)

	for _, raw := range layers {
		var layer map[string]any
		if err := json.Unmarshal(raw, &layer); err != nil {
			continue
		}
		mergeSettingsLayer(result, layer)
	}

	return result
}

// mergeSettingsLayer merges src into dst.
// Top-level keys: src wins. "hooks" map: arrays are appended.
func mergeSettingsLayer(dst, src map[string]any) {
	for k, v := range src {
		if k == "hooks" {
			// Special merge: hook arrays append rather than replace.
			srcHooks, ok := v.(map[string]any)
			if !ok {
				dst[k] = v
				continue
			}
			dstHooks, ok := dst[k].(map[string]any)
			if !ok {
				dstHooks = make(map[string]any)
				dst[k] = dstHooks
			}
			mergeHooksField(dstHooks, srcHooks)
		} else {
			// All other top-level keys: replace.
			dst[k] = v
		}
	}
}

// mergeHooksField appends hook entries from src into dst for each hook type.
func mergeHooksField(dst, src map[string]any) {
	for hookType, srcVal := range src {
		srcArr, ok := srcVal.([]any)
		if !ok {
			dst[hookType] = srcVal
			continue
		}
		dstArr, ok := dst[hookType].([]any)
		if !ok {
			dst[hookType] = srcArr
			continue
		}
		// Append: more specific hooks added after less specific.
		dst[hookType] = append(dstArr, srcArr...)
	}
}
