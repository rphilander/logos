package logos

import "fmt"

// --- Frame serialization: Go frame ↔ logos Value ---

var frameKindToKeyword = map[frameKind]string{
	frameFnBody:       "fn-body",
	frameScopeCleanup: "scope-cleanup",
	frameRef:          "ref",
	frameEvalHead:     "eval-head",
	frameBuiltinArg:   "builtin-arg",
	frameFnArg:        "fn-arg",
	frameIfCond:       "if-cond",
	frameLetBind:      "let-bind",
	frameDo:           "do",
	frameFormExpand:    "form-expand",
	frameApplyFn:      "apply-fn",
	frameApplyList:    "apply-list",
	frameLoopBind:     "loop-bind",
	frameLoop:         "loop",
	frameRecurArg:     "recur-arg",
}

var keywordToFrameKind map[string]frameKind

func init() {
	keywordToFrameKind = make(map[string]frameKind, len(frameKindToKeyword))
	for k, v := range frameKindToKeyword {
		keywordToFrameKind[v] = k
	}
}

func nodesToValues(nodes []*Node) Value {
	elems := make([]Value, len(nodes))
	for i, n := range nodes {
		elems[i] = nodeToValue(n)
	}
	return ListVal(elems)
}

func valuesToNodes(v Value) ([]*Node, error) {
	if v.Kind != ValList {
		return nil, fmt.Errorf("expected list of AST nodes, got %s", v.KindName())
	}
	elems := *v.List
	nodes := make([]*Node, len(elems))
	for i, e := range elems {
		n, err := valueToNode(e)
		if err != nil {
			return nil, fmt.Errorf("node[%d]: %w", i, err)
		}
		nodes[i] = n
	}
	return nodes, nil
}

func bindingsToValue(m map[string]Value) Value {
	result := make(map[string]Value, len(m))
	for k, v := range m {
		result[k] = v
	}
	return MapVal(result)
}

func valueToBindings(v Value) (map[string]Value, error) {
	if v.Kind != ValMap {
		return nil, fmt.Errorf("expected map for bindings, got %s", v.KindName())
	}
	m := *v.Map
	result := make(map[string]Value, len(m))
	for k, v := range m {
		result[k] = v
	}
	return result, nil
}

func stringsToValue(ss []string) Value {
	elems := make([]Value, len(ss))
	for i, s := range ss {
		elems[i] = StringVal(s)
	}
	return ListVal(elems)
}

func valueToStrings(v Value) ([]string, error) {
	if v.Kind != ValList {
		return nil, fmt.Errorf("expected list of strings, got %s", v.KindName())
	}
	elems := *v.List
	result := make([]string, len(elems))
	for i, e := range elems {
		if e.Kind != ValString {
			return nil, fmt.Errorf("expected string at index %d, got %s", i, e.KindName())
		}
		result[i] = e.Str
	}
	return result, nil
}

func valuesToValues(v Value) ([]Value, error) {
	if v.Kind != ValList {
		return nil, fmt.Errorf("expected list, got %s", v.KindName())
	}
	elems := *v.List
	result := make([]Value, len(elems))
	copy(result, elems)
	return result, nil
}

