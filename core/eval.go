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

// --- Frame-based iterative evaluator ---

type frameKind int

const (
	frameFnBody      frameKind = iota // entered fn body; holds scopeBase for cleanup/TCO
	frameScopeCleanup                 // pop one scope on value pass-through (non-tail let/letrec)
	frameRef                          // evaluating node-ref expression; restore currentNodeID, tag fn/form
	frameEvalHead                     // evaluated computed head; dispatch fn/form/error
	frameBuiltinArg                   // evaluating builtin arguments one by one
	frameFnArg                        // evaluating fn call arguments one by one
	frameIfCond                       // evaluated if condition; pick branch
	frameLetBind                      // evaluating let binding values sequentially
	frameLetrecBind                   // evaluating letrec binding values sequentially
	frameDo                           // evaluating do expressions sequentially
	frameFormExpand                    // form body produced expansion value; convert to AST and eval
	frameApplyFn                      // evaluated apply's fn arg; now eval list arg
	frameApplyList                    // evaluated apply's list arg; enter fn body
)

type frame struct {
	kind frameKind
	tail bool // is this frame in tail position?

	// frameFnBody
	scopeBase   int    // len(e.locals) before pushing closure+bindings
	savedNodeID string // currentNodeID to restore

	// frameRef
	refNodeID string // the node ID being evaluated

	// frameEvalHead
	headNode *Node // original head AST node (for error messages)
	argNodes []*Node

	// frameBuiltinArg
	builtin  Builtin
	builtinDone []Value
	builtinArgs []*Node
	builtinIdx  int

	// frameFnArg
	fn      *FnValue
	fnDone  []Value
	fnArgs  []*Node
	fnIdx   int

	// frameIfCond
	thenNode *Node
	elseNode *Node

	// frameLetBind / frameLetrecBind
	bindings     map[string]Value
	bindPairs    []*Node
	bindNames    []string // letrec only: ordered names for back-patching
	bindIdx      int
	bodyNode     *Node

	// frameDo
	doExprs []*Node
	doIdx   int

	// frameFormExpand (tail inherited from frame.tail)

	// frameApplyFn
	applyListNode *Node

	// frameApplyList
	applyFn *FnValue
}

// Eval evaluates a single AST node.
func (e *Evaluator) Eval(node *Node) (Value, error) {
	return e.evalLoop(node)
}

// CallFnWithValues calls a user-defined function with pre-evaluated Values.
func (e *Evaluator) CallFnWithValues(fn *FnValue, args []Value) (Value, error) {
	// Arity check
	if fn.RestParam != "" {
		if len(args) < len(fn.Params) {
			return Value{}, fmt.Errorf("fn: expected at least %d args, got %d", len(fn.Params), len(args))
		}
	} else {
		if len(args) != len(fn.Params) {
			return Value{}, fmt.Errorf("fn: expected %d args, got %d", len(fn.Params), len(args))
		}
	}

	scopeBase := len(e.locals)

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
	prevNodeID := e.currentNodeID
	if fn.NodeID != "" {
		e.currentNodeID = fn.NodeID
	}

	// Pre-seed the stack with a frameFnBody for TCO support
	stack := []frame{{
		kind:        frameFnBody,
		tail:        true,
		scopeBase:   scopeBase,
		savedNodeID: prevNodeID,
	}}

	val, err := e.evalLoopWith(fn.Body, stack, true)
	// Scope cleanup is handled by evalLoop via frameFnBody
	return val, err
}

// evalLoop is the main iterative evaluation loop.
func (e *Evaluator) evalLoop(startNode *Node) (Value, error) {
	return e.evalLoopWith(startNode, nil, false)
}

