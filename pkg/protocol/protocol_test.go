package protocol

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestWireFormat_RoundTrip(t *testing.T) {
	// bytes.Buffer acts like a network connection in memory
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	decoder := json.NewDecoder(&buf)

	// Create a sample request
	req := Request{
		ID:     "msg-123",
		Method: "register",
		Params: map[string]string{"harness": "claude-code"},
	}

	// 1. Encode into the buffer
	if err := encoder.Encode(req); err != nil {
		t.Fatalf("failed to encode: %v", err)
	}

	// 2. Decode out of the buffer
	var decodedReq Request
	if err := decoder.Decode(&decodedReq); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	// 3. Verify the data survived the round trip
	if decodedReq.ID != req.ID {
		t.Errorf("ID got %q, want %q", decodedReq.ID, req.ID)
	}
	if decodedReq.Method != req.Method {
		t.Errorf("Method got %q, want %q", decodedReq.Method, req.Method)
	}
}
