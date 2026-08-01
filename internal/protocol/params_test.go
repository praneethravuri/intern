package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestParamsAndResults_RoundTrip marshals every payload type and reads it back,
// asserting the decoded value is identical to the original.
func TestParamsAndResults_RoundTrip(t *testing.T) {
	delivered := "2026-07-28T10:00:00Z"
	acked := "2026-07-28T10:00:05Z"

	agent := AgentView{
		Address:      "alice@intern",
		Name:         "alice",
		Workspace:    "intern",
		Harness:      "claude-code",
		State:        "working",
		StateSource:  "heartbeat",
		StateAgeMS:   1500,
		StateDetail:  "ran intern send",
		Cwd:          "/home/p/intern",
		PID:          4242,
		Pending:      3,
		Dropped:      1,
		RegisteredAt: "2026-07-28T09:00:00Z",
		LastSeen:     "2026-07-28T10:00:00Z",
	}

	cases := []struct {
		name  string
		value any
		into  func() any
	}{
		{"RegisterParams", RegisterParams{Name: "alice", Workspace: "intern", Harness: "claude-code", SessionID: "sess-1", Cwd: "/tmp", PID: 7}, func() any { return new(RegisterParams) }},
		{"RegisterResult", RegisterResult{Address: "alice@intern", Harness: "claude-code", Created: true}, func() any { return new(RegisterResult) }},
		{"SendParams", SendParams{FromName: "alice", FromWorkspace: "intern", FromSession: "sess-1", ToName: "bob", ToWorkspace: "intern", Kind: "ask", Body: "status?", ReplyTo: "m0"}, func() any { return new(SendParams) }},
		{"SendResult", SendResult{MessageID: "m1"}, func() any { return new(SendResult) }},
		{"InboxParams", InboxParams{Name: "alice", Workspace: "intern", Session: "sess-1", Limit: 25, Peek: true}, func() any { return new(InboxParams) }},
		{"InboxResult", InboxResult{Messages: []MessageView{{ID: "m1", ThreadID: "t1", ReplyTo: "m0", From: "bob@intern", To: "alice@intern", Kind: "reply", Body: "done", CreatedAt: "2026-07-28T09:59:00Z", DeliveredAt: &delivered, AckedAt: &acked}}, Cleared: 1, Pending: 2, Dropped: 3}, func() any { return new(InboxResult) }},
		{"MessageView", MessageView{ID: "m1", ThreadID: "t1", From: "a@w", To: "b@w", Kind: "note", Body: "hi", CreatedAt: "2026-07-28T09:00:00Z"}, func() any { return new(MessageView) }},
		{"WaitParams", WaitParams{Name: "alice", Workspace: "intern", Session: "sess-1", TimeoutMS: 30000}, func() any { return new(WaitParams) }},
		{"WaitResult", WaitResult{Pending: 0, TimedOut: true}, func() any { return new(WaitResult) }},
		{"LsParams", LsParams{Workspace: "intern"}, func() any { return new(LsParams) }},
		{"AgentView", agent, func() any { return new(AgentView) }},
		{"LsResult", LsResult{Agents: []AgentView{agent}}, func() any { return new(LsResult) }},
		{"ExplainParams", ExplainParams{Name: "alice", Workspace: "intern"}, func() any { return new(ExplainParams) }},
		{"ExplainResult", ExplainResult{Agent: agent}, func() any { return new(ExplainResult) }},
		{"ClaimParams", ClaimParams{Workspace: "intern", Key: "src/main.go", OwnerPID: 42, Holder: "alice"}, func() any { return new(ClaimParams) }},
		{"ClaimResult", ClaimResult{LeaseID: "abc123", Holder: "alice", ExpiresAt: "2026-07-28T10:00:00Z", Renewed: true}, func() any { return new(ClaimResult) }},
		{"ReleaseParams", ReleaseParams{Workspace: "intern", Key: "src/main.go", LeaseID: "abc123"}, func() any { return new(ReleaseParams) }},
		{"ReleaseResult", ReleaseResult{}, func() any { return new(ReleaseResult) }},
		{"ClaimsParams", ClaimsParams{Workspace: "intern"}, func() any { return new(ClaimsParams) }},
		{"ClaimView", ClaimView{Workspace: "intern", Key: "src/main.go", OwnerPID: 42, Holder: "alice", Status: "held", LeasedAt: "2026-07-28T09:00:00Z", ExpiresAt: "2026-07-28T10:00:00Z"}, func() any { return new(ClaimView) }},
		{"ClaimsResult", ClaimsResult{Claims: []ClaimView{{Workspace: "intern", Key: "src/main.go", Status: "held"}}}, func() any { return new(ClaimsResult) }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.value)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			ptr := tc.into()
			if err := json.Unmarshal(data, ptr); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			got := reflect.ValueOf(ptr).Elem().Interface()
			if !reflect.DeepEqual(got, tc.value) {
				t.Errorf("round trip mismatch\n got: %+v\nwant: %+v\njson: %s", got, tc.value, data)
			}
		})
	}
}

