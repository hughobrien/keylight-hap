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
