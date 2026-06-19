# keylight-hap — Design

**Date:** 2026-06-18
**Status:** Approved (ready for implementation planning)

## Summary

A small Go daemon that discovers Elgato Key Lights on the LAN via mDNS at
startup, exposes each as a HomeKit Lightbulb (On / Brightness / Color
Temperature) behind a single bridge, and keeps HomeKit in sync with changes
made through other controllers (e.g. sway keybindings, the Elgato app).
Shipped as a Nix flake with a NixOS module.

Built on `github.com/brutella/hap` to match the existing `breezyd` project
(which uses v0.0.35 — see the dependency-currency follow-up), so the HAP
wiring, PIN handling, FsStore pairing persistence, and the NixOS
module/systemd hardening all transfer almost verbatim.

## Goals

- Expose every Key Light found on the LAN to Apple Home with no per-device
  configuration.
- Full local control from Home: power, brightness, colour temperature.
- Home reflects out-of-band changes (sway keybinds, Elgato app, Stream Deck)
  within the poll interval.
- One-line NixOS enablement; reproducible flake build.

## Non-goals (YAGNI)

- RGB / hue / saturation. The Key Light is white-only; only colour
  temperature applies.
- Runtime add/remove of lights. The light set is a **snapshot taken at
  startup** (see Discovery). A light that is added, removed, or changes IP
  after boot requires a service restart.
- Per-light power-on defaults, `colorChangeDurationMs`, or other
  `/elgato/lights/settings` fields.
- Any cloud/remote access. LAN + HomeKit only.

## Background: the Elgato Key Light HTTP API

Devices speak plain HTTP on **port 9123**, no authentication.

- `GET /elgato/lights` → current state.
- `PUT /elgato/lights` → set state. **Partial updates are allowed** (omit
  fields to leave them unchanged).
- `GET /elgato/accessory-info` → device metadata:
  ```json
  {
    "productName": "Elgato Key Light",
    "firmwareVersion": "1.0.3",
    "serialNumber": "XXXXXXXXXXXX",
    "displayName": "",
    "features": ["lights"]
  }
  ```
  Note `displayName` is frequently `""` (unset), so the HomeKit accessory
  name uses a fallback chain: `displayName` → mDNS instance name →
  `productName`.
- `POST /elgato/identify` → flashes the light a few times (HomeKit Identify).

Devices return only `200 OK` or `400 Bad Request`. A `PUT` echoes the full
resulting state in its response body, so the client refreshes its cached
state from the PUT response without a follow-up GET.

`/elgato/lights` body:

```json
{
  "numberOfLights": 1,
  "lights": [
    { "on": 0, "brightness": 20, "temperature": 213 }
  ]
}
```

