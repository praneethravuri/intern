package store

import "errors"

// Sentinel errors returned by the store. Callers should compare with
// errors.Is; the store always wraps or returns these directly rather than
// leaking driver-specific errors for expected conditions.
var (
	// ErrNameTaken means a live agent from a different session already holds
	// the requested workspace/name pair.
	ErrNameTaken = errors.New("store: agent name already taken in this workspace")

	// ErrNoSuchAgent means no row exists in agents for the workspace/name.
	ErrNoSuchAgent = errors.New("store: no such agent")

	// ErrNoSuchMessage means the referenced message id does not exist.
	ErrNoSuchMessage = errors.New("store: no such message")

	// ErrBodyTooLarge means the message body exceeded Store.MaxBodyBytes.
	ErrBodyTooLarge = errors.New("store: message body too large")

	// ErrEmptyBody means the message body was empty after trimming.
	ErrEmptyBody = errors.New("store: message body is empty")

	// ErrBadAddress means an address could not be parsed as name@workspace.
	ErrBadAddress = errors.New("store: bad address")
)
