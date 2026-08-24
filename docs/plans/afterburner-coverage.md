# Full Afterburner Coverage Plan

Status: **core VF curve done + verified**. The remaining Afterburner sliders
need their own NVAPI interfaces. Each follows the same reverse-engineer ->
selftest-on-hardware -> verify cycle we used for the curve.

## Current state

| Control | Implemented | Verified on 5090 |
|---------|-------------|------------------|
| Core VF curve (undervolt) | yes | yes (selftest + live read) |
| Core clock read | yes (`watch`) | yes (idle read) |
| Voltage read | yes | yes |
| Memory clock read | yes (`watch`) | needs verification |
| Memory clock offset | **no** | - |
| Power limit % | **no** | - |
| Temp limit C | **no** | - |
| Core voltage offset | **no** | - |
| Overvoltage +100mV | **no** | - |
| Fan curve | out of scope | NVIDIA clamps manual 30% |

## The gating unknown: half-step x2

Before adding ANY new offset control, close the load-test gate: does the card
actually clock to 2812 MHz @ 900 mV under load?

- If ~2812 -> NVAPI offset encoding confirmed, proceed.
- If ~1406 (half) -> Blackwell applies a x2 multiplier on freq offsets. One-line
  fix in `buildOffsets`/`SetOffset`, then re-verify every feature.

This is why feature work is gated on the load test.

## Roadmap (by value, one at a time)

### 1. Memory clock offset

- NVAPI: `NvAPI_GPU_GetAllClockFrequencies` (0xDCB616C3) already reads the
  memory domain. The offset control is a different interface (per-domain clock
  control) - verify function ID + struct before writing.
- Value: +200-300 MHz mem is an independent perf axis, ~3-5% in bandwidth-bound
  games, no stability interaction with the core undervolt.
- Risk: low. Mem OC doesn't crash the way core does; worst case is corruption
  artifacts that are immediately visible.

### 2. Power limit %

- NVAPI: perf-limit policy interface (verify exact ID; likely
  `NvAPI_GPU_SetPowerPoliciesInfo` family).
- Value: cap total board power for temps/noise, complements undervolt.
- Risk: low. Reversible, instant, no stability risk (it's a limit, not an OC).

### 3. Temp limit C

- NVAPI: `NvAPI_GPU_SetThermalSettings` (thermal policies).
- Value: shift throttle point up (OC margin) or down (quieter).
- Risk: low-medium. Raising temp limit pushes the card hotter; must pair with a
  fan curve (which we don't do) or accept higher temps.

### 4. Core voltage offset

- NVAPI: `ClientVoltRails` set control (mirror of the get we already use).
- Value: redundant for undervolting (the curve already pins voltage), but
  needed for a straight +offset if someone wants to OC voltage without a curve.
- Risk: medium. Voltage writes are the sharpest tool; a bad value is a hard
  crash.

### 5. Overvoltage (+100mV)

- NVAPI: requires voltage unlock (the MSI "extended" unlock is a driver hook,
  not a pure NVAPI call - may not be reproducible cleanly).
- Value: only for extreme OC; marginal for a daily undervolt.
- Risk: highest. Skip unless explicitly wanted.

## Selftest gate per feature

Each feature gets its own `selftest-<name>` command mirroring the curve
selftest: write a tiny safe delta, read back, verify, restore. A feature does
not ship until its selftest passes on the card.

## Fan curve

Deliberately out of scope. NVIDIA clamps manual fan % to 30% minimum and 0 RPM
is vBIOS-auto only. FanControl already does fan curves as well as the hardware
allows. No point re-implementing a floor-limited fan controller.
