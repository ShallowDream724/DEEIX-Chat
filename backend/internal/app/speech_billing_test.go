package app

import (
	"math"
	"testing"
)

func TestSpeechRateNanousdPreservesSmallPrices(t *testing.T) {
	for _, tc := range []struct {
		name        string
		price, rate float64
		want        int64
	}{
		{"asr hourly", 0.39, 7.2, 54166667},
		{"tts characters", 1.5, 7.2, 208333334},
		{"free", 0, 7.2, 0},
		{"sub nano rounds up", 0.0000000001, 7.2, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := speechRateNanousd(tc.price, tc.rate)
			if err != nil || got != tc.want {
				t.Fatalf("got %d, %v; want %d", got, err, tc.want)
			}
		})
	}
}

func TestSpeechRateNanousdRejectsInvalidRates(t *testing.T) {
	for _, pair := range [][2]float64{{-1, 7.2}, {math.NaN(), 7.2}, {math.Inf(1), 7.2}, {1, 0}, {1, -1}, {1, math.NaN()}, {1, math.Inf(1)}, {math.MaxFloat64, 1}} {
		if _, err := speechRateNanousd(pair[0], pair[1]); err == nil {
			t.Fatalf("accepted invalid rate %v", pair)
		}
	}
}
