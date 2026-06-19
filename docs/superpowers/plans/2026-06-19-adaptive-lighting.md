# Adaptive Lighting Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add native HomeKit Adaptive Lighting to keylight-hap so each Key Light gets the Home-app "Adaptive Lighting" toggle and the daemon drives colour temperature from the hub's daily schedule.

**Architecture:** The three Adaptive-Lighting characteristics and a reusable `AdaptiveLighting` controller live in the `github.com/hughobrien/hap` fork (module path stays `github.com/brutella/hap`). The controller decodes the hub's TLV8 curve, runs a timer, computes the target mired, persists across restarts, and calls a `SetColorTemperature(mired int) error` callback. `keylight-hap` supplies that callback (driving the Elgato device), wires the controller onto each Lightbulb, and disables AL when the poll loop sees an external temperature change.

**Tech Stack:** Go 1.26; `github.com/brutella/hap` fork (HAP server, `tlv8` struct-tag marshaller, `characteristic`/`accessory` packages); Elgato HTTP API; Nix flake build.

---

## Background the implementer must read first

Adaptive Lighting (AL) is undocumented by Apple. The authoritative reference for the TLV8 layout and curve math is HAP-NodeJS `src/lib/controller/AdaptiveLightingController.ts`. Clone it for reference:

```bash
git clone --depth 1 https://github.com/homebridge/HAP-NodeJS.git /tmp/hap-nodejs-ref
# read /tmp/hap-nodejs-ref/src/lib/controller/AdaptiveLightingController.ts
```

How AL works at runtime:
1. The accessory advertises which characteristics support transitions (Supported Transition Configuration).
2. When the user enables AL, a home hub writes a 24h schedule to the Transition Control characteristic: a piecewise-linear curve of `(temperature_mired, brightnessAdjustmentFactor, transitionTime, duration)` points, plus a start time (millis since 2001-01-01 UTC), update interval (~60s) and notify-interval threshold (~10min).
3. The accessory, every update interval, finds the current curve segment for "now", interpolates, computes `round(temperature + brightnessAdjustmentFactor * clamp(brightness, min, max))`, clamps to the light's mired range, and sets the colour temperature.
4. AL is disabled when ColorTemperature is written manually, when the hub sends an iid-only "disable" command, when the curve ends, or (our addition) when an external controller changes the temperature.
5. AL state must persist across reboots.

### TLV8 tag map (from the reference) — used throughout

```
Supported Transition Configuration (characteristic 0x144, read):
  0x01 SUPPORTED_TRANSITION_CONFIGURATION  (list of:)
       0x01 CHARACTERISTIC_IID    (var uint, little-endian)
       0x02 TRANSITION_TYPE       (1 byte: 0x01=brightness, 0x02=colour-temperature)

Transition Control (characteristic 0x143, read/write/write-response), write payload top level:
  0x01 READ_CURRENT_VALUE_TRANSITION_CONFIGURATION
       0x01 CHARACTERISTIC_IID
  0x02 UPDATE_VALUE_TRANSITION_CONFIGURATION
       0x01 VALUE_TRANSITION_CONFIGURATION:
            0x01 CHARACTERISTIC_IID            (var uint)
            0x02 TRANSITION_PARAMETERS:
                 0x01 TRANSITION_ID            (16 bytes)
                 0x02 START_TIME               (8 bytes, millis since 2001-01-01 UTC, LE)
                 0x03 UNKNOWN_3                (8 bytes, optional)
            0x03 UNKNOWN_3                      (1 byte == 1 -> enable; absent -> disable)
            0x05 TRANSITION_CURVE_CONFIGURATION:
                 0x01 TRANSITION_ENTRY  (list of:)
                      0x01 ADJUSTMENT_FACTOR   (float32 LE)
                      0x02 VALUE               (float32 LE, temperature mired)
                      0x03 TRANSITION_OFFSET   (var uint, ms)
                      0x04 DURATION            (var uint, ms, optional)
                 0x02 ADJUSTMENT_CHARACTERISTIC_IID  (var uint, the brightness iid)
                 0x03 ADJUSTMENT_MULTIPLIER_RANGE:
                      0x01 MINIMUM             (uint32)
                      0x02 MAXIMUM             (uint32)
            0x06 UPDATE_INTERVAL                (uint16)
            0x08 NOTIFY_INTERVAL_THRESHOLD      (uint32)

Write-response / read-response status (VALUE_CONFIGURATION_STATUS = 0x01):
       0x01 CHARACTERISTIC_IID
       0x02 TRANSITION_PARAMETERS (same as above: transition id + start time [+ unknown3])
       0x03 TIME_SINCE_START      (var uint, ms)

Active Transition Count: characteristic 0x24B, uint8, read+notify. 1 when active, 0 when not.
```