// frameToValue converts a Go frame to a logos map value.
func frameToValue(f frame) Value {
	m := map[string]Value{
		"kind": KeywordVal(frameKindToKeyword[f.kind]),
	}

	switch f.kind {
	case frameFnBody:
		m["scope-base"] = IntVal(int64(f.scopeBase))
		m["saved-node-id"] = StringVal(f.savedNodeID)

	case frameScopeCleanup:
		// no extra fields

	case frameRef:
		m["ref-node-id"] = StringVal(f.refNodeID)
		m["saved-node-id"] = StringVal(f.savedNodeID)

	case frameEvalHead:
		m["head-node"] = nodeToValue(f.headNode)
		m["arg-nodes"] = nodesToValues(f.argNodes)

	case frameBuiltinArg:
		m["builtin"] = BuiltinVal(f.builtinName, f.builtin)
		m["done"] = ListVal(f.builtinDone)
		m["args"] = nodesToValues(f.builtinArgs)
		m["idx"] = IntVal(int64(f.builtinIdx))

	case frameFnArg:
		m["fn"] = FnVal(f.fn)
		m["done"] = ListVal(f.fnDone)
		m["args"] = nodesToValues(f.fnArgs)
		m["idx"] = IntVal(int64(f.fnIdx))

	case frameIfCond:
		m["then"] = nodeToValue(f.thenNode)
		m["else"] = nodeToValue(f.elseNode)

	case frameLetBind:
		m["bindings"] = bindingsToValue(f.bindings)
		m["bind-pairs"] = nodesToValues(f.bindPairs)
		m["bind-idx"] = IntVal(int64(f.bindIdx))
		m["body"] = nodeToValue(f.bodyNode)
		m["scope-idx"] = IntVal(int64(f.scopeIdx))

	case frameDo:
		m["exprs"] = nodesToValues(f.doExprs)
		m["idx"] = IntVal(int64(f.doIdx))

	case frameFormExpand:
		// no extra fields

	case frameApplyFn:
		m["list-node"] = nodeToValue(f.applyListNode)

	case frameApplyList:
		if f.builtin != nil {
			m["builtin"] = BuiltinVal(f.builtinName, f.builtin)
		} else {
			m["fn"] = FnVal(f.applyFn)
		}

	case frameLoopBind:
		m["bindings"] = bindingsToValue(f.bindings)
		m["bind-pairs"] = nodesToValues(f.bindPairs)
		m["bind-idx"] = IntVal(int64(f.bindIdx))
		m["loop-names"] = stringsToValue(f.loopNames)
		m["loop-body"] = nodeToValue(f.loopBody)
		m["loop-scope-idx"] = IntVal(int64(f.loopScopeIdx))

	case frameLoop:
		m["loop-names"] = stringsToValue(f.loopNames)
		m["loop-body"] = nodeToValue(f.loopBody)
		m["loop-scope-idx"] = IntVal(int64(f.loopScopeIdx))

	case frameRecurArg:
		m["recur-args"] = nodesToValues(f.recurArgs)
		doneElems := make([]Value, len(f.recurDone))
		copy(doneElems, f.recurDone)
		m["recur-done"] = ListVal(doneElems)
		m["recur-idx"] = IntVal(int64(f.recurIdx))
	}

	return MapVal(m)
}

// helper to extract a required field from a map
func mapGet(m map[string]Value, key string) (Value, error) {
	v, ok := m[key]
	if !ok {
		return Value{}, fmt.Errorf("missing required field %q", key)
	}
	return v, nil
}

func mapGetInt(m map[string]Value, key string) (int, error) {
	v, err := mapGet(m, key)
	if err != nil {
		return 0, err
	}
	if v.Kind != ValInt {
		return 0, fmt.Errorf("field %q: expected Int, got %s", key, v.KindName())
	}
	return int(v.Int), nil
}

func mapGetBool(m map[string]Value, key string) (bool, error) {
	v, err := mapGet(m, key)
	if err != nil {
		return false, err
	}
	if v.Kind != ValBool {
		return false, fmt.Errorf("field %q: expected Bool, got %s", key, v.KindName())
	}
	return v.Bool, nil
}

func mapGetString(m map[string]Value, key string) (string, error) {
	v, err := mapGet(m, key)
	if err != nil {
		return "", err
	}
	if v.Kind != ValString {
		return "", fmt.Errorf("field %q: expected String, got %s", key, v.KindName())
	}
	return v.Str, nil
}

func mapGetNode(m map[string]Value, key string) (*Node, error) {
	v, err := mapGet(m, key)
	if err != nil {
		return nil, err
	}
	return valueToNode(v)
}

