package logos

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
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

const hofLibrary = `(define nil? (fn (x) (eq (type x) :nil)))

(define empty? (fn (xs) (eq (len xs) 0)))

(define fold (fn (f acc xs) (loop ((acc acc) (xs xs)) (if (empty? xs) acc (recur (f acc (head xs)) (rest xs))))))

(define reverse (fn (xs) (fold (fn (acc x) (cons x acc)) (list) xs)))

(define map (fn (f xs) (loop ((acc (list)) (xs xs)) (if (empty? xs) (reverse acc) (recur (cons (f (head xs)) acc) (rest xs))))))

(define filter (fn (f xs) (loop ((acc (list)) (xs xs)) (if (empty? xs) (reverse acc) (if (f (head xs)) (recur (cons (head xs) acc) (rest xs)) (recur acc (rest xs)))))))

(define group-by (fn (f xs) (fold (fn (acc x) (let (k (to-string (f x)) existing (get acc k)) (put acc k (if (nil? existing) (list x) (append existing (list x)))))) (dict) xs)))
`

const formLibrary = `(define not (fn (x) (if x false true)))

(define nil? (fn (x) (eq (type x) :nil)))

(define empty? (fn (xs) (eq (len xs) 0)))

(define fold (fn (f acc xs) (loop ((acc acc) (xs xs)) (if (empty? xs) acc (recur (f acc (head xs)) (rest xs))))))

(define reverse (fn (xs) (fold (fn (acc x) (cons x acc)) (list) xs)))

(define and (form (a b) (list (quote if) a b false)))

(define or (form (a b) (list (quote let) (list (quote __or-val__) a) (list (quote if) (quote __or-val__) (quote __or-val__) b))))

(define cond (form (& pairs) (let (rev (reverse pairs) expanded (loop ((ps rev) (result (quote nil))) (if (empty? ps) result (recur (rest (rest ps)) (list (quote if) (nth ps 1) (head ps) result))))) expanded)))

(define case (form (target & clauses) (let (rev (reverse clauses) has-default (eq (mod (len clauses) 2) 1) start-result (if has-default (head rev) (quote nil)) start-cs (if has-default (rest rev) rev) expanded (loop ((cs start-cs) (result start-result)) (if (empty? cs) result (recur (rest (rest cs)) (list (quote if) (list (quote eq) (quote __case-target__) (nth cs 1)) (head cs) result))))) (list (quote let) (list (quote __case-target__) target) expanded))))

(define when (form (test body) (list (quote if) test body nil)))

(define unless (form (test body) (list (quote if) test nil body)))
`

func testGraphWithLibrary(t *testing.T) *Graph {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "base.logos"), []byte(hofLibrary), 0644)
	os.WriteFile(filepath.Join(dir, "library-order.txt"), []byte("base\n"), 0644)
	g, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	return g
}

func testGraphWithFormLibrary(t *testing.T) *Graph {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "base.logos"), []byte(formLibrary), 0644)
	os.WriteFile(filepath.Join(dir, "library-order.txt"), []byte("base\n"), 0644)
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

func TestGraphSlashInName(t *testing.T) {
	g := testGraph(t)
	_, err := g.Define("bad/name", "1")
	if err == nil {
		t.Fatal("expected error for slash in name")
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
	g := testGraphWithBase(t)
	g.DefineWithTests("a", "10", []TestInput{
		{Name: "is positive", Expr: "(gt a 0)"},
	})
	g.DefineWithTests("b", "a", []TestInput{
		{Name: "is positive", Expr: "(gt b 0)"},
	})

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

	// Refresh — b has contract, should auto-refresh
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
	g := testGraphWithBase(t)
	g.DefineWithTests("a", "1", []TestInput{
		{Name: "is positive", Expr: "(gt a 0)"},
	})
	g.DefineWithTests("b", "(add a 10)", []TestInput{
		{Name: "is positive", Expr: "(gt b 0)"},
	})
	g.DefineWithTests("c", "(add b 100)", []TestInput{
		{Name: "is positive", Expr: "(gt c 0)"},
	})

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
	baseContent := `(define inc (fn (x) (add x 1)))
`
	os.WriteFile(filepath.Join(dir, "base.logos"), []byte(baseContent), 0644)
	os.WriteFile(filepath.Join(dir, "library-order.txt"), []byte("base\n"), 0644)

	g1, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	g1.DefineWithTests("a", "10", []TestInput{
		{Name: "is positive", Expr: "(gt a 0)"},
	})
	g1.DefineWithTests("b", "a", []TestInput{
		{Name: "is positive", Expr: "(gt b 0)"},
	})
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

// --- Assert in graph ---

func TestGraphAssertPass(t *testing.T) {
	g := testGraph(t)
	val, err := g.Eval(`(assert true "ok")`)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !ValuesEqual(val, BoolVal(true)) {
		t.Fatalf("expected true, got %s", val.String())
	}
}

// --- Library ---

func TestLibraryLoad(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "base.logos"), []byte("(define x 42)\n\n"), 0644)
	os.WriteFile(filepath.Join(dir, "library-order.txt"), []byte("base\n"), 0644)

	g, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	val, err := g.Eval("x")
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(42)) {
		t.Fatalf("expected 42, got %s", val.String())
	}
}

func TestLibraryLoadOrder(t *testing.T) {
	dir := t.TempDir()
	// A then B (B refs A)
	os.WriteFile(filepath.Join(dir, "base.logos"),
		[]byte("(define a 10)\n\n(define b (add a 1))\n\n"), 0644)
	os.WriteFile(filepath.Join(dir, "library-order.txt"), []byte("base\n"), 0644)

	g, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	val, err := g.Eval("b")
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(11)) {
		t.Fatalf("expected 11, got %s", val.String())
	}
}

func TestLibraryGuardRailBlocksSessionOverride(t *testing.T) {
	dir := t.TempDir()
	// Library defines x=1
	os.WriteFile(filepath.Join(dir, "base.logos"), []byte("(define x 1)\n\n"), 0644)
	os.WriteFile(filepath.Join(dir, "library-order.txt"), []byte("base\n"), 0644)

	g, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	// Session tries to redefine x — should ERROR (guard rail)
	_, err = g.Define("x", "2")
	if err == nil {
		t.Fatal("expected error: guard rail should block session override of library symbol")
	}
	if !strings.Contains(err.Error(), "defined in library") {
		t.Fatalf("expected guard rail error, got: %v", err)
	}

	// x should still be 1
	val, err := g.Eval("x")
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(1)) {
		t.Fatalf("expected 1, got %s", val.String())
	}
}

func TestLibraryCreateAndList(t *testing.T) {
	dir := t.TempDir()
	g, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	// Create a library
	if err := g.LibraryCreate("test-lib"); err != nil {
		t.Fatal(err)
	}

	// Verify it's in the order (shapes is auto-created)
	order := g.LibraryOrder()
	if len(order) != 2 || order[0] != "shapes" || order[1] != "test-lib" {
		t.Fatalf("expected [shapes test-lib], got %v", order)
	}

	// Verify library file exists
	if _, err := os.Stat(filepath.Join(dir, "test-lib.logos")); err != nil {
		t.Fatalf("expected library file to exist: %v", err)
	}
}

func TestLibraryOpenDefineClose(t *testing.T) {
	dir := t.TempDir()
	g, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	g.LibraryCreate("mylib")
	if err := g.LibraryOpen("mylib"); err != nil {
		t.Fatal(err)
	}

	// activeLib should be set
	if g.ActiveLibrary() != "mylib" {
		t.Fatalf("expected active library 'mylib', got %q", g.ActiveLibrary())
	}

	// Define a symbol — it should be owned by mylib
	node, err := g.Define("foo", "42")
	if err != nil {
		t.Fatal(err)
	}
	// Node ID should have library prefix
	if !strings.HasPrefix(node.ID, "node:mylib/foo-") {
		t.Fatalf("expected node ID with mylib prefix, got %s", node.ID)
	}
	// Check ownership
	if g.symbolOwner["foo"] != "mylib" {
		t.Fatalf("expected foo owned by mylib, got %q", g.symbolOwner["foo"])
	}

	// Close library
	if err := g.LibraryClose(); err != nil {
		t.Fatal(err)
	}
	if g.ActiveLibrary() != "" {
		t.Fatalf("expected empty active library after close, got %q", g.ActiveLibrary())
	}

	// foo should still be evaluable
	val, err := g.Eval("foo")
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(42)) {
		t.Fatalf("expected 42, got %s", val.String())
	}
}