| Field         | Type | Range   | Meaning                              |
|---------------|------|---------|--------------------------------------|
| `on`          | int  | 0 / 1   | power                                |
| `brightness`  | int  | 0–100   | percent — API accepts 1–100 (wider than the app's ~3–100) |
| `temperature` | int  | 143–344 | **mireds** — 143 = 7000K (cool), 344 = 2900K (warm) |

### Colour-temperature direction (resolved)

Public docs conflict on the temperature scale, so this was investigated
carefully:

- adamesch/elgato-key-light-api's prose claims a linear `K = value / 0.05`
  (i.e. `value × 20`): `143 ≈ 2900K` (warm), `344 ≈ 7000K` (cool).
- apihandyman claims the reverse: `143 = 7000K` (cool), `344 = 2900K` (warm).

These are mutually exclusive. Three things settle it in favour of the
**mired** interpretation (143 = cool/7000K, 344 = warm/2900K):

1. **adamesch is internally inconsistent.** Its own example computes
   `7000K × 0.05 = 350`, yet states the cool endpoint is `344` — the linear
   formula does not actually close on the documented range.
2. **The mired identities land exactly on the endpoints.**
   `1e6 / 7000 = 142.9 → 143` and `1e6 / 2900 = 344.8 → 344`. A linear
   `×20` map would put 7000K at 350, not 344. The exact fit is strong
   evidence the device value *is* mireds.
3. **The author's tested `elgato-rs`** drives the value **up** toward `344`
   for `temp+` ("Increase Warmth") — higher value = warmer, consistent with
   mireds.

Therefore the field **is mireds**, in the same units and direction as
HomeKit's `ColorTemperature` characteristic.

**Consequence:** the colour-temperature mapping to HomeKit is a 1:1
pass-through, no arithmetic — only a clamp to the device's supported
`[143, 344]` range.

**Verification gate — CONFIRMED on hardware (2026-06-18).** Tested against
the real light (192.168.1.31, serial `BW17K1A00377`) by setting values in
the Elgato iPhone app and reading `GET /elgato/lights`:

| Set in app    | Device reports     |
|---------------|--------------------|
| 2900K (warm)  | `temperature: 344` |
| 7000K (cool)  | `temperature: 143` |
| 3% brightness | `brightness: 3`    |
| 100%          | `brightness: 100`  |

Direction and units are settled: `temperature` is mireds (143 cool → 344
warm), matching HomeKit. The mapping is still isolated in one small,
well-named function with a unit test, but no inversion risk remains. The
adamesch `/0.05` formula is confirmed wrong on direction.

### Brightness floor (observed on hardware)

The API accepts `brightness` down to `1` while keeping `on: 1`, but at
`brightness: 1` the light is **visually off** (confirmed on the real unit
2026-06-18); the effective visible floor is ~2–3. The mapping stays a 1:1
pass-through — we do not special-case low values. HomeKit's own UX covers
the awkward zone: dragging brightness to 0 in Home sets the accessory's
`On` to false (→ `on: 0`), so the only oddity is that 1% in Home looks dark
while still reporting "on". Acceptable; no code handling needed.

### Discovery

Elgato Wi-Fi products advertise over mDNS/DNS-SD as service type
`_elg._tcp.local.` on port 9123. The mDNS instance name / serial provides a
stable per-device identity.

## Data mapping (HomeKit ↔ Elgato)

| HomeKit characteristic        | Elgato field  | Mapping                                   |
|-------------------------------|---------------|-------------------------------------------|
| `On` (bool)                   | `on` (0/1)    | direct                                    |
| `Brightness` (0–100)          | `brightness`  | direct                                    |
| `ColorTemperature` (mireds)   | `temperature` | direct; characteristic min/max set to 143/344 |
| `Identify` (event)            | —             | `POST /elgato/identify`                   |

The Lightbulb accessory's `ColorTemperature` characteristic is created with
`SetMinValue(143)` / `SetMaxValue(344)` so the Home app slider matches the
hardware's real range.

## Architecture

```
                  ┌─────────────────────────────────────────┐
   LAN (mDNS) ──► │ discover: browse _elg._tcp (startup only) │
                  └───────────────────┬─────────────────────┘
                                      │ []Light{serial, host:port, name}
                                      ▼
                  ┌─────────────────────────────────────────┐
   iPhone ◄──HAP─►│ bridge: one Lightbulb per light          │
                  │  - PIN (generate/persist) + FsStore       │
                  │  - write callbacks ──► elgato.PUT         │
                  │  - sync loop  ◄──────── elgato.GET (20s)  │
                  └───────────────────┬─────────────────────┘
                                      │ HTTP :9123
                                      ▼
                              Elgato Key Light(s)
```

### Repo layout (mirrors breezyd)

```
keylight-hap/
├── flake.nix                  # buildGoModule + nixosModules.default, per-system
├── nix/module.nix             # services.keylight-hap
├── go.mod                     # github.com/hughobrien/keylight-hap
├── cmd/keylight-hap/main.go   # flag parsing, wiring, lifecycle
├── internal/elgato/
│   ├── client.go              # GET/PUT /elgato/lights, accessory-info, identify
│   ├── fakedevice.go          # httptest server emulating a Key Light (tests)
│   └── client_test.go
├── internal/discover/
│   ├── discover.go            # browse _elg._tcp via brutella/dnssd
│   └── discover_test.go
└── internal/bridge/
    ├── bridge.go              # accessory construction, PIN, sync loop, callbacks
    ├── pin.go                 # loadOrGeneratePin (adapted from breezyd)
    └── bridge_test.go
```

### Components

1. **`internal/elgato` — device client.**
   - `Get(ctx) (State, error)` → `GET /elgato/lights`, returns first light's
     `{On, Brightness, Temperature}`.
   - `Set(ctx, partial)` → `PUT /elgato/lights` with only the changed
     field(s).
   - `Info(ctx) (Info, error)` → `GET /elgato/accessory-info` for
     `displayName`, `productName`, `serialNumber`, `firmwareVersion`. These
     populate the HomeKit AccessoryInformation service
     (Manufacturer=`Elgato`, Model=`productName`, SerialNumber,
     FirmwareRevision).
   - `Identify(ctx)` → `POST /elgato/identify`.
   - Short HTTP timeouts; errors are returned, not fatal.
   - Includes `fakedevice.go`: an `httptest.Server` that emulates the API
     (state held in memory, partial-PUT semantics, clamping) for tests.

2. **`internal/discover` — startup discovery.**
   - Browses `_elg._tcp.local.` for a bounded window (`discoveryTimeout`,
     default 5s) using `brutella/dnssd` (already in hap's dependency tree).
   - Returns a deduplicated list of lights: stable serial/instance id,
     resolved `host:port`, and advertised name.
   - If **zero** lights are found, back off and re-browse (handles the light
     powering on after the service). Proceeds once ≥1 light is found.

3. **`internal/bridge` — HAP server.**
   - Builds one `accessory.Lightbulb` per discovered light, attaches
     `Brightness` and a range-clamped `ColorTemperature`, and sets the
     accessory name via the fallback chain `displayName` → mDNS instance
     name → `productName`.
   - Stable accessory ids derived from device serial.
   - `pin.go`: `loadOrGeneratePin(stateDir)` — reused from breezyd
     (8-digit, rejects HomeKit's weak-PIN list, persists to `pin.txt`,
     0600). PIN logged at startup as `XXXX-XXXX`.
   - Pairings persisted via `hap.NewFsStore(stateDir)`.
   - **Write path:** `OnValueRemoteUpdate` callbacks on On/Brightness/
     ColorTemperature issue partial `PUT`s to the corresponding device.
     `Identify` calls `POST /elgato/identify`.
   - **Sync loop:** every `pollInterval` (default **20s**), `GET` each
     light and `SetValue` the characteristics so out-of-band changes appear
     in Home. (Local `SetValue` with `r=nil` does not re-trigger the write
     callback, mirroring breezyd's understanding of the hap library, so no
     feedback loop.)

4. **`cmd/keylight-hap/main.go`** — parse flags/env, run discovery, build
   the bridge, start the HAP server, handle SIGINT/SIGTERM for clean
   shutdown.

## Configuration

There are **no secrets** (the Elgato API is unauthenticated and there is no
device password), so there is no config file and no 0600/secrets dance.
Everything is CLI flags, also settable via environment variables. The NixOS
module passes them on the command line.

| Flag                  | Env                          | Default               | Meaning                                            |
|-----------------------|------------------------------|-----------------------|----------------------------------------------------|
| `--bridge-name`       | `KEYLIGHT_HAP_BRIDGE_NAME`   | `keylight-hap`        | Name shown during HomeKit pairing.                 |
| `--port`              | `KEYLIGHT_HAP_PORT`          | `0`                   | HAP TCP port; 0 = OS-assigned ephemeral.           |
| `--poll-interval`     | `KEYLIGHT_HAP_POLL_INTERVAL` | `20s`                 | State-sync poll period.                            |
| `--discovery-timeout` | `KEYLIGHT_HAP_DISCOVERY_TIMEOUT` | `5s`              | mDNS browse window per attempt.                    |
| `--state-dir`         | `KEYLIGHT_HAP_STATE_DIR`     | `/var/lib/keylight-hap` | PIN + pairing storage.                           |

## NixOS module (`services.keylight-hap`)

Adapted from `breezyd`'s module, minus the TOML/config-file and
nginx/prometheus machinery (not needed here).

Options:

- `enable`
- `package` (defaults to the flake's build for the host system, via the
  flake's `defaultModule` wrapper — same pattern as breezyd)
- `bridgeName` (default `"keylight-hap"`)
- `port` (default `0`)
- `pollInterval` (default `"20s"`)
- `discoveryTimeout` (default `"5s"`)
- `stateDir` (default `/var/lib/keylight-hap`)
- `user` / `group` (default `keylight-hap`)
- `openFirewall` (default `false`) — opens the HAP TCP port (when pinned)
  and UDP 5353 for mDNS.

systemd unit:

- `ExecStart = ${package}/bin/keylight-hap` with the flags above.
- `StateDirectory = keylight-hap` (systemd creates/chowns
  `/var/lib/keylight-hap`).
- `Wants`/`After = network-online.target`.
- Hardening block copied from breezyd, **including
  `RestrictAddressFamilies = [ AF_INET AF_INET6 AF_UNIX AF_NETLINK ]`** —
  `AF_NETLINK` is required or the hap mDNS responder silently advertises on
  zero interfaces (breezyd learned this the hard way), and the light is
  never visible to iPhones.
- `Restart = on-failure`.

## Error handling

- **No lights at startup:** re-browse with backoff rather than exiting, so
  boot-order races (service up before light) self-heal.
- **Light unreachable mid-run:** log and continue; keep the last HomeKit
  value; keep polling. HomeKit has no clean "unreachable" state for a
  bridged accessory, so we retain the last-known values.
- **Snapshot model:** because the light set is fixed at startup, a light
  that disappears stays present-but-unreachable in Home, and a newly added
  light is not exposed until restart. This is the accepted trade-off of the
  chosen discovery model.
- **PUT silent failures:** the device returns 200 even for out-of-range
  values, so the client clamps before sending (brightness 0–100,
  temperature 143–344).

## Testing

- **`internal/elgato`:** unit tests against the in-package `fakedevice`
  httptest server — value mapping, clamping, partial-PUT behaviour, and
  accessory-info parsing.
- **`internal/bridge`:** sync-loop and write-callback tests using the fake
  device plus an in-memory hap store; assert that a device-side change is
  reflected on the characteristic after a poll, and that a characteristic
  write issues the right partial PUT.
- **PIN:** reuse breezyd's PIN tests (persistence, mode 0600, weak-PIN
  rejection, format).
- The Nix build runs `go test` (`doCheck = true`), as breezyd does.

## Follow-up tasks

- **Verify dependency currency.** During implementation, confirm we pull the
  **latest** versions of every dependency rather than copying breezyd's
  pins: `github.com/brutella/hap` and its `brutella/dnssd` (check the latest
  tags — breezyd is on hap v0.0.35, which may no longer be newest), the Go
  toolchain version in `go.mod`, and the `nixpkgs` flake input. Run
  `go get -u ./... && go mod tidy` and `nix flake update`, then re-run tests
  before locking. Note any version that had to be held back and why.

(The colour-temperature direction has already been confirmed on hardware —
see the Verification gate above.)

## Defaults chosen (no objection raised)

- License **GPL-3.0-or-later** (matches breezyd).
- Go module path `github.com/hughobrien/keylight-hap`; binary
  `keylight-hap`.
- HomeKit Identify wired to `POST /elgato/identify`.
