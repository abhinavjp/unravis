package mcp

import (
	"strings"
	"testing"
)

// The places where the SDK loop's wire is not byte-for-byte the hand-rolled loop's, pinned so that an SDK bump that moves any of them is
// seen. Each is a field or code a host does not depend on (see docs/desktop/spikes/BL-341-go-sdk.md, "Known differences").
func TestSDKKnownWireDifferences(t *testing.T) {
	srv := demoDashboard(t)
	open := func(im impl) *wire {
		w := dial(t, im, bridgeFor(t, srv))
		w.handshake()
		return w
	}
	t.Run("successful tool result", func(t *testing.T) {
		const call = `{"jsonrpc":"2.0","id":"g","method":"tools/call","params":{"name":"audit_get_guide","arguments":{}}}`
		legacy, sdk := open(legacyImpl), open(sdkImpl)
		legacy.send(call)
		sdk.send(call)
		l, s := resultOf(t, legacy.reply("g")), resultOf(t, sdk.reply("g"))
		if v, present := l["isError"]; !present || v != false {
			t.Errorf("hand-rolled loop: isError=%v present=%v", v, present)
		}
		// The schema makes isError optional, default false: the SDK leaves it out of a success.
		if v, present := s["isError"]; present && v != false {
			t.Errorf("sdk loop: isError=%v", v)
		}
		// resultType belongs to 2026-07-28: absent after a handshake, "complete" on every stateless result.
		if _, present := s["resultType"]; present {
			t.Errorf("sdk loop sent resultType after a handshake: %v", s["resultType"])
		}
		stateless := dial(t, sdkImpl, bridgeFor(t, srv))
		stateless.stateless = true
		if got := resultOf(t, stateless.request("tools/call", `"name":"audit_get_guide","arguments":{}`))["resultType"]; got != "complete" {
			t.Errorf("sdk loop stateless: resultType=%v", got)
		}
	})
	t.Run("tools/list carries cache metadata", func(t *testing.T) {
		l, s := resultOf(t, open(legacyImpl).request("tools/list", "")), resultOf(t, open(sdkImpl).request("tools/list", ""))
		if _, present := l["cacheScope"]; present {
			t.Error("hand-rolled loop sent cacheScope")
		}
		if s["cacheScope"] != "private" || s["ttlMs"] != float64(0) {
			t.Errorf("sdk loop: %v %v", s["cacheScope"], s["ttlMs"])
		}
	})
	t.Run("lifecycle misuse is an error in both, with different codes", func(t *testing.T) {
		for _, c := range []struct {
			im   impl
			code int
		}{{legacyImpl, -32000}, {sdkImpl, 0}} {
			w := dial(t, c.im, bridgeFor(t, srv))
			w.send(`{"jsonrpc":"2.0","id":"early","method":"tools/list"}`)
			res := w.reply("early")
			if res["error"] == nil || errorCode(res) != c.code {
				t.Errorf("%s: tools/list before initialize: %v", c.im.name, res)
			}
			w.handshake()
			w.send(initLine)
			if res := w.reply("init"); res["error"] == nil {
				t.Errorf("%s: a second initialize was accepted", c.im.name)
			}
		}
	})
	t.Run("a cursor on tools/list", func(t *testing.T) {
		if res := open(legacyImpl).request("tools/list", `"cursor":"x"`); res["error"] != nil {
			t.Errorf("hand-rolled loop refused a cursor: %v", res)
		}
		// The catalog has one page and no loop hands out a cursor, so a host never sends one; the SDK refuses a made-up one, with a fixed text.
		if res := open(sdkImpl).request("tools/list", `"cursor":"x"`); errorCode(res) != -32602 || !strings.Contains(string(mustJSON(t, res)), "Invalid params") {
			t.Errorf("sdk loop: %v", res)
		}
	})
}
