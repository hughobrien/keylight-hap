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

	mu            sync.Mutex
	on            int
	brightness    int
	temperature   int
	identifyCount int
	info          Info
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
