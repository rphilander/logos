package logos

import (
	"os"
	"path/filepath"
	"sort"
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

// --- Graph builtins ---

func TestGraphSymbols(t *testing.T) {
	g := testGraph(t)
	g.Define("x", "42")
	g.Define("y", `"hello"`)

	val, err := g.Eval("(symbols)")
	if err != nil {
		t.Fatal(err)
	}
	if val.Kind != ValMap {
		t.Fatalf("expected Map, got %s", val.KindName())
	}
	m := *val.Map
	if len(m) != 2 {
		t.Fatalf("expected 2 symbols, got %d", len(m))
	}
	xRef := m["x"]
	if xRef.Kind != ValNodeRef {
		t.Fatalf("expected NodeRef for x, got %s", xRef.KindName())
	}
	if xRef.Str != "node:x-1" {
		t.Fatalf("expected node:x-1, got %s", xRef.Str)
	}
}

func TestGraphSymbolsEmpty(t *testing.T) {
	g := testGraph(t)
	val, err := g.Eval("(symbols)")
	if err != nil {
		t.Fatal(err)
	}
	if val.Kind != ValMap {
		t.Fatalf("expected Map, got %s", val.KindName())
	}
	if len(*val.Map) != 0 {
		t.Fatalf("expected empty map, got %d entries", len(*val.Map))
	}
}

func TestGraphNodeExprLiteral(t *testing.T) {
	g := testGraph(t)
	g.Define("x", "(add 1 2)")

	val, err := g.Eval(`(node-expr (quote x))`)
	if err != nil {
		t.Fatal(err)
	}
	if val.Kind != ValList {
		t.Fatalf("expected List, got %s", val.KindName())
	}
	elems := *val.List
	if len(elems) != 3 {
		t.Fatalf("expected 3 elements, got %d", len(elems))
	}
	// Head should be symbol 'add'
	if elems[0].Kind != ValSymbol || elems[0].Str != "add" {
		t.Fatalf("expected symbol 'add', got %s %q", elems[0].KindName(), elems[0].String())
	}
	// Args should be int literals
	if !ValuesEqual(elems[1], IntVal(1)) || !ValuesEqual(elems[2], IntVal(2)) {
		t.Fatalf("expected 1 and 2, got %s and %s", elems[1].String(), elems[2].String())
	}
}

func TestGraphNodeExprWithRef(t *testing.T) {
	g := testGraph(t)
	g.Define("a", "10")
	g.Define("b", "(add a 1)")

	val, err := g.Eval(`(node-expr (quote b))`)
	if err != nil {
		t.Fatal(err)
	}
	elems := *val.List
	if len(elems) != 3 {
		t.Fatalf("expected 3 elements, got %d", len(elems))
	}
	// 'a' should have been resolved to a NodeRef
	if elems[1].Kind != ValNodeRef {
		t.Fatalf("expected NodeRef for 'a' ref, got %s", elems[1].KindName())
	}
	if elems[1].Str != "node:a-1" {
		t.Fatalf("expected node:a-1, got %s", elems[1].Str)
	}
}

func TestGraphNodeExprScalar(t *testing.T) {
	g := testGraph(t)
	g.Define("x", "42")

	val, err := g.Eval(`(node-expr (quote x))`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(42)) {
		t.Fatalf("expected 42, got %s", val.String())
	}
}

func TestGraphRefBy(t *testing.T) {
	g := testGraph(t)
	g.Define("a", "10")
	g.Define("b", "(add a 1)")
	g.Define("c", "(add a 2)")
	g.Define("d", "99") // does not reference a

	val, err := g.Eval(`(ref-by (quote a))`)
	if err != nil {
		t.Fatal(err)
	}
	if val.Kind != ValList {
		t.Fatalf("expected List, got %s", val.KindName())
	}
	elems := *val.List
	if len(elems) != 2 {
		t.Fatalf("expected 2 dependents, got %d", len(elems))
	}
	names := []string{elems[0].Str, elems[1].Str}
	sort.Strings(names)
	if names[0] != "b" || names[1] != "c" {
		t.Fatalf("expected [b, c], got %v", names)
	}
	// Should be symbols
	if elems[0].Kind != ValSymbol {
		t.Fatalf("expected Symbol, got %s", elems[0].KindName())
	}
}

func TestGraphRefByNone(t *testing.T) {
	g := testGraph(t)
	g.Define("a", "10")

	val, err := g.Eval(`(ref-by (quote a))`)
	if err != nil {
		t.Fatal(err)
	}
	if val.Kind != ValList {
		t.Fatalf("expected List, got %s", val.KindName())
	}
	if len(*val.List) != 0 {
		t.Fatalf("expected empty list, got %d", len(*val.List))
	}
}

// --- RefreshAll ---

func TestRefreshAllDry(t *testing.T) {
	g := testGraph(t)
	g.Define("a", "10")
	g.Define("b", "(add a 1)")
	g.Define("c", "(add b 1)")

	result, err := g.RefreshAll([]string{"a"}, true)
	if err != nil {
		t.Fatal(err)
	}
	// b depends on a, c depends on b => both should cascade
	if len(result.Refreshed) != 2 {
		t.Fatalf("expected 2 refreshed, got %d: %v", len(result.Refreshed), result.Refreshed)
	}
	names := make([]string, len(result.Refreshed))
	copy(names, result.Refreshed)
	sort.Strings(names)
	if names[0] != "b" || names[1] != "c" {
		t.Fatalf("expected [b, c], got %v", names)
	}
}

func TestRefreshAll(t *testing.T) {
	g := testGraph(t)
	g.Define("a", "10")
	g.Define("b", "a")

	// b evaluates to 10 via node:a-1
	val, err := g.Eval("b")
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(10)) {
		t.Fatalf("expected 10, got %s", val.String())
	}

	// Redefine a — b still points to old node
	g.Define("a", "20")
	val, _ = g.Eval("b")
	if !ValuesEqual(val, IntVal(10)) {
		t.Fatalf("expected stale 10, got %s", val.String())
	}

	// Refresh
	result, err := g.RefreshAll([]string{"a"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Refreshed) != 1 || result.Refreshed[0] != "b" {
		t.Fatalf("expected [b], got %v", result.Refreshed)
	}

	// After refresh, b should see new a
	val, err = g.Eval("b")
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(20)) {
		t.Fatalf("expected 20 after refresh, got %s", val.String())
	}
}

func TestRefreshAllCascade(t *testing.T) {
	g := testGraph(t)
	g.Define("a", "1")
	g.Define("b", "(add a 10)")
	g.Define("c", "(add b 100)")

	g.Define("a", "2")

	result, err := g.RefreshAll([]string{"a"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Refreshed) != 2 {
		t.Fatalf("expected 2 refreshed, got %d", len(result.Refreshed))
	}

	val, err := g.Eval("c")
	if err != nil {
		t.Fatal(err)
	}
	// c = b + 100 = (a + 10) + 100 = 2 + 10 + 100 = 112
	if !ValuesEqual(val, IntVal(112)) {
		t.Fatalf("expected 112, got %s", val.String())
	}
}

func TestRefreshAllLogReplay(t *testing.T) {
	dir := t.TempDir()

	g1, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	g1.Define("a", "10")
	g1.Define("b", "a")
	g1.Define("a", "20")
	g1.RefreshAll([]string{"a"}, false)
	g1.Close()

	// Reopen and verify state persists
	g2, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	defer g2.Close()

	val, err := g2.Eval("b")
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(20)) {
		t.Fatalf("expected 20 after replay, got %s", val.String())
	}
}
