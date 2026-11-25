package wire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFrame(&buf, []byte(`{"t":"hello"}`)); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFrame(&buf, 0)
	if err != nil || string(got) != `{"t":"hello"}` {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := ReadFrame(&buf, 0); err != io.EOF {
		t.Errorf("clean end: %v", err)
	}
}

func TestFrameLimitsAreExact(t *testing.T) {
	var buf bytes.Buffer
	at := bytes.Repeat([]byte("a"), MaxFrame)
	if err := WriteFrame(&buf, at); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadFrame(&buf, 0); err != nil || len(got) != MaxFrame {
		t.Errorf("at the limit: %d %v", len(got), err)
	}
	if err := WriteFrame(&buf, append(at, 'a')); !errors.Is(err, ErrFrameTooLarge) {
		t.Errorf("write over: %v", err)
	}
	if err := WriteFrame(&buf, nil); !errors.Is(err, ErrEmptyFrame) {
		t.Errorf("write empty: %v", err)
	}
}

// A hostile length is refused before a byte of it is read or allocated.
func TestHostileLengthIsRefusedBeforeReading(t *testing.T) {
	for _, n := range []uint32{MaxFrame + 1, 1 << 30, 0xFFFFFFFF} {
		head := make([]byte, 4)
		binary.LittleEndian.PutUint32(head, n)
		r := &countingReader{r: bytes.NewReader(head)}
		if _, err := ReadFrame(r, 0); !errors.Is(err, ErrFrameTooLarge) {
			t.Errorf("length %d: %v", n, err)
		}
		if r.n != 4 {
			t.Errorf("length %d: read %d bytes", n, r.n)
		}
	}
	zero := make([]byte, 4)
	if _, err := ReadFrame(bytes.NewReader(zero), 0); !errors.Is(err, ErrEmptyFrame) {
		t.Errorf("zero length: %v", err)
	}
	if _, err := ReadFrame(bytes.NewReader([]byte{5, 0, 0, 0, 'a'}), 4); !errors.Is(err, ErrFrameTooLarge) {
		t.Errorf("caller's smaller maximum: %v", err)
	}
}

type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) { n, err := c.r.Read(p); c.n += n; return n, err }

func TestTruncatedFrames(t *testing.T) {
	if _, err := ReadFrame(bytes.NewReader([]byte{1, 0}), 0); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("cut in the length: %v", err)
	}
	if _, err := ReadFrame(bytes.NewReader([]byte{4, 0, 0, 0, 'a'}), 0); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("cut in the body: %v", err)
	}
}

func TestDecodeIsStrict(t *testing.T) {
	good := `{"t":"call","id":3,"tool":"audit_get_guide","args":{"a":1}}`
	var c Call
	if err := Decode([]byte(good), &c); err != nil || c.ID != 3 || string(c.Args) != `{"a":1}` {
		t.Fatalf("%+v %v", c, err)
	}
	bad := map[string]string{
		"unknown field":    `{"t":"call","id":3,"tool":"x","args":{},"zzz":1}`,
		"repeated":         `{"t":"call","id":3,"id":4,"tool":"x","args":{}}`,
		"repeated by case": `{"t":"call","id":3,"ID":4,"tool":"x","args":{}}`,
		"trailing value":   good + ` 1`,
		"trailing object":  good + good,
		"array":            `[` + good + `]`,
		"string":           `"x"`,
		"wrong type":       `{"t":"call","id":"3","tool":"x","args":{}}`,
		"empty":            ``,
		"null":             `null`,
		"truncated":        `{"t":"call"`,
		"invalid utf8":     "{\"t\":\"call\",\"tool\":\"\xff\"}",
	}
	for name, in := range bad {
		var c Call
		if err := Decode([]byte(in), &c); !errors.Is(err, ErrBadFrame) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestPeekType(t *testing.T) {
	if typ, err := PeekType([]byte(`{"t":"done","ok":true}`)); err != nil || typ != TypeDone {
		t.Errorf("%q %v", typ, err)
	}
	for _, in := range []string{`{}`, `{"t":""}`, `{"t":1}`, `{"t":"a","t":"b"}`, `[]`, `{"x":1}`, "{\"t\":\"\xff\"}"} {
		if typ, err := PeekType([]byte(in)); err == nil {
			t.Errorf("%q gave %q", in, typ)
		}
	}
}

func TestErrorsNeverCarryTheFrame(t *testing.T) {
	secret := "SECRET-VALUE"
	err := Decode([]byte(`{"t":"call","id":1,"tool":"`+secret+`","args":{},"zzz":1}`), &Call{})
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Errorf("%v", err)
	}
}

func TestMarshalRefusesOversizeFrames(t *testing.T) {
	if _, err := Marshal(Ret{T: TypeRet, ID: 1, OK: true, Result: []byte(`"` + strings.Repeat("a", MaxFrame) + `"`)}); !errors.Is(err, ErrFrameTooLarge) {
		t.Errorf("%v", err)
	}
}
