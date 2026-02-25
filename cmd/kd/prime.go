package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/groblegark/kbeads/internal/client"
	"github.com/groblegark/kbeads/internal/model"
	"github.com/spf13/cobra"
)

var primeCmd = &cobra.Command{
	Use:        "prime",
	Short:      "Output AI-optimized workflow context",
	Deprecated: "use 'gb prime' instead (ported to gasboat)",
	Long: `Output essential kbeads workflow context in AI-optimized markdown format.

Outputs 5 sections:
1. Workflow context — session close protocol, core rules, essential commands
2. Advice — scoped advice beads matching agent subscriptions
3. Jack awareness — active/expired infrastructure jacks
4. Agent roster — live agents with tasks, idle times, crash state
5. Auto-assign — assigns highest-priority ready task if agent is idle

Agent identity is resolved from KD_ACTOR or KD_AGENT_ID env vars,
or the --for flag.

Examples:
  kd prime
  kd prime --for beads/crew/test-agent
  kd prime --no-advice
  kd prime --json`,
	RunE: runPrime,
}

var (
	primeForAgent string
	primeNoAdvice bool
)

func init() {
	primeCmd.Flags().StringVar(&primeForAgent, "for", "", "agent ID to inject matching advice for")
	primeCmd.Flags().BoolVar(&primeNoAdvice, "no-advice", false, "suppress advice output")
	// Keep --agent as hidden alias for backwards compat.
	primeCmd.Flags().String("agent", "", "agent ID (deprecated: use --for)")
	_ = primeCmd.Flags().MarkHidden("agent")
}

func runPrime(cmd *cobra.Command, args []string) error {
	w := os.Stdout

	// Resolve agent identity: --for > --agent > KD_ACTOR > KD_AGENT_ID.
	agentID := resolvePrimeAgent(cmd)

	// 1. Workflow context.
	outputWorkflowContext(w)

	// 2. Advice (existing logic).
	if !primeNoAdvice && agentID != "" {
		outputAdvice(w, beadsClient, agentID)
	}

	// 3. Jack awareness.
	outputJackSection(w, beadsClient)

	// 4. Agent roster.
	outputRosterSection(w, beadsClient, agentID)

	// 5. Auto-assign (if agent has no in_progress bead).
	if agentID != "" {
		outputAutoAssign(w, beadsClient, agentID)
	}

	return nil
}

// resolvePrimeAgent resolves the agent identity for prime output.
func resolvePrimeAgent(cmd *cobra.Command) string {
	if primeForAgent != "" {
		return primeForAgent
	}
	// Backwards compat: --agent flag.
	if v, _ := cmd.Flags().GetString("agent"); v != "" {
		return v
	}
	if v := os.Getenv("KD_ACTOR"); v != "" {
		return v
	}
	if v := os.Getenv("KD_AGENT_ID"); v != "" {
		return v
	}
	// Fall back to the global actor (from --actor flag or git config).
	if actor != "" && actor != "unknown" {
		return actor
	}
	return ""
}

