package logos

import (
	"testing"
)

func makeStepEval() *Evaluator {
	ev := &Evaluator{Builtins: DataBuiltins()}
	ev.Builtins["assert"] = ev.builtinAssert
	ev.Builtins["step-eval"] = ev.builtinStepEval
	ev.Builtins["step-continue"] = ev.builtinStepContinue
	return ev
}

// getField extracts a field from a map value.
func getField(v Value, key string) Value {
	if v.Kind != ValMap {
		return NilVal()
	}
	m := *v.Map
	if val, ok := m[key]; ok {
		return val
	}
	return NilVal()
}

func getStatus(v Value) string {
	f := getField(v, "status")
	if f.Kind == ValKeyword {
		return f.Str
	}
	return ""
}

// stepToCompletion runs step-continue until :done or :error, with a max step limit.
func stepToCompletion(t *testing.T, ev *Evaluator, state Value, maxSteps int) Value {
	t.Helper()
	for i := 0; i < maxSteps; i++ {
		status := getStatus(state)
		if status == "done" || status == "error" {
			return state
		}
		var err error
		state, err = ev.builtinStepContinue([]Value{state})
		if err != nil {
			t.Fatalf("step-continue error at step %d: %v", i, err)
		}
	}
	t.Fatalf("did not complete within %d steps, status: %s", maxSteps, getStatus(state))
	return state
}

// --- step-eval tests ---

func TestStepEvalBasic(t *testing.T) {
	ev := makeStepEval()
	state, err := ev.builtinStepEval([]Value{StringVal("42")})
	if err != nil {
		t.Fatalf("step-eval error: %v", err)
	}
	if getStatus(state) != "evaluating" {
		t.Fatalf("expected status :evaluating, got %s", getStatus(state))
	}
	// Node should be the literal 42
	node := getField(state, "node")
	if !ValuesEqual(node, IntVal(42)) {
		t.Fatalf("expected node 42, got %s", node.String())
	}
	// Stack should be empty
	stack := getField(state, "stack")
	if stack.Kind != ValList || len(*stack.List) != 0 {
		t.Fatalf("expected empty stack, got %s", stack.String())
	}
}

