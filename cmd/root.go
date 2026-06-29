package cmd

import (
	"os"

	"github.com/spf13/cobra"

	"runpod-orchestrator/internal/clerk"
	"runpod-orchestrator/internal/config"
	"runpod-orchestrator/internal/runpod"
)

// Persistent flags shared by all commands. They override the config file and
// environment variables when set.
var (
	flagConfigPath   string
	flagSessionID    string
	flagClientCookie string
)

// rootCmd is the base command invoked without any subcommands.
var rootCmd = &cobra.Command{
	Use:   "runpod-orchestrator",
	Short: "Orchestrate RunPod workloads",
	Long:  "runpod-orchestrator is a CLI tool for orchestrating RunPod GPU/CPU workloads.",
}

// Execute runs the root command and exits non-zero on error.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	pf := rootCmd.PersistentFlags()
	pf.StringVar(&flagConfigPath, "config", "", "path to config file (default: ~/.config/runpod-orchestrator/config.json)")
	pf.StringVar(&flagSessionID, "session-id", "", "Clerk session id (overrides config/env)")
	pf.StringVar(&flagClientCookie, "client-cookie", "", "Clerk __client cookie value (overrides config/env)")

	rootCmd.AddCommand(hashcatCmd)
	rootCmd.AddCommand(sagemathCmd)
}

// loadConfig resolves credentials from the config file, environment, and flags
// (flags take precedence), then validates them.
func loadConfig() (*config.Config, error) {
	cfg, err := config.Load(flagConfigPath)
	if err != nil {
		return nil, err
	}
	if flagSessionID != "" {
		cfg.SessionID = flagSessionID
	}
	if flagClientCookie != "" {
		cfg.ClientCookie = flagClientCookie
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// newClient builds an authenticated RunPod client from the resolved config.
func newClient() (*runpod.Client, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	tokens := clerk.New(cfg.SessionID, cfg.ClientCookie)
	return runpod.New(tokens), nil
}