// outputWorkflowContext writes the core workflow context section.
func outputWorkflowContext(w io.Writer) {
	ctx := `# Beads Workflow Context

> **Context Recovery**: Run ` + "`kd prime`" + ` after compaction, clear, or new session
> Hooks auto-call this in Claude Code when kbeads is configured

# 🚨 SESSION CLOSE PROTOCOL 🚨

**CRITICAL**: Before saying "done" or "complete", you MUST run this checklist:

` + "```" + `
[ ] 1. git status              (check what changed)
[ ] 2. git add <files>         (stage code changes)
[ ] 3. git commit -m "..."     (commit code)
[ ] 4. git push                (push to remote)
` + "```" + `

**NEVER skip this.** Work is not done until pushed.

## Core Rules
- **Default**: Use kbeads for ALL task tracking (` + "`kd create`" + `, ` + "`kd ready`" + `, ` + "`kd close`" + `)
- **Prohibited**: Do NOT use TodoWrite, TaskCreate, or markdown files for task tracking
- **Workflow**: Create kbeads issue BEFORE writing code, ` + "`kd claim <id>`" + ` when starting
- Persistence you don't need beats lost context
- Git workflow: beads auto-synced by Postgres backend
- Session management: check ` + "`kd ready`" + ` for available work

## Essential Commands

### Finding Work
- ` + "`kd ready`" + ` - Show issues ready to work (no blockers)
- ` + "`kd news`" + ` - Show in-progress work by others (check for conflicts before starting)
- ` + "`kd list --status=open`" + ` - All open issues
- ` + "`kd list --status=in_progress`" + ` - Your active work
- ` + "`kd show <id>`" + ` - Detailed issue view with dependencies

### Creating & Updating
- ` + "`kd create --title=\"...\" --type=task|bug|feature --priority=2`" + ` - New issue
  - Priority: 0-4 or P0-P4 (0=critical, 2=medium, 4=backlog). NOT "high"/"medium"/"low"
- ` + "`kd claim <id>`" + ` - Claim work (sets assignee + status=in_progress)
- ` + "`kd update <id> --assignee=username`" + ` - Assign to someone
- ` + "`kd close <id>`" + ` - Mark complete
- ` + "`kd close <id> --reason=\"explanation\"`" + ` - Close with reason
- **WARNING**: Do NOT use ` + "`kd edit`" + ` - it opens $EDITOR (vim/nano) which blocks agents

### Dependencies & Blocking
- ` + "`kd dep add <issue> <depends-on>`" + ` - Add dependency
- ` + "`kd blocked`" + ` - Show all blocked issues
- ` + "`kd show <id>`" + ` - See what's blocking/blocked by this issue

### Project Health
- ` + "`kd list --status=open | wc -l`" + ` - Count open issues
- ` + "`kd gate status`" + ` - Show session gate status (decision, commit-push, etc.)

## Common Workflows

**Starting work:**
` + "```bash" + `
kd news            # Check what others are working on (avoid conflicts)
kd ready           # Find available work
kd show <id>       # Review issue details
kd claim <id>      # Claim it (sets assignee + in_progress)
` + "```" + `

**Completing work:**
` + "```bash" + `
kd close <id>              # Close completed issue
git add <files> && git commit -m "..." && git push
` + "```" + `

**Creating dependent work:**
` + "```bash" + `
kd create --title="Implement feature X" --type=feature
kd create --title="Write tests for X" --type=task
kd dep add <tests-id> <feature-id>  # Tests depend on Feature
` + "```" + `

## Human Decisions

When you need human input (approval, choices, clarification), use decision points:

` + "```bash" + `
# Create a decision point — blocks until human responds
kd decision create --prompt="Deploy to production?" \
  --options='[{"id":"y","label":"Yes, deploy"},{"id":"n","label":"No, abort"}]'
` + "```" + `

**Decision commands:**
- ` + "`kd decision create --prompt=\"...\" --options='[...]'`" + ` - Create and wait for response
- ` + "`kd decision list`" + ` - Show pending decisions
- ` + "`kd decision show <id>`" + ` - Decision details
`
	fmt.Fprint(w, ctx)
}

// outputJackSection fetches active/expired jacks and outputs warnings.
func outputJackSection(w io.Writer, c client.BeadsClient) {
	ctx := context.Background()

	resp, err := c.ListBeads(ctx, &client.ListBeadsRequest{
		Type:   []string{"jack"},
		Status: []string{"in_progress"},
		Limit:  50,
	})
	if err != nil || len(resp.Beads) == 0 {
		return
	}

	now := time.Now()
	type jackSummary struct {
		bead       *model.Bead
		target     string
		remaining  time.Duration
		expiredAgo time.Duration
		expired    bool
	}

	var jacks []jackSummary
	for _, b := range resp.Beads {
		j := jackSummary{bead: b}

		// Extract target from fields.
		if b.Fields != nil {
			var fields map[string]interface{}
			if json.Unmarshal(b.Fields, &fields) == nil {
				j.target, _ = fields["jack_target"].(string)
			}
		}

		if b.DueAt != nil && now.After(*b.DueAt) {
			j.expired = true
			j.expiredAgo = now.Sub(*b.DueAt)
		} else if b.DueAt != nil {
			j.remaining = time.Until(*b.DueAt)
		}

		jacks = append(jacks, j)
	}

	fmt.Fprintf(w, "\n## Active Jacks (%d)\n\n", len(jacks))
	for _, j := range jacks {
		agent := j.bead.Assignee
		if agent == "" {
			agent = j.bead.CreatedBy
		}
		if j.expired {
			fmt.Fprintf(w, "- **EXPIRED** `%s` on `%s` (by %s, expired %s ago) — run `kd jack down %s`\n",
				j.bead.ID, j.target, agent, formatDuration(j.expiredAgo), j.bead.ID)
		} else {
			remaining := "unknown"
			if j.remaining > 0 {
				remaining = formatDuration(j.remaining) + " remaining"
			}
			fmt.Fprintf(w, "- `%s` on `%s` (by %s, %s)\n",
				j.bead.ID, j.target, agent, remaining)
		}
	}
	fmt.Fprintln(w)
}

