# DDC Deck

Monitor control for Linux in the style of ASUS DisplayWidget Center: brightness, blue light
filter, color presets, input switching, profiles and automation (sunset, dock/undock, active
app, hotkeys). Works with any DDC/CI monitor; vendor-specific features come from community
model files.

Status: daemon, CLI, GUI and tray work. Profiles, rules and the discovery wizard come next;
see [docs/DESIGN.md](docs/DESIGN.md).

## Install

Needs Go and `ddcutil` (your user needs access to `/dev/i2c-*`, e.g. via the `i2c` group).

```sh
make install           # ~/.local/bin/ddcdeck + ddcdeckd symlink
make install-service   # optional: systemd --user daemon (fast cached reads, events)
```

Without the daemon the CLI talks to the monitor directly (slower, ~1-3 s per command).

### GUI and tray

Needs GTK 4, libadwaita ≥ 1.4 and gtk4-layer-shell.

```sh
make install-gui       # ddcdeck-gui, app menu entry, starts hidden in the tray at login
```

- Left-click the tray icon: quick panel (brightness, blue light, color, input) next to the icon.
- Scroll on the tray icon: brightness ±5. Right-click: Open / Quit.
- Closing the window keeps DDC Deck in the tray.
- The GUI starts the daemon if needed (`systemctl --user start ddcdeckd`, else it runs
  `ddcdeckd` directly). Set `DDCDECK_DEBUG=1` for logs.

## Profiles and automation

```sh
ddcdeck profile save night           # store the current state (all supported features)
ddcdeck profile save day --fields brightness,bluelight
ddcdeck profile apply night          # several: applied in order, only stored fields change
ddcdeck profile list | show night | edit night | rm night

ddcdeck rules                        # triggers, next fire time, shortcuts, auto state
ddcdeck auto disable | enable        # pause/resume all triggers
ddcdeck bluelight schedule sunset    # or "21:00-07:00", or off; --level N
```

App rules (KDE only) apply a profile while any of the listed window classes is focused
and restore the previous values when focus leaves, e.g. `when = "app"`,
`class = ["darktable", "gimp"]`. The class is the app id KWin reports ("kitty",
"io.github.aic0d3r.DDCDeck").

Rules live in `~/.config/ddcdeck/rules.toml`, profiles in `~/.config/ddcdeck/profiles/*.toml`
(see [docs/DESIGN.md](docs/DESIGN.md) for the format). Sun rules use an offline NOAA
calculation from `[location]` and fire on transitions, including ones missed while the
daemon was down. `connected`/`disconnected` rules fire on hotplug. Tool-written config
rewrites lose comments.

**Global shortcuts** are registered through the XDG GlobalShortcuts portal from the GUI:
assign keys in *System Settings → Shortcuts → DDC Deck* (the `keys` value in rules.toml is
a documented suggestion). The portal only accepts apps launched from the desktop entry or
autostart, not from a terminal.

### Discovering vendor features

`ddcdeck list` shows only mapped features. Every monitor also reports a list of VCP codes it
understands; the GUI's Settings page shows the unmapped ones. To map a vendor feature
(blue light, KVM, presets…), run the wizard and follow the prompts — you change one setting
in the monitor's OSD, ddcdeck diffs all ~90 codes and narrows it down:

```sh
ddcdeck discover                  # or: ddcdeck discover --feature kvm
ddcdeck model export-mccs         # regenerate ~/.local/share/ddcutil/*.mccs for ddcutil users
```

The result lands in `~/.local/share/ddcdeck/models/` immediately; submit it as a PR to
`data/models/` so it ships for everyone (the repo is the model database — no online
service, lookups never phone home).

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
