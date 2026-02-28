package logos

import "fmt"

// Trace captures the boundary points of a single eval: entry node, args,
// module sends, and result/error. The graph is deterministic so replaying
// from these boundaries reproduces the full computation.
type Trace struct {
	Entry     string       // node ID of the entry point (e.g. "node:on-request-3")
	Args      []Value      // arguments passed to the eval
	Sends     []SendRecord // module send/response pairs during this eval
	Result    Value        // final result value
	Error     string       // non-empty on error
	Timestamp string       // ISO 8601
}

// SendRecord captures a single module send during an eval.
type SendRecord struct {
	Module   string // module ID (e.g. "mod:0")
	Request  Value  // request value sent
	Response Value  // response value received
}

// TraceError captures assert failure context within a trace.
type TraceError struct {
	Message string // user-provided assert message
	Node    string // node ID where the assert failed
}

// AssertError is returned by the assert builtin on failure.
// It implements error so it propagates through eval normally.
type AssertError struct {
	Message string
	Node    string
}

func (e *AssertError) Error() string {
	if e.Node != "" {
		return fmt.Sprintf("assert failed [%s]: %s", e.Node, e.Message)
	}
	return fmt.Sprintf("assert failed: %s", e.Message)
}

// ToValue converts a Trace to a logos Map for the traces builtin.
func (t *Trace) ToValue() Value {
	m := map[string]Value{
		"entry":     StringVal(t.Entry),
		"timestamp": StringVal(t.Timestamp),
	}

	// args
	args := make([]Value, len(t.Args))
	copy(args, t.Args)
	m["args"] = ListVal(args)

	// sends
	sends := make([]Value, len(t.Sends))
	for i, s := range t.Sends {
		sends[i] = MapVal(map[string]Value{
			"module":   StringVal(s.Module),
			"request":  s.Request,
			"response": s.Response,
		})
	}
	m["sends"] = ListVal(sends)

	// result
	m["result"] = t.Result

	// error
	if t.Error != "" {
		m["error"] = StringVal(t.Error)
	} else {
		m["error"] = NilVal()
	}

	return MapVal(m)
}