// evalLoopWith runs the iterative eval loop with an optional pre-seeded stack.
// startTail indicates whether startNode is in tail position (true when called from CallFnWithValues).
func (e *Evaluator) evalLoopWith(startNode *Node, stack []frame, startTail bool) (Value, error) {
	// Save state for re-entrant safety
	savedLocalsLen := len(e.locals)
	savedNodeID := e.currentNodeID

	node := startNode
	evaluating := true
	tail := startTail // tracks whether current node is in tail position
	var result Value

	for {
		if evaluating {
			// --- Eval phase: process node ---
			if e.FuelSet {
				if e.Fuel <= 0 {
					e.locals = e.locals[:savedLocalsLen]
					e.currentNodeID = savedNodeID
					return Value{}, fmt.Errorf("fuel exhausted")
				}
				e.Fuel--
			}

			switch node.Kind {
			case NodeInt:
				result = IntVal(node.Int)
				evaluating = false
			case NodeFloat:
				result = FloatVal(node.Float)
				evaluating = false
			case NodeBool:
				result = BoolVal(node.Bool)
				evaluating = false
			case NodeString:
				result = StringVal(node.Str)
				evaluating = false
			case NodeNil:
				result = NilVal()
				evaluating = false
			case NodeKeyword:
				result = KeywordVal(node.Str)
				evaluating = false
			case NodeSymbol:
				val, err := e.resolveSymbol(node.Str)
				if err != nil {
					e.locals = e.locals[:savedLocalsLen]
					e.currentNodeID = savedNodeID
					return Value{}, err
				}
				result = val
				evaluating = false
			case NodeRef:
				// Push frameRef, then evaluate the referenced node's expression
				if e.ResolveNode == nil {
					e.locals = e.locals[:savedLocalsLen]
					e.currentNodeID = savedNodeID
					return Value{}, fmt.Errorf("cannot resolve node ref %s: no node resolver", node.Ref)
				}
				expr, err := e.ResolveNode(node.Ref)
				if err != nil {
					e.locals = e.locals[:savedLocalsLen]
					e.currentNodeID = savedNodeID
					return Value{}, err
				}
				stack = append(stack, frame{
					kind:      frameRef,
					refNodeID: node.Ref,
					savedNodeID: e.currentNodeID,
				})
				e.currentNodeID = node.Ref
				node = expr
				tail = false // ref content evaluation is not in tail position
			case NodeList:
				if len(node.Children) == 0 {
					e.locals = e.locals[:savedLocalsLen]
					e.currentNodeID = savedNodeID
					return Value{}, fmt.Errorf("cannot eval empty list")
				}

				head := node.Children[0]
				// tail is the loop variable — tracks whether this list is in tail position

				// Core forms (symbol head)
				if head.Kind == NodeSymbol {
					switch head.Str {
					case "if":
						if len(node.Children) != 4 {
							e.locals = e.locals[:savedLocalsLen]
							e.currentNodeID = savedNodeID
							return Value{}, fmt.Errorf("if: expected 3 args (cond then else), got %d", len(node.Children)-1)
						}
						stack = append(stack, frame{
							kind:     frameIfCond,
							tail:     tail,
							thenNode: node.Children[2],
							elseNode: node.Children[3],
						})
						node = node.Children[1]
						tail = false // condition is not in tail position
						continue

					case "let":
						if len(node.Children) != 3 {
							e.locals = e.locals[:savedLocalsLen]
							e.currentNodeID = savedNodeID
							return Value{}, fmt.Errorf("let: expected bindings and body")
						}
						bindingsNode := node.Children[1]
						if bindingsNode.Kind != NodeList {
							e.locals = e.locals[:savedLocalsLen]
							e.currentNodeID = savedNodeID
							return Value{}, fmt.Errorf("let: bindings must be a list")
						}
						bindings := make(map[string]Value, len(bindingsNode.Children))
						e.pushScope(bindings)
						if len(bindingsNode.Children) == 0 {
							// No bindings — eval body directly
							if !tail {
								stack = append(stack, frame{kind: frameScopeCleanup})
							}
							node = node.Children[2]
							continue
						}
						// Validate first pair
						pair := bindingsNode.Children[0]
						if pair.Kind != NodeList || len(pair.Children) != 2 {
							e.locals = e.locals[:savedLocalsLen]
							e.currentNodeID = savedNodeID
							return Value{}, fmt.Errorf("let: each binding must be (name expr)")
						}
						if pair.Children[0].Kind != NodeSymbol {
							e.locals = e.locals[:savedLocalsLen]
							e.currentNodeID = savedNodeID
							return Value{}, fmt.Errorf("let: binding name must be a symbol")
						}
						stack = append(stack, frame{
							kind:      frameLetBind,
							tail:      tail,
							bindings:  bindings,
							bindPairs: bindingsNode.Children,
							bindIdx:   0,
							bodyNode:  node.Children[2],
						})
						node = pair.Children[1]
						tail = false // binding expressions are not in tail position
						continue

					case "letrec":
						if len(node.Children) != 3 {
							e.locals = e.locals[:savedLocalsLen]
							e.currentNodeID = savedNodeID
							return Value{}, fmt.Errorf("letrec: expected bindings and body")
						}
						bindingsNode := node.Children[1]
						if bindingsNode.Kind != NodeList {
							e.locals = e.locals[:savedLocalsLen]
							e.currentNodeID = savedNodeID
							return Value{}, fmt.Errorf("letrec: bindings must be a list")
						}
						// Collect names and validate
						names := make([]string, len(bindingsNode.Children))
						for i, pair := range bindingsNode.Children {
							if pair.Kind != NodeList || len(pair.Children) != 2 {
								e.locals = e.locals[:savedLocalsLen]
								e.currentNodeID = savedNodeID
								return Value{}, fmt.Errorf("letrec: each binding must be (name expr)")
							}
							if pair.Children[0].Kind != NodeSymbol {
								e.locals = e.locals[:savedLocalsLen]
								e.currentNodeID = savedNodeID
								return Value{}, fmt.Errorf("letrec: binding name must be a symbol")
							}
							names[i] = pair.Children[0].Str
						}
						scope := make(map[string]Value, len(names))
						e.pushScope(scope)
						if len(bindingsNode.Children) == 0 {
							if !tail {
								stack = append(stack, frame{kind: frameScopeCleanup})
							}
							node = node.Children[2]
							continue
						}
						stack = append(stack, frame{
							kind:      frameLetrecBind,
							tail:      tail,
							bindings:  scope,
							bindPairs: bindingsNode.Children,
							bindNames: names,
							bindIdx:   0,
							bodyNode:  node.Children[2],
						})
						node = bindingsNode.Children[0].Children[1]
						tail = false // binding expressions are not in tail position
						continue

					case "do":
						if len(node.Children) < 2 {
							e.locals = e.locals[:savedLocalsLen]
							e.currentNodeID = savedNodeID
							return Value{}, fmt.Errorf("do: expected at least one expression")
						}
						exprs := node.Children[1:]
						if len(exprs) == 1 {
							// Single expression: just eval it (inherit tail)
							node = exprs[0]
							continue
						}
						stack = append(stack, frame{
							kind:    frameDo,
							tail:    tail,
							doExprs: exprs,
							doIdx:   0,
						})
						node = exprs[0]
						tail = false // non-last do expressions are not in tail position
						continue

					case "fn":
						val, err := e.evalFn(node)
						if err != nil {
							e.locals = e.locals[:savedLocalsLen]
							e.currentNodeID = savedNodeID
							return Value{}, err
						}
						result = val
						evaluating = false
						continue

					case "form":
						val, err := e.evalForm(node)
						if err != nil {
							e.locals = e.locals[:savedLocalsLen]
							e.currentNodeID = savedNodeID
							return Value{}, err
						}
						result = val
						evaluating = false
						continue

					case "quote":
						val, err := e.evalQuote(node)
						if err != nil {
							e.locals = e.locals[:savedLocalsLen]
							e.currentNodeID = savedNodeID
							return Value{}, err
						}
						result = val
						evaluating = false
						continue

					case "apply":
						if len(node.Children) != 3 {
							e.locals = e.locals[:savedLocalsLen]
							e.currentNodeID = savedNodeID
							return Value{}, fmt.Errorf("apply: expected 2 args (fn list), got %d", len(node.Children)-1)
						}
						stack = append(stack, frame{
							kind:          frameApplyFn,
							tail:          tail,
							applyListNode: node.Children[2],
						})
						node = node.Children[1]
						tail = false // apply args are not in tail position
						continue

					case "sort-by":
						// sort-by stays as a leaf operation (calls CallFnWithValues internally)
						val, err := e.evalSortBy(node)
						if err != nil {
							e.locals = e.locals[:savedLocalsLen]
							e.currentNodeID = savedNodeID
							return Value{}, err
						}
						result = val
						evaluating = false
						continue
					}
				}

				// Builtins (symbol head, not a core form)
				if head.Kind == NodeSymbol && e.Builtins != nil {
					if fn, ok := e.Builtins[head.Str]; ok {
						argNodes := node.Children[1:]
						if len(argNodes) == 0 {
							val, err := fn(nil)
							if err != nil {
								e.locals = e.locals[:savedLocalsLen]
								e.currentNodeID = savedNodeID
								return Value{}, err
							}
							result = val
							evaluating = false
							continue
						}
						stack = append(stack, frame{
							kind:        frameBuiltinArg,
							builtin:     fn,
							builtinDone: make([]Value, 0, len(argNodes)),
							builtinArgs: argNodes,
							builtinIdx:  0,
						})
						node = argNodes[0]
						tail = false // args are not in tail position
						continue
					}
				}

				// Computed head — evaluate it, then dispatch
				stack = append(stack, frame{
					kind:     frameEvalHead,
					tail:     tail,
					headNode: head,
					argNodes: node.Children[1:],
				})
				node = head
				tail = false // evaluating the head position is not tail
				continue

			default:
				e.locals = e.locals[:savedLocalsLen]
				e.currentNodeID = savedNodeID
				return Value{}, fmt.Errorf("unknown node kind: %d", node.Kind)
			}
		}

		// --- Value phase: we have `result`, process top frame ---
		if len(stack) == 0 {
			return result, nil
		}

		f := &stack[len(stack)-1]

		switch f.kind {
		case frameFnBody:
			// Function body completed. Restore scope and currentNodeID.
			e.locals = e.locals[:f.scopeBase]
			e.currentNodeID = f.savedNodeID
			stack = stack[:len(stack)-1]
			// result passes through

		case frameScopeCleanup:
			e.popScope()
			stack = stack[:len(stack)-1]
			// result passes through

		case frameRef:
			e.currentNodeID = f.savedNodeID
			if result.Kind == ValFn || result.Kind == ValForm {
				result.Fn.NodeID = f.refNodeID
			}
			stack = stack[:len(stack)-1]
			// result passes through

		case frameEvalHead:
			headNode := f.headNode
			argNodes := f.argNodes
			frameTail := f.tail
			stack = stack[:len(stack)-1]

			if result.Kind == ValFn {
				// User-defined fn: evaluate args
				fn := result.Fn
				if fn.RestParam != "" {
					if len(argNodes) < len(fn.Params) {
						e.locals = e.locals[:savedLocalsLen]
						e.currentNodeID = savedNodeID
						return Value{}, fmt.Errorf("fn: expected at least %d args, got %d", len(fn.Params), len(argNodes))
					}
				} else {
					if len(argNodes) != len(fn.Params) {
						e.locals = e.locals[:savedLocalsLen]
						e.currentNodeID = savedNodeID
						return Value{}, fmt.Errorf("fn: expected %d args, got %d", len(fn.Params), len(argNodes))
					}
				}
				if len(argNodes) == 0 {
					// No args to evaluate — enter fn body directly
					node = e.enterFnBody(fn, nil, frameTail, &stack)
					tail = true // fn body is always in tail position
					evaluating = true
					continue
				}
				stack = append(stack, frame{
					kind:   frameFnArg,
					tail:   frameTail,
					fn:     fn,
					fnDone: make([]Value, 0, len(argNodes)),
					fnArgs: argNodes,
					fnIdx:  0,
				})
				node = argNodes[0]
				tail = false // args are not in tail position
				evaluating = true
				continue
			}
			if result.Kind == ValForm {
				formFn := result.Fn
				// Handle form call
				n, err := e.handleFormCall(formFn, argNodes, frameTail, &stack)
				if err != nil {
					e.locals = e.locals[:savedLocalsLen]
					e.currentNodeID = savedNodeID
					return Value{}, err
				}
				node = n
				tail = frameTail // form expansion inherits tail from the form call site
				evaluating = true
				continue
			}
			if headNode.Kind == NodeSymbol {
				e.locals = e.locals[:savedLocalsLen]
				e.currentNodeID = savedNodeID
				return Value{}, fmt.Errorf("cannot call %s: not a function", headNode.Str)
			}
			e.locals = e.locals[:savedLocalsLen]
			e.currentNodeID = savedNodeID
			return Value{}, fmt.Errorf("cannot call %s value", result.KindName())

		case frameBuiltinArg:
			f.builtinDone = append(f.builtinDone, result)
			f.builtinIdx++
			if f.builtinIdx < len(f.builtinArgs) {
				node = f.builtinArgs[f.builtinIdx]
				evaluating = true
				continue
			}
			// All args evaluated — call builtin
			fn := f.builtin
			args := f.builtinDone
			stack = stack[:len(stack)-1]
			val, err := fn(args)
			if err != nil {
				e.locals = e.locals[:savedLocalsLen]
				e.currentNodeID = savedNodeID
				return Value{}, err
			}
			result = val

		case frameFnArg:
			f.fnDone = append(f.fnDone, result)
			f.fnIdx++
			if f.fnIdx < len(f.fnArgs) {
				node = f.fnArgs[f.fnIdx]
				tail = false // args are not in tail position
				evaluating = true
				continue
			}
			// All args evaluated — enter fn body
			fn := f.fn
			args := f.fnDone
			frameTail := f.tail
			stack = stack[:len(stack)-1]
			node = e.enterFnBody(fn, args, frameTail, &stack)
			tail = true // fn body is always in tail position
			evaluating = true
			continue

		case frameIfCond:
			thenNode := f.thenNode
			elseNode := f.elseNode
			frameTail := f.tail
			stack = stack[:len(stack)-1]
			if result.Truthy() {
				node = thenNode
			} else {
				node = elseNode
			}
			tail = frameTail // branches inherit tail from the if expression
			evaluating = true
			continue

		case frameLetBind:
			// Store binding value
			nameStr := f.bindPairs[f.bindIdx].Children[0].Str
			f.bindings[nameStr] = result
			f.bindIdx++
			if f.bindIdx < len(f.bindPairs) {
				// Validate next pair
				pair := f.bindPairs[f.bindIdx]
				if pair.Kind != NodeList || len(pair.Children) != 2 {
					e.locals = e.locals[:savedLocalsLen]
					e.currentNodeID = savedNodeID
					return Value{}, fmt.Errorf("let: each binding must be (name expr)")
				}
				if pair.Children[0].Kind != NodeSymbol {
					e.locals = e.locals[:savedLocalsLen]
					e.currentNodeID = savedNodeID
					return Value{}, fmt.Errorf("let: binding name must be a symbol")
				}
				node = pair.Children[1]
				tail = false // binding expressions are not in tail position
				evaluating = true
				continue
			}
			// All bindings done — eval body
			bodyNode := f.bodyNode
			frameTail := f.tail
			stack = stack[:len(stack)-1]
			if !frameTail {
				stack = append(stack, frame{kind: frameScopeCleanup})
			}
			node = bodyNode
			tail = frameTail // body inherits tail from the let expression
			evaluating = true
			continue

		case frameLetrecBind:
			// Store binding value
			f.bindings[f.bindNames[f.bindIdx]] = result
			f.bindIdx++
			if f.bindIdx < len(f.bindPairs) {
				node = f.bindPairs[f.bindIdx].Children[1]
				tail = false // binding expressions are not in tail position
				evaluating = true
				continue
			}
			// All bindings done — back-patch closures, eval body
			for _, val := range f.bindings {
				if (val.Kind == ValFn || val.Kind == ValForm) && val.Fn != nil {
					if val.Fn.Closure == nil {
						val.Fn.Closure = make(map[string]Value, len(f.bindNames))
					}
					for _, name := range f.bindNames {
						val.Fn.Closure[name] = f.bindings[name]
					}
				}
			}
			bodyNode := f.bodyNode
			frameTail := f.tail
			stack = stack[:len(stack)-1]
			if !frameTail {
				stack = append(stack, frame{kind: frameScopeCleanup})
			}
			node = bodyNode
			tail = frameTail // body inherits tail from the letrec expression
			evaluating = true
			continue

		case frameDo:
			f.doIdx++
			if f.doIdx < len(f.doExprs)-1 {
				// Not the last expression — discard result, eval next
				node = f.doExprs[f.doIdx]
				tail = false // non-last do expressions are not in tail position
				evaluating = true
				continue
			}
			if f.doIdx == len(f.doExprs)-1 {
				// Last expression — inherit tail
				frameTail := f.tail
				node = f.doExprs[f.doIdx]
				tail = frameTail // last do expression inherits tail
				stack = stack[:len(stack)-1]
				evaluating = true
				continue
			}
			// Past last (shouldn't happen)
			stack = stack[:len(stack)-1]

		case frameFormExpand:
			frameTail := f.tail
			stack = stack[:len(stack)-1]
			// result is the expansion value — convert to AST and eval
			expansionNode, err := valueToNode(result)
			if err != nil {
				e.locals = e.locals[:savedLocalsLen]
				e.currentNodeID = savedNodeID
				return Value{}, fmt.Errorf("form expansion: %w", err)
			}
			node = expansionNode
			tail = frameTail // expansion eval inherits tail from the form call
			evaluating = true
			continue

		case frameApplyFn:
			// result is the fn value
			if result.Kind == ValForm {
				e.locals = e.locals[:savedLocalsLen]
				e.currentNodeID = savedNodeID
				return Value{}, fmt.Errorf("apply: cannot apply a Form (forms receive unevaluated AST)")
			}
			if result.Kind != ValFn {
				e.locals = e.locals[:savedLocalsLen]
				e.currentNodeID = savedNodeID
				return Value{}, fmt.Errorf("apply: first arg must be Fn, got %s", result.KindName())
			}
			applyListNode := f.applyListNode
			frameTail := f.tail
			fn := result.Fn
			stack = stack[:len(stack)-1]
			stack = append(stack, frame{
				kind:    frameApplyList,
				tail:    frameTail,
				applyFn: fn,
			})
			node = applyListNode
			tail = false // apply list arg is not in tail position
			evaluating = true
			continue

		case frameApplyList:
			// result is the list value
			if result.Kind != ValList {
				e.locals = e.locals[:savedLocalsLen]
				e.currentNodeID = savedNodeID
				return Value{}, fmt.Errorf("apply: second arg must be List, got %s", result.KindName())
			}
			fn := f.applyFn
			args := *result.List
			frameTail := f.tail
			stack = stack[:len(stack)-1]
			// Arity check
			if fn.RestParam != "" {
				if len(args) < len(fn.Params) {
					e.locals = e.locals[:savedLocalsLen]
					e.currentNodeID = savedNodeID
					return Value{}, fmt.Errorf("fn: expected at least %d args, got %d", len(fn.Params), len(args))
				}
			} else {
				if len(args) != len(fn.Params) {
					e.locals = e.locals[:savedLocalsLen]
					e.currentNodeID = savedNodeID
					return Value{}, fmt.Errorf("fn: expected %d args, got %d", len(fn.Params), len(args))
				}
			}
			node = e.enterFnBody(fn, args, frameTail, &stack)
			tail = true // fn body is always in tail position
			evaluating = true
			continue
		}
	}
}

