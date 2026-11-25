package localci

import "testing"

func TestKeepAwakeSetAndCloseDoNotHang(t *testing.T) {
	k := NewKeepAwake()
	k.Set(true)
	k.Set(false)
	k.Set(true)
	k.Close()
	k.Close() // a second Close is harmless
}
