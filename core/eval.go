package logos

import (
	"fmt"
	"sort"
	"strings"
)

// Builtin is a function implemented in Go, called with eagerly evaluated arguments.
type Builtin func(args []Value) (Value, error)

// Resolver looks up a symbol name and returns its value if defined.
type Resolver func(name string) (Value, bool)

// NodeResolver looks up a node by ID and returns its expression AST.
type NodeResolver func(nodeID string) (*Node, error)

// Evaluator evaluates s-expression nodes.
type Evaluator struct {
	Resolve       Resolver
	ResolveNode   NodeResolver
	Builtins      map[string]Builtin
	locals        []map[string]Value
	activeTrace   *Trace  // set by Core during external evals
	currentNodeID string  // tracks innermost graph node being evaluated
	Fuel          int     // remaining eval steps when FuelSet is true
	FuelSet       bool    // whether fuel limiting is active
}

func (e *Evaluator) pushScope(bindings map[string]Value) {
	e.locals = append(e.locals, bindings)
}

func (e *Evaluator) popScope() {
	e.locals = e.locals[:len(e.locals)-1]
}

func (e *Evaluator) lookupLocal(name string) (Value, bool) {
	for i := len(e.locals) - 1; i >= 0; i-- {
		if val, ok := e.locals[i][name]; ok {
			return val, true
		}
	}
	return Value{}, false
}

func (e *Evaluator) Eval(node *Node) (Value, error) {
	if e.FuelSet {
		if e.Fuel <= 0 {
			return Value{}, fmt.Errorf("fuel exhausted")
		}
		e.Fuel--
	}
	switch node.Kind {
	case NodeInt:
		return IntVal(node.Int), nil
	case NodeFloat:
		return FloatVal(node.Float), nil
	case NodeBool:
		return BoolVal(node.Bool), nil
	case NodeString:
		return StringVal(node.Str), nil
	case NodeNil:
		return NilVal(), nil
	case NodeKeyword:
		return KeywordVal(node.Str), nil
	case NodeSymbol:
		return e.resolveSymbol(node.Str)
	case NodeRef:
		return e.evalRef(node.Ref)
	case NodeList:
		return e.evalList(node)
	default:
		return Value{}, fmt.Errorf("unknown node kind: %d", node.Kind)
	}
}

func (e *Evaluator) resolveSymbol(name string) (Value, error) {
	if val, ok := e.lookupLocal(name); ok {
		return val, nil
	}
	if e.Resolve != nil {
		if val, ok := e.Resolve(name); ok {
			return val, nil
		}
	}
	return Value{}, fmt.Errorf("unbound symbol: %s", name)
}

func (e *Evaluator) evalRef(nodeID string) (Value, error) {
	if e.ResolveNode == nil {
		return Value{}, fmt.Errorf("cannot resolve node ref %s: no node resolver", nodeID)
	}
	expr, err := e.ResolveNode(nodeID)
	if err != nil {
		return Value{}, err
	}
	prev := e.currentNodeID
	e.currentNodeID = nodeID
	val, err := e.Eval(expr)
	e.currentNodeID = prev
	if err != nil {
		return Value{}, err
	}
	if val.Kind == ValFn || val.Kind == ValForm {
		val.Fn.NodeID = nodeID
	}
	return val, nil
}

func (e *Evaluator) evalList(node *Node) (Value, error) {
	if len(node.Children) == 0 {
		return Value{}, fmt.Errorf("cannot eval empty list")
	}

	head := node.Children[0]

	// Core forms
	if head.Kind == NodeSymbol {
		switch head.Str {
		case "if":
			return e.evalIf(node)
		case "let":
			return e.evalLet(node)
		case "letrec":
			return e.evalLetrec(node)
		case "do":
			return e.evalDo(node)
		case "fn":
			return e.evalFn(node)
		case "form":
			return e.evalForm(node)
		case "quote":
			return e.evalQuote(node)
		case "apply":
			return e.evalApply(node)
		case "sort-by":
			return e.evalSortBy(node)
		}
	}

	// Builtins
	if head.Kind == NodeSymbol && e.Builtins != nil {
		if fn, ok := e.Builtins[head.Str]; ok {
			return e.callBuiltin(fn, node.Children[1:])
		}
	}

	// Evaluate head — should produce a function
	headVal, err := e.Eval(head)
	if err != nil {
		if head.Kind == NodeSymbol {
			return Value{}, fmt.Errorf("unknown function: %s", head.Str)
		}
		return Value{}, err
	}
	if headVal.Kind == ValFn {
		return e.callFn(headVal.Fn, node.Children[1:])
	}
	if headVal.Kind == ValForm {
		return e.callForm(headVal.Fn, node.Children[1:])
	}

	if head.Kind == NodeSymbol {
		return Value{}, fmt.Errorf("cannot call %s: not a function", head.Str)
	}
	return Value{}, fmt.Errorf("cannot call %s value", headVal.KindName())
}

func (e *Evaluator) callBuiltin(fn Builtin, argNodes []*Node) (Value, error) {
	args := make([]Value, len(argNodes))
	for i, child := range argNodes {
		val, err := e.Eval(child)
		if err != nil {
			return Value{}, err
		}
		args[i] = val
	}
	return fn(args)
}

// tailContinuation signals a tail call to the trampoline loop.
// Exported fields for future ToValue() conversion (step evaluator).
type tailContinuation struct {
	Fn   *FnValue
	Args []Value
}