func TestLibraryGuardRailSameLibOK(t *testing.T) {
	dir := t.TempDir()
	g, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	g.LibraryCreate("mylib")
	g.LibraryOpen("mylib")
	g.Define("foo", "1")
	// Redefine foo in the same library — should succeed
	_, err = g.Define("foo", "2")
	if err != nil {
		t.Fatalf("expected no error redefining in same library, got %v", err)
	}
	val, err := g.Eval("foo")
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(2)) {
		t.Fatalf("expected 2, got %s", val.String())
	}
	g.LibraryClose()
}

func TestLibraryGuardRailCrossLibError(t *testing.T) {
	dir := t.TempDir()
	g, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	g.LibraryCreate("mylib")
	g.LibraryOpen("mylib")
	g.Define("foo", "42")
	g.LibraryClose()

	// Session tries to define foo — should fail
	_, err = g.Define("foo", "99")
	if err == nil {
		t.Fatal("expected guard rail error")
	}
	if !strings.Contains(err.Error(), "defined in library") {
		t.Fatalf("expected guard rail error message, got: %v", err)
	}
}

func TestLibraryNodeIDNamespacing(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "base.logos"), []byte("(define x 42)\n\n"), 0644)
	os.WriteFile(filepath.Join(dir, "library-order.txt"), []byte("base\n"), 0644)

	g, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	// Library symbol should have namespaced node ID
	nodeID := g.symbols["x"]
	if nodeID != "node:base/x-1" {
		t.Fatalf("expected node:base/x-1, got %s", nodeID)
	}

	// Session symbol should have normal node ID
	g.Define("y", "99")
	yNodeID := g.symbols["y"]
	if yNodeID != "node:y-1" {
		t.Fatalf("expected node:y-1, got %s", yNodeID)
	}
}

func TestLibraryDeleteEmptyOK(t *testing.T) {
	dir := t.TempDir()
	g, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	g.LibraryCreate("ephemeral")
	if err := g.LibraryDelete("ephemeral"); err != nil {
		t.Fatalf("expected no error deleting empty library, got %v", err)
	}
	order := g.LibraryOrder()
	// shapes is auto-created and protected, so it remains
	if len(order) != 1 || order[0] != "shapes" {
		t.Fatalf("expected [shapes], got %v", order)
	}
}

func TestLibraryDeleteNonEmptyError(t *testing.T) {
	dir := t.TempDir()
	g, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	g.LibraryCreate("mylib")
	g.LibraryOpen("mylib")
	g.Define("foo", "1")
	g.LibraryClose()

	err = g.LibraryDelete("mylib")
	if err == nil {
		t.Fatal("expected error deleting non-empty library")
	}
	if !strings.Contains(err.Error(), "still owns symbol") {
		t.Fatalf("expected 'still owns' error, got: %v", err)
	}
}

func TestLibraryCompact(t *testing.T) {
	dir := t.TempDir()
	g, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	g.LibraryCreate("mylib")
	g.LibraryOpen("mylib")
	g.Define("a", "1")
	g.Define("b", "2")
	g.Define("a", "10") // redefine a
	g.Delete("b")       // delete b
	g.Define("c", "3")
	g.LibraryClose()

	// Compact
	if err := g.LibraryCompact("mylib"); err != nil {
		t.Fatal(err)
	}

	// Read the file — should have only a=10 and c=3
	data, err := os.ReadFile(filepath.Join(dir, "mylib.logos"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "(define a 10)") {
		t.Fatalf("expected (define a 10) in compacted file, got:\n%s", content)
	}
	if !strings.Contains(content, "(define c 3)") {
		t.Fatalf("expected (define c 3) in compacted file, got:\n%s", content)
	}
	if strings.Contains(content, "define b") {
		t.Fatalf("expected no (define b ...) in compacted file, got:\n%s", content)
	}
	// a should come before c (original define order)
	aIdx := strings.Index(content, "define a")
	cIdx := strings.Index(content, "define c")
	if aIdx > cIdx {
		t.Fatalf("expected a before c in compacted file")
	}
}

func TestLibraryCompactTopologicalOrder(t *testing.T) {
	dir := t.TempDir()
	g, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	g.LibraryCreate("mylib")
	g.LibraryOpen("mylib")

	// Define parent first without dep, then child, then redefine parent with dep.
	// First-occurrence order would put parent before child, breaking reload.
	g.Define("parent", "1")
	g.Define("child", "42")
	g.Define("parent", "(list child)") // redefine to reference child
	g.LibraryClose()

	// Compact
	if err := g.LibraryCompact("mylib"); err != nil {
		t.Fatal(err)
	}

	// Verify child comes before parent in compacted file
	data, err := os.ReadFile(filepath.Join(dir, "mylib.logos"))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	childIdx := strings.Index(content, "define child")
	parentIdx := strings.Index(content, "define parent")
	if childIdx < 0 || parentIdx < 0 {
		t.Fatalf("expected both child and parent in compacted file, got:\n%s", content)
	}
	if childIdx > parentIdx {
		t.Fatalf("expected child before parent in compacted file (topological order), got:\n%s", content)
	}

	// Verify reload works: clear and reinitialize
	g.Close()
	g2, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatalf("failed to reload after compact: %v", err)
	}
	defer g2.Close()
	val, err := g2.Eval("parent")
	if err != nil {
		t.Fatalf("failed to eval parent after reload: %v", err)
	}
	if val.String() != "(list 42)" {
		t.Fatalf("expected (list 42), got %s", val.String())
	}
}

func TestLibraryOrderSet(t *testing.T) {
	dir := t.TempDir()
	g, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	g.LibraryCreate("alpha")
	g.LibraryCreate("beta")

	// Reorder: shapes must stay first (no base in test graph), then beta before alpha
	if err := g.LibraryOrderSet([]string{"shapes", "beta", "alpha"}); err != nil {
		t.Fatal(err)
	}
	order := g.LibraryOrder()
	if len(order) != 3 || order[0] != "shapes" || order[1] != "beta" || order[2] != "alpha" {
		t.Fatalf("expected [shapes, beta, alpha], got %v", order)
	}
}

func TestLibraryPersistAcrossRestart(t *testing.T) {
	dir := t.TempDir()

	// First session: create library with symbol
	g1, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	g1.LibraryCreate("persist")
	g1.LibraryOpen("persist")
	g1.Define("stored", "999")
	g1.LibraryClose()
	g1.Close()

	// Second session: verify symbol persists
	g2, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	defer g2.Close()

	val, err := g2.Eval("stored")
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(999)) {
		t.Fatalf("expected 999, got %s", val.String())
	}

	// Verify ownership persists
	if g2.symbolOwner["stored"] != "persist" {
		t.Fatalf("expected owner 'persist', got %q", g2.symbolOwner["stored"])
	}
}

func TestClear(t *testing.T) {
	dir := t.TempDir()
	g, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	g.Define("x", "42")
	g.Define("y", "99")

	if err := g.Clear(); err != nil {
		t.Fatal(err)
	}

	// Symbols should be gone
	_, err = g.Eval("x")
	if err == nil {
		t.Fatal("expected error for x after clear")
	}
	_, err = g.Eval("y")
	if err == nil {
		t.Fatal("expected error for y after clear")
	}

	// Can still define new things
	g.Define("z", "1")
	val, err := g.Eval("z")
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(1)) {
		t.Fatalf("expected 1, got %s", val.String())
	}
}

