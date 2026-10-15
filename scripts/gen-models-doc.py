#!/usr/bin/env python3
"""Render data/models/*.toml as docs/MODELS.md, the page people search ("does it support my
monitor?"). GitHub's in-page search (Ctrl+F) and file search both work on it. Run via
`make models-doc`; CI fails if the committed page is stale."""
import pathlib
import tomllib

root = pathlib.Path(__file__).resolve().parent.parent
rows = []
for p in sorted((root / "data" / "models").glob("*.toml")):
    m = tomllib.loads(p.read_text())
    feats = m.get("feature", {})
    ok = sorted(k for k, f in feats.items() if f.get("verified"))
    maybe = sorted(k for k, f in feats.items() if not f.get("verified"))
    status = "verified" if ok else "unverified"
    rows.append((m["mfg"], m["model"], p.stem, status, ", ".join(ok) or "-", ", ".join(maybe) or "-"))

out = [
    "# Supported monitors",
    "",
    "Generated from `data/models/` by `scripts/gen-models-doc.py` - do not edit by hand.",
    "",
    "Any DDC/CI monitor gets brightness, contrast, input and power from the standard MCCS",
    "codes with no model file. A model file adds vendor features (blue light, presets, KVM).",
    "**Verified** = tested on real hardware; **unverified** = imported from",
    "[ddccontrol-db](https://github.com/ddccontrol/ddccontrol-db), may need correcting.",
    "Yours missing or wrong? `ddcdeck discover`, then `ddcdeck model submit`; if a file",
    "already works (or not) for you, `ddcdeck model report works|partial|broken`.",
    "",
    f"{len(rows)} models, {sum(r[3] == 'verified' for r in rows)} with verified features.",
    "",
    "| Mfg | Model | ID | Status | Verified features | Unverified features |",
    "|---|---|---|---|---|---|",
]
out += ["| " + " | ".join(r) + " |" for r in rows]
(root / "docs" / "MODELS.md").write_text("\n".join(out) + "\n")