func (e *Evaluator) callFn(fn *FnValue, argNodes []*Node) (Value, error) {
	if fn.RestParam != "" {
		if len(argNodes) < len(fn.Params) {
			return Value{}, fmt.Errorf("fn: expected at least %d args, got %d", len(fn.Params), len(argNodes))
		}
	} else {
		if len(argNodes) != len(fn.Params) {
			return Value{}, fmt.Errorf("fn: expected %d args, got %d", len(fn.Params), len(argNodes))
		}
	}
	args := make([]Value, len(argNodes))
	for i, argNode := range argNodes {
		val, err := e.Eval(argNode)
		if err != nil {
			return Value{}, err
		}
		args[i] = val
	}
	return e.callFnTrampolined(fn, args)
}

// CallFnWithValues calls a user-defined function with pre-evaluated Values.
func (e *Evaluator) CallFnWithValues(fn *FnValue, args []Value) (Value, error) {
	return e.callFnTrampolined(fn, args)
}

// callFnTrampolined is the TCO trampoline. It evaluates the function body
// in tail position. If the body is a tail call, it loops instead of recursing.
func (e *Evaluator) callFnTrampolined(fn *FnValue, args []Value) (Value, error) {
	baseScope := len(e.locals)
	defer func() { e.locals = e.locals[:baseScope] }()

	for {
		if fn.RestParam != "" {
			if len(args) < len(fn.Params) {
				return Value{}, fmt.Errorf("fn: expected at least %d args, got %d", len(fn.Params), len(args))
			}
		} else {
			if len(args) != len(fn.Params) {
				return Value{}, fmt.Errorf("fn: expected %d args, got %d", len(fn.Params), len(args))
			}
		}

		// Reset scope to base (cleans up from previous iteration)
		e.locals = e.locals[:baseScope]

		// Push closure scope
		if fn.Closure != nil {
			e.pushScope(fn.Closure)
		}

		// Push bindings scope
		bindings := make(map[string]Value, len(fn.Params)+1)
		for i, param := range fn.Params {
			bindings[param] = args[i]
		}
		if fn.RestParam != "" {
			bindings[fn.RestParam] = ListVal(args[len(fn.Params):])
		}
		e.pushScope(bindings)

		// Save/set currentNodeID
		prev := e.currentNodeID
		if fn.NodeID != "" {
			e.currentNodeID = fn.NodeID
		}

		// Evaluate body in tail position
		val, cont, err := e.evalTail(fn.Body)

		// Restore currentNodeID
		e.currentNodeID = prev

		if err != nil {
			return Value{}, err
		}
		if cont == nil {
			return val, nil
		}

		// Tail call: loop with new fn and args
		fn = cont.Fn
		args = cont.Args
	}
}

// evalIf: (if cond then else) — uses Truthy, not Bool-only.
func (e *Evaluator) evalIf(node *Node) (Value, error) {
	if len(node.Children) != 4 {
		return Value{}, fmt.Errorf("if: expected 3 args (cond then else), got %d", len(node.Children)-1)
	}
	cond, err := e.Eval(node.Children[1])
	if err != nil {
		return Value{}, err
	}
	if cond.Truthy() {
		return e.Eval(node.Children[2])
	}
	return e.Eval(node.Children[3])
}

// evalLet: (let ((x expr1) (y expr2)) body) — sequential bindings.
func (e *Evaluator) evalLet(node *Node) (Value, error) {
	if len(node.Children) != 3 {
		return Value{}, fmt.Errorf("let: expected bindings and body")
	}
	bindingsNode := node.Children[1]
	if bindingsNode.Kind != NodeList {
		return Value{}, fmt.Errorf("let: bindings must be a list")
	}
	bindings := make(map[string]Value, len(bindingsNode.Children))
	e.pushScope(bindings)
	defer e.popScope()

	for _, pair := range bindingsNode.Children {
		if pair.Kind != NodeList || len(pair.Children) != 2 {
			return Value{}, fmt.Errorf("let: each binding must be (name expr)")
		}
		nameNode := pair.Children[0]
		if nameNode.Kind != NodeSymbol {
			return Value{}, fmt.Errorf("let: binding name must be a symbol")
		}
		val, err := e.Eval(pair.Children[1])
		if err != nil {
			return Value{}, err
		}
		bindings[nameNode.Str] = val
	}
	return e.Eval(node.Children[2])
}

// evalLetrec: (letrec ((f expr1) (g expr2)) body) — recursive bindings.
// Like let, but back-patches closures so bindings can reference each other.
func (e *Evaluator) evalLetrec(node *Node) (Value, error) {
	if _, err := e.evalLetrecBindings(node); err != nil {
		return Value{}, err
	}
	defer e.popScope()
	return e.Eval(node.Children[2])
}

// evalLetrecBindings: shared setup for letrec and letrec-tail.
// Pushes scope, evaluates bindings, back-patches closures. Caller must pop scope.
func (e *Evaluator) evalLetrecBindings(node *Node) (map[string]Value, error) {
	if len(node.Children) != 3 {
		return nil, fmt.Errorf("letrec: expected bindings and body")
	}
	bindingsNode := node.Children[1]
	if bindingsNode.Kind != NodeList {
		return nil, fmt.Errorf("letrec: bindings must be a list")
	}

	// Push scope with nil placeholders so fn captures include these names.
	scope := make(map[string]Value, len(bindingsNode.Children))
	names := make([]string, len(bindingsNode.Children))
	for i, pair := range bindingsNode.Children {
		if pair.Kind != NodeList || len(pair.Children) != 2 {
			return nil, fmt.Errorf("letrec: each binding must be (name expr)")
		}
		nameNode := pair.Children[0]
		if nameNode.Kind != NodeSymbol {
			return nil, fmt.Errorf("letrec: binding name must be a symbol")
		}
		names[i] = nameNode.Str
	}
	e.pushScope(scope)

	// Evaluate bindings sequentially (like let).
	for i, pair := range bindingsNode.Children {
		val, err := e.Eval(pair.Children[1])
		if err != nil {
			return nil, err
		}
		scope[names[i]] = val
	}

	// Back-patch: update closures of FnValues and FormValues to see all final bindings.
	for _, val := range scope {
		if (val.Kind == ValFn || val.Kind == ValForm) && val.Fn != nil {
			if val.Fn.Closure == nil {
				val.Fn.Closure = make(map[string]Value, len(names))
			}
			for _, name := range names {
				val.Fn.Closure[name] = scope[name]
			}
		}
	}
	return scope, nil
}

