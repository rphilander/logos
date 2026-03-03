package logos

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Ref struct {
	Symbol string
	NodeID string
}

// SymbolStatus tracks the contract state of a symbol.
type SymbolStatus int

const (
	StatusUntested SymbolStatus = iota // no contract (no tests)
	StatusGreen                        // has contract, all deps current, tests pass
	StatusRed                          // has contract, stale deps (pinned to old version)
)

func (s SymbolStatus) String() string {
	switch s {
	case StatusGreen:
		return "green"
	case StatusRed:
		return "red"
	default:
		return "untested"
	}
}

type GraphNodeTest struct {
	Name   string // label for identification
	Source string // original test expression source
	Expr   *Node  // resolved test AST (builtins + base + self only)
}

type GraphNode struct {
	ID     string
	Expr   *Node
	Refs   []Ref
	Source string
	Tests  []GraphNodeTest // optional: contract tests
}

type Graph struct {
	nodes        map[string]*GraphNode
	symbols      map[string]string
	nameCounters map[string]int
	eval         *Evaluator
	dir          string
	logFile      *os.File
	activeLib    string                  // "" = session, else library name
	libraries    []string                // ordered library names from manifest
	symbolOwner  map[string]string       // symbol name → library name ("" = session)
	symbolStatus map[string]SymbolStatus // symbol name → green/red/untested
}

// coreFormKeywords are symbols that should not be resolved during define.
var coreFormKeywords = map[string]bool{
	"if": true, "let": true, "do": true, "fn": true, "form": true, "quote": true,
	"apply": true, "sort-by": true, "loop": true, "recur": true,
}

// --- Path helpers ---

func (g *Graph) sessionLogPath() string {
	return filepath.Join(g.dir, "log.logos")
}

func (g *Graph) libraryPath(name string) string {
	return filepath.Join(g.dir, name+".logos")
}

func (g *Graph) manifestPath() string {
	return filepath.Join(g.dir, "library-order.txt")
}

// makeNodeID generates a node ID with library prefix when applicable.
func (g *Graph) makeNodeID(name, libName string) string {
	g.nameCounters[name]++
	if libName != "" {
		return fmt.Sprintf("node:%s/%s-%d", libName, name, g.nameCounters[name])
	}
	return fmt.Sprintf("node:%s-%d", name, g.nameCounters[name])
}

// ActiveLibrary returns the currently active library name, or "" for session.
func (g *Graph) ActiveLibrary() string {
	return g.activeLib
}

func NewGraph(dir string, builtins map[string]Builtin) (*Graph, error) {
	g := &Graph{
		nodes:        make(map[string]*GraphNode),
		symbols:      make(map[string]string),
		nameCounters: make(map[string]int),
		symbolOwner:  make(map[string]string),
		symbolStatus: make(map[string]SymbolStatus),
		dir:          dir,
	}

	// Register graph builtins as closures over the graph.
	for k, v := range g.graphBuiltins() {
		builtins[k] = v
	}

	ev := &Evaluator{
		Builtins: builtins,
	}
	ev.ResolveNode = g.resolveNode
	ev.ResolveAST = func(node *Node) (*Node, error) {
		var refs []Ref
		return g.resolveAST(node, &refs, nil)
	}
	builtins["assert"] = ev.builtinAssert
	builtins["step-eval"] = ev.builtinStepEval
	builtins["step-continue"] = ev.builtinStepContinue
	g.eval = ev

	if err := g.loadLibraries(); err != nil {
		return nil, fmt.Errorf("libraries: %w", err)
	}

	if err := g.replayFile(g.sessionLogPath(), ""); err != nil {
		return nil, fmt.Errorf("replay session: %w", err)
	}

	// Set initial symbol status after replay: Green if tests exist, Untested otherwise
	for name, nodeID := range g.symbols {
		node := g.nodes[nodeID]
		if len(node.Tests) > 0 {
			g.symbolStatus[name] = StatusGreen
		} else {
			g.symbolStatus[name] = StatusUntested
		}
	}

	f, err := os.OpenFile(g.sessionLogPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("open log: %w", err)
	}
	g.logFile = f

	return g, nil
}

// EvalSymbol looks up a symbol by name, evaluates it, and returns the result.
func (g *Graph) EvalSymbol(name string) (Value, error) {
	nodeID, ok := g.symbols[name]
	if !ok {
		return Value{}, fmt.Errorf("undefined symbol: %s", name)
	}
	node := g.nodes[nodeID]
	val, err := g.eval.Eval(node.Expr)
	if err != nil {
		return Value{}, err
	}
	if val.Kind == ValFn || val.Kind == ValForm {
		val.Fn.NodeID = nodeID
	}
	return val, nil
}

func (g *Graph) resolveNode(nodeID string) (*Node, error) {
	node, ok := g.nodes[nodeID]
	if !ok {
		return nil, fmt.Errorf("unknown node: %s", nodeID)
	}
	return node.Expr, nil
}

// TestInput represents a test to attach to a symbol during define.
type TestInput struct {
	Name string // label for identification
	Expr string // test expression source
}

func (g *Graph) Define(name, expr string) (*GraphNode, error) {
	return g.DefineWithTests(name, expr, nil)
}

// DefineWithTests creates or redefines a symbol, optionally with tests.
// Tests are a gate: all must pass or the define fails.
func (g *Graph) DefineWithTests(name, expr string, tests []TestInput) (*GraphNode, error) {
	if strings.Contains(name, ":") {
		return nil, fmt.Errorf("define: symbol name cannot contain ':': %s", name)
	}
	if strings.Contains(name, "/") {
		return nil, fmt.Errorf("define: symbol name cannot contain '/': %s", name)
	}

	// Guard rail: reject builtin names
	if g.eval.Builtins != nil {
		if _, ok := g.eval.Builtins[name]; ok {
			return nil, fmt.Errorf("define: cannot redefine builtin: %s", name)
		}
	}

	// Guard rail: check symbol ownership
	if owner, exists := g.symbolOwner[name]; exists && owner != g.activeLib {
		if owner == "" {
			return nil, fmt.Errorf("define: %s is defined in session; close the library to modify it", name)
		}
		return nil, fmt.Errorf("define: %s is defined in library %q; open that library to modify it", name, owner)
	}

	parsed, err := Parse(expr)
	if err != nil {
		return nil, fmt.Errorf("parse error: %w", err)
	}

	var refs []Ref
	resolved, resolveErr := g.resolveAST(parsed, &refs, nil)
	if resolveErr != nil {
		return nil, fmt.Errorf("define %s: %w", name, resolveErr)
	}

	nodeID := g.makeNodeID(name, g.activeLib)

	node := &GraphNode{
		ID:     nodeID,
		Expr:   resolved,
		Refs:   refs,
		Source: expr,
	}

	// If tests provided, resolve and evaluate them as a gate.
	if len(tests) > 0 {
		// Temporarily add node to graph so tests can reference it
		g.nodes[node.ID] = node
		oldNodeID := g.symbols[name]
		g.symbols[name] = node.ID
		oldOwner, hadOwner := g.symbolOwner[name]
		g.symbolOwner[name] = g.activeLib

		var resolvedTests []GraphNodeTest
		var gateErr error

		for _, ti := range tests {
			parsedTest, parseErr := Parse(ti.Expr)
			if parseErr != nil {
				gateErr = fmt.Errorf("define %s: parse test %q: %w", name, ti.Name, parseErr)
				break
			}
			resolvedTest, resolveErr := g.resolveTestAST(parsedTest, &refs, name, nil)
			if resolveErr != nil {
				gateErr = fmt.Errorf("define %s: resolve test %q: %w", name, ti.Name, resolveErr)
				break
			}

			// Evaluate the test with a separate fuel budget
			savedFuel := g.eval.Fuel
			savedFuelSet := g.eval.FuelSet
			g.eval.Fuel = 100000
			g.eval.FuelSet = true
			result, evalErr := g.eval.Eval(resolvedTest)
			g.eval.Fuel = savedFuel
			g.eval.FuelSet = savedFuelSet

			if evalErr != nil {
				gateErr = fmt.Errorf("define %s: test %q failed: %w", name, ti.Name, evalErr)
				break
			}
			if !result.Truthy() {
				gateErr = fmt.Errorf("define %s: test %q returned falsy: %s", name, ti.Name, result.String())
				break
			}

			resolvedTests = append(resolvedTests, GraphNodeTest{
				Name:   ti.Name,
				Source: ti.Expr,
				Expr:   resolvedTest,
			})
		}

		if gateErr != nil {
			// Roll back: restore old symbol state, remove orphan node
			delete(g.nodes, node.ID)
			if oldNodeID != "" {
				g.symbols[name] = oldNodeID
			} else {
				delete(g.symbols, name)
			}
			if hadOwner {
				g.symbolOwner[name] = oldOwner
			} else {
				delete(g.symbolOwner, name)
			}
			return nil, gateErr
		}

		node.Tests = resolvedTests
		node.Refs = refs // update after test resolution added test deps
	}

	// Commit: store node and update symbol table
	g.nodes[node.ID] = node
	g.symbols[name] = node.ID
	g.symbolOwner[name] = g.activeLib
	if len(node.Tests) > 0 {
		g.symbolStatus[name] = StatusGreen
	} else {
		g.symbolStatus[name] = StatusUntested
	}

	// Write define entry to log
	if err := g.appendLog(fmt.Sprintf("(define %s %s)", name, expr)); err != nil {
		return nil, fmt.Errorf("write log: %w", err)
	}
	// Write test entries to log
	for _, test := range node.Tests {
		if err := g.appendLog(formatTestEntry(name, test)); err != nil {
			return nil, fmt.Errorf("write test log: %w", err)
		}
	}

	return node, nil
}

