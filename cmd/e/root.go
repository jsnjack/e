package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/jsnjack/e/internal/ai"
	"github.com/jsnjack/e/internal/config"
	"github.com/jsnjack/e/internal/logging"
	"github.com/jsnjack/e/internal/ui"
	"github.com/spf13/cobra"
	"github.com/zalando/go-keyring"
)

const (
	traceLogPath     = "/tmp/e.log"
	aiKeyringService = "e-ai"
)

var (
	flagDebug  bool
	flagTrace  bool
	flagConfig string
	logCleanup = func() {}
)

var rootCmd = &cobra.Command{
	Use:           "e [FILE...]",
	Short:         "A fast native text editor with AI autocomplete",
	Version:       Version,
	Args:          cobra.ArbitraryArgs,
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		tracePath := ""
		if flagTrace {
			tracePath = traceLogPath
		}
		cleanup, err := logging.Init(tracePath, flagDebug)
		if err != nil {
			return err
		}
		logCleanup = cleanup
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		paths, err := config.DefaultPaths()
		if err != nil {
			return err
		}
		configPath := flagConfig
		if configPath == "" {
			configPath = paths.ConfigFile
		}
		cfg, err := config.Load(configPath)
		if err != nil {
			return err
		}
		ai.SetUserAgent("E/" + Version)
		providers, err := ai.NewProviders(cfg.AI.Chain, lookupAIKey)
		if err != nil {
			return fmt.Errorf("configure autocomplete: %w", err)
		}
		var completer *ai.Completer
		if len(providers) > 0 {
			completer = ai.NewCompleter(providers)
		}
		slog.Info("e starting", "version", Version, "files", len(args))
		rebuild := func(next config.Config, keys map[string]string, forgotten []string) (*ai.Completer, error) {
			for _, endpoint := range forgotten {
				if err := keyring.Delete(aiKeyringService, endpoint); err != nil && !errors.Is(err, keyring.ErrNotFound) {
					return nil, fmt.Errorf("delete API key: %w", err)
				}
			}
			for endpoint, key := range keys {
				if err := keyring.Set(aiKeyringService, endpoint, key); err != nil {
					return nil, fmt.Errorf("store API key: %w", err)
				}
			}
			providers, err := ai.NewProviders(next.AI.Chain, lookupAIKey)
			if err != nil || len(providers) == 0 {
				return nil, err
			}
			return ai.NewCompleter(providers), nil
		}
		testModel := func(ctx context.Context, model config.Model, enteredKey string) error {
			providers, err := ai.NewProviders([]config.Model{model}, func(provider, endpoint string) string {
				if enteredKey != "" {
					return enteredKey
				}
				return lookupAIKey(provider, endpoint)
			})
			if err != nil {
				return err
			}
			return providers[0].Healthy(ctx)
		}
		return ui.Run(ui.Options{
			Version: Version, ConfigPath: configPath, Config: cfg, Paths: paths, Completer: completer,
			RebuildCompleter: rebuild, TestModel: testModel,
		}, expandHome(args))
	},
}

// expandHome replaces a leading "~" the shell left alone, as in `e '~'` or a
// launcher that passes the text through. Otherwise E would open a new file
// named "~" in the working directory.
func expandHome(args []string) []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return args
	}
	result := make([]string, len(args))
	for i, arg := range args {
		switch {
		case arg == "~":
			arg = home
		case strings.HasPrefix(arg, "~/"):
			arg = filepath.Join(home, arg[2:])
		}
		result[i] = arg
	}
	return result
}

func lookupAIKey(provider, endpoint string) string {
	if key := os.Getenv("E_AI_KEY"); key != "" {
		return key
	}
	if key, err := keyring.Get(aiKeyringService, endpoint); err == nil && key != "" {
		return key
	}
	if key, err := keyring.Get("mailbox-ai", endpoint); err == nil && key != "" {
		return key
	}
	if key, err := keyring.Get("mailbox-ai", provider); err == nil {
		return key
	}
	return ""
}

func init() {
	rootCmd.SetVersionTemplate("{{.Version}}\n")
	// Declaring the flag stops cobra from adding its -v alias.
	rootCmd.Flags().Bool("version", false, "Print the version and exit.")
	flags := rootCmd.PersistentFlags()
	flags.BoolVarP(&flagDebug, "debug", "d", false, "Write debug logs to stderr.")
	flags.BoolVar(&flagTrace, "trace", false, "Write trace logs to "+traceLogPath+".")
	flags.StringVarP(&flagConfig, "config", "c", "", "Path to the config file.")
}
