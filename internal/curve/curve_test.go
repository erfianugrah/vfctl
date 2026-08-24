package curve

import (
	"testing"

	"github.com/erfianugrah/vfctl/internal/nvapi"
)

func TestBuildOffsets(t *testing.T) {
	points := []nvapi.VFPoint{
		{Index: 0, VoltageUV: 800000, FreqKHz: 2000000, OffsetKHz: 0},
		{Index: 1, VoltageUV: 850000, FreqKHz: 2100000, OffsetKHz: 0},
		{Index: 2, VoltageUV: 900000, FreqKHz: 2200000, OffsetKHz: 0},
		{Index: 3, VoltageUV: 950000, FreqKHz: 2300000, OffsetKHz: 0},
	}

	tests := []struct {
		name     string
		voltage  float64
		freq     float64
		rampFrom float64
		want     map[int]int32
		wantErr  bool
	}{
		{
			name:     "bounds guard rejects high freq",
			voltage:  900,
			freq:     27970,
			rampFrom: 850,
			want:     nil,
			wantErr:  true,
		},
		{
			name:     "bounds guard rejects low voltage",
			voltage:  500,
			freq:     2797,
			rampFrom: 850,
			want:     nil,
			wantErr:  true,
		},
		{
			name:     "ramp/flatten math",
			voltage:  900,
			freq:     2200,
			rampFrom: 850,
			want: map[int]int32{
				1: 0,
				2: 0,
				3: -100000,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BuildOffsets(points, tt.voltage, tt.freq, tt.rampFrom)
			if (err != nil) != tt.wantErr {
				t.Errorf("BuildOffsets() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if len(got) != len(tt.want) {
					t.Errorf("got %v, want %v", got, tt.want)
				}
				for k, v := range tt.want {
					if got[k] != v {
						t.Errorf("at %d: got %d, want %d", k, got[k], v)
					}
				}
			}
		})
	}
}

func TestCompareOffsets(t *testing.T) {
	after := []nvapi.VFPoint{
		{Index: 0, VoltageUV: 80000, FreqKHz: 2000000, OffsetKHz: 0},
		{Index: 1, VoltageUV: 850000, FreqKHz: 2100000, OffsetKHz: 10000},
		{Index: 2, VoltageUV: 900000, FreqKHz: 2200000, OffsetKHz: 0},
	}

	tests := []struct {
		name    string
		want    map[int]int32
		wantErr bool
	}{
		{
			name:    "within-one-step tolerance passes",
			want:    map[int]int32{1: 15000},
			wantErr: false,
		},
		{
			name:    "mismatch beyond one step",
			want:    map[int]int32{1: 30000},
			wantErr: true,
		},
		{
			name:    "missing point",
			want:    map[int]int32{3: 0},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := compareOffsets(after, tt.want)
			if (err != nil) != tt.wantErr {
				t.Errorf("compareOffsets() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestMath(t *testing.T) {
	if abs64(-50) != 50 {
		t.Errorf("abs64(-50) = %d, want 50", abs64(-50))
	}
	if AbsF(-50.5) != 50.5 {
		t.Errorf("AbsF(-50.5) = %f, want 50.5", AbsF(-50.5))
	}
}
