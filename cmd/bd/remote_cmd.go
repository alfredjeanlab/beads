package main

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var remoteCmd = &cobra.Command{
	Use:   "remote",
	Short: "Manage named server remotes",
	// Skip the gRPC dial — all remote subcommands are local file operations.
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error { return nil },
}

var remoteAddCmd = &cobra.Command{
	Use:   "add <name> <url>",
	Short: "Add or update a named remote",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		name, url := args[0], args[1]
		token, _ := cmd.Flags().GetString("token")
		natsURL, _ := cmd.Flags().GetString("nats")

		cfg, err := loadRemotesConfig()
		if err != nil {
			return err
		}
		cfg.Remotes[name] = Remote{URL: url, Token: token, NATSURL: natsURL}
		if err := saveRemotesConfig(cfg); err != nil {
			return err
		}
		fmt.Printf("remote %q added (%s)\n", name, url)
		return nil
	},
}

var remoteRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove a named remote",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		cfg, err := loadRemotesConfig()
		if err != nil {
			return err
		}
		if _, ok := cfg.Remotes[name]; !ok {
			return fmt.Errorf("remote %q not found", name)
		}
		delete(cfg.Remotes, name)
		if cfg.Active == name {
			cfg.Active = ""
		}
		if err := saveRemotesConfig(cfg); err != nil {
			return err
		}
		fmt.Printf("remote %q removed\n", name)
		return nil
	},
}

var remoteListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all remotes",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadRemotesConfig()
		if err != nil {
			return err
		}
		if len(cfg.Remotes) == 0 {
			fmt.Println("no remotes configured")
			return nil
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "  NAME\tURL\tTOKEN")
		for name, r := range cfg.Remotes {
			marker := "  "
			if name == cfg.Active {
				marker = "* "
			}
			token := ""
			if r.Token != "" {
				if len(r.Token) > 8 {
					token = r.Token[:8] + "..."
				} else {
					token = r.Token
				}
			}
			fmt.Fprintf(w, "%s%s\t%s\t%s\n", marker, name, r.URL, token)
		}
		return w.Flush()
	},
}

var remoteUseCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Set the active remote",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		cfg, err := loadRemotesConfig()
		if err != nil {
			return err
		}
		if _, ok := cfg.Remotes[name]; !ok {
			return fmt.Errorf("remote %q not found", name)
		}
		cfg.Active = name
		if err := saveRemotesConfig(cfg); err != nil {
			return err
		}
		fmt.Printf("active remote set to %q\n", name)
		return nil
	},
}

var remoteShowCmd = &cobra.Command{
	Use:   "show [<name>]",
	Short: "Show details for a remote (defaults to active)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := loadRemotesConfig()
		if err != nil {
			return err
		}

		name := cfg.Active
		if len(args) == 1 {
			name = args[0]
		}
		if name == "" {
			return fmt.Errorf("no active remote; specify a name or run 'bd remote use <name>'")
		}

		r, ok := cfg.Remotes[name]
		if !ok {
			return fmt.Errorf("remote %q not found", name)
		}

		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		active := ""
		if name == cfg.Active {
			active = " (active)"
		}
		fmt.Fprintf(w, "name:\t%s%s\n", name, active)
		fmt.Fprintf(w, "url:\t%s\n", r.URL)
		if r.Token != "" {
			masked := r.Token
			if len(masked) > 8 {
				masked = masked[:8] + strings.Repeat("*", len(masked)-8)
			}
			fmt.Fprintf(w, "token:\t%s\n", masked)
		}
		if r.NATSURL != "" {
			fmt.Fprintf(w, "nats_url:\t%s\n", r.NATSURL)
		}
		return w.Flush()
	},
}

func init() {
	remoteAddCmd.Flags().String("token", "", "bearer token for authentication")
	remoteAddCmd.Flags().String("nats", "", "NATS URL for event streaming")

	remoteCmd.AddCommand(remoteAddCmd)
	remoteCmd.AddCommand(remoteRemoveCmd)
	remoteCmd.AddCommand(remoteListCmd)
	remoteCmd.AddCommand(remoteUseCmd)
	remoteCmd.AddCommand(remoteShowCmd)
}