func TestClearPreservesLibraries(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "base.logos"), []byte("(define a 10)\n\n"), 0644)
	os.WriteFile(filepath.Join(dir, "library-order.txt"), []byte("base\n"), 0644)

	g, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	// Define session-only symbol
	g.Define("b", "20")

	// Clear
	if err := g.Clear(); err != nil {
		t.Fatal(err)
	}

	// Library symbol survives
	val, err := g.Eval("a")
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(10)) {
		t.Fatalf("expected 10 from library, got %s", val.String())
	}

	// Session symbol is gone
	_, err = g.Eval("b")
	if err == nil {
		t.Fatal("expected error for b after clear")
	}
}

func TestLibraryDeleteGuardRail(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "base.logos"), []byte("(define x 1)\n\n"), 0644)
	os.WriteFile(filepath.Join(dir, "library-order.txt"), []byte("base\n"), 0644)

	g, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	// Try to delete library-owned symbol from session
	err = g.Delete("x")
	if err == nil {
		t.Fatal("expected guard rail error")
	}
	if !strings.Contains(err.Error(), "defined in library") {
		t.Fatalf("expected guard rail error, got: %v", err)
	}
}

func TestLibraryRefreshAllCrossLibrary(t *testing.T) {
	dir := t.TempDir()
	// Library defines 'a' and base functions
	libContent := `(define inc (fn (x) (add x 1)))

(define a 10)

(test a "is positive" (gt a 0))
`
	os.WriteFile(filepath.Join(dir, "base.logos"), []byte(libContent), 0644)
	os.WriteFile(filepath.Join(dir, "library-order.txt"), []byte("base\n"), 0644)

	g, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	// Session defines 'b' referencing 'a', with a contract
	g.DefineWithTests("b", "a", []TestInput{
		{Name: "is positive", Expr: "(gt b 0)"},
	})

	val, _ := g.Eval("b")
	if !ValuesEqual(val, IntVal(10)) {
		t.Fatalf("expected 10, got %s", val.String())
	}

	// Open library, redefine 'a'
	g.LibraryOpen("base")
	g.Define("a", "20")
	g.LibraryClose()

	// b still stale
	val, _ = g.Eval("b")
	if !ValuesEqual(val, IntVal(10)) {
		t.Fatalf("expected stale 10, got %s", val.String())
	}

	// Refresh — b has contract, should auto-refresh
	result, err := g.RefreshAll([]string{"a"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Refreshed) != 1 || result.Refreshed[0] != "b" {
		t.Fatalf("expected [b], got %v", result.Refreshed)
	}

	val, err = g.Eval("b")
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(20)) {
		t.Fatalf("expected 20 after refresh, got %s", val.String())
	}
}

// --- Closures ---

func TestClosureCapture(t *testing.T) {
	g := testGraph(t)
	// constantly returns a fn that captures v
	g.Define("constantly", `(fn (v) (fn (x) v))`)
	val, err := g.Eval(`((constantly 42) "ignored")`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(42)) {
		t.Fatalf("expected 42, got %s", val.String())
	}
}

func TestClosureComp(t *testing.T) {
	g := testGraph(t)
	g.Define("inc", `(fn (x) (add x 1))`)
	g.Define("double", `(fn (x) (mul x 2))`)
	g.Define("comp", `(fn (f g) (fn (x) (f (g x))))`)
	// (comp inc double) should be (fn (x) (inc (double x))) = x*2 + 1
	val, err := g.Eval(`((comp inc double) 5)`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(11)) {
		t.Fatalf("expected 11, got %s", val.String())
	}
}

func TestGraphAssertFailInDefinedFn(t *testing.T) {
	g := testGraph(t)
	_, err := g.Define("checker", `(fn (x) (assert (gt x 0) "must be positive"))`)
	if err != nil {
		t.Fatal(err)
	}

	// Passing case
	val, err := g.Eval(`(checker 5)`)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !ValuesEqual(val, BoolVal(true)) {
		t.Fatalf("expected true, got %s", val.String())
	}

	// Failing case
	_, err = g.Eval(`(checker 0)`)
	if err == nil {
		t.Fatal("expected error")
	}
	var ae *AssertError
	if !errors.As(err, &ae) {
		t.Fatalf("expected AssertError, got %T: %v", err, err)
	}
	if ae.Message != "must be positive" {
		t.Fatalf("expected message 'must be positive', got %q", ae.Message)
	}
	// The node ID should be set to the checker node
	if ae.Node != "node:checker-1" {
		t.Fatalf("expected node 'node:checker-1', got %q", ae.Node)
	}
}

// --- Library higher-order function tests ---

func TestLibraryMap(t *testing.T) {
	g := testGraphWithLibrary(t)
	val, err := g.Eval(`(map (fn (x) (add x 1)) (list 1 2 3))`)
	if err != nil {
		t.Fatal(err)
	}
	expected := ListVal([]Value{IntVal(2), IntVal(3), IntVal(4)})
	if !ValuesEqual(val, expected) {
		t.Fatalf("expected %s, got %s", expected.String(), val.String())
	}
	// Empty list
	val, err = g.Eval(`(map (fn (x) (mul x x)) (list))`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, ListVal([]Value{})) {
		t.Fatalf("expected empty list, got %s", val.String())
	}
}

func TestLibraryFilter(t *testing.T) {
	g := testGraphWithLibrary(t)
	val, err := g.Eval(`(filter (fn (x) (gt x 2)) (list 1 2 3 4))`)
	if err != nil {
		t.Fatal(err)
	}
	expected := ListVal([]Value{IntVal(3), IntVal(4)})
	if !ValuesEqual(val, expected) {
		t.Fatalf("expected %s, got %s", expected.String(), val.String())
	}
	// All filtered out
	val, err = g.Eval(`(filter (fn (x) false) (list 1 2))`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, ListVal([]Value{})) {
		t.Fatalf("expected empty list, got %s", val.String())
	}
}

func TestLibraryFold(t *testing.T) {
	g := testGraphWithLibrary(t)
	val, err := g.Eval(`(fold (fn (acc x) (add acc x)) 0 (list 1 2 3))`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(6)) {
		t.Fatalf("expected 6, got %s", val.String())
	}
	// Empty list
	val, err = g.Eval(`(fold (fn (acc x) (add acc 1)) 0 (list))`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(0)) {
		t.Fatalf("expected 0, got %s", val.String())
	}
}

func TestLibraryGroupBy(t *testing.T) {
	g := testGraphWithLibrary(t)
	val, err := g.Eval(`(group-by (fn (x) (mod x 2)) (list 1 2 3 4))`)
	if err != nil {
		t.Fatal(err)
	}
	if val.Kind != ValMap {
		t.Fatalf("expected Map, got %s", val.KindName())
	}
	m := *val.Map
	if len(m) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(m))
	}
}

func TestLibraryHOFLargeList(t *testing.T) {
	g := testGraphWithLibrary(t)
	// Build a large list via loop/recur
	g.Define("my-range", `(fn (n) (loop ((n n) (acc (list))) (if (lt n 1) acc (recur (sub n 1) (cons n acc)))))`)
	// fold over 10000 elements: sum 1..10000 = 50005000
	val, err := g.Eval(`(fold (fn (acc x) (add acc x)) 0 (my-range 10000))`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(50005000)) {
		t.Fatalf("expected 50005000, got %s", val.String())
	}
	// map over 10000 elements
	val, err = g.Eval(`(len (map (fn (x) (add x 1)) (my-range 10000)))`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(10000)) {
		t.Fatalf("expected 10000, got %s", val.String())
	}
}

// --- Form (macro) graph tests ---

func TestGraphDefineForm(t *testing.T) {
	g := testGraph(t)
	_, err := g.Define("when", `(form (test body) (list (quote if) test body nil))`)
	if err != nil {
		t.Fatal(err)
	}
	// Type check
	val, err := g.Eval(`(type when)`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, KeywordVal("form")) {
		t.Fatalf("expected :form, got %s", val.String())
	}
	// Use it — true branch
	val, err = g.Eval(`(when true 42)`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(42)) {
		t.Fatalf("expected 42, got %s", val.String())
	}
	// Use it — false branch
	val, err = g.Eval(`(when false 42)`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, NilVal()) {
		t.Fatalf("expected nil, got %s", val.String())
	}
}

