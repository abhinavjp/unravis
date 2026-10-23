// Package mcp exposes bounded read-only audit tools over MCP stdio.
package mcp

import (
	"api-audit-explorer/internal/audit"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"
)

const maxBody = 16 << 20
const resultTTL = 10 * time.Minute

var tokenPattern = regexp.MustCompile(`name="audit-session"\s+content="([^"]+)"`)

type Bridge struct {
	base      string
	client    *http.Client
	mu        sync.Mutex
	token     string
	result    *audit.Analysis
	resultID  string
	created   time.Time
	comparing bool
	now       func() time.Time
}

func New(port int) (*Bridge, error) {
	if port < 1 || port > 65535 {
		return nil, errors.New("MCP requires the running dashboard port, between 1 and 65535")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return newBridge("http://127.0.0.1:"+strconv.Itoa(port), &http.Client{Transport: transport, Timeout: audit.OperationTimeout + 5*time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}), nil
}
func newBridge(base string, client *http.Client) *Bridge {
	return &Bridge{base: base, client: client, now: time.Now}
}

func boundedBody(r io.Reader, limit int) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if err != nil || len(data) > limit {
		return nil, errors.New("audit response exceeded its read budget")
	}
	return data, nil
}
func (b *Bridge) session(ctx context.Context) (string, error) {
	b.mu.Lock()
	token := b.token
	b.mu.Unlock()
	if token != "" {
		return token, nil
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, b.base+"/", nil)
	response, err := b.client.Do(req)
	if err != nil {
		return "", errors.New("Unravis is unavailable. Start the dashboard and check the configured port")
	}
	defer response.Body.Close()
	data, err := boundedBody(response.Body, 1<<20)
	if err != nil || response.StatusCode != 200 {
		return "", errors.New("could not authenticate with the local audit dashboard")
	}
	match := tokenPattern.FindSubmatch(data)
	if len(match) != 2 || len(match[1]) > 4096 {
		return "", errors.New("the local dashboard did not supply an audit session")
	}
	token = html.UnescapeString(string(match[1]))
	b.mu.Lock()
	b.token = token
	b.mu.Unlock()
	return token, nil
}
func (b *Bridge) request(ctx context.Context, path string, input, output any) error {
	switch path {
	case "/api/info", "/api/sources", "/api/analyze", "/api/requests", "/api/activity", "/api/slowest", "/api/breakdown":
	default:
		return errors.New("audit operation is not allowed")
	}
	var payload []byte
	var err error
	if input != nil {
		payload, err = json.Marshal(input)
		if err != nil {
			return errors.New("invalid audit request")
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		token, err := b.session(ctx)
		if err != nil {
			return err
		}
		method := http.MethodGet
		if input != nil {
			method = http.MethodPost
		}
		req, _ := http.NewRequestWithContext(ctx, method, b.base+path, bytes.NewReader(payload))
		req.Header.Set("X-Audit-Token", token)
		if input != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		res, err := b.client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return errors.New("audit operation cancelled or its five-minute deadline expired")
			}
			return errors.New("local audit request failed; check the dashboard and retry explicitly")
		}
		data, readErr := boundedBody(res.Body, maxBody)
		res.Body.Close()
		if readErr != nil {
			return readErr
		}
		if res.StatusCode == http.StatusUnauthorized && attempt == 0 {
			b.mu.Lock()
			b.token = ""
			b.result = nil
			b.resultID = ""
			b.mu.Unlock()
			continue
		}
		if res.StatusCode != http.StatusOK {
			// Never reflect arbitrary upstream text, URLs, credentials or driver errors.
			switch res.StatusCode {
			case 413:
				return errors.New("audit processing limit reached; increase bucketSeconds, narrow controller/action or sources, and retry explicitly in the dashboard for its detailed limit message")
			case 400:
				return errors.New("audit query rejected; verify source IDs, date windows, filters and chart interval")
			case 404:
				return errors.New("audit source or record is unavailable; list sources again")
			case 429:
				return errors.New("audit service is busy; wait and retry explicitly")
			case 504:
				return errors.New("audit operation exceeded its five-minute deadline")
			default:
				return fmt.Errorf("local audit operation failed (HTTP %d); check the dashboard", res.StatusCode)
			}
		}
		if err = json.Unmarshal(data, output); err != nil {
			return errors.New("local audit response was not valid data")
		}
		return nil
	}
	return errors.New("local audit session changed; list sources and retry")
}
func strictArgs(data json.RawMessage, target any) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		data = []byte(`{}`)
	}
	if len(data) == 0 || data[0] != '{' {
		return errors.New("tool arguments must be an object")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return errors.New("invalid or unknown tool arguments; follow the tool input schema")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("invalid trailing tool arguments")
	}
	return nil
}