Note on the fork's `tlv8` marshaller (struct-tag based, `tlv8:"<n>"`):
- Nested structs encode/decode as a TLV whose value is the inner sequence. ✅
- A slice of structs with tag `tlv8:"1"` encodes as repeated tag-1 items separated by a `{0x00,0x00}` zero-length TLV, and decodes symmetrically. ✅ (this is exactly HomeKit's list format)
- Integers are fixed-width by Go type (uint16→2 bytes, uint32→4 bytes). On decode the reader auto-downsizes, so decoding the hub's variable-length ints into `uint32`/`uint64` fields works. ✅
- `float32` **encode is broken** (writes zero bytes) — Task 1 fixes it. Decode is correct.
- 8-byte fields we re-emit (transition id, start time) are stored and re-encoded as `[]byte`, never as int64 (whose encode is also buggy), so we sidestep that bug.

---

## File Structure

### Fork (`github.com/hughobrien/hap`, checked out at `~/src/hap`)
- `tlv8/writer.go` — modify: fix `writeFloat32`.
- `tlv8/writer_test.go` — modify: add float32 round-trip test.
- `characteristic/supported_characteristic_value_transition_configuration.go` — create (UUID 0x144, TLV8, read).
- `characteristic/characteristic_value_transition_control.go` — create (UUID 0x143, TLV8, read/write/write-response).
- `characteristic/characteristic_value_active_transition_count.go` — create (UUID 0x24B, uint8, read+notify).
- `adaptive/tlv.go` — create: the TLV struct definitions + encode/decode helpers.
- `adaptive/tlv_test.go` — create: round-trip + golden-bytes tests.
- `adaptive/curve.go` — create: transition-curve evaluation (pure functions).
- `adaptive/curve_test.go` — create: interpolation unit tests.
- `adaptive/controller.go` — create: the `AdaptiveLighting` controller (characteristics, timer, persistence, disable).
- `adaptive/controller_test.go` — create: controller behaviour tests.

### App (`keylight-hap`)
- `go.mod` — modify: dev-time local `replace`, then final pinned version.
- `internal/bridge/lightbulb.go` — modify: construct + wire the controller, `lastCommandedTemp`.
- `internal/bridge/bridge.go` — modify: poll-loop divergence check, controller start/stop, store wiring.
- `internal/bridge/adaptive_test.go` — create: integration test via the fake device.
- `README.md` — modify: Adaptive Lighting section.

---

## Phase 0: Fork dev setup

### Task 0: Clone the fork and point keylight-hap at it locally

**Files:**
- Clone: `~/src/hap`
- Modify: `/Users/hugh/src/keylight-hap/go.mod`

- [ ] **Step 1: Clone the fork at the pinned commit**

```bash
git clone https://github.com/hughobrien/hap.git ~/src/hap
cd ~/src/hap && git checkout 78932fb1aac2 -b adaptive-lighting
```

- [ ] **Step 2: Point keylight-hap's replace at the local checkout**

Edit `/Users/hugh/src/keylight-hap/go.mod` line 27, replacing the version directive with a local path:

```
replace github.com/brutella/hap => ../hap
```

- [ ] **Step 3: Verify the app still builds against the local fork**

Run: `cd /Users/hugh/src/keylight-hap && go build ./... && go test ./...`
Expected: builds and existing tests PASS.

- [ ] **Step 4: Commit the app-side replace change**

```bash
cd /Users/hugh/src/keylight-hap
git add go.mod
git commit -m "build: point hap replace at local fork for adaptive-lighting dev"
```

---

## Phase 1: Fork — TLV8 fix and characteristics

All Phase 1 commits happen in `~/src/hap`.

### Task 1: Fix the float32 TLV8 encoder bug

**Files:**
- Modify: `~/src/hap/tlv8/writer.go` (function `writeFloat32`)
- Test: `~/src/hap/tlv8/writer_test.go`

- [ ] **Step 1: Write the failing round-trip test**

Append to `~/src/hap/tlv8/writer_test.go`:

```go
func TestFloat32RoundTrip(t *testing.T) {
	type payload struct {
		F float32 `tlv8:"1"`
	}
	in := payload{F: 153.5}
	b, err := Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out payload
	if err := Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.F != in.F {
		t.Fatalf("got %v want %v (encoded bytes %x)", out.F, in.F, b)
	}
}
```

- [ ] **Step 2: Run it to confirm it fails**

Run: `cd ~/src/hap && go test ./tlv8/ -run TestFloat32RoundTrip -v`
Expected: FAIL — `got 0 want 153.5` (encoder writes zero bytes).

- [ ] **Step 3: Fix the encoder**

In `~/src/hap/tlv8/writer.go`, replace the body of `writeFloat32`:

```go
func (wr *writer) writeFloat32(tag uint8, v float32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], math.Float32bits(v))
	wr.writeBytes(tag, b[:])
}
```

Ensure `encoding/binary` and `math` are imported in the file (add to the import block if missing).

- [ ] **Step 4: Run the test to confirm it passes**

Run: `cd ~/src/hap && go test ./tlv8/ -run TestFloat32RoundTrip -v`
Expected: PASS.

- [ ] **Step 5: Run the whole tlv8 package to confirm no regressions**

Run: `cd ~/src/hap && go test ./tlv8/`
Expected: PASS (ok).

- [ ] **Step 6: Commit**

```bash
cd ~/src/hap
git add tlv8/writer.go tlv8/writer_test.go
git commit -m "fix(tlv8): writeFloat32 wrote zero bytes; encode the float bits"
```

### Task 2: Add the three Adaptive-Lighting characteristics

**Files:**
- Create: `~/src/hap/characteristic/supported_characteristic_value_transition_configuration.go`
- Create: `~/src/hap/characteristic/characteristic_value_transition_control.go`
- Create: `~/src/hap/characteristic/characteristic_value_active_transition_count.go`

- [ ] **Step 1: Create the Supported Transition Configuration characteristic**

Create `~/src/hap/characteristic/supported_characteristic_value_transition_configuration.go`:

```go
package characteristic

const TypeSupportedCharacteristicValueTransitionConfiguration = "144"

type SupportedCharacteristicValueTransitionConfiguration struct {
	*Bytes
}

func NewSupportedCharacteristicValueTransitionConfiguration() *SupportedCharacteristicValueTransitionConfiguration {
	c := NewBytes(TypeSupportedCharacteristicValueTransitionConfiguration)
	c.Format = FormatTLV8
	c.Permissions = []string{PermissionRead}
	c.Val = []byte{}

	return &SupportedCharacteristicValueTransitionConfiguration{c}
}
```

- [ ] **Step 2: Create the Transition Control characteristic (write-response)**

Create `~/src/hap/characteristic/characteristic_value_transition_control.go`:

```go
package characteristic

const TypeCharacteristicValueTransitionControl = "143"

type CharacteristicValueTransitionControl struct {
	*Bytes
}

func NewCharacteristicValueTransitionControl() *CharacteristicValueTransitionControl {
	c := NewBytes(TypeCharacteristicValueTransitionControl)
	c.Format = FormatTLV8
	c.Permissions = []string{PermissionRead, PermissionWrite, PermissionWriteResponse}
	c.Val = []byte{}

	return &CharacteristicValueTransitionControl{c}
}
```

- [ ] **Step 3: Create the Active Transition Count characteristic**

Create `~/src/hap/characteristic/characteristic_value_active_transition_count.go`:

```go
package characteristic

const TypeCharacteristicValueActiveTransitionCount = "24B"

type CharacteristicValueActiveTransitionCount struct {
	*Int
}

func NewCharacteristicValueActiveTransitionCount() *CharacteristicValueActiveTransitionCount {
	c := NewInt(TypeCharacteristicValueActiveTransitionCount)
	c.Format = FormatUInt8
	c.Permissions = []string{PermissionRead, PermissionEvents}
	c.SetValue(0)

	return &CharacteristicValueActiveTransitionCount{c}
}
```

- [ ] **Step 4: Verify the package compiles**

Run: `cd ~/src/hap && go build ./characteristic/`
Expected: builds clean.

- [ ] **Step 5: Commit**

```bash
cd ~/src/hap
git add characteristic/supported_characteristic_value_transition_configuration.go \
        characteristic/characteristic_value_transition_control.go \
        characteristic/characteristic_value_active_transition_count.go
git commit -m "feat(characteristic): add Adaptive Lighting transition characteristics"
```

---

## Phase 2: Fork — TLV codec

### Task 3: Define TLV structs and decode the hub's UPDATE payload

**Files:**
- Create: `~/src/hap/adaptive/tlv.go`
- Test: `~/src/hap/adaptive/tlv_test.go`

- [ ] **Step 1: Write the TLV struct definitions**

Create `~/src/hap/adaptive/tlv.go`:

```go
// Package adaptive implements HomeKit Adaptive Lighting for Lightbulb
// accessories: the transition characteristics, the TLV8 schedule codec, the
// curve evaluation, and a controller that drives colour temperature over time.
package adaptive

import (
	"encoding/binary"
	"time"
)

// hapEpoch is 2001-01-01 00:00:00 UTC. HomeKit transition start times are
// expressed in milliseconds since this instant.
var hapEpoch = time.Date(2001, time.January, 1, 0, 0, 0, 0, time.UTC)

// Transition types in the Supported Transition Configuration.
const (
	transitionTypeBrightness       byte = 0x01
	transitionTypeColorTemperature byte = 0x02
)

// supportedConfig is the value of the Supported Transition Configuration
// characteristic: a list of (iid, transition-type) pairs.
type supportedConfig struct {
	Entries []supportedEntry `tlv8:"1"`
}

type supportedEntry struct {
	IID            uint64 `tlv8:"1"`
	TransitionType byte   `tlv8:"2"`
}

// controlWrite is the top-level payload written to Transition Control.
type controlWrite struct {
	Read   *readRequest   `tlv8:"1,optional"`
	Update *updateRequest `tlv8:"2,optional"`
}

type readRequest struct {
	IID uint64 `tlv8:"1"`
}

type updateRequest struct {
	Config valueTransitionConfig `tlv8:"1"`
}

type valueTransitionConfig struct {
	IID                    uint64               `tlv8:"1"`
	Parameters             transitionParameters `tlv8:"2"`
	Enabled                byte                 `tlv8:"3,optional"` // 1 = enable; absent = disable
	Curve                  curveConfig          `tlv8:"5,optional"`
	UpdateInterval         uint16               `tlv8:"6,optional"`
	NotifyIntervalThreshold uint32              `tlv8:"8,optional"`
}

type transitionParameters struct {
	TransitionID []byte `tlv8:"1"` // 16 bytes
	StartTime    []byte `tlv8:"2"` // 8 bytes, millis since 2001 LE
	Unknown3     []byte `tlv8:"3,optional"`
}

type curveConfig struct {
	Entries          []curveEntryTLV `tlv8:"1"`
	AdjustmentIID    uint64          `tlv8:"2"`
	MultiplierRange  multiplierRange `tlv8:"3"`
}

type curveEntryTLV struct {
	AdjustmentFactor float32 `tlv8:"1"`
	Temperature      float32 `tlv8:"2"`
	TransitionOffset uint32  `tlv8:"3"`
	Duration         uint32  `tlv8:"4,optional"`
}

type multiplierRange struct {
	Min uint32 `tlv8:"1"`
	Max uint32 `tlv8:"2"`
}

// status is the write-response / read-response body.
type statusResponse struct {
	Status valueConfigStatus `tlv8:"1"`
}

type valueConfigStatus struct {
	IID            uint64               `tlv8:"1"`
	Parameters     transitionParameters `tlv8:"2"`
	TimeSinceStart uint64               `tlv8:"3"`
}

// startTimeMillis converts the 8-byte LE start-time buffer to epoch millis.
func startTimeMillis(buf []byte) int64 {
	var padded [8]byte
	copy(padded[:], buf)
	since2001 := binary.LittleEndian.Uint64(padded[:])
	return hapEpoch.UnixMilli() + int64(since2001)
}
```

- [ ] **Step 2: Write a decode round-trip test for the UPDATE payload**

Create `~/src/hap/adaptive/tlv_test.go`:

```go
package adaptive

import (
	"testing"

	"github.com/brutella/hap/tlv8"
)

func TestDecodeUpdateRoundTrip(t *testing.T) {
	in := controlWrite{
		Update: &updateRequest{
			Config: valueTransitionConfig{
				IID:     7,
				Enabled: 1,
				Parameters: transitionParameters{
					TransitionID: make([]byte, 16),
					StartTime:    []byte{0, 0, 0, 0, 0, 0, 0, 0},
				},
				Curve: curveConfig{
					Entries: []curveEntryTLV{
						{AdjustmentFactor: -1.5, Temperature: 200, TransitionOffset: 0},
						{AdjustmentFactor: -2.0, Temperature: 300, TransitionOffset: 1800000},
					},
					AdjustmentIID:   3,
					MultiplierRange: multiplierRange{Min: 10, Max: 100},
				},
				UpdateInterval:          60000,
				NotifyIntervalThreshold: 600000,
			},
		},
	}

	b, err := tlv8.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out controlWrite
	if err := tlv8.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Update == nil {
		t.Fatal("update missing")
	}
	if got := len(out.Update.Config.Curve.Entries); got != 2 {
		t.Fatalf("curve entries = %d, want 2", got)
	}
	if out.Update.Config.Curve.Entries[1].Temperature != 300 {
		t.Fatalf("entry[1] temp = %v, want 300", out.Update.Config.Curve.Entries[1].Temperature)
	}
	if out.Update.Config.IID != 7 {
		t.Fatalf("iid = %d, want 7", out.Update.Config.IID)
	}
}
```

- [ ] **Step 3: Run the test**

Run: `cd ~/src/hap && go test ./adaptive/ -run TestDecodeUpdateRoundTrip -v`
Expected: PASS. If the curve-entry list fails to split (count != 2), the fork's list separator handling needs the `{0x00,0x00}` delimiter — see Task 4 golden test before changing struct tags.

- [ ] **Step 4: Commit**

```bash
cd ~/src/hap
git add adaptive/tlv.go adaptive/tlv_test.go
git commit -m "feat(adaptive): TLV8 schedule structs with round-trip decode"
```

### Task 4: Golden-bytes test against a captured real curve

This guards against the list-separator risk: real HomeKit hubs encode the curve as repeated tag-1 entries. We assert our decode handles the real on-wire bytes.

**Files:**
- Test: `~/src/hap/adaptive/tlv_test.go` (append)
- Create: `~/src/hap/adaptive/testdata/transition_control_write.hex`

- [ ] **Step 1: Capture or obtain a real Transition Control write payload**

Obtain one real base64/hex Transition Control write from a hub. Two options:
- From HAP-NodeJS debug logs: run its `Light-AdaptiveLighting_accessory.ts` example, enable AL in Home, and copy the logged `DEBUG: '<base64>'` from `handleTransitionControlWrite`.
- From this project once Task 8 is wired, log the raw write bytes.

Save the hex (no spaces) to `~/src/hap/adaptive/testdata/transition_control_write.hex`.

- [ ] **Step 2: Write the golden decode test**

Append to `~/src/hap/adaptive/tlv_test.go`:

```go
func TestDecodeRealCurve(t *testing.T) {
	raw, err := os.ReadFile("testdata/transition_control_write.hex")
	if err != nil {
		t.Skip("no captured curve available")
	}
	b, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("bad hex: %v", err)
	}
	var w controlWrite
	if err := tlv8.Unmarshal(b, &w); err != nil {
		t.Fatalf("unmarshal real curve: %v", err)
	}
	if w.Update == nil {
		t.Fatal("expected an update transition")
	}
	if len(w.Update.Config.Curve.Entries) < 2 {
		t.Fatalf("expected a multi-point curve, got %d entries", len(w.Update.Config.Curve.Entries))
	}
	for i, e := range w.Update.Config.Curve.Entries {
		if e.Temperature < 50 || e.Temperature > 1000 {
			t.Fatalf("entry %d temperature %v out of sane mired range", i, e.Temperature)
		}
	}
}
```

Add imports `encoding/hex`, `os`, `strings` to the test file.

- [ ] **Step 3: Run the test**

Run: `cd ~/src/hap && go test ./adaptive/ -run TestDecodeRealCurve -v`
Expected: PASS (or SKIP if no capture yet — capture before merging).

- [ ] **Step 4: Commit**

```bash
cd ~/src/hap
git add adaptive/tlv_test.go adaptive/testdata/transition_control_write.hex
git commit -m "test(adaptive): golden decode of a real hub transition curve"
```

### Task 5: Encode the status response and the read-config response

**Files:**
- Modify: `~/src/hap/adaptive/tlv.go` (add builders)
- Test: `~/src/hap/adaptive/tlv_test.go` (append)

- [ ] **Step 1: Write the failing test for the status response**

Append to `~/src/hap/adaptive/tlv_test.go`:

```go
func TestBuildStatusResponse(t *testing.T) {
	params := transitionParameters{
		TransitionID: make([]byte, 16),
		StartTime:    []byte{1, 2, 3, 4, 5, 6, 7, 8},
	}
	b, err := buildStatusResponse(7, params, 1234)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var out statusResponse
	if err := tlv8.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Status.IID != 7 {
		t.Fatalf("iid = %d, want 7", out.Status.IID)
	}
	if out.Status.TimeSinceStart != 1234 {
		t.Fatalf("timeSinceStart = %d, want 1234", out.Status.TimeSinceStart)
	}
}
```

- [ ] **Step 2: Run it to confirm it fails**

Run: `cd ~/src/hap && go test ./adaptive/ -run TestBuildStatusResponse -v`
Expected: FAIL — `buildStatusResponse` undefined.

- [ ] **Step 3: Implement the builders**

Append to `~/src/hap/adaptive/tlv.go`:

```go
import "github.com/brutella/hap/tlv8" // add to the existing import block

// buildStatusResponse encodes the write-response / read-response body that the
// controller returns after an UPDATE or READ command.
func buildStatusResponse(iid uint64, params transitionParameters, timeSinceStart uint64) ([]byte, error) {
	return tlv8.Marshal(statusResponse{
		Status: valueConfigStatus{
			IID:            iid,
			Parameters:     params,
			TimeSinceStart: timeSinceStart,
		},
	})
}
```

(The Supported Transition Configuration value is built in the controller, Task 7, because it needs live IIDs.)

- [ ] **Step 4: Run the test to confirm it passes**

Run: `cd ~/src/hap && go test ./adaptive/ -run TestBuildStatusResponse -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd ~/src/hap
git add adaptive/tlv.go adaptive/tlv_test.go
git commit -m "feat(adaptive): encode transition status response"
```

---

## Phase 3: Fork — curve evaluation

### Task 6: Implement curve interpolation

**Files:**
- Create: `~/src/hap/adaptive/curve.go`
- Test: `~/src/hap/adaptive/curve_test.go`

- [ ] **Step 1: Write the failing interpolation test**

Create `~/src/hap/adaptive/curve_test.go`:

```go
package adaptive

import "testing"

func sampleCurve() []curvePoint {
	// Two points: at t=0 temp=200, at t=600000ms (10 min) temp=300.
	// brightnessAdjustmentFactor 0 to keep the test about time interpolation.
	return []curvePoint{
		{Temperature: 200, AdjustmentFactor: 0, TransitionTime: 0, Duration: 0},
		{Temperature: 300, AdjustmentFactor: 0, TransitionTime: 600000, Duration: 0},
	}
}

func TestEvaluateMidpoint(t *testing.T) {
	c := sampleCurve()
	// Halfway through the 10-minute segment -> 250 mired.
	temp, ok := evaluate(c, 300000, 100, brightnessRange{Min: 10, Max: 100})
	if !ok {
		t.Fatal("expected a value within the curve")
	}
	if temp != 250 {
		t.Fatalf("temp = %d, want 250", temp)
	}
}

func TestEvaluateBrightnessAdjustment(t *testing.T) {
	c := []curvePoint{
		{Temperature: 200, AdjustmentFactor: -1, TransitionTime: 0},
		{Temperature: 200, AdjustmentFactor: -1, TransitionTime: 600000},
	}
	// temp 200 + (-1 * clamp(50)) = 150.
	temp, ok := evaluate(c, 300000, 50, brightnessRange{Min: 10, Max: 100})
	if !ok || temp != 150 {
		t.Fatalf("temp = %d ok=%v, want 150 true", temp, ok)
	}
}

func TestEvaluatePastEndReturnsFalse(t *testing.T) {
	c := sampleCurve()
	if _, ok := evaluate(c, 999999999, 100, brightnessRange{Min: 10, Max: 100}); ok {
		t.Fatal("expected ok=false past the end of the curve")
	}
}

func TestEvaluateClampsBrightness(t *testing.T) {
	c := []curvePoint{
		{Temperature: 200, AdjustmentFactor: -1, TransitionTime: 0},
		{Temperature: 200, AdjustmentFactor: -1, TransitionTime: 600000},
	}
	// brightness 5 is below min 10 -> clamps to 10 -> 200 - 10 = 190.
	temp, _ := evaluate(c, 300000, 5, brightnessRange{Min: 10, Max: 100})
	if temp != 190 {
		t.Fatalf("temp = %d, want 190", temp)
	}
}
```

- [ ] **Step 2: Run it to confirm it fails**

Run: `cd ~/src/hap && go test ./adaptive/ -run TestEvaluate -v`
Expected: FAIL — `curvePoint`, `brightnessRange`, `evaluate` undefined.

- [ ] **Step 3: Implement the curve types and evaluator**

Create `~/src/hap/adaptive/curve.go`:

```go
package adaptive

import "math"

// curvePoint is one decoded transition entry. TransitionTime is the time in
// milliseconds to transition from the previous point to this one; Duration is
// how long this point holds before transitioning to the next.
type curvePoint struct {
	Temperature      float64
	AdjustmentFactor float64
	TransitionTime   int64
	Duration         int64
}

type brightnessRange struct {
	Min int
	Max int
}

// evaluate returns the colour temperature in mired for the given offset (ms
// since the curve start) and current brightness. ok is false when offset is
// past the end of the curve.
//
// Port of HAP-NodeJS getCurrentAdaptiveLightingTransitionPoint + scheduleNextUpdate.
func evaluate(curve []curvePoint, offsetMillis int64, brightness int, br brightnessRange) (int, bool) {
	if len(curve) < 2 {
		return 0, false
	}

	var lowerBoundTimeOffset int64
	var lower, upper *curvePoint
	var transitionOffset int64

	for i := 0; i+1 < len(curve); i++ {
		lb := curve[i]
		ub := curve[i+1]
		lowerBoundTimeOffset += lb.TransitionTime
		if offsetMillis >= lowerBoundTimeOffset &&
			offsetMillis <= lowerBoundTimeOffset+lb.Duration+ub.TransitionTime {
			lower = &curve[i]
			upper = &curve[i+1]
			transitionOffset = offsetMillis - lowerBoundTimeOffset
			break
		}
		lowerBoundTimeOffset += lb.Duration
	}

	if lower == nil || upper == nil {
		return 0, false
	}

	var temp, adj float64
	if lower.Duration > 0 && transitionOffset <= lower.Duration {
		temp = lower.Temperature
		adj = lower.AdjustmentFactor
	} else {
		pct := float64(transitionOffset-lower.Duration) / float64(upper.TransitionTime)
		temp = lower.Temperature + (upper.Temperature-lower.Temperature)*pct
		adj = lower.AdjustmentFactor + (upper.AdjustmentFactor-lower.AdjustmentFactor)*pct
	}

	b := brightness
	if b < br.Min {
		b = br.Min
	} else if b > br.Max {
		b = br.Max
	}

	return int(math.Round(temp + adj*float64(b))), true
}
```

- [ ] **Step 4: Run the tests to confirm they pass**

Run: `cd ~/src/hap && go test ./adaptive/ -run TestEvaluate -v`
Expected: PASS (all four).

- [ ] **Step 5: Commit**

```bash
cd ~/src/hap
git add adaptive/curve.go adaptive/curve_test.go
git commit -m "feat(adaptive): piecewise-linear curve evaluation with brightness adjustment"
```

---

## Phase 4: Fork — the controller

### Task 7: Controller construction, characteristics, and Supported-config read

**Files:**
- Create: `~/src/hap/adaptive/controller.go`
- Test: `~/src/hap/adaptive/controller_test.go`

- [ ] **Step 1: Write the failing construction test**

Create `~/src/hap/adaptive/controller_test.go`:

```go
package adaptive

import (
	"testing"

	"github.com/brutella/hap/accessory"
)

func newTestLightbulb() *accessory.Lightbulb {
	a := accessory.NewLightbulb(accessory.Info{Name: "Test"})
	return a
}

func TestNewControllerAddsCharacteristics(t *testing.T) {
	lb := newTestLightbulb()
	// Brightness and ColorTemperature must exist for AL.
	bright := newBrightness()
	ct := newColorTemperature()
	lb.Lightbulb.AddC(bright.C)
	lb.Lightbulb.AddC(ct.C)

	c := NewController(Options{
		Lightbulb:        lb.Lightbulb,
		Brightness:       bright,
		ColorTemperature: ct,
		SetColorTemperature: func(int) error { return nil },
	})
	if c == nil {
		t.Fatal("nil controller")
	}
	if !lb.Lightbulb.ContainsC(c.supported.C) {
		t.Fatal("supported transition characteristic not added")
	}
	if !lb.Lightbulb.ContainsC(c.control.C) {
		t.Fatal("transition control characteristic not added")
	}
	if !lb.Lightbulb.ContainsC(c.count.C) {
		t.Fatal("active transition count characteristic not added")
	}
}
```

Helpers `newBrightness`/`newColorTemperature` wrap the stock characteristics; add them at the top of `controller_test.go`:

```go
import "github.com/brutella/hap/characteristic"

func newBrightness() *characteristic.Brightness         { return characteristic.NewBrightness() }
func newColorTemperature() *characteristic.ColorTemperature { return characteristic.NewColorTemperature() }
```

- [ ] **Step 2: Run it to confirm it fails**

Run: `cd ~/src/hap && go test ./adaptive/ -run TestNewControllerAddsCharacteristics -v`
Expected: FAIL — `NewController`, `Options` undefined.

- [ ] **Step 3: Implement controller construction + Supported-config read**

Create `~/src/hap/adaptive/controller.go`:

```go
package adaptive

import (
	"net/http"
	"sync"
	"time"

	"github.com/brutella/hap/characteristic"
	"github.com/brutella/hap/service"
	"github.com/brutella/hap/tlv8"
)

// Options configures a Controller.
type Options struct {
	// Lightbulb is the service the transition characteristics are added to.
	Lightbulb *service.Lightbulb
	// Brightness and ColorTemperature must already be added to Lightbulb.
	Brightness       *characteristic.Brightness
	ColorTemperature *characteristic.ColorTemperature
	// SetColorTemperature drives the physical device. Called on every tick and
	// on brightness changes while AL is active. Must be safe to call from a
	// goroutine.
	SetColorTemperature func(mired int) error
	// Now returns the current time; defaults to time.Now. Injectable for tests.
	Now func() time.Time
}

// Controller implements Adaptive Lighting for one Lightbulb.
type Controller struct {
	opts Options
	now  func() time.Time

	supported *characteristic.SupportedCharacteristicValueTransitionConfiguration
	control   *characteristic.CharacteristicValueTransitionControl
	count     *characteristic.CharacteristicValueActiveTransitionCount

	mu     sync.Mutex
	active *activeTransition
	timer  *time.Timer
}

// activeTransition is the running schedule (also the serialized form).
type activeTransition struct {
	IID              uint64
	BrightnessIID    uint64
	TransitionID     []byte
	StartTimeBuf     []byte
	Unknown3         []byte
	StartMillis      int64
	TimeOffsetMillis int64 // localNow - startMillis at setup, absorbs clock skew
	Curve            []curvePoint
	Range            brightnessRange
	UpdateInterval   time.Duration
	NotifyThreshold  time.Duration
}

func NewController(opts Options) *Controller {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	c := &Controller{
		opts:      opts,
		now:       opts.Now,
		supported: characteristic.NewSupportedCharacteristicValueTransitionConfiguration(),
		control:   characteristic.NewCharacteristicValueTransitionControl(),
		count:     characteristic.NewCharacteristicValueActiveTransitionCount(),
	}

	opts.Lightbulb.AddC(c.supported.C)
	opts.Lightbulb.AddC(c.control.C)
	opts.Lightbulb.AddC(c.count.C)

	// Supported config is computed lazily so characteristic IIDs are assigned.
	c.supported.ValueRequestFunc = func(*http.Request) (interface{}, int) {
		b, err := c.buildSupportedConfig()
		if err != nil {
			return nil, -70402
		}
		return base64Std(b), 0
	}

	// Transition Control: write-response handler (Task 8) and read handler.
	c.control.SetValueRequestFunc = func(v interface{}, r *http.Request) (interface{}, int) {
		return c.handleControlWrite(v, r)
	}
	c.control.ValueRequestFunc = func(*http.Request) (interface{}, int) {
		b, err := c.buildControlReadValue()
		if err != nil {
			return nil, -70402
		}
		return base64Std(b), 0
	}

	return c
}

func (c *Controller) buildSupportedConfig() ([]byte, error) {
	return tlv8.Marshal(supportedConfig{
		Entries: []supportedEntry{
			{IID: c.opts.Brightness.C.Id, TransitionType: transitionTypeBrightness},
			{IID: c.opts.ColorTemperature.C.Id, TransitionType: transitionTypeColorTemperature},
		},
	})
}
```

Add a tiny base64 helper at the bottom of `controller.go`:

```go
import "encoding/base64" // add to import block

func base64Std(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
```

`handleControlWrite`, `buildControlReadValue` are added in Task 8; add temporary stubs so this task compiles and the construction test passes:

```go
func (c *Controller) handleControlWrite(v interface{}, r *http.Request) (interface{}, int) {
	return nil, 0
}
func (c *Controller) buildControlReadValue() ([]byte, error) { return []byte{}, nil }
```

- [ ] **Step 4: Run the test to confirm it passes**

Run: `cd ~/src/hap && go test ./adaptive/ -run TestNewControllerAddsCharacteristics -v`
Expected: PASS. (If `ContainsC` does not exist on the service, replace the assertions with a check that `c.supported.C.Id == 0` before server build and that the characteristic is in `lb.Lightbulb.Cs` — verify the actual field/method name in `service/lightbulb.go`.)

- [ ] **Step 5: Commit**

```bash
cd ~/src/hap
git add adaptive/controller.go adaptive/controller_test.go
git commit -m "feat(adaptive): controller construction and supported-config read"
```

### Task 8: Handle the Transition Control write (enable / disable / renew)

**Files:**
- Modify: `~/src/hap/adaptive/controller.go` (replace the stubs)
- Test: `~/src/hap/adaptive/controller_test.go` (append)

- [ ] **Step 1: Write the failing enable test**

Append to `~/src/hap/adaptive/controller_test.go`:

```go
import (
	"encoding/base64"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/brutella/hap/tlv8"
)

func enablePayload(ctIID, brightIID uint64, startMillis int64) string {
	var start [8]byte
	since := uint64(startMillis - hapEpoch.UnixMilli())
	for i := 0; i < 8; i++ {
		start[i] = byte(since >> (8 * i))
	}
	w := controlWrite{Update: &updateRequest{Config: valueTransitionConfig{
		IID:     ctIID,
		Enabled: 1,
		Parameters: transitionParameters{
			TransitionID: make([]byte, 16),
			StartTime:    start[:],
		},
		Curve: curveConfig{
			Entries: []curveEntryTLV{
				{AdjustmentFactor: 0, Temperature: 200, TransitionOffset: 0},
				{AdjustmentFactor: 0, Temperature: 300, TransitionOffset: 600000},
			},
			AdjustmentIID:   brightIID,
			MultiplierRange: multiplierRange{Min: 10, Max: 100},
		},
		UpdateInterval:          60000,
		NotifyIntervalThreshold: 600000,
	}}}
	b, _ := tlv8.Marshal(w)
	return base64.StdEncoding.EncodeToString(b)
}

func TestControlWriteEnables(t *testing.T) {
	lb := newTestLightbulb()
	bright := newBrightness()
	ct := newColorTemperature()
	lb.Lightbulb.AddC(bright.C)
	lb.Lightbulb.AddC(ct.C)
	ct.C.Id = 7
	bright.C.Id = 3
	bright.SetValue(100)

	var commanded int64
	c := NewController(Options{
		Lightbulb: lb.Lightbulb, Brightness: bright, ColorTemperature: ct,
		SetColorTemperature: func(m int) error { atomic.StoreInt64(&commanded, int64(m)); return nil },
		Now:                 func() time.Time { return hapEpoch.Add(5 * time.Minute) },
	})

	resp, code := c.handleControlWrite(enablePayload(7, 3, hapEpoch.UnixMilli()), (*http.Request)(nil))
	if code != 0 {
		t.Fatalf("code = %d, want 0", code)
	}
	if resp == nil {
		t.Fatal("expected a write-response body")
	}
	if !c.IsActive() {
		t.Fatal("expected AL active after enable")
	}
	if c.count.Value() != 1 {
		t.Fatalf("active count = %d, want 1", c.count.Value())
	}
	// 5 minutes into a 0..10min ramp from 200 to 300 -> 250.
	if got := atomic.LoadInt64(&commanded); got != 250 {
		t.Fatalf("commanded mired = %d, want 250", got)
	}
}

func TestControlWriteDisables(t *testing.T) {
	lb := newTestLightbulb()
	bright := newBrightness()
	ct := newColorTemperature()
	lb.Lightbulb.AddC(bright.C)
	lb.Lightbulb.AddC(ct.C)
	ct.C.Id = 7
	bright.C.Id = 3

	c := NewController(Options{
		Lightbulb: lb.Lightbulb, Brightness: bright, ColorTemperature: ct,
		SetColorTemperature: func(int) error { return nil },
		Now:                 func() time.Time { return hapEpoch.Add(time.Minute) },
	})
	c.handleControlWrite(enablePayload(7, 3, hapEpoch.UnixMilli()), (*http.Request)(nil))

	// iid-only update = disable.
	w := controlWrite{Update: &updateRequest{Config: valueTransitionConfig{IID: 7}}}
	b, _ := tlv8.Marshal(w)
	c.handleControlWrite(base64.StdEncoding.EncodeToString(b), (*http.Request)(nil))
	if c.IsActive() {
		t.Fatal("expected AL inactive after disable")
	}
	if c.count.Value() != 0 {
		t.Fatalf("active count = %d, want 0", c.count.Value())
	}
}
```

- [ ] **Step 2: Run it to confirm it fails**

Run: `cd ~/src/hap && go test ./adaptive/ -run TestControlWrite -v`
Expected: FAIL — stub returns nil and AL never activates.

- [ ] **Step 3: Implement the write handler, enable/disable, and the tick**

In `~/src/hap/adaptive/controller.go`, replace the two stub functions with:

```go
// IsActive reports whether an Adaptive Lighting transition is running.
func (c *Controller) IsActive() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.active != nil
}

func (c *Controller) handleControlWrite(v interface{}, r *http.Request) (interface{}, int) {
	str, ok := v.(string)
	if !ok {
		return nil, -70410
	}
	raw, err := base64.StdEncoding.DecodeString(str)
	if err != nil {
		return nil, -70410
	}

	var w controlWrite
	if err := tlv8.Unmarshal(raw, &w); err != nil {
		return nil, -70410
	}

	if w.Update != nil {
		cfg := w.Update.Config
		// An update without the enable flag (iid only) means disable.
		if cfg.Enabled != 1 || len(cfg.Curve.Entries) == 0 {
			c.Disable()
			b, _ := tlv8.Marshal(struct {
				Update []byte `tlv8:"2"`
			}{Update: []byte{}})
			return base64Std(b), 0
		}
		if err := c.enable(cfg); err != nil {
			c.Disable()
			return nil, -70402
		}
		c.mu.Lock()
		params, timeSince := c.statusLocked()
		iid := c.active.IID
		c.mu.Unlock()
		body, _ := buildStatusResponse(iid, params, timeSince)
		return base64Std(body), 0
	}

	if w.Read != nil {
		b, err := c.buildControlReadValue()
		if err != nil {
			return nil, -70402
		}
		return base64Std(b), 0
	}

	return base64Std([]byte{}), 0
}

func (c *Controller) enable(cfg valueTransitionConfig) error {
	curve := make([]curvePoint, len(cfg.Curve.Entries))
	for i, e := range cfg.Curve.Entries {
		curve[i] = curvePoint{
			Temperature:      float64(e.Temperature),
			AdjustmentFactor: float64(e.AdjustmentFactor),
			TransitionTime:   int64(e.TransitionOffset),
			Duration:         int64(e.Duration),
		}
	}
	startMillis := startTimeMillis(cfg.Parameters.StartTime)
	updateInterval := time.Duration(cfg.UpdateInterval) * time.Millisecond
	if updateInterval <= 0 {
		updateInterval = 60 * time.Second
	}

	c.mu.Lock()
	c.active = &activeTransition{
		IID:              cfg.IID,
		BrightnessIID:    cfg.Curve.AdjustmentIID,
		TransitionID:     cfg.Parameters.TransitionID,
		StartTimeBuf:     cfg.Parameters.StartTime,
		Unknown3:         cfg.Parameters.Unknown3,
		StartMillis:      startMillis,
		TimeOffsetMillis: c.now().UnixMilli() - startMillis,
		Curve:            curve,
		Range:            brightnessRange{Min: int(cfg.Curve.MultiplierRange.Min), Max: int(cfg.Curve.MultiplierRange.Max)},
		UpdateInterval:   updateInterval,
		NotifyThreshold:  time.Duration(cfg.NotifyIntervalThreshold) * time.Millisecond,
	}
	c.mu.Unlock()

	c.count.SetValue(1)
	c.tick()
	return nil
}

// tick computes and applies the colour temperature for "now" and schedules the
// next update. Safe to call while active; a no-op if AL was disabled.
func (c *Controller) tick() {
	c.mu.Lock()
	if c.active == nil {
		c.mu.Unlock()
		return
	}
	at := c.active
	offset := c.now().UnixMilli() - at.TimeOffsetMillis - at.StartMillis
	brightness := c.opts.Brightness.Value()
	mired, ok := evaluate(at.Curve, offset, brightness, at.Range)
	interval := at.UpdateInterval
	c.mu.Unlock()

	if !ok {
		c.Disable() // curve exhausted
		return
	}

	mired = clampCT(c.opts.ColorTemperature, mired)

	if err := c.opts.SetColorTemperature(mired); err == nil {
		// Reflect on the characteristic for the Home UI without re-triggering a
		// remote-write callback (SetValue uses a nil request).
		c.opts.ColorTemperature.SetValue(mired)
	}

	c.mu.Lock()
	if c.active != nil {
		if c.timer != nil {
			c.timer.Stop()
		}
		c.timer = time.AfterFunc(interval, c.tick)
	}
	c.mu.Unlock()
}

// Disable stops Adaptive Lighting and clears all state.
func (c *Controller) Disable() {
	c.mu.Lock()
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	wasActive := c.active != nil
	c.active = nil
	c.mu.Unlock()

	if wasActive {
		c.count.SetValue(0)
	}
}

// statusLocked builds the parameters + time-since-start for a response. Caller
// must hold c.mu and c.active must be non-nil.
func (c *Controller) statusLocked() (transitionParameters, uint64) {
	at := c.active
	params := transitionParameters{
		TransitionID: at.TransitionID,
		StartTime:    at.StartTimeBuf,
		Unknown3:     at.Unknown3,
	}
	timeSince := c.now().UnixMilli() - at.TimeOffsetMillis - at.StartMillis
	if timeSince < 0 {
		timeSince = 0
	}
	return params, uint64(timeSince)
}

func clampCT(ct *characteristic.ColorTemperature, v int) int {
	if v < ct.MinVal {
		return ct.MinVal
	}
	if v > ct.MaxVal {
		return ct.MaxVal
	}
	return v
}
```

Note: confirm the field names `ct.MinVal`/`ct.MaxVal` against `characteristic/color_temperature.go` (they may be `MinValue`/`MaxValue` or stored on the embedded `*Int`). Adjust to the real names.

- [ ] **Step 4: Run the tests to confirm they pass**

Run: `cd ~/src/hap && go test ./adaptive/ -run TestControlWrite -v`
Expected: PASS (enable + disable).

- [ ] **Step 5: Commit**

```bash
cd ~/src/hap
git add adaptive/controller.go adaptive/controller_test.go
git commit -m "feat(adaptive): enable/disable transition control + colour-temperature tick"
```

### Task 9: React to brightness changes immediately

**Files:**
- Modify: `~/src/hap/adaptive/controller.go`
- Test: `~/src/hap/adaptive/controller_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `controller_test.go`:

```go
func TestBrightnessChangeRecomputes(t *testing.T) {
	lb := newTestLightbulb()
	bright := newBrightness()
	ct := newColorTemperature()
	lb.Lightbulb.AddC(bright.C)
	lb.Lightbulb.AddC(ct.C)
	ct.C.Id, bright.C.Id = 7, 3
	bright.SetValue(100)

	var commanded int64
	c := NewController(Options{
		Lightbulb: lb.Lightbulb, Brightness: bright, ColorTemperature: ct,
		SetColorTemperature: func(m int) error { atomic.StoreInt64(&commanded, int64(m)); return nil },
		Now:                 func() time.Time { return hapEpoch.Add(5 * time.Minute) },
	})
	// Curve where adjustment factor is -1, temp flat 300 -> mired = 300 - brightness.
	w := controlWrite{Update: &updateRequest{Config: valueTransitionConfig{
		IID: 7, Enabled: 1,
		Parameters: transitionParameters{TransitionID: make([]byte, 16), StartTime: make([]byte, 8)},
		Curve: curveConfig{
			Entries: []curveEntryTLV{
				{AdjustmentFactor: -1, Temperature: 300, TransitionOffset: 0},
				{AdjustmentFactor: -1, Temperature: 300, TransitionOffset: 600000},
			},
			AdjustmentIID: 3, MultiplierRange: multiplierRange{Min: 10, Max: 100},
		},
		UpdateInterval: 60000, NotifyIntervalThreshold: 600000,
	}}}
	b, _ := tlv8.Marshal(w)
	c.handleControlWrite(base64.StdEncoding.EncodeToString(b), (*http.Request)(nil))
	if atomic.LoadInt64(&commanded) != 200 { // 300 - 100
		t.Fatalf("after enable commanded = %d, want 200", atomic.LoadInt64(&commanded))
	}

	bright.SetValue(50)
	c.HandleBrightnessChanged()
	if atomic.LoadInt64(&commanded) != 250 { // 300 - 50
		t.Fatalf("after dim commanded = %d, want 250", atomic.LoadInt64(&commanded))
	}
}
```

- [ ] **Step 2: Run it to confirm it fails**

Run: `cd ~/src/hap && go test ./adaptive/ -run TestBrightnessChangeRecomputes -v`
Expected: FAIL — `HandleBrightnessChanged` undefined.

- [ ] **Step 3: Implement it**

Append to `controller.go`:

```go
// HandleBrightnessChanged recomputes the colour temperature for the current
// brightness. The host app calls this when brightness changes (warm-on-dim).
// No-op when AL is inactive.
func (c *Controller) HandleBrightnessChanged() {
	if c.IsActive() {
		c.tick()
	}
}
```

- [ ] **Step 4: Run the test to confirm it passes**

Run: `cd ~/src/hap && go test ./adaptive/ -run TestBrightnessChangeRecomputes -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd ~/src/hap
git add adaptive/controller.go adaptive/controller_test.go
git commit -m "feat(adaptive): recompute colour temperature on brightness change"
```

### Task 10: Persistence (serialize / restore the active transition)

**Files:**
- Modify: `~/src/hap/adaptive/controller.go`
- Test: `~/src/hap/adaptive/controller_test.go` (append)

- [ ] **Step 1: Write the failing persistence test**

Append to `controller_test.go`:

```go
func TestSerializeRestore(t *testing.T) {
	lb := newTestLightbulb()
	bright := newBrightness()
	ct := newColorTemperature()
	lb.Lightbulb.AddC(bright.C)
	lb.Lightbulb.AddC(ct.C)
	ct.C.Id, bright.C.Id = 7, 3
	bright.SetValue(100)

	c := NewController(Options{
		Lightbulb: lb.Lightbulb, Brightness: bright, ColorTemperature: ct,
		SetColorTemperature: func(int) error { return nil },
		Now:                 func() time.Time { return hapEpoch.Add(5 * time.Minute) },
	})
	c.handleControlWrite(enablePayload(7, 3, hapEpoch.UnixMilli()), (*http.Request)(nil))

	blob, ok := c.Serialize()
	if !ok {
		t.Fatal("expected serialized state when active")
	}

	// Fresh controller, restore.
	lb2 := newTestLightbulb()
	bright2 := newBrightness()
	ct2 := newColorTemperature()
	lb2.Lightbulb.AddC(bright2.C)
	lb2.Lightbulb.AddC(ct2.C)
	ct2.C.Id, bright2.C.Id = 7, 3
	bright2.SetValue(100)

	var commanded int64
	c2 := NewController(Options{
		Lightbulb: lb2.Lightbulb, Brightness: bright2, ColorTemperature: ct2,
		SetColorTemperature: func(m int) error { atomic.StoreInt64(&commanded, int64(m)); return nil },
		Now:                 func() time.Time { return hapEpoch.Add(5 * time.Minute) },
	})
	if err := c2.Restore(blob); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if !c2.IsActive() {
		t.Fatal("expected active after restore")
	}
	if atomic.LoadInt64(&commanded) != 250 {
		t.Fatalf("restored commanded = %d, want 250", atomic.LoadInt64(&commanded))
	}
}
```

- [ ] **Step 2: Run it to confirm it fails**

Run: `cd ~/src/hap && go test ./adaptive/ -run TestSerializeRestore -v`
Expected: FAIL — `Serialize`/`Restore` undefined.

- [ ] **Step 3: Implement serialize/restore with JSON**

Append to `controller.go` (add `encoding/json` to imports):

```go
// Serialize returns a JSON blob of the active transition, or ok=false if AL is
// inactive. The host app persists this (e.g. in the HAP store) and passes it to
// Restore on startup.
func (c *Controller) Serialize() ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active == nil {
		return nil, false
	}
	b, err := json.Marshal(c.active)
	if err != nil {
		return nil, false
	}
	return b, true
}

