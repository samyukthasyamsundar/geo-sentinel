// Package trace reads GPS traces from CSV and GPX files.
//
// Trace files are untrusted input. Parsing is strict (every problem is reported with its
// line number), bounded (a maximum point count and file size), and never panics.
package trace

import (
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/samyukthasyamsundar/geo-sentinel/internal/geo"
)

const (
	// MaxPoints bounds memory use. 500k points is several days of 1 Hz tracking.
	MaxPoints = 500_000
	// MaxBytes bounds how much of an input is read.
	MaxBytes = 64 << 20
)

// Fix is one reported position.
type Fix struct {
	geo.Point
	Time time.Time
	// AccuracyM is the reported horizontal accuracy radius in metres; NaN if absent.
	AccuracyM float64
	// Line is the 1-based source line, for error reporting.
	Line int
}

// HasAccuracy reports whether the fix carried an accuracy value.
func (f Fix) HasAccuracy() bool { return !math.IsNaN(f.AccuracyM) }

// Trace is an ordered series of fixes.
type Trace struct {
	Fixes []Fix
}

// ParseError describes one problem in an input file.
type ParseError struct {
	Line int
	Msg  string
}

func (e ParseError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("line %d: %s", e.Line, e.Msg)
	}
	return e.Msg
}

// ErrTooLarge is returned when an input exceeds MaxBytes or MaxPoints.
var ErrTooLarge = errors.New("trace too large")

// limitedReader reads at most MaxBytes and reports ErrTooLarge instead of silently truncating.
func limitedReader(r io.Reader) io.Reader {
	return &capReader{r: r, left: MaxBytes}
}

type capReader struct {
	r    io.Reader
	left int64
}

func (c *capReader) Read(p []byte) (int, error) {
	if c.left <= 0 {
		// Distinguish "exactly MaxBytes" from "more than MaxBytes" by probing one byte.
		var b [1]byte
		n, err := c.r.Read(b[:])
		if n > 0 {
			return 0, ErrTooLarge
		}
		return 0, err
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.r.Read(p)
	c.left -= int64(n)
	return n, err
}

// Validate checks a parsed trace: coordinates in range, times set, and returns the problems found.
func (t *Trace) Validate() []ParseError {
	var errs []ParseError
	for _, f := range t.Fixes {
		switch {
		case !f.Point.Valid():
			errs = append(errs, ParseError{f.Line, fmt.Sprintf("coordinates out of range (%v, %v)", f.Lat, f.Lon)})
		case f.Time.IsZero():
			errs = append(errs, ParseError{f.Line, "missing timestamp"})
		case f.HasAccuracy() && (f.AccuracyM < 0 || math.IsInf(f.AccuracyM, 0)):
			errs = append(errs, ParseError{f.Line, fmt.Sprintf("invalid accuracy %v", f.AccuracyM)})
		}
	}
	return errs
}

// SortedByTime returns a copy ordered by timestamp (stable, so equal times keep file order) and
// whether the input was already in order. Detectors analyse time order, but ordering problems in
// the source are themselves worth reporting, so the caller keeps the original too.
func (t *Trace) SortedByTime() (*Trace, bool) {
	out := &Trace{Fixes: append([]Fix(nil), t.Fixes...)}
	inOrder := sort.SliceIsSorted(out.Fixes, func(i, j int) bool { return out.Fixes[i].Time.Before(out.Fixes[j].Time) })
	sort.SliceStable(out.Fixes, func(i, j int) bool { return out.Fixes[i].Time.Before(out.Fixes[j].Time) })
	return out, inOrder
}

func trimBOM(s string) string { return strings.TrimPrefix(s, "\xef\xbb\xbf") } // UTF-8 byte order mark
