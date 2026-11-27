package saltmaster

import "testing"

func TestChunkDefaultAndCap(t *testing.T) {
	if got := (Master{}).chunk(); got != 128<<10 {
		t.Fatalf("default chunk = %d", got)
	}
	if got := (Master{ChunkBytes: 1 << 30}).chunk(); got != maxChunk {
		t.Fatalf("capped chunk = %d", got)
	}
	if got := (Master{ChunkBytes: 1000}).chunk(); got != 1000 {
		t.Fatalf("explicit chunk = %d", got)
	}
}
