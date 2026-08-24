# vfctl

GPU voltage/frequency curve tool for NVIDIA cards. Replaces MSI Afterburner's curve editor with verified NVAPI writes, full telemetry, and automated stability search.

Built for an RTX 5090 FE after Afterburner's editor repeatedly produced broken curves (notch artifacts, temperature-stale baselines). Write path verified on live silicon via a single-point selftest; telemetry verified across full gaming sessions.

## Status: complete for the undervolt use case

The full chain works end-to-end on an RTX 5090 FE (driver 610.88):

- **Curve write** - verified: `selftest` writes +15 MHz to one point, reads it back, restores
- **Curve apply** - verified: `set` applies a full ramp+flatten curve with read-back verification
- **Telemetry** - verified: clock, voltage, utilization, temperature, power all live
- **Persistence** - scheduled task at sign-in, ~2s, no resident process

A 2812 MHz @ 900 mV curve held a full 15-minute gaming round at avg 76W / max 61C. Heat goal achieved: stock-or-better perf at a fraction of the 575W budget.

## Quick Start (Windows)

**Run as Administrator** (writes need elevation; reads don't):

```powershell
# Prove the write path (one-shot gate, safe)
.\vfctl.exe selftest

# Read the live curve
.\vfctl.exe live --min-mv 880 --max-mv 950

# Apply undervolt: 2812 MHz @ 900 mV
.\vfctl.exe set --voltage 900 --freq 2812

# Preview without applying
.\vfctl.exe set --voltage 900 --freq 2812 --dry-run

# Reset to stock
.\vfctl.exe reset

# Persist across reboots (sign-in scheduled task)
.\vfctl.exe persist --voltage 900 --freq 2812

# Automate stability search: step down 15 MHz on each TDR
.\vfctl.exe test --voltage 900 --freq 2812 --game "D:\SteamLibrary\steamapps\common\The Finals\Discovery.exe" --min-freq 2700

# Live telemetry: clock/voltage/util/temp/power, CSV log
.\vfctl.exe watch --interval 500ms --summary --csv run.csv
```

## Telemetry (watch)

`watch` samples the full live state every interval (default 1s) and logs to
CSV: core clock, memory clock, voltage, nearest VF point + its offset,
utilization, temperature, and board power. This replaces HWiNFO/overlay for
load-test verification - run it alongside a game and read the verdict from the
data.

```powershell
.\vfctl.exe watch --interval 500ms --summary --csv finals.csv
```

`--summary` prints a per-voltage histogram (time%, clock range) on Ctrl+C, so
a load test is self-contained: play, Ctrl+C, read where the card actually sat.

CSV columns: `unix_ns, elapsed, core_mhz, mem_mhz, volt_mv, point_mv,
offset_mhz, util_pct, temp_c, power_w, read_error`. Analyze with duckdb:

```bash
duckdb -c "SELECT volt_mv, count(*), avg(core_mhz), avg(power_w) FROM 'finals.csv' GROUP BY volt_mv"
```

## What the telemetry revealed (RTX 5090 findings)

Verified across full gaming sessions:

- **The DVFS governor picks voltage, not your curve.** Setting a curve cap at
  860 mV does not make the card run at 860 mV - Blackwell's governor adds
  voltage margin under sustained load and sits at ~930 mV regardless. The curve
  sets the frequency ceiling per voltage; the governor picks where it lives.
- **The cost of that margin is tiny.** ~5W between 880 mV and 930 mV at the
  same clock. Forcing it down requires a declining curve (notch territory).
- **Real-world result**: 2433 MHz avg, 76W avg, 61C max during gameplay -
  stock-or-better perf at 13% of the power budget.
- **Idle power is a memory-domain artifact**: ~33W at desktop with memory
  pinned at 15801 MHz (high-refresh monitor). Not fixable via the VF curve.

## Persistence

NVAPI offsets are volatile - they live in driver memory and reset on reboot or
TDR. `persist` installs a Windows scheduled task (`vfctl-undervolt`, logon,
elevated) that re-runs `vfctl set` at every sign-in (~2s, no resident process).
`set` reads the live curve at apply time, so it is temperature-correct every
boot, and self-cleans any stale offsets below the ramp region.

```powershell
.\vfctl.exe persist --voltage 900 --freq 2812   # install
.\vfctl.exe persist --remove                    # remove
```

## Automated stability testing

`test` runs the apply-then-crash loop: apply a curve, launch the game, watch
for a TDR (Windows Event 4101, nvlddmkm) or a non-zero process exit, and step
down 15 MHz on failure. Stops at the first curve that survives `--run-for`
without a TDR, or at `--min-freq`.

```powershell
.\vfctl.exe test --voltage 900 --freq 2812 --game "D:\...\Discovery.exe"
```

- A clean exit (exit code 0) also stops the loop - treat it as accepted
- A hard hang/BSOD can't be caught in-process; the machine reboots and you
  re-run from the last-known-good frequency

## Afterburner cfg mode (Linux/WSL, legacy import)

```bash
vfctl list                          # profiles in the cfg
vfctl show Profile2 --min-mv 900    # display curve
vfctl validate Profile1             # notch/discontinuity check
vfctl create --voltage 900 --freq 2812 --ramp-from 850   # preview
vfctl apply --to Profile3 --voltage 900 --freq 2812      # write (close Afterburner first)
vfctl hex --voltage 900 --freq 2812                      # raw hex for manual paste
```

## How it works

### NVAPI direct (Windows, primary mode)
- Reads live VF curve via `ClkVfPointsGetInfo/GetStatus/GetControl` (255-point, v3 status / v1 control structs)
- Computes offsets against the **live base** (`vf_tuple_base.freq_kHz`), temperature-correct
- Writes via read-modify-write `ClkVfPointsSetControl` - the driver ignores writes that don't preserve `type_`/`rsvd` from a prior read
- Filters to editable core points only (`type==0 && voltage_based==1`) - never touches memory/other clock domains
- Verifies every write by read-back (within one 15 MHz step)
- Self-cleans stale offsets below the ramp region on every `set`

### Telemetry reads
- Clocks: `NvAPI_GPU_GetAllClockFrequencies` (0xDCB616C3) - documented public API
- Utilization: `NvAPI_GPU_GetDynamicPstatesInfoEx` (0x60DED2ED)
- Temperature: `NvAPI_GPU_GetThermalSettings` (0xE3640A56) - note the 3-arg signature (gpu, sensorIndex, struct)
- Power: `NvAPI_GPU_ClientPowerTopologyGetStatus` (0xEDCF624E) - **NOT** `ClientPowerPoliciesGetStatus` (0x70916171), which returns the policy/limit (frozen ~100%), not live draw
- Voltage: `ClientVoltRailsGetStatus` (0x465F9BCF)

### Afterburner cfg (Linux/WSL, legacy)
- Parses VFCurve hex blobs (12-byte header, 127 points, 3 x float32 LE per point)
- Generates smooth undervolt curves (ramp + flatten) with bounds guard
- Validates for notches/discontinuities before writing

## Build

```bash
go build ./cmd/vfctl                      # Linux (WSL, cfg mode)
GOOS=windows go build -o vfctl.exe ./cmd/vfctl   # Windows (NVAPI mode)
```

Stdlib only, no dependencies.

## Notes

- **Writes require Administrator** (error -137 = `NVAPI_INVALID_USER_PRIVILEGE`); reads work unelevated
- NVIDIA snaps frequencies to 15 MHz steps
- The base curve shifts with temperature; `set` reads live values at apply time
- `mem_mhz` reports the physical GDDR7 clock (~15801 on a 5090)
- Fan control is out of scope (hardware clamps manual to 30% min, 0 RPM is vBIOS-auto only; use FanControl)

## References

- PenguinBurner `hidden_nvapi_vf.py`: VF curve struct layouts (write-capable reference)
- LACT issue #936: NVAPI VF curve discovery (Loong0x00 read-only PoC)
- nvapi-sys (docs.rs): struct layouts for power/thermal/utilization reads
- NVIDIA NVAPI official docs (docs.nvidia.com/nvapi, Release 590): documented clock/thermal APIs
- nvapi-rs enum: function ID registry (decimal -> hex)
