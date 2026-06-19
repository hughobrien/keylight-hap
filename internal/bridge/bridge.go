package bridge

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/brutella/hap"
	"github.com/brutella/hap/accessory"

	"github.com/hughobrien/keylight-hap/internal/elgato"
)

// Target is a discovered light to expose: a display-name hint and its client.
type Target struct {
	Name   string
	Client *elgato.Client
}

// Options configures the bridge.
type Options struct {
	StateDir     string
	BridgeName   string
	Port         int
	PollInterval time.Duration
	Lights       []Target
}

// Bridge holds the assembled HAP server and the light accessories it polls.
type Bridge struct {
	Server *hap.Server
	Pin    string

	lights []*lightAccessory
	poll   time.Duration
}

// New queries each target for its info + state, builds one accessory per
// light under a bridge, loads/generates the PIN, and constructs the server.
func New(ctx context.Context, opts Options) (*Bridge, error) {
	if len(opts.Lights) == 0 {
		return nil, fmt.Errorf("bridge: no lights")
	}

	b := &Bridge{poll: opts.PollInterval}

	var children []*accessory.A
	for _, t := range opts.Lights {
		info, err := t.Client.Info(ctx)
		if err != nil {
			return nil, fmt.Errorf("bridge: info for %s: %w", t.Name, err)
		}
		st, err := t.Client.Get(ctx)
		if err != nil {
			return nil, fmt.Errorf("bridge: state for %s: %w", t.Name, err)
		}
		la := newLightAccessory(ctx, accessoryName(info, t.Name), info, st, t.Client)
		b.lights = append(b.lights, la)
		children = append(children, la.a.A)
	}

	bridgeAcc := accessory.NewBridge(accessory.Info{
		Name:         opts.BridgeName,
		Manufacturer: "keylight-hap",
		Model:        "keylight-hap",
	})

	store := hap.NewFsStore(opts.StateDir)
	server, err := hap.NewServer(store, bridgeAcc.A, children...)
	if err != nil {
		return nil, fmt.Errorf("bridge: new server: %w", err)
	}

	pin, err := loadOrGeneratePin(opts.StateDir)
	if err != nil {
		return nil, fmt.Errorf("bridge: pin: %w", err)
	}
	server.Pin = pin
	server.Addr = ":" + strconv.Itoa(opts.Port)

	b.Server = server
	b.Pin = pin
	return b, nil
}

// accessoryName picks a HomeKit name: device displayName, else the mDNS name.
func accessoryName(info elgato.Info, mdnsName string) string {
	if info.DisplayName != "" {
		return info.DisplayName
	}
	if mdnsName != "" {
		return mdnsName
	}
	return info.ProductName
}

// Run starts the poll loops and serves until ctx is cancelled.
func (b *Bridge) Run(ctx context.Context) error {
	for i := range b.lights {
		go b.pollLoop(ctx, b.lights[i])
	}
	return b.Server.ListenAndServe(ctx)
}

func (b *Bridge) pollLoop(ctx context.Context, la *lightAccessory) {
	ticker := time.NewTicker(b.poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			st, err := la.client.Get(ctx)
			if err != nil {
				slog.Warn("poll failed", "err", err)
				continue
			}
			la.sync(st)
		}
	}
}