// Refine creates a new node for an existing symbol by applying deltas.
// newExpr is the new expression (empty string = carry forward current).
// addTests are tests to add. removeTests are test names to remove.
// Returns the new node, a propagation flag, and any error.
// Propagation is suppressed when both expression and tests change simultaneously.
func (g *Graph) Refine(name, newExpr string, addTests []TestInput, removeTests []string) (*GraphNode, bool, error) {
	// Look up current node
	nodeID, ok := g.symbols[name]
	if !ok {
		return nil, false, fmt.Errorf("refine: unknown symbol: %s", name)
	}

	// Guard rail: check symbol ownership
	if owner, exists := g.symbolOwner[name]; exists && owner != g.activeLib {
		if owner == "" {
			return nil, false, fmt.Errorf("refine: %s is defined in session; close the library to modify it", name)
		}
		return nil, false, fmt.Errorf("refine: %s is defined in library %q; open that library to modify it", name, owner)
	}

	currentNode := g.nodes[nodeID]

	// Determine what changes
	exprChanged := newExpr != ""
	testsChanged := len(addTests) > 0 || len(removeTests) > 0

	if !exprChanged && !testsChanged {
		return nil, false, fmt.Errorf("refine %s: no changes specified", name)
	}

	// Determine expression: carry forward or use new
	var exprResolved *Node
	var exprRefs []Ref
	exprSource := currentNode.Source

	if exprChanged {
		exprSource = newExpr
		parsed, err := Parse(exprSource)
		if err != nil {
			return nil, false, fmt.Errorf("refine %s: parse: %w", name, err)
		}
		resolved, resolveErr := g.resolveAST(parsed, &exprRefs, nil)
		if resolveErr != nil {
			return nil, false, fmt.Errorf("refine %s: %w", name, resolveErr)
		}
		exprResolved = resolved
	} else {
		// Carry forward expression from current node
		exprResolved = currentNode.Expr
		exprRefs = make([]Ref, len(currentNode.Refs))
		copy(exprRefs, currentNode.Refs)
	}

	// Build test list: start with current, apply removals, then additions
	type testEntry struct{ Name, Source string }
	tests := make([]testEntry, 0, len(currentNode.Tests))
	for _, t := range currentNode.Tests {
		tests = append(tests, testEntry{t.Name, t.Source})
	}

	// Remove tests
	for _, removeName := range removeTests {
		found := false
		for i, t := range tests {
			if t.Name == removeName {
				tests = append(tests[:i], tests[i+1:]...)
				found = true
				break
			}
		}
		if !found {
			return nil, false, fmt.Errorf("refine %s: test %q not found", name, removeName)
		}
	}

	// Check for duplicate names before adding
	for _, ti := range addTests {
		for _, t := range tests {
			if t.Name == ti.Name {
				return nil, false, fmt.Errorf("refine %s: test %q already exists", name, ti.Name)
			}
		}
		tests = append(tests, testEntry{ti.Name, ti.Expr})
	}

	// Create new node
	newNodeID := g.makeNodeID(name, g.activeLib)
	newNode := &GraphNode{
		ID:     newNodeID,
		Expr:   exprResolved,
		Refs:   exprRefs,
		Source: exprSource,
	}

	// Temporarily set node in graph for test self-reference
	g.nodes[newNode.ID] = newNode
	oldNodeID := g.symbols[name]
	g.symbols[name] = newNode.ID

	// Resolve and evaluate all tests (gate)
	var resolvedTests []GraphNodeTest
	var gateErr error

	for _, t := range tests {
		parsedTest, parseErr := Parse(t.Source)
		if parseErr != nil {
			gateErr = fmt.Errorf("refine %s: parse test %q: %w", name, t.Name, parseErr)
			break
		}
		resolvedTest, rErr := g.resolveTestAST(parsedTest, &exprRefs, name, nil)
		if rErr != nil {
			gateErr = fmt.Errorf("refine %s: resolve test %q: %w", name, t.Name, rErr)
			break
		}
		resolvedTests = append(resolvedTests, GraphNodeTest{
			Name:   t.Name,
			Source: t.Source,
			Expr:   resolvedTest,
		})
	}

	// Evaluate tests
	if gateErr == nil && len(resolvedTests) > 0 {
		for _, t := range resolvedTests {
			savedFuel := g.eval.Fuel
			savedFuelSet := g.eval.FuelSet
			g.eval.Fuel = 100000
			g.eval.FuelSet = true
			result, evalErr := g.eval.Eval(t.Expr)
			g.eval.Fuel = savedFuel
			g.eval.FuelSet = savedFuelSet

			if evalErr != nil {
				gateErr = fmt.Errorf("refine %s: test %q failed: %w", name, t.Name, evalErr)
				break
			}
			if !result.Truthy() {
				gateErr = fmt.Errorf("refine %s: test %q returned falsy: %s", name, t.Name, result.String())
				break
			}
		}
	}

	if gateErr != nil {
		// Rollback: restore old symbol, remove orphan node
		delete(g.nodes, newNode.ID)
		g.symbols[name] = oldNodeID
		return nil, false, gateErr
	}

	// Commit
	newNode.Refs = exprRefs // update after test resolution added test deps
	newNode.Tests = resolvedTests
	if len(newNode.Tests) > 0 {
		g.symbolStatus[name] = StatusGreen
	} else {
		g.symbolStatus[name] = StatusUntested
	}

	// Write to log (define + test entries)
	if err := g.appendLog(fmt.Sprintf("(define %s %s)", name, exprSource)); err != nil {
		return nil, false, fmt.Errorf("refine %s: write log: %w", name, err)
	}
	for _, test := range newNode.Tests {
		if err := g.appendLog(formatTestEntry(name, test)); err != nil {
			return nil, false, fmt.Errorf("refine %s: write test log: %w", name, err)
		}
	}

	return newNode, true, nil
}

func (g *Graph) Delete(name string) error {
	_, ok := g.symbols[name]
	if !ok {
		return fmt.Errorf("undefined symbol: %s", name)
	}

	// Guard rail: check symbol ownership
	if owner, exists := g.symbolOwner[name]; exists && owner != g.activeLib {
		if owner == "" {
			return fmt.Errorf("delete: %s is defined in session; close the library to modify it", name)
		}
		return fmt.Errorf("delete: %s is defined in library %q; open that library to modify it", name, owner)
	}

	delete(g.symbols, name)
	delete(g.symbolOwner, name)
	delete(g.symbolStatus, name)

	if err := g.appendLog(fmt.Sprintf("(delete %s)", name)); err != nil {
		return fmt.Errorf("write log: %w", err)
	}
	return nil
}