// Restore re-activates a transition previously returned by Serialize and resumes
// ticking. A nil/empty blob is a no-op.
func (c *Controller) Restore(blob []byte) error {
	if len(blob) == 0 {
		return nil
	}
	var at activeTransition
	if err := json.Unmarshal(blob, &at); err != nil {
		return err
	}
	c.mu.Lock()
	c.active = &at
	c.mu.Unlock()
	c.count.SetValue(1)
	c.tick()
	return nil
}
```

Make persistence robust by having the controller notify the host on every state change. Add a hook to `Options`:

```go
// OnStateChange (optional) is called after the active transition changes
// (enable, disable, renew). The host re-persists Serialize() output.
OnStateChange func()
```

And call `c.opts.OnStateChange` (if non-nil) at the end of `enable` and `Disable` (outside the lock).

- [ ] **Step 4: Run the test to confirm it passes**

Run: `cd ~/src/hap && go test ./adaptive/ -run TestSerializeRestore -v`
Expected: PASS.

- [ ] **Step 5: Run the whole adaptive package with race detection**

Run: `cd ~/src/hap && go test -race ./adaptive/`
Expected: PASS, no race warnings.

- [ ] **Step 6: Commit**

```bash
cd ~/src/hap
git add adaptive/controller.go adaptive/controller_test.go
git commit -m "feat(adaptive): persist and restore active transition across restarts"
```

### Task 11: Implement the read-current-configuration response

**Files:**
- Modify: `~/src/hap/adaptive/controller.go` (`buildControlReadValue`)
- Test: `~/src/hap/adaptive/controller_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `controller_test.go`:

