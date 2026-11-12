package detect

import (
	"encoding/binary"
	"math"
	"testing"
	"time"

	"github.com/samyukthasyamsundar/geo-sentinel/internal/geo"
	"github.com/samyukthasyamsundar/geo-sentinel/internal/trace"
)

// FuzzAnalyze builds a trace from arbitrary bytes (valid coordinates, but any timing, spacing and
// accuracy, including duplicates, reversals, zero gaps and extreme values) and runs every check.
// Properties: no panic or hang, every finding has a reason and a valid confidence, and the verdict
// is one of the three labels. Run with: go test -fuzz=FuzzAnalyze -fuzztime=30s ./internal/detect
func FuzzAnalyze(f *testing.F) {
	f.Add([]byte{})
	f.Add(make([]byte, 64))
	f.Add([]byte("\x00\x00\x00\x00\x00\x00\x00\x01\x40\x47\x00\x00\x00\x00\x00\x00\xc0\x5e\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"))
	f.Fuzz(func(t *testing.T, data []byte) {
		const rec = 24 // 4 x uint48-ish fields packed from 24 bytes
		var fixes []trace.Fix
		base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		for i := 0; i+rec <= len(data) && len(fixes) < 400; i += rec {
			lat := unit(binary.LittleEndian.Uint64(data[i:]))*180 - 90
			lon := unit(binary.LittleEndian.Uint64(data[i+8:]))*360 - 180
			secs := int64(int32(binary.LittleEndian.Uint32(data[i+16:])))      // can be negative or huge
			acc := float64(binary.LittleEndian.Uint32(data[i+20:])%2000) / 4.0 // 0..500, with 0.25 steps
			fx := trace.Fix{Point: geo.Point{Lat: lat, Lon: lon}, Time: base.Add(time.Duration(secs) * time.Second), AccuracyM: acc, Line: len(fixes) + 2}
			if secs%7 == 0 {
				fx.AccuracyM = math.NaN() // a fix with no accuracy
			}
			fixes = append(fixes, fx)
		}
		tr := &trace.Trace{Fixes: fixes}
		fs := Analyze(tr, All(DefaultConfig()))
		for _, x := range fs {
			if x.Reason == "" || x.Check == "" {
				t.Fatalf("finding without a reason or check name: %+v", x)
			}
			if x.Confidence < Low || x.Confidence > High || x.Level == "" {
				t.Fatalf("invalid confidence: %+v", x)
			}
			if math.IsNaN(float64(x.Line)) {
				t.Fatal("unreachable")
			}
		}
		switch Judge(fs) {
		case Clean, Suspicious, Spoofed:
		default:
			t.Fatal("unknown verdict")
		}
	})
}

// unit maps 64 random bits to [0,1].
func unit(u uint64) float64 { return float64(u>>11) / float64(1<<53) }
