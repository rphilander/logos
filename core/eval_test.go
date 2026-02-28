package logos

import (
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
	testEval(t, `(quote foo)`, StringVal("foo"))
	testEval(t, `(quote (a b c))`, ListVal([]Value{StringVal("a"), StringVal("b"), StringVal("c")}))
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
	testEval(t, `(len "hello")`, IntVal(5))
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
