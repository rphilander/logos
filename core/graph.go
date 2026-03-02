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

type GraphNode struct {
	ID     string
	Expr   *Node
	Refs   []Ref
	Source string
}

type Graph struct {
	nodes        map[string]*GraphNode
	symbols      map[string]string
	nameCounters map[string]int
	eval         *Evaluator
	dir          string
	logFile      *os.File
	activeLib    string            // "" = session, else library name
	libraries    []string          // ordered library names from manifest
	symbolOwner  map[string]string // symbol name → library name ("" = session)
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

func (g *Graph) Define(name, expr string) (*GraphNode, error) {
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
	g.nodes[node.ID] = node
	g.symbols[name] = node.ID
	g.symbolOwner[name] = g.activeLib

	if err := g.appendLog(fmt.Sprintf("(define %s %s)", name, expr)); err != nil {
		return nil, fmt.Errorf("write log: %w", err)
	}

	return node, nil
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
	Refreshed []string
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

	// BFS: find all symbols to refresh.
	var refreshOrder []string
	refreshed := make(map[string]bool)

	// Find initial dependents.
	for name, symNodeID := range g.symbols {
		if targetSymbols[name] {
			continue // skip targets themselves
		}
		node := g.nodes[symNodeID]
		for _, ref := range node.Refs {
			if targetSymbols[ref.Symbol] || targetNodeIDs[ref.NodeID] {
				if !refreshed[name] {
					refreshed[name] = true
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
			if refreshed[name] || targetSymbols[name] {
				continue
			}
			node := g.nodes[symNodeID]
			for _, ref := range node.Refs {
				if ref.Symbol == current {
					refreshed[name] = true
					refreshOrder = append(refreshOrder, name)
					break
				}
			}
		}
	}

	// Sort for deterministic output.
	sort.Strings(refreshOrder)

	if dry {
		return &RefreshResult{Refreshed: refreshOrder}, nil
	}

	// Re-parse and re-resolve each dependent, logging to owning file.
	for _, name := range refreshOrder {
		currentNodeID := g.symbols[name]
		node := g.nodes[currentNodeID]

		parsed, err := Parse(node.Source)
		if err != nil {
			return nil, fmt.Errorf("refresh-all: re-parse %s: %w", name, err)
		}

		var refs []Ref
		resolved, resolveErr := g.resolveAST(parsed, &refs, nil)
		if resolveErr != nil {
			return nil, fmt.Errorf("refresh-all: resolve %s: %w", name, resolveErr)
		}

		owner := g.symbolOwner[name]
		newNodeID := g.makeNodeID(name, owner)

		newNode := &GraphNode{
			ID:     newNodeID,
			Expr:   resolved,
			Refs:   refs,
			Source: node.Source,
		}
		g.nodes[newNodeID] = newNode
		g.symbols[name] = newNodeID

		// Log to the owning file
		if err := g.appendLogToOwner(name, fmt.Sprintf("(define %s %s)", name, node.Source)); err != nil {
			return nil, fmt.Errorf("refresh-all: log %s: %w", name, err)
		}
	}

	return &RefreshResult{Refreshed: refreshOrder}, nil
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

	// Read current file to get define order
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("library-compact: %w", err)
	}

	entries := splitLogEntries(string(data))

	// Track which symbols we've already seen (preserve first occurrence order)
	seen := make(map[string]bool)
	var orderedNames []string
	for _, entry := range entries {
		node, err := Parse(entry)
		if err != nil {
			continue
		}
		if node.Kind != NodeList || len(node.Children) < 3 {
			continue
		}
		if node.Children[0].Kind != NodeSymbol || node.Children[0].Str != "define" {
			continue
		}
		symName := node.Children[1].Str
		if !seen[symName] {
			seen[symName] = true
			orderedNames = append(orderedNames, symName)
		}
	}

	// Write only live symbols in their original define order
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
	}

	return os.WriteFile(path, []byte(sb.String()), 0644)
}

func (g *Graph) LibraryOrder() []string {
	result := make([]string, len(g.libraries))
	copy(result, g.libraries)
	return result
}

func (g *Graph) LibraryOrderSet(names []string) error {
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
