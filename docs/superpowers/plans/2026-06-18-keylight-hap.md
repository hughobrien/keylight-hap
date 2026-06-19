# keylight-hap Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Go daemon that discovers Elgato Key Lights via mDNS at startup and exposes each as a HomeKit Lightbulb (On / Brightness / Color Temperature), packaged as a Nix flake with a NixOS module.

**Architecture:** `cmd/keylight-hap` wires three internal packages: `elgato` (HTTP client for the light), `discover` (mDNS browse of `_elg._tcp`), and `bridge` (brutella/hap accessory wiring + a poll loop that keeps HomeKit in sync with out-of-band changes). The light set is a snapshot taken at startup. The HomeKit ↔ Elgato mapping is 1:1 (brightness direct; `temperature` is mireds, same as HomeKit `ColorTemperature`, clamped to the device's 143–344 range — hardware-verified).

**Tech Stack:** Go 1.26, `github.com/brutella/hap` v0.0.35, `github.com/brutella/dnssd` v1.2.14 (both confirmed latest as of 2026-06-18), Nix flake (`buildGoModule`), NixOS module.

**Reference material:**
- Spec: `docs/superpowers/specs/2026-06-18-keylight-hap-design.md` (read for context, especially the hardware-verified mapping table).
- `../breezyd` is a sibling project using the same `brutella/hap` library and an analogous Nix flake + NixOS module. Useful references: `../breezyd/cmd/breezyd/homekit.go` (PIN generation, FsStore, server setup), `../breezyd/nix/module.nix` and `../breezyd/flake.nix` (module + flake patterns, systemd hardening — note the `AF_NETLINK` requirement). **Follow its patterns; do not copy its breezy-specific options (TOML config, nginx, prometheus).**

**Key brutella/hap API facts (verified against the source):**
- `accessory.NewLightbulb(accessory.Info{Name, Manufacturer, Model, SerialNumber, Firmware}) *accessory.Lightbulb`. The returned accessory exposes `.Lightbulb.On` (a `*characteristic.On`, which embeds `*Bool`) and `.A` (the underlying `*accessory.A`).
- Brightness/ColorTemperature are NOT on the Lightbulb by default; add them: `c := characteristic.NewBrightness(); lb.Lightbulb.AddC(c.C)`. Same for `characteristic.NewColorTemperature()`.
- `characteristic.NewColorTemperature()` defaults to min 140 / max 500 / value 140. Call `c.SetMinValue(143); c.SetMaxValue(344)` to match the device.
- `*Bool` (On): `SetValue(bool)`, `Value() bool`, `OnValueRemoteUpdate(func(v bool))`.
- `*Int` (Brightness, ColorTemperature): `SetValue(int) error`, `Value() int`, `SetMinValue/SetMaxValue(int)`, `OnValueRemoteUpdate(func(v int))`.
- `OnValueRemoteUpdate` fires only on writes from a paired controller (non-nil `*http.Request`); local `SetValue` calls do NOT re-trigger it — so the poll loop won't feed back into PUTs.
- Identify: set `lb.A.IdentifyFunc = func(*http.Request){ ... }`.
- Server: `hap.NewServer(store hap.Store, a *accessory.A, as ...*accessory.A) (*hap.Server, error)`. Set `server.Pin` (8-digit string) and `server.Addr` (e.g. `":0"` or `":21063"`), then `server.ListenAndServe(ctx) error`.
- Store: `hap.NewFsStore(dir) hap.Store`. Weak-PIN set: `hap.InvalidPins` (map keyed by pin string).
- Bridge accessory: `accessory.NewBridge(accessory.Info{...}) *accessory.Bridge` with `.A`.

**Elgato API facts (hardware-verified):**
- `GET /elgato/lights` → `{"numberOfLights":1,"lights":[{"on":0|1,"brightness":0-100,"temperature":143-344}]}`.
- `PUT /elgato/lights` accepts partial light objects (omit fields to leave unchanged) and echoes the full resulting state.
- `temperature` is mireds: 143 = 7000K (cool), 344 = 2900K (warm). Brightness API floor is 1 (visually off at 1; no special-casing).
- `GET /elgato/accessory-info` → `{"productName","serialNumber","firmwareVersion","displayName",...}` (`displayName` may be `""`).
- `POST /elgato/identify` → flashes the light.
- mDNS service type: `_elg._tcp.local.`, port 9123.

---

## File Structure

```
keylight-hap/
├── go.mod                          # module github.com/hughobrien/keylight-hap
├── go.sum
├── LICENSE                         # GPL-3.0-or-later
├── flake.nix                       # buildGoModule + nixosModules.default
├── nix/module.nix                  # services.keylight-hap
├── cmd/keylight-hap/main.go        # flags/env, lifecycle, wiring
├── internal/elgato/
│   ├── client.go                   # State/Info/Patch types + Client (Get/Set/Info/Identify)
│   ├── fakedevice.go               # httptest device emulator for tests
│   └── client_test.go
├── internal/discover/
│   ├── discover.go                 # Browse / BrowseUntilFound, entryToLight helper
│   └── discover_test.go
└── internal/bridge/
    ├── pin.go                      # loadOrGeneratePin, formatPinDisplay
    ├── pin_test.go
    ├── lightbulb.go                # per-light accessory construction + callbacks + sync
    ├── bridge.go                   # bridge/server assembly + poll loop (Run)
    └── bridge_test.go
```

---

## Task 1: Project foundation (module, deps, license, skeleton)

**Files:**
- Create: `go.mod`, `LICENSE`, `.gitignore` (already exists — leave it)
- Create placeholder dirs via the first real files in later tasks.

- [ ] **Step 1: Initialize the module**

Run from the repo root (`/Users/hugh/src/keylight-hap`):

```bash
go mod init github.com/hughobrien/keylight-hap
```

Then edit `go.mod` so the Go directive reads `go 1.26`.

- [ ] **Step 2: Add the two direct dependencies at their latest versions**

```bash
go get github.com/brutella/hap@v0.0.35
go get github.com/brutella/dnssd@v1.2.14
go mod tidy
```

Expected: `go.mod` lists `github.com/brutella/hap v0.0.35` and `github.com/brutella/dnssd v1.2.14` as direct requires; `go.sum` populated. (These are the latest tags as of 2026-06-18; if `go list -m -u all` later shows newer ones, that's the dependency-currency follow-up — note it, don't silently bump.)

- [ ] **Step 3: Add the license**

Create `LICENSE` containing the full GPL-3.0-or-later text (same license as `../breezyd/LICENSE` — copy it verbatim):

```bash
cp ../breezyd/LICENSE ./LICENSE
```

- [ ] **Step 4: Verify the module builds (no packages yet)**

Run: `go build ./...`
Expected: succeeds with no output (no packages to build yet is fine; if it errors with "no Go files", that's acceptable at this stage).

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum LICENSE
git commit -m "Scaffold Go module with hap + dnssd deps"
```

---

## Task 2: `internal/elgato` — device client

**Files:**
- Create: `internal/elgato/client.go`
- Create: `internal/elgato/fakedevice.go`
- Test: `internal/elgato/client_test.go`

This package owns all knowledge of the Elgato HTTP API. Types are HomeKit-friendly (bool `On`, percent `Brightness`, mired `Temperature`) so the bridge layer needs no conversion.

- [ ] **Step 1: Write `fakedevice.go` (test helper, but it is production-quality emulation)**

```go
package elgato

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
)

// FakeDevice is an in-memory emulation of a Key Light's HTTP API for tests.
// It implements partial-PUT merge semantics and clamps like the real device.
type FakeDevice struct {
	*httptest.Server

	mu           sync.Mutex
	on           int
	brightness   int
	temperature  int
	identifyCount int
	info         Info
}

// NewFakeDevice starts a fake device server. Caller must Close it.
func NewFakeDevice() *FakeDevice {
	d := &FakeDevice{
		on:          1,
		brightness:  20,
		temperature: 213,
		info: Info{
			ProductName:     "Elgato Key Light",
			SerialNumber:    "FAKE0000",
			FirmwareVersion: "1.0.3",
			DisplayName:     "Fake Light",
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/elgato/lights", d.handleLights)
	mux.HandleFunc("/elgato/accessory-info", d.handleInfo)
	mux.HandleFunc("/elgato/identify", d.handleIdentify)
	d.Server = httptest.NewServer(mux)
	return d
}

// HostPort returns "host:port" for the fake server (no scheme).
func (d *FakeDevice) HostPort() string {
	// httptest URL is like http://127.0.0.1:NNNNN
	return d.Server.URL[len("http://"):]
}

func (d *FakeDevice) IdentifyCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.identifyCount
}

func (d *FakeDevice) handleLights(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if r.Method == http.MethodPut {
		var body wireLights
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if len(body.Lights) > 0 {
			l := body.Lights[0]
			if l.On != nil {
				d.on = *l.On
			}
			if l.Brightness != nil {
				d.brightness = clampBrightness(*l.Brightness)
			}
			if l.Temperature != nil {
				d.temperature = clampTemperature(*l.Temperature)
			}
		}
	}
	on, b, t := d.on, d.brightness, d.temperature
	json.NewEncoder(w).Encode(wireLights{
		NumberOfLights: 1,
		Lights:         []wireLight{{On: &on, Brightness: &b, Temperature: &t}},
	})
}

func (d *FakeDevice) handleInfo(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	json.NewEncoder(w).Encode(d.info)
}

func (d *FakeDevice) handleIdentify(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	d.identifyCount++
	d.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}
```

- [ ] **Step 2: Write the failing test `client_test.go`**

```go
package elgato

import (
	"context"
	"testing"
)

func TestGet(t *testing.T) {
	d := NewFakeDevice()
	defer d.Close()
	c := New(d.HostPort())

	st, err := c.Get(context.Background())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !st.On || st.Brightness != 20 || st.Temperature != 213 {
		t.Fatalf("unexpected state: %+v", st)
	}
}

func TestSetPartialAndClamp(t *testing.T) {
	d := NewFakeDevice()
	defer d.Close()
	c := New(d.HostPort())

	// Set only brightness; temperature/on must be unchanged (partial PUT).
	b := 55
	st, err := c.Set(context.Background(), Patch{Brightness: &b})
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if st.Brightness != 55 || st.Temperature != 213 {
		t.Fatalf("partial PUT failed: %+v", st)
	}

	// Out-of-range temperature must be clamped to 344.
	tt := 9000
	st, err = c.Set(context.Background(), Patch{Temperature: &tt})
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if st.Temperature != 344 {
		t.Fatalf("temperature not clamped: %+v", st)
	}
}

func TestInfoAndIdentify(t *testing.T) {
	d := NewFakeDevice()
	defer d.Close()
	c := New(d.HostPort())

	info, err := c.Info(context.Background())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.SerialNumber != "FAKE0000" || info.DisplayName != "Fake Light" {
		t.Fatalf("unexpected info: %+v", info)
	}

	if err := c.Identify(context.Background()); err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if d.IdentifyCount() != 1 {
		t.Fatalf("identify not called")
	}
}
```

- [ ] **Step 3: Run the test, expect failure**

Run: `go test ./internal/elgato/ -run Test -v`
Expected: compile failure (`New`, `Client`, `State`, etc. undefined).

- [ ] **Step 4: Write `client.go`**

```go
// Package elgato is an HTTP client for the Elgato Key Light local API.
package elgato

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Mired bounds supported by the device (143 = 7000K cool, 344 = 2900K warm).
const (
	TempMin = 143
	TempMax = 344
)

// State is the HomeKit-friendly view of a light's current values.
type State struct {
	On          bool
	Brightness  int // 0-100 percent
	Temperature int // 143-344 mireds
}

// Patch is a partial update; nil fields are left unchanged on the device.
type Patch struct {
	On          *bool
	Brightness  *int
	Temperature *int
}

// Info is device metadata from /elgato/accessory-info.
type Info struct {
	ProductName     string `json:"productName"`
	SerialNumber    string `json:"serialNumber"`
	FirmwareVersion string `json:"firmwareVersion"`
	DisplayName     string `json:"displayName"`
}

// wire types mirror the device JSON exactly.
type wireLight struct {
	On          *int `json:"on,omitempty"`
	Brightness  *int `json:"brightness,omitempty"`
	Temperature *int `json:"temperature,omitempty"`
}

type wireLights struct {
	NumberOfLights int         `json:"numberOfLights,omitempty"`
	Lights         []wireLight `json:"lights"`
}

// Client talks to one Key Light at host:port.
type Client struct {
	base string
	hc   *http.Client
}

// New returns a client for a light addressed as "host:port" (no scheme).
func New(hostPort string) *Client {
	return &Client{
		base: "http://" + hostPort,
		hc:   &http.Client{Timeout: 4 * time.Second},
	}
}

func clampBrightness(v int) int {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func clampTemperature(v int) int {
	if v < TempMin {
		return TempMin
	}
	if v > TempMax {
		return TempMax
	}
	return v
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Get returns the first light's current state.
func (c *Client) Get(ctx context.Context) (State, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/elgato/lights", nil)
	if err != nil {
		return State{}, err
	}
	var out wireLights
	if err := c.do(req, &out); err != nil {
		return State{}, err
	}
	if len(out.Lights) == 0 {
		return State{}, fmt.Errorf("elgato: no lights in response")
	}
	return fromWire(out.Lights[0]), nil
}

// Set applies a partial update and returns the resulting state (the device
// echoes full state in its PUT response).
func (c *Client) Set(ctx context.Context, p Patch) (State, error) {
	var l wireLight
	if p.On != nil {
		v := b2i(*p.On)
		l.On = &v
	}
	if p.Brightness != nil {
		v := clampBrightness(*p.Brightness)
		l.Brightness = &v
	}
	if p.Temperature != nil {
		v := clampTemperature(*p.Temperature)
		l.Temperature = &v
	}
	payload, err := json.Marshal(wireLights{NumberOfLights: 1, Lights: []wireLight{l}})
	if err != nil {
		return State{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.base+"/elgato/lights", bytes.NewReader(payload))
	if err != nil {
		return State{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	var out wireLights
	if err := c.do(req, &out); err != nil {
		return State{}, err
	}
	if len(out.Lights) == 0 {
		return State{}, fmt.Errorf("elgato: no lights in PUT response")
	}
	return fromWire(out.Lights[0]), nil
}

// Info returns device metadata.
func (c *Client) Info(ctx context.Context) (Info, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/elgato/accessory-info", nil)
	if err != nil {
		return Info{}, err
	}
	var out Info
	if err := c.do(req, &out); err != nil {
		return Info{}, err
	}
	return out, nil
}

// Identify flashes the light.
func (c *Client) Identify(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/elgato/identify", nil)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

func (c *Client) do(req *http.Request, out interface{}) error {
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("elgato: %s %s -> %s", req.Method, req.URL.Path, resp.Status)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func fromWire(l wireLight) State {
	s := State{}
	if l.On != nil {
		s.On = *l.On != 0
	}
	if l.Brightness != nil {
		s.Brightness = *l.Brightness
	}
	if l.Temperature != nil {
		s.Temperature = *l.Temperature
	}
	return s
}
```

- [ ] **Step 5: Run tests, expect pass**

Run: `go test ./internal/elgato/ -v`
Expected: `TestGet`, `TestSetPartialAndClamp`, `TestInfoAndIdentify` all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/elgato
git commit -m "Add elgato HTTP client with fake device and tests"
```

---

## Task 3: `internal/discover` — mDNS discovery

**Files:**
- Create: `internal/discover/discover.go`
- Test: `internal/discover/discover_test.go`

mDNS itself can't be unit-tested without a network, so the testable seam is `entryToLight`, which converts a `dnssd.BrowseEntry` to our `Light`. `Browse` and `BrowseUntilFound` are thin wrappers around `dnssd.LookupType`.

- [ ] **Step 1: Write the failing test `discover_test.go`**

```go
package discover

import (
	"net"
	"testing"

	"github.com/brutella/dnssd"
)

func TestEntryToLight_PicksIPv4(t *testing.T) {
	e := dnssd.BrowseEntry{
		Name: "Elgato Key Light AE8A",
		Port: 9123,
		IPs:  []net.IP{net.ParseIP("fe80::1"), net.ParseIP("192.168.1.31")},
	}
	l, ok := entryToLight(e)
	if !ok {
		t.Fatal("expected ok")
	}
	if l.Name != "Elgato Key Light AE8A" {
		t.Fatalf("name: %q", l.Name)
	}
	if l.HostPort != "192.168.1.31:9123" {
		t.Fatalf("hostport: %q", l.HostPort)
	}
}

func TestEntryToLight_NoUsableIP(t *testing.T) {
	e := dnssd.BrowseEntry{Name: "x", Port: 9123, IPs: []net.IP{net.ParseIP("fe80::1")}}
	if _, ok := entryToLight(e); ok {
		t.Fatal("expected not ok when only link-local IPv6 present")
	}
}
```

- [ ] **Step 2: Run the test, expect failure**

Run: `go test ./internal/discover/ -v`
Expected: compile failure (`entryToLight`, `Light` undefined).

- [ ] **Step 3: Write `discover.go`**

```go
// Package discover finds Elgato Key Lights on the LAN via mDNS.
package discover

import (
	"context"
	"net"
	"sort"
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
			HostPort: net.JoinHostPort(v4.String(), itoa(e.Port)),
		}, true
	}
	return Light{}, false
}

func itoa(p int) string {
	// small, allocation-free enough; strconv would be fine too
	return netPort(p)
}

func netPort(p int) string {
	return (&net.TCPAddr{Port: p}).String()[1:] // ":9123" -> "9123"
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
```

Note: replace the `itoa`/`netPort` hack with `strconv.Itoa(e.Port)` and `import "strconv"` — it is cleaner and the code-quality reviewer will expect it. (Written this way only to flag the choice; the implementer should use `strconv.Itoa`.)

- [ ] **Step 4: Use `strconv.Itoa` for the port**

Replace the `itoa`/`netPort` helpers with a direct `strconv.Itoa(e.Port)` call inside `entryToLight`, and add `"strconv"` to imports (remove now-unused helpers).

- [ ] **Step 5: Run tests, expect pass**

Run: `go test ./internal/discover/ -v`
Expected: `TestEntryToLight_PicksIPv4` and `TestEntryToLight_NoUsableIP` PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/discover
git commit -m "Add mDNS discovery for Elgato lights"
```

---

## Task 4: `internal/bridge` — PIN handling

**Files:**
- Create: `internal/bridge/pin.go`
- Test: `internal/bridge/pin_test.go`

Adapted from `../breezyd/cmd/breezyd/homekit.go`. Reuses `hap.InvalidPins` rather than maintaining a local weak-PIN list.

- [ ] **Step 1: Write the failing test `pin_test.go`**

```go
package bridge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/brutella/hap"
)

func TestLoadOrGeneratePin_PersistsAndStrong(t *testing.T) {
	dir := t.TempDir()
	p1, err := loadOrGeneratePin(dir)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(p1) != 8 {
		t.Fatalf("pin length %d", len(p1))
	}
	if _, weak := hap.InvalidPins[p1]; weak {
		t.Fatalf("generated a weak pin: %s", p1)
	}
	// Second call must return the same persisted pin.
	p2, err := loadOrGeneratePin(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if p1 != p2 {
		t.Fatalf("pin not persisted: %s != %s", p1, p2)
	}
}

func TestPinFileMode(t *testing.T) {
	dir := t.TempDir()
	if _, err := loadOrGeneratePin(dir); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "pin.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("pin file mode %o", fi.Mode().Perm())
	}
}

func TestFormatPinDisplay(t *testing.T) {
	if got := formatPinDisplay("12345678"); got != "1234-5678" {
		t.Fatalf("got %q", got)
	}
}
```

- [ ] **Step 2: Run the test, expect failure**

Run: `go test ./internal/bridge/ -run TestPin -v` and `... -run TestLoadOrGenerate -v`
Expected: compile failure (`loadOrGeneratePin`, `formatPinDisplay` undefined).

- [ ] **Step 3: Write `pin.go`**

```go
package bridge

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"github.com/brutella/hap"
)

const pinFile = "pin.txt"

// loadOrGeneratePin returns the persisted 8-digit HomeKit PIN from
// stateDir/pin.txt, generating and saving a strong one if absent or invalid.
func loadOrGeneratePin(stateDir string) (string, error) {
	path := filepath.Join(stateDir, pinFile)
	if data, err := os.ReadFile(path); err == nil {
		pin := strings.TrimSpace(string(data))
		if validPin(pin) {
			return pin, nil
		}
	}
	pin, err := generatePin()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(pin+"\n"), 0o600); err != nil {
		return "", err
	}
	return pin, nil
}

func validPin(pin string) bool {
	if len(pin) != 8 {
		return false
	}
	for _, r := range pin {
		if r < '0' || r > '9' {
			return false
		}
	}
	_, weak := hap.InvalidPins[pin]
	return !weak
}

func generatePin() (string, error) {
	for {
		var sb strings.Builder
		for i := 0; i < 8; i++ {
			n, err := rand.Int(rand.Reader, big.NewInt(10))
			if err != nil {
				return "", err
			}
			sb.WriteByte(byte('0' + n.Int64()))
		}
		pin := sb.String()
		if _, weak := hap.InvalidPins[pin]; !weak {
			return pin, nil
		}
	}
}

// formatPinDisplay renders an 8-digit pin as "XXXX-XXXX" for logging.
func formatPinDisplay(pin string) string {
	if len(pin) != 8 {
		return pin
	}
	return fmt.Sprintf("%s-%s", pin[:4], pin[4:])
}
```

- [ ] **Step 4: Run tests, expect pass**

Run: `go test ./internal/bridge/ -run 'TestPin|TestLoadOrGenerate|TestFormatPin' -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bridge/pin.go internal/bridge/pin_test.go
git commit -m "Add HomeKit PIN generation and persistence"
```

---

## Task 5: `internal/bridge` — light accessory

**Files:**
- Create: `internal/bridge/lightbulb.go`
- Test: extend `internal/bridge/bridge_test.go` (created here)

Each light becomes one `accessory.Lightbulb` with On + Brightness + range-clamped ColorTemperature, write callbacks that PUT to the device, an Identify hook, and a `sync` method the poll loop uses.

- [ ] **Step 1: Write the failing test `bridge_test.go`**

```go
package bridge

import (
	"context"
	"testing"

	"github.com/hughobrien/keylight-hap/internal/elgato"
)

func TestLightAccessory_WriteAndSync(t *testing.T) {
	d := elgato.NewFakeDevice()
	defer d.Close()
	c := elgato.New(d.HostPort())
	ctx := context.Background()

	la := newLightAccessory(ctx, "Desk", elgato.Info{ProductName: "Elgato Key Light", SerialNumber: "S1"}, elgato.State{On: true, Brightness: 20, Temperature: 213}, c)

	// A HomeKit write to Brightness must PUT to the device.
	la.bright.SetValue(70) // local set
	la.bright.OnValueRemoteUpdate // sanity: field exists (compile check only)
	la.onBrightnessWrite(70)
	st, err := c.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Brightness != 70 {
		t.Fatalf("device brightness = %d, want 70", st.Brightness)
	}

	// sync() must reflect an out-of-band device change onto the characteristics.
	if _, err := c.Set(ctx, elgato.Patch{Temperature: ptr(300)}); err != nil {
		t.Fatal(err)
	}
	la.sync(elgato.State{On: true, Brightness: 70, Temperature: 300})
	if la.ct.Value() != 300 {
		t.Fatalf("ct value = %d, want 300", la.ct.Value())
	}
}

func ptr[T any](v T) *T { return &v }
```

Note: the line `la.bright.OnValueRemoteUpdate` above is illustrative pseudo-code — DELETE it; the real wiring registers callbacks inside `newLightAccessory`. The test should instead exercise the registered callback indirectly. Use this corrected test body:

```go
func TestLightAccessory_WriteAndSync(t *testing.T) {
	d := elgato.NewFakeDevice()
	defer d.Close()
	c := elgato.New(d.HostPort())
	ctx := context.Background()

	la := newLightAccessory(ctx, "Desk",
		elgato.Info{ProductName: "Elgato Key Light", SerialNumber: "S1"},
		elgato.State{On: true, Brightness: 20, Temperature: 213}, c)

	// Invoke the brightness write handler as a remote write would.
	la.onBrightnessWrite(70)
	st, err := c.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Brightness != 70 {
		t.Fatalf("device brightness = %d, want 70", st.Brightness)
	}

	// sync() reflects an out-of-band device change onto the characteristics.
	la.sync(elgato.State{On: true, Brightness: 70, Temperature: 300})
	if la.ct.Value() != 300 {
		t.Fatalf("ct value = %d, want 300", la.ct.Value())
	}
}

func ptr[T any](v T) *T { return &v }
```

- [ ] **Step 2: Run the test, expect failure**

Run: `go test ./internal/bridge/ -run TestLightAccessory -v`
Expected: compile failure (`newLightAccessory`, `lightAccessory` undefined).

- [ ] **Step 3: Write `lightbulb.go`**

```go
package bridge

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"

	"github.com/hughobrien/keylight-hap/internal/elgato"
)

// lightAccessory wraps a brutella/hap Lightbulb plus typed handles and the
// device client it controls.
type lightAccessory struct {
	a      *accessory.Lightbulb
	on     *characteristic.On
	bright *characteristic.Brightness
	ct     *characteristic.ColorTemperature

	ctx    context.Context
	client *elgato.Client
}

// newLightAccessory builds the HomeKit accessory for one light, seeds it with
// the current state, and registers write + identify callbacks.
func newLightAccessory(ctx context.Context, name string, info elgato.Info, st elgato.State, client *elgato.Client) *lightAccessory {
	a := accessory.NewLightbulb(accessory.Info{
		Name:         name,
		Manufacturer: "Elgato",
		Model:        info.ProductName,
		SerialNumber: info.SerialNumber,
		Firmware:     info.FirmwareVersion,
	})

	bright := characteristic.NewBrightness()
	ct := characteristic.NewColorTemperature()
	ct.SetMinValue(elgato.TempMin)
	ct.SetMaxValue(elgato.TempMax)
	a.Lightbulb.AddC(bright.C)
	a.Lightbulb.AddC(ct.C)

	la := &lightAccessory{
		a:      a,
		on:     a.Lightbulb.On,
		bright: bright,
		ct:     ct,
		ctx:    ctx,
		client: client,
	}
	la.sync(st)

	la.on.OnValueRemoteUpdate(la.onPowerWrite)
	la.bright.OnValueRemoteUpdate(la.onBrightnessWrite)
	la.ct.OnValueRemoteUpdate(la.onTemperatureWrite)
	a.A.IdentifyFunc = func(*http.Request) {
		if err := client.Identify(ctx); err != nil {
			slog.Warn("identify failed", "light", name, "err", err)
		}
	}
	return la
}

func (l *lightAccessory) onPowerWrite(v bool) {
	if _, err := l.client.Set(l.ctx, elgato.Patch{On: &v}); err != nil {
		slog.Warn("set power failed", "err", err)
	}
}

func (l *lightAccessory) onBrightnessWrite(v int) {
	if _, err := l.client.Set(l.ctx, elgato.Patch{Brightness: &v}); err != nil {
		slog.Warn("set brightness failed", "err", err)
	}
}

func (l *lightAccessory) onTemperatureWrite(v int) {
	if _, err := l.client.Set(l.ctx, elgato.Patch{Temperature: &v}); err != nil {
		slog.Warn("set temperature failed", "err", err)
	}
}

// sync pushes device state onto the HomeKit characteristics. SetValue with a
// nil request does not re-trigger OnValueRemoteUpdate, so this never feeds
// back into a PUT.
func (l *lightAccessory) sync(st elgato.State) {
	l.on.SetValue(st.On)
	_ = l.bright.SetValue(st.Brightness)
	t := st.Temperature
	if t < elgato.TempMin {
		t = elgato.TempMin
	} else if t > elgato.TempMax {
		t = elgato.TempMax
	}
	_ = l.ct.SetValue(t)
}
```

- [ ] **Step 4: Run tests, expect pass**

Run: `go test ./internal/bridge/ -run TestLightAccessory -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/bridge/lightbulb.go internal/bridge/bridge_test.go
git commit -m "Add HomeKit light accessory with write + sync"
```

---

## Task 6: `internal/bridge` — bridge assembly + poll loop

**Files:**
- Create: `internal/bridge/bridge.go`
- Test: extend `internal/bridge/bridge_test.go`

Assembles the bridge accessory + all lights into a `hap.Server`, loads the PIN, and runs a per-light poll loop alongside `ListenAndServe`.

- [ ] **Step 1: Add the failing test to `bridge_test.go`**

```go
func TestNew_BuildsServerWithPin(t *testing.T) {
	d := elgato.NewFakeDevice()
	defer d.Close()

	b, err := New(context.Background(), Options{
		StateDir:     t.TempDir(),
		BridgeName:   "test-bridge",
		Port:         0,
		PollInterval: 20 * 1000 * 1000, // 20ms in nanoseconds, fast for test
		Lights:       []Target{{Name: "Desk", Client: elgato.New(d.HostPort())}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if len(b.Pin) != 8 {
		t.Fatalf("pin: %q", b.Pin)
	}
	if b.Server == nil {
		t.Fatal("nil server")
	}
	if len(b.lights) != 1 {
		t.Fatalf("lights: %d", len(b.lights))
	}
}
```

- [ ] **Step 2: Run the test, expect failure**

Run: `go test ./internal/bridge/ -run TestNew -v`
Expected: compile failure (`New`, `Options`, `Target` undefined).

- [ ] **Step 3: Write `bridge.go`**

```go
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
```

- [ ] **Step 4: Run tests, expect pass**

Run: `go test ./internal/bridge/ -v`
Expected: all bridge tests PASS (`TestPin*`, `TestLoadOrGenerate*`, `TestFormatPin`, `TestLightAccessory_WriteAndSync`, `TestNew_BuildsServerWithPin`).

- [ ] **Step 5: Commit**

```bash
git add internal/bridge/bridge.go internal/bridge/bridge_test.go
git commit -m "Assemble HAP bridge with per-light poll loop"
```

---

## Task 7: `cmd/keylight-hap/main.go` — entrypoint

**Files:**
- Create: `cmd/keylight-hap/main.go`

Parses flags (env-var defaults), discovers lights, builds the bridge, logs the PIN, and runs until SIGINT/SIGTERM.

- [ ] **Step 1: Write `main.go`**

```go
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
	flag.Parse()

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

	slog.Info("HomeKit PIN", "pin", formatPin(b.Pin))

	if err := b.Run(ctx); err != nil && ctx.Err() == nil {
		slog.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func formatPin(pin string) string {
	if len(pin) != 8 {
		return pin
	}
	return pin[:4] + "-" + pin[4:]
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
```

- [ ] **Step 2: Build the whole module**

Run: `go build ./...`
Expected: succeeds, producing no errors. Then `go vet ./...` — expected clean.

- [ ] **Step 3: Run the full test suite**

Run: `go test ./...`
Expected: all packages PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/keylight-hap/main.go
git commit -m "Add keylight-hap entrypoint with discovery and lifecycle"
```

---

## Task 8: Nix flake + NixOS module

**Files:**
- Create: `flake.nix`
- Create: `nix/module.nix`

Adapted from `../breezyd/flake.nix` and `../breezyd/nix/module.nix`, stripped of breezy-specific options (TOML config, nginx, prometheus). Read those two files first for the exact structure.

- [ ] **Step 1: Write `flake.nix`**

```nix
{
  description = "keylight-hap — exposes Elgato Key Lights to HomeKit via mDNS discovery";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs = { self, nixpkgs }: let
    systems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];
    forEachSystem = f: nixpkgs.lib.genAttrs systems f;

    perSystem = forEachSystem (system: let
      pkgs = import nixpkgs { inherit system; };
      version = "0.1.0";

      keylight-hap-pkg = pkgs.buildGoModule {
        pname = "keylight-hap";
        inherit version;
        src = ./.;
        vendorHash = "sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=";
        subPackages = [ "cmd/keylight-hap" ];
        ldflags = [ "-s" "-w" ];
        doCheck = true;
        meta = with pkgs.lib; {
          description = "Exposes Elgato Key Lights to HomeKit";
          homepage = "https://github.com/hughobrien/keylight-hap";
          license = licenses.gpl3Plus;
          platforms = platforms.unix;
          mainProgram = "keylight-hap";
        };
      };
    in {
      packages = {
        default = keylight-hap-pkg;
        keylight-hap = keylight-hap-pkg;
      };

      apps.default = {
        type = "app";
        program = "${keylight-hap-pkg}/bin/keylight-hap";
      };

      devShells.default = pkgs.mkShell {
        packages = with pkgs; [ go gopls gotools go-tools ];
      };

      formatter = pkgs.nixpkgs-fmt;
    });

    defaultModule = { pkgs, lib, ... }: {
      imports = [ ./nix/module.nix ];
      services.keylight-hap.package = lib.mkDefault
        self.packages.${pkgs.stdenv.hostPlatform.system}.default;
    };
  in {
    nixosModules.default = defaultModule;
    nixosModules.keylight-hap = defaultModule;

    packages   = forEachSystem (system: perSystem.${system}.packages);
    apps       = forEachSystem (system: perSystem.${system}.apps);
    devShells  = forEachSystem (system: perSystem.${system}.devShells);
    formatter  = forEachSystem (system: perSystem.${system}.formatter);
  };
}
```

- [ ] **Step 2: Write `nix/module.nix`**

```nix
# SPDX-License-Identifier: GPL-3.0-or-later
#
# NixOS module for keylight-hap.
#
#   {
#     imports = [ inputs.keylight-hap.nixosModules.default ];
#     services.keylight-hap = {
#       enable = true;
#       openFirewall = true;
#     };
#   }

{ config, lib, pkgs, ... }:

let
  cfg = config.services.keylight-hap;
in {
  options.services.keylight-hap = {
    enable = lib.mkEnableOption "keylight-hap — Elgato Key Light to HomeKit bridge";

    package = lib.mkOption {
      type = lib.types.package;
      default = pkgs.keylight-hap or (throw
        "services.keylight-hap.package is unset. Set it explicitly, e.g. `services.keylight-hap.package = inputs.keylight-hap.packages.\${pkgs.system}.default;`.");
      defaultText = lib.literalExpression "pkgs.keylight-hap";
      description = "The keylight-hap package providing /bin/keylight-hap.";
    };

    bridgeName = lib.mkOption {
      type = lib.types.str;
      default = "keylight-hap";
      description = "Name shown in iOS during HomeKit pairing.";
    };

    port = lib.mkOption {
      type = lib.types.port;
      default = 0;
      description = "HAP TCP port. 0 = ephemeral (OS-assigned). Pin a port if the firewall needs a fixed hole.";
    };

    pollInterval = lib.mkOption {
      type = lib.types.str;
      default = "20s";
      description = "How often to poll each light to reflect out-of-band changes in HomeKit.";
    };

    discoveryTimeout = lib.mkOption {
      type = lib.types.str;
      default = "5s";
      description = "mDNS browse window per discovery attempt at startup.";
    };

    stateDir = lib.mkOption {
      type = lib.types.path;
      default = "/var/lib/keylight-hap";
      description = ''
        Directory where the HAP server persists pairing keys + the generated
        PIN. Delete to factory-reset HomeKit pairing. Must reside under
        /var/lib/keylight-hap (the StateDirectory) to be writable under
        ProtectSystem=strict.
      '';
    };

    user = lib.mkOption {
      type = lib.types.str;
      default = "keylight-hap";
      description = "System user the daemon runs as.";
    };

    group = lib.mkOption {
      type = lib.types.str;
      default = "keylight-hap";
      description = "System group the daemon runs as.";
    };

    openFirewall = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = ''
        Open the HAP TCP port (when pinned) and mDNS UDP 5353 in the firewall.
        HomeKit needs inbound UDP/5353 for iPhones to discover the bridge.
      '';
    };
  };

  config = lib.mkIf cfg.enable {
    users.users.${cfg.user} = {
      isSystemUser = true;
      group = cfg.group;
      description = "keylight-hap daemon user";
    };
    users.groups.${cfg.group} = { };

    systemd.services.keylight-hap = {
      description = "Elgato Key Light to HomeKit bridge";
      wants = [ "network-online.target" ];
      after = [ "network-online.target" ];
      wantedBy = [ "multi-user.target" ];

      serviceConfig = {
        ExecStart = lib.concatStringsSep " " [
          "${cfg.package}/bin/keylight-hap"
          "--bridge-name" (lib.escapeShellArg cfg.bridgeName)
          "--port" (toString cfg.port)
          "--poll-interval" cfg.pollInterval
          "--discovery-timeout" cfg.discoveryTimeout
          "--state-dir" cfg.stateDir
        ];
        User = cfg.user;
        Group = cfg.group;
        Restart = "on-failure";
        RestartSec = "5s";
        StateDirectory = "keylight-hap";

        # Hardening. AF_NETLINK is REQUIRED: Go's net.Interfaces() (used by the
        # hap mDNS responder) needs it, or the bridge advertises on zero
        # interfaces and is invisible to iPhones — silently.
        NoNewPrivileges = true;
        ProtectSystem = "strict";
        ProtectHome = true;
        PrivateTmp = true;
        PrivateDevices = true;
        ProtectKernelTunables = true;
        ProtectKernelModules = true;
        ProtectKernelLogs = true;
        ProtectControlGroups = true;
        ProtectClock = true;
        ProtectHostname = true;
        ProtectProc = "invisible";
        RestrictAddressFamilies = [ "AF_INET" "AF_INET6" "AF_UNIX" "AF_NETLINK" ];
        RestrictNamespaces = true;
        RestrictRealtime = true;
        RestrictSUIDSGID = true;
        LockPersonality = true;
        MemoryDenyWriteExecute = true;
        SystemCallArchitectures = "native";
        SystemCallFilter = [ "@system-service" "~@privileged" ];
      };
    };

    networking.firewall = lib.mkIf cfg.openFirewall {
      allowedUDPPorts = [ 5353 ];
      allowedTCPPorts = lib.optional (cfg.port != 0) cfg.port;
    };
  };
}
```

- [ ] **Step 3: Compute the real `vendorHash`**

The `vendorHash` above is a placeholder. Compute the real one:

```bash
nix build .#keylight-hap 2>&1 | grep -A2 'got:' || true
```

The build will fail with a hash mismatch reporting the correct `got: sha256-...`. Copy that value into `flake.nix`'s `vendorHash`. (Alternative: set `vendorHash = pkgs.lib.fakeHash;` first, build, read the expected hash from the error, then replace.) If `nix` is unavailable on this machine, leave a clearly-marked note in the commit message that the maintainer must run this on a Nix host; do NOT invent a hash.

- [ ] **Step 4: Build via Nix (if available)**

Run: `nix build .#keylight-hap`
Expected: succeeds; `./result/bin/keylight-hap` exists. This also runs `go test` via `doCheck = true`.

If `nix` is not installed in this environment, skip the build but verify `go build ./...` and `go test ./...` still pass, and note in the commit that Nix build verification is pending on a Nix host.

- [ ] **Step 5: Commit**

```bash
git add flake.nix nix/module.nix
git commit -m "Add Nix flake and NixOS module"
```

---

## Task 9: Final verification + README + dependency-currency check

**Files:**
- Create: `README.md`

- [ ] **Step 1: Dependency-currency check (the spec follow-up)**

Run:

```bash
go list -m -u all
```

Confirm `github.com/brutella/hap` and `github.com/brutella/dnssd` show no newer version (as of 2026-06-18 the latest are v0.0.35 and v1.2.14, already pinned). If newer versions exist, run `go get -u` for them, `go mod tidy`, re-run `go test ./...`, and note the bump in the commit. Record the outcome (held back anything? why?) in the commit message.

- [ ] **Step 2: Format and vet**

Run: `gofmt -l .` (expected: no output) and `go vet ./...` (expected: clean). Fix anything reported.

- [ ] **Step 3: Write `README.md`**

A short README covering: what it does, `nix run`/`nix build`, the NixOS module snippet (`services.keylight-hap.enable = true; openFirewall = true;`), how pairing works (PIN printed in the journal: `journalctl -u keylight-hap | grep PIN`), and the snapshot-discovery caveat (restart to pick up new lights / IP changes). Keep it factual and concise.

- [ ] **Step 4: Final full build + test**

Run: `go build ./... && go test ./... && go vet ./...`
Expected: all succeed. If Nix is available: `nix build .#keylight-hap` succeeds.

- [ ] **Step 5: Commit**

```bash
git add README.md
git commit -m "Add README and finalize dependency versions"
```

---

## Self-Review Checklist (controller, before execution)

- **Spec coverage:** discovery (Task 3), elgato client incl. identify + accessory-info (Task 2), 1:1 mired mapping with 143–344 clamp (Tasks 2 & 5), On/Brightness/ColorTemperature accessory (Task 5), poll-based sync (Task 6), PIN + FsStore (Tasks 4 & 6), flags/env config (Task 7), flake + module with AF_NETLINK + firewall (Task 8), dependency-currency follow-up (Task 9). All present.
- **Mapping direction:** ColorTemperature pass-through with `SetMinValue(143)/SetMaxValue(344)`, hardware-verified — no inversion.
- **Feedback-loop safety:** sync uses `SetValue` (nil request) which does not trigger `OnValueRemoteUpdate`; documented in Task 5.
- **No-lights-at-startup:** handled by `BrowseUntilFound` retry (Task 3 / Task 7).
- **Known cleanups flagged for the implementer:** the `strconv.Itoa` fix in Task 3 (Step 4), and deletion of the illustrative pseudo-code line in Task 5 (Step 1).
