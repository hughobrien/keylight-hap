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
	// Re-clamp: guards against the device reporting a temperature outside its
	// advertised 143-344 mired range. Not redundant with the characteristic's
	// min/max, which constrains HomeKit writes but not values we push via
	// SetValue here.
	t := st.Temperature
	if t < elgato.TempMin {
		t = elgato.TempMin
	} else if t > elgato.TempMax {
		t = elgato.TempMax
	}
	_ = l.ct.SetValue(t)
}
