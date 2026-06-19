// Package discover finds Elgato Key Lights on the LAN via mDNS.
package discover

import (
	"context"
	"net"
	"sort"
	"strconv"
	"time"

	"github.com/brutella/dnssd"
)

// ServiceType is the DNS-SD service Elgato Wi-Fi lights advertise.
const ServiceType = "_elg._tcp.local."

// Light is a discovered device: its mDNS instance name and address.
type Light struct {
	Name     string
	HostPort string // "host:port"
}

// entryToLight converts a browse entry to a Light, choosing the first usable
// (global) IPv4 address. Returns ok=false if none is found.
func entryToLight(e dnssd.BrowseEntry) (Light, bool) {
	for _, ip := range e.IPs {
		v4 := ip.To4()
		if v4 == nil || v4.IsLinkLocalUnicast() {
			continue
		}
		return Light{
			Name:     e.Name,
			HostPort: net.JoinHostPort(v4.String(), strconv.Itoa(e.Port)),
		}, true
	}
	return Light{}, false
}

// Browse performs a single mDNS browse for up to `window`, returning all
// distinct lights found (deduplicated by mDNS instance name).
func Browse(ctx context.Context, window time.Duration) ([]Light, error) {
	ctx, cancel := context.WithTimeout(ctx, window)
	defer cancel()

	found := map[string]Light{}
	add := func(e dnssd.BrowseEntry) {
		if l, ok := entryToLight(e); ok {
			found[l.Name] = l
		}
	}
	rmv := func(e dnssd.BrowseEntry) {}

	err := dnssd.LookupType(ctx, ServiceType, add, rmv)
	if err != nil && ctx.Err() == nil {
		// A real error, not just the timeout we imposed.
		return nil, err
	}

	out := make([]Light, 0, len(found))
	for _, l := range found {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// BrowseUntilFound browses repeatedly (each attempt up to `window`) until at
// least one light is found or ctx is cancelled. Between empty attempts it
// waits `backoff`.
func BrowseUntilFound(ctx context.Context, window, backoff time.Duration) ([]Light, error) {
	for {
		lights, err := Browse(ctx, window)
		if err != nil {
			return nil, err
		}
		if len(lights) > 0 {
			return lights, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
	}
}
