package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func cli(t *testing.T, args ...string) (code int, out, errOut string) {
	t.Helper()
	var o, e bytes.Buffer
	code = run(args, &o, &e)
	return code, o.String(), e.String()
}

const sample = "../../testdata/"

func TestExitCodesMatchVerdicts(t *testing.T) {
	cases := []struct {
		file string
		code int
		word string
	}{
		{"honest_drive.csv", exitClean, "CLEAN"},
		{"honest_walk.csv", exitClean, "CLEAN"},
		{"fake_teleport.csv", exitSpoofed, "LIKELY-SPOOFED"},
		{"fake_frozen.csv", exitSuspicious, "SUSPICIOUS"},
		{"fake_scripted.csv", exitSuspicious, "SUSPICIOUS"},
		{"fake_careful.csv", exitClean, "CLEAN"}, // the documented blind spot, pinned on purpose
	}
	for _, c := range cases {
		code, out, errOut := cli(t, sample+c.file)
		if code != c.code || !strings.Contains(out, "verdict: "+c.word) {
			t.Errorf("%s: exit %d (want %d), out %q, err %q", c.file, code, c.code, out, errOut)
		}
	}
}

func TestJSONOutputShape(t *testing.T) {
	code, out, _ := cli(t, "--json", sample+"fake_teleport.csv")
	if code != exitSpoofed {
		t.Fatalf("exit %d", code)
	}
	var v struct {
		File     string `json:"file"`
		Fixes    int    `json:"fixes"`
		Verdict  string `json:"verdict"`
		Findings []struct {
			Check, Confidence, Reason string
			Line                      int
		} `json:"findings"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, out)
	}
	if v.Verdict != "likely-spoofed" || v.Fixes != 300 || len(v.Findings) == 0 || v.Findings[0].Reason == "" {
		t.Errorf("unexpected JSON: %+v", v)
	}
}

func TestJSONCleanHasEmptyArrayNotNull(t *testing.T) {
	_, out, _ := cli(t, "--json", sample+"honest_walk.csv")
	if !strings.Contains(out, `"findings": []`) {
		t.Errorf("clean result must serialise findings as [] for consumers: %s", out)
	}
}

func TestBadInputs(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := []struct {
		name string
		args []string
		code int
		msg  string
	}{
		{"no args", nil, exitUsage, "usage"},
		{"two files", []string{"a.csv", "b.csv"}, exitUsage, "usage"},
		{"missing file", []string{filepath.Join(dir, "nope.csv")}, exitBadInput, "no such file"},
		{"unknown extension", []string{write("t.txt", "x")}, exitUsage, "cannot tell the format"},
		{"bad csv", []string{write("bad.csv", "time,lat,lon\n1700000000,north,2\n")}, exitBadInput, "line 2"},
		{"bad gpx", []string{write("bad.gpx", "<kml/>")}, exitBadInput, "not a GPX file"},
		{"out of range coordinate", []string{write("range.csv", "time,lat,lon\n1700000000,95,2\n")}, exitBadInput, "out of range"},
		{"negative speed", []string{"--max-speed-kmh", "-1", sample + "honest_walk.csv"}, exitUsage, "max-speed-kmh"},
		{"NaN speed", []string{"--max-speed-kmh", "NaN", sample + "honest_walk.csv"}, exitUsage, ""},
		{"unknown flag", []string{"--frobnicate", "x.csv"}, exitUsage, ""},
	}
	for _, c := range cases {
		code, _, errOut := cli(t, c.args...)
		if code != c.code {
			t.Errorf("%s: exit %d, want %d (stderr %q)", c.name, code, c.code, errOut)
		}
		if c.msg != "" && !strings.Contains(strings.ToLower(errOut), strings.ToLower(c.msg)) {
			t.Errorf("%s: stderr %q should mention %q", c.name, errOut, c.msg)
		}
	}
}

func TestFormatOverrideAndGPX(t *testing.T) {
	gpx := `<?xml version="1.0"?><gpx>` + strings.Repeat(
		`<trkpt lat="47.6" lon="-122.3"><time>2026-11-12T10:00:00Z</time></trkpt>`, 1) + `</gpx>`
	p := filepath.Join(t.TempDir(), "track.dat")
	if err := os.WriteFile(p, []byte(gpx), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := cli(t, "--format", "gpx", p); code != exitClean {
		t.Errorf("--format gpx: exit %d, stderr %q", code, errOut)
	}
}

func TestSpeedFlagRaisesTheLimit(t *testing.T) {
	// A 200 km/h train is "likely spoofed" at the default limit but fine when the limit is raised.
	dir := t.TempDir()
	var b strings.Builder
	b.WriteString("time,lat,lon\n")
	for i := 0; i < 60; i++ {
		b.WriteString(strings.Replace("2026-11-12T10:MM:SSZ,LAT,-122.3\n", "MM:SS", twoDigits(i/60)+":"+twoDigits(i%60), 1))
	}
	_ = b
	// Build with real numbers: 55 m/s northward = 0.000495 deg/s lat.
	b.Reset()
	b.WriteString("time,lat,lon\n")
	for i := 0; i < 60; i++ {
		lat := 47.0 + float64(i)*0.000495*30 // 30 s apart => 1650 m per step => 55 m/s
		b.WriteString("2026-11-12T10:" + twoDigits(i/2) + ":" + twoDigits((i%2)*30) + "Z," + ftoa(lat) + ",-122.3\n")
	}
	p := filepath.Join(dir, "train.csv")
	if err := os.WriteFile(p, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := cli(t, "--max-speed-kmh", "300", p); code == exitSpoofed {
		t.Error("55 m/s (198 km/h) is under a 300 km/h limit and must not be called spoofed")
	}
	if code, _, _ := cli(t, "--max-speed-kmh", "100", p); code != exitSpoofed {
		t.Errorf("55 m/s (198 km/h) exceeds a 100 km/h limit: exit %d, want %d", code, exitSpoofed)
	}
}

func twoDigits(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

func ftoa(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}

func TestMaxFindingsTruncatesTextOutput(t *testing.T) {
	_, out, _ := cli(t, "--max-findings", "1", sample+"fake_teleport.csv")
	if !strings.Contains(out, "and 1 more") {
		t.Errorf("expected a truncation notice: %s", out)
	}
}