func mapGetNodes(m map[string]Value, key string) ([]*Node, error) {
	v, err := mapGet(m, key)
	if err != nil {
		return nil, err
	}
	return valuesToNodes(v)
}

func mapGetFn(m map[string]Value, key string) (*FnValue, error) {
	v, err := mapGet(m, key)
	if err != nil {
		return nil, err
	}
	if v.Kind != ValFn && v.Kind != ValForm {
		return nil, fmt.Errorf("field %q: expected Fn/Form, got %s", key, v.KindName())
	}
	return v.Fn, nil
}

// valueToFrame converts a logos map back to a Go frame.
func valueToFrame(v Value, builtins map[string]Builtin) (frame, error) {
	if v.Kind != ValMap {
		return frame{}, fmt.Errorf("frame: expected Map, got %s", v.KindName())
	}
	m := *v.Map

	kindVal, err := mapGet(m, "kind")
	if err != nil {
		return frame{}, fmt.Errorf("frame: %w", err)
	}
	if kindVal.Kind != ValKeyword {
		return frame{}, fmt.Errorf("frame: :kind must be Keyword, got %s", kindVal.KindName())
	}
	fk, ok := keywordToFrameKind[kindVal.Str]
	if !ok {
		return frame{}, fmt.Errorf("frame: unknown kind %q", kindVal.Str)
	}

	f := frame{kind: fk}

	switch fk {
	case frameFnBody:
		f.scopeBase, err = mapGetInt(m, "scope-base")
		if err != nil {
			return frame{}, err
		}
		f.savedNodeID, err = mapGetString(m, "saved-node-id")
		if err != nil {
			return frame{}, err
		}

	case frameScopeCleanup:
		// no extra fields

	case frameRef:
		f.refNodeID, err = mapGetString(m, "ref-node-id")
		if err != nil {
			return frame{}, err
		}
		f.savedNodeID, err = mapGetString(m, "saved-node-id")
		if err != nil {
			return frame{}, err
		}

	case frameEvalHead:
		f.headNode, err = mapGetNode(m, "head-node")
		if err != nil {
			return frame{}, err
		}
		f.argNodes, err = mapGetNodes(m, "arg-nodes")
		if err != nil {
			return frame{}, err
		}

	case frameBuiltinArg:
		bVal, berr := mapGet(m, "builtin")
		if berr != nil {
			return frame{}, berr
		}
		if bVal.Kind == ValBuiltin {
			f.builtinName = bVal.BuiltinName
			f.builtin = bVal.BuiltinFunc
			// If func is nil (e.g. from what-if), resolve by name
			if f.builtin == nil && builtins != nil {
				f.builtin = builtins[f.builtinName]
			}
		} else {
			return frame{}, fmt.Errorf("frame builtin-arg: expected Builtin value for :builtin")
		}
		if f.builtin == nil {
			return frame{}, fmt.Errorf("frame builtin-arg: cannot resolve builtin %q", f.builtinName)
		}
		doneVal, derr := mapGet(m, "done")
		if derr != nil {
			return frame{}, derr
		}
		f.builtinDone, err = valuesToValues(doneVal)
		if err != nil {
			return frame{}, fmt.Errorf("frame builtin-arg done: %w", err)
		}
		f.builtinArgs, err = mapGetNodes(m, "args")
		if err != nil {
			return frame{}, err
		}
		f.builtinIdx, err = mapGetInt(m, "idx")
		if err != nil {
			return frame{}, err
		}

	case frameFnArg:
		f.fn, err = mapGetFn(m, "fn")
		if err != nil {
			return frame{}, err
		}
		doneVal, derr := mapGet(m, "done")
		if derr != nil {
			return frame{}, derr
		}
		f.fnDone, err = valuesToValues(doneVal)
		if err != nil {
			return frame{}, fmt.Errorf("frame fn-arg done: %w", err)
		}
		f.fnArgs, err = mapGetNodes(m, "args")
		if err != nil {
			return frame{}, err
		}
		f.fnIdx, err = mapGetInt(m, "idx")
		if err != nil {
			return frame{}, err
		}

	case frameIfCond:
		f.thenNode, err = mapGetNode(m, "then")
		if err != nil {
			return frame{}, err
		}
		f.elseNode, err = mapGetNode(m, "else")
		if err != nil {
			return frame{}, err
		}

	case frameLetBind:
		// bindings deserialized here as fallback; reconnected to locals in valueToEvalState
		bindsVal, berr := mapGet(m, "bindings")
		if berr != nil {
			return frame{}, berr
		}
		f.bindings, err = valueToBindings(bindsVal)
		if err != nil {
			return frame{}, err
		}
		f.bindPairs, err = mapGetNodes(m, "bind-pairs")
		if err != nil {
			return frame{}, err
		}
		f.bindIdx, err = mapGetInt(m, "bind-idx")
		if err != nil {
			return frame{}, err
		}
		f.bodyNode, err = mapGetNode(m, "body")
		if err != nil {
			return frame{}, err
		}
		f.scopeIdx, err = mapGetInt(m, "scope-idx")
		if err != nil {
			return frame{}, err
		}

	case frameDo:
		f.doExprs, err = mapGetNodes(m, "exprs")
		if err != nil {
			return frame{}, err
		}
		f.doIdx, err = mapGetInt(m, "idx")
		if err != nil {
			return frame{}, err
		}

	case frameFormExpand:
		// no extra fields

	case frameApplyFn:
		f.applyListNode, err = mapGetNode(m, "list-node")
		if err != nil {
			return frame{}, err
		}

	case frameApplyList:
		// Either "builtin" or "fn" is present
		if bVal, ok := m["builtin"]; ok && bVal.Kind == ValBuiltin {
			f.builtinName = bVal.BuiltinName
			f.builtin = bVal.BuiltinFunc
			if f.builtin == nil && builtins != nil {
				f.builtin = builtins[f.builtinName]
			}
		} else {
			f.applyFn, err = mapGetFn(m, "fn")
			if err != nil {
				return frame{}, err
			}
		}

	case frameLoopBind:
		bindsVal, berr := mapGet(m, "bindings")
		if berr != nil {
			return frame{}, berr
		}
		f.bindings, err = valueToBindings(bindsVal)
		if err != nil {
			return frame{}, err
		}
		f.bindPairs, err = mapGetNodes(m, "bind-pairs")
		if err != nil {
			return frame{}, err
		}
		f.bindIdx, err = mapGetInt(m, "bind-idx")
		if err != nil {
			return frame{}, err
		}
		namesVal, nerr := mapGet(m, "loop-names")
		if nerr != nil {
			return frame{}, nerr
		}
		f.loopNames, err = valueToStrings(namesVal)
		if err != nil {
			return frame{}, err
		}
		f.loopBody, err = mapGetNode(m, "loop-body")
		if err != nil {
			return frame{}, err
		}
		f.loopScopeIdx, err = mapGetInt(m, "loop-scope-idx")
		if err != nil {
			return frame{}, err
		}

	case frameLoop:
		namesVal, nerr := mapGet(m, "loop-names")
		if nerr != nil {
			return frame{}, nerr
		}
		f.loopNames, err = valueToStrings(namesVal)
		if err != nil {
			return frame{}, err
		}
		f.loopBody, err = mapGetNode(m, "loop-body")
		if err != nil {
			return frame{}, err
		}
		f.loopScopeIdx, err = mapGetInt(m, "loop-scope-idx")
		if err != nil {
			return frame{}, err
		}

	case frameRecurArg:
		f.recurArgs, err = mapGetNodes(m, "recur-args")
		if err != nil {
			return frame{}, err
		}
		doneVal, derr := mapGet(m, "recur-done")
		if derr != nil {
			return frame{}, derr
		}
		f.recurDone, err = valuesToValues(doneVal)
		if err != nil {
			return frame{}, err
		}
		f.recurIdx, err = mapGetInt(m, "recur-idx")
		if err != nil {
			return frame{}, err
		}
	}

	return f, nil
}

