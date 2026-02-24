package main

// agent_start_k8s.go — kd agent start --k8s
//
// Replaces entrypoint.sh as the PID-1 process in the K8s agent pod.
// Handles all workspace setup, credential provisioning, coop startup,
// background goroutines, signal forwarding, and the restart loop.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

// k8sConfig holds all configuration for the K8s agent start command,
// resolved from flags and environment variables at startup.
type k8sConfig struct {
	workspace      string
	coopPort       int
	coopHealthPort int
	maxRestarts    int
	command        string
	sessionResume  bool

	// from env
	role    string
	project string
	agent   string
	podIP   string
	hostname string
}

func init() {
	agentStartCmd.Flags().String("workspace", "/home/agent/workspace", "Workspace path (--k8s only)")
	agentStartCmd.Flags().Int("coop-port", 8080, "Coop HTTP port (--k8s only)")
	agentStartCmd.Flags().Int("coop-health-port", 9090, "Coop health port (--k8s only)")
	agentStartCmd.Flags().Int("max-restarts", 0, "Max consecutive restarts — 0 uses COOP_MAX_RESTARTS env or 10 (--k8s only)")
}

func runAgentStartK8s(cmd *cobra.Command, args []string) error {
	workspace, _ := cmd.Flags().GetString("workspace")
	coopPort, _ := cmd.Flags().GetInt("coop-port")
	coopHealthPort, _ := cmd.Flags().GetInt("coop-health-port")
	maxRestarts, _ := cmd.Flags().GetInt("max-restarts")
	if maxRestarts == 0 {
		maxRestarts = intEnvOr("COOP_MAX_RESTARTS", 10)
	}

	hostname, _ := os.Hostname()
	podIP := os.Getenv("POD_IP")
	if podIP == "" {
		podIP = "localhost"
	}

	cfg := k8sConfig{
		workspace:      workspace,
		coopPort:       coopPort,
		coopHealthPort: coopHealthPort,
		maxRestarts:    maxRestarts,
		command:        envOr("BOAT_COMMAND", "claude --dangerously-skip-permissions"),
		sessionResume:  envOr("BOAT_SESSION_RESUME", "1") == "1",
		role:           envOr("BOAT_ROLE", "unknown"),
		project:        os.Getenv("BOAT_PROJECT"),
		agent:          envOr("BOAT_AGENT", "unknown"),
		podIP:          podIP,
		hostname:       hostname,
	}

	fmt.Printf("[kd agent start] starting %s agent (mode: k8s): %s (project: %s)\n",
		cfg.role, cfg.agent, orStr(cfg.project, "none"))

	// ── One-time setup (idempotent on restart) ──────────────────────────

	if err := setupWorkspace(cfg); err != nil {
		return fmt.Errorf("workspace setup: %w", err)
	}

	stateDir := filepath.Join(workspace, ".state")
	claudeStateDir := filepath.Join(stateDir, "claude")
	coopStateDir := filepath.Join(stateDir, "coop")

	if err := setupPVC(cfg); err != nil {
		return fmt.Errorf("PVC setup: %w", err)
	}

	credMode := provisionCredentials(claudeStateDir)

	claudeDir := filepath.Join(homeDir(), ".claude")
	if err := writeClaudeSettings(claudeDir); err != nil {
		fmt.Printf("[kd agent start] warning: write claude settings: %v\n", err)
	}

	// Hook materialization via kd setup claude (falls back to defaults).
	if err := runSetupClaude(context.Background(), workspace, cfg.role); err != nil {
		fmt.Printf("[kd agent start] config beads not found, installing default hooks\n")
		if err2 := runSetupClaudeDefaults(workspace); err2 != nil {
			fmt.Printf("[kd agent start] warning: could not write workspace .claude/settings.json: %v\n", err2)
		}
	}

	writeClaudeMD(cfg)
	writeOnboardingSkip()

	// ── Context + signal handling ────────────────────────────────────────

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// ── Mux registration ─────────────────────────────────────────────────

	mux := newMuxClient()
	coopURL := fmt.Sprintf("http://%s:%d", cfg.podIP, cfg.coopPort)
	go func() {
		if err := mux.Register(ctx, cfg.hostname, coopURL, cfg.role, cfg.agent, cfg.hostname, cfg.podIP); err != nil {
			fmt.Printf("[kd agent start] mux register error: %v\n", err)
		}
	}()
	defer mux.Deregister(cfg.hostname)

	// ── OAuth refresh (survives coop restarts) ───────────────────────────

	go oauthRefreshLoop(ctx, claudeStateDir, credMode)

	// ── Restart loop ──────────────────────────────────────────────────────

	const minRuntimeSecs = 30
	restarts := 0

	for {
		if ctx.Err() != nil {
			return nil // SIGTERM/SIGINT — clean exit
		}
		if restarts >= cfg.maxRestarts {
			return fmt.Errorf("max restarts (%d) reached", cfg.maxRestarts)
		}

		cleanStalePipes(coopStateDir)
		resumeLog := findResumeSession(claudeStateDir, cfg.sessionResume)

		start := time.Now()
		exitCode, _ := runCoopOnce(ctx, cfg, coopStateDir, resumeLog)
		elapsed := time.Since(start)

		if ctx.Err() != nil {
			return nil // clean SIGTERM exit
		}

		fmt.Printf("[kd agent start] coop exited (code %d) after %s\n", exitCode, elapsed.Round(time.Second))

		if exitCode != 0 && resumeLog != "" {
			retireStaleSession(resumeLog)
		}

		if elapsed >= time.Duration(minRuntimeSecs)*time.Second {
			restarts = 0
		}
		restarts++
		fmt.Printf("[kd agent start] restarting (attempt %d/%d) in 2s...\n", restarts, cfg.maxRestarts)

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(2 * time.Second):
		}
	}
}

