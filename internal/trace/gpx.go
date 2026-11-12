package trace

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
)

// ParseGPX reads trackpoints (<trkpt>) from a GPX document, across all tracks and segments.
// Waypoints and routes are ignored: they are planned places, not recorded positions.
//
// Hostile XML is handled by construction: the decoder is run in Strict mode, never resolves
// external entities, has no custom entity table (so entity-expansion "bombs" have nothing to
// expand into), and input size and point count are capped.
func ParseGPX(r io.Reader) (*Trace, error) {
	dec := xml.NewDecoder(limitedReader(r))
	dec.Strict = true
	dec.Entity = nil // only the five predefined XML entities are recognised
	// A CharsetReader that refuses everything prevents the decoder from trying to open arbitrary
	// encodings; GPX is UTF-8 by specification.
	dec.CharsetReader = func(charset string, in io.Reader) (io.Reader, error) {
		if strings.EqualFold(charset, "utf-8") || strings.EqualFold(charset, "utf8") {
			return in, nil
		}
		return nil, fmt.Errorf("unsupported encoding %q (GPX must be UTF-8)", charset)
	}

	t := &Trace{}
	var (
		cur      *Fix
		inEle    string // name of the child element currently being read
		sawRoot  bool
		depth    int
		textBuf  strings.Builder
		curAccOK bool
	)
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if errors.Is(err, ErrTooLarge) {
				return nil, fmt.Errorf("%w: input exceeds %d bytes", ErrTooLarge, MaxBytes)
			}
			line, _ := dec.InputPos()
			return nil, ParseError{line, "invalid XML: " + err.Error()}
		}
		switch el := tok.(type) {
		case xml.StartElement:
			depth++
			if depth > 32 {
				return nil, ParseError{0, "XML nested too deeply"}
			}
			name := strings.ToLower(el.Name.Local)
			if depth == 1 {
				sawRoot = true
				if name != "gpx" {
					return nil, ParseError{0, fmt.Sprintf("not a GPX file: root element is <%s>", el.Name.Local)}
				}
			}
			switch {
			case name == "trkpt":
				if len(t.Fixes) >= MaxPoints {
					return nil, fmt.Errorf("%w: more than %d points", ErrTooLarge, MaxPoints)
				}
				line, _ := dec.InputPos()
				f := Fix{AccuracyM: math.NaN(), Line: line}
				var latOK, lonOK bool
				for _, a := range el.Attr {
					switch strings.ToLower(a.Name.Local) {
					case "lat":
						v, e := parseFloat(a.Value)
						if e != nil {
							return nil, ParseError{line, "bad lat: " + e.Error()}
						}
						f.Lat, latOK = v, true
					case "lon":
						v, e := parseFloat(a.Value)
						if e != nil {
							return nil, ParseError{line, "bad lon: " + e.Error()}
						}
						f.Lon, lonOK = v, true
					}
				}
				if !latOK || !lonOK {
					return nil, ParseError{line, "<trkpt> needs lat and lon attributes"}
				}
				cur = &f
				curAccOK = false
			case cur != nil && (name == "time" || name == "hdop" || name == "accuracy"):
				inEle = name
				textBuf.Reset()
			}
		case xml.CharData:
			if cur != nil && inEle != "" {
				if textBuf.Len()+len(el) > 256 {
					return nil, ParseError{0, "unreasonably long text inside a trackpoint element"}
				}
				textBuf.Write(el)
			}
		case xml.EndElement:
			name := strings.ToLower(el.Name.Local)
			if cur != nil && inEle != "" && name == inEle {
				line, _ := dec.InputPos()
				txt := strings.TrimSpace(textBuf.String())
				switch inEle {
				case "time":
					tm, e := parseTime(txt)
					if e != nil {
						return nil, ParseError{line, "bad time: " + e.Error()}
					}
					cur.Time = tm
				case "accuracy": // explicit metres; takes precedence over hdop
					v, e := parseFloat(txt)
					if e != nil {
						return nil, ParseError{line, "bad accuracy: " + e.Error()}
					}
					cur.AccuracyM, curAccOK = v, true
				case "hdop": // dimensionless; roughly 5 m of error per unit is a common rule of thumb
					if !curAccOK {
						v, e := parseFloat(txt)
						if e != nil {
							return nil, ParseError{line, "bad hdop: " + e.Error()}
						}
						cur.AccuracyM = v * 5
					}
				}
				inEle = ""
			}
			if name == "trkpt" && cur != nil {
				t.Fixes = append(t.Fixes, *cur)
				cur = nil
			}
			depth--
		}
	}
	if !sawRoot {
		return nil, ParseError{0, "empty document"}
	}
	if len(t.Fixes) == 0 {
		return nil, ParseError{0, "no <trkpt> trackpoints found"}
	}
	return t, nil
}