// outputRosterSection fetches the live agent roster and outputs it.
func outputRosterSection(w io.Writer, c client.BeadsClient, self string) {
	ctx := context.Background()

	roster, err := c.GetAgentRoster(ctx, 1800) // 30-min threshold
	if err != nil || roster == nil || len(roster.Actors) == 0 {
		return
	}

	// Partition into active vs stale.
	const staleThresholdSecs = 600 // 10 minutes
	var active, stale []client.RosterEntry
	for _, a := range roster.Actors {
		if a.Reaped {
			stale = append(stale, a)
			continue
		}
		isStopped := a.LastEvent == "Stop" && a.IdleSecs > 60
		if isStopped {
			continue
		}
		if a.IdleSecs > staleThresholdSecs {
			stale = append(stale, a)
		} else {
			active = append(active, a)
		}
	}

	if len(active) == 0 && len(stale) == 0 {
		return
	}

	fmt.Fprintf(w, "\n## Active Agents (%d)\n\n", len(active))
	if self != "" {
		fmt.Fprintf(w, "You are **%s**. Do not pick up other agents' in-progress tasks.\n\n", self)
	}

	for _, a := range active {
		idleStr := formatIdleDur(a.IdleSecs)
		youTag := ""
		if self != "" && a.Actor == self {
			youTag = " ← you"
		}

		if a.TaskID != "" {
			epicStr := ""
			if a.EpicTitle != "" {
				epicStr = fmt.Sprintf(" (epic: %s)", a.EpicTitle)
			}
			fmt.Fprintf(w, "- **%s**%s — working on %s: %s%s (idle %s)\n",
				a.Actor, youTag, a.TaskID, a.TaskTitle, epicStr, idleStr)
		} else {
			activityHint := ""
			if a.ToolName != "" {
				activityHint = fmt.Sprintf(", last: %s", a.ToolName)
			}
			fmt.Fprintf(w, "- **%s**%s — active, no claimed task (idle %s%s)\n",
				a.Actor, youTag, idleStr, activityHint)
		}
	}

	// Show stale agents.
	if len(stale) > 0 {
		var crashed, idle []string
		for _, a := range stale {
			idleStr := formatIdleDur(a.IdleSecs)
			if a.Reaped {
				if a.TaskID != "" {
					crashed = append(crashed, fmt.Sprintf("%s (had %s: %s)", a.Actor, a.TaskID, a.TaskTitle))
				} else {
					crashed = append(crashed, fmt.Sprintf("%s (idle %s)", a.Actor, idleStr))
				}
			} else {
				idle = append(idle, fmt.Sprintf("%s (idle %s)", a.Actor, idleStr))
			}
		}
		if len(crashed) > 0 {
			fmt.Fprintf(w, "\n_Crashed (%d): %s_\n", len(crashed), strings.Join(crashed, ", "))
		}
		if len(idle) > 0 {
			fmt.Fprintf(w, "\n_Stale (%d, likely disconnected): %s_\n", len(idle), strings.Join(idle, ", "))
		}
	}

	// Show unclaimed work.
	if len(roster.UnclaimedTasks) > 0 {
		fmt.Fprintf(w, "\n> **Unclaimed in-progress work** (no assignee — consider claiming):\n")
		for _, t := range roster.UnclaimedTasks {
			fmt.Fprintf(w, ">   - %s [P%d]: %s\n", t.ID, t.Priority, t.Title)
		}
	}

	fmt.Fprintln(w)
}

// outputAutoAssign checks if the agent has in_progress beads and auto-assigns
// the highest-priority ready task if idle.
func outputAutoAssign(w io.Writer, c client.BeadsClient, agentID string) {
	ctx := context.Background()

	// Check if agent already has in_progress work.
	resp, err := c.ListBeads(ctx, &client.ListBeadsRequest{
		Assignee: agentID,
		Status:   []string{"in_progress"},
		Limit:    1,
	})
	if err != nil || len(resp.Beads) > 0 {
		return // agent already has work
	}

	// Fetch ready tasks.
	ready, err := c.ListBeads(ctx, &client.ListBeadsRequest{
		Status: []string{"open"},
		Sort:   "priority",
		Limit:  1,
	})
	if err != nil || len(ready.Beads) == 0 {
		return
	}

	// Auto-claim.
	task := ready.Beads[0]
	inProgress := "in_progress"
	_, err = c.UpdateBead(ctx, task.ID, &client.UpdateBeadRequest{
		Assignee: &agentID,
		Status:   &inProgress,
	})
	if err != nil {
		return // fail silently
	}

	fmt.Fprintf(w, "\nAuto-assigned bead %s: %s\n", task.ID, task.Title)
	fmt.Fprintf(w, "Run `kd show %s` for full details.\n", task.ID)
}

// --- Advice output (existing logic, unchanged) ---