// runCoopOnce starts coop for a single session, launches per-session
// goroutines, waits for coop to exit, and returns its exit code.
func runCoopOnce(ctx context.Context, cfg k8sConfig, coopStateDir, resumeLog string) (int, error) {
	coopArgs := []string{
		"--agent=claude",
		fmt.Sprintf("--port=%d", cfg.coopPort),
		fmt.Sprintf("--port-health=%d", cfg.coopHealthPort),
		"--cols=200", "--rows=50",
	}
	if resumeLog != "" {
		coopArgs = append(coopArgs, "--resume", resumeLog)
		fmt.Printf("[kd agent start] starting coop (%s/%s) with resume\n", cfg.role, cfg.agent)
	} else {
		fmt.Printf("[kd agent start] starting coop (%s/%s)\n", cfg.role, cfg.agent)
	}
	coopArgs = append(coopArgs, "--", "sh", "-c", cfg.command)

	// Each session gets a child context so goroutines exit when coop exits.
	sessionCtx, sessionCancel := context.WithCancel(ctx)
	defer sessionCancel()

	coopCmd := exec.CommandContext(sessionCtx, "coop", coopArgs...)
	coopCmd.Dir = cfg.workspace
	coopCmd.Stdout = os.Stdout
	coopCmd.Stderr = os.Stderr
	coopCmd.Env = append(os.Environ(),
		"COOP_LOG_LEVEL="+envOr("COOP_LOG_LEVEL", "info"),
	)

	if err := coopCmd.Start(); err != nil {
		return 1, fmt.Errorf("start coop: %w", err)
	}

	// Per-session goroutines.
	go autoBypassStartup(sessionCtx, cfg.coopPort)
	go injectInitialPrompt(sessionCtx, cfg.coopPort, cfg.role)
	go monitorAgentExit(sessionCtx, cfg.coopPort)

	waitErr := coopCmd.Wait()
	sessionCancel() // signal goroutines to stop

	exitCode := 0
	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
				exitCode = status.ExitStatus()
			} else {
				exitCode = 1
			}
		}
	}
	return exitCode, nil
}

// ── helpers ───────────────────────────────────────────────────────────────

func intEnvOr(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		return def
	}
	return n
}

func orStr(s, def string) string {
	if strings.TrimSpace(s) != "" {
		return s
	}
	return def
}
