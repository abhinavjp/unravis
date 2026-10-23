package audit

import "fmt"

// The two orders a request page can be listed in. Newest is the default and the
// only order of the plain history; oldest first exists for the stale-work list.
const (
	OrderNewest = "newest"
	OrderOldest = "oldest"
)

// NormalizeOrder returns newest for an empty order, the order itself when it is
// known and an invalid-input error otherwise.
func NormalizeOrder(order string) (string, error) {
	switch order {
	case "", OrderNewest:
		return OrderNewest, nil
	case OrderOldest:
		return OrderOldest, nil
	}
	return "", fmt.Errorf("%w: order must be newest or oldest", ErrInvalidInput)
}

// ValidateOrder checks a page's order against its query. Oldest first is legal
// only for a list of unfinished work: an incomplete or pending completion filter,
// or an incomplete-age range (which implies incomplete). Anywhere else it would
// page the oldest rows of an ordinary window, which nothing asks for, so it is
// rejected rather than silently ignored. It returns the normalized order.
func ValidateOrder(q Query, order string) (string, error) {
	order, err := NormalizeOrder(order)
	if err != nil {
		return "", err
	}
	if order == OrderOldest {
		switch EffectiveCompletion(q) {
		case StateIncomplete, StatePending:
		default:
			return "", fmt.Errorf("%w: oldest first needs an incomplete or pending completion filter", ErrInvalidInput)
		}
	}
	return order, nil
}