// --- Eval state serialization ---

func evalStateToValue(es *evalState, locals []map[string]Value, nodeID string, fuel int, fuelSet bool) Value {
	m := make(map[string]Value, 8)

	// Status
	if es.err != nil {
		m["status"] = KeywordVal("error")
		m["error"] = StringVal(es.err.Error())
		// Include result if available
		if es.result.Kind != ValNil || es.done {
			m["result"] = es.result
		}
	} else if es.done {
		m["status"] = KeywordVal("done")
		m["result"] = es.result
	} else if es.evaluating {
		m["status"] = KeywordVal("evaluating")
		m["node"] = nodeToValue(es.node)
	} else {
		m["status"] = KeywordVal("returning")
		m["result"] = es.result
		if es.node != nil {
			m["node"] = nodeToValue(es.node)
		}
	}

	// Stack
	stackElems := make([]Value, len(es.stack))
	for i, f := range es.stack {
		stackElems[i] = frameToValue(f)
	}
	m["stack"] = ListVal(stackElems)

	// Locals — list of scope maps
	localElems := make([]Value, len(locals))
	for i, scope := range locals {
		localElems[i] = bindingsToValue(scope)
	}
	m["locals"] = ListVal(localElems)

	// Node ID
	m["node-id"] = StringVal(nodeID)

	// Fuel
	if fuelSet {
		m["fuel"] = IntVal(int64(fuel))
	}

	return MapVal(m)
}

