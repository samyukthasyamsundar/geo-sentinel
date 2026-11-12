package trace

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

const goodCSV = "time,lat,lon,accuracy\n2026-11-12T10:00:00Z,47.6000,-122.3000,5\n2026-11-12T10:00:10Z,47.6001,-122.3001,6.5\n"

func TestCSVBasic(t *testing.T) {
	tr, err := ParseCSV(strings.NewReader(goodCSV))
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Fixes) != 2 {
		t.Fatalf("got %d fixes", len(tr.Fixes))
	}
	f := tr.Fixes[1]
	if f.Lat != 47.6001 || f.Lon != -122.3001 || f.AccuracyM != 6.5 || f.Line != 3 {
		t.Errorf("bad fix: %+v", f)
	}
	if !f.Time.Equal(time.Date(2026, 11, 12, 10, 0, 10, 0, time.UTC)) {
		t.Errorf("bad time %v", f.Time)
	}
}

func TestCSVColumnOrderCaseAndBOM(t *testing.T) {
	in := "\xef\xbb\xbfLON, Lat ,TIME,extra\n-122.3,47.6,1700000000,zzz\n"
	tr, err := ParseCSV(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if tr.Fixes[0].Lat != 47.6 || tr.Fixes[0].Time.Unix() != 1700000000 || tr.Fixes[0].HasAccuracy() {
		t.Errorf("bad fix: %+v", tr.Fixes[0])
	}
}

func TestCSVFractionalUnixTime(t *testing.T) {
	tr, err := ParseCSV(strings.NewReader("time,lat,lon\n1700000000.5,1,2\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := tr.Fixes[0].Time.Nanosecond(); math.Abs(float64(got)-5e8) > 1e6 {
		t.Errorf("nanoseconds = %d, want ~500000000", got)
	}
}

func TestCSVRejectsBadInputWithLineNumbers(t *testing.T) {
	cases := []struct {
		name, in, wantMsg string
		wantLine          int
	}{
		{"empty", "", "empty file", 1},
		{"header only", "time,lat,lon\n", "no data rows", 0},
		{"missing column", "time,lat\n1,2\n", `missing required column "lon"`, 1},
		{"duplicate column", "time,lat,lat,lon\n", "duplicate column", 1},
		{"short row", "time,lat,lon\n1700000000,1\n", "row has 2 columns", 2},
		{"bad lat", "time,lat,lon\n1700000000,north,2\n", "bad lat", 2},
		{"NaN lat", "time,lat,lon\n1700000000,NaN,2\n", "not finite", 2},
		{"Inf lon", "time,lat,lon\n1700000000,1,Inf\n", "not finite", 2},
		{"hex float", "time,lat,lon\n1700000000,0x1p3,2\n", "plain decimal", 2},
		{"bad time", "time,lat,lon\nyesterday,1,2\n", "neither RFC 3339 nor Unix", 2},
		{"negative unix", "time,lat,lon\n-5,1,2\n", "outside 1970-2100", 2},
		{"huge unix", "time,lat,lon\n9e18,1,2\n", "outside 1970-2100", 2},
		{"bad accuracy", "time,lat,lon,accuracy\n1700000000,1,2,wide\n", "bad accuracy", 2},
		{"second bad row", goodCSV + "oops,1,2,3\n", "bad time", 4},
	}
	for _, c := range cases {
		_, err := ParseCSV(strings.NewReader(c.in))
		if err == nil {
			t.Errorf("%s: expected an error", c.name)
			continue
		}
		var pe ParseError
		if !errors.As(err, &pe) {
			t.Errorf("%s: error %v is not a ParseError", c.name, err)
			continue
		}
		if !strings.Contains(pe.Msg, c.wantMsg) {
			t.Errorf("%s: message %q does not contain %q", c.name, pe.Msg, c.wantMsg)
		}
		if pe.Line != c.wantLine {
			t.Errorf("%s: line = %d, want %d", c.name, pe.Line, c.wantLine)
		}
	}
}

func TestCSVPointLimit(t *testing.T) {
	var b strings.Builder
	b.WriteString("time,lat,lon\n")
	for i := 0; i <= MaxPoints; i++ {
		b.WriteString("1700000000,1,2\n")
	}
	_, err := ParseCSV(strings.NewReader(b.String()))
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}

func TestSizeLimit(t *testing.T) {
	// A reader that never ends must be cut off at MaxBytes, not read forever.
	r := io_endless{}
	_, err := ParseCSV(io_prefix("time,lat,lon\n", r))
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge for an endless input, got %v", err)
	}
}

func TestValidate(t *testing.T) {
	tr, _ := ParseCSV(strings.NewReader("time,lat,lon\n1700000000,91,0\n1700000001,0,181\n1700000002,10,10\n"))
	errs := tr.Validate()
	if len(errs) != 2 || errs[0].Line != 2 || errs[1].Line != 3 {
		t.Errorf("validate = %v", errs)
	}
}

func TestSortedByTime(t *testing.T) {
	tr, _ := ParseCSV(strings.NewReader("time,lat,lon\n1700000030,3,3\n1700000010,1,1\n1700000020,2,2\n"))
	sorted, inOrder := tr.SortedByTime()
	if inOrder {
		t.Error("input was out of order")
	}
	if sorted.Fixes[0].Lat != 1 || sorted.Fixes[2].Lat != 3 {
		t.Errorf("not sorted: %+v", sorted.Fixes)
	}
	if tr.Fixes[0].Lat != 3 {
		t.Error("original must be left untouched")
	}
}

// ---- GPX ----

const goodGPX = `<?xml version="1.0" encoding="UTF-8"?>
<gpx version="1.1" creator="t" xmlns="http://www.topografix.com/GPX/1/1">
 <wpt lat="1" lon="1"><time>2026-11-12T09:00:00Z</time></wpt>
 <trk><trkseg>
  <trkpt lat="47.6000" lon="-122.3000"><ele>10</ele><time>2026-11-12T10:00:00Z</time><hdop>1.2</hdop></trkpt>
  <trkpt lat="47.6001" lon="-122.3001"><time>2026-11-12T10:00:10Z</time><accuracy>3.5</accuracy></trkpt>
 </trkseg></trk>
</gpx>`

func TestGPXBasic(t *testing.T) {
	tr, err := ParseGPX(strings.NewReader(goodGPX))
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Fixes) != 2 {
		t.Fatalf("got %d fixes (waypoints must be ignored)", len(tr.Fixes))
	}
	if tr.Fixes[0].AccuracyM != 6 { // hdop 1.2 * 5
		t.Errorf("hdop accuracy = %v", tr.Fixes[0].AccuracyM)
	}
	if tr.Fixes[1].AccuracyM != 3.5 {
		t.Errorf("explicit accuracy = %v", tr.Fixes[1].AccuracyM)
	}
}

