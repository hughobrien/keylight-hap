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
