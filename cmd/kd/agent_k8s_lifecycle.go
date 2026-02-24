package main

// agent_k8s_lifecycle.go — per-session lifecycle goroutines.
//
// Handles: startup prompt bypass, initial work nudge, agent exit monitor,
// and stale session log detection for the restart loop.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// autoBypassStartup polls the coop API and dismisses interactive startup
// prompts (resume picker, API key dialog, setup wizard). Runs as a goroutine
// and exits when ctx is cancelled or after the agent is past startup.
func autoBypassStartup(ctx context.Context, coopPort int) {
	base := fmt.Sprintf("http://localhost:%d/api/v1", coopPort)
	client := &http.Client{Timeout: 3 * time.Second}
	falsePositives := 0

	for i := 0; i < 30; i++ {
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}

		state, err := getAgentState(client, base)
		if err != nil {
			continue
		}

		agentState := state["state"].(string)
		if agentState == "idle" || agentState == "working" {
			return // past startup
		}

		if agentState == "starting" {
			screen, _ := getScreenText(client, base)

			if strings.Contains(screen, "Resume Session") {
				fmt.Printf("[kd agent start] detected resume session picker, pressing Escape
")
				postKeys(client, base, "Escape")
				time.Sleep(3 * time.Second)
				continue
			}
			if strings.Contains(screen, "Detected a custom API key") {
				fmt.Printf("[kd agent start] detected API key prompt, selecting Yes
")
				postKeys(client, base, "Up", "Return")
				time.Sleep(3 * time.Second)
				continue
			}
		}

		promptType := ""
		if pt, ok := state["prompt"].(map[string]any); ok {
			promptType, _ = pt["type"].(string)
		}
		if promptType == "setup" {
			screen, _ := getScreenText(client, base)
			if strings.Contains(screen, "No, exit") {
				subtype := ""
				if pt, ok := state["prompt"].(map[string]any); ok {
					subtype, _ = pt["subtype"].(string)
				}
				fmt.Printf("[kd agent start] auto-accepting setup prompt (subtype: %s)
", subtype)
				respondToAgent(client, base, 2)
				falsePositives = 0
				time.Sleep(5 * time.Second)
				continue
			}
			falsePositives++
			if falsePositives >= 5 {
				fmt.Printf("[kd agent start] skipping false-positive setup prompt
")
				return
			}
		}
	}
	fmt.Printf("[kd agent start] WARNING: auto-bypass timed out after 60s
")
}

// injectInitialPrompt waits for the agent to reach idle state, then sends a
// nudge message to kick off the work session.
func injectInitialPrompt(ctx context.Context, coopPort int, role string) {
	base := fmt.Sprintf("http://localhost:%d/api/v1", coopPort)
	client := &http.Client{Timeout: 3 * time.Second}
	nudge := "Check `kd ready` for your workflow steps and begin working."

	for i := 0; i < 60; i++ {
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}

		state, err := getAgentState(client, base)
		if err != nil {
			continue
		}
		agentState, _ := state["state"].(string)

		if agentState == "working" {
			fmt.Printf("[kd agent start] agent already working, skipping initial prompt
")
			return
		}
		if agentState != "idle" {
			continue
		}

		fmt.Printf("[kd agent start] injecting initial work prompt (role: %s)
", role)
		body, _ := json.Marshal(map[string]string{"message": nudge})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/agent/nudge", bytes.NewReader(body))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			fmt.Printf("[kd agent start] WARNING: nudge failed: %v
", err)
			return
		}
		defer resp.Body.Close()
		var result struct {
			Delivered bool   `json:"delivered"`
			Reason    string `json:"reason"`
		}
		json.NewDecoder(resp.Body).Decode(&result)
		if result.Delivered {
			fmt.Printf("[kd agent start] initial prompt delivered
")
		} else {
			fmt.Printf("[kd agent start] WARNING: nudge not delivered: %s
", result.Reason)
		}
		return
	}
}

// monitorAgentExit polls the coop API and triggers a graceful coop shutdown
// when the agent process exits. Runs as a goroutine.
func monitorAgentExit(ctx context.Context, coopPort int) {
	base := fmt.Sprintf("http://localhost:%d/api/v1", coopPort)
	client := &http.Client{Timeout: 3 * time.Second}

	time.Sleep(10 * time.Second) // let agent start

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}

		state, err := getAgentState(client, base)
		if err != nil {
			return // coop gone
		}
		agentState, _ := state["state"].(string)
		if agentState == "exited" {
			fmt.Printf("[kd agent start] agent exited, requesting coop shutdown
")
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/shutdown", nil)
			client.Do(req) //nolint:errcheck
			return
		}
	}
}

// cleanStalePipes removes leftover hook.pipe FIFO files from the coop state
// directory before each session start.
func cleanStalePipes(coopStateDir string) {
	sessionsDir := filepath.Join(coopStateDir, "sessions")
	if _, err := os.Stat(sessionsDir); err != nil {
		return
	}
	entries, err := filepath.Glob(filepath.Join(sessionsDir, "*", "hook.pipe"))
	if err != nil {
		return
	}
	for _, p := range entries {
		os.Remove(p)
	}
}

// findResumeSession returns the path of the latest non-stale Claude session
// log under claudeStateDir/projects/, or "" if no suitable log exists.
// If sessionResume is false, always returns "".
func findResumeSession(claudeStateDir string, sessionResume bool) string {
	if !sessionResume {
		return ""
	}

	projectsDir := filepath.Join(claudeStateDir, "projects")
	if _, err := os.Stat(projectsDir); err != nil {
		return ""
	}

	// Count stale sessions; too many → skip resume.
	staleCount := 0
	_ = filepath.Walk(projectsDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && strings.HasSuffix(path, ".jsonl.stale") && !info.IsDir() {
			staleCount++
		}
		return nil
	})
	const maxStaleRetries = 2
	if staleCount >= maxStaleRetries {
		fmt.Printf("[kd agent start] skipping resume: %d stale session(s) found (max %d)
", staleCount, maxStaleRetries)
		return ""
	}

	// Find the most recently modified .jsonl that is not in a subagents dir.
	type candidate struct {
		path    string
		modTime time.Time
	}
	var candidates []candidate
	_ = filepath.Walk(projectsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if strings.Contains(path, "/subagents/") {
			return nil
		}
		candidates = append(candidates, candidate{path, info.ModTime()})
		return nil
	})

	if len(candidates) == 0 {
		return ""
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].modTime.After(candidates[j].modTime)
	})
	return candidates[0].path
}

// retireStaleSession renames a session log to .jsonl.stale so it won't be
// resumed on the next restart.
func retireStaleSession(logPath string) {
	stalePath := logPath + ".stale"
	if err := os.Rename(logPath, stalePath); err != nil {
		fmt.Printf("[kd agent start] WARNING: could not retire stale session: %v
", err)
	} else {
		fmt.Printf("[kd agent start] retired stale session: %s → %s
", logPath, stalePath)
	}
}

// ── coop HTTP helpers ─────────────────────────────────────────────────────

func getAgentState(client *http.Client, base string) (map[string]any, error) {
	resp, err := client.Get(base + "/agent")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var state map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		return nil, err
	}
	if _, ok := state["state"]; !ok {
		state["state"] = ""
	}
	return state, nil
}

func getScreenText(client *http.Client, base string) (string, error) {
	resp, err := client.Get(base + "/screen/text")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return string(data), err
}

func postKeys(client *http.Client, base string, keys ...string) {
	body, _ := json.Marshal(map[string][]string{"keys": keys})
	req, _ := http.NewRequest(http.MethodPost, base+"/input/keys", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

func respondToAgent(client *http.Client, base string, option int) {
	body, _ := json.Marshal(map[string]int{"option": option})
	req, _ := http.NewRequest(http.MethodPost, base+"/agent/respond", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}