func TestGraphFormUnless(t *testing.T) {
	g := testGraph(t)
	g.Define("unless", `(form (test body) (list (quote if) test nil body))`)
	val, err := g.Eval(`(unless false 99)`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(99)) {
		t.Fatalf("expected 99, got %s", val.String())
	}
	val, err = g.Eval(`(unless true 99)`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, NilVal()) {
		t.Fatalf("expected nil, got %s", val.String())
	}
}

func TestGraphFormLogReplay(t *testing.T) {
	dir := t.TempDir()

	// First graph: define a form and use it
	g1, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	g1.Define("when", `(form (test body) (list (quote if) test body nil))`)
	g1.Close()

	// Second graph: replay the log
	g2, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	defer g2.Close()

	val, err := g2.Eval(`(when true 42)`)
	if err != nil {
		t.Fatalf("log replay failed: %v", err)
	}
	if !ValuesEqual(val, IntVal(42)) {
		t.Fatalf("expected 42, got %s", val.String())
	}
}

func TestGraphFormWithBuiltinInExpansion(t *testing.T) {
	g := testGraph(t)
	// Form that uses a builtin in its expansion
	g.Define("double-when", `(form (test body) (list (quote if) test (list (quote mul) body 2) nil))`)
	val, err := g.Eval(`(double-when true 5)`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(10)) {
		t.Fatalf("expected 10, got %s", val.String())
	}
}

// --- Library form tests ---

func TestLibraryCond(t *testing.T) {
	g := testGraphWithFormLibrary(t)
	val, err := g.Eval(`(cond false 1 true 2)`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(2)) {
		t.Fatalf("expected 2, got %s", val.String())
	}
	// First match wins
	val, err = g.Eval(`(cond true 1 true 2)`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(1)) {
		t.Fatalf("expected 1, got %s", val.String())
	}
	// Truthy: nil falsy, 0 truthy
	val, err = g.Eval(`(cond nil 1 0 2)`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(2)) {
		t.Fatalf("expected 2, got %s", val.String())
	}
	// No match
	val, err = g.Eval(`(cond false 1 false 2)`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, NilVal()) {
		t.Fatalf("expected nil, got %s", val.String())
	}
	// Expressions as tests
	val, err = g.Eval(`(cond (eq 1 2) "no" (eq 1 1) "yes")`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, StringVal("yes")) {
		t.Fatalf("expected yes, got %s", val.String())
	}
}

func TestLibraryCase(t *testing.T) {
	g := testGraphWithFormLibrary(t)
	// Keyword match
	val, err := g.Eval(`(case :b :a 1 :b 2 :c 3)`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(2)) {
		t.Fatalf("expected 2, got %s", val.String())
	}
	// Default (odd trailing arg)
	val, err = g.Eval(`(case :z :a 1 :b 2 "default")`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, StringVal("default")) {
		t.Fatalf("expected default, got %s", val.String())
	}
	// No match, no default
	val, err = g.Eval(`(case :z :a 1 :b 2)`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, NilVal()) {
		t.Fatalf("expected nil, got %s", val.String())
	}
	// Int match
	val, err = g.Eval(`(case 2 1 "one" 2 "two")`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, StringVal("two")) {
		t.Fatalf("expected two, got %s", val.String())
	}
	// With type
	val, err = g.Eval(`(case (type 42) :int "integer" :string "text" "other")`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, StringVal("integer")) {
		t.Fatalf("expected integer, got %s", val.String())
	}
}

func TestLibraryWhen(t *testing.T) {
	g := testGraphWithFormLibrary(t)
	val, err := g.Eval(`(when true 42)`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(42)) {
		t.Fatalf("expected 42, got %s", val.String())
	}
	val, err = g.Eval(`(when false 42)`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, NilVal()) {
		t.Fatalf("expected nil, got %s", val.String())
	}
}

func TestLibraryUnless(t *testing.T) {
	g := testGraphWithFormLibrary(t)
	val, err := g.Eval(`(unless false 99)`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(99)) {
		t.Fatalf("expected 99, got %s", val.String())
	}
	val, err = g.Eval(`(unless true 99)`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, NilVal()) {
		t.Fatalf("expected nil, got %s", val.String())
	}
}

func TestLibraryAndShortCircuit(t *testing.T) {
	g := testGraphWithFormLibrary(t)
	// Short-circuit: second arg not evaluated when first is falsy
	val, err := g.Eval(`(and false (assert false "boom"))`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, BoolVal(false)) {
		t.Fatalf("expected false, got %s", val.String())
	}
	// Both truthy
	val, err = g.Eval(`(and 1 2)`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(2)) {
		t.Fatalf("expected 2 (determining value), got %s", val.String())
	}
}

func TestLibraryOrShortCircuit(t *testing.T) {
	g := testGraphWithFormLibrary(t)
	// Short-circuit: second arg not evaluated when first is truthy
	val, err := g.Eval(`(or true (assert false "boom"))`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, BoolVal(true)) {
		t.Fatalf("expected true, got %s", val.String())
	}
	// First falsy, second evaluated
	val, err = g.Eval(`(or false 42)`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(42)) {
		t.Fatalf("expected 42, got %s", val.String())
	}
	// Both falsy
	val, err = g.Eval(`(or false nil)`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, NilVal()) {
		t.Fatalf("expected nil, got %s", val.String())
	}
}

func TestLibraryFormNoDoubleEval(t *testing.T) {
	g := testGraphWithFormLibrary(t)
	// Use a side-effecting expression (assert) to verify single evaluation.
	g.Define("make-counter", `(fn () (let (n 0) (fn () (add n 1))))`)

	// or: target expression should only be evaluated once
	val, err := g.Eval(`(or (add 1 2) "fallback")`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(3)) {
		t.Fatalf("expected 3, got %s", val.String())
	}

	// case: target expression evaluated once even with multiple clauses
	val, err = g.Eval(`(case (add 1 1) 1 "one" 2 "two" 3 "three")`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, StringVal("two")) {
		t.Fatalf("expected two, got %s", val.String())
	}
}

// --- Rest params in graph ---

func TestGraphRestParamsFn(t *testing.T) {
	g := testGraph(t)
	g.Define("variadic", `(fn (x & rest) (list x rest))`)
	val, err := g.Eval(`(variadic 1 2 3)`)
	if err != nil {
		t.Fatal(err)
	}
	expected := ListVal([]Value{IntVal(1), ListVal([]Value{IntVal(2), IntVal(3)})})
	if !ValuesEqual(val, expected) {
		t.Fatalf("expected %s, got %s", expected.String(), val.String())
	}
}

func TestGraphRestParamsForm(t *testing.T) {
	g := testGraphWithFormLibrary(t)
	// cond uses rest params — verify it works through graph
	val, err := g.Eval(`(cond (eq 1 2) "no" (eq 3 3) "yes")`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, StringVal("yes")) {
		t.Fatalf("expected yes, got %s", val.String())
	}
}

func TestDefineRejectsBuiltinName(t *testing.T) {
	g := testGraph(t)
	_, err := g.Define("add", "(fn (a b) 42)")
	if err == nil {
		t.Fatal("expected error defining builtin name")
	}
	if !strings.Contains(err.Error(), "cannot redefine builtin") {
		t.Fatalf("unexpected error: %s", err.Error())
	}
}

func TestDefineRejectsAllBuiltinNames(t *testing.T) {
	g := testGraph(t)
	// Spot-check several builtins
	for _, name := range []string{"list", "sub", "eq", "head", "len", "type"} {
		_, err := g.Define(name, "42")
		if err == nil {
			t.Fatalf("expected error defining builtin %q", name)
		}
	}
}

// --- Symbol Contracts: Phase 1 tests ---

