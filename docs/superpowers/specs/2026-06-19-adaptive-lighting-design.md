# Adaptive Lighting — Design

**Status:** approved (brainstorm), pending implementation plan
**Date:** 2026-06-19

## Goal

Add native HomeKit **Adaptive Lighting** to keylight-hap: the "Adaptive
Lighting" toggle appears on each Key Light in the Home app, and the daemon
drives the light's colour temperature across the day from the schedule a home
hub (Apple TV / HomePod) pushes to it. Requires a home hub, like all HomeKit
Adaptive Lighting.

This is the real Apple feature, not a local sunrise/sunset approximation.

## Background / why this is feasible

Adaptive Lighting is undocumented by Apple but reverse-engineered by the
community. Verified during brainstorming (2026-06-19):

- Upstream `brutella/hap` does **not** support it (0 issues, 0 PRs, no
  characteristics, no curve logic). The maintainer built it only in his
  closed-source `hkknx` product. So our fork `hughobrien/hap` is the right
  home, with a real chance of upstreaming later (cf. open issue brutella/hap#63
  asking for colour-temperature support).
- The fork **already has** the two capabilities this needs: TLV8
  encode/decode, and write-response characteristics
  (`characteristic.C.IsWriteResponse()`, and `SetValueRequestFunc` returning
  `(value, status)` — see `characteristics.go:222`). The Transition Control
  characteristic requires write-response; that was the only real risk and it's
  clear.
- The canonical reference implementation is HAP-NodeJS
  `src/lib/controller/AdaptiveLightingController.ts` (also HAP-python
  `adaptive_lightbulb.py`). We port its TLV layout and interpolation math
  directly. Both are OSS; cribbing is intended.

## Decisions (from brainstorming)

| Decision | Choice |
|---|---|
| Feature | Native HomeKit Adaptive Lighting (not a local scheduler) |
| Code placement | Characteristics **and** controller logic in the `hughobrien/hap` fork; `keylight-hap` only exposes the device |
| External colour-temp change (Elgato app / Stream Deck, seen via poll) | **Disables AL** — treated like a manual override |
| Update cadence | Periodic tick (hub's `updateInterval`, ~60s) **and** immediate recompute on brightness change (warm-on-dim) |
| Restart behaviour | **Persist** the active transition in StateDir; restore and resume on startup |
| `customTemperatureAdjustment` (static curve bias) | **Out of scope** — not exposed |
| Hue/Saturation, MANUAL-mode native device transitions | Out of scope (Key Lights are white-only) |

## The three HomeKit characteristics (fork)

New auto-generated-style files in `characteristic/`, all on the Lightbulb
service:

| Characteristic | UUID (short) | Format | Perms |
|---|---|---|---|
| Supported Characteristic Value Transition Configuration | `144` | TLV8 | read |
| Characteristic Value Transition Control | `143` | TLV8 | read, write, **write-response** |
| Characteristic Value Active Transition Count | `24B` | uint8 | read, notify |

UUIDs follow the standard suffix `-0000-1000-8000-0026BB765291`.

## Architecture & split

### Fork (`hughobrien/hap`)

A new `AdaptiveLightingController` (new package, e.g. `adaptivelighting`, or
alongside the lightbulb service helper). Responsibilities:

- Owns and registers the three characteristics on a given Lightbulb.
- **Supported config (read):** advertises the Brightness and ColorTemperature
  instance IDs (IIDs) with their transition types.
- **Transition Control (write, write-response):** decodes the hub's TLV. Two
  command branches mirroring the reference:
  - *Read current configuration* — return the active transition's status TLV.
  - *Update configuration* — parse the curve and parameters, store as the
    active transition, persist, set Active Transition Count → 1, start the
    timer. An iid-only update (no curve) means **disable**.
  Returns a status TLV (transition id, start-time buffer, time-since-start) as
  the write-response.
- **Curve evaluation / tick** (every `updateInterval`):
  - "now" is corrected by `timeMillisOffset = localNow − hubStartTime` to absorb
    clock skew (stored at setup, like the reference).
  - Locate the current curve segment, linearly interpolate `temperature` and
    `brightnessAdjustmentFactor`, then
    `result = round(temp + adjFactor × clamp(brightness, min, max))`, clamped to
    the light's mired range (143–344).
  - Invoke the **device-set callback** the app provides (see driving model).
  - Throttle ColorTemperature characteristic event-notifications to the hub's
    `notifyIntervalThreshold` (~10 min) so the Home UI tracks without flooding.
- **Disable** (clears transition, count → 0, stops timer, clears persisted
  state) on any of: HomeKit manual write to ColorTemperature; hub disable
  command; curve exhausted; app calls `Disable()`.
- **Persistence:** serialize the active transition (curve entries, transition
  id, start-time buffer, brightness IID + multiplier range, intervals) via the
  hap store under StateDir; deserialize and resume on startup.

Time encoding: hub start time is "milliseconds since 2001-01-01 00:00:00 UTC",
64-bit LE. Curve floats are 32-bit LE; offsets/intervals are variable-length
LE uints. (Exact TLV tag numbers per the reference, recorded in the plan.)

### Driving model

brutella/hap splits `SetValue` (local, no callback) from
`OnValueRemoteUpdate` (remote writes only), so a characteristic-driven update
would not reach the device. Therefore:

> The controller computes the target mired and invokes a
> `SetColorTemperature(mired int) error` callback supplied by the app, **and**
> reflects the value onto the ColorTemperature characteristic via `SetValue`
> (throttled). The app's callback is what actually drives the device.

This realises the agreed split: the app exposes the device, the fork drives
the schedule.

### App (`keylight-hap`)

- `newLightAccessory` (`internal/bridge/lightbulb.go`) constructs the
  controller for each light, passing a `SetColorTemperature` callback that
  reuses the existing `elgato.Patch{Temperature}` path, plus the
  ColorTemperature characteristic and the light's mired bounds.
- The existing `onTemperatureWrite` (manual HomeKit writes) is unchanged; such
  a write disables AL via the controller's own listener.
- **External-change detection:** track `lastCommandedTemp`, updated whenever AL
  or HomeKit commands a temperature. The poll loop (`bridge.go:pollLoop`)
  compares the device-reported temperature against `lastCommandedTemp`; a
  divergence beyond a small mired tolerance means something external moved it
  → call `controller.Disable()`.
- **Light off:** AL keeps ticking internally (so the value is correct on
  power-on) but the app skips device writes while the light is off, and
  resyncs on power-on.
- Controller lifecycle is tied to the bridge's context (start/stop with the
  poll loop).

