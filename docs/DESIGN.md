# DDC Deck — design spec (v1)

A Linux counterpart to ASUS DisplayWidget Center: control monitor features over DDC/CI
from a GUI, tray, CLI and automation. Works with any DDC monitor; vendor extras (ASUS first)
come from per-model definition files that users can contribute.

Decisions from the interview (2025-10):

| Topic | Decision |
|---|---|
| Name | `ddcdeck` (CLI), `ddcdeckd` (daemon), "DDC Deck" (GUI) |
| Shape | DWC-style window with sidebar tabs + tray icon |
| Toolkit | Go + GTK4/libadwaita (same stack as z13gui-plus) |
| Architecture | user daemon owns DDC; CLI/GUI/tray are socket clients |
| Scope | any DDC monitor + per-model extras, ASUS first |
| v1 features | image + blue light, ProArt/Splendid presets, input/KVM, profiles & automation |
| Triggers | time/sunset, dock/undock, active app (KWin script), hotkeys (portal) |
| Std features | MCCS defaults incl. power (D6), volume (62), mute (8D); unknown caps codes named via `ddcutil vcpinfo` (queried, never copied — GPL) |
| Profiles | partial (only stored fields), stackable |
| Model support | discovery wizard in CLI + GUI, outputs model file + `.mccs` |
| Tray | left-click popover, right-click menu |
| Z13 link | separate repo; optional `on_apply` hooks, z13gui-plus may read the socket API |
| Build order | daemon + CLI → profiles/triggers → GUI + tray → wizard |

## 1. Architecture

```
ddcdeckd  (systemd --user, Type=notify)
 ├─ monitor registry   EDID from /sys/class/drm/*/edid → model file match
 ├─ DDC queue          one worker per I2C bus, serialized, cached reads
 ├─ hotplug watcher    udev "drm" change events → re-scan, fire dock/undock
 ├─ scheduler          fixed times + sunrise/sunset (NOAA, no network)
 ├─ app watcher        KWin script → D-Bus → active window class
 ├─ shortcuts          XDG GlobalShortcuts portal
 └─ API                $XDG_RUNTIME_DIR/ddcdeck.sock (JSON lines)
        ▲                   ▲                 ▲
     ddcdeck (CLI)     ddcdeck-gui        tray (in gui process)
```

- **DDC backend:** v1 shells out to `ddcutil --bus N --noverify`. That keeps the licence
  separate (libddcutil is GPL) and gets ddcutil's per-monitor quirks for free. If it proves
  too slow, swap it for raw `/dev/i2c-N` DDC/CI later; everything goes through the same internal `Backend` interface.
- **Core vs platform:** `internal/` (deck, automation, model, ddc, ...) has no OS-specific
  imports and is vetted for Windows and macOS in CI. Everything Linux-only (ddcutil backend,
  DRM hotplug, KWin app watcher, D-Bus notifications and shortcuts, diagnostics) lives in
  `internal/platform/linux`, and `cmd/` wires it in: `ddc.Backend` for DDC, `automation.SetNotifier`
  and `SetDiagnoser` for the rest. A new OS adds a sibling package under `internal/platform`.
- **Cache:** reads are served from the cache. Writes update the cache optimistically, then
  re-read once 200 ms later to confirm. The GUI never blocks on I2C.
- **Identity:** monitors are keyed by `MFG-MODEL-SERIAL` from the EDID, never by bus number
  (bus numbers change when you re-dock).
- The CLI falls back to calling the backend directly when the daemon isn't running, so
  scripts keep working without the daemon.

## 2. CLI

Noun → verb. Every GUI action has exactly one CLI equivalent. `--json` on every read.
Global flags: `-d, --display <name|serial|index>` (default: the only/primary external monitor),
`--json`, `-q`.

