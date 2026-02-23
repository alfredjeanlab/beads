package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	beadsv1 "github.com/alfredjeanlab/beads/gen/beads/v1"
	"github.com/alfredjeanlab/beads/internal/ui"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	serverAddr string
	jsonOutput bool
	actor      string

	conn   *grpc.ClientConn
	client beadsv1.BeadsServiceClient
)

func defaultActor() string {
	out, err := exec.Command("git", "config", "user.name").Output()
	if err == nil {
		name := strings.TrimSpace(string(out))
		if name != "" {
			return name
		}
	}
	return "unknown"
}

func defaultServer() string {
	if s := os.Getenv("BEADS_SERVER"); s != "" {
		return s
	}
	if u := activeRemoteURL(); u != "" {
		return u
	}
	return "localhost:9090"
}

var rootCmd = &cobra.Command{
	Use:   "bd <command>",
	Short: "CLI client for the Beads service",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
		if tok := activeRemoteToken(); tok != "" {
			opts = append(opts, grpc.WithUnaryInterceptor(bearerTokenInterceptor(tok)))
		}
		var err error
		conn, err = grpc.NewClient(serverAddr, opts...)
		if err != nil {
			return fmt.Errorf("failed to connect to server: %w", err)
		}
		client = beadsv1.NewBeadsServiceClient(conn)
		return nil
	},
	PersistentPostRun: func(cmd *cobra.Command, args []string) {
		if conn != nil {
			conn.Close()
		}
	},
}

func init() {
	if !ui.ShouldUseColor() {
		ui.ForceNoColor()
	}

	rootCmd.PersistentFlags().StringVar(&serverAddr, "server", defaultServer(), "gRPC server address")
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "output as JSON")
	rootCmd.PersistentFlags().StringVar(&actor, "actor", defaultActor(), "actor name for created_by fields")

	rootCmd.AddGroup(
		&cobra.Group{ID: "beads", Title: "Beads:"},
		&cobra.Group{ID: "workflow", Title: "Workflows:"},
		&cobra.Group{ID: "views", Title: "Views:"},
		&cobra.Group{ID: "system", Title: "System:"},
	)

	cobra.EnableCommandSorting = false
	rootCmd.SetHelpFunc(colorizedHelpFunc())

	// Beads
	rootCmd.AddCommand(showCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(searchCmd)
	rootCmd.AddCommand(createCmd)
	rootCmd.AddCommand(updateCmd)
	rootCmd.AddCommand(deleteCmd)
	rootCmd.AddCommand(depCmd)
	rootCmd.AddCommand(labelCmd)
	rootCmd.AddCommand(commentCmd)

	// Workflows
	rootCmd.AddCommand(claimCmd)
	rootCmd.AddCommand(unclaimCmd)
	rootCmd.AddCommand(closeCmd)
	rootCmd.AddCommand(reopenCmd)
	rootCmd.AddCommand(doneCmd)
	rootCmd.AddCommand(deferCmd)
	rootCmd.AddCommand(undeferCmd)

	// Views
	rootCmd.AddCommand(viewCmd)
	rootCmd.AddCommand(contextCmd)
	rootCmd.AddCommand(watchCmd)
	rootCmd.AddCommand(treeCmd)

	// System
	rootCmd.AddCommand(serveCmd)
	rootCmd.AddCommand(healthCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(remoteCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
