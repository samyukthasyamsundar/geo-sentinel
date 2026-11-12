package trace

import "io"

// io_endless yields 'x' bytes forever.
type io_endless struct{}

func (io_endless) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'x'
	}
	return len(p), nil
}

func io_prefix(s string, r io.Reader) io.Reader {
	return io.MultiReader(stringsReader(s), r)
}

func stringsReader(s string) io.Reader { return &strReader{s: s} }

type strReader struct {
	s string
	i int
}

func (r *strReader) Read(p []byte) (int, error) {
	if r.i >= len(r.s) {
		return 0, io.EOF
	}
	n := copy(p, r.s[r.i:])
	r.i += n
	return n, nil
}
