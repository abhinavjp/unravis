package store

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type clientFactory func(context.Context, string, *tls.Config) (clientHandle, error)

type clientHandle interface {
	Probe(context.Context, string, string) (version, indexWarning string, err error)
	Aggregate(context.Context, string, string, mongo.Pipeline) (mongoCursor, error)
	Close(context.Context) error
}

type mongoCursor interface {
	Next(context.Context) bool
	Decode(any) error
	Err() error
	Close(context.Context) error
}

type sharedClient struct {
	publicID string
	label    string
	client   clientHandle
	key      string
	refs     int
	uses     int
	removed  bool
}

type profile struct {
	source        audit.Source
	uri           string
	identity      string
	connectionKey string
	shared        *sharedClient
	version       string
	indexWarning  string
}

// Sources returns only public metadata. The connection URI is never part of audit.Source.
func (s *Store) Sources(ctx context.Context) ([]audit.Source, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	items := make([]audit.Source, 0, len(s.sources))
	for _, p := range s.sources {
		items = append(items, p.source)
	}
	s.mu.RUnlock()
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

// AddSource validates and probes a profile before making it visible to callers.
func (s *Store) AddSource(ctx context.Context, in audit.SourceInput) (audit.Source, error) {
	validated, identity, connectionKey, err := validateProfile(in)
	if err != nil {
		return audit.Source{}, errInvalidProfile
	}
	s.addMu.Lock()
	defer s.addMu.Unlock()
	if err := ctx.Err(); err != nil {
		return audit.Source{}, err
	}

	s.mu.RLock()
	if len(s.sources) >= maxSources {
		s.mu.RUnlock()
		return audit.Source{}, errSourceLimit
	}
	for _, p := range s.sources {
		if p.identity == identity {
			s.mu.RUnlock()
			return audit.Source{}, errDuplicate
		}
	}
	shared := s.clients[connectionKey]
	s.mu.RUnlock()

	newShared := false
	if shared == nil {
		tlsConfig, err := buildTLSConfig(validated.TLSMode, validated.CACertificate)
		if err != nil {
			return audit.Source{}, errInvalidProfile
		}
		client, err := s.newClient(ctx, validated.URI, tlsConfig)
		if err != nil {
			return audit.Source{}, fmt.Errorf("%w: could not connect to MongoDB source", audit.ErrInvalidInput)
		}
		publicID, idErr := newID()
		if idErr != nil {
			closeClientWithTimeout(client)
			return audit.Source{}, errors.New("could not allocate connection identifier")
		}
		connectionLabel := validated.ConnectionLabel
		if connectionLabel == "" {
			parsed, _ := url.Parse(validated.URI)
			connectionLabel = parsed.Host
		}
		shared = &sharedClient{client: client, key: connectionKey, publicID: publicID, label: connectionLabel}
		newShared = true
	}
	version, warning, err := shared.client.Probe(ctx, validated.Database, validated.Collection)
	if err != nil {
		if newShared {
			closeClientWithTimeout(shared.client)
		}
		if ctx.Err() != nil {
			return audit.Source{}, ctx.Err()
		}
		return audit.Source{}, fmt.Errorf("%w: MongoDB source validation failed; check the connection, permissions, and server version", audit.ErrInvalidInput)
	}
	if err := ctx.Err(); err != nil {
		if newShared {
			closeClientWithTimeout(shared.client)
		}
		return audit.Source{}, err
	}

	id, err := newID()
	if err != nil {
		if newShared {
			closeClientWithTimeout(shared.client)
		}
		return audit.Source{}, errors.New("could not allocate source identifier")
	}
	p := &profile{
		source: audit.Source{SelectionKey: sourceSelectionKey(validated), ID: id, Label: validated.Label, Database: validated.Database, Collection: validated.Collection, ConnectionID: shared.publicID, ConnectionLabel: shared.label},
		uri:    validated.URI, identity: identity, connectionKey: connectionKey,
		shared: shared, version: version, indexWarning: warning,
	}
	s.mu.Lock()
	if err := ctx.Err(); err != nil {
		s.mu.Unlock()
		if newShared {
			closeClientWithTimeout(shared.client)
		}
		return audit.Source{}, err
	}
	if newShared {
		s.clients[connectionKey] = shared
	}
	shared.refs++
	s.sources[id] = p
	if _, exists := s.perSource[id]; !exists {
		s.perSource[id] = make(chan struct{}, 2)
	}
	s.mu.Unlock()
	return p.source, nil
}

// DeleteSource removes a profile and closes its connection when no profile shares it.
func (s *Store) DeleteSource(ctx context.Context, id string) error {
	s.addMu.Lock()
	defer s.addMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	p, ok := s.sources[id]
	if !ok {
		s.mu.Unlock()
		return errSourceNotFound
	}
	delete(s.sources, id)
	delete(s.perSource, id)
	p.shared.refs--
	var closeClient clientHandle
	if p.shared.refs == 0 {
		delete(s.clients, p.connectionKey)
		p.shared.removed = true
		if p.shared.uses == 0 {
			closeClient = p.shared.client
		}
	}
	s.mu.Unlock()
	if closeClient != nil {
		if err := closeClient.Close(ctx); err != nil && ctx.Err() == nil {
			return errors.New("source removed, but MongoDB connection cleanup failed")
		}
	}
	return nil
}

func (s *Store) leaseSource(id string) (*profile, func(), error) {
	s.mu.Lock()
	p, ok := s.sources[id]
	if !ok {
		s.mu.Unlock()
		return nil, nil, errSourceNotFound
	}
	p.shared.uses++
	s.mu.Unlock()
	var once sync.Once
	return p, func() {
		once.Do(func() {
			s.mu.Lock()
			p.shared.uses--
			closeClient := p.shared.client
			shouldClose := p.shared.removed && p.shared.refs == 0 && p.shared.uses == 0
			s.mu.Unlock()
			if shouldClose {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = closeClient.Close(ctx)
			}
		})
	}, nil
}

func validateProfile(in audit.SourceInput) (audit.SourceInput, string, string, error) {
	in.Label = strings.TrimSpace(in.Label)
	in.ConnectionLabel = strings.TrimSpace(in.ConnectionLabel)
	in.Database = strings.TrimSpace(in.Database)
	in.Collection = strings.TrimSpace(in.Collection)
	if in.Label == "" || len(in.Label) > 100 || len(in.ConnectionLabel) > 120 ||
		in.Database == "" || len(in.Database) > 120 || in.Collection == "" || len(in.Collection) > 120 ||
		strings.ContainsAny(in.Collection, `/\\`) {
		return audit.SourceInput{}, "", "", errInvalidProfile
	}
	mode, err := normalizeTLSMode(in.TLSMode)
	if err != nil {
		return audit.SourceInput{}, "", "", err
	}
	if _, err := buildTLSConfig(mode, in.CACertificate); err != nil {
		return audit.SourceInput{}, "", "", err
	}
	in.TLSMode = mode
	uri, err := composeMongoURI(audit.ConnectionInput{URI: in.URI, Username: in.Username, Password: in.Password, AuthSource: in.AuthSource, TLSMode: in.TLSMode, CACertificate: in.CACertificate, SavedConnectionID: in.SavedConnectionID})
	if err != nil {
		return audit.SourceInput{}, "", "", err
	}
	in.URI = uri
	u, _ := url.Parse(uri)
	query, _ := url.ParseQuery(u.RawQuery)
	// Normalize host and option order for safe connection reuse.
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimSuffix(u.Path, "/")
	query = queryToLowerSorted(query)
	u.RawQuery = query.Encode()
	connectionKey := u.String()
	tlsKey, err := tlsConnectionKeyPart(in.TLSMode, in.CACertificate)
	if err != nil {
		return audit.SourceInput{}, "", "", err
	}
	connectionKey += "|tls=" + tlsKey
	// Duplicate identity intentionally excludes credentials and URI database path:
	// a deployment/database/collection is one source regardless of its label.
	identity := sourceSelectionKey(in)
	return in, identity, connectionKey, nil
}

func composeMongoURI(in audit.ConnectionInput) (string, error) {
	if in.SavedConnectionID != "" {
		return "", fmt.Errorf("%w: saved connection must be resolved before store use", audit.ErrInvalidInput)
	}
	if in.URI == "" || len(in.URI) > 4096 || len(in.Username) > 1024 || len(in.Password) > 4096 || len(in.AuthSource) > 120 ||
		(in.Username == "" && in.Password != "") {
		return "", errInvalidProfile
	}
	tlsMode, err := normalizeTLSMode(in.TLSMode)
	if err != nil {
		return "", err
	}
	if _, err := buildTLSConfig(tlsMode, in.CACertificate); err != nil {
		return "", err
	}
	if !strings.Contains(in.URI, "://") {
		if strings.ContainsAny(in.URI, `/\\`) {
			return "", errInvalidProfile
		}
		in.URI = "mongodb://" + in.URI
	}
	u, err := url.Parse(in.URI)
	if err != nil || (u.Scheme != "mongodb" && u.Scheme != "mongodb+srv") || u.Host == "" || u.Fragment != "" {
		return "", errInvalidProfile
	}
	if u.User != nil {
		return "", fmt.Errorf("%w: MongoDB URI must not contain embedded credentials; use separate username and password fields", audit.ErrInvalidInput)
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return "", errInvalidProfile
	}
	if tlsMode == "uri" {
		for name, values := range query {
			lowerName := strings.ToLower(name)
			if lowerName == "tlsinsecure" || lowerName == "tlsallowinvalidcertificates" || lowerName == "tlsallowinvalidhostnames" || lowerName == "sslinsecure" || lowerName == "sslallowinvalidcertificates" || lowerName == "sslallowinvalidhostnames" {
				for _, value := range values {
					if strings.EqualFold(value, "true") || value == "1" {
						return "", errInvalidProfile
					}
				}
			}
		}
	} else {
		removeURITLSOptions(query)
		if tlsMode == "off" {
			query.Set("tls", "false")
		} else {
			query.Set("tls", "true")
		}
	}
	if u.Scheme == "mongodb+srv" && strings.Contains(u.Host, ",") {
		return "", errInvalidProfile
	}
	if authSource := strings.TrimSpace(in.AuthSource); authSource != "" {
		query = queryToLowerSorted(query)
		query.Set("authsource", authSource)
	}
	if in.Username != "" {
		u.User = url.UserPassword(in.Username, in.Password)
	}
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimSuffix(u.Path, "/")
	// MongoDB's URI parser requires an explicit slash between hosts and query
	// options, including when the URI has no database path. Apply this after
	// trimming a root slash from the caller's URI.
	if u.Path == "" && len(query) > 0 {
		u.Path = "/"
	}
	u.RawQuery = queryToLowerSorted(query).Encode()
	return u.String(), nil
}

func queryToLowerSorted(values url.Values) url.Values {
	normalized := make(url.Values, len(values))
	for key, list := range values {
		key = strings.ToLower(key)
		copyList := append([]string(nil), list...)
		sort.Slice(copyList, func(i, j int) bool { return copyList[i] < copyList[j] })
		normalized[key] = copyList
	}
	return normalized
}

// sourceSelectionKey identifies the logical dataset across process restarts.
// It excludes credentials, connection options and user-facing labels.
func sourceSelectionKey(in audit.SourceInput) string {
	parsed, err := url.Parse(in.URI)
	if err != nil {
		return ""
	}
	hosts := strings.Split(strings.ToLower(parsed.Host), ",")
	for i, host := range hosts {
		if hostname, port, splitErr := net.SplitHostPort(host); splitErr == nil && port == "27017" && parsed.Scheme == "mongodb" {
			host = hostname
			if strings.Contains(host, ":") {
				host = "[" + host + "]"
			}
		}
		hosts[i] = host
	}
	sort.Strings(hosts)
	encoded, _ := json.Marshal([]any{strings.ToLower(parsed.Scheme), hosts, in.Database, in.Collection})
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", digest)
}