```go
func TestControlReadValueWhenActive(t *testing.T) {
	lb := newTestLightbulb()
	bright := newBrightness()
	ct := newColorTemperature()
	lb.Lightbulb.AddC(bright.C)
	lb.Lightbulb.AddC(ct.C)
	ct.C.Id, bright.C.Id = 7, 3
	bright.SetValue(100)

	c := NewController(Options{
		Lightbulb: lb.Lightbulb, Brightness: bright, ColorTemperature: ct,
		SetColorTemperature: func(int) error { return nil },
		Now:                 func() time.Time { return hapEpoch.Add(time.Minute) },
	})
	c.handleControlWrite(enablePayload(7, 3, hapEpoch.UnixMilli()), (*http.Request)(nil))

	b, err := c.buildControlReadValue()
	if err != nil {
		t.Fatalf("read value: %v", err)
	}
	if len(b) == 0 {
		t.Fatal("expected non-empty read value when active")
	}
}

func TestControlReadValueWhenInactive(t *testing.T) {
	lb := newTestLightbulb()
	bright := newBrightness()
	ct := newColorTemperature()
	lb.Lightbulb.AddC(bright.C)
	lb.Lightbulb.AddC(ct.C)
	c := NewController(Options{
		Lightbulb: lb.Lightbulb, Brightness: bright, ColorTemperature: ct,
		SetColorTemperature: func(int) error { return nil },
	})
	b, err := c.buildControlReadValue()
	if err != nil || len(b) != 0 {
		t.Fatalf("expected empty read value when inactive, got %d bytes err=%v", len(b), err)
	}
}
```

