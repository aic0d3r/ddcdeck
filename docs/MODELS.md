# Supported monitors

Generated from `data/models/` by `scripts/gen-models-doc.py` - do not edit by hand.

Any DDC/CI monitor gets brightness, contrast, input and power from the standard MCCS
codes with no model file. A model file adds vendor features (blue light, presets, KVM).
**Verified** = tested on real hardware; **unverified** = imported from
[ddccontrol-db](https://github.com/ddccontrol/ddccontrol-db), may need correcting.
Yours missing or wrong? `ddcdeck discover`, then `ddcdeck model submit`; if a file
already works (or not) for you, `ddcdeck model report works|partial|broken`.

92 models, 1 with verified features.

| Mfg | Model | ID | Status | Verified features | Unverified features |
|---|---|---|---|---|---|
| ACI | MG28U | ACI-MG28U-10407 | unverified | - | ascr, bluelight, displayscaling, ecomode, gamevisualdemo, keylock, powerkeylock, powerled, saturation, skintone, smartview, tracefree, vividpixel |
| ACI | PA238 | ACI-PA238-9137 | unverified | - | bluehue, bluesaturate, coarse, cyanhue, cyansaturate, fine, greenhue, greensaturate, magentahue, magentasaturate, redhue, redsaturate, saturation, sharpness, yellowhue, yellowsaturate |
| ACR | ED273UR | ACR-ED273UR-1682 | unverified | - | blackboost, bluehue, bluesaturate, cyanhue, cyansaturate, dynamiccontrast, greenhue, greensaturate, magentahue, magentasaturate, mode, redhue, redsaturate, supersharpness, yellowhue, yellowsaturate |
| ACR | KA242Y | ACR-KA242Y-1835 | unverified | - | blackboost, bluehue, bluesaturate, cyanhue, cyansaturate, gamma, greenhue, greensaturate, magentahue, magentasaturate, mode, redhue, redsaturate, yellowhue, yellowsaturate |
| ACR | VG270U | ACR-VG270U-1743 | unverified | - | blackboost, bluehue, bluesaturate, cyanhue, cyansaturate, greenhue, greensaturate, magentahue, magentasaturate, mode, redhue, redsaturate, yellowhue, yellowsaturate |
| ACR | XB273U | ACR-XB273U-1866 | unverified | - | blackboost, bluehue, bluesaturate, cyanhue, cyansaturate, gamma, greenhue, greensaturate, magentahue, magentasaturate, mode, redhue, redsaturate, yellowhue, yellowsaturate |
| ACR | XV273K | ACR-XV273K-1713 | unverified | - | bluehue, bluesaturate, cyanhue, cyansaturate, greenhue, greensaturate, magentahue, magentasaturate, redhue, redsaturate, yellowhue, yellowsaturate |
| ACR | XV275K | ACR-XV275K-2699 | unverified | - | blackboost, bluehue, bluesaturate, cyanhue, cyansaturate, greenhue, greensaturate, magentahue, magentasaturate, redhue, redsaturate, yellowhue, yellowsaturate |
| AOC | Q3279VWF | AOC-Q3279VWF-12921 | unverified | - | freesync, osd |
| AUS | PA279CRV | AUS-PA279CRV-10209 | verified | bluelight, preset | input |
| AUS | VG27AQ | AUS-VG27AQ-10018 | unverified | - | bluelight, ecomode, osd, saturation, shadowboost |
| AUS | VG328QA1A | AUS-VG328QA1A-12973 | unverified | - | alignmentframe, bluelight, ecomode, fps_counter, osd, saturation, shadowboost, timer, variableod |
| AUS | VG34VQ3B | AUS-VG34VQ3B-13395 | unverified | - | bluelight, crosshair, fps_counter, saturation |
| AUS | VP28U | AUS-VP28U-10417 | unverified | - | ascr, bluelight, displayscaling, ecomode, keylock, powerkeylock, powerled, saturation, skintone, smartview, sourcecolorcoding, tracefree, vividpixel |
| BNQ | BL2410 | BNQ-BL2410-33537 | unverified | - | bnqmode, coarse, hue, osd, saturation, screenorientation |
| BNQ | GW2283 | BNQ-GW2283-30953 | unverified | - | bnqbrightintelligence, bnqlightmeter, osd |
| BNQ | GW2480 | BNQ-GW2480-30951 | unverified | - | bnqbrightintelligence, bnqlightmeter, osd |
| BNQ | RL2450 | BNQ-RL2450-32526 | unverified | - | osd |
| BNQ | XL2411P | BNQ-XL2411P-32563 | unverified | - | bnqmode, bsforce, bslength, bsphase |
| BNQ | XL2411Z | BNQ-XL2411Z-32561 | unverified | - | bnqmode, bsforce, bslength, bsphase |
| DEL | 1907FP | DEL-1907FP-16405 | unverified | - | osd |
| DEL | 1909W | DEL-1909W-41021 | unverified | - | magicbright |
| DEL | P1100 | DEL-P1100-20651 | unverified | - | autosize, colorreturn, vbottommisconvergence, vtopmisconvergence |
| DEL | P2219H | DEL-P2219H-41236 | unverified | - | osdorientation |
| DEL | P2415Q | DEL-P2415Q-41150 | unverified | - | coloruniformity, dynamiccontrast, magicbright |
| DEL | P2416D | DEL-P2416D-41155 | unverified | - | dellpaper, magicbright |
| DEL | P2417H | DEL-P2417H-41179 | unverified | - | magicbright |
| DEL | P2719HC | DEL-P2719HC-16775 | unverified | - | dellpaper, magicbright |
| DEL | P3222QE | DEL-P3222QE-16965 | unverified | - | magicbright, osdorientation |
| DEL | P3223DE | DEL-P3223DE-17045 | unverified | - | energysaving2, magicbright, osdorientation |
| DEL | P3225QE | DEL-P3225QE-17279 | unverified | - | energysaving2, magicbright, osdorientation |
| DEL | S2316M | DEL-S2316M-53376 | unverified | - | dellpaper, magicbright |
| DEL | S2721D | DEL-S2721D-41370 | unverified | - | energysaving2, osd |
| DEL | S2721DGFA | DEL-S2721DGFA-16857 | unverified | - | buttonaccess, energysaving2 |
| DEL | S2721DGFA | DEL-S2721DGFA-16858 | unverified | - | buttonaccess, energysaving2 |
| DEL | S3221QS | DEL-S3221QS-53511 | unverified | - | bdistortionbalance, bdistortionctrl, coarse, colortemp, febrightness, fine, focus, hmisconvergence, hmoire, hpincushion, hpincushionbalance, hsize, inputsource_sub1, key, keybalance, magicbright, osd, osdorientation, pbp, settings, tdistortionbalance, tdistortionctrl, tilt, vlinearity, vlinearitybalance, vmisconvergence, vmoire, vsize |
| DEL | T15A | DEL-T15A-16627 | unverified | - | febrightness, osd |
| DEL | U2414H | DEL-U2414H-41124 | unverified | - | dellpaper, magicbright, osdorientation |
| DEL | U2518D | DEL-U2518D-16698 | unverified | - | magicbright |
| DEL | U2520D | DEL-U2520D-41294 | unverified | - | magicbright, osd, osdorientation, settings |
| DEL | U2713HM | DEL-U2713HM-16512 | unverified | - | magicbright, osd |
| DEL | U2715H | DEL-U2715H-53353 | unverified | - | osdorientation |
| DEL | U2719D | DEL-U2719D-16730 | unverified | - | magicbright, osdorientation |
| DEL | U2720Q | DEL-U2720Q-16823 | unverified | - | magicbright |
| DEL | U2720QM | DEL-U2720QM-16828 | unverified | - | magicbright |
| DEL | U2721DE | DEL-U2721DE-16864 | unverified | - | magicbright, osdorientation |
| DEL | U3219Q | DEL-U3219Q-41251 | unverified | - | inputsource_sub1, pbp |
| DEL | U3219Q | DEL-U3219Q-41252 | unverified | - | inputsource_sub1, pbp |
| DEL | U3219Q | DEL-U3219Q-41267 | unverified | - | osdorientation |
| DEL | U3415W | DEL-U3415W-41126 | unverified | - | dellpaper, uniformitycompensation |
| DEL | U3421WE | DEL-U3421WE-41345 | unverified | - | dellpaper, magicbright, pbp |
| DEL | U3818DW | DEL-U3818DW-41203 | unverified | - | inputsource_sub1, pbp |
| DEL | U4021QW | DEL-U4021QW-16902 | unverified | - | inputsource_sub1, kvm_switch, magicbright, osd, pbp, settings |
| DEL | u2410 | DEL-u2410-61463 | unverified | - | magicbright |
| DEL | u3011 | DEL-u3011-16483 | unverified | - | magicbright |
| DEL | u3011 | DEL-u3011-16485 | unverified | - | magicbright |
| GSM | 27GL850 | GSM-27GL850-23423 | unverified | - | blackstabilization, bluehue, bluesaturate, colortempmanual, cyanhue, cyansaturate, energysaving, freesync, gammamode, greenhue, greensaturate, lgmode, magentahue, magentasaturate, osdlock, ratio, redhue, redsaturate, responsetime, yellowhue, yellowsaturate |
| GSM | 32GS95UV-W | GSM-32GS95UV-W-30735 | unverified | - | energysaving, osdlock |
| GSM | IPS226V-PN | GSM-IPS226V-PN-22535 | unverified | - | femode, gamma, powerled |
| GSM | L1760TR | GSM-L1760TR-17501 | unverified | - | fengine, fengine_ace, fengine_brightness, fengine_rcm |
| GSM | L1760TR | GSM-L1760TR-17502 | unverified | - | fengine, fengine_ace, fengine_brightness, fengine_rcm |
| GSM | L1952HM | GSM-L1952HM-19183 | unverified | - | gamma, sharpness |
| GSM | L1953S | GSM-L1953S-19262 | unverified | - | gamma, sharpness |
| GSM | L2000C | GSM-L2000C-20026 | unverified | - | gamma, powerled |
| GSM | Monitor-19M38HB | GSM-Monitor-19M38HB-23245 | unverified | - | dvmode, gamma, superbright |
| HWP | HWP2943 | HWP-HWP2943-10563 | unverified | - | osdorientation |
| IVM | GB3461WQSU | IVM-GB3461WQSU-30228 | unverified | - | osd |
| IVM | PL2730H | IVM-PL2730H-26170 | unverified | - | dynamiccontrast |
| IVM | PL3494WQ | IVM-PL3494WQ-30257 | unverified | - | osd, ratio |
| IVM | T2735M | IVM-T2735M-26127 | unverified | - | osd |
| LCA | 321 | LCA-321-8640 | unverified | - | osd, osd2, resolutionnotifier, sharpness |
| LEN | E27q-20 | LEN-E27q-20-25296 | unverified | - | osd |
| LEN | L32p-30 | LEN-L32p-30-26313 | unverified | - | dynamiccontrast, freesync, hdr, scenario, variableod |
| LEN | Y27gq-20 | LEN-Y27gq-20-26092 | unverified | - | backlight, blackboost, bluesaturate, cyansaturate, gamma, greensaturate, magentasaturate, redsaturate, saturation, yellowsaturate |
| NEC | 2080UX+ | NEC-2080UX+-26155 | unverified | - | osd, sharpness |
| NEC | 2080UX | NEC-2080UX-26117 | unverified | - | osd, saturation, sharpness |
| NEC | 20WGX2 | NEC-20WGX2-26265 | unverified | - | sharpness |
| PHL | 221P6Q | PHL-221P6Q-2268 | unverified | - | powerled |
| SAM | 171P | SAM-171P-29 | unverified | - | coarse, fine |
| SAM | 173s | SAM-173s-163 | unverified | - | coarse, fine |
| SAM | 244T | SAM-244T-486 | unverified | - | gamma |
| SAM | 245T | SAM-245T-757 | unverified | - | gamma, magicbright |
| SAM | 997MB | SAM-997MB-265 | unverified | - | magicbright |
| SAM | C49RG9x | SAM-C49RG9x-3996 | unverified | - | responsetime |
| SAM | E1920 | SAM-E1920-1699 | unverified | - | buttonaccess, coloreffect, customkey, ecomode, gamma, magicangle, magicbright, screensize |
| SAM | G5 | SAM-G5-28953 | unverified | - | focus, settings |
| SAM | G9 | SAM-G9-29814 | unverified | - | samsung_lighting, samsung_lighting_color, samsung_lighting_effect, screensize |
| SAM | S22A100N | SAM-S22A100N-2152 | unverified | - | settings |
| SAM | S24F354FHU | SAM-S24F354FHU-3360 | unverified | - | ecomode, gamma, magicbright, responsetime |
| STN | 91S | STN-91S-25 | unverified | - | coarse, fine |
| VSC | VG2253 | VSC-VG2253-2355 | unverified | - | osd |
| VSC | VSCBD44 | VSC-VSCBD44-48452 | unverified | - | coarse, fine, osd |
