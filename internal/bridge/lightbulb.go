package bridge

import (
	"context"
	"log/slog"
	"net/http"
	"sync"

	"github.com/brutella/hap"
	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/adaptive"
	"github.com/brutella/hap/characteristic"

	"github.com/hughobrien/keylight-hap/internal/elgato"
)

// ctExternalTolerance is the mired gap beyond which a polled temperature that
// AL/HomeKit did not command is treated as an external change and disables AL.
const ctExternalTolerance = 3

// lightAccessory wraps a brutella/hap Lightbulb plus typed handles and the
// device client it controls.
type lightAccessory struct {
	a      *accessory.Lightbulb
	on     *characteristic.On
	bright *characteristic.Brightness
	ct     *characteristic.ColorTemperature

	ctx    context.Context
	client *elgato.Client

	al *adaptive.Controller

	cmdMu       sync.Mutex
	lastCmdTemp int
	hasLastCmd  bool
}

// recordCmd notes the temperature AL or HomeKit last drove the device to, so
// the poll loop can tell apart AL/HomeKit writes from external changes.
func (l *lightAccessory) recordCmd(temp int) {
	l.cmdMu.Lock()
	l.lastCmdTemp = temp
	l.hasLastCmd = true
	l.cmdMu.Unlock()
}

// newLightAccessory builds the HomeKit accessory for one light, seeds it with
// the current state, and registers write + identify callbacks.
func newLightAccessory(ctx context.Context, name string, info elgato.Info, st elgato.State, client *elgato.Client, store hap.Store) *lightAccessory {
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

	alKey := "adaptive-" + info.SerialNumber
	la.al = adaptive.NewController(adaptive.Options{
		Lightbulb:        a.Lightbulb,
		Brightness:       bright,
		ColorTemperature: ct,
		SetColorTemperature: func(mired int) error {
			res, err := client.Set(ctx, elgato.Patch{Temperature: &mired})
			if err == nil {
				la.recordCmd(res.Temperature)
			}
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
			slog.Warn("restore adaptive lighting failed", "light", name, "err", err)
			_ = store.Delete(alKey)
		}
	}

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
	if l.al != nil {
		l.al.HandleBrightnessChanged()
	}
}

func (l *lightAccessory) onTemperatureWrite(v int) {
	if l.al != nil {
		l.al.Disable()
	}
	res, err := l.client.Set(l.ctx, elgato.Patch{Temperature: &v})
	if err != nil {
		slog.Warn("set temperature failed", "err", err)
		return
	}
	l.recordCmd(res.Temperature)
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
	if l.al != nil && l.al.IsActive() {
		l.cmdMu.Lock()
		last, has := l.lastCmdTemp, l.hasLastCmd
		l.cmdMu.Unlock()
		if has {
			if diff := t - last; diff > ctExternalTolerance || diff < -ctExternalTolerance {
				slog.Info("external colour-temperature change; disabling adaptive lighting",
					"light", l.a.A.Name(), "polled", t, "lastCommanded", last)
				l.al.Disable()
			}
		}
	}
	_ = l.ct.SetValue(t)
}