// enterFnBody sets up scope for a function call and returns the body node to evaluate.
// If tail=true, it performs TCO by reusing the nearest frameFnBody.
func (e *Evaluator) enterFnBody(fn *FnValue, args []Value, tail bool, stack *[]frame) *Node {
	if tail {
		// TCO: find nearest frameFnBody and reuse it
		for i := len(*stack) - 1; i >= 0; i-- {
			if (*stack)[i].kind == frameFnBody {
				// Reset scope to that frame's scopeBase
				scopeBase := (*stack)[i].scopeBase
				savedNodeID := (*stack)[i].savedNodeID
				e.locals = e.locals[:scopeBase]
				e.currentNodeID = savedNodeID
				// Remove that frame and everything above it
				*stack = (*stack)[:i]
				// Push new frameFnBody
				*stack = append(*stack, frame{
					kind:        frameFnBody,
					tail:        true,
					scopeBase:   scopeBase,
					savedNodeID: savedNodeID,
				})
				// Push closure + bindings
				if fn.Closure != nil {
					e.pushScope(fn.Closure)
				}
				bindings := make(map[string]Value, len(fn.Params)+1)
				for i, param := range fn.Params {
					bindings[param] = args[i]
				}
				if fn.RestParam != "" {
					bindings[fn.RestParam] = ListVal(args[len(fn.Params):])
				}
				e.pushScope(bindings)
				if fn.NodeID != "" {
					e.currentNodeID = fn.NodeID
				}
				return fn.Body
			}
		}
	}

	// Non-tail or no frameFnBody found: push a new one
	scopeBase := len(e.locals)
	prevNodeID := e.currentNodeID
	*stack = append(*stack, frame{
		kind:        frameFnBody,
		tail:        true, // body of fn is always tail
		scopeBase:   scopeBase,
		savedNodeID: prevNodeID,
	})
	if fn.Closure != nil {
		e.pushScope(fn.Closure)
	}
	bindings := make(map[string]Value, len(fn.Params)+1)
	for i, param := range fn.Params {
		bindings[param] = args[i]
	}
	if fn.RestParam != "" {
		bindings[fn.RestParam] = ListVal(args[len(fn.Params):])
	}
	e.pushScope(bindings)
	if fn.NodeID != "" {
		e.currentNodeID = fn.NodeID
	}
	return fn.Body
}

