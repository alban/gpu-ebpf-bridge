// SPDX-License-Identifier: Apache-2.0

// Command gpu-ebpf-bridge polls GPU telemetry (NVML in v1) and
// publishes it through bpffs-pinned BPF maps. See README.md for the
// data flow and the API contract.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/cilium/ebpf/rlimit"

	"github.com/alban/gpu-ebpf-bridge/internal/maps"
	"github.com/alban/gpu-ebpf-bridge/internal/nvml"
	"github.com/alban/gpu-ebpf-bridge/internal/poller"
)

type options struct {
	mode         string
	pollInterval time.Duration
	pinDir       string
	keepPins     bool
	logLevel     string
	showVersion  bool
	dump         bool
}

func parseFlags(args []string) (options, error) {
	fs := flag.NewFlagSet("gpu-ebpf-bridge", flag.ContinueOnError)
	var opt options
	fs.StringVar(&opt.mode, "mode", "auto",
		"Telemetry source: auto (try real, fall back to mock), real, mock")
	fs.DurationVar(&opt.pollInterval, "poll-interval", 100*time.Millisecond,
		"How often to poll the telemetry source")
	fs.StringVar(&opt.pinDir, "pin-dir", maps.DefaultPinDir,
		"bpffs directory under which to pin the maps")
	fs.BoolVar(&opt.keepPins, "keep-pins", false,
		"On clean shutdown, leave the pinned maps in place (default: unpin)")
	fs.StringVar(&opt.logLevel, "log-level", "info",
		"Logger level: debug, info, warn, error")
	fs.BoolVar(&opt.showVersion, "version", false, "Print version and exit")
	fs.BoolVar(&opt.dump, "dump", false,
		"Read the bpffs-pinned bridge maps and print their contents, then exit. "+
			"Useful as a bpftool-free debugging aid; does not start the poller.")
	if err := fs.Parse(args); err != nil {
		return options{}, err
	}
	switch opt.mode {
	case "auto", "real", "mock":
	default:
		return options{}, fmt.Errorf("invalid --mode=%q (want: auto, real, mock)", opt.mode)
	}
	return opt, nil
}

func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})
	return slog.New(h)
}

func version() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "(unknown)"
}

// chooseSource resolves --mode to a concrete nvml.Poller. For "auto"
// it tries the real backend first and falls back to the mock if NVML
// is unavailable on this host (typical for non-GPU dev machines).
func chooseSource(ctx context.Context, mode string, logger *slog.Logger) (nvml.Poller, string, error) {
	tryReal := func() (nvml.Poller, error) {
		p, err := newRealPoller()
		if err != nil {
			return nil, err
		}
		if err := p.Init(ctx); err != nil {
			_ = p.Close()
			return nil, err
		}
		return p, nil
	}

	switch mode {
	case "real":
		p, err := tryReal()
		if err != nil {
			return nil, "", fmt.Errorf("real NVML backend requested but unavailable: %w", err)
		}
		return p, "real", nil
	case "mock":
		p := nvml.NewMock()
		if err := p.Init(ctx); err != nil {
			return nil, "", err
		}
		return p, "mock", nil
	case "auto":
		if p, err := tryReal(); err == nil {
			return p, "real", nil
		} else {
			logger.Info("NVML unavailable, falling back to mock backend", "err", err)
			p := nvml.NewMock()
			if err := p.Init(ctx); err != nil {
				return nil, "", err
			}
			return p, "mock", nil
		}
	default:
		return nil, "", fmt.Errorf("unhandled --mode=%q", mode)
	}
}

func run(args []string) error {
	opt, err := parseFlags(args)
	if err != nil {
		return err
	}
	if opt.showVersion {
		fmt.Printf("gpu-ebpf-bridge %s\n", version())
		return nil
	}
	if opt.dump {
		return runDump(opt.pinDir)
	}

	logger := newLogger(opt.logLevel)
	slog.SetDefault(logger)

	if err := rlimit.RemoveMemlock(); err != nil {
		return fmt.Errorf("RemoveMemlock: %w", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	source, sourceName, err := chooseSource(ctx, opt.mode, logger)
	if err != nil {
		return err
	}

	bridge, err := maps.Open(opt.pinDir)
	if err != nil {
		_ = source.Close()
		return fmt.Errorf("opening bridge maps in %s: %w", opt.pinDir, err)
	}

	logger.Info("gpu-ebpf-bridge started",
		"version", version(),
		"source", sourceName,
		"pin-dir", opt.pinDir,
		"poll-interval", opt.pollInterval,
		"pid", os.Getpid())

	p, err := poller.New(poller.Config{
		PollInterval: opt.pollInterval,
		Source:       source,
		Bridge:       bridge,
		Logger:       logger,
	})
	if err != nil {
		_ = bridge.Close()
		_ = source.Close()
		return err
	}

	runErr := p.Run(ctx)

	// Clean shutdown path. Close releases FDs; Unpin removes bpffs
	// entries unless the operator asked us to keep them (e.g. they
	// want consumer gadgets to keep reading the last known values
	// across a bridge restart).
	logger.Info("shutting down")
	if cerr := bridge.Close(); cerr != nil {
		logger.Warn("bridge Close failed", "err", cerr)
	}
	if !opt.keepPins {
		if uerr := bridge.Unpin(); uerr != nil {
			logger.Warn("bridge Unpin failed", "err", uerr)
		} else {
			logger.Info("removed pinned maps", "pin-dir", opt.pinDir)
		}
	} else {
		logger.Info("leaving pinned maps in place (--keep-pins)", "pin-dir", opt.pinDir)
	}
	return runErr
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
