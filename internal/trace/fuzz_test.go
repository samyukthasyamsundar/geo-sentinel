package trace

import (
	"math"
	"strings"
	"testing"
)

// Run with: go test -fuzz=FuzzParseCSV -fuzztime=30s ./internal/trace
// The properties: never panic, never hang, and anything accepted must be internally sane.

func checkTrace(t *testing.T, tr *Trace) {
	t.Helper()
	if tr == nil {
		t.Fatal("nil trace with nil error")
	}
	if len(tr.Fixes) == 0 || len(tr.Fixes) > MaxPoints {
		t.Fatalf("accepted trace has %d fixes", len(tr.Fixes))
	}
	for _, f := range tr.Fixes {
		if math.IsNaN(f.Lat) || math.IsNaN(f.Lon) || math.IsInf(f.Lat, 0) || math.IsInf(f.Lon, 0) {
			t.Fatalf("accepted a non-finite coordinate: %+v", f)
		}
		if f.Time.IsZero() {
			// CSV requires a time; GPX may legitimately omit it (Validate reports that separately).
			t.Fatalf("CSV fix without a time: %+v", f)
		}
		if f.HasAccuracy() && math.IsInf(f.AccuracyM, 0) {
			t.Fatalf("accepted infinite accuracy: %+v", f)
		}
	}
}

func FuzzParseCSV(f *testing.F) {
	f.Add(goodCSV)
	f.Add("time,lat,lon\n1700000000,1,2\n")
	f.Add("\xef\xbb\xbftime,lat,lon\n2026-11-12T10:00:00Z,0,0\n")
	f.Add("time,lat,lon,accuracy\n1e9,-90,180,0\n")
	f.Add("lat,lon,time\n\"1\",\"2\",\"3\"\n")
	f.Fuzz(func(t *testing.T, in string) {
		tr, err := ParseCSV(strings.NewReader(in))
		if err == nil {
			checkTrace(t, tr)
		}
	})
}

func FuzzParseGPX(f *testing.F) {
	f.Add(goodGPX)
	f.Add(`<gpx><trkpt lat="1" lon="2"><time>2026-11-12T10:00:00Z</time></trkpt></gpx>`)
	f.Add(`<?xml version="1.0"?><!DOCTYPE gpx [<!ENTITY a "x">]><gpx><trkpt lat="1" lon="2"/></gpx>`)
	f.Add(`<gpx><trkpt lat="1e400" lon="2"/></gpx>`)
	f.Fuzz(func(t *testing.T, in string) {
		tr, err := ParseGPX(strings.NewReader(in))
		if err == nil {
			if tr == nil || len(tr.Fixes) == 0 {
				t.Fatal("accepted a document with no fixes")
			}
			for _, fx := range tr.Fixes {
				if math.IsNaN(fx.Lat) || math.IsNaN(fx.Lon) || math.IsInf(fx.Lat, 0) || math.IsInf(fx.Lon, 0) {
					t.Fatalf("non-finite coordinate: %+v", fx)
				}
			}
		}
	})
}