func TestStepEvalParseError(t *testing.T) {
	ev := makeStepEval()
	_, err := ev.builtinStepEval([]Value{StringVal("(")})
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestStepEvalWrongArity(t *testing.T) {
	ev := makeStepEval()
	_, err := ev.builtinStepEval([]Value{})
	if err == nil {
		t.Fatal("expected arity error")
	}
}

func TestStepEvalWrongType(t *testing.T) {
	ev := makeStepEval()
	_, err := ev.builtinStepEval([]Value{IntVal(42)})
	if err == nil {
		t.Fatal("expected type error")
	}
}

// --- step-continue tests ---

func TestStepContinueLiteral(t *testing.T) {
	ev := makeStepEval()
	state, err := ev.builtinStepEval([]Value{StringVal("42")})
	if err != nil {
		t.Fatalf("step-eval error: %v", err)
	}
	// Step 1: evaluate literal → returning (evaluating=false, stack empty)
	state, err = ev.builtinStepContinue([]Value{state})
	if err != nil {
		t.Fatalf("step-continue error: %v", err)
	}
	if getStatus(state) != "returning" {
		t.Fatalf("expected :returning after step 1, got %s", getStatus(state))
	}
	// Step 2: empty stack → done
	state, err = ev.builtinStepContinue([]Value{state})
	if err != nil {
		t.Fatalf("step-continue error: %v", err)
	}
	if getStatus(state) != "done" {
		t.Fatalf("expected :done after step 2, got %s", getStatus(state))
	}
	result := getField(state, "result")
	if !ValuesEqual(result, IntVal(42)) {
		t.Fatalf("expected 42, got %s", result.String())
	}
}

func TestStepContinueAdd(t *testing.T) {
	ev := makeStepEval()
	state, err := ev.builtinStepEval([]Value{StringVal("(add 1 2)")})
	if err != nil {
		t.Fatalf("step-eval error: %v", err)
	}
	state = stepToCompletion(t, ev, state, 100)
	result := getField(state, "result")
	if !ValuesEqual(result, IntVal(3)) {
		t.Fatalf("expected 3, got %s", result.String())
	}
}

func TestStepContinueFnCall(t *testing.T) {
	ev := makeStepEval()
	state, err := ev.builtinStepEval([]Value{StringVal("(let ((f (fn (x) (add x 1)))) (f 10))")})
	if err != nil {
		t.Fatalf("step-eval error: %v", err)
	}
	state = stepToCompletion(t, ev, state, 200)
	result := getField(state, "result")
	if !ValuesEqual(result, IntVal(11)) {
		t.Fatalf("expected 11, got %s", result.String())
	}
}

func TestStepContinueIfTrue(t *testing.T) {
	ev := makeStepEval()
	state, err := ev.builtinStepEval([]Value{StringVal("(if true 1 2)")})
	if err != nil {
		t.Fatalf("step-eval error: %v", err)
	}
	state = stepToCompletion(t, ev, state, 100)
	result := getField(state, "result")
	if !ValuesEqual(result, IntVal(1)) {
		t.Fatalf("expected 1, got %s", result.String())
	}
}

func TestStepContinueIfFalse(t *testing.T) {
	ev := makeStepEval()
	state, err := ev.builtinStepEval([]Value{StringVal("(if false 1 2)")})
	if err != nil {
		t.Fatalf("step-eval error: %v", err)
	}
	state = stepToCompletion(t, ev, state, 100)
	result := getField(state, "result")
	if !ValuesEqual(result, IntVal(2)) {
		t.Fatalf("expected 2, got %s", result.String())
	}
}

func TestStepContinueDo(t *testing.T) {
	ev := makeStepEval()
	state, err := ev.builtinStepEval([]Value{StringVal("(do 1 2 3)")})
	if err != nil {
		t.Fatalf("step-eval error: %v", err)
	}
	state = stepToCompletion(t, ev, state, 100)
	result := getField(state, "result")
	if !ValuesEqual(result, IntVal(3)) {
		t.Fatalf("expected 3, got %s", result.String())
	}
}

func TestStepContinueLetrec(t *testing.T) {
	ev := makeStepEval()
	// Simple letrec without recursion
	state, err := ev.builtinStepEval([]Value{StringVal("(letrec ((x 10) (y 20)) (add x y))")})
	if err != nil {
		t.Fatalf("step-eval error: %v", err)
	}
	state = stepToCompletion(t, ev, state, 200)
	result := getField(state, "result")
	if !ValuesEqual(result, IntVal(30)) {
		t.Fatalf("expected 30, got %s", result.String())
	}
}

func TestStepContinueNestedFn(t *testing.T) {
	ev := makeStepEval()
	state, err := ev.builtinStepEval([]Value{StringVal("((fn (x) ((fn (y) (add x y)) 20)) 10)")})
	if err != nil {
		t.Fatalf("step-eval error: %v", err)
	}
	state = stepToCompletion(t, ev, state, 300)
	result := getField(state, "result")
	if !ValuesEqual(result, IntVal(30)) {
		t.Fatalf("expected 30, got %s", result.String())
	}
}

func TestStepContinueQuote(t *testing.T) {
	ev := makeStepEval()
	state, err := ev.builtinStepEval([]Value{StringVal("(quote foo)")})
	if err != nil {
		t.Fatalf("step-eval error: %v", err)
	}
	state = stepToCompletion(t, ev, state, 100)
	result := getField(state, "result")
	if !ValuesEqual(result, SymbolVal("foo")) {
		t.Fatalf("expected symbol foo, got %s", result.String())
	}
}

func TestStepContinueApplyFn(t *testing.T) {
	ev := makeStepEval()
	state, err := ev.builtinStepEval([]Value{StringVal("(apply (fn (a b) (add a b)) (list 1 2))")})
	if err != nil {
		t.Fatalf("step-eval error: %v", err)
	}
	state = stepToCompletion(t, ev, state, 200)
	result := getField(state, "result")
	if !ValuesEqual(result, IntVal(3)) {
		t.Fatalf("expected 3, got %s", result.String())
	}
}

func TestStepContinueApplyBuiltin(t *testing.T) {
	ev := makeStepEval()
	state, err := ev.builtinStepEval([]Value{StringVal("(apply add (list 1 2))")})
	if err != nil {
		t.Fatalf("step-eval error: %v", err)
	}
	state = stepToCompletion(t, ev, state, 200)
	result := getField(state, "result")
	if !ValuesEqual(result, IntVal(3)) {
		t.Fatalf("expected 3, got %s", result.String())
	}
}

// --- done/error passthrough ---

func TestStepContinueDonePassthrough(t *testing.T) {
	ev := makeStepEval()
	state, err := ev.builtinStepEval([]Value{StringVal("42")})
	if err != nil {
		t.Fatalf("step-eval error: %v", err)
	}
	state = stepToCompletion(t, ev, state, 10)
	// Calling step-continue on a done state returns it unchanged
	state2, err := ev.builtinStepContinue([]Value{state})
	if err != nil {
		t.Fatalf("step-continue on done state error: %v", err)
	}
	if getStatus(state2) != "done" {
		t.Fatalf("expected :done, got %s", getStatus(state2))
	}
	result := getField(state2, "result")
	if !ValuesEqual(result, IntVal(42)) {
		t.Fatalf("expected 42, got %s", result.String())
	}
}

func TestStepContinueErrorPassthrough(t *testing.T) {
	ev := makeStepEval()
	// Step through an expression that will error (unbound symbol)
	state, err := ev.builtinStepEval([]Value{StringVal("undefined_var")})
	if err != nil {
		t.Fatalf("step-eval error: %v", err)
	}
	state = stepToCompletion(t, ev, state, 10)
	if getStatus(state) != "error" {
		t.Fatalf("expected :error, got %s", getStatus(state))
	}
	// Calling step-continue on error state returns it unchanged
	state2, err := ev.builtinStepContinue([]Value{state})
	if err != nil {
		t.Fatalf("step-continue on error state error: %v", err)
	}
	if getStatus(state2) != "error" {
		t.Fatalf("expected :error, got %s", getStatus(state2))
	}
}

// --- State inspection ---

func TestStepStateHasRequiredFields(t *testing.T) {
	ev := makeStepEval()
	state, err := ev.builtinStepEval([]Value{StringVal("(add 1 2)")})
	if err != nil {
		t.Fatalf("step-eval error: %v", err)
	}

	// Required fields should be present
	for _, key := range []string{"status", "node", "stack", "locals", "node-id", "tail"} {
		if getField(state, key).Kind == ValNil && key != "node-id" {
			t.Errorf("missing field %q in initial state", key)
		}
	}

	// Step once and check again
	state, err = ev.builtinStepContinue([]Value{state})
	if err != nil {
		t.Fatalf("step-continue error: %v", err)
	}
	stack := getField(state, "stack")
	if stack.Kind != ValList {
		t.Fatalf("expected stack to be List, got %s", stack.KindName())
	}
}

// --- Fuel ---

func TestStepFuel(t *testing.T) {
	ev := makeStepEval()
	state, err := ev.builtinStepEval([]Value{StringVal("(add 1 2)")})
	if err != nil {
		t.Fatalf("step-eval error: %v", err)
	}

	// Inject fuel into the state
	m := *state.Map
	m["fuel"] = IntVal(2)
	state = MapVal(m)

	// Step — each eval-phase step should decrement fuel
	state, err = ev.builtinStepContinue([]Value{state})
	if err != nil {
		t.Fatalf("step 1 error: %v", err)
	}

	// Should still have fuel (decremented but not exhausted)
	fuelVal := getField(state, "fuel")
	if fuelVal.Kind != ValInt {
		t.Fatalf("expected fuel to be Int, got %s", fuelVal.KindName())
	}
	if fuelVal.Int >= 2 {
		t.Fatalf("expected fuel to decrease, still %d", fuelVal.Int)
	}
}

func TestStepFuelExhaustion(t *testing.T) {
	ev := makeStepEval()
	state, err := ev.builtinStepEval([]Value{StringVal("(add (add 1 2) 3)")})
	if err != nil {
		t.Fatalf("step-eval error: %v", err)
	}

	// Set fuel=1 so it will run out quickly
	m := *state.Map
	m["fuel"] = IntVal(1)
	state = MapVal(m)

	// Keep stepping until done or error
	state = stepToCompletion(t, ev, state, 100)
	// With only 1 fuel, should eventually error
	if getStatus(state) != "error" {
		t.Fatalf("expected :error from fuel exhaustion, got %s", getStatus(state))
	}
}

// --- What-if: modify locals ---

func TestStepWhatIfModifyLocals(t *testing.T) {
	ev := makeStepEval()
	// Start with let that binds x=10, then computes (add x 1)
	state, err := ev.builtinStepEval([]Value{StringVal("(let ((x 10)) (add x 1))")})
	if err != nil {
		t.Fatalf("step-eval error: %v", err)
	}

	// Step until we're past the let binding and into the body
	// We'll step until the locals include x
	var foundX bool
	for i := 0; i < 50; i++ {
		status := getStatus(state)
		if status == "done" || status == "error" {
			break
		}

		// Check if locals contain x
		locals := getField(state, "locals")
		if locals.Kind == ValList {
			for _, scope := range *locals.List {
				if scope.Kind == ValMap {
					if v, ok := (*scope.Map)["x"]; ok && ValuesEqual(v, IntVal(10)) {
						foundX = true
						// Modify x to 99
						newScope := make(map[string]Value)
						for k, v := range *scope.Map {
							newScope[k] = v
						}
						newScope["x"] = IntVal(99)

						// Rebuild locals with modified scope
						newLocals := make([]Value, len(*locals.List))
						copy(newLocals, *locals.List)
						for j, s := range newLocals {
							if s.Kind == ValMap {
								if _, hasX := (*s.Map)["x"]; hasX {
									newLocals[j] = MapVal(newScope)
								}
							}
						}

						// Rebuild state with new locals
						newM := make(map[string]Value)
						for k, v := range *state.Map {
							newM[k] = v
						}
						newM["locals"] = ListVal(newLocals)
						state = MapVal(newM)
						break
					}
				}
			}
		}
		if foundX {
			break
		}

		state, err = ev.builtinStepContinue([]Value{state})
		if err != nil {
			t.Fatalf("step-continue error at step %d: %v", i, err)
		}
	}

	if !foundX {
		t.Fatal("never found x=10 in locals")
	}

	// Now step to completion — result should be (add 99 1) = 100
	state = stepToCompletion(t, ev, state, 200)
	result := getField(state, "result")
	if !ValuesEqual(result, IntVal(100)) {
		t.Fatalf("expected 100 (what-if: x=99, add x 1), got %s", result.String())
	}
}

// --- step-continue wrong arity/type ---

func TestStepContinueWrongArity(t *testing.T) {
	ev := makeStepEval()
	_, err := ev.builtinStepContinue([]Value{})
	if err == nil {
		t.Fatal("expected arity error")
	}
}

func TestStepContinueWrongType(t *testing.T) {
	ev := makeStepEval()
	_, err := ev.builtinStepContinue([]Value{IntVal(42)})
	if err == nil {
		t.Fatal("expected type error")
	}
}

// --- frame roundtrip ---

func TestFrameRoundtrip(t *testing.T) {
	builtins := DataBuiltins()

	// Test various frame types roundtrip through serialization
	frames := []frame{
		{kind: frameFnBody, tail: true, scopeBase: 2, savedNodeID: "node:x-1"},
		{kind: frameScopeCleanup},
		{kind: frameRef, refNodeID: "node:foo-3", savedNodeID: "node:bar-1"},
		{kind: frameIfCond, tail: true, thenNode: &Node{Kind: NodeInt, Int: 1}, elseNode: &Node{Kind: NodeInt, Int: 2}},
		{kind: frameDo, tail: false, doExprs: []*Node{{Kind: NodeInt, Int: 1}, {Kind: NodeInt, Int: 2}}, doIdx: 0},
		{kind: frameFormExpand, tail: true},
		{kind: frameApplyFn, tail: false, applyListNode: &Node{Kind: NodeSymbol, Str: "xs"}},
		{kind: frameApplyList, tail: true, applyFn: &FnValue{Params: []string{"x"}, Body: &Node{Kind: NodeSymbol, Str: "x"}}},
	}

	for i, f := range frames {
		v := frameToValue(f)
		f2, err := valueToFrame(v, builtins)
		if err != nil {
			t.Fatalf("frame[%d] roundtrip error: %v", i, err)
		}
		if f2.kind != f.kind {
			t.Fatalf("frame[%d] kind mismatch: %d vs %d", i, f2.kind, f.kind)
		}
		if f2.tail != f.tail {
			t.Fatalf("frame[%d] tail mismatch: %v vs %v", i, f2.tail, f.tail)
		}
	}
}

func TestFrameBuiltinArgRoundtrip(t *testing.T) {
	builtins := DataBuiltins()

	f := frame{
		kind:        frameBuiltinArg,
		builtin:     builtins["add"],
		builtinName: "add",
		builtinDone: []Value{IntVal(1)},
		builtinArgs: []*Node{{Kind: NodeInt, Int: 2}},
		builtinIdx:  1,
	}

	v := frameToValue(f)
	f2, err := valueToFrame(v, builtins)
	if err != nil {
		t.Fatalf("builtin-arg roundtrip error: %v", err)
	}
	if f2.kind != frameBuiltinArg {
		t.Fatal("kind mismatch")
	}
	if f2.builtinName != "add" {
		t.Fatalf("builtinName: expected add, got %s", f2.builtinName)
	}
	if f2.builtin == nil {
		t.Fatal("builtin func is nil after roundtrip")
	}
	if f2.builtinIdx != 1 {
		t.Fatalf("builtinIdx: expected 1, got %d", f2.builtinIdx)
	}
}

// --- Integration: step-eval via eval string ---

func TestStepEvalViaEvalString(t *testing.T) {
	ev := makeStepEval()
	// Use the builtins through EvalString to test they're callable
	state, err := ev.EvalString(`(step-eval "(add 1 2)")`)
	if err != nil {
		t.Fatalf("step-eval via EvalString: %v", err)
	}
	if state.Kind != ValMap {
		t.Fatalf("expected Map, got %s", state.KindName())
	}
	if getStatus(state) != "evaluating" {
		t.Fatalf("expected :evaluating, got %s", getStatus(state))
	}
}

// --- Recursive fn ---

func TestStepRecursiveFn(t *testing.T) {
	ev := makeStepEval()
	// Factorial via letrec
	expr := `(letrec ((fact (fn (n) (if (eq n 0) 1 (mul n (fact (sub n 1))))))) (fact 5))`
	state, err := ev.builtinStepEval([]Value{StringVal(expr)})
	if err != nil {
		t.Fatalf("step-eval error: %v", err)
	}
	state = stepToCompletion(t, ev, state, 2000)
	result := getField(state, "result")
	if !ValuesEqual(result, IntVal(120)) {
		t.Fatalf("expected 120, got %s", result.String())
	}
}

// --- Rest params ---

func TestStepRestParams(t *testing.T) {
	ev := makeStepEval()
	state, err := ev.builtinStepEval([]Value{StringVal("((fn (x & rest) (len rest)) 1 2 3 4)")})
	if err != nil {
		t.Fatalf("step-eval error: %v", err)
	}
	state = stepToCompletion(t, ev, state, 200)
	result := getField(state, "result")
	if !ValuesEqual(result, IntVal(3)) {
		t.Fatalf("expected 3, got %s", result.String())
	}
}

// --- Form (macro) ---

func TestStepForm(t *testing.T) {
	ev := makeStepEval()
	// A simple form that wraps in a list
	state, err := ev.builtinStepEval([]Value{StringVal("(let ((my-when (form (cond body) (list (quote if) cond body nil)))) (my-when true 42))")})
	if err != nil {
		t.Fatalf("step-eval error: %v", err)
	}
	state = stepToCompletion(t, ev, state, 300)
	result := getField(state, "result")
	if !ValuesEqual(result, IntVal(42)) {
		t.Fatalf("expected 42, got %s", result.String())
	}
}