// evalDo: (do expr1 expr2 ... exprN) — eval all, return last.
func (e *Evaluator) evalDo(node *Node) (Value, error) {
	if len(node.Children) < 2 {
		return Value{}, fmt.Errorf("do: expected at least one expression")
	}
	var result Value
	var err error
	for _, child := range node.Children[1:] {
		result, err = e.Eval(child)
		if err != nil {
			return Value{}, err
		}
	}
	return result, nil
}

// parseFnParams parses a param list, supporting & rest syntax.
// Returns (positional params, rest param name, error).
func parseFnParams(kind string, paramsNode *Node) ([]string, string, error) {
	if paramsNode.Kind != NodeList {
		return nil, "", fmt.Errorf("%s: params must be a list", kind)
	}
	var params []string
	var restParam string
	children := paramsNode.Children
	for i := 0; i < len(children); i++ {
		p := children[i]
		if p.Kind == NodeSymbol && p.Str == "&" {
			// & must be followed by exactly one symbol
			if i+1 != len(children)-1 {
				return nil, "", fmt.Errorf("%s: & must be followed by exactly one rest param at end", kind)
			}
			rest := children[i+1]
			if rest.Kind != NodeSymbol {
				return nil, "", fmt.Errorf("%s: rest param name must be a symbol", kind)
			}
			restParam = rest.Str
			break
		}
		if p.Kind != NodeSymbol {
			return nil, "", fmt.Errorf("%s: param names must be symbols", kind)
		}
		params = append(params, p.Str)
	}
	return params, restParam, nil
}

// evalFn: (fn (params...) body) — create closure.
func (e *Evaluator) evalFn(node *Node) (Value, error) {
	if len(node.Children) != 3 {
		return Value{}, fmt.Errorf("fn: expected (fn (params...) body)")
	}
	params, restParam, err := parseFnParams("fn", node.Children[1])
	if err != nil {
		return Value{}, err
	}
	// Capture enclosing local scope for closure.
	var closure map[string]Value
	if len(e.locals) > 0 {
		closure = make(map[string]Value)
		for _, scope := range e.locals {
			for k, v := range scope {
				closure[k] = v
			}
		}
	}
	return FnVal(&FnValue{
		Params:    params,
		RestParam: restParam,
		Body:      node.Children[2],
		Closure:   closure,
	}), nil
}

// evalForm: (form (params...) body) — create a form (macro) value.
func (e *Evaluator) evalForm(node *Node) (Value, error) {
	if len(node.Children) != 3 {
		return Value{}, fmt.Errorf("form: expected (form (params...) body)")
	}
	params, restParam, err := parseFnParams("form", node.Children[1])
	if err != nil {
		return Value{}, err
	}
	var closure map[string]Value
	if len(e.locals) > 0 {
		closure = make(map[string]Value)
		for _, scope := range e.locals {
			for k, v := range scope {
				closure[k] = v
			}
		}
	}
	return FormVal(&FnValue{
		Params:    params,
		RestParam: restParam,
		Body:      node.Children[2],
		Closure:   closure,
	}), nil
}

// evalQuote: (quote expr) — return the expression as a value.
// Converts the AST node to a logos Value representation.
func (e *Evaluator) evalQuote(node *Node) (Value, error) {
	if len(node.Children) != 2 {
		return Value{}, fmt.Errorf("quote: expected 1 arg")
	}
	return nodeToValue(node.Children[1]), nil
}

// --- Tail-call optimization (TCO) ---

// evalTail evaluates a node in tail position. Returns either a value or a
// tail-call continuation for the trampoline to execute.
func (e *Evaluator) evalTail(node *Node) (Value, *tailContinuation, error) {
	if node.Kind == NodeList {
		return e.evalListTail(node)
	}
	// Literals, symbols, refs — no tail-call opportunity
	val, err := e.Eval(node)
	return val, nil, err
}