- [ ] **Step 2: Run it to confirm it fails**

Run: `cd ~/src/hap && go test ./adaptive/ -run TestControlReadValue -v`
Expected: FAIL — current `buildControlReadValue` returns empty even when active.

- [ ] **Step 3: Implement the read response**

Replace `buildControlReadValue` in `controller.go`:

```go
// readResponse mirrors the UPDATE configuration shape, wrapped in tag 1.
type readResponse struct {
	Config readConfig `tlv8:"1"`
}

type readConfig struct {
	IID                     uint64               `tlv8:"1"`
	Parameters              transitionParameters `tlv8:"2"`
	Unknown3                byte                 `tlv8:"3"`
	Curve                   curveConfig          `tlv8:"5"`
	UpdateInterval          uint16               `tlv8:"6"`
	NotifyIntervalThreshold uint32               `tlv8:"8"`
}

func (c *Controller) buildControlReadValue() ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active == nil {
		return []byte{}, nil
	}
	at := c.active

	entries := make([]curveEntryTLV, len(at.Curve))
	for i, p := range at.Curve {
		entries[i] = curveEntryTLV{
			AdjustmentFactor: float32(p.AdjustmentFactor),
			Temperature:      float32(p.Temperature),
			TransitionOffset: uint32(p.TransitionTime),
			Duration:         uint32(p.Duration),
		}
	}

	return tlv8.Marshal(readResponse{Config: readConfig{
		IID: at.IID,
		Parameters: transitionParameters{
			TransitionID: at.TransitionID,
			StartTime:    at.StartTimeBuf,
			Unknown3:     at.Unknown3,
		},
		Unknown3: 1,
		Curve: curveConfig{
			Entries:         entries,
			AdjustmentIID:   at.BrightnessIID,
			MultiplierRange: multiplierRange{Min: uint32(at.Range.Min), Max: uint32(at.Range.Max)},
		},
		UpdateInterval:          uint16(at.UpdateInterval.Milliseconds()),
		NotifyIntervalThreshold: uint32(at.NotifyThreshold.Milliseconds()),
	}})
}
```

