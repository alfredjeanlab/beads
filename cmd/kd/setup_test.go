package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultHookSettings(t *testing.T) {
	settings := defaultHookSettings()

	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		t.Fatal("settings missing hooks key")
	}

	expected := []string{"SessionStart", "PreCompact", "Stop", "PreToolUse", "PostToolUse"}
	for _, ht := range expected {
		arr, ok := hooks[ht].([]any)
		if !ok {
			t.Errorf("hooks missing %s", ht)
			continue
		}
		if len(arr) != 1 {
			t.Errorf("hooks[%s] has %d entries, want 1", ht, len(arr))
			continue
		}
		entry := arr[0].(map[string]any)
		innerHooks := entry["hooks"].([]any)
		cmd := innerHooks[0].(map[string]any)["command"].(string)
		wantCmd := "kd bus emit --hook=" + ht
		if cmd != wantCmd {
			t.Errorf("hooks[%s] command = %q, want %q", ht, cmd, wantCmd)
		}
	}
}

func TestRunSetupClaudeDefaults(t *testing.T) {
	dir := t.TempDir()
	if err := runSetupClaudeDefaults(dir); err != nil {
		t.Fatalf("runSetupClaudeDefaults: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("reading settings.json: %v", err)
	}

	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parsing settings.json: %v", err)
	}

	hooks, ok := settings["hooks"].(map[string]any)
	if !ok {
		t.Fatal("settings.json missing hooks")
	}

	if _, ok := hooks["SessionStart"]; !ok {
		t.Error("missing SessionStart hook")
	}
	if _, ok := hooks["Stop"]; !ok {
		t.Error("missing Stop hook")
	}
}

func TestHookContainsKD(t *testing.T) {
	hooks := map[string]any{
		"SessionStart": []any{
			map[string]any{
				"matcher": "",
				"hooks": []any{
					map[string]any{"type": "command", "command": "kd bus emit --hook=SessionStart"},
				},
			},
		},
		"Stop": []any{
			map[string]any{
				"matcher": "",
				"hooks": []any{
					map[string]any{"type": "command", "command": "other-tool --hook=Stop"},
				},
			},
		},
	}

	if !hookContainsKD(hooks, "SessionStart") {
		t.Error("expected SessionStart to contain kd hook")
	}
	if hookContainsKD(hooks, "Stop") {
		t.Error("expected Stop to NOT contain kd hook")
	}
	if hookContainsKD(hooks, "PreToolUse") {
		t.Error("expected missing hook type to return false")
	}
}

func TestRunSetupClaudeRemove(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	_ = os.MkdirAll(claudeDir, 0755)

	// Write settings with both kd hooks and other hooks.
	settings := map[string]any{
		"hooks": map[string]any{
			"SessionStart": []any{
				map[string]any{
					"matcher": "",
					"hooks": []any{
						map[string]any{"type": "command", "command": "kd bus emit --hook=SessionStart"},
					},
				},
				map[string]any{
					"matcher": "",
					"hooks": []any{
						map[string]any{"type": "command", "command": "other-tool start"},
					},
				},
			},
			"Stop": []any{
				map[string]any{
					"matcher": "",
					"hooks": []any{
						map[string]any{"type": "command", "command": "kd bus emit --hook=Stop"},
					},
				},
			},
		},
	}
	data, _ := json.MarshalIndent(settings, "", "  ")
	_ = os.WriteFile(filepath.Join(claudeDir, "settings.json"), data, 0600)

	if err := runSetupClaudeRemove(dir); err != nil {
		t.Fatalf("runSetupClaudeRemove: %v", err)
	}

	// Re-read and verify.
	data, _ = os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	var result map[string]any
	json.Unmarshal(data, &result)
	hooks := result["hooks"].(map[string]any)

	// SessionStart should still have the other-tool entry.
	ssArr := hooks["SessionStart"].([]any)
	if len(ssArr) != 1 {
		t.Errorf("SessionStart should have 1 entry after remove, got %d", len(ssArr))
	}

	// Stop should be completely removed (was only kd hook).
	if _, exists := hooks["Stop"]; exists {
		t.Error("Stop hook should be removed entirely")
	}
}

