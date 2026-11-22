//go:build !windows

package setup

import "time"

// ShowNotice does nothing off Windows.
func ShowNotice(title, text string) {}

// AskOKCancel is not available off Windows: it answers Cancel, so nothing proceeds that needed the person's click.
func AskOKCancel(title, text string, wait time.Duration) bool { return false }