This is where the Task 1 float32 fix is required.

- [ ] **Step 4: Run the tests to confirm they pass**

Run: `cd ~/src/hap && go test ./adaptive/ -run TestControlReadValue -v`
Expected: PASS (both).

- [ ] **Step 5: Run the full fork test suite with race detection**

Run: `cd ~/src/hap && go test -race ./...`
Expected: PASS across `tlv8`, `characteristic`, `adaptive`, and existing packages.

- [ ] **Step 6: Commit**

```bash
cd ~/src/hap
git add adaptive/controller.go adaptive/controller_test.go
git commit -m "feat(adaptive): encode read-current-configuration response"
```

---

## Phase 5: App — wire the controller into keylight-hap

All Phase 5 commits happen in `/Users/hugh/src/keylight-hap`.

### Task 12: Add the controller to each light accessory

**Files:**
- Modify: `internal/bridge/lightbulb.go`

- [ ] **Step 1: Add the controller field and an option flag**

In `internal/bridge/lightbulb.go`, extend the imports and the struct:

```go
import (
	// ...existing...
	"github.com/brutella/hap/adaptive"
)

type lightAccessory struct {
	a      *accessory.Lightbulb
	on     *characteristic.On
	bright *characteristic.Brightness
	ct     *characteristic.ColorTemperature

	ctx    context.Context
	client *elgato.Client

	al            *adaptive.Controller
	lastCmdTemp   int  // last temperature commanded by AL or HomeKit
	hasLastCmd    bool
}
```