```
ddcdeck list                                  # monitors, connector, model, supported features
ddcdeck status                                # all known features with current values

ddcdeck brightness get | set <0-100|+N|-N>
ddcdeck contrast   get | set <0-100|+N|-N>
ddcdeck bluelight  get | on | off | toggle | set <0-20>
ddcdeck bluelight  schedule sunset|off|"21:00-07:00"
ddcdeck colortemp  get | set <name>           # names from model file, e.g. 5000k, 6500k
ddcdeck preset     list | get | set <name>    # srgb, adobergb, dci-p3, rec709, user1 ...
ddcdeck input      list | get | set <name>    # dp, hdmi1, hdmi2, usbc
ddcdeck kvm        get | set <upstream>       # if the model supports it
ddcdeck volume     get | set <0-max|+N|-N>    # built-in speakers (VCP 62)
ddcdeck mute       set <mute|unmute>          # VCP 8D
ddcdeck power      set <on|standby|suspend|off>  # VCP D6

ddcdeck default [selector] [--clear]          # pin the monitor used when -d is omitted
ddcdeck profile    list | show <name> | apply <name>... | save <name> [--fields a,b]
ddcdeck profile    edit <name> | rm <name>
ddcdeck rule       add <when> <profile>... | rm <id> | location [city | "lat lon"]
ddcdeck auto       status | enable | disable  # pause all triggers
ddcdeck rules      list                       # active triggers and their next fire time

ddcdeck vcp        get <code> | set <code> <v>  # raw escape hatch
ddcdeck discover   [--feature <name>]           # guided code discovery, see §6
ddcdeck model      export-mccs [model]          # write ~/.local/share/ddcutil/*.mccs
ddcdeck model      fetch <MFG-MODEL-PRODUCT>      # opt-in lookup from the repo's data/models/
ddcdeck reset      set factory                   # MCCS 0x04
ddcdeck doctor                                  # ddcutil, i2c, monitor, daemon, portal, location
ddcdeck daemon     status | logs
```

Exit codes: 0 ok, 1 usage, 2 no monitor, 3 feature unsupported, 4 DDC error.

## 3. Socket API

Newline-delimited JSON over `$XDG_RUNTIME_DIR/ddcdeck.sock` (mode 0600), same style as
z13ctl-plus.

```json
→ {"id":1,"method":"set","params":{"display":"PA279CRV","feature":"bluelight","value":20}}
← {"id":1,"result":{"value":20}}
→ {"id":2,"method":"subscribe","params":{"events":["value","hotplug","profile"]}}
← {"event":"hotplug","display":"AUS-PA279CRV-TALMSB010776","state":"connected"}
```

Methods: `list`, `status`, `get`, `set`, `profile.apply|list|save`, `rules.list`,
`auto.set`, `vcp.get|set`, `subscribe`. Include `api_version` in `list` from day one.

## 4. Config formats

### Model file (source of truth, contributable) — `data/models/<MFG>-<MODEL>-<PRODUCT>.toml`
Maps friendly feature names to VCP codes. `ddcdeck model export-mccs` generates the ddcutil
`.mccs` from it, so ddcutil and ddcui users benefit too.

### Profiles — `~/.config/ddcdeck/profiles/<name>.toml`

Partial: applying a profile changes only the fields it stores. `apply a b` applies them in order.

```toml
[monitor.PA279CRV]        # model, serial or "*"
bluelight  = 20
brightness = 30
on_apply   = ["z13ctl-plus preset apply docked"]   # optional shell hooks, no shell expansion
```

### Rules — `~/.config/ddcdeck/rules.toml`

```toml
[location]                 # default: guessed from the timezone, editable in the GUI
lat = 52.37
lon = 4.90

[[rule]]
when  = "sunset"           # sunset | sunrise | "HH:MM"
apply = ["night"]

[[rule]]
when  = "sunrise"
apply = ["day"]

[[rule]]
when    = "connected"      # connected | disconnected
display = "PA279CRV"
apply   = ["coding"]

[[rule]]
when  = "app"              # while focused; reverts the touched fields on blur
class = ["darktable", "gimp"]   # KWin resourceClass (app id), e.g. "kitty"
apply = ["photo"]

[[shortcut]]
keys   = "Meta+Shift+B"
action = "bluelight toggle"
```

Rules fire on **transitions** only. A manual change sticks until the next transition, the
same behaviour as the current `bluelight auto` script.

### Global settings — `~/.config/ddcdeck/config.toml`

```toml
default_display = "PA279CRV"   # index, model, serial, connector or ID; "" = first detected
```

Written by `ddcdeck default` and by the GUI's Settings page, read on every command (and by
the daemon when no `-d` is given). Bus numbers change when you re-dock, so this is what keeps
commands and rules on the intended screen; if the pinned monitor is unplugged, the first
monitor is used instead of failing.

## 5. GUI

Libadwaita, DWC layout: `AdwNavigationSplitView`, a monitor picker in the header bar, live
updates via `subscribe`.

```
┌─ ☰  [PA279CRV ▾]                          ⏻ auto ─┐
│ Display   │ ╭ Picture ──────────────────────────╮ │
│ Color     │ │ Brightness          ━━━━━●───  60 │ │
│ Input     │ │ Contrast            ━━━━●────  50 │ │
│ Profiles  │ ╰───────────────────────────────────╯ │
│ Automation│ ╭ Eye care ─────────────────────────╮ │
│ Settings  │ │ Blue light filter   [■]  ━━●── 20 │ │
│           │ │ Schedule            [Sunset ▾]    │ │
│           │ ╰───────────────────────────────────╯ │
└───────────┴───────────────────────────────────────┘
```

