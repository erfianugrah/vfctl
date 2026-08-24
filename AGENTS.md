# AGENTS.md - vfctl

## Purpose

CLI to generate, validate, and apply NVIDIA GPU voltage/frequency curves without MSI Afterburner's curve editor. Built after Afterburner's editor repeatedly produced broken curves (notch artifacts, cliff discontinuities, temperature-stale baselines) on an RTX 5090 FE. The NVAPI write path is verified on live silicon; telemetry is verified across full gaming sessions.

## Stack

- Go 1.24+ (module `github.com/erfianugrah/vfctl`), stdlib only - no dependencies
- Single static binary; cross-compiles `GOOS=windows go build`
- See `~/.pi/agent/skills/software-architecture/SKILL.md` for the bounded-context layout rationale

## Layout

```
cmd/vfctl/main.go         CLI: list / show / validate / create / apply / hex / probe / live / set / reset / selftest / persist / test / watch
internal/vfcurve/         VFCurve blob parse/encode, undervolt generation, validation
internal/curve/           Live-curve math: StockAt / BuildOffsets / VerifyOffsets / ApplyCurve
internal/testloop/        TDR-loop logic: ParseTDRCount / StepFreq / MinFreqFloor / Classify
internal/profile/         Afterburner profile .cfg read/write (INI-like)
internal/nvapi/           Windows-only syscall layer over nvapi64.dll (curve + telemetry)
testdata/                 Real curves captured from the 5090 FE (stock, broken)
```

## Key facts (verified, do not re-learn)

### Curve format and semantics

- **VFCurve blob** (Afterburner cfg): 12-byte header (magic u32 `0x00020000`, count u32 = 127, flags u32), then N x 3 float32 LE (voltage mV, freq MHz, offset MHz), zero-padded to 3224 bytes. An early Python prototype used an 888-char header - wrong, regression-tested.
- The stored `freq` in a saved profile is the FINAL curve at save time, not vBIOS stock (Unwinder, Guru3D). Only the offset is real. Generate only from a zero-offset (stock) profile - `generate()` refuses otherwise.
- The base curve shifts with temperature (GPU Boost). Offsets computed against a cold baseline display differently when warm - inherent to the interface, affects Afterburner too. `set` reads the live base at apply time, so it is temperature-correct.
- NVIDIA frequency steps: 15 MHz.

### NVAPI curve writes (Blackwell, verified by selftest on 5090)