- [ ] **Step 2: Construct the controller in newLightAccessory**

In `newLightAccessory`, after `la := &lightAccessory{...}` and before the callback wiring, add:

```go
la.al = adaptive.NewController(adaptive.Options{
	Lightbulb:        a.Lightbulb,
	Brightness:       bright,
	ColorTemperature: ct,
	SetColorTemperature: func(mired int) error {
		la.lastCmdTemp = mired
		la.hasLastCmd = true
		_, err := client.Set(ctx, elgato.Patch{Temperature: &mired})
		return err
	},
})
```

- [ ] **Step 3: Disable AL on manual HomeKit temperature writes; track commands**

Replace `onTemperatureWrite` so a manual write turns AL off and records the command:

```go
func (l *lightAccessory) onTemperatureWrite(v int) {
	if l.al != nil {
		l.al.Disable()
	}
	l.lastCmdTemp = v
	l.hasLastCmd = true
	if _, err := l.client.Set(l.ctx, elgato.Patch{Temperature: &v}); err != nil {
		slog.Warn("set temperature failed", "err", err)
	}
}
```

- [ ] **Step 4: Notify the controller on brightness writes (warm-on-dim)**

Replace `onBrightnessWrite`:

```go
func (l *lightAccessory) onBrightnessWrite(v int) {
	if _, err := l.client.Set(l.ctx, elgato.Patch{Brightness: &v}); err != nil {
		slog.Warn("set brightness failed", "err", err)
	}
	if l.al != nil {
		l.al.HandleBrightnessChanged()
	}
}
```

- [ ] **Step 5: Build the app**

Run: `cd /Users/hugh/src/keylight-hap && go build ./...`
Expected: builds clean.

- [ ] **Step 6: Commit**

```bash
cd /Users/hugh/src/keylight-hap
git add internal/bridge/lightbulb.go
git commit -m "feat(bridge): wire Adaptive Lighting controller onto each light"
```

### Task 13: Disable AL on external temperature changes in the poll loop

**Files:**
- Modify: `internal/bridge/lightbulb.go` (`sync`)
- Modify: `internal/bridge/bridge.go` (no logic change expected; verify `sync` is the only caller)

- [ ] **Step 1: Add the divergence check to sync**

In `internal/bridge/lightbulb.go`, update `sync` to detect external temperature changes. Add a tolerance constant at the top of the file:

```go
// ctExternalTolerance is the mired gap beyond which a polled temperature that
// AL/HomeKit did not command is treated as an external change and disables AL.
const ctExternalTolerance = 3
```

Then in `sync`, after the existing clamp of `t`, before `l.ct.SetValue(t)`:

```go
	if l.al != nil && l.al.IsActive() && l.hasLastCmd {
		if diff := t - l.lastCmdTemp; diff > ctExternalTolerance || diff < -ctExternalTolerance {
			slog.Info("external colour-temperature change; disabling adaptive lighting",
				"polled", t, "lastCommanded", l.lastCmdTemp)
			l.al.Disable()
		}
	}
```

- [ ] **Step 2: Build**

Run: `cd /Users/hugh/src/keylight-hap && go build ./...`
Expected: builds clean.

- [ ] **Step 3: Commit**

```bash
cd /Users/hugh/src/keylight-hap
git add internal/bridge/lightbulb.go
git commit -m "feat(bridge): disable adaptive lighting on external temperature change"
```

### Task 14: Persist AL state in the HAP store

**Files:**
- Modify: `internal/bridge/lightbulb.go` (OnStateChange + a Store handle)
- Modify: `internal/bridge/bridge.go` (pass the store; restore on startup)

- [ ] **Step 1: Thread a Store + key into the accessory**

In `internal/bridge/bridge.go`, the store is created at `bridge.go:70` (`hap.NewFsStore(opts.StateDir)`). Create it before building accessories and pass it to `newLightAccessory`. Change `New` so the store is constructed first:

```go
	store := hap.NewFsStore(opts.StateDir)
	// ...
	la := newLightAccessory(ctx, accessoryName(info, t.Name), info, st, t.Client, store)
```

And pass the same `store` to `hap.NewServer(store, ...)` (reuse the variable instead of constructing twice).

- [ ] **Step 2: Persist on state change and restore on construct**

In `internal/bridge/lightbulb.go`, change `newLightAccessory`'s signature to accept `store hap.Store` and use a per-light key. Add to the `adaptive.Options`:

```go
	alKey := "adaptive-" + info.SerialNumber
	la.al = adaptive.NewController(adaptive.Options{
		Lightbulb:        a.Lightbulb,
		Brightness:       bright,
		ColorTemperature: ct,
		SetColorTemperature: func(mired int) error {
			la.lastCmdTemp = mired
			la.hasLastCmd = true
			_, err := client.Set(ctx, elgato.Patch{Temperature: &mired})
			return err
		},
		OnStateChange: func() {
			if blob, ok := la.al.Serialize(); ok {
				_ = store.Set(alKey, blob)
			} else {
				_ = store.Delete(alKey)
			}
		},
	})
	if blob, err := store.Get(alKey); err == nil && len(blob) > 0 {
		if err := la.al.Restore(blob); err != nil {
			slog.Warn("restore adaptive lighting failed", "err", err)
			_ = store.Delete(alKey)
		}
	}
```