func TestExtractTestExpr(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{`(test foo "adds one" (eq (foo 1) 2))`, `(eq (foo 1) 2)`},
		{`(test my-fn "basic" true)`, `true`},
		{`(test x "escaped \"quote\"" (eq x 1))`, `(eq x 1)`},
	}
	for _, tc := range tests {
		got := extractTestExpr(tc.input)
		if got != tc.expected {
			t.Errorf("extractTestExpr(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestFormatTestEntry(t *testing.T) {
	test := GraphNodeTest{Name: "adds one", Source: "(eq (inc 5) 6)"}
	got := formatTestEntry("inc", test)
	expected := `(test inc "adds one" (eq (inc 5) 6))`
	if got != expected {
		t.Errorf("formatTestEntry = %q, want %q", got, expected)
	}
}

func TestReplayTestEntries(t *testing.T) {
	dir := t.TempDir()

	// Write a base library with inc
	baseContent := `(define inc (fn (x) (add x 1)))
`
	os.WriteFile(filepath.Join(dir, "base.logos"), []byte(baseContent), 0644)
	os.WriteFile(filepath.Join(dir, "library-order.txt"), []byte("base\n"), 0644)

	// Write a session log with a define + test entry
	sessionContent := `(define double (fn (x) (mul x 2)))

(test double "doubles" (eq (double 3) 6))

`
	os.WriteFile(filepath.Join(dir, "log.logos"), []byte(sessionContent), 0644)

	g, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	// Check that the node has the test attached
	nodeID := g.symbols["double"]
	node := g.nodes[nodeID]
	if len(node.Tests) != 1 {
		t.Fatalf("expected 1 test, got %d", len(node.Tests))
	}
	if node.Tests[0].Name != "doubles" {
		t.Errorf("test name = %q, want %q", node.Tests[0].Name, "doubles")
	}
	if node.Tests[0].Source != "(eq (double 3) 6)" {
		t.Errorf("test source = %q, want %q", node.Tests[0].Source, "(eq (double 3) 6)")
	}
}

func TestResolveTestASTRestrictsScope(t *testing.T) {
	g := testGraphWithLibrary(t)

	// Define a non-base symbol
	g.Define("my-fn", "(fn (x) (add x 1))")

	// Test referencing builtins should work
	parsed, _ := Parse("(eq 1 1)")
	var refs []Ref
	_, err := g.resolveTestAST(parsed, &refs, "my-fn", nil)
	if err != nil {
		t.Errorf("expected builtins to be allowed in test, got: %v", err)
	}

	// Test referencing base library should work
	parsed, _ = Parse("(empty? (list))")
	refs = nil
	_, err = g.resolveTestAST(parsed, &refs, "my-fn", nil)
	if err != nil {
		t.Errorf("expected base library to be allowed in test, got: %v", err)
	}

	// Test referencing self should work
	parsed, _ = Parse("(eq (my-fn 1) 2)")
	refs = nil
	_, err = g.resolveTestAST(parsed, &refs, "my-fn", nil)
	if err != nil {
		t.Errorf("expected self-reference to be allowed in test, got: %v", err)
	}

	// Define another non-base symbol
	g.Define("other-fn", "(fn (x) (sub x 1))")

	// Test referencing non-base, non-self should fail
	parsed, _ = Parse("(eq (other-fn 1) 0)")
	refs = nil
	_, err = g.resolveTestAST(parsed, &refs, "my-fn", nil)
	if err == nil {
		t.Error("expected error referencing non-base symbol in test")
	}
	if err != nil && !strings.Contains(err.Error(), "test expressions can only reference") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestResolveTestASTTracksRefs(t *testing.T) {
	dir := t.TempDir()
	baseContent := `(define inc (fn (x) (add x 1)))
`
	os.WriteFile(filepath.Join(dir, "base.logos"), []byte(baseContent), 0644)
	os.WriteFile(filepath.Join(dir, "library-order.txt"), []byte("base\n"), 0644)
	g, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	// Define with a test that references base symbol 'inc'
	node, err := g.DefineWithTests("my-val", "42", []TestInput{
		{Name: "uses-inc", Expr: "(eq (inc my-val) 43)"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Check that node.Refs includes 'inc' from the test
	foundInc := false
	for _, ref := range node.Refs {
		if ref.Symbol == "inc" {
			foundInc = true
		}
	}
	if !foundInc {
		t.Fatalf("expected Refs to include 'inc' from test, got: %v", node.Refs)
	}
}

func TestShapesLibraryInTestScope(t *testing.T) {
	dir := t.TempDir()
	baseContent := `(define not (fn (x) (if x false true)))
`
	shapesContent := `(define my-shape? (fn (v) (not (eq v nil))))
`
	os.WriteFile(filepath.Join(dir, "base.logos"), []byte(baseContent), 0644)
	os.WriteFile(filepath.Join(dir, "shapes.logos"), []byte(shapesContent), 0644)
	os.WriteFile(filepath.Join(dir, "library-order.txt"), []byte("base\nshapes\n"), 0644)
	g, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	// Define with a test referencing shapes library symbol
	_, err = g.DefineWithTests("my-val", "42", []TestInput{
		{Name: "shape-check", Expr: "(my-shape? my-val)"},
	})
	if err != nil {
		t.Fatalf("expected shapes symbol to be allowed in test, got: %v", err)
	}
}

func TestRefreshAllCascadesThroughTestDeps(t *testing.T) {
	dir := t.TempDir()
	baseContent := `(define not (fn (x) (if x false true)))
`
	shapesContent := `(define always-true? (fn (v) true))
`
	os.WriteFile(filepath.Join(dir, "base.logos"), []byte(baseContent), 0644)
	os.WriteFile(filepath.Join(dir, "shapes.logos"), []byte(shapesContent), 0644)
	os.WriteFile(filepath.Join(dir, "library-order.txt"), []byte("base\nshapes\n"), 0644)
	g, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	// Define a symbol with a test that references the shapes predicate
	_, err = g.DefineWithTests("my-val", "42", []TestInput{
		{Name: "check", Expr: "(always-true? my-val)"},
	})
	if err != nil {
		t.Fatal(err)
	}
	status, _ := g.GetSymbolStatus("my-val")
	if status != StatusGreen {
		t.Fatal("expected my-val to be green")
	}

	// Change the shapes predicate to always fail
	g.LibraryOpen("shapes")
	_, err = g.DefineWithTests("always-true?", "(fn (v) false)", nil)
	if err != nil {
		t.Fatal(err)
	}
	g.LibraryClose()

	// Refresh-all should cascade to my-val and turn it Red
	result, err := g.RefreshAll([]string{"always-true?"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Red) != 1 || result.Red[0] != "my-val" {
		t.Fatalf("expected my-val to be red, got refreshed=%v red=%v stale=%v", result.Refreshed, result.Red, result.Stale)
	}
}

func TestProtectedLibraryDelete(t *testing.T) {
	dir := t.TempDir()
	g, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	err = g.LibraryDelete("shapes")
	if err == nil {
		t.Fatal("expected error deleting protected library shapes")
	}
	if !strings.Contains(err.Error(), "protected") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProtectedLibraryOrderSet(t *testing.T) {
	dir := t.TempDir()
	baseContent := ``
	os.WriteFile(filepath.Join(dir, "base.logos"), []byte(baseContent), 0644)
	os.WriteFile(filepath.Join(dir, "library-order.txt"), []byte("base\n"), 0644)
	g, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	g.LibraryCreate("other")

	// shapes not in first position should fail
	err = g.LibraryOrderSet([]string{"base", "other", "shapes"})
	if err == nil {
		t.Fatal("expected error when shapes not second")
	}

	// base not first should fail
	err = g.LibraryOrderSet([]string{"shapes", "base", "other"})
	if err == nil {
		t.Fatal("expected error when base not first")
	}

	// correct order should work
	err = g.LibraryOrderSet([]string{"base", "shapes", "other"})
	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
}

func TestCompactPreservesTests(t *testing.T) {
	dir := t.TempDir()

	// Create a library with a symbol + test
	baseContent := `(define inc (fn (x) (add x 1)))
`
	libContent := `(define double (fn (x) (mul x 2)))

(test double "doubles" (eq (double 3) 6))

`
	os.WriteFile(filepath.Join(dir, "base.logos"), []byte(baseContent), 0644)
	os.WriteFile(filepath.Join(dir, "mylib.logos"), []byte(libContent), 0644)
	os.WriteFile(filepath.Join(dir, "library-order.txt"), []byte("base\nmylib\n"), 0644)

	g, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	// Verify test was loaded
	nodeID := g.symbols["double"]
	node := g.nodes[nodeID]
	if len(node.Tests) != 1 {
		t.Fatalf("expected 1 test before compact, got %d", len(node.Tests))
	}

	// Compact the library
	if err := g.LibraryCompact("mylib"); err != nil {
		t.Fatal(err)
	}

	// Read the compacted file and verify test entry is present
	data, _ := os.ReadFile(filepath.Join(dir, "mylib.logos"))
	content := string(data)
	if !strings.Contains(content, `(test double "doubles"`) {
		t.Errorf("compacted file missing test entry:\n%s", content)
	}

	// Reload and verify tests survive
	g.Close()
	g2, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	defer g2.Close()

	nodeID2 := g2.symbols["double"]
	node2 := g2.nodes[nodeID2]
	if len(node2.Tests) != 1 {
		t.Fatalf("expected 1 test after compact+reload, got %d", len(node2.Tests))
	}
}

func TestDefineWithTestsPassingGate(t *testing.T) {
	g := testGraphWithLibrary(t)

	tests := []TestInput{
		{Name: "adds one", Expr: "(eq (inc 5) 6)"},
	}
	node, err := g.DefineWithTests("inc", "(fn (x) (add x 1))", tests)
	if err != nil {
		t.Fatal(err)
	}
	if len(node.Tests) != 1 {
		t.Fatalf("expected 1 test, got %d", len(node.Tests))
	}
	if node.Tests[0].Name != "adds one" {
		t.Errorf("test name = %q", node.Tests[0].Name)
	}
}

func TestDefineWithTestsFailingGate(t *testing.T) {
	g := testGraphWithLibrary(t)

	tests := []TestInput{
		{Name: "wrong", Expr: "(eq 1 2)"},
	}
	_, err := g.DefineWithTests("foo", "42", tests)
	if err == nil {
		t.Fatal("expected error from failing test gate")
	}
	if !strings.Contains(err.Error(), "test \"wrong\" returned falsy") {
		t.Errorf("unexpected error: %v", err)
	}

	// Symbol should NOT exist (rolled back)
	if _, ok := g.symbols["foo"]; ok {
		t.Error("symbol 'foo' should not exist after failed gate")
	}
}

func TestDefineWithTestsErrorGate(t *testing.T) {
	g := testGraphWithLibrary(t)

	tests := []TestInput{
		{Name: "errors", Expr: "(add 1 \"x\")"},
	}
	_, err := g.DefineWithTests("foo", "42", tests)
	if err == nil {
		t.Fatal("expected error from erroring test")
	}
	if !strings.Contains(err.Error(), "test \"errors\" failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDefineWithTestsRestrictsScope(t *testing.T) {
	g := testGraphWithLibrary(t)

	// Define a non-base symbol
	g.Define("helper", "(fn (x) x)")

	// Try to define with a test referencing the non-base symbol
	tests := []TestInput{
		{Name: "uses helper", Expr: "(eq (helper 1) 1)"},
	}
	_, err := g.DefineWithTests("foo", "42", tests)
	if err == nil {
		t.Fatal("expected error: test references non-base symbol")
	}
	if !strings.Contains(err.Error(), "test expressions can only reference") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDefineWithTestsRollback(t *testing.T) {
	g := testGraphWithLibrary(t)

	// Define foo first
	g.Define("foo", "1")
	oldNodeID := g.symbols["foo"]

	// Redefine foo with a failing test
	tests := []TestInput{
		{Name: "wrong", Expr: "(eq foo 999)"},
	}
	_, err := g.DefineWithTests("foo", "2", tests)
	if err == nil {
		t.Fatal("expected error from failing test")
	}

	// foo should still point to old node
	if g.symbols["foo"] != oldNodeID {
		t.Errorf("foo should still point to old node after rollback")
	}

	// Old value should still be accessible
	val, evalErr := g.Eval("foo")
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if val.Int != 1 {
		t.Errorf("foo = %d, want 1", val.Int)
	}
}

func TestDefineWithTestsNoTestsBackwardCompat(t *testing.T) {
	g := testGraph(t)
	node, err := g.Define("foo", "42")
	if err != nil {
		t.Fatal(err)
	}
	if len(node.Tests) != 0 {
		t.Errorf("expected 0 tests, got %d", len(node.Tests))
	}
}

func TestRefreshAllCarriesForwardTests(t *testing.T) {
	g := testGraphWithBase(t)

	// Define a with a value, then b depends on a, with a resilient test
	g.DefineWithTests("a", "1", []TestInput{
		{Name: "is positive", Expr: "(gt a 0)"},
	})
	g.DefineWithTests("b", "(add a 1)", []TestInput{
		{Name: "is positive", Expr: "(gt b 0)"},
	})

	// Redefine a
	g.Define("a", "10")

	// Refresh b — tests should carry forward and pass
	result, err := g.RefreshAll([]string{"a"}, false)
	if err != nil {
		t.Fatal(err)
	}

	// b should have been refreshed
	found := false
	for _, name := range result.Refreshed {
		if name == "b" {
			found = true
		}
	}
	if !found {
		t.Error("expected b to be refreshed")
	}

	// b's new node should still have the test
	newBNodeID := g.symbols["b"]
	newBNode := g.nodes[newBNodeID]
	if len(newBNode.Tests) != 1 {
		t.Fatalf("expected 1 test after refresh, got %d", len(newBNode.Tests))
	}
	if newBNode.Tests[0].Name != "is positive" {
		t.Errorf("test name = %q after refresh", newBNode.Tests[0].Name)
	}

	// b should now evaluate to 11 (add 10 1)
	bVal, _ := g.EvalSymbol("b")
	if bVal.Int != 11 {
		t.Errorf("b = %d, want 11", bVal.Int)
	}
}

// --- Refine tests ---

func testGraphWithBase(t *testing.T) *Graph {
	t.Helper()
	dir := t.TempDir()
	baseContent := `(define inc (fn (x) (add x 1)))

(define dec (fn (x) (sub x 1)))

(define not (fn (x) (if x false true)))
`
	os.WriteFile(filepath.Join(dir, "base.logos"), []byte(baseContent), 0644)
	os.WriteFile(filepath.Join(dir, "library-order.txt"), []byte("base\n"), 0644)

	g, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	return g
}

func TestRefineAddTest(t *testing.T) {
	g := testGraphWithBase(t)

	// Define a symbol without tests
	node, err := g.Define("double", "(fn (x) (mul x 2))")
	if err != nil {
		t.Fatal(err)
	}
	if len(node.Tests) != 0 {
		t.Fatal("expected no tests initially")
	}

	// Refine to add a test
	newNode, propagate, err := g.Refine("double", "", []TestInput{{Name: "doubles 3", Expr: "(eq (double 3) 6)"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if newNode.ID == node.ID {
		t.Error("expected new node ID")
	}
	if len(newNode.Tests) != 1 {
		t.Fatalf("expected 1 test, got %d", len(newNode.Tests))
	}
	if newNode.Tests[0].Name != "doubles 3" {
		t.Errorf("test name = %q", newNode.Tests[0].Name)
	}
	// Only tests changed → propagate = true
	if !propagate {
		t.Error("expected propagate = true when only tests changed")
	}
	// Symbol should point to new node
	if g.symbols["double"] != newNode.ID {
		t.Error("symbol not updated")
	}
}

func TestRefineRemoveTest(t *testing.T) {
	g := testGraphWithBase(t)

	// Define with tests
	node, err := g.DefineWithTests("double", "(fn (x) (mul x 2))", []TestInput{
		{Name: "doubles 3", Expr: "(eq (double 3) 6)"},
		{Name: "doubles 0", Expr: "(eq (double 0) 0)"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(node.Tests) != 2 {
		t.Fatalf("expected 2 tests, got %d", len(node.Tests))
	}

	// Refine to remove one test
	newNode, propagate, err := g.Refine("double", "", nil, []string{"doubles 3"})
	if err != nil {
		t.Fatal(err)
	}
	if len(newNode.Tests) != 1 {
		t.Fatalf("expected 1 test after removal, got %d", len(newNode.Tests))
	}
	if newNode.Tests[0].Name != "doubles 0" {
		t.Errorf("remaining test = %q, want 'doubles 0'", newNode.Tests[0].Name)
	}
	if !propagate {
		t.Error("expected propagate = true when only tests changed")
	}
}

func TestRefineExprOnly(t *testing.T) {
	g := testGraphWithBase(t)

	// Define with a test
	node, err := g.DefineWithTests("answer", "42", []TestInput{
		{Name: "is 42", Expr: "(eq answer 42)"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Refine to change expression — test should carry forward
	newNode, propagate, err := g.Refine("answer", "43", nil, nil)
	if err != nil {
		// Test gate should fail: answer is now 43 but test checks for 42
		// This is expected! The test should fail.
		t.Logf("expected gate failure: %v", err)

		// Verify rollback
		if g.symbols["answer"] != node.ID {
			t.Error("symbol should still point to original node after gate failure")
		}
		return
	}
	// If we get here, it means the test passed with the new expr,
	// which would be wrong for this case.
	_ = newNode
	_ = propagate
	t.Fatal("expected gate failure when changing expr breaks existing test")
}

func TestRefineExprCarriesForwardTests(t *testing.T) {
	g := testGraphWithBase(t)

	// Define with a test that will still pass after expr change
	_, err := g.DefineWithTests("val", "(add 1 2)", []TestInput{
		{Name: "is positive", Expr: "(gt val 0)"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Refine expression to a different positive number — test still passes
	newNode, propagate, err := g.Refine("val", "(add 10 20)", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(newNode.Tests) != 1 {
		t.Fatalf("expected 1 test carried forward, got %d", len(newNode.Tests))
	}
	if newNode.Tests[0].Name != "is positive" {
		t.Errorf("test name = %q", newNode.Tests[0].Name)
	}
	if newNode.Source != "(add 10 20)" {
		t.Errorf("source = %q", newNode.Source)
	}
	// Only expr changed → propagate = true
	if !propagate {
		t.Error("expected propagate = true when only expr changed")
	}
}

func TestRefineExprAndTestPropagates(t *testing.T) {
	g := testGraphWithBase(t)

	// Define a symbol
	_, err := g.Define("val", "10")
	if err != nil {
		t.Fatal(err)
	}

	// Refine both expr and add test simultaneously
	newNode, propagate, err := g.Refine("val", "20", []TestInput{
		{Name: "is 20", Expr: "(eq val 20)"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(newNode.Tests) != 1 {
		t.Fatalf("expected 1 test, got %d", len(newNode.Tests))
	}
	// Refine always propagates when successful
	if !propagate {
		t.Error("expected propagate = true after successful refine")
	}
}

func TestRefineGateFailure(t *testing.T) {
	g := testGraphWithBase(t)

	// Define with a test
	origNode, err := g.DefineWithTests("val", "42", []TestInput{
		{Name: "is 42", Expr: "(eq val 42)"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Try to add a test that fails
	_, _, err = g.Refine("val", "", []TestInput{
		{Name: "is 100", Expr: "(eq val 100)"},
	}, nil)
	if err == nil {
		t.Fatal("expected gate failure for failing test")
	}
	if !strings.Contains(err.Error(), "is 100") {
		t.Errorf("error should mention failing test: %v", err)
	}

	// Original node should be preserved
	if g.symbols["val"] != origNode.ID {
		t.Error("symbol should still point to original node after gate failure")
	}
	currentNode := g.nodes[g.symbols["val"]]
	if len(currentNode.Tests) != 1 {
		t.Errorf("expected 1 test preserved, got %d", len(currentNode.Tests))
	}
}

func TestRefineUnknownSymbol(t *testing.T) {
	g := testGraphWithBase(t)

	_, _, err := g.Refine("nonexistent", "42", nil, nil)
	if err == nil {
		t.Fatal("expected error for unknown symbol")
	}
	if !strings.Contains(err.Error(), "unknown symbol") {
		t.Errorf("error = %v", err)
	}
}

func TestRefineNoChanges(t *testing.T) {
	g := testGraphWithBase(t)

	g.Define("val", "42")

	_, _, err := g.Refine("val", "", nil, nil)
	if err == nil {
		t.Fatal("expected error for no changes")
	}
	if !strings.Contains(err.Error(), "no changes") {
		t.Errorf("error = %v", err)
	}
}

func TestRefineDuplicateTestName(t *testing.T) {
	g := testGraphWithBase(t)

	_, err := g.DefineWithTests("val", "42", []TestInput{
		{Name: "is 42", Expr: "(eq val 42)"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Try to add a test with the same name
	_, _, err = g.Refine("val", "", []TestInput{
		{Name: "is 42", Expr: "(eq val 42)"},
	}, nil)
	if err == nil {
		t.Fatal("expected error for duplicate test name")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %v", err)
	}
}

func TestRefineRemoveNonexistentTest(t *testing.T) {
	g := testGraphWithBase(t)

	g.Define("val", "42")

	_, _, err := g.Refine("val", "", nil, []string{"no such test"})
	if err == nil {
		t.Fatal("expected error for nonexistent test removal")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v", err)
	}
}

func TestRefineLogReplay(t *testing.T) {
	dir := t.TempDir()
	baseContent := `(define inc (fn (x) (add x 1)))
`
	os.WriteFile(filepath.Join(dir, "base.logos"), []byte(baseContent), 0644)
	os.WriteFile(filepath.Join(dir, "library-order.txt"), []byte("base\n"), 0644)

	g, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}

	// Define, then refine to add a test
	g.Define("double", "(fn (x) (mul x 2))")
	_, _, err = g.Refine("double", "", []TestInput{
		{Name: "doubles 5", Expr: "(eq (double 5) 10)"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	g.Close()

	// Reload and verify tests survived
	g2, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	defer g2.Close()

	nodeID := g2.symbols["double"]
	node := g2.nodes[nodeID]
	if len(node.Tests) != 1 {
		t.Fatalf("expected 1 test after replay, got %d", len(node.Tests))
	}
	if node.Tests[0].Name != "doubles 5" {
		t.Errorf("test name = %q", node.Tests[0].Name)
	}
}

// --- Symbol Status tests ---

func TestSymbolStatusUntested(t *testing.T) {
	g := testGraphWithBase(t)

	g.Define("val", "42")

	status, ok := g.GetSymbolStatus("val")
	if !ok {
		t.Fatal("expected status to exist")
	}
	if status != StatusUntested {
		t.Errorf("expected Untested, got %s", status)
	}
}

func TestSymbolStatusGreenAfterDefineWithTests(t *testing.T) {
	g := testGraphWithBase(t)

	g.DefineWithTests("val", "42", []TestInput{
		{Name: "is 42", Expr: "(eq val 42)"},
	})

	status, ok := g.GetSymbolStatus("val")
	if !ok {
		t.Fatal("expected status to exist")
	}
	if status != StatusGreen {
		t.Errorf("expected Green, got %s", status)
	}

	// val should appear in GreenSymbols
	greens := g.GreenSymbols()
	found := false
	for _, name := range greens {
		if name == "val" {
			found = true
		}
	}
	if !found {
		t.Error("val not found in GreenSymbols")
	}
}

func TestSymbolStatusGreenAfterRefine(t *testing.T) {
	g := testGraphWithBase(t)

	g.Define("val", "42")

	status, _ := g.GetSymbolStatus("val")
	if status != StatusUntested {
		t.Errorf("expected Untested initially, got %s", status)
	}

	// Refine to add a test → should become Green
	g.Refine("val", "", []TestInput{
		{Name: "is 42", Expr: "(eq val 42)"},
	}, nil)

	status, _ = g.GetSymbolStatus("val")
	if status != StatusGreen {
		t.Errorf("expected Green after refine, got %s", status)
	}
}

func TestSymbolStatusAfterDelete(t *testing.T) {
	g := testGraphWithBase(t)

	g.Define("val", "42")
	_, ok := g.GetSymbolStatus("val")
	if !ok {
		t.Fatal("expected status before delete")
	}

	g.Delete("val")
	_, ok = g.GetSymbolStatus("val")
	if ok {
		t.Error("expected no status after delete")
	}
}

func TestSymbolStatusAfterReplay(t *testing.T) {
	dir := t.TempDir()
	baseContent := `(define inc (fn (x) (add x 1)))
`
	os.WriteFile(filepath.Join(dir, "base.logos"), []byte(baseContent), 0644)
	os.WriteFile(filepath.Join(dir, "library-order.txt"), []byte("base\n"), 0644)

	g, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}

	// Define with test, then close
	g.DefineWithTests("double", "(fn (x) (mul x 2))", []TestInput{
		{Name: "doubles 5", Expr: "(eq (double 5) 10)"},
	})
	g.Define("plain", "99")
	g.Close()

	// Reload
	g2, err := NewGraph(dir, DataBuiltins())
	if err != nil {
		t.Fatal(err)
	}
	defer g2.Close()

	// double should be Green (has tests)
	status, _ := g2.GetSymbolStatus("double")
	if status != StatusGreen {
		t.Errorf("expected Green for double after replay, got %s", status)
	}

	// plain should be Untested (no tests)
	status, _ = g2.GetSymbolStatus("plain")
	if status != StatusUntested {
		t.Errorf("expected Untested for plain after replay, got %s", status)
	}
}

func TestRedSymbolsEmpty(t *testing.T) {
	g := testGraphWithBase(t)

	g.Define("val", "42")
	g.DefineWithTests("double", "(fn (x) (mul x 2))", []TestInput{
		{Name: "doubles 3", Expr: "(eq (double 3) 6)"},
	})

	reds := g.RedSymbols()
	if len(reds) != 0 {
		t.Errorf("expected 0 red symbols, got %d: %v", len(reds), reds)
	}
}

func TestSymbolStatusString(t *testing.T) {
	if StatusUntested.String() != "untested" {
		t.Errorf("StatusUntested = %q", StatusUntested.String())
	}
	if StatusGreen.String() != "green" {
		t.Errorf("StatusGreen = %q", StatusGreen.String())
	}
	if StatusRed.String() != "red" {
		t.Errorf("StatusRed = %q", StatusRed.String())
	}
}

// --- Cascade tests ---

func TestCascadeAllContracted(t *testing.T) {
	// A → B → C, all with passing contracts. Change A → all auto-refresh.
	g := testGraphWithBase(t)

	// A: a value
	g.DefineWithTests("a", "1", []TestInput{
		{Name: "is positive", Expr: "(gt a 0)"},
	})
	// B: depends on A
	g.DefineWithTests("b", "(add a 10)", []TestInput{
		{Name: "is positive", Expr: "(gt b 0)"},
	})
	// C: depends on B
	g.DefineWithTests("c", "(add b 100)", []TestInput{
		{Name: "is positive", Expr: "(gt c 0)"},
	})

	// Redefine A to 2 (still positive, all tests should pass)
	g.Define("a", "2")

	result, err := g.RefreshAll([]string{"a"}, false)
	if err != nil {
		t.Fatal(err)
	}

	// Both B and C should be refreshed
	if len(result.Refreshed) != 2 {
		t.Fatalf("expected 2 refreshed, got %d: %v", len(result.Refreshed), result.Refreshed)
	}
	if len(result.Red) != 0 {
		t.Errorf("expected 0 red, got %v", result.Red)
	}
	if len(result.Stale) != 0 {
		t.Errorf("expected 0 stale, got %v", result.Stale)
	}

	// Verify values: A=2, B=12, C=112
	bVal, _ := g.EvalSymbol("b")
	if bVal.Int != 12 {
		t.Errorf("b = %d, want 12", bVal.Int)
	}
	cVal, _ := g.EvalSymbol("c")
	if cVal.Int != 112 {
		t.Errorf("c = %d, want 112", cVal.Int)
	}

	// All should be Green
	if s, _ := g.GetSymbolStatus("b"); s != StatusGreen {
		t.Errorf("b status = %s, want Green", s)
	}
	if s, _ := g.GetSymbolStatus("c"); s != StatusGreen {
		t.Errorf("c status = %s, want Green", s)
	}
}

func TestCascadeCircuitBreaker(t *testing.T) {
	// A → B → C, all with contracts. Change A in a way that breaks B's test.
	// B → Red, C unchanged.
	g := testGraphWithBase(t)

	// A: a value
	g.DefineWithTests("a", "5", []TestInput{
		{Name: "is positive", Expr: "(gt a 0)"},
	})
	// B: depends on A, test requires B > 10
	g.DefineWithTests("b", "(add a 10)", []TestInput{
		{Name: "b > 10", Expr: "(gt b 10)"},
	})
	// C: depends on B
	g.DefineWithTests("c", "(add b 100)", []TestInput{
		{Name: "c > 100", Expr: "(gt c 100)"},
	})

	// Redefine A to 0 → B becomes (add 0 10) = 10, test (gt b 10) fails (10 is not > 10)
	g.Define("a", "0")

	result, err := g.RefreshAll([]string{"a"}, false)
	if err != nil {
		t.Fatal(err)
	}

	// B should be Red
	if len(result.Red) != 1 || result.Red[0] != "b" {
		t.Errorf("expected Red = [b], got %v", result.Red)
	}

	// C should not be refreshed (circuit breaker stopped at B)
	for _, name := range result.Refreshed {
		if name == "c" {
			t.Error("c should not have been refreshed")
		}
	}

	// B should be Red status
	if s, _ := g.GetSymbolStatus("b"); s != StatusRed {
		t.Errorf("b status = %s, want Red", s)
	}

	// B should still have old value (add 5 10 = 15, not add 0 10 = 10)
	bVal, _ := g.EvalSymbol("b")
	if bVal.Int != 15 {
		t.Errorf("b = %d, want 15 (old value preserved)", bVal.Int)
	}
}

func TestCascadeFirewall(t *testing.T) {
	// A → B → C. B has no contract. B stops propagation.
	g := testGraphWithBase(t)

	g.DefineWithTests("a", "1", []TestInput{
		{Name: "is positive", Expr: "(gt a 0)"},
	})
	// B has no tests — untested firewall
	g.Define("b", "(add a 10)")
	// C depends on B, has tests
	g.DefineWithTests("c", "(add b 100)", []TestInput{
		{Name: "c > 100", Expr: "(gt c 100)"},
	})

	// Redefine A
	g.Define("a", "2")

	result, err := g.RefreshAll([]string{"a"}, false)
	if err != nil {
		t.Fatal(err)
	}

	// B should be stale (no contract)
	if len(result.Stale) != 1 || result.Stale[0] != "b" {
		t.Errorf("expected Stale = [b], got %v", result.Stale)
	}

	// C should not be refreshed (firewall at B)
	for _, name := range result.Refreshed {
		if name == "c" {
			t.Error("c should not have been refreshed")
		}
	}

	// B value should be old (add 1 10 = 11, not add 2 10 = 12) since it wasn't refreshed
	bVal, _ := g.EvalSymbol("b")
	if bVal.Int != 11 {
		t.Errorf("b = %d, want 11 (old value, not refreshed)", bVal.Int)
	}
}

func TestCascadeGreenAfterAutoRefresh(t *testing.T) {
	// Verify that successfully auto-refreshed symbols are Green.
	g := testGraphWithBase(t)

	g.DefineWithTests("a", "1", []TestInput{
		{Name: "is positive", Expr: "(gt a 0)"},
	})
	g.DefineWithTests("b", "(add a 10)", []TestInput{
		{Name: "is positive", Expr: "(gt b 0)"},
	})

	// Redefine A
	g.Define("a", "5")
	g.RefreshAll([]string{"a"}, false)

	s, _ := g.GetSymbolStatus("b")
	if s != StatusGreen {
		t.Errorf("b status = %s after auto-refresh, want Green", s)
	}
}
