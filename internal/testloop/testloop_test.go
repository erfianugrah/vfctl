package testloop

import (
	"errors"
	"testing"
)

func TestParseTDRCount(t *testing.T) {
	tests := []struct {
		input    string
		expected int
		wantErr  bool
	}{
		{"0", 0, false},
		{"12", 12, false},
		{"  42\n", 42, false},
		{"", -1, true},
		{"not-a-number", -1, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseTDRCount(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseTDRCount(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.expected {
				t.Errorf("ParseTDRCount(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestStepFreq(t *testing.T) {
	tests := []struct {
		start float64
		step  int
		want  float64
	}{
		{2812, 0, 2812},
		{2812, 1, 2797},
		{2812, 2, 2782},
	}

	for _, tt := range tests {
		if got := StepFreq(tt.start, tt.step); got != tt.want {
			t.Errorf("StepFreq(%v, %v) = %v, want %v", tt.start, tt.step, got, tt.want)
		}
	}
}

func TestMinFreqFloor(t *testing.T) {
	tests := []struct {
		start    float64
		maxSteps int
		want     float64
	}{
		{2812, 10, 2662},
	}

	for _, tt := range tests {
		if got := MinFreqFloor(tt.start, tt.maxSteps); got != tt.want {
			t.Errorf("MinFreqFloor(%v, %v) = %v, want %v", tt.start, tt.maxSteps, got, tt.want)
		}
	}
}

func TestClassify(t *testing.T) {
	errCrash := errors.New("crash")
	tests := []struct {
		name        string
		exitErr     error
		tdrCount    int
		tdrBaseline int
		timedOut    bool
		want        string
	}{
		{"exit-clean", nil, 5, 5, false, "exit-clean"},
		{"exit-crash", errCrash, 5, 5, false, "exit-crash"},
		{"tdr", nil, 6, 5, false, "tdr"},
		{"stable", nil, 5, 5, true, "stable"},
		{"tdr beats timeout", nil, 6, 5, true, "tdr"},
		{"crash beats tdr", errCrash, 6, 5, false, "exit-crash"},
		{"unknown baseline disables tdr", nil, 0, -1, false, "exit-clean"},
		{"unknown baseline, timed out -> stable", nil, 0, -1, true, "stable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Classify(tt.exitErr, tt.tdrCount, tt.tdrBaseline, tt.timedOut); got != tt.want {
				t.Errorf("Classify() = %v, want %v", got, tt.want)
			}
		})
	}
}
