package logos

import (
	"os"
	"path/filepath"
	"testing"
)

func testGraph(t *testing.T) *Graph {
	t.Helper()
	dir := t.TempDir()
	g, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	return g
}

func TestGraphDefineAndEval(t *testing.T) {
	g := testGraph(t)

	node, err := g.Define("x", "42")
	if err != nil {
		t.Fatal(err)
	}
	if node.ID != "node:x-1" {
		t.Fatalf("expected node:x-1, got %s", node.ID)
	}

	val, err := g.Eval("x")
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(42)) {
		t.Fatalf("expected 42, got %s", val.String())
	}
}

func TestGraphDefineFunction(t *testing.T) {
	g := testGraph(t)

	_, err := g.Define("double", `(fn (x) (concat x x))`)
	if err != nil {
		t.Fatal(err)
	}

	val, err := g.Eval(`(double "ha")`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, StringVal("haha")) {
		t.Fatalf("expected 'haha', got %s", val.String())
	}
}

func TestGraphDelete(t *testing.T) {
	g := testGraph(t)

	g.Define("x", "1")
	if err := g.Delete("x"); err != nil {
		t.Fatal(err)
	}

	_, err := g.Eval("x")
	if err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestGraphDeleteUndefined(t *testing.T) {
	g := testGraph(t)
	err := g.Delete("nonexistent")
	if err == nil {
		t.Fatal("expected error deleting undefined symbol")
	}
}

func TestGraphColonInName(t *testing.T) {
	g := testGraph(t)
	_, err := g.Define("bad:name", "1")
	if err == nil {
		t.Fatal("expected error for colon in name")
	}
}

func TestGraphRedefine(t *testing.T) {
	g := testGraph(t)

	g.Define("x", "1")
	g.Define("x", "2")

	val, err := g.Eval("x")
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(2)) {
		t.Fatalf("expected 2, got %s", val.String())
	}
}

func TestGraphResolveAST(t *testing.T) {
	g := testGraph(t)

	g.Define("a", "10")
	// Define b referencing a — a should be resolved to a NodeRef
	node, err := g.Define("b", "a")
	if err != nil {
		t.Fatal(err)
	}
	if len(node.Refs) != 1 || node.Refs[0].Symbol != "a" {
		t.Fatalf("expected ref to 'a', got %v", node.Refs)
	}

	val, err := g.Eval("b")
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(10)) {
		t.Fatalf("expected 10, got %s", val.String())
	}
}

func TestGraphResolveASTFnScoping(t *testing.T) {
	g := testGraph(t)

	// Define x globally
	g.Define("x", "99")
	// Define a fn with param x — param should shadow, not resolve to NodeRef
	node, err := g.Define("f", "(fn (x) x)")
	if err != nil {
		t.Fatal(err)
	}
	// The fn body's x should NOT be resolved to a NodeRef (it's a param)
	for _, ref := range node.Refs {
		if ref.Symbol == "x" {
			t.Fatal("fn param 'x' should not be resolved to a NodeRef")
		}
	}

	// Calling f should use the argument, not the global
	val, err := g.Eval(`(f 42)`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(42)) {
		t.Fatalf("expected 42, got %s", val.String())
	}
}

func TestGraphLogReplay(t *testing.T) {
	dir := t.TempDir()

	// Create graph, define some things, close it
	g1, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	g1.Define("a", "1")
	g1.Define("b", "2")
	g1.Delete("a")
	g1.Define("f", `(fn (x) x)`)
	g1.Close()

	// Reopen — should replay
	g2, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	defer g2.Close()

	// a was deleted
	_, err = g2.Eval("a")
	if err == nil {
		t.Fatal("expected error for deleted 'a'")
	}

	// b survives
	val, err := g2.Eval("b")
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(2)) {
		t.Fatalf("expected 2, got %s", val.String())
	}

	// f survives
	val, err = g2.Eval(`(f 42)`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(42)) {
		t.Fatalf("expected 42, got %s", val.String())
	}
}

func TestGraphLogFormat(t *testing.T) {
	dir := t.TempDir()
	g, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	g.Define("x", "42")
	g.Delete("x")
	g.Close()

	data, err := os.ReadFile(filepath.Join(dir, "log.logos"))
	if err != nil {
		t.Fatal(err)
	}
	expected := "(define x 42)\n\n(delete x)\n\n"
	if string(data) != expected {
		t.Fatalf("log mismatch:\nexpected: %q\ngot:      %q", expected, string(data))
	}
}
