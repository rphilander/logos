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
	testEval(t, `(let ((x 1)) x)`, IntVal(1))
	testEval(t, `(let ((x 1) (y 2)) (list x y))`, ListVal([]Value{IntVal(1), IntVal(2)}))
}

func TestEvalLetSequential(t *testing.T) {
	testEval(t, `(let ((x 1) (y x)) y)`, IntVal(1))
}

// --- Cond ---

func TestEvalCond(t *testing.T) {
	testEval(t, `(cond false 1 true 2)`, IntVal(2))
	testEval(t, `(cond true 1 true 2)`, IntVal(1))
}

func TestEvalCondTruthy(t *testing.T) {
	testEval(t, `(cond nil 1 0 2)`, IntVal(2))
}

func TestEvalCondExprs(t *testing.T) {
	testEval(t, `(cond (eq 1 2) "no" (eq 1 1) "yes")`, StringVal("yes"))
}

func TestEvalCondNoMatch(t *testing.T) {
	testEval(t, `(cond false 1 false 2)`, NilVal())
}

func TestEvalCondOddArgs(t *testing.T) {
	testEvalError(t, `(cond true)`)
}

// --- Case ---

func TestEvalCaseKeyword(t *testing.T) {
	testEval(t, `(case :b :a 1 :b 2 :c 3)`, IntVal(2))
}

func TestEvalCaseDefault(t *testing.T) {
	testEval(t, `(case :z :a 1 :b 2 "default")`, StringVal("default"))
}

func TestEvalCaseNoMatch(t *testing.T) {
	testEval(t, `(case :z :a 1 :b 2)`, NilVal())
}

func TestEvalCaseInt(t *testing.T) {
	testEval(t, `(case 2 1 "one" 2 "two")`, StringVal("two"))
}

func TestEvalCaseWithType(t *testing.T) {
	testEval(t, `(case (type 42) :int "integer" :string "text" "other")`, StringVal("integer"))
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

func TestBuiltinEmpty(t *testing.T) {
	testEval(t, `(empty? (list))`, BoolVal(true))
	testEval(t, `(empty? (list 1))`, BoolVal(false))
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

func TestBuiltinNilQ(t *testing.T) {
	testEval(t, `(nil? nil)`, BoolVal(true))
	testEval(t, `(nil? 0)`, BoolVal(false))
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

func TestBuiltinNeq(t *testing.T) {
	testEval(t, `(neq 1 2)`, BoolVal(true))
	testEval(t, `(neq 1 1)`, BoolVal(false))
}

func TestBuiltinLtGt(t *testing.T) {
	testEval(t, `(lt 1 2)`, BoolVal(true))
	testEval(t, `(lt 2 1)`, BoolVal(false))
	testEval(t, `(gt 2 1)`, BoolVal(true))
	testEval(t, `(gt 1 2)`, BoolVal(false))
}

func TestBuiltinLeGe(t *testing.T) {
	testEval(t, `(le 1 1)`, BoolVal(true))
	testEval(t, `(le 1 2)`, BoolVal(true))
	testEval(t, `(le 2 1)`, BoolVal(false))
	testEval(t, `(ge 1 1)`, BoolVal(true))
	testEval(t, `(ge 2 1)`, BoolVal(true))
	testEval(t, `(ge 1 2)`, BoolVal(false))
}

func TestComparisonMixed(t *testing.T) {
	testEval(t, `(lt 1 2.5)`, BoolVal(true))
	testEval(t, `(gt 3.0 2)`, BoolVal(true))
}

func TestComparisonStrings(t *testing.T) {
	testEval(t, `(lt "a" "b")`, BoolVal(true))
	testEval(t, `(gt "b" "a")`, BoolVal(true))
}

// --- Boolean ---

func TestBuiltinAnd(t *testing.T) {
	testEval(t, `(and true true)`, BoolVal(true))
	testEval(t, `(and true false)`, BoolVal(false))
	testEval(t, `(and 1 2)`, BoolVal(true))
	testEval(t, `(and 1 nil)`, BoolVal(false))
}

func TestBuiltinOr(t *testing.T) {
	testEval(t, `(or false false)`, BoolVal(false))
	testEval(t, `(or false true)`, BoolVal(true))
	testEval(t, `(or nil 1)`, BoolVal(true))
}

func TestBuiltinNot(t *testing.T) {
	testEval(t, `(not true)`, BoolVal(false))
	testEval(t, `(not false)`, BoolVal(true))
	testEval(t, `(not nil)`, BoolVal(true))
	testEval(t, `(not 0)`, BoolVal(false))
}

// --- Higher-order (core forms that remain in Go) ---

func TestEvalApply(t *testing.T) {
	testEval(t, `(apply (fn (a b) (add a b)) (list 3 4))`, IntVal(7))
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

func TestBuiltinReverse(t *testing.T) {
	testEval(t, `(reverse (list 1 2 3))`, ListVal([]Value{IntVal(3), IntVal(2), IntVal(1)}))
	testEval(t, `(reverse (list))`, ListVal([]Value{}))
}

func TestBuiltinUniq(t *testing.T) {
	testEval(t, `(uniq (list 1 2 1 3 2))`, ListVal([]Value{IntVal(1), IntVal(2), IntVal(3)}))
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

func TestBuiltinDissoc(t *testing.T) {
	testEval(t, `(len (dissoc (dict "a" 1 "b" 2) "a"))`, IntVal(1))
	testEval(t, `(get (dissoc (dict "a" 1 "b" 2) "a") "b")`, IntVal(2))
}

func TestBuiltinMerge(t *testing.T) {
	testEval(t, `(get (merge (dict "a" 1) (dict "b" 2)) "b")`, IntVal(2))
	testEval(t, `(get (merge (dict "a" 1) (dict "a" 2)) "a")`, IntVal(2))
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