Add `"github.com/brutella/hap"` to imports in `lightbulb.go` for the `hap.Store` type.

- [ ] **Step 3: Build and run existing tests**

Run: `cd /Users/hugh/src/keylight-hap && go build ./... && go test ./...`
Expected: builds; existing bridge tests still pass (update `newLightAccessory` call sites in tests to pass a store — use `hap.NewFsStore(t.TempDir())`).

- [ ] **Step 4: Commit**

```bash
cd /Users/hugh/src/keylight-hap
git add internal/bridge/lightbulb.go internal/bridge/bridge.go internal/bridge/bridge_test.go
git commit -m "feat(bridge): persist adaptive lighting state in the HAP store"
```

### Task 15: Integration test — AL drives the fake device

**Files:**
- Create: `internal/bridge/adaptive_test.go`

- [ ] **Step 1: Inspect the existing fake device + bridge test helpers**

Read `internal/elgato/fakedevice.go` and `internal/bridge/bridge_test.go` to reuse the existing httptest fake Key Light and accessory construction helpers. Note the constructor used to make a `*elgato.Client` against the fake.

- [ ] **Step 2: Write the integration test**

Create `internal/bridge/adaptive_test.go`. Build a light accessory against the fake device, enable AL via the controller with a short two-point curve and an injected clock, and assert the fake device received the interpolated temperature. Use the fork's `adaptive` test pattern but drive through `lightAccessory`:

```go
package bridge

import (
	"context"
	"testing"
	"time"

	"github.com/brutella/hap"
	"github.com/hughobrien/keylight-hap/internal/elgato"
)

func TestAdaptiveLightingDrivesDevice(t *testing.T) {
	fake := elgato.NewFakeDevice() // adjust to the real fake constructor
	defer fake.Close()
	client := fake.Client()        // adjust to the real accessor

	ctx := context.Background()
	info, _ := client.Info(ctx)
	st, _ := client.Get(ctx)
	store := hap.NewFsStore(t.TempDir())
	la := newLightAccessory(ctx, "Test", info, st, client, store)

	// Drive AL directly via the controller using the exported test seam.
	// Set a known brightness, enable a flat curve, and assert the device temp.
	la.bright.SetValue(100)
	enableFlatCurve(t, la) // helper: writes a controlWrite via la.al's control characteristic

	got, _ := client.Get(ctx)
	if got.Temperature == 0 {
		t.Fatalf("device temperature was not driven by AL")
	}
}
```

Because the TLV control types are unexported in the fork's `adaptive` package, drive the enable in this test by writing a base64 payload through the public characteristic: capture a known-good payload (the Task 4 golden file) or expose a small test helper in the `adaptive` package (`adaptive.EnableForTest(c *Controller, ctIID, brightIID uint64, curve ...)`). Prefer adding an exported test helper in the fork:

```go
// in ~/src/hap/adaptive/controller.go
// EnableForTest enables a simple flat-temperature transition. Test-only seam.
func EnableForTest(c *Controller, ctIID, brightIID uint64, temp float32) {
	c.enable(valueTransitionConfig{
		IID: ctIID, Enabled: 1,
		Parameters: transitionParameters{TransitionID: make([]byte, 16), StartTime: make([]byte, 8)},
		Curve: curveConfig{
			Entries: []curveEntryTLV{
				{Temperature: temp, TransitionOffset: 0},
				{Temperature: temp, TransitionOffset: 3600000},
			},
			AdjustmentIID: brightIID, MultiplierRange: multiplierRange{Min: 10, Max: 100},
		},
		UpdateInterval: 60000, NotifyIntervalThreshold: 600000,
	})
}
```

Then in the app test call `adaptive.EnableForTest(la.al, la.ct.C.Id, la.bright.C.Id, 250)` and assert `client.Get(ctx)` reports ~250 mired. Commit the helper in the fork first (amend Task 11's package or a small follow-up commit).

- [ ] **Step 3: Run the test**

Run: `cd /Users/hugh/src/keylight-hap && go test ./internal/bridge/ -run TestAdaptiveLighting -v`
Expected: PASS — device temperature driven to ~250.

- [ ] **Step 4: Run the full app suite with race**

Run: `cd /Users/hugh/src/keylight-hap && go test -race ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /Users/hugh/src/keylight-hap
git add internal/bridge/adaptive_test.go
git commit -m "test(bridge): adaptive lighting drives the fake device"
```

---

## Phase 6: Finalize fork dependency and docs

### Task 16: Publish the fork and pin keylight-hap to it

**Files:**
- Fork: push branch + tag
- Modify: `/Users/hugh/src/keylight-hap/go.mod`, `flake.nix` (vendorHash)

- [ ] **Step 1: Push the fork branch and confirm CI/tests**

```bash
cd ~/src/hap
go test -race ./...
git push origin adaptive-lighting
```

Expected: tests pass; branch pushed.

- [ ] **Step 2: Pin keylight-hap to the fork commit**

Get the pushed commit hash (`git -C ~/src/hap rev-parse HEAD`). In `/Users/hugh/src/keylight-hap`, replace the local path replace with a versioned one:

```bash
cd /Users/hugh/src/keylight-hap
# Replace the ../hap replace line with a pseudo-version pointing at the fork commit:
go mod edit -replace github.com/brutella/hap=github.com/hughobrien/hap@<commit-hash>
go mod tidy
go build ./... && go test ./...
```

Expected: resolves the pseudo-version, builds, tests pass.

- [ ] **Step 3: Refresh the flake vendorHash**

Per the README's documented procedure: set `vendorHash = pkgs.lib.fakeHash;` in `flake.nix`, run `nix build .#keylight-hap`, copy the reported `got:` hash back into `flake.nix`.

```bash
cd /Users/hugh/src/keylight-hap
# edit flake.nix vendorHash -> fakeHash
nix build .#keylight-hap 2>&1 | grep -A1 "got:"
# paste the got: hash into flake.nix
nix build .#keylight-hap
```

Expected: nix build succeeds with the real hash.

- [ ] **Step 4: Commit**

```bash
cd /Users/hugh/src/keylight-hap
git add go.mod go.sum flake.nix
git commit -m "build: pin hap fork with adaptive lighting; refresh vendorHash"
```

### Task 17: Documentation

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Add an Adaptive Lighting section**

In `README.md`, under "What's covered", add a bullet and a dedicated section:

```markdown
- **Adaptive Lighting:** each light supports HomeKit Adaptive Lighting. Enable
  it on the bulb in the Home app; a home hub (Apple TV / HomePod) pushes a daily
  colour-temperature schedule and the daemon tracks it, warming the light as it
  dims. Manually setting the temperature (in Home or via the Elgato app / Stream
  Deck) turns Adaptive Lighting off until you re-enable it. The active schedule
  is persisted in the state directory and resumes across restarts. Requires a
  home hub, like all HomeKit Adaptive Lighting.
```

- [ ] **Step 2: Remove the stale implication in "Known limitations"**

The "White temperature only" bullet remains accurate (no hue/sat), but ensure nothing claims temperature is static/uncontrolled beyond manual writes. Adjust wording if needed.

- [ ] **Step 3: Commit**

```bash
cd /Users/hugh/src/keylight-hap
git add README.md
git commit -m "docs: document Adaptive Lighting support"
```

### Task 18: Manual end-to-end verification

**Files:** none (verification only)

- [ ] **Step 1: Run against a real Key Light**

Build and run the daemon, pair in Home, and on a Key Light tile enable Adaptive Lighting (requires a home hub). Confirm: the toggle appears; temperature shifts over time; dimming warms the light; manually setting temperature turns AL off; re-enabling restores it; restarting the daemon resumes AL without re-enabling.

- [ ] **Step 2: Capture a real curve into the golden test (if not done in Task 4)**

If Task 4's golden file was a placeholder/skip, capture the real Transition Control write now (log it in `handleControlWrite`), save to `~/src/hap/adaptive/testdata/transition_control_write.hex`, and confirm `TestDecodeRealCurve` passes. Push the fork update and re-pin if the bytes revealed any decode fix.

---

## Self-Review notes

- **Spec coverage:** native AL (Tasks 2–11); characteristics in fork + logic in fork, app exposes device via callback (Tasks 7, 12); external-change disables AL (Task 13); tick + brightness change (Tasks 8, 9); persist in StateDir (Tasks 10, 14); white-only / no hue-sat and no customTemperatureAdjustment (honoured — not implemented); README (Task 17); tests incl. TLV round-trip, golden bytes, interpolation, disable, integration (Tasks 3, 4, 6, 8, 15). All spec sections map to a task.
- **Known risk flagged in-plan:** the fork's TLV8 list-separator handling for the curve entries (Task 3 Step 3 note + Task 4 golden test). The float32 encode bug is fixed up front (Task 1).
- **Field-name confirmations the implementer must verify against the fork source (called out at point of use):** `service.Lightbulb.ContainsC`/`Cs` (Task 7), `ColorTemperature.MinVal`/`MaxVal` (Task 8), the fake device constructor/accessor (Task 15). These are the only spots where exact API names couldn't be pinned from the excerpts read during planning.
