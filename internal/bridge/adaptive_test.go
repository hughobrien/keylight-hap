package bridge

import (
	"context"
	"testing"

	"github.com/brutella/hap"
	"github.com/brutella/hap/adaptive"

	"github.com/hughobrien/keylight-hap/internal/elgato"
)

func newALTestLight(t *testing.T) (*lightAccessory, *elgato.Client, *elgato.FakeDevice) {
	t.Helper()
	d := elgato.NewFakeDevice()
	c := elgato.New(d.HostPort())
	ctx := context.Background()
	info, err := c.Info(ctx)
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	st, err := c.Get(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	la := newLightAccessory(ctx, "Test", info, st, c, hap.NewFsStore(t.TempDir()))
	return la, c, d
}

func TestAdaptiveLightingDrivesDevice(t *testing.T) {
	la, c, d := newALTestLight(t)
	defer d.Close()
	defer la.al.Disable()

	la.bright.SetValue(100)
	adaptive.EnableForTest(la.al, la.ct.C.Id, la.bright.C.Id, 250)

	if !la.al.IsActive() {
		t.Fatal("expected AL active after enable")
	}
	got, err := c.Get(context.Background())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Temperature != 250 {
		t.Fatalf("device temperature = %d, want 250 (AL should have driven it)", got.Temperature)
	}
}

func TestAdaptiveLightingDisablesOnExternalChange(t *testing.T) {
	la, _, d := newALTestLight(t)
	defer d.Close()
	defer la.al.Disable()

	la.bright.SetValue(100)
	adaptive.EnableForTest(la.al, la.ct.C.Id, la.bright.C.Id, 250)
	if !la.al.IsActive() {
		t.Fatal("expected AL active after enable")
	}

	// Simulate the poll loop observing an external temperature far from the
	// last AL-commanded value (250). This must disable AL.
	la.sync(elgato.State{On: true, Brightness: 100, Temperature: 143})
	if la.al.IsActive() {
		t.Fatal("expected AL disabled after external temperature change")
	}
}

func TestAdaptiveLightingIgnoresMatchingPoll(t *testing.T) {
	la, _, d := newALTestLight(t)
	defer d.Close()
	defer la.al.Disable()

	la.bright.SetValue(100)
	adaptive.EnableForTest(la.al, la.ct.C.Id, la.bright.C.Id, 250)

	// A poll reporting the AL-commanded value (within tolerance) must NOT disable.
	la.sync(elgato.State{On: true, Brightness: 100, Temperature: 250})
	if !la.al.IsActive() {
		t.Fatal("AL should stay active when the poll matches the commanded temperature")
	}
}
