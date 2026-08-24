# TODO

## Done
- [x] Parse/generate VFCurve blobs (12-byte header, 127 points)
- [x] Afterburner profile cfg read/write
- [x] Undervolt generator (ramp + flatten) with validation
- [x] CLI: list / show / validate / create / apply / hex
- [x] NVAPI layer: probe, live curve read, per-point offset write
- [x] CLI: probe / live / set / reset / selftest (NVAPI direct, Windows)
- [x] Error message lookup (NVAPI_GetErrorMessage)
- [x] NVAPI write path verified (selftest: +15000 read back +15000)
- [x] Core-domain filter (excludes memory/other clock domains)
- [x] Base-frequency field (vf_tuple_base, not nominal)
- [x] `set` verifies read-back (parity with reset)
- [x] `watch` telemetry: live core/mem clock + voltage + offset, CSV log

## Now (the load-test gate)
- [ ] Load the card, confirm it clocks to 2812 MHz @ 900 mV under load
  (not 1406 - the half-step x2 question). `vfctl watch --csv out.csv`
- [ ] If half: fix x2 multiplier, re-verify
- [ ] Confirm 2812@900 stable in The Finals (NVAPI-applied, not Afterburner cfg)
- [ ] Then: uninstall Afterburner

## Next (full coverage - see docs/plans/afterburner-coverage.md)
- [ ] Memory clock offset (independent perf axis)
- [ ] Power limit % (temps/noise)
- [ ] Temp limit C
- [ ] Core voltage offset (redundant with curve, for OC)
- [ ] Overvoltage +100mV (highest risk, last)

## Maybe later
- [ ] JSON profile store (named curves, applied via NVAPI)
- [ ] Auto step-down on failure integrated with `test`
- [ ] Import existing Afterburner profiles into JSON store
- [ ] CI: Windows cross-compile + test on release tag

## Not doing
- Fan control (NVIDIA clamps manual to 30% min; 0 RPM is vBIOS-auto only; FanControl already optimal)
- Linux NVML path (global offset only, no per-point curves)
