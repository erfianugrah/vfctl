# TODO

## Done
- [x] Parse/generate VFCurve blobs (12-byte header, 127 points)
- [x] Afterburner profile cfg read/write
- [x] Undervolt generator (ramp + flatten) with validation
- [x] CLI: list / show / validate / create / apply / hex
- [x] NVAPI layer: probe, live curve read, per-point offset write
- [x] CLI: probe / live / set / reset (NVAPI direct, Windows)
- [x] Error message lookup (NVAPI_GetErrorMessage)
- [x] Verified live on RTX 5090 FE: probe + live read + write attempt
- [x] Identified admin requirement for writes (-137 = NVAPI_INVALID_USER_PRIVILEGE)

## Now (stability testing)
- [ ] Confirm 2812MHz @ 900mV (Profile3) stable in The Finals
- [ ] If crash: Profile4 (2797) or `vfctl set --voltage 900 --freq 2797`
- [ ] If stable: consider 890mV at same clock for lower temps

## Next (NVAPI polish)
- [ ] Verify write path with admin rights (blocked on user running elevated)
- [ ] Read-back verification after set (compare offsets post-write)
- [ ] Auto step-down on failure: apply, if write fails midway, reset
- [ ] JSON profile store (named undervolt profiles, applied via NVAPI)
- [ ] `--watch` mode: live voltage/freq/temp polling for stability monitoring

## Maybe later
- [ ] Import existing Afterburner profiles into JSON store
- [ ] VFCDump diffing (Ctrl+F5) to understand apply-time behavior
- [ ] CI: Windows cross-compile + test on release tag

## Not doing
- Fan control (NVIDIA clamps manual to 30% min; 0 RPM is vBIOS-auto only; FanControl already optimal)
- Linux NVML path (global offset only, no per-point curves)
