package logos

import (
	"errors"
	"testing"
)

func testEval(t *testing.T, input string, expected Value) {
	t.Helper()
	ev := &Evaluator{Builtins: DataBuiltins()}
	val, err := ev.EvalString(input)
	if err != nil {
		t.Fatalf("eval %q: %v", input, err)
	}
	if !ValuesEqual(val, expected) {
		t.Fatalf("eval %q: expected %s, got %s", input, expected.String(), val.String())
	}
}

func testEvalError(t *testing.T, input string) {
	t.Helper()
	ev := &Evaluator{Builtins: DataBuiltins()}
	_, err := ev.EvalString(input)
	if err == nil {
		t.Fatalf("expected error for %q", input)
	}
}

// --- Literals ---

func TestEvalLiterals(t *testing.T) {
	testEval(t, "42", IntVal(42))
	testEval(t, "3.14", FloatVal(3.14))
	testEval(t, "true", BoolVal(true))
	testEval(t, "false", BoolVal(false))
	testEval(t, `"hello"`, StringVal("hello"))
	testEval(t, "nil", NilVal())
}

// --- If with Truthy ---

func TestEvalIfTruthy(t *testing.T) {
	testEval(t, `(if true "yes" "no")`, StringVal("yes"))
	testEval(t, `(if false "yes" "no")`, StringVal("no"))
	testEval(t, `(if nil "yes" "no")`, StringVal("no"))
	testEval(t, `(if 0 "yes" "no")`, StringVal("yes"))       // 0 is truthy
	testEval(t, `(if "" "yes" "no")`, StringVal("yes"))       // "" is truthy
	testEval(t, `(if (list) "yes" "no")`, StringVal("yes"))   // empty list is truthy
}

// --- Let ---

func TestEvalLet(t *testing.T) {
	testEval(t, `(let (x 1) x)`, IntVal(1))
	testEval(t, `(let (x 1 y 2) (list x y))`, ListVal([]Value{IntVal(1), IntVal(2)}))
}

func TestEvalLetSequential(t *testing.T) {
	testEval(t, `(let (x 1 y x) y)`, IntVal(1))
}

// --- Fuel ---

func testEvalFuel(t *testing.T, input string, fuel int, expected Value) {
	t.Helper()
	ev := &Evaluator{Builtins: DataBuiltins(), Fuel: fuel, FuelSet: true}
	val, err := ev.EvalString(input)
	if err != nil {
		t.Fatalf("eval %q (fuel=%d): %v", input, fuel, err)
	}
	if !ValuesEqual(val, expected) {
		t.Fatalf("eval %q (fuel=%d): expected %s, got %s", input, fuel, expected.String(), val.String())
	}
}

func testEvalFuelError(t *testing.T, input string, fuel int) {
	t.Helper()
	ev := &Evaluator{Builtins: DataBuiltins(), Fuel: fuel, FuelSet: true}
	_, err := ev.EvalString(input)
	if err == nil {
		t.Fatalf("expected fuel error for %q (fuel=%d)", input, fuel)
	}
}

func TestFuelExhaustion(t *testing.T) {
	// Infinite loop caught by fuel
	testEvalFuelError(t, `(loop ((x 0)) (recur (add x 1)))`, 100)
}

func TestFuelSufficient(t *testing.T) {
	// Simple expression with generous fuel succeeds
	testEvalFuel(t, `(add 1 2)`, 1000, IntVal(3))
}

func TestFuelExact(t *testing.T) {
	// Literal requires exactly 1 eval step
	testEvalFuel(t, `42`, 1, IntVal(42))
	testEvalFuelError(t, `42`, 0)
}

func TestNoFuelDefault(t *testing.T) {
	// Default (FuelSet=false) means no limit — existing testEval covers this
	testEval(t, `(add 1 2)`, IntVal(3))
}

// --- Do ---

func TestEvalDo(t *testing.T) {
	testEval(t, `(do 1 2 3)`, IntVal(3))
}

// --- Fn ---