// TestJSONTags_AreSnakeCase guards the wire contract: every exported field on
// every payload type must carry an explicit lower_snake_case json tag.
func TestJSONTags_AreSnakeCase(t *testing.T) {
	types := []any{
		RegisterParams{}, RegisterResult{},
		SendParams{}, SendResult{},
		InboxParams{}, InboxResult{}, MessageView{},
		WaitParams{}, WaitResult{},
		LsParams{}, LsResult{}, AgentView{},
		ExplainParams{}, ExplainResult{},
		ClaimParams{}, ClaimResult{}, ReleaseParams{}, ReleaseResult{},
		ClaimsParams{}, ClaimView{}, ClaimsResult{},
		Request{}, Response{}, Error{},
	}

	for _, v := range types {
		rt := reflect.TypeOf(v)
		t.Run(rt.Name(), func(t *testing.T) {
			for i := 0; i < rt.NumField(); i++ {
				f := rt.Field(i)
				tag, ok := f.Tag.Lookup("json")
				if !ok {
					t.Errorf("%s.%s has no json tag", rt.Name(), f.Name)
					continue
				}
				name := strings.Split(tag, ",")[0]
				if name == "" {
					t.Errorf("%s.%s has an empty json name", rt.Name(), f.Name)
					continue
				}
				if name != strings.ToLower(name) {
					t.Errorf("%s.%s json name %q is not lowercase", rt.Name(), f.Name, name)
				}
				if strings.ContainsAny(name, "-. ") {
					t.Errorf("%s.%s json name %q is not snake_case", rt.Name(), f.Name, name)
				}
			}
		})
	}
}

// TestMessageView_OmitsNilTimes checks that a message that has not been
// delivered or acked emits no delivered_at / acked_at keys at all, so consumers
// can distinguish "not yet" from "at the zero time".
func TestMessageView_OmitsNilTimes(t *testing.T) {
	data, err := json.Marshal(MessageView{
		ID:        "m1",
		From:      "a@w",
		To:        "b@w",
		Kind:      "note",
		Body:      "hi",
		CreatedAt: "2026-07-28T09:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"delivered_at", "acked_at"} {
		if _, ok := m[key]; ok {
			t.Errorf("unexpected %q key in %s", key, data)
		}
	}
	if _, ok := m["created_at"]; !ok {
		t.Errorf("missing \"created_at\" key in %s", data)
	}

	// And a delivered message does emit delivered_at.
	at := "2026-07-28T09:00:01Z"
	data, err = json.Marshal(MessageView{ID: "m1", CreatedAt: "x", DeliveredAt: &at})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"delivered_at":"2026-07-28T09:00:01Z"`) {
		t.Errorf("delivered_at missing from %s", data)
	}
}

// TestParams_DecodeFromRawRequest is the exact path the daemon takes: read the
// envelope, switch on Method, decode Params into the concrete struct.
func TestParams_DecodeFromRawRequest(t *testing.T) {
	line := `{"id":"r1","method":"send","params":{"from_name":"alice","from_workspace":"intern","to_name":"bob","to_workspace":"intern","kind":"ask","body":"ping","reply_to":""}}`

	var req Request
	if err := json.Unmarshal([]byte(line), &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if req.Method != MethodSend {
		t.Fatalf("method got %q, want %q", req.Method, MethodSend)
	}

	var p SendParams
	if err := json.Unmarshal(req.Params, &p); err != nil {
		t.Fatalf("unmarshal params: %v", err)
	}

	want := SendParams{FromName: "alice", FromWorkspace: "intern", ToName: "bob", ToWorkspace: "intern", Kind: "ask", Body: "ping"}
	if p != want {
		t.Errorf("got %+v, want %+v", p, want)
	}
}

func TestParams_MissingParamsDecodesToZeroValue(t *testing.T) {
	var req Request
	if err := json.Unmarshal([]byte(`{"id":"r1","method":"who"}`), &req); err != nil {
		t.Fatal(err)
	}
	if req.Params != nil {
		t.Fatalf("Params got %s, want nil", req.Params)
	}

	// The daemon treats an absent params block as the zero value.
	var p LsParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			t.Fatal(err)
		}
	}
	if p != (LsParams{}) {
		t.Errorf("got %+v, want zero value", p)
	}
}

func TestParams_UnknownFieldsAreIgnored(t *testing.T) {
	// Forward compatibility: a newer CLI may send fields this daemon build
	// does not know about. Decoding must not fail.
	var p RegisterParams
	in := `{"name":"alice","workspace":"intern","harness":"claude-code","pid":9,"future_field":true}`
	if err := json.Unmarshal([]byte(in), &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p.Name != "alice" || p.PID != 9 {
		t.Errorf("got %+v", p)
	}
}

func TestMethodConstants(t *testing.T) {
	want := map[string]string{
		MethodRegister: "register",
		MethodSend:     "send",
		MethodInbox:    "inbox",
		MethodWait:     "wait",
		MethodLs:       "ls",
		MethodExplain:  "explain",
		MethodClaim:    "claim",
		MethodRelease:  "release",
		MethodClaims:   "claims",
	}
	if len(want) != 9 {
		t.Fatalf("method constants collide: got %d distinct values, want 9", len(want))
	}
	for got, expected := range want {
		if got != expected {
			t.Errorf("method constant got %q, want %q", got, expected)
		}
	}
}

func TestErrorCodeConstants(t *testing.T) {
	codes := map[int]string{
		CodeBadRequest: "bad request",
		CodeNotFound:   "not found",
		CodeConflict:   "conflict",
		CodeTooLarge:   "too large",
		CodeInternal:   "internal",
	}
	if len(codes) != 5 {
		t.Fatalf("error code constants collide: got %d distinct values, want 5", len(codes))
	}
	for _, want := range []int{400, 404, 409, 413, 500} {
		if _, ok := codes[want]; !ok {
			t.Errorf("missing error code %d", want)
		}
	}
}
