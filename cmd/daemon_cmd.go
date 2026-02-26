package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/davebream/mcpl/internal/config"
	"github.com/davebream/mcpl/internal/daemon"
	"github.com/davebream/mcpl/internal/logging"
	"github.com/spf13/cobra"
)

var daemonForeground bool

var daemonCmd = &cobra.Command{
	Use:    "daemon",
	Short:  "Run the mcpl daemon (internal — started automatically)",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Set umask for secure file creation
		syscall.Umask(0077)

		// Ignore signals that Claude Code might send
		signal.Ignore(syscall.SIGINT, syscall.SIGHUP, syscall.SIGPIPE)

		cfgPath, err := config.ConfigFilePath()
		if err != nil {
			return err
		}

		cfg, err := config.Load(cfgPath)
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}

		socketPath, err := config.SocketPath()
		if err != nil {
			return err
		}

		logDir, err := config.LogDir()
		if err != nil {
			return err
		}
		if err := config.EnsureDir(logDir, 0700); err != nil {
			// Non-fatal: logging falls back to stderr
			fmt.Fprintf(os.Stderr, "mcpl: cannot create log directory: %v\n", err)
		}

		logger, logCleanup, logErr := logging.Setup(logDir, slog.LevelInfo, daemonForeground)
		if logErr != nil {
			// Non-fatal: fall back to stderr-only logging
			fmt.Fprintf(os.Stderr, "mcpl: cannot set up file logging: %v\n", logErr)
			logger = slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
				Level: slog.LevelInfo,
			}))
			logCleanup = func() {}
		}
		defer logCleanup()

		d, err := daemon.New(cfg, socketPath, logger)
		if err != nil {
			return err
		}

		// Write PID file
		pidPath, err := config.PIDFilePath()
		if err != nil {
			logger.Warn("cannot determine PID file path", "error", err)
		} else if err := config.AtomicWriteFile(pidPath, []byte(fmt.Sprintf("%d", os.Getpid())), 0600); err != nil {
			logger.Warn("failed to write PID file", "error", err)
		} else {
			defer os.Remove(pidPath)
		}

		// Handle SIGTERM for graceful shutdown
		ctx, cancel := context.WithCancel(context.Background())
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGTERM)
		go func() {
			<-sigCh
			logger.Info("received SIGTERM, shutting down")
			cancel()
		}()

		return d.Run(ctx)
	},
}

func init() {
	daemonCmd.Flags().BoolVar(&daemonForeground, "foreground", false, "Run in foreground")
	rootCmd.AddCommand(daemonCmd)
}