func TestEvalFn(t *testing.T) {
	testEval(t, `((fn (x) x) 42)`, IntVal(42))
	testEval(t, `((fn (a b) (list a b)) 1 2)`, ListVal([]Value{IntVal(1), IntVal(2)}))
}

func TestEvalFnWrongArity(t *testing.T) {
	testEvalError(t, `((fn (x) x) 1 2)`)
}

// --- Rest params ---

func TestRestParamsFn(t *testing.T) {
	testEval(t, `((fn (x & rest) rest) 1 2 3)`, ListVal([]Value{IntVal(2), IntVal(3)}))
	testEval(t, `((fn (x & rest) (list x rest)) 1)`, ListVal([]Value{IntVal(1), ListVal([]Value{})}))
	testEval(t, `((fn (& rest) rest) 1 2 3)`, ListVal([]Value{IntVal(1), IntVal(2), IntVal(3)}))
	testEval(t, `((fn (& rest) rest))`, ListVal([]Value{}))
}

func TestRestParamsFnArity(t *testing.T) {
	// Too few args for positional params
	testEvalError(t, `((fn (x y & rest) rest) 1)`)
}

func TestRestParamsForm(t *testing.T) {
	testEval(t, `(let (my-list (form (& items) (cons (quote list) items))) (my-list 1 2 3))`,
		ListVal([]Value{IntVal(1), IntVal(2), IntVal(3)}))
}

func TestRestParamsString(t *testing.T) {
	ev := &Evaluator{Builtins: DataBuiltins()}
	val, err := ev.EvalString(`(fn (x & rest) x)`)
	if err != nil {
		t.Fatalf("fn creation: %v", err)
	}
	expected := "<fn(x, & rest)>"
	if val.String() != expected {
		t.Fatalf("expected %q, got %q", expected, val.String())
	}
	// Form with rest
	val, err = ev.EvalString(`(form (& args) args)`)
	if err != nil {
		t.Fatalf("form creation: %v", err)
	}
	expected = "<form(& args)>"
	if val.String() != expected {
		t.Fatalf("expected %q, got %q", expected, val.String())
	}
}

func TestRestParamsParseErrors(t *testing.T) {
	// & not followed by exactly one name
	testEvalError(t, `(fn (x &) x)`)
	testEvalError(t, `(fn (x & a b) x)`)
}

// --- Quote ---

func TestEvalQuote(t *testing.T) {
	testEval(t, `(quote 42)`, IntVal(42))
	testEval(t, `(quote foo)`, SymbolVal("foo"))
	testEval(t, `(quote (a b c))`, ListVal([]Value{SymbolVal("a"), SymbolVal("b"), SymbolVal("c")}))
	testEval(t, `(type (quote foo))`, KeywordVal("symbol"))
}

// --- Data builtins ---

func TestBuiltinList(t *testing.T) {
	testEval(t, `(list 1 2 3)`, ListVal([]Value{IntVal(1), IntVal(2), IntVal(3)}))
	testEval(t, `(list)`, ListVal([]Value{}))
}

func TestBuiltinDict(t *testing.T) {
	testEval(t, `(get (dict "a" 1 "b" 2) "a")`, IntVal(1))
	testEval(t, `(get (dict "a" 1) "missing")`, NilVal())
}

func TestBuiltinHead(t *testing.T) {
	testEval(t, `(head (list 1 2 3))`, IntVal(1))
	testEval(t, `(head (list))`, NilVal())
}

func TestBuiltinRest(t *testing.T) {
	testEval(t, `(rest (list 1 2 3))`, ListVal([]Value{IntVal(2), IntVal(3)}))
	testEval(t, `(rest (list))`, ListVal([]Value{}))
}

func TestBuiltinLen(t *testing.T) {
	testEval(t, `(len (list 1 2 3))`, IntVal(3))
}

func TestBuiltinKeys(t *testing.T) {
	testEval(t, `(keys (dict "b" 2 "a" 1))`, ListVal([]Value{StringVal("a"), StringVal("b")}))
}