func (g *Graph) Eval(expr string) (Value, error) {
	parsed, err := Parse(expr)
	if err != nil {
		return Value{}, fmt.Errorf("parse error: %w", err)
	}
	var refs []Ref
	resolved, resolveErr := g.resolveAST(parsed, &refs, nil)
	if resolveErr != nil {
		return Value{}, resolveErr
	}
	return g.eval.Eval(resolved)
}

func (g *Graph) Lookup(name string) (*GraphNode, bool) {
	nodeID, ok := g.symbols[name]
	if !ok {
		return nil, false
	}
	return g.nodes[nodeID], true
}

// ResolveSymbol looks up a symbol by name and evaluates it to a Value.
func (g *Graph) ResolveSymbol(name string) (Value, bool) {
	val, err := g.EvalSymbol(name)
	if err != nil {
		return Value{}, false
	}
	return val, true
}

func (g *Graph) List() []string {
	names := make([]string, 0, len(g.symbols))
	for name := range g.symbols {
		names = append(names, name)
	}
	return names
}

// GetSymbolStatus returns the contract status of a symbol.
func (g *Graph) GetSymbolStatus(name string) (SymbolStatus, bool) {
	status, ok := g.symbolStatus[name]
	return status, ok
}

// RedSymbols returns names of all symbols with status Red.
func (g *Graph) RedSymbols() []string {
	var result []string
	for name, status := range g.symbolStatus {
		if status == StatusRed {
			result = append(result, name)
		}
	}
	sort.Strings(result)
	return result
}

// GreenSymbols returns names of all symbols with status Green.
func (g *Graph) GreenSymbols() []string {
	var result []string
	for name, status := range g.symbolStatus {
		if status == StatusGreen {
			result = append(result, name)
		}
	}
	sort.Strings(result)
	return result
}

func (g *Graph) Close() error {
	if g.logFile != nil {
		return g.logFile.Close()
	}
	return nil
}

// --- resolveAST ---