| Page | Content |
|---|---|
| Display | brightness, contrast, sharpness, blue light + schedule |
| Color | preset (ProArt/Splendid), color temperature, RGB gain |
| Input | input source cards, KVM upstream, (later: PiP/PbP) |
| Profiles | list, apply, "save current as…", edit (rename/values/drop fields), remove |
| Automation | rules list (time/sunset, dock, app, shortcut), location |
| Settings | model info, community-file fetch, unsupported features, "Add support for this monitor" (wizard), diagnostics (= `doctor`), default monitor pin, raw VCP |

Unsupported features are hidden on the main pages and listed under Settings, never shown greyed out.

**Tray** (StatusNotifierItem): left-click opens a popover with brightness, blue light toggle,
preset, input and profile chips. Right-click menu: Open DDC Deck, Pause automation, Quit.

Accessibility: every slider has a label and a value readout, works by keyboard, and has no
colour-only state.

## 6. Discovery wizard

Status: **CLI wizard done.** `ddcdeck discover` snapshots caps codes + E0–FF (minus
noise codes 0x02 "new control" and 0xC0 "usage time", plus a stability filter), diffs
consecutive snapshots across OSD changes, infers range/enum from the reading type
(C vs NC), collects enum names, confirms with a write-back, then writes the user model
TOML + `.mccs`. Verified against a real PA279CRV. The GUI shows unmapped codes on the
Settings page (named via `ddcutil vcpinfo` at runtime — the table is GPL, so it is
queried, never copied). The Settings page's “Discover…” button opens the graphical
wizard (AdwDialog, four steps: name → change → details → confirm), driving the same
engine; after saving it asks the daemon to rescan, so the feature is usable instantly.
It also offers one-click submission: `ddcdeck model submit` (or the wizard's Submit
button) opens a prefilled GitHub issue with the whole accumulated model file. The `.mccs`
export carries `VALUE` lines for enums, so ddcutil names the values too ("input: usbc",
not "0x1a").

Automates the manual method used to find blue light = `0xE6`:

1. Snapshot every VCP code the monitor lists in its capabilities (plus `E0–FF`).
2. "In the monitor menu, change **<feature>** to **<value>**, then press Next."
3. Snapshot again and diff. Exactly one changed code → candidate. More → ask again with
   another value to narrow it down.
4. Confirm by writing the value back and asking "did the menu change?"
5. Write the model TOML + `.mccs`, and offer a prefilled GitHub PR / issue text.

## 7. Milestones

1. **Core:** daemon, cache/queue, EDID registry, CLI basic features, PA279CRV model file,
   migrate the `~/.local/bin/bluelight` script. *Done except the script migration (waits
   for milestone 2 rules) and `model export-mccs`. Extra API method: `monitor` (resolved
   selector + features).*
2. **Automation:** profiles, time/sunset + hotplug rules, shortcuts portal. *Done, and the
   `bluelight` script is migrated (rules.toml + night/day profiles, bluelight.timer
   disabled). Deviations: shortcut binding runs in the GUI process because the portal
   derives the app id from the launching systemd app scope (a plain user service has none);
   `keys` is therefore a suggestion shown in descriptions, real keys are assigned in the
   desktop settings. `on_apply` hooks and profiles apply through the daemon API, so writes
   get the same coalescing as manual changes. rules.toml rewrites (CLI/GUI) lose comments.*
