# vfctl

GPU voltage/frequency curve tool for NVIDIA cards. Two modes:

1. **Afterburner cfg editing** (Linux/WSL) - generate and write VFCurve hex to profile files
2. **NVAPI direct** (Windows, requires admin) - read/write curves directly via NVIDIA driver

Built for an RTX 5090 FE after Afterburner's editor repeatedly produced broken curves.

## Quick Start (Windows, Direct)

```powershell
# Run as Administrator

# Check what APIs are available
.\vfctl.exe probe

# Read live curve from GPU
.\vfctl.exe live --min-mv 880 --max-mv 950

# Apply undervolt: 2797MHz @ 900mV
.\vfctl.exe set --voltage 900 --freq 2797

# Reset to stock
.\vfctl.exe reset
```

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

### Direct NVAPI (Windows)
- Reads live VF curve from GPU via undocumented NVAPI calls
- Computes per-point offsets against the live baseline (temperature-correct)
- Writes offsets via `ClockClientClkVfPointsSetControl` (Blackwell) or `ClockBoostTable` (pre-Blackwell)
- No Afterburner needed, no stale config files

### Afterburner cfg (Linux/WSL)
- Parses VFCurve hex blobs from profile files
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

- **Write requires Administrator** on Windows (NVAPI_INVALID_USER_PRIVILEGE)
- NVIDIA snaps frequencies to 15MHz steps
- The base curve shifts with temperature - the tool reads live values when applying
- Fan control is out of scope (hardware clamps manual to 30% min, 0 RPM is vBIOS-auto only)

## References

- LACT issue #936: NVAPI VF curve discovery
- PenguinBurner: struct layouts
- nvapioc: pre-Blackwell ClockBoostTable method
