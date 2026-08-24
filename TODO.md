# TODO

## Now (usable today via cfg editing)
- [x] Parse/generate VFCurve blobs (12-byte header, 127 points)
- [x] Afterburner profile cfg read/write
- [x] Undervolt generator (ramp + flatten) with validation
- [x] CLI: list / show / validate / create / apply / hex
- [ ] Apply and stability-test 2797@900 (Profile4) in The Finals
- [ ] If stable, try 2812 (Profile3); if crash, generate 2782/2767

## Next (NVAPI direct - removes Afterburner from the loop)
- [ ] `vfctl probe` - report which NVAPI VF interface the driver exposes (Blackwell ClkVfPoints vs pre-Blackwell ClockBoostTable)
- [ ] Read live base curve via NVAPI at apply time (fixes temperature-stale baseline)
- [ ] Write per-point offsets via `ClockClientClkVfPointsSetControl` (0xC0B82220)
- [ ] Read-back verification after apply
- [ ] Profile store in JSON (ours, not Afterburner's)

## Maybe later
- [ ] Import existing Afterburner profiles into the JSON store
- [ ] Auto step-down: apply, watch for crash, retry -15MHz
- [ ] Ctrl+F5 VFCDump diffing to pin down apply-time behavior empirically

## Not doing
- Fan control (NVIDIA clamps manual to 30% min; 0 RPM is vBIOS-auto only; FanControl already optimal)
- Linux support (NVML is global-offset only; no per-point curves)
