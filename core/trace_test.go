package logos

import (
	"fmt"
	"testing"
)

func TestTraceToValue(t *testing.T) {
	tr := &Trace{
		Entry:     "node:handler-1",
		Args:      []Value{StringVal("hello")},
		Sends:     []SendRecord{{Module: "mod:0", Request: IntVal(1), Response: IntVal(2)}},
		Result:    IntVal(42),
		Timestamp: "2026-02-27T20:00:00Z",
	}

	v := tr.ToValue()
	if v.Kind != ValMap {
		t.Fatalf("expected Map, got %s", v.KindName())
	}
	m := *v.Map

	if !ValuesEqual(m["entry"], StringVal("node:handler-1")) {
		t.Fatalf("entry mismatch: %s", m["entry"].String())
	}
	if !ValuesEqual(m["timestamp"], StringVal("2026-02-27T20:00:00Z")) {
		t.Fatalf("timestamp mismatch: %s", m["timestamp"].String())
	}
	if !ValuesEqual(m["result"], IntVal(42)) {
		t.Fatalf("result mismatch: %s", m["result"].String())
	}
	if !ValuesEqual(m["error"], NilVal()) {
		t.Fatalf("error should be nil, got %s", m["error"].String())
	}

	// Check args
	args := *m["args"].List
	if len(args) != 1 || !ValuesEqual(args[0], StringVal("hello")) {
		t.Fatalf("args mismatch: %s", m["args"].String())
	}

	// Check sends
	sends := *m["sends"].List
	if len(sends) != 1 {
		t.Fatalf("expected 1 send, got %d", len(sends))
	}
	send := *sends[0].Map
	if !ValuesEqual(send["module"], StringVal("mod:0")) {
		t.Fatalf("send module mismatch: %s", send["module"].String())
	}
}

func TestTraceToValueWithError(t *testing.T) {
	tr := &Trace{
		Entry:     "(bad expr)",
		Error:     "assert failed: boom",
		Timestamp: "2026-02-27T20:00:00Z",
	}

	v := tr.ToValue()
	m := *v.Map
	if !ValuesEqual(m["error"], StringVal("assert failed: boom")) {
		t.Fatalf("error mismatch: %s", m["error"].String())
	}
}

func TestAssertErrorFormat(t *testing.T) {
	ae := &AssertError{Message: "count must be positive", Node: "node:handler-3"}
	expected := "assert failed [node:handler-3]: count must be positive"
	if ae.Error() != expected {
		t.Fatalf("expected %q, got %q", expected, ae.Error())
	}

	// Without node
	ae2 := &AssertError{Message: "something wrong"}
	expected2 := "assert failed: something wrong"
	if ae2.Error() != expected2 {
		t.Fatalf("expected %q, got %q", expected2, ae2.Error())
	}
}

func TestAppendTraceCap(t *testing.T) {
	c := &Core{
		maxTraces: 3,
	}
	for i := 0; i < 5; i++ {
		c.appendTrace(&Trace{Entry: fmt.Sprintf("%d", i)})
	}
	if len(c.traces) != 3 {
		t.Fatalf("expected 3 traces, got %d", len(c.traces))
	}
	// Should have traces 2, 3, 4
	if c.traces[0].Entry != "2" {
		t.Fatalf("expected oldest trace entry '2', got %q", c.traces[0].Entry)
	}
	if c.traces[2].Entry != "4" {
		t.Fatalf("expected newest trace entry '4', got %q", c.traces[2].Entry)
	}
}