func valueToEvalState(v Value, builtins map[string]Builtin) (es *evalState, locals []map[string]Value, nodeID string, fuel *int, err error) {
	if v.Kind != ValMap {
		return nil, nil, "", nil, fmt.Errorf("step-continue: expected Map, got %s", v.KindName())
	}
	m := *v.Map

	es = &evalState{}

	// Status
	statusVal, serr := mapGet(m, "status")
	if serr != nil {
		return nil, nil, "", nil, serr
	}
	if statusVal.Kind != ValKeyword {
		return nil, nil, "", nil, fmt.Errorf("step-continue: :status must be Keyword")
	}

	switch statusVal.Str {
	case "evaluating":
		es.evaluating = true
		nodeVal, nerr := mapGet(m, "node")
		if nerr != nil {
			return nil, nil, "", nil, nerr
		}
		es.node, err = valueToNode(nodeVal)
		if err != nil {
			return nil, nil, "", nil, fmt.Errorf("step-continue: node: %w", err)
		}
	case "returning":
		es.evaluating = false
		resultVal, rerr := mapGet(m, "result")
		if rerr != nil {
			return nil, nil, "", nil, rerr
		}
		es.result = resultVal
		// Restore node if present (for context)
		if nodeVal, ok := m["node"]; ok {
			es.node, _ = valueToNode(nodeVal)
		}
	case "done":
		es.done = true
		if resultVal, ok := m["result"]; ok {
			es.result = resultVal
		}
		return es, nil, "", nil, nil
	case "error":
		errMsg := "unknown error"
		if errVal, ok := m["error"]; ok && errVal.Kind == ValString {
			errMsg = errVal.Str
		}
		es.err = fmt.Errorf("%s", errMsg)
		return es, nil, "", nil, nil
	default:
		return nil, nil, "", nil, fmt.Errorf("step-continue: unknown status %q", statusVal.Str)
	}

	// Stack
	stackVal, serr2 := mapGet(m, "stack")
	if serr2 != nil {
		return nil, nil, "", nil, serr2
	}
	if stackVal.Kind != ValList {
		return nil, nil, "", nil, fmt.Errorf("step-continue: :stack must be List")
	}
	stackElems := *stackVal.List
	es.stack = make([]frame, len(stackElems))
	for i, fv := range stackElems {
		es.stack[i], err = valueToFrame(fv, builtins)
		if err != nil {
			return nil, nil, "", nil, fmt.Errorf("step-continue: stack[%d]: %w", i, err)
		}
	}

	// Locals
	localsVal, lerr := mapGet(m, "locals")
	if lerr != nil {
		return nil, nil, "", nil, lerr
	}
	if localsVal.Kind != ValList {
		return nil, nil, "", nil, fmt.Errorf("step-continue: :locals must be List")
	}
	localsElems := *localsVal.List
	locals = make([]map[string]Value, len(localsElems))
	for i, lv := range localsElems {
		locals[i], err = valueToBindings(lv)
		if err != nil {
			return nil, nil, "", nil, fmt.Errorf("step-continue: locals[%d]: %w", i, err)
		}
	}

	// Reconnect let/loop frame bindings to their shared scope in locals.
	// The evaluator relies on frame.bindings and locals[scopeIdx] being the same map.
	for i := range es.stack {
		f := &es.stack[i]
		if f.kind == frameLetBind {
			if f.scopeIdx >= 0 && f.scopeIdx < len(locals) {
				f.bindings = locals[f.scopeIdx]
			}
		}
		if f.kind == frameLoopBind {
			if f.loopScopeIdx >= 0 && f.loopScopeIdx < len(locals) {
				f.bindings = locals[f.loopScopeIdx]
			}
		}
	}

	// Node ID
	if nidVal, ok := m["node-id"]; ok && nidVal.Kind == ValString {
		nodeID = nidVal.Str
	}

	// Fuel
	if fuelVal, ok := m["fuel"]; ok && fuelVal.Kind == ValInt {
		f := int(fuelVal.Int)
		fuel = &f
	}

	return es, locals, nodeID, fuel, nil
}