func copyBoundNames(m map[string]bool) map[string]bool {
	cp := make(map[string]bool, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

func (g *Graph) resolveAST(node *Node, refs *[]Ref, boundNames map[string]bool) (*Node, error) {
	switch node.Kind {
	case NodeSymbol:
		if boundNames[node.Str] {
			return node, nil
		}
		if strings.HasPrefix(node.Str, "node:") {
			nodeID := node.Str
			*refs = append(*refs, Ref{Symbol: node.Str, NodeID: nodeID})
			return &Node{Kind: NodeRef, Str: node.Str, Ref: nodeID}, nil
		}
		if nodeID, ok := g.symbols[node.Str]; ok {
			*refs = append(*refs, Ref{Symbol: node.Str, NodeID: nodeID})
			return &Node{Kind: NodeRef, Str: node.Str, Ref: nodeID}, nil
		}
		// Resolve builtins in non-head position
		if g.eval.Builtins != nil {
			if _, ok := g.eval.Builtins[node.Str]; ok {
				return &Node{Kind: NodeBuiltin, Str: node.Str}, nil
			}
		}
		return nil, fmt.Errorf("unresolved symbol: %s", node.Str)

	case NodeList:
		if len(node.Children) == 0 {
			return node, nil
		}

		head := node.Children[0]
		newChildren := make([]*Node, len(node.Children))

		if head.Kind == NodeSymbol {
			switch head.Str {
			case "fn", "form":
				newChildren[0] = head
				if len(node.Children) >= 2 {
					newChildren[1] = node.Children[1]
				}
				if len(node.Children) >= 3 {
					innerBound := copyBoundNames(boundNames)
					if len(node.Children) >= 2 && node.Children[1].Kind == NodeList {
						for _, p := range node.Children[1].Children {
							if p.Kind == NodeSymbol && p.Str != "&" {
								innerBound[p.Str] = true
							}
						}
					}
					resolved, err := g.resolveAST(node.Children[2], refs, innerBound)
					if err != nil {
						return nil, err
					}
					newChildren[2] = resolved
				}
				for i := 3; i < len(node.Children); i++ {
					newChildren[i] = node.Children[i]
				}
				return &Node{Kind: NodeList, Children: newChildren}, nil

			case "let":
				newChildren[0] = head
				innerBound := copyBoundNames(boundNames)
				if len(node.Children) >= 2 {
					bindingsNode := node.Children[1]
					if bindingsNode.Kind == NodeList {
						newBindings := make([]*Node, len(bindingsNode.Children))
						// Detect flat vs nested pair syntax
						isFlat := len(bindingsNode.Children) == 0 ||
							bindingsNode.Children[0].Kind != NodeList
						if isFlat {
							// Flat alternating: (let (name val name val ...) body)
							for i := 0; i < len(bindingsNode.Children); i += 2 {
								nameNode := bindingsNode.Children[i]
								newBindings[i] = nameNode
								if i+1 < len(bindingsNode.Children) {
									resolved, err := g.resolveAST(bindingsNode.Children[i+1], refs, innerBound)
									if err != nil {
										return nil, err
									}
									newBindings[i+1] = resolved
								}
								if nameNode.Kind == NodeSymbol {
									innerBound[nameNode.Str] = true
								}
							}
						} else {
							// Nested pair: (let ((name val) (name val) ...) body)
							for i, pair := range bindingsNode.Children {
								if pair.Kind == NodeList && len(pair.Children) == 2 {
									resolved, err := g.resolveAST(pair.Children[1], refs, innerBound)
									if err != nil {
										return nil, err
									}
									newBindings[i] = &Node{
										Kind: NodeList,
										Children: []*Node{
											pair.Children[0],
											resolved,
										},
									}
									if pair.Children[0].Kind == NodeSymbol {
										innerBound[pair.Children[0].Str] = true
									}
								} else {
									newBindings[i] = pair
								}
							}
						}
						newChildren[1] = &Node{Kind: NodeList, Children: newBindings}
					} else {
						newChildren[1] = bindingsNode
					}
				}
				if len(node.Children) >= 3 {
					resolved, err := g.resolveAST(node.Children[2], refs, innerBound)
					if err != nil {
						return nil, err
					}
					newChildren[2] = resolved
				}
				for i := 3; i < len(node.Children); i++ {
					newChildren[i] = node.Children[i]
				}
				return &Node{Kind: NodeList, Children: newChildren}, nil

			case "quote":
				// Don't resolve anything inside quote
				return node, nil

			case "loop":
				// (loop ((name1 val1) (name2 val2)) body) — like let with nested pairs
				newChildren[0] = head
				innerBound := copyBoundNames(boundNames)
				if len(node.Children) >= 2 {
					bindingsNode := node.Children[1]
					if bindingsNode.Kind == NodeList {
						newBindings := make([]*Node, len(bindingsNode.Children))
						for i, pair := range bindingsNode.Children {
							if pair.Kind == NodeList && len(pair.Children) == 2 {
								resolved, err := g.resolveAST(pair.Children[1], refs, innerBound)
								if err != nil {
									return nil, err
								}
								newBindings[i] = &Node{
									Kind: NodeList,
									Children: []*Node{
										pair.Children[0],
										resolved,
									},
								}
								if pair.Children[0].Kind == NodeSymbol {
									innerBound[pair.Children[0].Str] = true
								}
							} else {
								newBindings[i] = pair
							}
						}
						newChildren[1] = &Node{Kind: NodeList, Children: newBindings}
					} else {
						newChildren[1] = bindingsNode
					}
				}
				if len(node.Children) >= 3 {
					resolved, err := g.resolveAST(node.Children[2], refs, innerBound)
					if err != nil {
						return nil, err
					}
					newChildren[2] = resolved
				}
				for i := 3; i < len(node.Children); i++ {
					newChildren[i] = node.Children[i]
				}
				return &Node{Kind: NodeList, Children: newChildren}, nil
			}

			// Core form keywords and builtins: don't resolve the head
			if coreFormKeywords[head.Str] {
				newChildren[0] = head
				for i := 1; i < len(node.Children); i++ {
					resolved, err := g.resolveAST(node.Children[i], refs, boundNames)
					if err != nil {
						return nil, err
					}
					newChildren[i] = resolved
				}
				return &Node{Kind: NodeList, Children: newChildren}, nil
			}

			if g.eval.Builtins != nil {
				if _, ok := g.eval.Builtins[head.Str]; ok {
					newChildren[0] = &Node{Kind: NodeBuiltin, Str: head.Str}
					for i := 1; i < len(node.Children); i++ {
						resolved, err := g.resolveAST(node.Children[i], refs, boundNames)
						if err != nil {
							return nil, err
						}
						newChildren[i] = resolved
					}
					return &Node{Kind: NodeList, Children: newChildren}, nil
				}
			}
		}

		// Generic list: resolve all children including head
		for i, child := range node.Children {
			resolved, err := g.resolveAST(child, refs, boundNames)
			if err != nil {
				return nil, err
			}
			newChildren[i] = resolved
		}
		return &Node{Kind: NodeList, Children: newChildren}, nil

	default:
		return node, nil
	}
}

// --- Restricted test resolution ---

// resolveTestAST resolves a test expression with restricted scope:
// only builtins, base library symbols, and the symbol under test are allowed.
// boundNames works the same as in resolveAST (for let/fn/form params).
func (g *Graph) resolveTestAST(node *Node, refs *[]Ref, selfName string, boundNames map[string]bool) (*Node, error) {
	switch node.Kind {
	case NodeSymbol:
		if boundNames[node.Str] {
			return node, nil
		}
		if strings.HasPrefix(node.Str, "node:") {
			*refs = append(*refs, Ref{Symbol: node.Str, NodeID: node.Str})
			return &Node{Kind: NodeRef, Str: node.Str, Ref: node.Str}, nil
		}
		// Allow the symbol under test
		if node.Str == selfName {
			if nodeID, ok := g.symbols[node.Str]; ok {
				*refs = append(*refs, Ref{Symbol: node.Str, NodeID: nodeID})
				return &Node{Kind: NodeRef, Str: node.Str, Ref: nodeID}, nil
			}
			return nil, fmt.Errorf("unresolved symbol: %s", node.Str)
		}
		// Allow builtins
		if g.eval.Builtins != nil {
			if _, ok := g.eval.Builtins[node.Str]; ok {
				return &Node{Kind: NodeBuiltin, Str: node.Str}, nil
			}
		}
		// Allow base and shapes library symbols
		if owner, exists := g.symbolOwner[node.Str]; exists && (owner == "base" || owner == "shapes") {
			if nodeID, ok := g.symbols[node.Str]; ok {
				*refs = append(*refs, Ref{Symbol: node.Str, NodeID: nodeID})
				return &Node{Kind: NodeRef, Str: node.Str, Ref: nodeID}, nil
			}
		}
		return nil, fmt.Errorf("test expressions can only reference builtins, base library, shapes library, and the symbol under test; got: %s", node.Str)

	case NodeList:
		if len(node.Children) == 0 {
			return node, nil
		}

		head := node.Children[0]
		newChildren := make([]*Node, len(node.Children))

		if head.Kind == NodeSymbol {
			switch head.Str {
			case "fn", "form":
				newChildren[0] = head
				if len(node.Children) >= 2 {
					newChildren[1] = node.Children[1]
				}
				if len(node.Children) >= 3 {
					innerBound := copyBoundNames(boundNames)
					if len(node.Children) >= 2 && node.Children[1].Kind == NodeList {
						for _, p := range node.Children[1].Children {
							if p.Kind == NodeSymbol && p.Str != "&" {
								innerBound[p.Str] = true
							}
						}
					}
					resolved, err := g.resolveTestAST(node.Children[2], refs, selfName, innerBound)
					if err != nil {
						return nil, err
					}
					newChildren[2] = resolved
				}
				for i := 3; i < len(node.Children); i++ {
					newChildren[i] = node.Children[i]
				}
				return &Node{Kind: NodeList, Children: newChildren}, nil

			case "let":
				newChildren[0] = head
				innerBound := copyBoundNames(boundNames)
				if len(node.Children) >= 2 {
					bindingsNode := node.Children[1]
					if bindingsNode.Kind == NodeList {
						newBindings := make([]*Node, len(bindingsNode.Children))
						isFlat := len(bindingsNode.Children) == 0 ||
							bindingsNode.Children[0].Kind != NodeList
						if isFlat {
							for i := 0; i < len(bindingsNode.Children); i += 2 {
								nameNode := bindingsNode.Children[i]
								newBindings[i] = nameNode
								if i+1 < len(bindingsNode.Children) {
									resolved, err := g.resolveTestAST(bindingsNode.Children[i+1], refs, selfName, innerBound)
									if err != nil {
										return nil, err
									}
									newBindings[i+1] = resolved
								}
								if nameNode.Kind == NodeSymbol {
									innerBound[nameNode.Str] = true
								}
							}
						} else {
							for i, pair := range bindingsNode.Children {
								if pair.Kind == NodeList && len(pair.Children) == 2 {
									resolved, err := g.resolveTestAST(pair.Children[1], refs, selfName, innerBound)
									if err != nil {
										return nil, err
									}
									newBindings[i] = &Node{
										Kind: NodeList,
										Children: []*Node{
											pair.Children[0],
											resolved,
										},
									}
									if pair.Children[0].Kind == NodeSymbol {
										innerBound[pair.Children[0].Str] = true
									}
								} else {
									newBindings[i] = pair
								}
							}
						}
						newChildren[1] = &Node{Kind: NodeList, Children: newBindings}
					} else {
						newChildren[1] = bindingsNode
					}
				}
				if len(node.Children) >= 3 {
					resolved, err := g.resolveTestAST(node.Children[2], refs, selfName, innerBound)
					if err != nil {
						return nil, err
					}
					newChildren[2] = resolved
				}
				for i := 3; i < len(node.Children); i++ {
					newChildren[i] = node.Children[i]
				}
				return &Node{Kind: NodeList, Children: newChildren}, nil

			case "quote":
				return node, nil

			case "loop":
				newChildren[0] = head
				innerBound := copyBoundNames(boundNames)
				if len(node.Children) >= 2 {
					bindingsNode := node.Children[1]
					if bindingsNode.Kind == NodeList {
						newBindings := make([]*Node, len(bindingsNode.Children))
						for i, pair := range bindingsNode.Children {
							if pair.Kind == NodeList && len(pair.Children) == 2 {
								resolved, err := g.resolveTestAST(pair.Children[1], refs, selfName, innerBound)
								if err != nil {
									return nil, err
								}
								newBindings[i] = &Node{
									Kind: NodeList,
									Children: []*Node{
										pair.Children[0],
										resolved,
									},
								}
								if pair.Children[0].Kind == NodeSymbol {
									innerBound[pair.Children[0].Str] = true
								}
							} else {
								newBindings[i] = pair
							}
						}
						newChildren[1] = &Node{Kind: NodeList, Children: newBindings}
					} else {
						newChildren[1] = bindingsNode
					}
				}
				if len(node.Children) >= 3 {
					resolved, err := g.resolveTestAST(node.Children[2], refs, selfName, innerBound)
					if err != nil {
						return nil, err
					}
					newChildren[2] = resolved
				}
				for i := 3; i < len(node.Children); i++ {
					newChildren[i] = node.Children[i]
				}
				return &Node{Kind: NodeList, Children: newChildren}, nil
			}

			if coreFormKeywords[head.Str] {
				newChildren[0] = head
				for i := 1; i < len(node.Children); i++ {
					resolved, err := g.resolveTestAST(node.Children[i], refs, selfName, boundNames)
					if err != nil {
						return nil, err
					}
					newChildren[i] = resolved
				}
				return &Node{Kind: NodeList, Children: newChildren}, nil
			}

			if g.eval.Builtins != nil {
				if _, ok := g.eval.Builtins[head.Str]; ok {
					newChildren[0] = &Node{Kind: NodeBuiltin, Str: head.Str}
					for i := 1; i < len(node.Children); i++ {
						resolved, err := g.resolveTestAST(node.Children[i], refs, selfName, boundNames)
						if err != nil {
							return nil, err
						}
						newChildren[i] = resolved
					}
					return &Node{Kind: NodeList, Children: newChildren}, nil
				}
			}
		}

		for i, child := range node.Children {
			resolved, err := g.resolveTestAST(child, refs, selfName, boundNames)
			if err != nil {
				return nil, err
			}
			newChildren[i] = resolved
		}
		return &Node{Kind: NodeList, Children: newChildren}, nil

	default:
		return node, nil
	}
}

// --- Graph builtins ---

func (g *Graph) graphBuiltins() map[string]Builtin {
	return map[string]Builtin{
		"symbols":   g.builtinSymbols,
		"node-expr": g.builtinNodeExpr,
		"ref-by":    g.builtinRefBy,
		"follow":    g.builtinFollow,
	}
}

// resolveToNodeID accepts a ValSymbol, ValNodeRef, or ValString and returns the node ID.
func (g *Graph) resolveToNodeID(v Value) (string, error) {
	switch v.Kind {
	case ValSymbol:
		nodeID, ok := g.symbols[v.Str]
		if !ok {
			return "", fmt.Errorf("undefined symbol: %s", v.Str)
		}
		return nodeID, nil
	case ValNodeRef:
		if _, ok := g.nodes[v.Str]; !ok {
			return "", fmt.Errorf("unknown node: %s", v.Str)
		}
		return v.Str, nil
	case ValString:
		if strings.HasPrefix(v.Str, "node:") {
			if _, ok := g.nodes[v.Str]; !ok {
				return "", fmt.Errorf("unknown node: %s", v.Str)
			}
			return v.Str, nil
		}
		nodeID, ok := g.symbols[v.Str]
		if !ok {
			return "", fmt.Errorf("undefined symbol: %s", v.Str)
		}
		return nodeID, nil
	default:
		return "", fmt.Errorf("expected Symbol or NodeRef, got %s", v.KindName())
	}
}

func (g *Graph) builtinSymbols(args []Value) (Value, error) {
	if len(args) != 0 {
		return Value{}, fmt.Errorf("symbols: expected 0 args, got %d", len(args))
	}
	m := make(map[string]Value, len(g.symbols))
	for name, nodeID := range g.symbols {
		m[name] = NodeRefVal(nodeID)
	}
	return MapVal(m), nil
}

func (g *Graph) builtinNodeExpr(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("node-expr: expected 1 arg, got %d", len(args))
	}
	nodeID, err := g.resolveToNodeID(args[0])
	if err != nil {
		return Value{}, fmt.Errorf("node-expr: %w", err)
	}
	node := g.nodes[nodeID]
	return nodeToValue(node.Expr), nil
}

func (g *Graph) builtinRefBy(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("ref-by: expected 1 arg, got %d", len(args))
	}
	nodeID, err := g.resolveToNodeID(args[0])
	if err != nil {
		return Value{}, fmt.Errorf("ref-by: %w", err)
	}
	var result []Value
	for name, symNodeID := range g.symbols {
		node := g.nodes[symNodeID]
		for _, ref := range node.Refs {
			if ref.NodeID == nodeID {
				result = append(result, SymbolVal(name))
				break
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Str < result[j].Str
	})
	return ListVal(result), nil
}

func (g *Graph) builtinFollow(args []Value) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("follow: expected 1 arg (link), got %d", len(args))
	}
	if args[0].Kind != ValLink {
		return Value{}, fmt.Errorf("follow: expected Link, got %s", args[0].KindName())
	}
	name := args[0].Str
	nodeID, ok := g.symbols[name]
	if !ok {
		return Value{}, fmt.Errorf("follow: undefined symbol: %s", name)
	}
	node := g.nodes[nodeID]
	val, err := g.eval.Eval(node.Expr)
	if err != nil {
		return Value{}, fmt.Errorf("follow: eval %s: %w", name, err)
	}
	if val.Kind == ValFn || val.Kind == ValForm {
		val.Fn.NodeID = nodeID
	}
	return val, nil
}

