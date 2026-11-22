package releasedash

import "sync"

// logKey says which file, as which reader, in which state: the same key is the same answer.
type logKey struct {
	path, names string
	size, mod   int64
}

// memo keeps recent answers that are expensive to make (a saved log unpacked, redacted and parsed). It is least recently used: a hit
// moves the entry to the front of the line, so the log a person is paging through is not pushed out by the detail page's reads. The
// lock is held only to look up and to insert, never while an answer is made, so a slow read does not hold up a quick one. Two callers
// that ask for the same key at the same time share one read.
type memo[T any] struct {
	mu        sync.Mutex
	items     []memoEntry[T] // oldest first
	flights   map[logKey]*memoFlight[T]
	weight    int // sum of the weights of items
	maxItems  int
	maxWeight int
}

type memoEntry[T any] struct {
	key logKey
	val T
	w   int
}

type memoFlight[T any] struct {
	done chan struct{}
	val  T
	err  error
}

// bigRead limits how many saved logs are unpacked at the same time (each can hold tens of MB while it is read).
var bigRead = make(chan struct{}, 2)

func (m *memo[T]) get(key logKey, weigh func(T) int, load func() (T, error)) (T, error) {
	m.mu.Lock()
	for i := len(m.items) - 1; i >= 0; i-- {
		if m.items[i].key == key {
			e := m.items[i]
			m.items = append(append(m.items[:i:i], m.items[i+1:]...), e) // most recently used goes last
			m.mu.Unlock()
			return e.val, nil
		}
	}
	if f, ok := m.flights[key]; ok {
		m.mu.Unlock()
		<-f.done
		return f.val, f.err
	}
	f := &memoFlight[T]{done: make(chan struct{})}
	if m.flights == nil {
		m.flights = map[logKey]*memoFlight[T]{}
	}
	m.flights[key] = f
	m.mu.Unlock()

	bigRead <- struct{}{}
	f.val, f.err = load()
	<-bigRead

	m.mu.Lock()
	delete(m.flights, key)
	if f.err == nil {
		w := weigh(f.val)
		m.items = append(m.items, memoEntry[T]{key, f.val, w})
		m.weight += w
		// Oldest out first, but never the entry just added.
		for len(m.items) > 1 && (len(m.items) > m.maxItems || m.weight > m.maxWeight) {
			m.weight -= m.items[0].w
			m.items = m.items[1:]
		}
	}
	m.mu.Unlock()
	close(f.done)
	return f.val, f.err
}
