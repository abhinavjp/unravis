package applog

import "io"

// Tee returns a writer that offers every write to each of writers in order and
// never stops at a failing one. io.MultiWriter stops at the first error, so in
// a windowless build (where stderr is invalid) nothing would reach the log
// file behind it.
//
// Nil interface values are dropped when Tee is called; with none left it
// returns io.Discard. A typed-nil *os.File (os.Stderr in a windowless build) is
// not detected: its Write returns os.ErrInvalid, which is just a failing writer.
//
// Write reports len(p), nil when at least one writer took all of p without
// error. When every writer failed it reports 0 and the first error seen. A
// short write counts as a failure of that writer.
func Tee(writers ...io.Writer) io.Writer {
	live := make([]io.Writer, 0, len(writers))
	for _, w := range writers {
		if w != nil {
			live = append(live, w)
		}
	}
	if len(live) == 0 {
		return io.Discard
	}
	return teeWriter{writers: live}
}

type teeWriter struct {
	writers []io.Writer
}

func (t teeWriter) Write(p []byte) (int, error) {
	var firstErr error
	ok := false
	for _, w := range t.writers {
		n, err := w.Write(p)
		if err == nil && n < len(p) {
			err = io.ErrShortWrite
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		ok = true
	}
	if ok {
		return len(p), nil
	}
	return 0, firstErr
}