func TestBuiltinEq(t *testing.T) {
	testEval(t, `(eq 1 1)`, BoolVal(true))
	testEval(t, `(eq 1 2)`, BoolVal(false))
	testEval(t, `(eq "a" "a")`, BoolVal(true))
	testEval(t, `(eq nil nil)`, BoolVal(true))
	testEval(t, `(eq (list 1 2) (list 1 2))`, BoolVal(true))
}

func TestBuiltinToString(t *testing.T) {
	testEval(t, `(to-string 42)`, StringVal("42"))
	testEval(t, `(to-string nil)`, StringVal("nil"))
}

func TestBuiltinConcat(t *testing.T) {
	testEval(t, `(concat "hello" " " "world")`, StringVal("hello world"))
}

// --- Arithmetic ---

func TestBuiltinAdd(t *testing.T) {
	testEval(t, `(add 1 2)`, IntVal(3))
	testEval(t, `(add 1 2.5)`, FloatVal(3.5))
	testEval(t, `(add 1.5 2.5)`, FloatVal(4.0))
}

func TestBuiltinSub(t *testing.T) {
	testEval(t, `(sub 5 3)`, IntVal(2))
	testEval(t, `(sub 5 2.5)`, FloatVal(2.5))
}

func TestBuiltinMul(t *testing.T) {
	testEval(t, `(mul 3 4)`, IntVal(12))
	testEval(t, `(mul 3 1.5)`, FloatVal(4.5))
}

func TestBuiltinDiv(t *testing.T) {
	testEval(t, `(div 10 3)`, IntVal(3))
	testEval(t, `(div 10 3.0)`, FloatVal(10.0/3.0))
	testEvalError(t, `(div 1 0)`)
}

func TestBuiltinMod(t *testing.T) {
	testEval(t, `(mod 10 3)`, IntVal(1))
	testEvalError(t, `(mod 10 0)`)
	testEvalError(t, `(mod 1.5 2)`)
}

// --- Comparison ---

func TestBuiltinLtGt(t *testing.T) {
	testEval(t, `(lt 1 2)`, BoolVal(true))
	testEval(t, `(lt 2 1)`, BoolVal(false))
	testEval(t, `(gt 2 1)`, BoolVal(true))
	testEval(t, `(gt 1 2)`, BoolVal(false))
}

func TestComparisonMixed(t *testing.T) {
	testEval(t, `(lt 1 2.5)`, BoolVal(true))
	testEval(t, `(gt 3.0 2)`, BoolVal(true))
}

func TestComparisonStrings(t *testing.T) {
	testEval(t, `(lt "a" "b")`, BoolVal(true))
	testEval(t, `(gt "b" "a")`, BoolVal(true))
}

func TestComparisonCrossType(t *testing.T) {
	// nil < bool < int < float < string < keyword < list < map
	testEval(t, `(lt nil false)`, BoolVal(true))
	testEval(t, `(lt false 0)`, BoolVal(true))
	testEval(t, `(lt 0 0.0)`, BoolVal(false)) // numeric promotion: equal
	testEval(t, `(lt 0 "a")`, BoolVal(true))
	testEval(t, `(lt "a" :a)`, BoolVal(true))
	testEval(t, `(lt :a (list))`, BoolVal(true))
	testEval(t, `(lt (list) (dict))`, BoolVal(true))
}

func TestComparisonBool(t *testing.T) {
	testEval(t, `(lt false true)`, BoolVal(true))
	testEval(t, `(gt true false)`, BoolVal(true))
	testEval(t, `(lt true true)`, BoolVal(false))
}

func TestComparisonLists(t *testing.T) {
	// Lexicographic
	testEval(t, `(lt (list 1 2) (list 1 3))`, BoolVal(true))
	testEval(t, `(lt (list 1) (list 1 2))`, BoolVal(true)) // shorter < longer
	testEval(t, `(lt (list 1 2) (list 1 2))`, BoolVal(false)) // equal
	testEval(t, `(gt (list 2) (list 1 2 3))`, BoolVal(true))
}

func TestComparisonMaps(t *testing.T) {
	// Same keys, different values
	testEval(t, `(lt (dict "a" 1) (dict "a" 2))`, BoolVal(true))
	// Same keys and values
	testEval(t, `(lt (dict "a" 1) (dict "a" 1))`, BoolVal(false))
}