func TestGPXRejectsBadDocuments(t *testing.T) {
	cases := map[string]string{
		"not xml":        "hello",
		"wrong root":     `<kml><trkpt lat="1" lon="1"/></kml>`,
		"no trackpoints": `<gpx><wpt lat="1" lon="1"/></gpx>`,
		"missing lon":    `<gpx><trk><trkseg><trkpt lat="1"><time>2026-11-12T10:00:00Z</time></trkpt></trkseg></trk></gpx>`,
		"bad lat":        `<gpx><trkpt lat="abc" lon="1"/></gpx>`,
		"bad time":       `<gpx><trkpt lat="1" lon="1"><time>never</time></trkpt></gpx>`,
		"truncated":      `<gpx><trk><trkseg><trkpt lat="1" lon="1"><time>2026-11-12T10:00:00Z</time>`,
		"mismatched":     `<gpx><trkpt lat="1" lon="1"></gpx>`,
		"latin1":         `<?xml version="1.0" encoding="ISO-8859-1"?><gpx><trkpt lat="1" lon="1"/></gpx>`,
	}
	for name, in := range cases {
		if _, err := ParseGPX(strings.NewReader(in)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestGPXEntityBombIsHarmless(t *testing.T) {
	// "Billion laughs": nested entity definitions that expand exponentially in a naive parser.
	bomb := `<?xml version="1.0"?>
<!DOCTYPE gpx [
 <!ENTITY a "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa">
 <!ENTITY b "&a;&a;&a;&a;&a;&a;&a;&a;&a;&a;">
 <!ENTITY c "&b;&b;&b;&b;&b;&b;&b;&b;&b;&b;">
 <!ENTITY d "&c;&c;&c;&c;&c;&c;&c;&c;&c;&c;">
]>
<gpx><trk><trkseg><trkpt lat="1" lon="1"><time>&d;</time></trkpt></trkseg></trk></gpx>`
	start := time.Now()
	_, err := ParseGPX(strings.NewReader(bomb))
	if err == nil {
		t.Fatal("entity reference must not resolve to valid content")
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("took %v: entity expansion must not blow up", time.Since(start))
	}
}

func TestGPXExternalEntityIsNotFetched(t *testing.T) {
	xxe := `<?xml version="1.0"?>
<!DOCTYPE gpx [ <!ENTITY x SYSTEM "file:///etc/passwd"> ]>
<gpx><trk><trkseg><trkpt lat="1" lon="1"><time>&x;</time></trkpt></trkseg></trk></gpx>`
	_, err := ParseGPX(strings.NewReader(xxe))
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "root:") {
		t.Fatalf("file contents leaked into the error: %v", err)
	}
}

// The next two tests use input that is otherwise perfectly valid, so that the limit being tested is
// the ONLY thing that can reject it. (An earlier version used inputs that failed for other reasons,
// which let the limits be deleted without any test noticing; found by mutation testing.)

func TestGPXDeepNestingIsRefused(t *testing.T) {
	// Well-formed XML, valid GPX, just buried 100 levels deep inside harmless wrapper elements.
	deep := `<gpx>` + strings.Repeat("<x>", 100) +
		`<trkpt lat="1" lon="1"><time>2026-11-12T10:00:00Z</time></trkpt>` + strings.Repeat("</x>", 100) + `</gpx>`
	_, err := ParseGPX(strings.NewReader(deep))
	if err == nil || !strings.Contains(err.Error(), "nested too deeply") {
		t.Fatalf("want a nesting error, got %v", err)
	}
	// And the same document with modest nesting must be accepted, proving only depth was the problem.
	ok := `<gpx>` + strings.Repeat("<x>", 10) +
		`<trkpt lat="1" lon="1"><time>2026-11-12T10:00:00Z</time></trkpt>` + strings.Repeat("</x>", 10) + `</gpx>`
	if _, err := ParseGPX(strings.NewReader(ok)); err != nil {
		t.Fatalf("10 levels should be fine: %v", err)
	}
}

func TestGPXLongElementTextIsRefused(t *testing.T) {
	// A <time> whose text is a valid RFC 3339 timestamp padded with spaces to 1000 bytes: it would
	// parse fine if it were not for the length limit.
	padded := "2026-11-12T10:00:00Z" + strings.Repeat(" ", 1000)
	long := `<gpx><trkpt lat="1" lon="1"><time>` + padded + `</time></trkpt></gpx>`
	_, err := ParseGPX(strings.NewReader(long))
	if err == nil || !strings.Contains(err.Error(), "unreasonably long") {
		t.Fatalf("want a length error, got %v", err)
	}
	short := `<gpx><trkpt lat="1" lon="1"><time>2026-11-12T10:00:00Z   </time></trkpt></gpx>`
	if _, err := ParseGPX(strings.NewReader(short)); err != nil {
		t.Fatalf("short padded text should be fine: %v", err)
	}
}

// Regression: found by fuzzing. A stray quote produced a read error, and the code then asked the CSV
// reader for the position of a field in a record that did not exist, which panicked.
func TestCSVMalformedQuotingDoesNotPanic(t *testing.T) {
	inputs := []string{
		"time,lAt,lon \n0000\"00000000000000000000000",
		"time,lat,lon\n\"unterminated,1,2\n",
		"time,lat,lon\n1700000000,\"1,2\n",
		"time,lat,lon\n1700000000,1\"2,3\n",
		"\"time\n",
	}
	for _, in := range inputs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("input %q panicked: %v", in, r)
				}
			}()
			if _, err := ParseCSV(strings.NewReader(in)); err == nil {
				t.Errorf("input %q should be rejected", in)
			}
		}()
	}
}
