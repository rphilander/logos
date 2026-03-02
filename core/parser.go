package logos

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

type NodeKind int

const (
	NodeInt    NodeKind = iota
	NodeFloat
	NodeBool
	NodeString
	NodeSymbol
	NodeList
	NodeRef     // resolved reference: Str = original symbol name, Ref = node ID
	NodeNil
	NodeKeyword
	NodeBuiltin // resolved builtin: Str = builtin name
)

type Node struct {
	Kind     NodeKind
	Int      int64
	Float    float64
	Bool     bool
	Str      string
	Ref      string // node ID, used only for NodeRef
	Children []*Node
}

func (n *Node) String() string {
	switch n.Kind {
	case NodeInt:
		return strconv.FormatInt(n.Int, 10)
	case NodeFloat:
		return strconv.FormatFloat(n.Float, 'g', -1, 64)
	case NodeBool:
		if n.Bool {
			return "true"
		}
		return "false"
	case NodeString:
		return fmt.Sprintf("%q", n.Str)
	case NodeSymbol:
		return n.Str
	case NodeRef:
		return n.Str
	case NodeNil:
		return "nil"
	case NodeKeyword:
		return ":" + n.Str
	case NodeBuiltin:
		return n.Str
	case NodeList:
		parts := make([]string, len(n.Children))
		for i, c := range n.Children {
			parts[i] = c.String()
		}
		return "(" + strings.Join(parts, " ") + ")"
	default:
		return "<unknown>"
	}
}

type parser struct {
	input []rune
	pos   int
}

func Parse(input string) (*Node, error) {
	p := &parser{input: []rune(input), pos: 0}
	p.skipWhitespace()
	if p.pos >= len(p.input) {
		return nil, fmt.Errorf("empty input")
	}
	node, err := p.parseNode()
	if err != nil {
		return nil, err
	}
	p.skipWhitespace()
	if p.pos < len(p.input) {
		return nil, fmt.Errorf("unexpected input after expression at position %d", p.pos)
	}
	return node, nil
}

func (p *parser) parseNode() (*Node, error) {
	if p.pos >= len(p.input) {
		return nil, fmt.Errorf("unexpected end of input")
	}
	ch := p.input[p.pos]
	switch {
	case ch == '\'':
		return p.parseQuote()
	case ch == '(':
		return p.parseList()
	case ch == '"':
		return p.parseString()
	default:
		return p.parseAtom()
	}
}

func (p *parser) parseQuote() (*Node, error) {
	p.pos++ // skip '\''
	inner, err := p.parseNode()
	if err != nil {
		return nil, err
	}
	return &Node{
		Kind: NodeList,
		Children: []*Node{
			{Kind: NodeSymbol, Str: "quote"},
			inner,
		},
	}, nil
}

func (p *parser) parseList() (*Node, error) {
	p.pos++ // skip '('
	var children []*Node
	for {
		p.skipWhitespace()
		if p.pos >= len(p.input) {
			return nil, fmt.Errorf("unclosed list")
		}
		if p.input[p.pos] == ')' {
			p.pos++ // skip ')'
			return &Node{Kind: NodeList, Children: children}, nil
		}
		child, err := p.parseNode()
		if err != nil {
			return nil, err
		}
		children = append(children, child)
	}
}

func (p *parser) parseString() (*Node, error) {
	p.pos++ // skip opening '"'
	var buf strings.Builder
	for p.pos < len(p.input) {
		ch := p.input[p.pos]
		if ch == '\\' {
			p.pos++
			if p.pos >= len(p.input) {
				return nil, fmt.Errorf("unexpected end of input in string escape")
			}
			esc := p.input[p.pos]
			switch esc {
			case 'n':
				buf.WriteRune('\n')
			case 't':
				buf.WriteRune('\t')
			case '\\':
				buf.WriteRune('\\')
			case '"':
				buf.WriteRune('"')
			default:
				return nil, fmt.Errorf("unknown escape sequence: \\%c", esc)
			}
			p.pos++
			continue
		}
		if ch == '"' {
			p.pos++ // skip closing '"'
			return &Node{Kind: NodeString, Str: buf.String()}, nil
		}
		buf.WriteRune(ch)
		p.pos++
	}
	return nil, fmt.Errorf("unclosed string")
}

func (p *parser) parseAtom() (*Node, error) {
	start := p.pos
	for p.pos < len(p.input) && !isDelimiter(p.input[p.pos]) {
		p.pos++
	}
	token := string(p.input[start:p.pos])
	if token == "" {
		return nil, fmt.Errorf("unexpected character: %c", p.input[start])
	}

	if token == "true" {
		return &Node{Kind: NodeBool, Bool: true}, nil
	}
	if token == "false" {
		return &Node{Kind: NodeBool, Bool: false}, nil
	}
	if token == "nil" {
		return &Node{Kind: NodeNil}, nil
	}

	if len(token) > 1 && token[0] == ':' {
		return &Node{Kind: NodeKeyword, Str: token[1:]}, nil
	}

	if i, err := strconv.ParseInt(token, 10, 64); err == nil {
		return &Node{Kind: NodeInt, Int: i}, nil
	}

	if f, err := strconv.ParseFloat(token, 64); err == nil {
		return &Node{Kind: NodeFloat, Float: f}, nil
	}

	return &Node{Kind: NodeSymbol, Str: token}, nil
}

func (p *parser) skipWhitespace() {
	for p.pos < len(p.input) {
		ch := p.input[p.pos]
		if ch == ';' {
			for p.pos < len(p.input) && p.input[p.pos] != '\n' {
				p.pos++
			}
			continue
		}
		if !unicode.IsSpace(ch) {
			break
		}
		p.pos++
	}
}

func isDelimiter(ch rune) bool {
	return unicode.IsSpace(ch) || ch == '(' || ch == ')' || ch == '"' || ch == ';' || ch == '\''
}
