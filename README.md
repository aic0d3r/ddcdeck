# DDC Deck

Monitor control for Linux in the style of ASUS DisplayWidget Center: brightness, blue light
filter, color presets, input switching, profiles and automation (sunset, dock/undock, active
app, hotkeys). Works with any DDC/CI monitor; vendor-specific features come from community
model files.

Status: milestone 1 (daemon + CLI) works. Profiles, rules, GUI and the wizard come next; see
[docs/DESIGN.md](docs/DESIGN.md).

## Install

Needs Go and `ddcutil` (your user needs access to `/dev/i2c-*`, e.g. via the `i2c` group).

```sh
make install           # ~/.local/bin/ddcdeck + ddcdeckd symlink
make install-service   # optional: systemd --user daemon (fast cached reads, events)
```

Without the daemon the CLI talks to the monitor directly (slower, ~1-3 s per command).

## Usage

```sh
ddcdeck list                      # monitors and supported features
ddcdeck status                    # every feature with its current value
ddcdeck brightness set +10        # also: contrast, sharpness
ddcdeck bluelight toggle          # on | off | set 0-20 (model-file feature)
ddcdeck input list && ddcdeck input set hdmi1
ddcdeck colortemp set 6500k
ddcdeck vcp get E6                # raw escape hatch
ddcdeck -d DP-8 --json status     # pick a monitor by index, model, serial, connector or ID
```

Exit codes: 0 ok, 1 usage, 2 no monitor, 3 feature unsupported, 4 DDC error.

Model files: bundled ones live in `data/models/`; drop your own in
`~/.local/share/ddcdeck/models/*.toml` (they win over bundled ones).

## Use the model data with ddcutil today

```sh
cp data/mccs/*.mccs ~/.local/share/ddcutil/
ddcutil getvcp E6   # → "Blue Light Filter" on an ASUS PA279CRV
```