// evalListTail dispatches list evaluation in tail position.
func (e *Evaluator) evalListTail(node *Node) (Value, *tailContinuation, error) {
	if e.FuelSet {
		if e.Fuel <= 0 {
			return Value{}, nil, fmt.Errorf("fuel exhausted")
		}
		e.Fuel--
	}
	if len(node.Children) == 0 {
		return Value{}, nil, fmt.Errorf("cannot eval empty list")
	}

	head := node.Children[0]

	// Core forms with tail positions
	if head.Kind == NodeSymbol {
		switch head.Str {
		case "if":
			return e.evalIfTail(node)
		case "let":
			return e.evalLetTail(node)
		case "letrec":
			return e.evalLetrecTail(node)
		case "do":
			return e.evalDoTail(node)
		// Non-tail core forms: delegate to regular eval
		case "fn", "form", "quote", "apply", "sort-by":
			val, err := e.evalList(node)
			return val, nil, err
		}
	}

	// Builtins
	if head.Kind == NodeSymbol && e.Builtins != nil {
		if fn, ok := e.Builtins[head.Str]; ok {
			val, err := e.callBuiltin(fn, node.Children[1:])
			return val, nil, err
		}
	}

	// Evaluate head — should produce a function
	headVal, err := e.Eval(head)
	if err != nil {
		if head.Kind == NodeSymbol {
			return Value{}, nil, fmt.Errorf("unknown function: %s", head.Str)
		}
		return Value{}, nil, err
	}
	if headVal.Kind == ValFn {
		// Tail call: return continuation instead of calling
		args := make([]Value, len(node.Children)-1)
		for i, argNode := range node.Children[1:] {
			val, err := e.Eval(argNode)
			if err != nil {
				return Value{}, nil, err
			}
			args[i] = val
		}
		return Value{}, &tailContinuation{Fn: headVal.Fn, Args: args}, nil
	}
	if headVal.Kind == ValForm {
		return e.callFormTail(headVal.Fn, node.Children[1:])
	}

	if head.Kind == NodeSymbol {
		return Value{}, nil, fmt.Errorf("cannot call %s: not a function", head.Str)
	}
	return Value{}, nil, fmt.Errorf("cannot call %s value", headVal.KindName())
}

func (e *Evaluator) evalIfTail(node *Node) (Value, *tailContinuation, error) {
	if len(node.Children) != 4 {
		return Value{}, nil, fmt.Errorf("if: expected 3 args (cond then else), got %d", len(node.Children)-1)
	}
	cond, err := e.Eval(node.Children[1])
	if err != nil {
		return Value{}, nil, err
	}
	if cond.Truthy() {
		return e.evalTail(node.Children[2])
	}
	return e.evalTail(node.Children[3])
}

func (e *Evaluator) evalLetTail(node *Node) (Value, *tailContinuation, error) {
	if len(node.Children) != 3 {
		return Value{}, nil, fmt.Errorf("let: expected bindings and body")
	}
	bindingsNode := node.Children[1]
	if bindingsNode.Kind != NodeList {
		return Value{}, nil, fmt.Errorf("let: bindings must be a list")
	}
	bindings := make(map[string]Value, len(bindingsNode.Children))
	e.pushScope(bindings)
	// No defer popScope — the trampoline handles scope cleanup via baseScope

	for _, pair := range bindingsNode.Children {
		if pair.Kind != NodeList || len(pair.Children) != 2 {
			return Value{}, nil, fmt.Errorf("let: each binding must be (name expr)")
		}
		nameNode := pair.Children[0]
		if nameNode.Kind != NodeSymbol {
			return Value{}, nil, fmt.Errorf("let: binding name must be a symbol")
		}
		val, err := e.Eval(pair.Children[1])
		if err != nil {
			return Value{}, nil, err
		}
		bindings[nameNode.Str] = val
	}
	return e.evalTail(node.Children[2])
}

func (e *Evaluator) evalLetrecTail(node *Node) (Value, *tailContinuation, error) {
	_, err := e.evalLetrecBindings(node)
	if err != nil {
		return Value{}, nil, err
	}
	// No defer popScope — the trampoline handles scope cleanup via baseScope
	return e.evalTail(node.Children[2])
}

func (e *Evaluator) evalDoTail(node *Node) (Value, *tailContinuation, error) {
	if len(node.Children) < 2 {
		return Value{}, nil, fmt.Errorf("do: expected at least one expression")
	}
	// Evaluate all but the last expression normally
	for _, child := range node.Children[1 : len(node.Children)-1] {
		_, err := e.Eval(child)
		if err != nil {
			return Value{}, nil, err
		}
	}
	// Last expression is in tail position
	return e.evalTail(node.Children[len(node.Children)-1])
}

// evalApply: (apply fn list) — call fn with list elements as args.
func (e *Evaluator) evalApply(node *Node) (Value, error) {
	if len(node.Children) != 3 {
		return Value{}, fmt.Errorf("apply: expected 2 args (fn list), got %d", len(node.Children)-1)
	}
	fnVal, err := e.Eval(node.Children[1])
	if err != nil {
		return Value{}, err
	}
	if fnVal.Kind == ValForm {
		return Value{}, fmt.Errorf("apply: cannot apply a Form (forms receive unevaluated AST)")
	}
	if fnVal.Kind != ValFn {
		return Value{}, fmt.Errorf("apply: first arg must be Fn, got %s", fnVal.KindName())
	}
	listVal, err := e.Eval(node.Children[2])
	if err != nil {
		return Value{}, err
	}
	if listVal.Kind != ValList {
		return Value{}, fmt.Errorf("apply: second arg must be List, got %s", listVal.KindName())
	}
	return e.CallFnWithValues(fnVal.Fn, *listVal.List)
}

