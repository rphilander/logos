package logos

import (
	"testing"
)

func TestParseInt(t *testing.T) {
	n, err := Parse("42")
	if err != nil {
		t.Fatal(err)
	}
	if n.Kind != NodeInt || n.Int != 42 {
		t.Fatalf("expected Int 42, got %v", n)
	}
}

func TestParseNegativeInt(t *testing.T) {
	n, err := Parse("-7")
	if err != nil {
		t.Fatal(err)
	}
	if n.Kind != NodeInt || n.Int != -7 {
		t.Fatalf("expected Int -7, got %v", n)
	}
}

func TestParseFloat(t *testing.T) {
	n, err := Parse("3.14")
	if err != nil {
		t.Fatal(err)
	}
	if n.Kind != NodeFloat || n.Float != 3.14 {
		t.Fatalf("expected Float 3.14, got %v", n)
	}
}

func TestParseBool(t *testing.T) {
	for _, tc := range []struct {
		input string
		val   bool
	}{
		{"true", true},
		{"false", false},
	} {
		n, err := Parse(tc.input)
		if err != nil {
			t.Fatal(err)
		}
		if n.Kind != NodeBool || n.Bool != tc.val {
			t.Fatalf("expected Bool %v, got %v", tc.val, n)
		}
	}
}

func TestParseNil(t *testing.T) {
	n, err := Parse("nil")
	if err != nil {
		t.Fatal(err)
	}
	if n.Kind != NodeNil {
		t.Fatalf("expected Nil, got %v", n)
	}
}

func TestParseString(t *testing.T) {
	n, err := Parse(`"hello world"`)
	if err != nil {
		t.Fatal(err)
	}
	if n.Kind != NodeString || n.Str != "hello world" {
		t.Fatalf("expected String 'hello world', got %v", n)
	}
}

func TestParseStringEscapes(t *testing.T) {
	n, err := Parse(`"line\none\ttab\\"`)
	if err != nil {
		t.Fatal(err)
	}
	if n.Str != "line\none\ttab\\" {
		t.Fatalf("expected escapes, got %q", n.Str)
	}
}

func TestParseSymbol(t *testing.T) {
	n, err := Parse("foo-bar?")
	if err != nil {
		t.Fatal(err)
	}
	if n.Kind != NodeSymbol || n.Str != "foo-bar?" {
		t.Fatalf("expected Symbol 'foo-bar?', got %v", n)
	}
}

func TestParseList(t *testing.T) {
	n, err := Parse("(add 1 2)")
	if err != nil {
		t.Fatal(err)
	}
	if n.Kind != NodeList || len(n.Children) != 3 {
		t.Fatalf("expected list with 3 children, got %v", n)
	}
	if n.Children[0].Kind != NodeSymbol || n.Children[0].Str != "add" {
		t.Fatalf("expected head 'add', got %v", n.Children[0])
	}
}

func TestParseNested(t *testing.T) {
	n, err := Parse("(if true (list 1) (list 2))")
	if err != nil {
		t.Fatal(err)
	}
	if n.Kind != NodeList || len(n.Children) != 4 {
		t.Fatalf("expected list with 4 children, got %v", n)
	}
}

func TestParseEmptyList(t *testing.T) {
	n, err := Parse("()")
	if err != nil {
		t.Fatal(err)
	}
	if n.Kind != NodeList || len(n.Children) != 0 {
		t.Fatalf("expected empty list, got %v", n)
	}
}

func TestParseComment(t *testing.T) {
	n, err := Parse("; this is a comment\n42")
	if err != nil {
		t.Fatal(err)
	}
	if n.Kind != NodeInt || n.Int != 42 {
		t.Fatalf("expected 42, got %v", n)
	}
}

func TestParseKeyword(t *testing.T) {
	n, err := Parse(":foo")
	if err != nil {
		t.Fatal(err)
	}
	if n.Kind != NodeKeyword || n.Str != "foo" {
		t.Fatalf("expected Keyword 'foo', got %v", n)
	}
}

func TestParseKeywordInList(t *testing.T) {
	n, err := Parse("(list :a :b)")
	if err != nil {
		t.Fatal(err)
	}
	if n.Kind != NodeList || len(n.Children) != 3 {
		t.Fatalf("expected list with 3 children, got %v", n)
	}
	if n.Children[1].Kind != NodeKeyword || n.Children[1].Str != "a" {
		t.Fatalf("expected keyword :a, got %v", n.Children[1])
	}
}

func TestParseErrors(t *testing.T) {
	cases := []string{
		"",          // empty
		"(unclosed", // unclosed list
		`"unclosed`, // unclosed string
		"1 2",       // trailing input
	}
	for _, input := range cases {
		_, err := Parse(input)
		if err == nil {
			t.Fatalf("expected error for %q", input)
		}
	}
}
