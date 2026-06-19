// Command keylight-hap exposes Elgato Key Lights to HomeKit.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	haplog "github.com/brutella/hap/log"

	"github.com/hughobrien/keylight-hap/internal/bridge"
	"github.com/hughobrien/keylight-hap/internal/discover"
	"github.com/hughobrien/keylight-hap/internal/elgato"
)

func main() {
	bridgeName := flag.String("bridge-name", envOr("KEYLIGHT_HAP_BRIDGE_NAME", "keylight-hap"), "HomeKit bridge name shown during pairing")
	port := flag.Int("port", envIntOr("KEYLIGHT_HAP_PORT", 0), "HAP TCP port (0 = OS-assigned)")
	pollInterval := flag.Duration("poll-interval", envDurOr("KEYLIGHT_HAP_POLL_INTERVAL", 20*time.Second), "state-sync poll interval")
	discoveryTimeout := flag.Duration("discovery-timeout", envDurOr("KEYLIGHT_HAP_DISCOVERY_TIMEOUT", 5*time.Second), "mDNS browse window per attempt")
	stateDir := flag.String("state-dir", envOr("KEYLIGHT_HAP_STATE_DIR", "/var/lib/keylight-hap"), "PIN + pairing storage directory")
	debug := flag.Bool("debug", envBoolOr("KEYLIGHT_HAP_DEBUG", false), "enable verbose HAP debug logging (logs raw protocol payloads)")
	flag.Parse()

	if *debug {
		haplog.Debug.Enable()
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	slog.Info("discovering Key Lights", "service", discover.ServiceType)
	lights, err := discover.BrowseUntilFound(ctx, *discoveryTimeout, 5*time.Second)
	if err != nil {
		slog.Error("discovery aborted", "err", err)
		os.Exit(1)
	}
	slog.Info("discovered lights", "count", len(lights))

	targets := make([]bridge.Target, 0, len(lights))
	for _, l := range lights {
		slog.Info("light", "name", l.Name, "addr", l.HostPort)
		targets = append(targets, bridge.Target{Name: l.Name, Client: elgato.New(l.HostPort)})
	}

	b, err := bridge.New(ctx, bridge.Options{
		StateDir:     *stateDir,
		BridgeName:   *bridgeName,
		Port:         *port,
		PollInterval: *pollInterval,
		Lights:       targets,
	})
	if err != nil {
		slog.Error("bridge setup failed", "err", err)
		os.Exit(1)
	}

	slog.Info("HomeKit PIN", "pin", bridge.FormatPin(b.Pin))

	if err := b.Run(ctx); err != nil && ctx.Err() == nil {
		slog.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envIntOr(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envDurOr(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func envBoolOr(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}
