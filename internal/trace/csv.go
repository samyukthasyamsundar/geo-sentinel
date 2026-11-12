package trace

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/samyukthasyamsundar/geo-sentinel/internal/geo"
)

// ParseCSV reads a CSV trace. The first row must be a header naming the columns; order is free.
//
//	time      required. RFC 3339 (2026-11-12T10:00:00Z) or Unix seconds (integer or fractional)
//	lat, lon  required. Decimal degrees
//	accuracy  optional. Horizontal accuracy radius in metres
//
// Unknown columns are ignored. Parsing stops at the first malformed row and reports its line.
func ParseCSV(r io.Reader) (*Trace, error) {
	cr := csv.NewReader(limitedReader(r))
	cr.FieldsPerRecord = -1 // we check column counts ourselves, to report friendlier errors
	cr.ReuseRecord = true

	header, err := cr.Read()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, ParseError{1, "empty file: expected a header row"}
		}
		return nil, wrapCSV(err)
	}
	cols := map[string]int{}
	for i, h := range header {
		name := strings.ToLower(strings.TrimSpace(trimBOM(h)))
		if _, dup := cols[name]; dup && name != "" {
			return nil, ParseError{1, fmt.Sprintf("duplicate column %q", name)}
		}
		cols[name] = i
	}
	for _, need := range []string{"time", "lat", "lon"} {
		if _, ok := cols[need]; !ok {
			return nil, ParseError{1, fmt.Sprintf("missing required column %q (have: %s)", need, strings.Join(header, ","))}
		}
	}
	ti, la, lo := cols["time"], cols["lat"], cols["lon"]
	ai, hasAcc := cols["accuracy"]

	t := &Trace{}
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, wrapCSV(err)
		}
		// FieldPos is only valid once Read has produced a record. (Calling it before checking err
		// panicked on malformed quoting; found by fuzzing.)
		line, _ := cr.FieldPos(0)
		if len(t.Fixes) >= MaxPoints {
			return nil, fmt.Errorf("%w: more than %d points", ErrTooLarge, MaxPoints)
		}
		maxIdx := max(ti, max(la, lo))
		if hasAcc {
			maxIdx = max(maxIdx, ai)
		}
		if len(rec) <= maxIdx {
			return nil, ParseError{line, fmt.Sprintf("row has %d columns, need at least %d", len(rec), maxIdx+1)}
		}
		tm, err := parseTime(rec[ti])
		if err != nil {
			return nil, ParseError{line, "bad time: " + err.Error()}
		}
		lat, err := parseFloat(rec[la])
		if err != nil {
			return nil, ParseError{line, "bad lat: " + err.Error()}
		}
		lon, err := parseFloat(rec[lo])
		if err != nil {
			return nil, ParseError{line, "bad lon: " + err.Error()}
		}
		acc := math.NaN()
		if hasAcc && strings.TrimSpace(rec[ai]) != "" {
			if acc, err = parseFloat(rec[ai]); err != nil {
				return nil, ParseError{line, "bad accuracy: " + err.Error()}
			}
		}
		t.Fixes = append(t.Fixes, Fix{Point: geo.Point{Lat: lat, Lon: lon}, Time: tm, AccuracyM: acc, Line: line})
	}
	if len(t.Fixes) == 0 {
		return nil, ParseError{0, "no data rows"}
	}
	return t, nil
}

func wrapCSV(err error) error {
	if errors.Is(err, ErrTooLarge) {
		return fmt.Errorf("%w: input exceeds %d bytes", ErrTooLarge, MaxBytes)
	}
	var pe *csv.ParseError
	if errors.As(err, &pe) {
		return ParseError{pe.Line, pe.Err.Error()}
	}
	return err
}

// parseFloat accepts only finite decimal numbers (rejects NaN, Inf, hex floats, empty strings).
func parseFloat(s string) (float64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty value")
	}
	if strings.ContainsAny(s, "xXpP") {
		return 0, fmt.Errorf("%q is not a plain decimal", s)
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", s)
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("%q is not finite", s)
	}
	return v, nil
}

// parseTime accepts RFC 3339 or Unix seconds. Unix values are bounded to 1970..2100 so a stray
// large number cannot overflow time arithmetic later.
func parseTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, errors.New("empty value")
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC(), nil
	}
	v, err := parseFloat(s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is neither RFC 3339 nor Unix seconds", s)
	}
	const maxUnix = 4102444800 // 2100-01-01
	if v < 0 || v > maxUnix {
		return time.Time{}, fmt.Errorf("Unix time %v is outside 1970-2100", v)
	}
	sec := math.Floor(v)
	return time.Unix(int64(sec), int64((v-sec)*1e9)).UTC(), nil
}
