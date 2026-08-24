# vfctl

GPU voltage/frequency curve tool for NVIDIA cards. Bypasses MSI Afterburner's broken curve editor.

Two modes:
- **NVAPI direct** (Windows, admin) - read/write curves directly via NVIDIA driver
- **Afterburner cfg** (Linux/WSL) - generate and write VFCurve hex to profile files

Built for an RTX 5090 FE after Afterburner's editor repeatedly produced broken curves (notch artifacts, temperature-stale baselines).

## Quick Start (Windows)

**Run as Administrator:**

```powershell
# Check what APIs are available
.\vfctl.exe probe

# Read live curve from GPU (safe, no admin needed)
.\vfctl.exe live --min-mv 880 --max-mv 950

# Apply undervolt: 2797MHz @ 900mV
.\vfctl.exe set --voltage 900 --freq 2797

# Preview without applying
.\vfctl.exe set --voltage 900 --freq 2797 --dry-run

# Reset to stock
.\vfctl.exe reset

# Persist across reboots (installs a sign-in task)
.\vfctl.exe persist --voltage 900 --freq 2797

# Automate stability search: step down 15 MHz on each TDR
.\vfctl.exe test --voltage 900 --freq 2812 --game "C:\path\to\game.exe" --min-freq 2700 --run-for 30m
```

## Persistence

NVAPI offsets are volatile - they live in driver memory and reset on reboot or
TDR. `persist` installs a Windows scheduled task (`vfctl-undervolt`, logon,
elevated) that re-runs `vfctl set` at every sign-in. Because `set` reads the
live curve at apply time, it is temperature-correct each boot - no stale
baseline.

```powershell
.\vfctl.exe persist --voltage 900 --freq 2797   # install
.\vfctl.exe persist --remove                    # remove
```

## Automated stability testing

`test` runs the apply-then-crash loop: apply a curve, launch the game, watch
for a TDR (Windows Event 4101, nvlddmkm) or a non-zero process exit, and step
down 15 MHz on failure. It stops at the first curve that survives `--run-for`
without a TDR, or at `--min-freq`.

```powershell
.\vfctl.exe test --voltage 900 --freq 2812 --game "C:\games\thefinals.exe"
```

- A clean exit (exit code 0) also stops the loop - treat it as accepted
- A hard hang/BSOD can't be caught in-process; the machine reboots and you
  re-run from the last-known-good frequency
- For games with no benchmark mode, play normally: a TDR auto-steps down and
  relaunches

## Telemetry (HWiNFO)

The crash loop needs no sensors - the TDR event IS the stability signal. Use
HWiNFO for verification telemetry: confirm the card actually runs at the target
voltage/frequency under load and check hotspot temperature. Enable Shared
Memory Support and CSV-log during a session (green + button), then compare
against the curve. Note: shared-memory access is time-limited on the free
version (12 hours); a Pro license lifts it.

## Afterburner cfg mode (Linux/WSL)

```bash
# List profiles
vfctl list

# Show curve
vfctl show Profile2 --min-mv 900 --max-mv 950

# Validate for issues
vfctl validate Profile1

# Generate undervolt (preview)
vfctl create --voltage 900 --freq 2797 --ramp-from 850

# Write to profile (close Afterburner first)
vfctl apply --to Profile3 --voltage 900 --freq 2797

# Just the hex for manual paste
vfctl hex --voltage 900 --freq 2797
```

## How it works

### NVAPI direct (Windows)
- Reads live VF curve via undocumented NVAPI calls
- Computes offsets against the **live** baseline (temperature-correct)
- Writes via `ClockClientClkVfPointsSetControl` (Blackwell) or `ClockBoostTable` (pre-Blackwell)
- No Afterburner needed, no stale config

### Afterburner cfg (Linux/WSL)
- Parses VFCurve hex blobs (12-byte header, 127 points)
- Generates smooth undervolt curves (ramp + flatten)
- Validates for notches/discontinuities before writing
- Writes to `C:\Program Files (x86)\MSI Afterburner\Profiles\*.cfg`

## Build

```bash
# Linux (WSL, for cfg editing)
go build ./cmd/vfctl

# Windows (for NVAPI direct)
GOOS=windows go build -o vfctl.exe ./cmd/vfctl
```

## Notes

- **Write requires Administrator** (error -137 = `NVAPI_INVALID_USER_PRIVILEGE`)
- NVIDIA snaps frequencies to 15MHz steps
- The base curve shifts with temperature - the tool reads live values when applying
- Fan control is out of scope (hardware clamps manual to 30% min, 0 RPM is vBIOS-auto only)

## References

- LACT issue #936: NVAPI VF curve discovery
- PenguinBurner: struct layouts
- nvapioc: pre-Blackwell ClockBoostTable method