// handleFormCall sets up form expansion. The form body is evaluated via CallFnWithValues
// (which is re-entrant), then the expansion is converted to AST and evaluated.
func (e *Evaluator) handleFormCall(formFn *FnValue, argNodes []*Node, tail bool, stack *[]frame) (*Node, error) {
	if formFn.RestParam != "" {
		if len(argNodes) < len(formFn.Params) {
			return nil, fmt.Errorf("form: expected at least %d args, got %d", len(formFn.Params), len(argNodes))
		}
	} else {
		if len(argNodes) != len(formFn.Params) {
			return nil, fmt.Errorf("form: expected %d args, got %d", len(formFn.Params), len(argNodes))
		}
	}
	// Convert each arg AST to a Value (not evaluated)
	args := make([]Value, len(argNodes))
	for i, argNode := range argNodes {
		args[i] = nodeToValue(argNode)
	}
	// Evaluate form body to produce expansion (re-entrant call)
	expansion, err := e.CallFnWithValues(formFn, args)
	if err != nil {
		return nil, err
	}
	// Convert expansion back to AST
	expansionNode, err := valueToNode(expansion)
	if err != nil {
		return nil, fmt.Errorf("form expansion: %w", err)
	}
	// The expansion will be evaluated in the caller's scope.
	// If tail, it inherits tail position — no extra frame needed,
	// the existing frameFnBody on the stack handles TCO.
	return expansionNode, nil
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

// parseFnParams parses a param list, supporting & rest syntax.
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
func (e *Evaluator) evalQuote(node *Node) (Value, error) {
	if len(node.Children) != 2 {
		return Value{}, fmt.Errorf("quote: expected 1 arg")
	}
	return nodeToValue(node.Children[1]), nil
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

// builtinAssert: (assert condition message)
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
	ra, rb := typeRank(a.Kind), typeRank(b.Kind)
	if ra != rb {
		if ra < rb {
			return -1, nil
		}
		return 1, nil
	}
	switch a.Kind {
	case ValNil:
		return 0, nil
	case ValBool:
		if a.Bool == b.Bool {
			return 0, nil
		}
		if !a.Bool {
			return -1, nil
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
