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
