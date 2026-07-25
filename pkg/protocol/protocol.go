// Package protocol defines the wire format and socket transport for tether.
package protocol

// Request represents an incoming command from an agent
type Request struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params,omitempty"`
}

// Error maps closely to the CLI exit codes
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Response is sent back to the agent
type Response struct {
	ID     string `json:"id"`
	Result any    `json:"result,omitempty"`
	Error  *Error `json:"error,omitempty"`
}