// --- Refresh ---

type RefreshResult struct {
	Refreshed []string // symbols successfully auto-refreshed (Green)
	Red       []string // symbols whose tests failed during cascade
	Stale     []string // symbols with no contract, skipped
}

func (g *Graph) RefreshAll(targets []string, dry bool) (*RefreshResult, error) {
	// Build target set: for symbol targets match by ref.Symbol,
	// for node ID targets match by ref.NodeID.
	targetSymbols := make(map[string]bool)
	targetNodeIDs := make(map[string]bool)
	for _, target := range targets {
		if strings.HasPrefix(target, "node:") {
			if _, ok := g.nodes[target]; !ok {
				return nil, fmt.Errorf("refresh-all: unknown node: %s", target)
			}
			targetNodeIDs[target] = true
		} else {
			if _, ok := g.symbols[target]; !ok {
				return nil, fmt.Errorf("refresh-all: undefined symbol: %s", target)
			}
			targetSymbols[target] = true
		}
	}

	if dry {
		return g.refreshAllDry(targetSymbols, targetNodeIDs)
	}
	return g.refreshAllLive(targetSymbols, targetNodeIDs)
}

// refreshAllDry reports all reachable dependents without modifying state.
func (g *Graph) refreshAllDry(targetSymbols, targetNodeIDs map[string]bool) (*RefreshResult, error) {
	var refreshOrder []string
	visited := make(map[string]bool)

	// Find initial dependents.
	for name, symNodeID := range g.symbols {
		if targetSymbols[name] {
			continue
		}
		node := g.nodes[symNodeID]
		for _, ref := range node.Refs {
			if targetSymbols[ref.Symbol] || targetNodeIDs[ref.NodeID] {
				if !visited[name] {
					visited[name] = true
					refreshOrder = append(refreshOrder, name)
				}
				break
			}
		}
	}

	// Cascade: dependents of dependents.
	for i := 0; i < len(refreshOrder); i++ {
		current := refreshOrder[i]
		for name, symNodeID := range g.symbols {
			if visited[name] || targetSymbols[name] {
				continue
			}
			node := g.nodes[symNodeID]
			for _, ref := range node.Refs {
				if ref.Symbol == current {
					visited[name] = true
					refreshOrder = append(refreshOrder, name)
					break
				}
			}
		}
	}

	sort.Strings(refreshOrder)
	return &RefreshResult{Refreshed: refreshOrder}, nil
}

