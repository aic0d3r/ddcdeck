# DDC Deck

Monitor control for Linux in the style of ASUS DisplayWidget Center: brightness, blue light
filter, color presets, input switching, profiles and automation (sunset, dock/undock, active
app, hotkeys). Works with any DDC/CI monitor; vendor-specific features come from community
model files.

Status: design phase, see [docs/DESIGN.md](docs/DESIGN.md).

## Use the model data with ddcutil today

```sh
cp data/mccs/*.mccs ~/.local/share/ddcutil/
ddcutil getvcp E6   # → "Blue Light Filter" on an ASUS PA279CRV
```