func TestMergeHookLayers(t *testing.T) {
	global := json.RawMessage(`{
		"hooks": {
			"SessionStart": [{"matcher":"","hooks":[{"type":"command","command":"kd bus emit --hook=SessionStart"}]}]
		},
		"permissions": {"deny": ["AskUserQuestion"]}
	}`)

	role := json.RawMessage(`{
		"hooks": {
			"SessionStart": [{"matcher":"","hooks":[{"type":"command","command":"role-specific-hook"}]}],
			"Stop": [{"matcher":"","hooks":[{"type":"command","command":"kd bus emit --hook=Stop"}]}]
		}
	}`)

	result := mergeHookLayers([]json.RawMessage{global, role})

	hooks := result["hooks"].(map[string]any)

	// SessionStart should have BOTH entries (appended).
	ssArr := hooks["SessionStart"].([]any)
	if len(ssArr) != 2 {
		t.Errorf("SessionStart should have 2 entries after merge, got %d", len(ssArr))
	}

	// Stop should have 1 entry (from role).
	stopArr := hooks["Stop"].([]any)
	if len(stopArr) != 1 {
		t.Errorf("Stop should have 1 entry, got %d", len(stopArr))
	}

	// Permissions should be from global (role didn't override).
	perms := result["permissions"].(map[string]any)
	deny := perms["deny"].([]any)
	if len(deny) != 1 || deny[0].(string) != "AskUserQuestion" {
		t.Errorf("permissions.deny not preserved from global layer")
	}
}

func TestMergeSettingsLayer_TopLevelReplace(t *testing.T) {
	dst := map[string]any{"key1": "old", "key2": "keep"}
	src := map[string]any{"key1": "new", "key3": "added"}
	mergeSettingsLayer(dst, src)

	if dst["key1"] != "new" {
		t.Errorf("key1 should be replaced: got %v", dst["key1"])
	}
	if dst["key2"] != "keep" {
		t.Errorf("key2 should be preserved: got %v", dst["key2"])
	}
	if dst["key3"] != "added" {
		t.Errorf("key3 should be added: got %v", dst["key3"])
	}
}

func TestRunSetupClaudeCheck_Installed(t *testing.T) {
	dir := t.TempDir()
	// Install defaults first.
	if err := runSetupClaudeDefaults(dir); err != nil {
		t.Fatalf("setup defaults: %v", err)
	}

	// Check should succeed (not call os.Exit).
	// We can't easily test os.Exit, so just verify the function logic.
	settingsPath := filepath.Join(dir, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var settings map[string]any
	json.Unmarshal(data, &settings)
	hooks := settings["hooks"].(map[string]any)

	if !hookContainsKD(hooks, "SessionStart") {
		t.Error("expected SessionStart kd hook")
	}
	if !hookContainsKD(hooks, "Stop") {
		t.Error("expected Stop kd hook")
	}
}

func TestRunSetupClaudeCheck_NoKDHooks(t *testing.T) {
	dir := t.TempDir()
	claudeDir := filepath.Join(dir, ".claude")
	_ = os.MkdirAll(claudeDir, 0755)

	// Write settings without kd hooks.
	settings := map[string]any{
		"hooks": map[string]any{
			"SessionStart": []any{
				map[string]any{
					"matcher": "",
					"hooks": []any{
						map[string]any{"type": "command", "command": "some-other-tool"},
					},
				},
			},
		},
	}
	data, _ := json.MarshalIndent(settings, "", "  ")
	_ = os.WriteFile(filepath.Join(claudeDir, "settings.json"), data, 0600)

	// Verify hooks check fails.
	var result map[string]any
	json.Unmarshal(data, &result)
	hooks := result["hooks"].(map[string]any)

	if hookContainsKD(hooks, "SessionStart") {
		t.Error("should NOT find kd hook in non-kd settings")
	}

	_ = strings.Contains // use the import
}