// refreshAllLive performs BFS with circuit breaker:
// - Symbols with tests: re-resolve, run tests. Pass → Green + continue. Fail → Red + stop.
// - Symbols without tests: stale + stop (no auto-refresh without contract).
func (g *Graph) refreshAllLive(targetSymbols, targetNodeIDs map[string]bool) (*RefreshResult, error) {
	var refreshed, redSymbols, staleSymbols []string
	visited := make(map[string]bool)

	// Find initial dependents.
	var queue []string
	for name, symNodeID := range g.symbols {
		if targetSymbols[name] {
			continue
		}
		node := g.nodes[symNodeID]
		for _, ref := range node.Refs {
			if targetSymbols[ref.Symbol] || targetNodeIDs[ref.NodeID] {
				if !visited[name] {
					visited[name] = true
					queue = append(queue, name)
				}
				break
			}
		}
	}

	// Sort initial queue for deterministic processing.
	sort.Strings(queue)

	// Process queue: BFS with circuit breaker.
	for i := 0; i < len(queue); i++ {
		name := queue[i]
		currentNodeID := g.symbols[name]
		node := g.nodes[currentNodeID]

		// No contract → stale, stop this branch.
		if len(node.Tests) == 0 {
			staleSymbols = append(staleSymbols, name)
			// Don't add dependents — circuit breaker.
			continue
		}

		// Re-parse and re-resolve expression.
		parsed, err := Parse(node.Source)
		if err != nil {
			return nil, fmt.Errorf("refresh-all: re-parse %s: %w", name, err)
		}
		var refs []Ref
		resolved, resolveErr := g.resolveAST(parsed, &refs, nil)
		if resolveErr != nil {
			return nil, fmt.Errorf("refresh-all: resolve %s: %w", name, resolveErr)
		}

		// Create new node (temporary for test evaluation).
		owner := g.symbolOwner[name]
		newNodeID := g.makeNodeID(name, owner)
		newNode := &GraphNode{
			ID:     newNodeID,
			Expr:   resolved,
			Refs:   refs,
			Source: node.Source,
		}
		g.nodes[newNodeID] = newNode
		oldNodeID := g.symbols[name]
		g.symbols[name] = newNodeID

		// Re-resolve tests with restricted scope.
		var newTests []GraphNodeTest
		var gateErr error
		for _, test := range node.Tests {
			parsedTest, parseErr := Parse(test.Source)
			if parseErr != nil {
				gateErr = fmt.Errorf("refresh-all: re-parse test %s/%s: %w", name, test.Name, parseErr)
				break
			}
			resolvedTest, rErr := g.resolveTestAST(parsedTest, &refs, name, nil)
			if rErr != nil {
				gateErr = fmt.Errorf("refresh-all: resolve test %s/%s: %w", name, test.Name, rErr)
				break
			}
			newTests = append(newTests, GraphNodeTest{
				Name:   test.Name,
				Source: test.Source,
				Expr:   resolvedTest,
			})
		}

		// Evaluate tests.
		if gateErr == nil {
			for _, t := range newTests {
				savedFuel := g.eval.Fuel
				savedFuelSet := g.eval.FuelSet
				g.eval.Fuel = 100000
				g.eval.FuelSet = true
				result, evalErr := g.eval.Eval(t.Expr)
				g.eval.Fuel = savedFuel
				g.eval.FuelSet = savedFuelSet

				if evalErr != nil {
					gateErr = evalErr
					break
				}
				if !result.Truthy() {
					gateErr = fmt.Errorf("test %q returned falsy", t.Name)
					break
				}
			}
		}

		if gateErr != nil {
			// Tests failed → Red: rollback, keep old node, stop this branch.
			delete(g.nodes, newNodeID)
			g.symbols[name] = oldNodeID
			g.symbolStatus[name] = StatusRed
			redSymbols = append(redSymbols, name)
			// Don't add dependents — circuit breaker.
			continue
		}

		// Tests passed → Green: commit new node.
		newNode.Refs = refs // update after test resolution added test deps
		newNode.Tests = newTests
		g.symbolStatus[name] = StatusGreen
		refreshed = append(refreshed, name)

		// Log to the owning file.
		logWriter := func(entry string) error {
			return g.appendLogToOwner(name, entry)
		}
		if err := g.writeNodeToLog(name, newNode, logWriter); err != nil {
			return nil, fmt.Errorf("refresh-all: log %s: %w", name, err)
		}

		// Add dependents to queue (continue cascade).
		var newDeps []string
		for depName, symNodeID := range g.symbols {
			if visited[depName] || targetSymbols[depName] {
				continue
			}
			depNode := g.nodes[symNodeID]
			for _, ref := range depNode.Refs {
				if ref.Symbol == name {
					visited[depName] = true
					newDeps = append(newDeps, depName)
					break
				}
			}
		}
		sort.Strings(newDeps)
		queue = append(queue, newDeps...)
	}

	sort.Strings(refreshed)
	sort.Strings(redSymbols)
	sort.Strings(staleSymbols)

	return &RefreshResult{Refreshed: refreshed, Red: redSymbols, Stale: staleSymbols}, nil
}

// appendLogToOwner writes an entry to the log file that owns the given symbol.
// If the owner is the currently active log, use the open file handle.
// Otherwise, open the owner's file, append, and close.
func (g *Graph) appendLogToOwner(name, entry string) error {
	owner := g.symbolOwner[name]
	if owner == g.activeLib {
		// Writing to the currently active log file
		return g.appendLog(entry)
	}
	// Need to write to a different file
	var path string
	if owner == "" {
		path = g.sessionLogPath()
	} else {
		path = g.libraryPath(owner)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(f, "%s\n\n", entry)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	return closeErr
}

// --- Libraries ---

func readManifest(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			names = append(names, line)
		}
	}
	return names, nil
}

func writeManifest(path string, names []string) error {
	var sb strings.Builder
	for _, name := range names {
		sb.WriteString(name)
		sb.WriteByte('\n')
	}
	return os.WriteFile(path, []byte(sb.String()), 0644)
}

func (g *Graph) loadLibraries() error {
	names, err := readManifest(g.manifestPath())
	if err != nil {
		return err
	}

	// Auto-create shapes library if missing (migration path)
	hasShapes := false
	for _, n := range names {
		if n == "shapes" {
			hasShapes = true
			break
		}
	}
	if !hasShapes {
		shapesPath := g.libraryPath("shapes")
		if err := os.WriteFile(shapesPath, []byte{}, 0644); err != nil {
			return fmt.Errorf("auto-create shapes: %w", err)
		}
		// Insert at position 1 (after base)
		if len(names) > 0 {
			names = append(names[:1], append([]string{"shapes"}, names[1:]...)...)
		} else {
			names = append(names, "shapes")
		}
		if err := writeManifest(g.manifestPath(), names); err != nil {
			return fmt.Errorf("auto-create shapes: write manifest: %w", err)
		}
	}

	g.libraries = names

	for _, name := range names {
		path := g.libraryPath(name)
		if err := g.replayFile(path, name); err != nil {
			return fmt.Errorf("library %q: %w", name, err)
		}
	}
	return nil
}

func (g *Graph) replayFile(path, libName string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	entries := splitLogEntries(string(data))
	for _, entry := range entries {
		if err := g.replayEntryForLib(entry, libName); err != nil {
			return fmt.Errorf("replaying %q: %w", entry, err)
		}
	}
	return nil
}

func (g *Graph) replayEntryForLib(entry, libName string) error {
	node, err := Parse(entry)
	if err != nil {
		return err
	}
	if node.Kind != NodeList || len(node.Children) < 2 {
		return fmt.Errorf("invalid log entry: %s", entry)
	}
	cmd := node.Children[0]
	if cmd.Kind != NodeSymbol {
		return fmt.Errorf("invalid log entry command: %s", entry)
	}

	switch cmd.Str {
	case "define":
		if len(node.Children) < 3 {
			return fmt.Errorf("define requires name and value: %s", entry)
		}
		nameNode := node.Children[1]
		if nameNode.Kind != NodeSymbol {
			return fmt.Errorf("define name must be symbol: %s", entry)
		}
		name := nameNode.Str
		exprSource := extractDefineExpr(entry)
		if exprSource == "" {
			return fmt.Errorf("cannot extract expression from define: %s", entry)
		}

		parsed, parseErr := Parse(exprSource)
		if parseErr != nil {
			return fmt.Errorf("parsing expression in define: %w", parseErr)
		}

		var refs []Ref
		resolved, resolveErr := g.resolveAST(parsed, &refs, nil)
		if resolveErr != nil {
			return fmt.Errorf("define %s: %w", name, resolveErr)
		}

		nodeID := g.makeNodeID(name, libName)

		n := &GraphNode{
			ID:     nodeID,
			Expr:   resolved,
			Refs:   refs,
			Source: exprSource,
		}
		g.nodes[n.ID] = n
		g.symbols[name] = n.ID
		g.symbolOwner[name] = libName
		return nil

	case "delete":
		if len(node.Children) != 2 {
			return fmt.Errorf("delete requires name: %s", entry)
		}
		nameNode := node.Children[1]
		if nameNode.Kind != NodeSymbol {
			return fmt.Errorf("delete name must be symbol: %s", entry)
		}
		delete(g.symbols, nameNode.Str)
		delete(g.symbolOwner, nameNode.Str)
		return nil

	case "test":
		// (test symbol-name "label" test-expr)
		if len(node.Children) < 4 {
			return fmt.Errorf("test requires name, label, and expression: %s", entry)
		}
		nameNode := node.Children[1]
		if nameNode.Kind != NodeSymbol {
			return fmt.Errorf("test symbol name must be symbol: %s", entry)
		}
		name := nameNode.Str
		labelNode := node.Children[2]
		if labelNode.Kind != NodeString {
			return fmt.Errorf("test label must be string: %s", entry)
		}
		label := labelNode.Str

		testSource := extractTestExpr(entry)
		if testSource == "" {
			return fmt.Errorf("cannot extract test expression: %s", entry)
		}

		parsedTest, parseErr := Parse(testSource)
		if parseErr != nil {
			return fmt.Errorf("parsing test expression: %w", parseErr)
		}

		// Resolve test with restricted scope (builtins + base + shapes + self)
		nodeID, ok := g.symbols[name]
		if !ok {
			return fmt.Errorf("test: symbol %s not defined", name)
		}
		gn := g.nodes[nodeID]
		var testRefs []Ref
		resolvedTest, resolveErr := g.resolveTestAST(parsedTest, &testRefs, name, nil)
		if resolveErr != nil {
			return fmt.Errorf("test %s/%s: %w", name, label, resolveErr)
		}

		// Attach test and merge refs into the node
		gn.Tests = append(gn.Tests, GraphNodeTest{
			Name:   label,
			Source: testSource,
			Expr:   resolvedTest,
		})
		gn.Refs = append(gn.Refs, testRefs...)
		g.symbolStatus[name] = StatusGreen
		return nil

	default:
		return fmt.Errorf("unknown log command: %s", cmd.Str)
	}
}

