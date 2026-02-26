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
		config.EnsureDir(logDir, 0700)

		// TODO: set up file-based logger
		logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		}))

		d, err := daemon.New(cfg, socketPath, logger)
		if err != nil {
			return err
		}

		// Write PID file
		pidPath, _ := config.PIDFilePath()
		config.AtomicWriteFile(pidPath, []byte(fmt.Sprintf("%d", os.Getpid())), 0600)
		defer os.Remove(pidPath)

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
