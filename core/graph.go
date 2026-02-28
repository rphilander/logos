package logos

import (
	"fmt"
	"os"
	"path/filepath"
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
	logPath      string
	logFile      *os.File
}

// coreFormKeywords are symbols that should not be resolved during define.
var coreFormKeywords = map[string]bool{
	"if": true, "let": true, "do": true, "fn": true, "quote": true,
	"cond": true, "case": true,
}

func NewGraph(dir string, builtins map[string]Builtin) (*Graph, error) {
	logPath := filepath.Join(dir, "log.logos")

	g := &Graph{
		nodes:        make(map[string]*GraphNode),
		symbols:      make(map[string]string),
		nameCounters: make(map[string]int),
		logPath:      logPath,
	}

	ev := &Evaluator{
		Builtins: builtins,
	}
	ev.Resolve = g.makeResolver(ev)
	ev.ResolveNode = g.resolveNode
	g.eval = ev

	if err := g.replay(); err != nil {
		return nil, fmt.Errorf("replay: %w", err)
	}

	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("open log: %w", err)
	}
	g.logFile = f

	return g, nil
}

func (g *Graph) makeResolver(ev *Evaluator) Resolver {
	return func(name string) (Value, bool) {
		if strings.HasPrefix(name, "node:") {
			node, ok := g.nodes[name]
			if !ok {
				return Value{}, false
			}
			val, err := ev.Eval(node.Expr)
			if err != nil {
				return Value{}, false
			}
			if val.Kind == ValFn {
				val.Fn.NodeID = name
			}
			return val, true
		}

		nodeID, ok := g.symbols[name]
		if !ok {
			return Value{}, false
		}
		node := g.nodes[nodeID]
		val, err := ev.Eval(node.Expr)
		if err != nil {
			return Value{}, false
		}
		if val.Kind == ValFn {
			val.Fn.NodeID = nodeID
		}
		return val, true
	}
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

	parsed, err := Parse(expr)
	if err != nil {
		return nil, fmt.Errorf("parse error: %w", err)
	}

	var refs []Ref
	resolved := g.resolveAST(parsed, &refs, nil)

	g.nameCounters[name]++
	nodeID := fmt.Sprintf("node:%s-%d", name, g.nameCounters[name])

	node := &GraphNode{
		ID:     nodeID,
		Expr:   resolved,
		Refs:   refs,
		Source: expr,
	}
	g.nodes[node.ID] = node
	g.symbols[name] = node.ID

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
	delete(g.symbols, name)

	if err := g.appendLog(fmt.Sprintf("(delete %s)", name)); err != nil {
		return fmt.Errorf("write log: %w", err)
	}
	return nil
}

func (g *Graph) Eval(expr string) (Value, error) {
	return g.eval.EvalString(expr)
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
	return g.eval.Resolve(name)
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

func (g *Graph) resolveAST(node *Node, refs *[]Ref, boundNames map[string]bool) *Node {
	switch node.Kind {
	case NodeSymbol:
		if boundNames[node.Str] {
			return node
		}
		if strings.HasPrefix(node.Str, "node:") {
			nodeID := node.Str
			*refs = append(*refs, Ref{Symbol: node.Str, NodeID: nodeID})
			return &Node{Kind: NodeRef, Str: node.Str, Ref: nodeID}
		}
		if nodeID, ok := g.symbols[node.Str]; ok {
			*refs = append(*refs, Ref{Symbol: node.Str, NodeID: nodeID})
			return &Node{Kind: NodeRef, Str: node.Str, Ref: nodeID}
		}
		return node

	case NodeList:
		if len(node.Children) == 0 {
			return node
		}

		head := node.Children[0]
		newChildren := make([]*Node, len(node.Children))

		if head.Kind == NodeSymbol {
			switch head.Str {
			case "fn":
				newChildren[0] = head
				if len(node.Children) >= 2 {
					newChildren[1] = node.Children[1]
				}
				if len(node.Children) >= 3 {
					innerBound := copyBoundNames(boundNames)
					if len(node.Children) >= 2 && node.Children[1].Kind == NodeList {
						for _, p := range node.Children[1].Children {
							if p.Kind == NodeSymbol {
								innerBound[p.Str] = true
							}
						}
					}
					newChildren[2] = g.resolveAST(node.Children[2], refs, innerBound)
				}
				for i := 3; i < len(node.Children); i++ {
					newChildren[i] = node.Children[i]
				}
				return &Node{Kind: NodeList, Children: newChildren}

			case "let":
				newChildren[0] = head
				innerBound := copyBoundNames(boundNames)
				if len(node.Children) >= 2 {
					bindingsNode := node.Children[1]
					if bindingsNode.Kind == NodeList {
						newBindings := make([]*Node, len(bindingsNode.Children))
						for i, pair := range bindingsNode.Children {
							if pair.Kind == NodeList && len(pair.Children) == 2 {
								newBindings[i] = &Node{
									Kind: NodeList,
									Children: []*Node{
										pair.Children[0],
										g.resolveAST(pair.Children[1], refs, innerBound),
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
					newChildren[2] = g.resolveAST(node.Children[2], refs, innerBound)
				}
				for i := 3; i < len(node.Children); i++ {
					newChildren[i] = node.Children[i]
				}
				return &Node{Kind: NodeList, Children: newChildren}

			case "quote":
				// Don't resolve anything inside quote
				return node
			}

			// Core form keywords and builtins: don't resolve the head
			if coreFormKeywords[head.Str] {
				newChildren[0] = head
				for i := 1; i < len(node.Children); i++ {
					newChildren[i] = g.resolveAST(node.Children[i], refs, boundNames)
				}
				return &Node{Kind: NodeList, Children: newChildren}
			}

			if g.eval.Builtins != nil {
				if _, ok := g.eval.Builtins[head.Str]; ok {
					newChildren[0] = head
					for i := 1; i < len(node.Children); i++ {
						newChildren[i] = g.resolveAST(node.Children[i], refs, boundNames)
					}
					return &Node{Kind: NodeList, Children: newChildren}
				}
			}
		}

		// Generic list: resolve all children including head
		for i, child := range node.Children {
			newChildren[i] = g.resolveAST(child, refs, boundNames)
		}
		return &Node{Kind: NodeList, Children: newChildren}

	default:
		return node
	}
}

// --- Log ---

func (g *Graph) appendLog(entry string) error {
	_, err := fmt.Fprintf(g.logFile, "%s\n\n", entry)
	return err
}

func (g *Graph) replay() error {
	data, err := os.ReadFile(g.logPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	entries := splitLogEntries(string(data))
	for _, entry := range entries {
		if err := g.replayEntry(entry); err != nil {
			return fmt.Errorf("replaying %q: %w", entry, err)
		}
	}
	return nil
}

func (g *Graph) replayEntry(entry string) error {
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
		resolved := g.resolveAST(parsed, &refs, nil)

		g.nameCounters[name]++
		nodeID := fmt.Sprintf("node:%s-%d", name, g.nameCounters[name])

		n := &GraphNode{
			ID:     nodeID,
			Expr:   resolved,
			Refs:   refs,
			Source: exprSource,
		}
		g.nodes[n.ID] = n
		g.symbols[name] = n.ID
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
		return nil

	default:
		return fmt.Errorf("unknown log command: %s", cmd.Str)
	}
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
