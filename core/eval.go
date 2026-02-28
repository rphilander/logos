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
	Resolve     Resolver
	ResolveNode NodeResolver
	Builtins    map[string]Builtin
	locals      []map[string]Value
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
	val, err := e.Eval(expr)
	if err != nil {
		return Value{}, err
	}
	if val.Kind == ValFn {
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
		case "do":
			return e.evalDo(node)
		case "fn":
			return e.evalFn(node)
		case "quote":
			return e.evalQuote(node)
		case "cond":
			return e.evalCond(node)
		case "case":
			return e.evalCase(node)
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

func (e *Evaluator) callFn(fn *FnValue, argNodes []*Node) (Value, error) {
	if len(argNodes) != len(fn.Params) {
		return Value{}, fmt.Errorf("fn: expected %d args, got %d", len(fn.Params), len(argNodes))
	}
	bindings := make(map[string]Value, len(fn.Params))
	for i, param := range fn.Params {
		val, err := e.Eval(argNodes[i])
		if err != nil {
			return Value{}, err
		}
		bindings[param] = val
	}
	e.pushScope(bindings)
	defer e.popScope()
	return e.Eval(fn.Body)
}

// CallFnWithValues calls a user-defined function with pre-evaluated Values.
func (e *Evaluator) CallFnWithValues(fn *FnValue, args []Value) (Value, error) {
	if len(args) != len(fn.Params) {
		return Value{}, fmt.Errorf("fn: expected %d args, got %d", len(fn.Params), len(args))
	}
	bindings := make(map[string]Value, len(fn.Params))
	for i, param := range fn.Params {
		bindings[param] = args[i]
	}
	e.pushScope(bindings)
	defer e.popScope()
	return e.Eval(fn.Body)
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

// evalFn: (fn (params...) body) — create closure.
func (e *Evaluator) evalFn(node *Node) (Value, error) {
	if len(node.Children) != 3 {
		return Value{}, fmt.Errorf("fn: expected (fn (params...) body)")
	}
	paramsNode := node.Children[1]
	if paramsNode.Kind != NodeList {
		return Value{}, fmt.Errorf("fn: params must be a list")
	}
	params := make([]string, len(paramsNode.Children))
	for i, p := range paramsNode.Children {
		if p.Kind != NodeSymbol {
			return Value{}, fmt.Errorf("fn: param names must be symbols")
		}
		params[i] = p.Str
	}
	return FnVal(&FnValue{
		Params: params,
		Body:   node.Children[2],
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

// evalCond: (cond test1 expr1 test2 expr2 ... ) — multi-way branch.
// Evaluates tests top to bottom, returns the expr for the first truthy test.
func (e *Evaluator) evalCond(node *Node) (Value, error) {
	args := node.Children[1:]
	if len(args) == 0 || len(args)%2 != 0 {
		return Value{}, fmt.Errorf("cond: expected even number of args (test/expr pairs), got %d", len(args))
	}
	for i := 0; i < len(args); i += 2 {
		test, err := e.Eval(args[i])
		if err != nil {
			return Value{}, err
		}
		if test.Truthy() {
			return e.Eval(args[i+1])
		}
	}
	return NilVal(), nil
}

// evalCase: (case target match1 expr1 match2 expr2 ... [default]) — value dispatch.
// Evaluates target once, checks each match value with ValuesEqual.
// If odd trailing arg, it's the default. If no match and no default, returns nil.
func (e *Evaluator) evalCase(node *Node) (Value, error) {
	if len(node.Children) < 2 {
		return Value{}, fmt.Errorf("case: expected target and at least one clause")
	}
	target, err := e.Eval(node.Children[1])
	if err != nil {
		return Value{}, err
	}
	args := node.Children[2:]
	pairs := len(args) / 2
	for i := 0; i < pairs; i++ {
		matchVal, err := e.Eval(args[i*2])
		if err != nil {
			return Value{}, err
		}
		if ValuesEqual(target, matchVal) {
			return e.Eval(args[i*2+1])
		}
	}
	if len(args)%2 != 0 {
		return e.Eval(args[len(args)-1])
	}
	return NilVal(), nil
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
		return StringVal(n.Str)
	case NodeKeyword:
		return KeywordVal(n.Str)
	case NodeRef:
		return StringVal(n.Str)
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
		"empty?":    builtinEmpty,
		"len":       builtinLen,
		"keys":      builtinKeys,
		"eq":        builtinEq,
		"nil?":      builtinNilQ,
		"to-string": builtinToString,
		"concat":    builtinConcat,
		"type":      builtinType,
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

func builtinEmpty(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("empty?: expected 1 arg, got %d", len(args))
	}
	if args[0].Kind != ValList {
		return Value{}, fmt.Errorf("empty?: expected List, got %s", args[0].KindName())
	}
	return BoolVal(len(*args[0].List) == 0), nil
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
	case ValString:
		return IntVal(int64(len(args[0].Str))), nil
	default:
		return Value{}, fmt.Errorf("len: expected List, Map, or String, got %s", args[0].KindName())
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

func builtinNilQ(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("nil?: expected 1 arg, got %d", len(args))
	}
	return BoolVal(args[0].Kind == ValNil), nil
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
	default:
		return KeywordVal("unknown"), nil
	}
}