// outputAdvice fetches open advice beads, filters by agent subscriptions,
// groups by scope, and writes markdown to w.
func outputAdvice(w io.Writer, c client.BeadsClient, agentID string) {
	ctx := context.Background()

	resp, err := c.ListBeads(ctx, &client.ListBeadsRequest{
		Type:   []string{"advice"},
		Status: []string{"open"},
		Limit:  500,
	})
	if err != nil || len(resp.Beads) == 0 {
		return
	}

	subs := model.BuildAgentSubscriptions(agentID, nil)

	type matchedAdvice struct {
		Bead          *model.Bead
		MatchedLabels []string
	}
	var matched []matchedAdvice
	for _, bead := range resp.Beads {
		if model.MatchesSubscriptions(bead.Labels, subs) {
			ml := findMatchedAdviceLabels(bead.Labels, subs)
			matched = append(matched, matchedAdvice{Bead: bead, MatchedLabels: ml})
		}
	}

	if len(matched) == 0 {
		return
	}

	if jsonOutput {
		type jsonItem struct {
			ID            string   `json:"id"`
			Title         string   `json:"title"`
			Description   string   `json:"description,omitempty"`
			Labels        []string `json:"labels"`
			MatchedLabels []string `json:"matched_labels"`
		}
		items := make([]jsonItem, len(matched))
		for i, m := range matched {
			items[i] = jsonItem{
				ID:            m.Bead.ID,
				Title:         m.Bead.Title,
				Description:   m.Bead.Description,
				Labels:        m.Bead.Labels,
				MatchedLabels: m.MatchedLabels,
			}
		}
		data, _ := json.MarshalIndent(items, "", "  ")
		fmt.Fprintln(w, string(data))
		return
	}

	type scopeGroup struct {
		Scope  string
		Target string
		Header string
		Items  []matchedAdvice
	}

	groupMap := make(map[string]*scopeGroup)
	for _, m := range matched {
		scope, target := categorizeScope(m.Bead.Labels)
		key := scope + ":" + target
		g, ok := groupMap[key]
		if !ok {
			g = &scopeGroup{
				Scope:  scope,
				Target: target,
				Header: buildHeader(scope, target),
			}
			groupMap[key] = g
		}
		g.Items = append(g.Items, m)
	}

	var groups []*scopeGroup
	for _, g := range groupMap {
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool {
		return groupSortKey(groups[i].Scope, groups[i].Target) < groupSortKey(groups[j].Scope, groups[j].Target)
	})

	fmt.Fprintf(w, "\n## Advice (%d items)\n\n", len(matched))
	for _, g := range groups {
		for _, item := range g.Items {
			fmt.Fprintf(w, "**[%s]** %s\n", g.Header, item.Bead.Title)
			desc := item.Bead.Description
			if desc != "" && desc != item.Bead.Title {
				for _, line := range strings.Split(desc, "\n") {
					fmt.Fprintf(w, "  %s\n", line)
				}
			}
			fmt.Fprintln(w)
		}
	}
}

// --- Helper functions ---

func findMatchedAdviceLabels(adviceLabels, subscriptions []string) []string {
	subSet := make(map[string]bool, len(subscriptions))
	for _, s := range subscriptions {
		subSet[s] = true
	}
	seen := make(map[string]bool)
	var matched []string
	for _, l := range adviceLabels {
		clean := model.StripGroupPrefix(l)
		if subSet[clean] && !seen[clean] {
			matched = append(matched, clean)
			seen[clean] = true
		}
	}
	return matched
}

func categorizeScope(labels []string) (scope, target string) {
	for _, l := range labels {
		clean := model.StripGroupPrefix(l)
		switch {
		case strings.HasPrefix(clean, "agent:"):
			return "agent", strings.TrimPrefix(clean, "agent:")
		case strings.HasPrefix(clean, "role:"):
			scope, target = "role", strings.TrimPrefix(clean, "role:")
		case strings.HasPrefix(clean, "rig:") && scope != "role":
			scope, target = "rig", strings.TrimPrefix(clean, "rig:")
		case clean == "global" && scope == "":
			scope, target = "global", ""
		}
	}
	if scope == "" {
		scope = "global"
	}
	return scope, target
}

func buildHeader(scope, target string) string {
	switch scope {
	case "global":
		return "Global"
	case "rig":
		return "Rig: " + target
	case "role":
		return "Role: " + target
	case "agent":
		return "Agent: " + target
	default:
		return scope
	}
}

func groupSortKey(scope, target string) string {
	switch scope {
	case "global":
		return "0:" + target
	case "rig":
		return "1:" + target
	case "role":
		return "2:" + target
	case "agent":
		return "3:" + target
	default:
		return "9:" + target
	}
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%dm", h, m)
}

func formatIdleDur(secs float64) string {
	if secs < 60 {
		return fmt.Sprintf("%ds", int(secs))
	}
	if secs < 3600 {
		return fmt.Sprintf("%dm%ds", int(secs)/60, int(secs)%60)
	}
	h := int(secs) / 3600
	m := (int(secs) % 3600) / 60
	return fmt.Sprintf("%dh%dm", h, m)
}
