# DDC Deck

![CI](https://github.com/aic0d3r/ddcdeck/actions/workflows/ci.yml/badge.svg)

Monitor control for Linux in the style of ASUS DisplayWidget Center: brightness, blue light
filter, color presets, input switching, profiles and automation (sunset, dock/undock, active
app, hotkeys). Works with any DDC/CI monitor; vendor-specific features come from community
model files.

Status: daemon, CLI, GUI, tray, profiles, rules, shortcuts and the discovery wizard all work.
Remaining: AUR packaging and more community model files. See [docs/DESIGN.md](docs/DESIGN.md).

## Install

Needs Go and `ddcutil` (your user needs access to `/dev/i2c-*`, e.g. via the `i2c` group).

```sh
make install           # everything: ddcdeck + ddcdeckd + ddcdeck-gui, tray at login
make install-cli       # headless subset (server/WM-less machines)
make install-service   # optional: systemd --user daemon (fast cached reads, events)
```

Without the daemon the CLI talks to the monitor directly (slower, ~1-3 s per command).

```sh
ddcdeck model fetch               # your monitor: pull its community model file, if any
ddcdeck model fetch DEL-U2723QE-41351
```

(Settings has a Fetch… button for the same lookup; it appears when a monitor has no
community file yet.)

On Arch, `packaging/PKGBUILD` builds both packages (`ddcdeck` and `ddcdeck-gui`):
`makepkg -si` in a copy of that directory.

### GUI and tray

Needs GTK 4, libadwaita ≥ 1.4 and gtk4-layer-shell.

The GUI is installed by `make install` above (it needs GTK 4, libadwaita ≥ 1.4 and
gtk4-layer-shell to build).


- Left-click the tray icon: quick panel (brightness, blue light, color, input) next to the icon.
- Scroll on the tray icon: brightness ±5. Right-click: Open / Quit.
- Closing the window keeps DDC Deck in the tray.
- The GUI starts the daemon if needed (`systemctl --user start ddcdeckd`, else it runs
  `ddcdeckd` directly and logs to `~/.cache/ddcdeck/daemon.log`; `ddcdeck daemon logs` follows
  whichever is in use). Set `DDCDECK_DEBUG=1` for logs.
- Numbers you did not change in the app (a brightness change made with the monitor's own
  buttons) are re-read from the monitor while the window is on screen.
- The Display page carries the blue light schedule (`ddcdeck bluelight schedule`), and
  Settings has the same checks as `ddcdeck doctor` under Diagnostics.

## Profiles and automation

```sh
ddcdeck profile save night           # store the current state (all supported features)
ddcdeck profile save day --fields brightness,bluelight
ddcdeck profile apply night          # several: applied in order, only stored fields change
ddcdeck profile list | show night | edit night | rm night

ddcdeck rules                        # triggers, next and last fire time, shortcuts, auto state
ddcdeck auto disable | enable        # pause/resume all triggers
ddcdeck bluelight schedule sunset    # or "21:00-07:00", or off; --level N
```

A fired rule sends a desktop notification ("Sunset → night") and `ddcdeck rules` shows when
each rule last ran, so a quiet screen is never mistaken for a broken rule. App rules stay
silent — they would fire on every window switch.

Rules and profiles are fully editable in either place: the GUI has Add…/trash per rule
and edit/trash per profile, the CLI has `ddcdeck rule add|rm` and
`profile save|edit|rm` — the same calls underneath. Sun/time/hotplug rules need no TOML. Until you set a `[location]`, sun times
use your timezone's reference city — close enough everywhere in the zone.

```sh
ddcdeck rule add sunset night     # or a time (21:30), connected, disconnected
ddcdeck rule rm sunset|[night]|   # ids are listed by `ddcdeck rules`
ddcdeck rule location 52.37 4.90  # for sun rules (defaults to your timezone's city)
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
assign keys in *System Settings → Shortcuts → DDC Deck* (the `keys` value in rules.toml is a
suggestion, and desktops that support it get it pre-filled). The portal only accepts apps
launched from the desktop entry or autostart, not from a terminal. With no `[[shortcut]]` in
rules.toml, DDC Deck offers three suggestions (brightness ±10, blue light toggle); add your
own to replace them.

If your desktop has no GlobalShortcuts portal, use its own custom-shortcut feature and call
the CLI — each action is one command:

| Suggested keys | Command |
|---|---|
| <kbd>Super</kbd>+<kbd>Shift</kbd>+<kbd>↑</kbd> | `ddcdeck brightness set +10` |
| <kbd>Super</kbd>+<kbd>Shift</kbd>+<kbd>↓</kbd> | `ddcdeck brightness set -10` |
| <kbd>Super</kbd>+<kbd>Shift</kbd>+<kbd>B</kbd> | `ddcdeck bluelight toggle` |
| any | `ddcdeck profile apply night`, `ddcdeck input set hdmi2`, … |

**Blue light**: DDC Deck drives the monitor's own filter (VCP code found by the wizard). Many
monitors have no such code — the desktop already fixes that in software, and does it better
(per-screen, no banding): KDE Night Color, GNOME Night Light, `wlsunset`/`wl-gammactl` on
wlroots compositors (Sway, Hyprland), `redshift` or `xrandr --gamma` on X11. Use those; DDC
Deck stays out of the colour pipeline.

### Discovering vendor features

`ddcdeck list` shows only mapped features. Every monitor also reports a list of VCP codes it
understands; the GUI's Settings page shows the unmapped ones. To map a vendor feature
(blue light, KVM, presets…), run the wizard and follow the prompts — you change one setting
in the monitor's OSD, ddcdeck diffs all ~90 codes and narrows it down:

```sh
ddcdeck discover                  # or: ddcdeck discover --feature kvm
ddcdeck model export-mccs         # regenerate ~/.local/share/ddcutil/*.mccs for ddcutil users
# or click "Discover…" in the GUI's Settings page — same flow, saves both files
```

The result lands in `~/.local/share/ddcdeck/models/` immediately, and submitting it is one
click — the wizard's **Submit to community database** button (or `ddcdeck model submit`)
opens a prefilled GitHub issue containing the whole model file; you review it and press
Submit there. A maintainer merges it into `data/models/` so it ships for everyone (the repo
is the model database — nothing is uploaded until you press Submit in the browser).

## Usage

```sh
ddcdeck list                      # monitors and supported features
ddcdeck status                    # every feature with its current value
ddcdeck brightness set +10        # also: contrast, sharpness
ddcdeck bluelight toggle          # on | off | set 0-20 (model-file feature)
ddcdeck input list && ddcdeck input set hdmi1
ddcdeck colortemp set 6500k
ddcdeck volume set 40             # also: mute set mute|unmute, power set standby|off
ddcdeck reset set factory         # every monitor setting back to its defaults
ddcdeck vcp get E6                # raw escape hatch
ddcdeck -d DP-8 --json status     # pick a monitor by index, model, serial, connector or ID
ddcdeck -d all brightness set +10 # every connected monitor at once
ddcdeck default PA279CRV          # pin the monitor used when -d is omitted
ddcdeck doctor                    # check ddcutil, i2c, the monitor, daemon, portal, location
```

`ddcdeck default` (no arguments) shows what is pinned; `--clear` goes back to the first
monitor detected. Docking renumbers buses, so pinning is what keeps commands and rules on the
screen you mean. The same setting has a button in the GUI's Settings page.

Exit codes: 0 ok, 1 usage, 2 no monitor, 3 feature unsupported, 4 DDC error.

Model files: bundled ones live in `data/models/`; drop your own in
`~/.local/share/ddcdeck/models/*.toml` (they win over bundled ones).

## Use the model data with ddcutil today

```sh
cp data/mccs/*.mccs ~/.local/share/ddcutil/
ddcutil getvcp E6   # → "Blue Light Filter" on an ASUS PA279CRV
```