func (g *Graph) LibraryCreate(name string) error {
	if strings.Contains(name, "/") || strings.Contains(name, ":") {
		return fmt.Errorf("library-create: invalid library name: %s", name)
	}
	if name == "" {
		return fmt.Errorf("library-create: name cannot be empty")
	}
	// Check if already exists
	for _, lib := range g.libraries {
		if lib == name {
			return fmt.Errorf("library-create: library %q already exists", name)
		}
	}
	// Create empty file
	path := g.libraryPath(name)
	if err := os.WriteFile(path, []byte{}, 0644); err != nil {
		return fmt.Errorf("library-create: %w", err)
	}
	// Append to manifest
	g.libraries = append(g.libraries, name)
	if err := writeManifest(g.manifestPath(), g.libraries); err != nil {
		return fmt.Errorf("library-create: write manifest: %w", err)
	}
	return nil
}

func (g *Graph) LibraryDelete(name string) error {
	// Protect special libraries
	if name == "base" || name == "shapes" {
		return fmt.Errorf("library-delete: %q is a protected library and cannot be deleted", name)
	}
	// Check library exists
	found := false
	for _, lib := range g.libraries {
		if lib == name {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("library-delete: library %q not found", name)
	}
	// Check not currently open
	if g.activeLib == name {
		return fmt.Errorf("library-delete: library %q is currently open; close it first", name)
	}
	// Check no symbols owned
	for sym, owner := range g.symbolOwner {
		if owner == name {
			return fmt.Errorf("library-delete: library %q still owns symbol %q; delete it first", name, sym)
		}
	}
	// Remove from manifest
	var filtered []string
	for _, lib := range g.libraries {
		if lib != name {
			filtered = append(filtered, lib)
		}
	}
	g.libraries = filtered
	if err := writeManifest(g.manifestPath(), g.libraries); err != nil {
		return fmt.Errorf("library-delete: write manifest: %w", err)
	}
	// Remove file
	if err := os.Remove(g.libraryPath(name)); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("library-delete: remove file: %w", err)
	}
	return nil
}