func TestSortByUniversal(t *testing.T) {
	// sort-by identity on mixed types
	testEval(t, `(sort-by (fn (x) x) (list 3 1 2))`, ListVal([]Value{IntVal(1), IntVal(2), IntVal(3)}))
	testEval(t, `(sort-by (fn (x) x) (list "b" "a" "c"))`, ListVal([]Value{StringVal("a"), StringVal("b"), StringVal("c")}))
}

// --- Higher-order (core forms that remain in Go) ---

func TestEvalApply(t *testing.T) {
	testEval(t, `(apply (fn (a b) (add a b)) (list 3 4))`, IntVal(7))
	// Apply with builtins
	testEval(t, `(apply add (list 3 4))`, IntVal(7))
	testEval(t, `(apply list (list 1 2 3))`, ListVal([]Value{IntVal(1), IntVal(2), IntVal(3)}))
}

func TestEvalSortBy(t *testing.T) {
	testEval(t, `(sort-by (fn (x) x) (list 3 1 2))`, ListVal([]Value{IntVal(1), IntVal(2), IntVal(3)}))
	testEval(t, `(sort-by (fn (x) x) :desc (list 3 1 2))`, ListVal([]Value{IntVal(3), IntVal(2), IntVal(1)}))
}

// --- List ops ---

func TestBuiltinCons(t *testing.T) {
	testEval(t, `(cons 0 (list 1 2))`, ListVal([]Value{IntVal(0), IntVal(1), IntVal(2)}))
	testEval(t, `(cons 1 (list))`, ListVal([]Value{IntVal(1)}))
}

func TestBuiltinNth(t *testing.T) {
	testEval(t, `(nth (list 10 20 30) 0)`, IntVal(10))
	testEval(t, `(nth (list 10 20 30) 2)`, IntVal(30))
	testEval(t, `(nth (list 10 20 30) 5)`, NilVal())
}

func TestBuiltinAppend(t *testing.T) {
	testEval(t, `(append (list 1 2) (list 3 4))`, ListVal([]Value{IntVal(1), IntVal(2), IntVal(3), IntVal(4)}))
	testEval(t, `(append (list) (list 1))`, ListVal([]Value{IntVal(1)}))
}

// --- Map ops ---

func TestBuiltinPut(t *testing.T) {
	testEval(t, `(get (put (dict "a" 1) "b" 2) "b")`, IntVal(2))
	testEval(t, `(get (put (dict :a 1) :b 2) :b)`, IntVal(2))
}

func TestBuiltinHas(t *testing.T) {
	testEval(t, `(has? (dict "a" 1) "a")`, BoolVal(true))
	testEval(t, `(has? (dict "a" 1) "b")`, BoolVal(false))
	testEval(t, `(has? (dict :x 1) :x)`, BoolVal(true))
}

// --- String ops ---

func TestBuiltinSplitOnce(t *testing.T) {
	// Basic match — splits on first occurrence only
	testEval(t, `(split-once "," "a,b,c")`, ListVal([]Value{StringVal("a"), StringVal("b,c")}))
	// No match → nil
	testEval(t, `(split-once "x" "abc")`, NilVal())
	// Match at start
	testEval(t, `(split-once "a" "abc")`, ListVal([]Value{StringVal(""), StringVal("bc")}))
	// Match at end
	testEval(t, `(split-once "c" "abc")`, ListVal([]Value{StringVal("ab"), StringVal("")}))
	// Whole string matches
	testEval(t, `(split-once "abc" "abc")`, ListVal([]Value{StringVal(""), StringVal("")}))
	// Multi-char needle
	testEval(t, `(split-once "::" "a::b::c")`, ListVal([]Value{StringVal("a"), StringVal("b::c")}))
}

func TestBuiltinSplitOnceEmptyNeedle(t *testing.T) {
	testEvalError(t, `(split-once "" "abc")`)
}

// --- Keywords ---

func TestKeywordSelfEval(t *testing.T) {
	testEval(t, `:foo`, KeywordVal("foo"))
	testEval(t, `:hello-world`, KeywordVal("hello-world"))
}

