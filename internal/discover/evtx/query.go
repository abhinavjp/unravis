package evtx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ErrAccessDenied marks a channel the account may not read. It wraps
// fs.ErrPermission, so discover.IsAccessDenied recognises it and the scan
// reports needs-elevation.
var ErrAccessDenied = fmt.Errorf("access denied: %w", fs.ErrPermission)

// ErrChannelRefused is returned for a channel name the reader will not open.
var ErrChannelRefused = errors.New("evtx: channel refused")

// Result is what one Query wrote.
type Result struct {
	// Rows is the number of CSV data rows written.
	Rows int
	// LastRecordID is the highest EventRecordID written; 0 when no row was written.
	LastRecordID uint64
	// Skipped counts events whose values could not be read. They are not in the output.
	Skipped int
	// Truncated is true when the row cap stopped the pull. Oldest events come
	// first, so LastRecordID is a valid watermark to continue from.
	Truncated bool
}

// Filter narrows a query to one provider and, optionally, some event ids.
type Filter struct {
	Provider string
	IDs      []uint16
}

// Query reads channel from the first event inside [from, to) whose record ID is
// above afterRecordID, oldest first, and writes the win-events CSV to w. A zero
// from or to leaves that end open; afterRecordID 0 reads from the start. It
// returns when the window is exhausted, the row cap is reached or ctx is done.
//
// Only System values are read, plus the AppPoolID value of WAS events. Message
// text and all other EventData are never requested. The Security channel is
// refused.
func Query(ctx context.Context, w io.Writer, channel string, from, to time.Time, afterRecordID uint64) (Result, error) {
	return QueryFiltered(ctx, w, channel, from, to, afterRecordID, nil)
}

// QueryFiltered is Query with a provider and id filter added to the XPath, for
// callers that want only the events a classifier knows.
func QueryFiltered(ctx context.Context, w io.Writer, channel string, from, to time.Time, afterRecordID uint64, filters []Filter) (Result, error) {
	if err := ValidateChannel(channel); err != nil {
		return Result{}, err
	}
	xp, err := BuildXPath(from, to, afterRecordID, filters)
	if err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	return queryChannel(ctx, w, channel, xp)
}

// ValidateChannel refuses an empty or oddly shaped name and the Security channel.
func ValidateChannel(name string) error {
	if name == "" || len(name) > 255 || cleanText(name) != name {
		return ErrChannelRefused
	}
	if strings.EqualFold(name, "Security") || strings.HasPrefix(strings.ToLower(name), "security/") {
		return ErrChannelRefused
	}
	return nil
}

var providerNameRE = regexp.MustCompile(`^[A-Za-z0-9 ._/()#+-]{1,128}$`)

const xpathTime = "2006-01-02T15:04:05.000Z"

// BuildXPath returns the query for a window, a record ID watermark and optional
// provider filters. With nothing to narrow it is "*". A provider name outside a
// plain character set is refused so no value can break out of the quoted string.
func BuildXPath(from, to time.Time, afterRecordID uint64, filters []Filter) (string, error) {
	var conds []string
	switch {
	case !from.IsZero() && !to.IsZero():
		if !to.After(from) {
			return "", errors.New("evtx: window end is not after its start")
		}
		conds = append(conds, "TimeCreated[@SystemTime>='"+from.UTC().Format(xpathTime)+"' and @SystemTime<'"+to.UTC().Format(xpathTime)+"']")
	case !from.IsZero():
		conds = append(conds, "TimeCreated[@SystemTime>='"+from.UTC().Format(xpathTime)+"']")
	case !to.IsZero():
		conds = append(conds, "TimeCreated[@SystemTime<'"+to.UTC().Format(xpathTime)+"']")
	}
	if afterRecordID > 0 {
		conds = append(conds, "EventRecordID>"+strconv.FormatUint(afterRecordID, 10))
	}
	if len(filters) > 0 {
		var alts []string
		for _, f := range filters {
			if !providerNameRE.MatchString(f.Provider) {
				return "", errors.New("evtx: provider name not allowed in a filter")
			}
			c := "Provider[@Name='" + f.Provider + "']"
			if len(f.IDs) > 0 {
				ids := make([]string, len(f.IDs))
				for i, id := range f.IDs {
					ids[i] = "EventID=" + strconv.Itoa(int(id))
				}
				c = "(" + c + " and (" + strings.Join(ids, " or ") + "))"
			} else {
				c = "(" + c + ")"
			}
			alts = append(alts, c)
		}
		conds = append(conds, "("+strings.Join(alts, " or ")+")")
	}
	if len(conds) == 0 {
		return "*", nil
	}
	return "*[System[" + strings.Join(conds, " and ") + "]]", nil
}