## Edge cases

- **Clock skew** hub↔daemon — absorbed by `timeMillisOffset`.
- **AL's own writes** update `lastCommandedTemp`, so they never self-trigger the
  external-change disable.
- **Tolerance** for the divergence check is a few mired (exact value chosen in
  the plan) to allow for device rounding.
- **Curve exhaustion** (schedule is ~24h) disables cleanly; the hub normally
  re-sends before then.
- **Restart gap** — none, because the transition is persisted and resumed.

## Testing

- TLV decode/encode round-trip against a captured real-hub curve (golden bytes).
- Interpolation unit tests: known curve + timestamps + brightness → expected
  mired.
- Disable triggers: HomeKit manual write, hub disable command, curve end, and
  external change (via the existing `httptest` fake Key Light).
- Integration: fake device driven through a time-compressed schedule, asserting
  the device receives the expected temperature sequence.
- `go test -race ./...` clean (the controller's timer + poll loop are
  concurrent).

## Out of scope

- Hue/Saturation adaptation (white-only device).
- MANUAL-mode native on-device transitions (the Elgato API has no transition
  primitive).
- `customTemperatureAdjustment` / any tuning knob.

## Documentation

- README: new "Adaptive Lighting" section (what it does, requires a home hub,
  how to enable in Home, that a manual temperature change turns it off).
- Note the persisted state lives alongside the PIN in StateDir (already covered
  by the impermanence guidance).

## Affected files (anticipated)

**Fork (`hughobrien/hap`):** 3 new `characteristic/*.go` files; new
`adaptivelighting` controller + serialization; tests. Tag a new version and
bump the `replace` in `go.mod` + `vendorHash`.

**App (`keylight-hap`):** `internal/bridge/lightbulb.go` (wire controller +
callback + lastCommandedTemp), `internal/bridge/bridge.go` (poll-loop
divergence check, controller lifecycle), possibly `internal/elgato` (no change
expected — reuses `Patch`), README, and tests.
