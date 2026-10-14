#!/usr/bin/env python3
"""Generate unverified data/models/*.toml from a ddccontrol-db checkout.

    git clone --depth 1 https://github.com/ddccontrol/ddccontrol-db /tmp/dcdb
    scripts/import-ddccontrol.py /tmp/dcdb/db/monitor data/models

ddccontrol-db is GPL-2.0-or-later (compatible with this repo's GPL-3.0). Only vendor-specific
controls are imported; standard MCCS features already come from model.Defaults(). Everything is
written verified = false. Never overwrites an existing file (hand-verified ones win).
"""
import re, sys, glob, os
import xml.etree.ElementTree as ET

# Standard MCCS codes (handled by model.Defaults / the GUI) and dangerous or useless ones.
SKIP_ADDR = {0x02, 0x04, 0x05, 0x06, 0x08, 0x0B, 0x0C, 0x10, 0x12, 0x14, 0x16, 0x18, 0x1A, 0x1E, 0x20,
             0x30, 0x52, 0x60, 0x62, 0x68, 0x6C, 0x6E, 0x70, 0x87, 0x8D, 0xCC, 0xD6}
SKIP_NAME = re.compile(r"default|reset|restore|factory|degauss|power$|dpms|language|newcontrolvalue|firmware|^0x")
RENAME = {"bluelightfilter": "bluelight", "preset_profile": "preset"}
PREFIX = re.compile(r"^(asus|acer|aoc|benq|dell|lg|samsung|philips|viewsonic|lenovo|hp|msi|gigabyte|iiyama|nec|eizo|"
                    r"tuf|rog|proart|nitro|predator|gaming|ultrasharp|alienware|odyssey|tuf)$", re.I)


def ident(s):
    return re.sub(r"[^a-z0-9]+", "_", s.lower()).strip("_")


def edid_model(name):
    """Best guess of the EDID model string from the marketing name; '' if ambiguous."""
    name = re.sub(r"\(.*?\)", "", name)
    if "/" in name or "," in name:
        return ""
    toks = [t for t in name.split() if not PREFIX.match(t)]
    toks = [t for t in toks if re.search(r"\d", t)]
    return toks[0] if toks else ""


def num(s):
    return int(s, 0)


def convert(path):
    root = ET.parse(path).getroot()
    feats = {}
    for c in root.iter("control"):
        cid, addr = c.get("id", ""), c.get("address")
        if not addr:
            continue
        a = num(addr)
        name = RENAME.get(ident(cid), ident(cid))
        if a in SKIP_ADDR or SKIP_NAME.search(name) or not name or name in feats:
            continue
        vals = {}
        for v in c.findall("value"):
            k = ident(v.get("id", ""))
            if k and k[0].isdigit():
                k = "v" + k
            if k and k not in vals:
                vals[k] = num(v.get("value"))
        feats[name] = (a, vals)
    return root.get("name", ""), feats


def main(src, dst):
    made = 0
    for path in sorted(glob.glob(os.path.join(src, "*.xml"))):
        base = os.path.basename(path)[:-4]
        if not re.fullmatch(r"[A-Z]{3}[0-9A-F]{4}", base):
            continue
        mfg, product = base[:3], int(base[3:], 16)
        try:
            name, feats = convert(path)
        except ET.ParseError:
            continue
        model = edid_model(name)
        if not feats or not model:
            continue
        out = os.path.join(dst, f"{mfg}-{model}-{product}.toml")
        if os.path.exists(out):
            continue
        lines = [f"# {name}. Imported from ddccontrol-db ({base}.xml, GPL-2.0+), NOT verified on hardware.",
                 "# Model string and product id are inferred; correct them or confirm with `ddcdeck discover`.",
                 f'mfg     = "{mfg}"', f'model   = "{model}"', f"product = {product}", ""]
        for fname, (a, vals) in feats.items():
            lines += [f"[feature.{fname}]", f"vcp      = {a:#04x}"]
            if vals:
                lines += ['type     = "enum"', "values   = { " + ", ".join(f"{k} = {v:#04x}" for k, v in vals.items()) + " }"]
            else:
                lines += ['type     = "range"']
            lines += ["verified = false", ""]
        open(out, "w").write("\n".join(lines))
        made += 1
    print(f"wrote {made} model files")


if __name__ == "__main__":
    main(*sys.argv[1:3])
