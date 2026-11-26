package main

import "testing"

func TestBrowserCommandOffWindowsUsesTheSystemOpener(t *testing.T) {
	for goos, want := range map[string]string{"darwin": "open", "linux": "xdg-open"} {
		c := browserCommand(goos, "http://127.0.0.1:1/")
		if len(c.Args) != 2 || c.Args[0] != want || c.Args[1] != "http://127.0.0.1:1/" {
			t.Fatalf("%s: unexpected command %q", goos, c.Args)
		}
	}
}