// evalSortBy: (sort-by fn list) or (sort-by fn :desc list) — sort list by key function.
func (e *Evaluator) evalSortBy(node *Node) (Value, error) {
	argc := len(node.Children) - 1
	if argc < 2 || argc > 3 {
		return Value{}, fmt.Errorf("sort-by: expected 2-3 args (fn [dir] list), got %d", argc)
	}
	fnVal, err := e.Eval(node.Children[1])
	if err != nil {
		return Value{}, err
	}
	if fnVal.Kind == ValForm {
		return Value{}, fmt.Errorf("sort-by: cannot use a Form (forms receive unevaluated AST)")
	}
	if fnVal.Kind != ValFn {
		return Value{}, fmt.Errorf("sort-by: first arg must be Fn, got %s", fnVal.KindName())
	}
	desc := false
	listIdx := 2
	if argc == 3 {
		dirVal, err := e.Eval(node.Children[2])
		if err != nil {
			return Value{}, err
		}
		if dirVal.Kind == ValKeyword && dirVal.Str == "desc" {
			desc = true
		}
		listIdx = 3
	}
	listVal, err := e.Eval(node.Children[listIdx])
	if err != nil {
		return Value{}, err
	}
	if listVal.Kind != ValList {
		return Value{}, fmt.Errorf("sort-by: last arg must be List, got %s", listVal.KindName())
	}
	elems := *listVal.List
	// Compute keys
	type keyed struct {
		val Value
		key Value
	}
	items := make([]keyed, len(elems))
	for i, elem := range elems {
		k, err := e.CallFnWithValues(fnVal.Fn, []Value{elem})
		if err != nil {
			return Value{}, err
		}
		items[i] = keyed{val: elem, key: k}
	}
	// Sort
	var sortErr error
	sort.SliceStable(items, func(i, j int) bool {
		if sortErr != nil {
			return false
		}
		cmp, err := compareValues("sort-by", []Value{items[i].key, items[j].key})
		if err != nil {
			sortErr = err
			return false
		}
		if desc {
			return cmp > 0
		}
		return cmp < 0
	})
	if sortErr != nil {
		return Value{}, sortErr
	}
	result := make([]Value, len(items))
	for i, item := range items {
		result[i] = item.val
	}
	return ListVal(result), nil
}

// nodeToValue converts an AST node to a Value for quote.
func nodeToValue(n *Node) Value {
	switch n.Kind {
	case NodeInt:
		return IntVal(n.Int)
	case NodeFloat:
		return FloatVal(n.Float)
	case NodeBool:
		return BoolVal(n.Bool)
	case NodeString:
		return StringVal(n.Str)
	case NodeSymbol:
		return SymbolVal(n.Str)
	case NodeKeyword:
		return KeywordVal(n.Str)
	case NodeRef:
		return NodeRefVal(n.Ref)
	case NodeNil:
		return NilVal()
	case NodeList:
		elems := make([]Value, len(n.Children))
		for i, c := range n.Children {
			elems[i] = nodeToValue(c)
		}
		return ListVal(elems)
	default:
		return NilVal()
	}
}

// valueToNode converts a logos Value back to an AST node (inverse of nodeToValue).
// Used by form expansion: the form body returns a Value, which becomes AST to evaluate.
func valueToNode(v Value) (*Node, error) {
	switch v.Kind {
	case ValInt:
		return &Node{Kind: NodeInt, Int: v.Int}, nil
	case ValFloat:
		return &Node{Kind: NodeFloat, Float: v.Float}, nil
	case ValBool:
		return &Node{Kind: NodeBool, Bool: v.Bool}, nil
	case ValString:
		return &Node{Kind: NodeString, Str: v.Str}, nil
	case ValNil:
		return &Node{Kind: NodeNil}, nil
	case ValKeyword:
		return &Node{Kind: NodeKeyword, Str: v.Str}, nil
	case ValSymbol:
		return &Node{Kind: NodeSymbol, Str: v.Str}, nil
	case ValNodeRef:
		return &Node{Kind: NodeRef, Ref: v.Str}, nil
	case ValList:
		elems := *v.List
		children := make([]*Node, len(elems))
		for i, e := range elems {
			child, err := valueToNode(e)
			if err != nil {
				return nil, err
			}
			children[i] = child
		}
		return &Node{Kind: NodeList, Children: children}, nil
	default:
		return nil, fmt.Errorf("valueToNode: cannot convert %s to AST", v.KindName())
	}
}

// callForm calls a form value with unevaluated AST args.
func (e *Evaluator) callForm(form *FnValue, argNodes []*Node) (Value, error) {
	if form.RestParam != "" {
		if len(argNodes) < len(form.Params) {
			return Value{}, fmt.Errorf("form: expected at least %d args, got %d", len(form.Params), len(argNodes))
		}
	} else {
		if len(argNodes) != len(form.Params) {
			return Value{}, fmt.Errorf("form: expected %d args, got %d", len(form.Params), len(argNodes))
		}
	}
	// Convert each arg AST to a Value (not evaluated)
	args := make([]Value, len(argNodes))
	for i, argNode := range argNodes {
		args[i] = nodeToValue(argNode)
	}
	// Evaluate form body to produce expansion
	expansion, err := e.CallFnWithValues(form, args)
	if err != nil {
		return Value{}, err
	}
	// Convert expansion back to AST
	expansionNode, err := valueToNode(expansion)
	if err != nil {
		return Value{}, fmt.Errorf("form expansion: %w", err)
	}
	// Evaluate the expansion in the caller's scope
	return e.Eval(expansionNode)
}

// callFormTail calls a form value in tail position.
func (e *Evaluator) callFormTail(form *FnValue, argNodes []*Node) (Value, *tailContinuation, error) {
	if form.RestParam != "" {
		if len(argNodes) < len(form.Params) {
			return Value{}, nil, fmt.Errorf("form: expected at least %d args, got %d", len(form.Params), len(argNodes))
		}
	} else {
		if len(argNodes) != len(form.Params) {
			return Value{}, nil, fmt.Errorf("form: expected %d args, got %d", len(form.Params), len(argNodes))
		}
	}
	args := make([]Value, len(argNodes))
	for i, argNode := range argNodes {
		args[i] = nodeToValue(argNode)
	}
	expansion, err := e.CallFnWithValues(form, args)
	if err != nil {
		return Value{}, nil, err
	}
	expansionNode, err := valueToNode(expansion)
	if err != nil {
		return Value{}, nil, fmt.Errorf("form expansion: %w", err)
	}
	return e.evalTail(expansionNode)
}

