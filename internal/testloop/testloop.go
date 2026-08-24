package testloop

import (
	"strconv"
	"strings"
)

// ParseTDRCount parses the raw stdout from the PowerShell Measure-Object.Count command.
func ParseTDRCount(out string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return -1, err
	}
	return n, nil
}

// StepFreq returns the frequency after stepping down by 15 MHz per step.
func StepFreq(startFreq float64, step int) float64 {
	return startFreq - float64(step)*15
}

// MinFreqFloor returns the minimum frequency floor after maxSteps of 15 MHz steps.
func MinFreqFloor(startFreq float64, maxSteps int) float64 {
	return startFreq - float64(maxSteps)*15
}

// Classify decides an attempt's outcome from its observed signals.
// Precedence: exit (clean/crash) > tdr > stable. A tdrBaseline of -1 means
// the baseline read failed and TDR detection is disabled - never classify
// a TDR against an unknown baseline.
func Classify(exitErr error, tdrCount, tdrBaseline int, timedOut bool) string {
	if exitErr != nil {
		return "exit-crash"
	}
	if tdrBaseline >= 0 && tdrCount > tdrBaseline {
		return "tdr"
	}
	if timedOut {
		return "stable"
	}
	return "exit-clean"
}