func TestKeywordInList(t *testing.T) {
	testEval(t, `(list :a :b :c)`, ListVal([]Value{KeywordVal("a"), KeywordVal("b"), KeywordVal("c")}))
}

func TestKeywordAsMapKey(t *testing.T) {
	testEval(t, `(get (dict :name "alice") :name)`, StringVal("alice"))
	testEval(t, `(get (dict :x 1 :y 2) :y)`, IntVal(2))
}

func TestKeywordEquality(t *testing.T) {
	testEval(t, `(eq :foo :foo)`, BoolVal(true))
	testEval(t, `(eq :foo :bar)`, BoolVal(false))
	testEval(t, `(eq :foo "foo")`, BoolVal(false))
}

func TestKeywordTruthy(t *testing.T) {
	testEval(t, `(if :ok "yes" "no")`, StringVal("yes"))
}

func TestKeywordQuote(t *testing.T) {
	testEval(t, `(quote :foo)`, KeywordVal("foo"))
}

// --- Type builtin ---

func TestBuiltinType(t *testing.T) {
	testEval(t, `(type 42)`, KeywordVal("int"))
	testEval(t, `(type 3.14)`, KeywordVal("float"))
	testEval(t, `(type true)`, KeywordVal("bool"))
	testEval(t, `(type "hi")`, KeywordVal("string"))
	testEval(t, `(type :foo)`, KeywordVal("keyword"))
	testEval(t, `(type nil)`, KeywordVal("nil"))
	testEval(t, `(type (list 1))`, KeywordVal("list"))
	testEval(t, `(type (dict "a" 1))`, KeywordVal("map"))
	testEval(t, `(type (fn (x) x))`, KeywordVal("fn"))
	testEval(t, `(type add)`, KeywordVal("builtin"))
}

// --- Truthy ---

// --- Assert ---

func TestAssertPass(t *testing.T) {
	ev := &Evaluator{Builtins: DataBuiltins()}
	ev.Builtins["assert"] = ev.builtinAssert
	val, err := ev.EvalString(`(assert true "ok")`)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !ValuesEqual(val, BoolVal(true)) {
		t.Fatalf("expected true, got %s", val.String())
	}
}

func TestAssertFail(t *testing.T) {
	ev := &Evaluator{Builtins: DataBuiltins()}
	ev.Builtins["assert"] = ev.builtinAssert
	_, err := ev.EvalString(`(assert false "boom")`)
	if err == nil {
		t.Fatal("expected error")
	}
	var ae *AssertError
	if !errors.As(err, &ae) {
		t.Fatalf("expected AssertError, got %T: %v", err, err)
	}
	if ae.Message != "boom" {
		t.Fatalf("expected message 'boom', got %q", ae.Message)
	}
}

func TestAssertTruthy(t *testing.T) {
	ev := &Evaluator{Builtins: DataBuiltins()}
	ev.Builtins["assert"] = ev.builtinAssert
	// 0 is truthy in logos
	val, err := ev.EvalString(`(assert 0 "zero is truthy")`)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !ValuesEqual(val, BoolVal(true)) {
		t.Fatalf("expected true, got %s", val.String())
	}
	// nil is falsy
	_, err = ev.EvalString(`(assert nil "nil is falsy")`)
	if err == nil {
		t.Fatal("expected error for nil assertion")
	}
}

func TestAssertWrongArity(t *testing.T) {
	ev := &Evaluator{Builtins: DataBuiltins()}
	ev.Builtins["assert"] = ev.builtinAssert
	_, err := ev.EvalString(`(assert true)`)
	if err == nil {
		t.Fatal("expected error for wrong arity")
	}
}

func TestAssertNonStringMessage(t *testing.T) {
	ev := &Evaluator{Builtins: DataBuiltins()}
	ev.Builtins["assert"] = ev.builtinAssert
	_, err := ev.EvalString(`(assert false 42)`)
	if err == nil {
		t.Fatal("expected error for non-string message")
	}
}

// --- Form (macros) ---