// --- Builtins ---

// builtinStepEval: (step-eval "expr") → state map
func (e *Evaluator) builtinStepEval(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("step-eval: expected 1 arg (string), got %d", len(args))
	}
	if args[0].Kind != ValString {
		return Value{}, fmt.Errorf("step-eval: arg must be String, got %s", args[0].KindName())
	}

	node, err := Parse(args[0].Str)
	if err != nil {
		return Value{}, fmt.Errorf("step-eval: parse error: %w", err)
	}

	es := &evalState{
		node:       node,
		evaluating: true,
	}

	// Initial state: no locals, empty currentNodeID, no fuel
	return evalStateToValue(es, nil, "", 0, false), nil
}

// builtinStepContinue: (step-continue state-map) → new state map
func (e *Evaluator) builtinStepContinue(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("step-continue: expected 1 arg (map), got %d", len(args))
	}
	if args[0].Kind != ValMap {
		return Value{}, fmt.Errorf("step-continue: arg must be Map, got %s", args[0].KindName())
	}

	// Reconstruct eval state
	es, locals, nodeID, fuel, err := valueToEvalState(args[0], e.Builtins)
	if err != nil {
		return Value{}, err
	}

	// If already done or error, return as-is
	if es.done || es.err != nil {
		return args[0], nil
	}

	// Save outer eval state
	savedLocals := e.locals
	savedNodeID := e.currentNodeID
	savedFuel := e.Fuel
	savedFuelSet := e.FuelSet

	// Install step state
	e.locals = locals
	e.currentNodeID = nodeID
	if fuel != nil {
		e.Fuel = *fuel
		e.FuelSet = true
	} else {
		e.FuelSet = false
	}

	// Run one step
	e.evalStep(es)

	// Capture new state
	newLocals := e.locals
	newNodeID := e.currentNodeID
	newFuel := e.Fuel
	newFuelSet := e.FuelSet

	// Restore outer eval state
	e.locals = savedLocals
	e.currentNodeID = savedNodeID
	e.Fuel = savedFuel
	e.FuelSet = savedFuelSet

	return evalStateToValue(es, newLocals, newNodeID, newFuel, newFuelSet), nil
}