// builtinAssert: (assert condition message) — returns true if condition is truthy,
// otherwise returns an AssertError with the message and current node ID.
func (e *Evaluator) builtinAssert(args []Value) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("assert: expected 2 args (condition message), got %d", len(args))
	}
	if args[1].Kind != ValString {
		return Value{}, fmt.Errorf("assert: message must be String, got %s", args[1].KindName())
	}
	if args[0].Truthy() {
		return BoolVal(true), nil
	}
	return Value{}, &AssertError{
		Message: args[1].Str,
		Node:    e.currentNodeID,
	}
}

func (e *Evaluator) EvalString(input string) (Value, error) {
	node, err := Parse(input)
	if err != nil {
		return Value{}, fmt.Errorf("parse error: %w", err)
	}
	return e.Eval(node)
}

// DataBuiltins returns the minimal data primitive builtins.
func DataBuiltins() map[string]Builtin {
	return map[string]Builtin{
		"list":      builtinList,
		"dict":      builtinDict,
		"get":       builtinGet,
		"head":      builtinHead,
		"rest":      builtinRest,
		"len":       builtinLen,
		"keys":      builtinKeys,
		"eq":        builtinEq,
		"to-string": builtinToString,
		"concat":    builtinConcat,
		"type":      builtinType,
		// Arithmetic
		"add": builtinAdd,
		"sub": builtinSub,
		"mul": builtinMul,
		"div": builtinDiv,
		"mod": builtinMod,
		// Comparison
		"lt": builtinLt,
		"gt": builtinGt,
		// List
		"cons":   builtinCons,
		"nth":    builtinNth,
		"append": builtinAppend,
		// Map
		"put":  builtinPut,
		"has?": builtinHas,
		// String
		"split-once": builtinSplitOnce,
	}
}

// --- Builtin implementations ---

func builtinList(args []Value) (Value, error) {
	return ListVal(args), nil
}

func builtinDict(args []Value) (Value, error) {
	if len(args)%2 != 0 {
		return Value{}, fmt.Errorf("dict: expected even number of args (key-value pairs), got %d", len(args))
	}
	m := make(map[string]Value, len(args)/2)
	for i := 0; i < len(args); i += 2 {
		if args[i].Kind != ValString && args[i].Kind != ValKeyword {
			return Value{}, fmt.Errorf("dict: key must be String or Keyword, got %s", args[i].KindName())
		}
		m[args[i].Str] = args[i+1]
	}
	return MapVal(m), nil
}

func builtinGet(args []Value) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("get: expected 2 args, got %d", len(args))
	}
	if args[0].Kind != ValMap {
		return Value{}, fmt.Errorf("get: expected Map, got %s", args[0].KindName())
	}
	if args[1].Kind != ValString && args[1].Kind != ValKeyword {
		return Value{}, fmt.Errorf("get: expected String or Keyword key, got %s", args[1].KindName())
	}
	m := *args[0].Map
	val, ok := m[args[1].Str]
	if !ok {
		return NilVal(), nil
	}
	return val, nil
}

func builtinHead(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("head: expected 1 arg, got %d", len(args))
	}
	if args[0].Kind != ValList {
		return Value{}, fmt.Errorf("head: expected List, got %s", args[0].KindName())
	}
	elems := *args[0].List
	if len(elems) == 0 {
		return NilVal(), nil
	}
	return elems[0], nil
}

func builtinRest(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("rest: expected 1 arg, got %d", len(args))
	}
	if args[0].Kind != ValList {
		return Value{}, fmt.Errorf("rest: expected List, got %s", args[0].KindName())
	}
	elems := *args[0].List
	if len(elems) == 0 {
		return ListVal(nil), nil
	}
	return ListVal(elems[1:]), nil
}



func builtinLen(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("len: expected 1 arg, got %d", len(args))
	}
	switch args[0].Kind {
	case ValList:
		return IntVal(int64(len(*args[0].List))), nil
	case ValMap:
		return IntVal(int64(len(*args[0].Map))), nil
	default:
		return Value{}, fmt.Errorf("len: expected List or Map, got %s", args[0].KindName())
	}
}

func builtinKeys(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("keys: expected 1 arg, got %d", len(args))
	}
	if args[0].Kind != ValMap {
		return Value{}, fmt.Errorf("keys: expected Map, got %s", args[0].KindName())
	}
	m := *args[0].Map
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	result := make([]Value, len(keys))
	for i, k := range keys {
		result[i] = StringVal(k)
	}
	return ListVal(result), nil
}

func builtinEq(args []Value) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("eq: expected 2 args, got %d", len(args))
	}
	return BoolVal(ValuesEqual(args[0], args[1])), nil
}



func builtinToString(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("to-string: expected 1 arg, got %d", len(args))
	}
	return StringVal(args[0].String()), nil
}

func builtinConcat(args []Value) (Value, error) {
	if len(args) < 1 {
		return Value{}, fmt.Errorf("concat: expected at least 1 arg, got 0")
	}
	var sb strings.Builder
	for _, a := range args {
		if a.Kind != ValString {
			return Value{}, fmt.Errorf("concat: expected String, got %s", a.KindName())
		}
		sb.WriteString(a.Str)
	}
	return StringVal(sb.String()), nil
}