func TestFormCreatesFormType(t *testing.T) {
	testEval(t, `(type (form (x) x))`, KeywordVal("form"))
}

func TestFormWhenMacro(t *testing.T) {
	// when: (when test body) → (if test body nil)
	testEval(t, `(let (when (form (test body) (list (quote if) test body nil))) (when true 42))`, IntVal(42))
	testEval(t, `(let (when (form (test body) (list (quote if) test body nil))) (when false 42))`, NilVal())
}

func TestFormArgsNotEvaluated(t *testing.T) {
	// The form should receive AST data, not evaluated values
	testEval(t, `(let (my-quote (form (x) (list (quote quote) x))) (my-quote hello))`, SymbolVal("hello"))
}

func TestFormArityError(t *testing.T) {
	testEvalError(t, `(let (my-form (form (a b) a)) (my-form 1))`)
}

func TestFormSyntaxErrors(t *testing.T) {
	testEvalError(t, `(form)`)
	testEvalError(t, `(form (x))`)           // missing body
	testEvalError(t, `(form "bad" x)`)        // params not a list
	testEvalError(t, `(form (1) x)`)           // param not a symbol
}

func TestFormApplyRejects(t *testing.T) {
	testEvalError(t, `(apply (form (x) x) (list 1))`)
}

func TestFormSortByRejects(t *testing.T) {
	testEvalError(t, `(sort-by (form (x) x) (list 1 2))`)
}

func TestFormFuelCatchesInfiniteExpansion(t *testing.T) {
	// A form that expands to calling itself — infinite macro expansion
	// Use let to bind loop-form, then call it — fuel should catch infinite expansion
	testEvalFuelError(t, `(let (loop-form (form () (list (quote loop-form)))) (loop-form))`, 50)
}

func TestFormInLet(t *testing.T) {
	// Forms work in let bindings
	testEval(t, `(let (my-when (form (test body) (list (quote if) test body nil)))
		(my-when true 42))`, IntVal(42))
}

func TestFormString(t *testing.T) {
	ev := &Evaluator{Builtins: DataBuiltins()}
	val, err := ev.EvalString(`(form (a b) a)`)
	if err != nil {
		t.Fatalf("form creation: %v", err)
	}
	expected := "<form(a, b)>"
	if val.String() != expected {
		t.Fatalf("expected %q, got %q", expected, val.String())
	}
}

func TestValueToNodeRoundtrip(t *testing.T) {
	// Verify valueToNode is inverse of nodeToValue for all supported types
	cases := []string{
		"42", "3.14", "true", `"hello"`, "nil", ":foo",
		"(list 1 2 3)", "(list (list 1) (list 2))",
	}
	ev := &Evaluator{Builtins: DataBuiltins()}
	for _, input := range cases {
		node, err := Parse(input)
		if err != nil {
			t.Fatalf("parse %q: %v", input, err)
		}
		val := nodeToValue(node)
		roundtripped, err := valueToNode(val)
		if err != nil {
			t.Fatalf("valueToNode %q: %v", input, err)
		}
		result, err := ev.Eval(roundtripped)
		if err != nil {
			t.Fatalf("eval roundtripped %q: %v", input, err)
		}
		original, err := ev.Eval(node)
		if err != nil {
			t.Fatalf("eval original %q: %v", input, err)
		}
		if !ValuesEqual(result, original) {
			t.Fatalf("roundtrip %q: expected %s, got %s", input, original.String(), result.String())
		}
	}
}

func TestValueToNodeRejectsUnsupported(t *testing.T) {
	// Map should be rejected
	m := MapVal(map[string]Value{"a": IntVal(1)})
	_, err := valueToNode(m)
	if err == nil {
		t.Fatal("expected error for Map")
	}
	// Fn should be rejected
	fn := FnVal(&FnValue{Params: []string{"x"}, Body: &Node{Kind: NodeInt, Int: 1}})
	_, err = valueToNode(fn)
	if err == nil {
		t.Fatal("expected error for Fn")
	}
	// Form should be rejected
	form := FormVal(&FnValue{Params: []string{"x"}, Body: &Node{Kind: NodeInt, Int: 1}})
	_, err = valueToNode(form)
	if err == nil {
		t.Fatal("expected error for Form")
	}
}