- **Write requires Administrator** (error -137 = `NVAPI_INVALID_USER_PRIVILEGE`); reads work unelevated.
- **Write flow is READ-MODIFY-WRITE**: GetControl fills type_/rsvd for all points, then change one freq_offset_kHz, then SetControl writes the whole table back with the full active mask. A fresh zeroed buffer does NOT stick (driver ignores writes to points whose type_ doesn't match what it returned).
- **Only core points are editable**: filter by info-struct `type==0 && voltage_based==1`. The 255-point buffer also carries memory/other clock domains - writing those offsets corrupts them (points 127-131 showed 13-14 GHz values).
- **The base frequency is `vf_tuple_base.freq_kHz` (status +12), not `freq_kHz` (+4)**: the latter is nominal (base + applied offset). Using the nominal double-counts on a non-stock card. `FreqKHz` in `VFPoint` is the base; `NominalKHz` is kept for display.
- Offsets apply at 1x, not 2x - disproven empirically (890mV: base 1755 + 714 = 2469, observed ~2500; a x2 would read ~2112).

### NVAPI telemetry reads (verified live)

- Clocks: `NvAPI_GPU_GetAllClockFrequencies` (0xDCB616C3) - documented public API, CURRENT_FREQ via ClockType=0, GRAPHICS=domain[0], MEMORY=domain[4].
- Utilization: `NvAPI_GPU_GetDynamicPstatesInfoEx` (0x60DED2ED), domain 0 percentage.
- Temperature: `NvAPI_GPU_GetThermalSettings` (0xE3640A56) - **3-arg call** (gpu, sensorIndex, struct); missing the sensorIndex silently returns zeros.
- **Power: `NvAPI_GPU_ClientPowerTopologyGetStatus` (0xEDCF624E)** - live draw. **NOT** `ClientPowerPoliciesGetStatus` (0x70916171), which returns the power-limit POLICY (frozen ~100%), not instantaneous draw. One name apart, completely different data.
- Voltage: `ClientVoltRailsGetStatus` (0x465F9BCF), value at +0x28.

### RTX 5090 behavior findings (verified via telemetry)

- **The DVFS governor picks the operating voltage, not the curve.** A curve capped at 860 mV still runs at ~930 mV under sustained load - Blackwell adds voltage margin when load persists, independent of the curve. The curve sets the frequency ceiling per voltage; the governor chooses where to live. Forcing lower voltage needs a declining curve (notch territory).
- The governor's margin costs ~5W (880 vs 930 mV at same clock).
- Idle ~33W with memory pinned at 15801 MHz (high-refresh monitor) - memory-domain behavior, not fixable via the VF curve.
- 2812 @ 900 mV is stable; 2827 (old driver's Profile1) crashes on driver 610.88. The stability cliff is one 15 MHz step wide.

## NVAPI structs (verified via ctypes against PenguinBurner + nvapi-sys)

Three curve structs, all using the SAME 255-point index space and the active-point mask read from GetInfo (0x507B4B59). Do not mix generations.

```
InfoV1 (0x507B4B59) - active mask + point types:
  size 0x182C (6188)
  vf_points_mask: u32[8] at 0x04
  points: 255 x 24 bytes at 0x44
    +0x00: type (u32)
    +0x04: b_voltage_based (u8)

StatusV3 (0x21537AD4) - freq/voltage, version 3:
  size 0x15B0C (88844)
  vf_points_mask: u32[8] at 0x04
  points: 255 x 348 bytes at 0x68
    +0x00: type (u32)
    +0x04: freq_kHz (u32, NOMINAL - base + offset)
    +0x08: voltage_uV (u32)
    +0x0C: vf_tuple_base.freq_kHz (u32, TRUE BASE)
    +0x34: vf_tuple_offset

ControlV1 (0x23F1B133 read / 0x0733E009 write) - offsets:
  size 0x2420 (9248)
  vf_points_mask: u32[8] at 0x04
  points: 255 x 36 bytes at 0x44
    +0x00: type (u32)
    +0x04: rsvd[16]
    +0x14: prog.freq_offset_kHz (i32)   <-- 0x44 + i*36 + 20
```

Power (NV_GPU_POWER_TOPO_V1, 72 bytes): version u32, count u32, entries[4] x 16 bytes each { a, b, power_mW, d }, power at entry+8.

Thermal (NV_GPU_THERMAL_SETTINGS_V2): version u32, count u32, sensor[3] x 20 bytes each { controller, defaultMin, defaultMax, currentTemp (i32), target }, currentTemp at 8+12.

The earlier 0x48 base and 128-point/28-byte layout came from Loong0x00's read-only PoC, which reused the status stride for the control read and never exercised a write - the 4-byte error only surfaced once selftest actually attempted a write.

## Commands

```bash
go build ./...                      # build
go test ./...                       # tests (fixtures in testdata/)
GOOS=windows go build -o vfctl.exe ./cmd/vfctl

# NVAPI direct (Windows) - primary mode
./vfctl.exe selftest                # one-shot write-path proof (admin)
./vfctl.exe live --min-mv 880       # read live curve (no admin)
./vfctl.exe set --voltage 900 --freq 2812    # apply + verify (admin)
./vfctl.exe reset                   # zero all offsets (admin)
./vfctl.exe persist --voltage 900 --freq 2812   # sign-in task (admin)
./vfctl.exe watch --summary --csv run.csv       # telemetry (no admin)
./vfctl.exe test --voltage 900 --freq 2812 --game <exe>  # TDR step-down (admin)

# Afterburner cfg mode (Linux/WSL) - legacy import
./vfctl list / show / validate / create / apply / hex
```

## Conventions

- Default `--cfg` is the 5090 FE profile file path (WSL mount). Override for other machines.
- `set` self-cleans stale offsets below the ramp region - switching curves never stacks.
- `apply` refuses to write a curve that fails validation.
- Afterburner must be closed before `apply` (it overwrites the cfg from memory on exit).
- Every write is verified by read-back within one 15 MHz step.

## Roadmap

See TODO.md and docs/plans/afterburner-coverage.md. Undervolt use case is complete; remaining items are perf-oriented (memory offset, power limit) not heat.
