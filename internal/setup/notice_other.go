//go:build !windows

package setup

// ShowNotice does nothing off Windows.
func ShowNotice(title, text string) {}