// --- Truthy ---

func TestValueTruthy(t *testing.T) {
	cases := []struct {
		val    Value
		truthy bool
	}{
		{NilVal(), false},
		{BoolVal(false), false},
		{BoolVal(true), true},
		{IntVal(0), true},
		{IntVal(1), true},
		{StringVal(""), true},
		{ListVal([]Value{}), true},
	}
	for _, tc := range cases {
		if tc.val.Truthy() != tc.truthy {
			t.Fatalf("%s.Truthy() = %v, want %v", tc.val.String(), tc.val.Truthy(), tc.truthy)
		}
	}
}

// --- ValBuiltin ---

func TestValBuiltinType(t *testing.T) {
	testEval(t, `(type add)`, KeywordVal("builtin"))
	testEval(t, `(type list)`, KeywordVal("builtin"))
	testEval(t, `(type concat)`, KeywordVal("builtin"))
}

func TestBuiltinAsValue(t *testing.T) {
	// Builtin bound to a local, then called via computed head
	testEval(t, `(let (f add) (f 1 2))`, IntVal(3))
}

func TestBuiltinHigherOrder(t *testing.T) {
	// Pass builtin as argument to a fn
	testEval(t, `((fn (f) (f 1 2)) add)`, IntVal(3))
	testEval(t, `((fn (f) (f 10 3)) sub)`, IntVal(7))
}

func TestBuiltinEquality(t *testing.T) {
	testEval(t, `(eq add add)`, BoolVal(true))
	testEval(t, `(eq add sub)`, BoolVal(false))
}

func TestBuiltinNoArgComputedHead(t *testing.T) {
	// Builtin with no args called via computed head
	testEval(t, `(let (f list) (f))`, ListVal([]Value{}))
}

func TestBuiltinString(t *testing.T) {
	v := BuiltinVal("add", nil)
	if v.String() != "<builtin:add>" {
		t.Fatalf("expected <builtin:add>, got %s", v.String())
	}
}

func TestBuiltinKindName(t *testing.T) {
	v := BuiltinVal("add", nil)
	if v.KindName() != "Builtin" {
		t.Fatalf("expected Builtin, got %s", v.KindName())
	}
}

// --- JSON builtins ---

func TestToJSON(t *testing.T) {
	testEval(t, `(to-json 42)`, StringVal("42"))
	testEval(t, `(to-json "hello")`, StringVal(`"hello"`))
	testEval(t, `(to-json true)`, StringVal("true"))
	testEval(t, `(to-json nil)`, StringVal("null"))
	testEval(t, `(to-json (list 1 2 3))`, StringVal("[1,2,3]"))
}

func TestFromJSON(t *testing.T) {
	testEval(t, `(from-json "42")`, IntVal(42))
	testEval(t, `(from-json "\"hello\"")`, StringVal("hello"))
	testEval(t, `(from-json "true")`, BoolVal(true))
	testEval(t, `(from-json "null")`, NilVal())
	testEval(t, `(from-json "[1,2,3]")`, ListVal([]Value{IntVal(1), IntVal(2), IntVal(3)}))
}

func TestJSONRoundtrip(t *testing.T) {
	// map round-trip
	testEval(t, `(from-json (to-json (dict "a" 1 "b" (list 2 3))))`,
		MapVal(map[string]Value{"a": IntVal(1), "b": ListVal([]Value{IntVal(2), IntVal(3)})}))
}

func TestToJSONError(t *testing.T) {
	// fn values can't be serialized
	testEvalError(t, `(to-json (fn (x) x))`)
}

func TestFromJSONError(t *testing.T) {
	// invalid JSON
	testEvalError(t, `(from-json "not json")`)
}

// --- Loop/Recur ---

func TestLoopBasic(t *testing.T) {
	// Counter to 5
	testEval(t, `(loop ((x 0)) (if (eq x 5) x (recur (add x 1))))`, IntVal(5))
}