func builtinType(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("type: expected 1 arg, got %d", len(args))
	}
	switch args[0].Kind {
	case ValInt:
		return KeywordVal("int"), nil
	case ValFloat:
		return KeywordVal("float"), nil
	case ValBool:
		return KeywordVal("bool"), nil
	case ValString:
		return KeywordVal("string"), nil
	case ValKeyword:
		return KeywordVal("keyword"), nil
	case ValNil:
		return KeywordVal("nil"), nil
	case ValList:
		return KeywordVal("list"), nil
	case ValMap:
		return KeywordVal("map"), nil
	case ValFn:
		return KeywordVal("fn"), nil
	case ValForm:
		return KeywordVal("form"), nil
	case ValSymbol:
		return KeywordVal("symbol"), nil
	case ValNodeRef:
		return KeywordVal("node-ref"), nil
	default:
		return KeywordVal("unknown"), nil
	}
}

// --- Arithmetic ---

// numericArgs extracts two numeric args, promoting to float if mixed.
func numericArgs(name string, args []Value) (int64, int64, float64, float64, bool, error) {
	if len(args) != 2 {
		return 0, 0, 0, 0, false, fmt.Errorf("%s: expected 2 args, got %d", name, len(args))
	}
	a, b := args[0], args[1]
	if a.Kind == ValInt && b.Kind == ValInt {
		return a.Int, b.Int, 0, 0, false, nil
	}
	var fa, fb float64
	switch a.Kind {
	case ValInt:
		fa = float64(a.Int)
	case ValFloat:
		fa = a.Float
	default:
		return 0, 0, 0, 0, false, fmt.Errorf("%s: expected number, got %s", name, a.KindName())
	}
	switch b.Kind {
	case ValInt:
		fb = float64(b.Int)
	case ValFloat:
		fb = b.Float
	default:
		return 0, 0, 0, 0, false, fmt.Errorf("%s: expected number, got %s", name, b.KindName())
	}
	return 0, 0, fa, fb, true, nil
}

func builtinAdd(args []Value) (Value, error) {
	ai, bi, af, bf, isFloat, err := numericArgs("add", args)
	if err != nil {
		return Value{}, err
	}
	if isFloat {
		return FloatVal(af + bf), nil
	}
	return IntVal(ai + bi), nil
}

func builtinSub(args []Value) (Value, error) {
	ai, bi, af, bf, isFloat, err := numericArgs("sub", args)
	if err != nil {
		return Value{}, err
	}
	if isFloat {
		return FloatVal(af - bf), nil
	}
	return IntVal(ai - bi), nil
}

func builtinMul(args []Value) (Value, error) {
	ai, bi, af, bf, isFloat, err := numericArgs("mul", args)
	if err != nil {
		return Value{}, err
	}
	if isFloat {
		return FloatVal(af * bf), nil
	}
	return IntVal(ai * bi), nil
}

func builtinDiv(args []Value) (Value, error) {
	ai, bi, af, bf, isFloat, err := numericArgs("div", args)
	if err != nil {
		return Value{}, err
	}
	if isFloat {
		if bf == 0 {
			return Value{}, fmt.Errorf("div: division by zero")
		}
		return FloatVal(af / bf), nil
	}
	if bi == 0 {
		return Value{}, fmt.Errorf("div: division by zero")
	}
	return IntVal(ai / bi), nil
}

func builtinMod(args []Value) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("mod: expected 2 args, got %d", len(args))
	}
	if args[0].Kind != ValInt || args[1].Kind != ValInt {
		return Value{}, fmt.Errorf("mod: expected Int args, got %s and %s", args[0].KindName(), args[1].KindName())
	}
	if args[1].Int == 0 {
		return Value{}, fmt.Errorf("mod: division by zero")
	}
	return IntVal(args[0].Int % args[1].Int), nil
}

// --- Comparison ---



func typeRank(k ValueKind) int {
	switch k {
	case ValNil:
		return 0
	case ValBool:
		return 1
	case ValInt:
		return 2
	case ValFloat:
		return 3
	case ValString:
		return 4
	case ValKeyword:
		return 5
	case ValSymbol:
		return 6
	case ValNodeRef:
		return 7
	case ValList:
		return 8
	case ValMap:
		return 9
	case ValFn:
		return 10
	case ValForm:
		return 11
	default:
		return 99
	}
}

