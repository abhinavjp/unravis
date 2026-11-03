// Package tray describes what the notification-area icon needs from the app: a
// tooltip, a menu and callbacks. This file holds the plain types and their
// validation; the Windows window and icon live in other files.
package tray

import (
	"errors"
	"fmt"
	"unicode"
	"unicode/utf8"
)

// Limits on what the icon shows.
const (
	MaxItems        = 16
	MaxTextRunes    = 64
	MaxTooltipRunes = 127
)

// ErrUnsupported is returned by Run on a platform without a notification area.
var ErrUnsupported = errors.New("tray: not supported on this platform")

// Item is one menu entry. A separator has no text and no ID.
type Item struct {
	ID        uint32
	Text      string
	Checked   bool
	Separator bool
	Disabled  bool
}

// Options is everything the icon needs from the app.
type Options struct {
	Tooltip      string
	Menu         func() []Item
	OnSelect     func(id uint32)
	OnOpen       func()
	OnEndSession func()
	Logf         func(format string, args ...any)
}

// ValidateItems checks a menu: at most MaxItems entries; a separator is bare;
// every other entry has printable text of at most MaxTextRunes runes and a
// non-zero ID that no other entry uses.
func ValidateItems(items []Item) error {
	if len(items) > MaxItems {
		return fmt.Errorf("tray: %d menu items, at most %d allowed", len(items), MaxItems)
	}
	seen := make(map[uint32]bool, len(items))
	for i, it := range items {
		if it.Separator {
			if it.Text != "" || it.ID != 0 {
				return fmt.Errorf("tray: menu item %d: a separator has no text and no ID", i)
			}
			continue
		}
		if it.Text == "" {
			return fmt.Errorf("tray: menu item %d: empty text", i)
		}
		if n := utf8.RuneCountInString(it.Text); n > MaxTextRunes {
			return fmt.Errorf("tray: menu item %d: text is %d characters, at most %d allowed", i, n, MaxTextRunes)
		}
		for _, r := range it.Text {
			if unicode.IsControl(r) {
				return fmt.Errorf("tray: menu item %d: text has a control character", i)
			}
		}
		if it.ID == 0 {
			return fmt.Errorf("tray: menu item %d: ID must not be zero", i)
		}
		if seen[it.ID] {
			return fmt.Errorf("tray: menu item %d: duplicate ID %d", i, it.ID)
		}
		seen[it.ID] = true
	}
	return nil
}

// validate checks the options before any window is made.
func (o Options) validate() error {
	if o.Menu == nil {
		return errors.New("tray: Menu callback is required")
	}
	if o.OnSelect == nil {
		return errors.New("tray: OnSelect callback is required")
	}
	if o.OnOpen == nil {
		return errors.New("tray: OnOpen callback is required")
	}
	if n := utf8.RuneCountInString(o.Tooltip); n > MaxTooltipRunes {
		return fmt.Errorf("tray: tooltip is %d characters, at most %d allowed", n, MaxTooltipRunes)
	}
	return nil
}

// logf logs through Logf, or does nothing when it is unset.
func (o Options) logf(format string, args ...any) {
	if o.Logf != nil {
		o.Logf(format, args...)
	}
}
