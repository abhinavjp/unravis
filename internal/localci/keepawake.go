package localci

import (
	"runtime"
	"sync"
)

// KeepAwake keeps the PC from going to sleep while there is CI work. On Windows it asks for "system required"
// (SetThreadExecutionState); the display may still turn off. Elsewhere it does nothing. The request belongs to
// one OS thread, so a goroutine locked to a thread sets and clears it.
type KeepAwake struct {
	ch   chan bool
	done chan struct{}
	once sync.Once
}

// NewKeepAwake starts the goroutine. Call Close to release the request.
func NewKeepAwake() *KeepAwake {
	k := &KeepAwake{ch: make(chan bool, 4), done: make(chan struct{})}
	go k.loop()
	return k
}

func (k *KeepAwake) loop() {
	runtime.LockOSThread()
	defer close(k.done)
	defer setExecutionState(false)
	for on := range k.ch {
		setExecutionState(on)
	}
}

// Set asks to keep the system awake (true) or lets it sleep again (false).
func (k *KeepAwake) Set(on bool) { k.ch <- on }

// Close releases the request and stops the goroutine.
func (k *KeepAwake) Close() {
	k.once.Do(func() { close(k.ch) })
	<-k.done
}
