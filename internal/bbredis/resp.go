package bbredis

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"
)

func sortStrings(s []string) { sort.Strings(s) }

// Reply limits. A reply bigger than these is refused, not truncated: the caller is told, and the connection is closed.
const (
	maxBulk    = 4 << 20 // one bulk string (INFO with commandstats, CLIENT LIST)
	maxArray   = 10000
	maxDepth   = 4
	maxLineLen = 64 << 10
)

// Errors of the client. None carries text from the server except RedisError, which holds the error word only.
var (
	ErrTooLarge = errors.New("bbredis: the reply is too large")
	ErrProtocol = errors.New("bbredis: the reply is not valid")
	ErrNotLocal = errors.New("bbredis: only this PC can be read")
)

// RedisError is an error reply from the server ("NOAUTH", "WRONGPASS", "ERR", "NOPERM"). Only the first word is kept:
// the rest of the line can hold names or values.
type RedisError struct{ Word string }

func (e *RedisError) Error() string { return "bbredis: the server answered " + e.Word }

// IsNoAuth reports whether err means the server wants a password (or refused the one given).
func IsNoAuth(err error) bool {
	var re *RedisError
	return errors.As(err, &re) && (re.Word == "NOAUTH" || re.Word == "WRONGPASS" || re.Word == "NOPERM")
}

// Reply is one decoded answer.
type Reply struct {
	Kind byte // '+' simple string, ':' integer, '$' bulk string, '*' array
	Str  string
	Int  int64
	Nil  bool
	Arr  []Reply
}

// Client is one connection to a Redis on this PC. It is not safe for concurrent use.
type Client struct {
	conn    net.Conn
	r       *bufio.Reader
	timeout time.Duration
}

// DefaultPort is the standard Redis port.
const DefaultPort = 6379

// Dial connects to 127.0.0.1:port. The address is fixed in code: there is no host argument, so settings cannot point
// the recorder (which runs as SYSTEM) at another machine.
func Dial(port int, timeout time.Duration) (*Client, error) {
	if port < 1 || port > 65535 {
		return nil, ErrNotLocal
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), timeout)
	if err != nil {
		return nil, err
	}
	return NewClient(conn, timeout), nil
}

// NewClient wraps an open connection (used by tests).
func NewClient(conn net.Conn, timeout time.Duration) *Client {
	return &Client{conn: conn, r: bufio.NewReaderSize(conn, 32<<10), timeout: timeout}
}

// Close closes the connection.
func (c *Client) Close() error { return c.conn.Close() }

// Auth sends the password. It is the only place a password leaves this package. The password is never part of an
// error or a log line.
func (c *Client) Auth(password string) error {
	_, err := c.roundTrip([]string{"AUTH", password})
	return err
}

// Do sends one allowlisted command and returns its decoded answer.
func (c *Client) Do(args ...string) (Reply, error) {
	if err := Allowed(args); err != nil {
		return Reply{}, err
	}
	return c.roundTrip(args)
}

func (c *Client) roundTrip(args []string) (Reply, error) {
	_ = c.conn.SetDeadline(time.Now().Add(c.timeout))
	var b strings.Builder
	fmt.Fprintf(&b, "*%d\r\n", len(args))
	for _, a := range args {
		fmt.Fprintf(&b, "$%d\r\n%s\r\n", len(a), a)
	}
	if _, err := io.WriteString(c.conn, b.String()); err != nil {
		return Reply{}, err
	}
	return c.read(0)
}

func (c *Client) line() (string, error) {
	var out []byte
	for {
		part, isPrefix, err := c.r.ReadLine()
		if err != nil {
			return "", err
		}
		out = append(out, part...)
		if len(out) > maxLineLen {
			return "", ErrTooLarge
		}
		if !isPrefix {
			return string(out), nil
		}
	}
}

func (c *Client) read(depth int) (Reply, error) {
	if depth > maxDepth {
		return Reply{}, ErrProtocol
	}
	l, err := c.line()
	if err != nil {
		return Reply{}, err
	}
	if l == "" {
		return Reply{}, ErrProtocol
	}
	kind, rest := l[0], l[1:]
	switch kind {
	case '+':
		return Reply{Kind: '+', Str: rest}, nil
	case '-':
		word := rest
		if i := strings.IndexByte(word, ' '); i >= 0 {
			word = word[:i]
		}
		if len(word) > 32 {
			word = word[:32]
		}
		return Reply{}, &RedisError{Word: strings.ToUpper(word)}
	case ':':
		n, err := strconv.ParseInt(rest, 10, 64)
		if err != nil {
			return Reply{}, ErrProtocol
		}
		return Reply{Kind: ':', Int: n}, nil
	case '$':
		n, err := strconv.Atoi(rest)
		if err != nil || n < -1 {
			return Reply{}, ErrProtocol
		}
		if n == -1 {
			return Reply{Kind: '$', Nil: true}, nil
		}
		if n > maxBulk {
			_ = c.conn.Close() // the rest of the stream is not read: the connection is done
			return Reply{}, ErrTooLarge
		}
		buf := make([]byte, n+2)
		if _, err := io.ReadFull(c.r, buf); err != nil {
			return Reply{}, err
		}
		if buf[n] != '\r' || buf[n+1] != '\n' {
			return Reply{}, ErrProtocol
		}
		return Reply{Kind: '$', Str: string(buf[:n])}, nil
	case '*':
		n, err := strconv.Atoi(rest)
		if err != nil || n < -1 {
			return Reply{}, ErrProtocol
		}
		if n == -1 {
			return Reply{Kind: '*', Nil: true}, nil
		}
		if n > maxArray {
			_ = c.conn.Close()
			return Reply{}, ErrTooLarge
		}
		arr := make([]Reply, 0, n)
		for i := 0; i < n; i++ {
			el, err := c.read(depth + 1)
			if err != nil {
				return Reply{}, err
			}
			arr = append(arr, el)
		}
		return Reply{Kind: '*', Arr: arr}, nil
	}
	return Reply{}, ErrProtocol
}