3. **GUI:** window + tray on the socket API. *Done (built before milestone 2). Deviations:
   the tray "popover" is a gtk4-layer-shell quick panel placed at the SNI click position
   (GTK4 popovers can't anchor to a tray icon on Wayland); scrolling the tray icon changes
   brightness; Profiles/Automation pages and "Pause automation" arrive with milestone 2.
   Writes coalesce per VCP code in the daemon (latest wins, in arrival order), so slider
   drags cost a few I2C writes.*
4. **Community:** discovery wizard (CLI done), KWin app watcher (done), AUR package
   (`packaging/PKGBUILD`, split into `ddcdeck` + `ddcdeck-gui`; cut the `v$pkgver` tag before
   publishing it), CI (`.github/workflows/ci.yml`), docs site.

**Basics pass (2026-10).** The gaps a first-time user hits immediately:

- **CLI parity** — `volume`, `mute` and `power` were in the GUI and in `model.Defaults()` but
  had no CLI commands. §2's promise holds again.
- **`ddcdeck default`** — `-d` used to mean "whatever ddcutil lists first", which changes when
  you dock. `config.toml` now pins a monitor; unplugged again falls back instead of failing.
- **Visible automation** — a fired rule sends a desktop notification (`org.freedesktop.Notifications`,
  no portal needed) and the engine persists `last_fire` per rule, shown by `ddcdeck rules` and
  on the Automation page. App rules are excluded: they fire on every window switch.
- **Stale values** — `status` takes `fresh`, and the GUI re-reads the monitor every 15 s while
  the window is visible but not focused, so changes made with the monitor's own buttons appear.
- **Shortcut suggestions** — rules.toml `keys` is sent to the portal as `preferred_trigger`
  (freedesktop shortcuts-spec: `Meta+Shift+B` → `LOGO+SHIFT+b`), and three suggested shortcuts
  appear when rules.toml declares none. They are display-only: `SaveConfig` never writes them.
- **Logs without systemd** — the GUI-spawned daemon writes `~/.cache/ddcdeck/daemon.log`
  (`DDCDECK_LOG`), which `ddcdeck daemon logs` prefers over the journal.

**Density guard (2026-10).** Plain Gtk controls in preference cards read oversized
(Adwaita enforces a 34px touch floor that padding-only fixes bounce off). The app CSS now
sets row density for every button/entry/spinbutton/dropdown inside `.card` - future rows
cannot forget a size class and come out chunky. Prefer AdwComboRow/AdwSpinRow for new
rows; the CSS is the safety net, not the primary tool.

**Skill audit round (2026-10, impeccable polish + renders).** The rule dialog gates Add
until the time parses (client mirrors server grammar); Diagnostics gained a summary line
("All 7 checks passed"); the header automation button shows a pause glyph when paused
(visible without hover; initial sync must set syncing or the SetActive reads as a user
click — it toasted into a half-built window and panicked); raw D-Bus errors replaced by
"Start DDC Deck from the app menu to enable them"; DDCDECK_PAGE opens a page at start
(screenshot/testing affordance). The render audit also caught automation left paused from
an earlier test — enabled again.

**Eye care UX round (2026-10).** The filter row speaks with the schedule's voice
(updateBlueSub: "Off — schedule turns it on at sunset"); Set arms only when the controls
diverge from saved state (it rewrites rules.toml — an idle armed button is a trap); levels
renamed (Filter level / Level when scheduled). Width is libadwaita's clamp (600 CSS px),
not ours. Manual toggling never touches the schedule (user decision: schedule survives).

**Schedule & location follow-up (2026-10).** `bluelight schedule` now applies the state
the schedule implies at the moment it is set (bluelightNow: span with midnight wrap, or
whichever sun event comes next) — through the engine, so daemon caches stay right;
previously setting it at 13:29 meant the filter stayed wrong until tomorrow. Location
input became names instead of numbers: `internal/location` indexes all of tzdata's
zone/city rows (`Zones`, `Find` with exact→prefix→substring scoring, `Name` for the
reverse label), `location.set` takes `{"query": "Tokyo"}`, the GUI City row completes
over `location.zones`, and diag/doctor label coordinates with the nearest zone.
Adwaita subtitles are Pango markup — server text is html-escaped before display.

**Parity pass (2026-10): every action works in both the GUI and the CLI.** CLI gained
`rule add|rm|location` (the GUI's rule editor); the GUI gained the blue light schedule
(the CLI's `bluelight schedule`, via `schedule.set`), model Fetch… (`model.fetch`),
Diagnostics (`doctor`, shared `internal/diag`), profile edit/remove (`profile.set`,
replace semantics — merging would resurrect dropped fields), and default-monitor
pin/unpin (`ddcdeck default --clear`). Deliberately CLI-only: `daemon run|logs` (terminal
output), `model export-mccs` (the wizard and `model.fetch` write the file themselves),
`vcp get/set` has its Settings rows, `discover` has the wizard dialog.

**Basics pass 2 (2026-10).** The rest of the first-day gaps:

- **Sun rules anywhere** — `[location]` now defaults to the local timezone's reference
  city, read from the system's own zone.tab/zone1970.tab (Amsterdam stays the last
  resort). zone1970.tab alone is not enough: zones that share a clock since 1970 (Tokyo)
  live only in zone.tab.
- **`ddcdeck doctor`** — one read-only walk of the whole chain (ddcutil, i2c permission,
  monitor answering, model files, daemon, shortcuts portal, sun location), each failure
  with its fix.
- **Rules without a text editor** — `rule.add`/`rule.rm` API methods; the Automation page
  gained Add… (sunset/sunrise/time/connected/disconnected + profile picker) and a trash
  button per rule. App rules still need rules.toml (they need a class list).
- **`ddcdeck model fetch`** — the consumer side of the model database: pulls one file
  from the repo's raw URL, parses, validates, identity-checks (a renamed file cannot
  shadow another model) and saves into the user model dir.
- **`-d all`** — feature commands apply to every connected monitor (`eachDisplay`).
- **Factory reset** — MCCS 0x04 as `ddcdeck reset set factory`; GUI row behind a
  confirmation on the Settings page, never stored in profiles (applying a profile must
  not reset the monitor), hidden from the Display page.

Deliberately not built: a software blue-light/brightness fallback (KDE Night Color and GNOME
Night Light already do this in the compositor; hardware codes stay DDC Deck's job).

**Adoption pass (2026-10).**

- **i18n** — `internal/i18n`: `T("English text")`, the English is the key and the fallback.
  Locales are TOML files embedded from `internal/i18n/locales/` and picked from `LC_ALL` /
  `LC_MESSAGES` / `LANG`; `scripts/gen-locale-template.py` regenerates `template.toml` (CI
  checks it). Only fixed GUI strings are wrapped. Shortcut: strings built with `fmt.Sprintf`
  or concatenation ("Applied " + name) and server-side text (rule descriptions, doctor
  output) stay English; move them to whole-sentence keys when the first translator asks.
- **DDC timing** — `sleep_multiplier` and `disable_dynamic_sleep` in config.toml become global
  ddcutil flags (`ddcutil.Tuning`, set when the service is created, so a daemon restart
  applies them); `doctor` points at them when no monitor answers. Whether DisplayPort alt-mode
  paths are a common cause is unverified.
- **App rules beyond KWin** — `appwatch/wm.go` follows Hyprland (`.socket2.sock`,
  `activewindow>>`) and Sway (`swaymsg -t subscribe`). GNOME needs a shell extension: not done.
- **Model feedback** — `ddcdeck model report works|partial|broken` (prefilled issue, same
  no-upload rule as `submit`). CLI-only for now. `docs/MODELS.md` is generated from
  `data/models/` (`make generate`) as the searchable "is my monitor covered" page.
- **Not done**: a README screenshot/GIF (needs a real capture of the running GUI) and the
  release pipeline.

**App watcher (2026-10, implemented):** a KWin script (embedded, installed to
`~/.local/share/ddcdeck/kwin/`) reports `windowActivated` + the active window's
`resourceClass` to a `SetClass` D-Bus method on `io.github.aic0d3r.ddcdeckd`; the
daemon reloads the script when KWin restarts. An AT-SPI-based cross-desktop watcher
was tried first but event delivery is disabled on the target system, so v1 stays
KWin-only. Pausing automation keeps the sun baseline: a transition during a pause
fires on resume, same as after a restart.

**Model database decision (2026-10): the repo is the database.** Wizard output is submitted
as a GitHub PR/issue (prefilled text generated by the wizard); accepted files ship embedded
in `data/models/`. No online service we run, no phone-home: lookups by mfg-model-product are
opt-in (`ddcdeck model fetch <id>`, built: validated and identity-checked before saving)
and pull a single file from the repo's raw
URL into the user model dir. Submissions are GPL-3.0 like the repo, serials stripped, one
prefilled GitHub issue per monitor (opened by `ddcdeck model submit` / the wizard's Submit
button — the user reviews and presses Submit, ddcdeck uploads nothing itself), PR review is
the moderation, wizard `verified` flags are the quality signal. Auto-lookup at
runtime was explicitly rejected (offline-first, trust boundary: unreviewed VCP writes).

## 8. Known codes — ASUS PA279CRV (product 10209)

| Feature | VCP | Values | Status |
|---|---|---|---|
| Blue light filter | `E6` | 0–20, 0 = off | verified (on/off/levels) |
| Color preset (MCCS) | `14` | 04 5000K, 05 6500K, 08 9300K, 0e/0f/11 = ? | to map |
| Input | `60` | 0f DP, 11 HDMI1, 12 HDMI2, 1a = USB-C? | to verify |
| ProArt preset | `E0`? | current 0x03, max 0x05 | candidate, to diff |
| Brightness / contrast | `10` / `12` | 0–100 | standard |

Open questions:

- Which OSD blue light level maps to which E6 value? The OSD level the user picked read back as 20.
- Is there a KVM/USB-upstream code?
- Can PiP/PbP be controlled over DDC at all?
- Active-app trigger on GNOME needs a Shell extension; KWin, Hyprland and Sway are covered.
