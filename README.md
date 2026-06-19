# keylight-hap

[![License: GPL v3](https://img.shields.io/badge/License-GPL%20v3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0)
[![Go Reference](https://pkg.go.dev/badge/github.com/hughobrien/keylight-hap.svg)](https://pkg.go.dev/github.com/hughobrien/keylight-hap)

A small Go daemon that puts [Elgato Key
Lights](https://www.elgato.com/us/en/p/key-light) into Apple Home. It
discovers every Key Light on the LAN over mDNS, exposes each as a HomeKit
Lightbulb — power, brightness, colour temperature — and keeps Home in sync
with whatever else is poking the light (the Elgato app, a Stream Deck, a
keyboard shortcut). No cloud account, no Elgato Control Center, no Home
Assistant. LAN only.

It talks to the light's built-in HTTP API (port 9123) directly and is built
on [`brutella/hap`](https://github.com/brutella/hap), shipped as a Nix flake
with a NixOS module.

> **Why this exists:** the Key Light has no native HomeKit support — Elgato
> expects you to use Control Center or a Stream Deck. This bridges the gap so
> the light lives in Home alongside everything else, and so a Siri "turn off
> the key light" actually works.

## At a glance

The light speaks a tiny unauthenticated HTTP API; the daemon is a thin,
stateful HomeKit front-end over it:

```sh
# The HTTP endpoint the daemon drives (via Go's net/http) — shown with curl
# here only so you can poke it by hand:
$ curl http://192.168.1.31:9123/elgato/lights
{"numberOfLights":1,"lights":[{"on":1,"brightness":20,"temperature":213}]}

# The daemon, once running, logs its pairing PIN:
$ journalctl -u keylight-hap | grep -i pin
... HomeKit PIN pin=1234-5678
```

Add the bridge in the Home app, enter the PIN, done — the light shows up with
an on/off toggle, a brightness slider, and a warm↔cool temperature slider,
and it tracks changes you make from any other controller within one poll
interval.

What's covered:

- **Discovery:** browses `_elg._tcp.local.` at startup and exposes every
  light it finds, named from each device's `displayName`.
- **Control from Home:** power, brightness (0–100 %), and colour temperature
  (2900 K–7000 K). HomeKit *Identify* flashes the light.
- **Two-way sync:** polls each light (default every 20 s) so changes from the
  Elgato app, a Stream Deck, or a keybinding appear in Home.
- **HomeKit done right:** generated + persisted 8-digit PIN, pairing survives
  restarts, each light carries its real manufacturer / model / serial /
  firmware in the accessory details.

What's deliberately out (see [Known limitations](#known-limitations)):

- The RGB **Light Strip** / colour models — this exposes white-temperature
  Key Lights only.
- Runtime add/remove of lights — the device set is a startup snapshot.

## Supported devices

Any Elgato Wi-Fi light that advertises `_elg._tcp` and exposes the
`on` / `brightness` / `temperature` light object:

| Model                          | Status                                                 |
|--------------------------------|--------------------------------------------------------|
| Elgato **Key Light**           | Tested (firmware 1.0.3).                               |
| Elgato **Key Light Air**       | Expected to work — same API surface.                  |
| Elgato **Key Light Mini**      | Expected to work — same API surface.                  |
| Elgato **Light Strip** (RGB)   | Not supported (uses hue/saturation, no `temperature`). |

`temperature` is reported in **mireds**: `143` = 7000 K (cool) through `344`
= 2900 K (warm), which maps 1:1 onto HomeKit's `ColorTemperature`
characteristic, so the colour slider needs no conversion — just a clamp to
the device's range.

## Run

With Nix, no install:

```sh
# Run straight from the flake (slower per-invocation; re-checks the flake):
nix run github:hughobrien/keylight-hap

# Or build the binary into ./result/bin:
nix build github:hughobrien/keylight-hap
./result/bin/keylight-hap
```

From source (Go 1.26+):

```sh
go build ./cmd/keylight-hap && ./keylight-hap
```

On first run it browses for lights, builds the bridge, and prints the PIN.

### Configuration

Every flag also reads an environment variable; the NixOS module sets them on
the command line. There are no secrets — the Elgato API is unauthenticated —
so there is no config file.

| Flag                  | Env                              | Default                 | Meaning                                  |
|-----------------------|----------------------------------|-------------------------|------------------------------------------|
| `--bridge-name`       | `KEYLIGHT_HAP_BRIDGE_NAME`       | `keylight-hap`          | Name shown during HomeKit pairing.       |
| `--port`              | `KEYLIGHT_HAP_PORT`              | `0`                     | HAP TCP port; 0 = OS-assigned ephemeral. |
| `--poll-interval`     | `KEYLIGHT_HAP_POLL_INTERVAL`     | `20s`                   | State-sync poll period.                  |
| `--discovery-timeout` | `KEYLIGHT_HAP_DISCOVERY_TIMEOUT` | `5s`                    | mDNS browse window per attempt.          |
| `--state-dir`         | `KEYLIGHT_HAP_STATE_DIR`         | `/var/lib/keylight-hap` | PIN + pairing storage directory.         |

## NixOS

### 1. Add the flake input + module import

```nix
# flake.nix
{
  inputs.keylight-hap.url = "github:hughobrien/keylight-hap";

  outputs = { self, nixpkgs, keylight-hap, ... }: {
    nixosConfigurations.myhost = nixpkgs.lib.nixosSystem {
      modules = [
        keylight-hap.nixosModules.default
        ./configuration.nix
      ];
    };
  };
}
```

### 2. Enable it

The simplest form — one knob, ephemeral port, firewall opened for you:

```nix
services.keylight-hap = {
  enable = true;
  openFirewall = true;   # opens UDP 5353 (mDNS) + the HAP TCP port if pinned
};
```

`openFirewall` only opens the HAP TCP port when you **pin one** (an ephemeral
`port = 0` can't be firewalled), so for a fixed hole set a port too:

```nix
services.keylight-hap = {
  enable = true;
  port = 21063;
  openFirewall = true;
};
```

Prefer to manage the firewall centrally instead of per-module? Leave
`openFirewall = false`, pin `port`, and open that TCP port (plus mDNS
`5353/udp`) on your LAN interface yourself.

### 3. Rebuild and pair

`nixos-rebuild switch`, then find the PIN and add the bridge in Home:

```sh
journalctl -u keylight-hap | grep -i pin
```

### What the module does

- Runs `keylight-hap` as a dedicated system user under a hardened systemd
  unit (`ProtectSystem=strict`, syscall filtering, etc.). `AF_NETLINK` is
  deliberately allowed — without it the mDNS responder silently advertises on
  zero interfaces and the bridge never appears on iPhones.
- Stores the PIN and pairing keys in `StateDirectory=/var/lib/keylight-hap`.

#### Module options

| Option                              | Default            | Meaning                                  |
|-------------------------------------|--------------------|------------------------------------------|
| `services.keylight-hap.enable`      | `false`            | Enable the service.                      |
| `keylight-hap.bridgeName`                       | `"keylight-hap"`   | Name shown during pairing.               |
| `keylight-hap.port`                             | `0`                | HAP TCP port (0 = ephemeral).            |
| `keylight-hap.pollInterval`                     | `"20s"`            | State-sync poll period.                  |
| `keylight-hap.discoveryTimeout`                 | `"5s"`             | mDNS browse window per attempt.          |
| `keylight-hap.stateDir`                         | `/var/lib/keylight-hap` | PIN + pairing storage.              |
| `keylight-hap.openFirewall`                     | `false`            | Open mDNS + the pinned HAP port.         |

> **Impermanence:** if your root is wiped on boot, persist
> `/var/lib/keylight-hap` (e.g. add it to your impermanence `directories`).
> Otherwise the PIN regenerates and HomeKit drops the pairing every reboot.

## Pairing

The 8-digit HomeKit PIN is generated on first run (random, screened against
HomeKit's weak-PIN list), written to `pin.txt` (mode 0600) in the state
directory, and logged at startup as `XXXX-XXXX`:

```sh
journalctl -u keylight-hap | grep -i pin
```

Add the bridge in the Home app and enter that PIN. Every discovered light
appears together under the one bridge, each as its own tile. To **factory-reset
pairing**, delete the state directory and restart — a fresh PIN is generated.

## Known limitations

Deliberate choices, not bugs:

- **Startup snapshot.** The set of lights is fixed when the daemon starts. A
  light added, removed, or re-addressed (new DHCP lease) afterwards isn't
  picked up until you restart the service. A light that goes offline stays
  present-but-unreachable in Home with its last-known values. This keeps the
  bridge simple and avoids re-announcing the HomeKit accessory database at
  runtime (which `brutella/hap` isn't built for).
- **White temperature only.** Key Lights are white-with-adjustable-temperature
  devices; there is no hue/saturation to expose. The RGB Light Strip uses a
  different light object and is out of scope.
- **1 % is "on but dark."** The device accepts `brightness` down to 1, but at
  1 it is effectively off while still reporting `on`. Dragging brightness to 0
  in Home turns the accessory off cleanly; the 1 % corner is harmless.

## Developing

```sh
nix develop          # Go, gopls, gotools, go-tools
go test ./...        # unit tests, incl. an httptest fake Key Light
go test -race ./...
go vet ./... && gofmt -l .
```

Project layout:

```
cmd/keylight-hap/      entrypoint: flags/env, discovery, lifecycle
internal/elgato/       HTTP client for the light + an in-memory fake device
internal/discover/     mDNS browse of _elg._tcp
internal/bridge/       HAP accessory wiring, PIN/FsStore, the poll loop
nix/module.nix         the services.keylight-hap NixOS module
```

The design spec and implementation plan live under `docs/superpowers/`.

After bumping a dependency, refresh the flake's `vendorHash`: set it to
`pkgs.lib.fakeHash`, run `nix build .#keylight-hap`, and paste the reported
`got:` hash back in.

## Credits

The Elgato Key Light's local API is undocumented by the vendor. This project
stands on two community reverse-engineering write-ups:

- **adamesch/elgato-key-light-api** — the resource/field reference for
  `/elgato/lights`, `/elgato/accessory-info`, and `/elgato/identify`:
  <https://github.com/adamesch/elgato-key-light-api>
- **apihandyman — "Hacking Elgato Key Light with Postman"** — for the
  endpoint walkthrough and the temperature-range notes:
  <https://apihandyman.io/hacking-elgato-key-light-with-postman/>

(The two sources disagree on the colour-temperature direction; the
`temperature`-is-mireds reading used here was confirmed against a real Key
Light.) HomeKit support is provided by
[`brutella/hap`](https://github.com/brutella/hap).

## License

Copyright (C) 2026 Hugh O'Brien

This program is free software: you can redistribute it and/or modify it under
the terms of the GNU General Public License as published by the Free Software
Foundation, either version 3 of the License, or (at your option) any later
version (`SPDX-License-Identifier: GPL-3.0-or-later`). See [LICENSE](LICENSE)
for the full text.

This project is not affiliated with or endorsed by Elgato / Corsair. "Elgato"
and "Key Light" are trademarks of their respective owners.