func compareTwo(a, b Value) (int, error) {
	// Numeric promotion: int and float are cross-comparable
	if (a.Kind == ValInt || a.Kind == ValFloat) && (b.Kind == ValInt || b.Kind == ValFloat) {
		var fa, fb float64
		if a.Kind == ValInt {
			fa = float64(a.Int)
		} else {
			fa = a.Float
		}
		if b.Kind == ValInt {
			fb = float64(b.Int)
		} else {
			fb = b.Float
		}
		if fa < fb {
			return -1, nil
		}
		if fa > fb {
			return 1, nil
		}
		return 0, nil
	}
	// Different types: compare by rank
	ra, rb := typeRank(a.Kind), typeRank(b.Kind)
	if ra != rb {
		if ra < rb {
			return -1, nil
		}
		return 1, nil
	}
	// Same type
	switch a.Kind {
	case ValNil:
		return 0, nil
	case ValBool:
		if a.Bool == b.Bool {
			return 0, nil
		}
		if !a.Bool {
			return -1, nil // false < true
		}
		return 1, nil
	case ValString, ValKeyword, ValSymbol, ValNodeRef:
		if a.Str < b.Str {
			return -1, nil
		}
		if a.Str > b.Str {
			return 1, nil
		}
		return 0, nil
	case ValList:
		as, bs := *a.List, *b.List
		for i := 0; i < len(as) && i < len(bs); i++ {
			cmp, err := compareTwo(as[i], bs[i])
			if err != nil {
				return 0, err
			}
			if cmp != 0 {
				return cmp, nil
			}
		}
		if len(as) < len(bs) {
			return -1, nil
		}
		if len(as) > len(bs) {
			return 1, nil
		}
		return 0, nil
	case ValMap:
		am, bm := *a.Map, *b.Map
		// Collect and sort keys from both maps
		keySet := make(map[string]bool)
		for k := range am {
			keySet[k] = true
		}
		for k := range bm {
			keySet[k] = true
		}
		keys := make([]string, 0, len(keySet))
		for k := range keySet {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			av, aok := am[k]
			bv, bok := bm[k]
			if aok && !bok {
				return 1, nil
			}
			if !aok && bok {
				return -1, nil
			}
			cmp, err := compareTwo(av, bv)
			if err != nil {
				return 0, err
			}
			if cmp != 0 {
				return cmp, nil
			}
		}
		return 0, nil
	case ValFn, ValForm:
		as, bs := a.String(), b.String()
		if as < bs {
			return -1, nil
		}
		if as > bs {
			return 1, nil
		}
		return 0, nil
	}
	return 0, nil
}

func compareValues(name string, args []Value) (int, error) {
	if len(args) != 2 {
		return 0, fmt.Errorf("%s: expected 2 args, got %d", name, len(args))
	}
	return compareTwo(args[0], args[1])
}

func builtinLt(args []Value) (Value, error) {
	cmp, err := compareValues("lt", args)
	if err != nil {
		return Value{}, err
	}
	return BoolVal(cmp < 0), nil
}

func builtinGt(args []Value) (Value, error) {
	cmp, err := compareValues("gt", args)
	if err != nil {
		return Value{}, err
	}
	return BoolVal(cmp > 0), nil
}



// --- List ---

func builtinCons(args []Value) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("cons: expected 2 args, got %d", len(args))
	}
	if args[1].Kind != ValList {
		return Value{}, fmt.Errorf("cons: second arg must be List, got %s", args[1].KindName())
	}
	elems := *args[1].List
	result := make([]Value, len(elems)+1)
	result[0] = args[0]
	copy(result[1:], elems)
	return ListVal(result), nil
}

func builtinNth(args []Value) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("nth: expected 2 args, got %d", len(args))
	}
	if args[0].Kind != ValList {
		return Value{}, fmt.Errorf("nth: first arg must be List, got %s", args[0].KindName())
	}
	if args[1].Kind != ValInt {
		return Value{}, fmt.Errorf("nth: second arg must be Int, got %s", args[1].KindName())
	}
	elems := *args[0].List
	idx := int(args[1].Int)
	if idx < 0 || idx >= len(elems) {
		return NilVal(), nil
	}
	return elems[idx], nil
}

func builtinAppend(args []Value) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("append: expected 2 args, got %d", len(args))
	}
	if args[0].Kind != ValList || args[1].Kind != ValList {
		return Value{}, fmt.Errorf("append: expected two Lists, got %s and %s", args[0].KindName(), args[1].KindName())
	}
	a, b := *args[0].List, *args[1].List
	result := make([]Value, len(a)+len(b))
	copy(result, a)
	copy(result[len(a):], b)
	return ListVal(result), nil
}

// --- Map ops ---

func builtinPut(args []Value) (Value, error) {
	if len(args) != 3 {
		return Value{}, fmt.Errorf("put: expected 3 args, got %d", len(args))
	}
	if args[0].Kind != ValMap {
		return Value{}, fmt.Errorf("put: first arg must be Map, got %s", args[0].KindName())
	}
	if args[1].Kind != ValString && args[1].Kind != ValKeyword {
		return Value{}, fmt.Errorf("put: key must be String or Keyword, got %s", args[1].KindName())
	}
	orig := *args[0].Map
	m := make(map[string]Value, len(orig)+1)
	for k, v := range orig {
		m[k] = v
	}
	m[args[1].Str] = args[2]
	return MapVal(m), nil
}

func builtinHas(args []Value) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("has?: expected 2 args, got %d", len(args))
	}
	if args[0].Kind != ValMap {
		return Value{}, fmt.Errorf("has?: first arg must be Map, got %s", args[0].KindName())
	}
	if args[1].Kind != ValString && args[1].Kind != ValKeyword {
		return Value{}, fmt.Errorf("has?: key must be String or Keyword, got %s", args[1].KindName())
	}
	_, ok := (*args[0].Map)[args[1].Str]
	return BoolVal(ok), nil
}

// --- String ops ---

func builtinSplitOnce(args []Value) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("split-once: expected 2 args, got %d", len(args))
	}
	if args[0].Kind != ValString || args[1].Kind != ValString {
		return Value{}, fmt.Errorf("split-once: expected two Strings, got %s and %s", args[0].KindName(), args[1].KindName())
	}
	needle := args[0].Str
	if needle == "" {
		return Value{}, fmt.Errorf("split-once: needle must not be empty")
	}
	haystack := args[1].Str
	idx := strings.Index(haystack, needle)
	if idx == -1 {
		return NilVal(), nil
	}
	before := haystack[:idx]
	after := haystack[idx+len(needle):]
	return ListVal([]Value{StringVal(before), StringVal(after)}), nil
}