func TestLoopMultipleBindings(t *testing.T) {
	// Accumulator pattern: sum 1..5
	testEval(t, `(loop ((i 1) (acc 0)) (if (gt i 5) acc (recur (add i 1) (add acc i))))`, IntVal(15))
}

func TestLoopNested(t *testing.T) {
	// Outer loop counts to 3, inner loop counts to 2
	testEval(t, `(loop ((i 0) (total 0))
		(if (eq i 3) total
			(let (inner (loop ((j 0) (sum 0))
				(if (eq j 2) sum (recur (add j 1) (add sum 1)))))
				(recur (add i 1) (add total inner)))))`, IntVal(6))
}

func TestLoopNoRecur(t *testing.T) {
	// Body returns immediately without recur
	testEval(t, `(loop ((x 42)) x)`, IntVal(42))
}

func TestRecurOutsideLoop(t *testing.T) {
	testEvalError(t, `(recur 1)`)
}

func TestRecurWrongArity(t *testing.T) {
	testEvalError(t, `(loop ((x 0)) (recur 1 2))`)
}

func TestLoopNoBindings(t *testing.T) {
	// Loop with no bindings, body returns directly
	testEval(t, `(loop () 42)`, IntVal(42))
}

func TestLoopSyntaxErrors(t *testing.T) {
	testEvalError(t, `(loop)`)                        // missing bindings and body
	testEvalError(t, `(loop ())`)                     // missing body
	testEvalError(t, `(loop "bad" 1)`)               // bindings not a list
	testEvalError(t, `(loop ((1 2)) 1)`)              // name not a symbol
	testEvalError(t, `(loop ((x)) 1)`)                // pair has only one element
}

// --- NodeBuiltin ---

func TestNodeBuiltinResolve(t *testing.T) {
	g := testGraph(t)
	node, err := g.Define("inc", `(fn (x) (add x 1))`)
	if err != nil {
		t.Fatal(err)
	}
	// The stored AST should have `add` resolved to NodeBuiltin
	body := node.Expr.Children[2] // (add x 1)
	if body.Kind != NodeList {
		t.Fatalf("expected body to be list, got %d", body.Kind)
	}
	if body.Children[0].Kind != NodeBuiltin {
		t.Fatalf("expected head to be NodeBuiltin, got %d", body.Children[0].Kind)
	}
	if body.Children[0].Str != "add" {
		t.Fatalf("expected builtin name 'add', got %q", body.Children[0].Str)
	}
	// Verify it still works
	val, err := g.Eval(`(inc 5)`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(6)) {
		t.Fatalf("expected 6, got %s", val.String())
	}
}

func TestNodeBuiltinNonHead(t *testing.T) {
	// Builtin in non-head position (bound to a local)
	g := testGraph(t)
	g.Define("apply-add", `(let (f add) (f 1 2))`)
	val, err := g.Eval(`apply-add`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(3)) {
		t.Fatalf("expected 3, got %s", val.String())
	}
}

// --- Link ---

func TestLinkFollow(t *testing.T) {
	g := testGraph(t)
	g.Define("a", `42`)
	val, err := g.Eval(`(follow (link 'a))`)
	if err != nil {
		t.Fatal(err)
	}
	if !ValuesEqual(val, IntVal(42)) {
		t.Fatalf("expected 42, got %s", val.String())
	}
}

func TestLinkType(t *testing.T) {
	testEval(t, `(type (link 'a))`, KeywordVal("link"))
}

func TestLinkNonexistent(t *testing.T) {
	// Creating a link to a nonexistent symbol succeeds
	testEval(t, `(link 'nonexistent)`, LinkVal("nonexistent"))
}

func TestFollowNonexistent(t *testing.T) {
	g := testGraph(t)
	_, err := g.Eval(`(follow (link 'nonexistent))`)
	if err == nil {
		t.Fatal("expected error following nonexistent link")
	}
}

func TestLinkInData(t *testing.T) {
	testEval(t, `(get (dict "ref" (link 'foo)) "ref")`, LinkVal("foo"))
}

func TestLinkTarget(t *testing.T) {
	testEval(t, `(link-target (link 'foo))`, SymbolVal("foo"))
}
