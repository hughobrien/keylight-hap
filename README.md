# keylight-hap

A small Go daemon that discovers [Elgato Key Lights](https://www.elgato.com/)
on the LAN via mDNS at startup and exposes each one as a HomeKit Lightbulb
(On / Brightness / Color Temperature) behind a single bridge. Changes made
through other controllers (the Elgato app, a Stream Deck, sway keybindings)
are reflected back into Apple Home within the poll interval.

It is built on [`brutella/hap`](https://github.com/brutella/hap) and shipped
as a Nix flake with a NixOS module.

## What it does

- Browses `_elg._tcp.local.` (port 9123) once at startup and exposes every
  light it finds.
- Maps HomeKit ↔ Elgato 1:1: `On` ↔ `on`, `Brightness` (0–100) ↔
  `brightness`, `ColorTemperature` (mireds, clamped to the device's 143–344
  range) ↔ `temperature`. HomeKit Identify triggers `POST /elgato/identify`.
- Polls each light every `--poll-interval` (default 20s) so out-of-band
  changes appear in Home.

## Run

With Nix:

```sh
nix run github:hughobrien/keylight-hap
# or build the binary:
nix build github:hughobrien/keylight-hap
./result/bin/keylight-hap
```

From source:

```sh
go build ./cmd/keylight-hap && ./keylight-hap
```

### Configuration

All flags also read an environment variable; the NixOS module passes them on
the command line.

| Flag                  | Env                              | Default                 | Meaning                                  |
|-----------------------|----------------------------------|-------------------------|------------------------------------------|
| `--bridge-name`       | `KEYLIGHT_HAP_BRIDGE_NAME`       | `keylight-hap`          | Name shown during HomeKit pairing.       |
| `--port`              | `KEYLIGHT_HAP_PORT`              | `0`                     | HAP TCP port; 0 = OS-assigned ephemeral. |
| `--poll-interval`     | `KEYLIGHT_HAP_POLL_INTERVAL`     | `20s`                   | State-sync poll period.                  |
| `--discovery-timeout` | `KEYLIGHT_HAP_DISCOVERY_TIMEOUT` | `5s`                    | mDNS browse window per attempt.          |
| `--state-dir`         | `KEYLIGHT_HAP_STATE_DIR`         | `/var/lib/keylight-hap` | PIN + pairing storage directory.         |

## NixOS module

```nix
{
  imports = [ inputs.keylight-hap.nixosModules.default ];
  services.keylight-hap = {
    enable = true;
    openFirewall = true; # opens UDP 5353 (mDNS) and the HAP TCP port if pinned
  };
}
```

## Pairing

The 8-digit HomeKit PIN is generated on first run, persisted to
`pin.txt` (mode 0600) in the state directory, and logged at startup as
`XXXX-XXXX`. Find it in the journal:

```sh
journalctl -u keylight-hap | grep PIN
```

Add the bridge in the Home app and enter that PIN. Delete the state directory
to factory-reset HomeKit pairing.

## Snapshot-discovery caveat

The set of lights is a **snapshot taken at startup**. A light that is added,
removed, or changes IP address after the daemon starts is not picked up until
you restart the service. A light that goes offline stays present (but
unreachable) in Home, keeping its last-known values.

## License

GPL-3.0-or-later. See [LICENSE](./LICENSE).