func (g *Graph) LibraryOpen(name string) error {
	// Check library exists in manifest
	found := false
	for _, lib := range g.libraries {
		if lib == name {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("library-open: library %q not found in manifest", name)
	}
	if g.activeLib != "" {
		return fmt.Errorf("library-open: library %q is already open; close it first", g.activeLib)
	}
	// Close session log file
	if g.logFile != nil {
		g.logFile.Close()
		g.logFile = nil
	}
	// Open library file for appending
	f, err := os.OpenFile(g.libraryPath(name), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		// Try to reopen session log on error
		sf, _ := os.OpenFile(g.sessionLogPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		g.logFile = sf
		return fmt.Errorf("library-open: %w", err)
	}
	g.logFile = f
	g.activeLib = name
	return nil
}

func (g *Graph) LibraryClose() error {
	if g.activeLib == "" {
		return fmt.Errorf("library-close: no library is open")
	}
	// Close library file
	if g.logFile != nil {
		g.logFile.Close()
		g.logFile = nil
	}
	g.activeLib = ""
	// Reopen session log
	f, err := os.OpenFile(g.sessionLogPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("library-close: reopen session log: %w", err)
	}
	g.logFile = f
	return nil
}

func (g *Graph) LibraryCompact(name string) error {
	// Check library exists
	found := false
	for _, lib := range g.libraries {
		if lib == name {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("library-compact: library %q not found", name)
	}
	// Must not be currently open
	if g.activeLib == name {
		return fmt.Errorf("library-compact: library %q is currently open; close it first", name)
	}

	path := g.libraryPath(name)

	// Collect live symbols owned by this library
	libSymbols := make(map[string]bool)
	for symName, owner := range g.symbolOwner {
		if owner == name {
			if _, exists := g.symbols[symName]; exists {
				libSymbols[symName] = true
			}
		}
	}

	// Topological sort using Kahn's algorithm.
	// Build in-degree map counting intra-library dependencies.
	inDegree := make(map[string]int)
	// dependents[B] = list of symbols that depend on B
	dependents := make(map[string][]string)
	for sym := range libSymbols {
		inDegree[sym] = 0
	}
	for sym := range libSymbols {
		nodeID := g.symbols[sym]
		node := g.nodes[nodeID]
		seen := make(map[string]bool)
		for _, ref := range node.Refs {
			if libSymbols[ref.Symbol] && ref.Symbol != sym && !seen[ref.Symbol] {
				seen[ref.Symbol] = true
				inDegree[sym]++
				dependents[ref.Symbol] = append(dependents[ref.Symbol], sym)
			}
		}
	}

	// Seed queue with symbols that have no intra-library dependencies
	var queue []string
	for sym := range libSymbols {
		if inDegree[sym] == 0 {
			queue = append(queue, sym)
		}
	}
	sort.Strings(queue) // stable tie-breaking

	var orderedNames []string
	for len(queue) > 0 {
		sym := queue[0]
		queue = queue[1:]
		orderedNames = append(orderedNames, sym)
		// Sort dependents for deterministic ordering
		deps := dependents[sym]
		sort.Strings(deps)
		for _, dep := range deps {
			inDegree[dep]--
			if inDegree[dep] == 0 {
				queue = append(queue, dep)
			}
		}
		// Re-sort queue after adding new items
		sort.Strings(queue)
	}

	// Write live symbols in dependency order
	var sb strings.Builder
	for _, symName := range orderedNames {
		owner, ownerExists := g.symbolOwner[symName]
		if !ownerExists || owner != name {
			continue // symbol was deleted or moved
		}
		nodeID, symExists := g.symbols[symName]
		if !symExists {
			continue
		}
		node := g.nodes[nodeID]
		fmt.Fprintf(&sb, "(define %s %s)\n\n", symName, node.Source)
		for _, test := range node.Tests {
			fmt.Fprintf(&sb, "%s\n\n", formatTestEntry(symName, test))
		}
	}

	return os.WriteFile(path, []byte(sb.String()), 0644)
}

func (g *Graph) Promote(name, targetLib string) (*GraphNode, error) {
	// Symbol must exist
	nodeID, ok := g.symbols[name]
	if !ok {
		return nil, fmt.Errorf("promote: unknown symbol: %s", name)
	}

	// Must be session-owned
	owner := g.symbolOwner[name]
	if owner != "" {
		return nil, fmt.Errorf("promote: %s is owned by library %q, not session", name, owner)
	}

	// No library currently open
	if g.activeLib != "" {
		return nil, fmt.Errorf("promote: library %q is open; close it first", g.activeLib)
	}

	// Target library must exist
	found := false
	for _, lib := range g.libraries {
		if lib == targetLib {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("promote: target library %q not found", targetLib)
	}

	// Get source and tests from current node
	oldNode := g.nodes[nodeID]
	source := oldNode.Source
	oldTests := oldNode.Tests

	// Remove session ownership so Define won't reject it
	delete(g.symbolOwner, name)

	// Open target library, define there, close it
	if err := g.LibraryOpen(targetLib); err != nil {
		g.symbolOwner[name] = "" // restore on error
		return nil, fmt.Errorf("promote: %w", err)
	}
	node, err := g.Define(name, source)
	if err != nil {
		g.symbolOwner[name] = "" // restore on error
		g.LibraryClose()
		return nil, fmt.Errorf("promote: %w", err)
	}

	// Carry forward tests from old node and write test entries to library log
	if len(oldTests) > 0 {
		// Re-resolve tests in the new library context
		var newTests []GraphNodeTest
		for _, test := range oldTests {
			parsedTest, parseErr := Parse(test.Source)
			if parseErr != nil {
				g.LibraryClose()
				return nil, fmt.Errorf("promote: re-parse test %s/%s: %w", name, test.Name, parseErr)
			}
			resolvedTest, resolveErr := g.resolveTestAST(parsedTest, &node.Refs, name, nil)
			if resolveErr != nil {
				g.LibraryClose()
				return nil, fmt.Errorf("promote: resolve test %s/%s: %w", name, test.Name, resolveErr)
			}
			newTests = append(newTests, GraphNodeTest{
				Name:   test.Name,
				Source: test.Source,
				Expr:   resolvedTest,
			})
			if err := g.appendLog(formatTestEntry(name, test)); err != nil {
				g.LibraryClose()
				return nil, fmt.Errorf("promote: log test %s/%s: %w", name, test.Name, err)
			}
		}
		node.Tests = newTests
	}

	if err := g.LibraryClose(); err != nil {
		return nil, fmt.Errorf("promote: %w", err)
	}

	// Compact session log to purge the old define
	if err := g.compactSession(); err != nil {
		return nil, fmt.Errorf("promote: compact session: %w", err)
	}

	return node, nil
}

func (g *Graph) compactSession() error {
	// Close current session log
	if g.logFile != nil {
		g.logFile.Close()
		g.logFile = nil
	}

	// Rewrite with only live session-owned symbols
	path := g.sessionLogPath()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	var orderedNames []string
	seen := make(map[string]bool)
	if len(data) > 0 {
		entries := splitLogEntries(string(data))
		for _, entry := range entries {
			n, err := Parse(entry)
			if err != nil {
				continue
			}
			if n.Kind != NodeList || len(n.Children) < 3 {
				continue
			}
			if n.Children[0].Kind != NodeSymbol || n.Children[0].Str != "define" {
				continue
			}
			symName := n.Children[1].Str
			if !seen[symName] {
				seen[symName] = true
				orderedNames = append(orderedNames, symName)
			}
		}
	}

	var sb strings.Builder
	for _, symName := range orderedNames {
		owner, ownerExists := g.symbolOwner[symName]
		if !ownerExists || owner != "" {
			continue // not session-owned
		}
		nid, symExists := g.symbols[symName]
		if !symExists {
			continue
		}
		gn := g.nodes[nid]
		fmt.Fprintf(&sb, "(define %s %s)\n\n", symName, gn.Source)
		for _, test := range gn.Tests {
			fmt.Fprintf(&sb, "%s\n\n", formatTestEntry(symName, test))
		}
	}

	if err := os.WriteFile(path, []byte(sb.String()), 0644); err != nil {
		return err
	}

	// Reopen session log for appending
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	g.logFile = f
	return nil
}

func (g *Graph) LibraryOrder() []string {
	result := make([]string, len(g.libraries))
	copy(result, g.libraries)
	return result
}

func (g *Graph) LibraryOrderSet(names []string) error {
	// Enforce special library positions: if present, base must be first, shapes must be second
	for i, name := range names {
		if name == "base" && i != 0 {
			return fmt.Errorf("library-order-set: \"base\" must be the first library")
		}
		if name == "shapes" {
			expectedPos := 1
			hasBase := false
			for _, n := range names {
				if n == "base" {
					hasBase = true
					break
				}
			}
			if !hasBase {
				expectedPos = 0
			}
			if i != expectedPos {
				return fmt.Errorf("library-order-set: \"shapes\" must be the second library (after base)")
			}
		}
	}
	// Validate all names have corresponding files
	for _, name := range names {
		path := g.libraryPath(name)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return fmt.Errorf("library-order-set: library file not found for %q", name)
		}
	}
	g.libraries = make([]string, len(names))
	copy(g.libraries, names)
	return writeManifest(g.manifestPath(), g.libraries)
}

// --- Clear ---

func (g *Graph) Clear() error {
	// Close current log file
	if g.logFile != nil {
		g.logFile.Close()
		g.logFile = nil
	}

	// Truncate session log
	if err := os.Remove(g.sessionLogPath()); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clear: remove log: %w", err)
	}

	// Reset graph state
	g.nodes = make(map[string]*GraphNode)
	g.symbols = make(map[string]string)
	g.nameCounters = make(map[string]int)
	g.symbolOwner = make(map[string]string)
	g.symbolStatus = make(map[string]SymbolStatus)
	g.activeLib = ""

	// Reload libraries
	if err := g.loadLibraries(); err != nil {
		return fmt.Errorf("clear: reload libraries: %w", err)
	}

	// Reopen session log file
	f, err := os.OpenFile(g.sessionLogPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("clear: reopen log: %w", err)
	}
	g.logFile = f
	return nil
}

// --- Log ---

func (g *Graph) appendLog(entry string) error {
	_, err := fmt.Fprintf(g.logFile, "%s\n\n", entry)
	return err
}

// writeNodeToLog writes a define entry and any test entries for a node.
func (g *Graph) writeNodeToLog(name string, node *GraphNode, writer func(string) error) error {
	if err := writer(fmt.Sprintf("(define %s %s)", name, node.Source)); err != nil {
		return err
	}
	for _, test := range node.Tests {
		if err := writer(formatTestEntry(name, test)); err != nil {
			return err
		}
	}
	return nil
}

func extractDefineExpr(entry string) string {
	s := strings.TrimSpace(entry)
	if !strings.HasPrefix(s, "(") || !strings.HasSuffix(s, ")") {
		return ""
	}
	inner := s[1 : len(s)-1]
	inner = strings.TrimSpace(inner)

	if !strings.HasPrefix(inner, "define") {
		return ""
	}
	inner = strings.TrimSpace(inner[6:])

	i := 0
	for i < len(inner) && inner[i] != ' ' && inner[i] != '\t' && inner[i] != '\n' && inner[i] != '(' && inner[i] != ')' {
		i++
	}
	if i == 0 {
		return ""
	}
	inner = strings.TrimSpace(inner[i:])
	return inner
}

// extractTestExpr extracts the test expression from a (test name "label" expr) entry.
func extractTestExpr(entry string) string {
	s := strings.TrimSpace(entry)
	if !strings.HasPrefix(s, "(") || !strings.HasSuffix(s, ")") {
		return ""
	}
	inner := s[1 : len(s)-1]
	inner = strings.TrimSpace(inner)

	// Skip "test"
	if !strings.HasPrefix(inner, "test") {
		return ""
	}
	inner = strings.TrimSpace(inner[4:])

	// Skip symbol name
	i := 0
	for i < len(inner) && inner[i] != ' ' && inner[i] != '\t' && inner[i] != '\n' {
		i++
	}
	inner = strings.TrimSpace(inner[i:])

	// Skip the string label — find opening quote, scan to closing quote
	if len(inner) == 0 || inner[0] != '"' {
		return ""
	}
	i = 1
	for i < len(inner) {
		if inner[i] == '\\' {
			i += 2
			continue
		}
		if inner[i] == '"' {
			break
		}
		i++
	}
	if i >= len(inner) {
		return ""
	}
	inner = strings.TrimSpace(inner[i+1:])
	return inner
}

// formatTestEntry formats a test log entry.
func formatTestEntry(symbolName string, test GraphNodeTest) string {
	return fmt.Sprintf("(test %s %q %s)", symbolName, test.Name, test.Source)
}

func splitLogEntries(data string) []string {
	raw := strings.Split(data, "\n\n")
	var entries []string
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s != "" {
			entries = append(entries, s)
		}
	}
	return entries
}
