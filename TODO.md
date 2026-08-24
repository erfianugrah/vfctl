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
- [x] `set` self-cleans stale offsets below ramp (curve switching safe)
- [x] `watch` telemetry: core/mem clock + voltage + offset, CSV log
- [x] Full telemetry: utilization, temperature, power (live draw)
- [x] `--summary` voltage histogram on exit
- [x] `persist` sign-in scheduled task
- [x] Full gaming session verified: 2812@900, 76W avg, 61C max, zero crashes
- [x] Lexicanum reference page

## Next (see docs/plans/afterburner-coverage.md)
- [ ] **Pstates20 reader** - dump pstates/clocks/delta-ranges (scouts
      memory + core + voltage offset in one read-only step)
- [ ] Memory clock offset via Pstates20 + selftest-mem (the perf win)
- [ ] Core clock offset (flat, Pstates20)
- [ ] Core voltage offset (uV, Pstates20)
- [ ] Power limit % (only if memory OC needs it)
- [ ] Perf-limit flags in watch telemetry (PerfPoliciesGetStatus)

## Investigate later
- [ ] TDR recovery: do offsets survive a TDR? (persist model assumes not)
- [ ] Step-up ceiling search mode for `test`
- [ ] JSON profile store (named curves, applied via NVAPI)
- [ ] CI: Windows cross-compile + test on release tag

## Not doing
- Fan control (NVIDIA clamps manual to 30% min; 0 RPM is vBIOS-auto only; FanControl already optimal)
- Linux NVML path (global offset only, no per-point curves)
- Overvoltage +100mV (highest risk, zero value for the goals)
- Temp limit (card never exceeds 61C; the limit never engages)
