package logos

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type ValueKind int

const (
	ValInt    ValueKind = iota
	ValFloat
	ValBool
	ValString
	ValFn
	ValList
	ValMap
	ValNil
	ValKeyword
	ValSymbol
	ValNodeRef
	ValForm
)

type FnValue struct {
	Params    []string
	RestParam string
	Body      *Node
	NodeID    string
	Closure   map[string]Value
}

type Value struct {
	Kind  ValueKind
	Int   int64
	Float float64
	Bool  bool
	Str   string
	Fn    *FnValue
	List  *[]Value
	Map   *map[string]Value
}

func IntVal(n int64) Value     { return Value{Kind: ValInt, Int: n} }
func FloatVal(f float64) Value { return Value{Kind: ValFloat, Float: f} }
func BoolVal(b bool) Value     { return Value{Kind: ValBool, Bool: b} }
func StringVal(s string) Value { return Value{Kind: ValString, Str: s} }
func FnVal(fn *FnValue) Value  { return Value{Kind: ValFn, Fn: fn} }
func ListVal(elems []Value) Value {
	if elems == nil {
		elems = []Value{}
	}
	return Value{Kind: ValList, List: &elems}
}
func MapVal(m map[string]Value) Value {
	return Value{Kind: ValMap, Map: &m}
}
func NilVal() Value        { return Value{Kind: ValNil} }
func KeywordVal(s string) Value { return Value{Kind: ValKeyword, Str: s} }
func SymbolVal(s string) Value  { return Value{Kind: ValSymbol, Str: s} }
func NodeRefVal(s string) Value { return Value{Kind: ValNodeRef, Str: s} }
func FormVal(fn *FnValue) Value { return Value{Kind: ValForm, Fn: fn} }

// Truthy implements nil-as-flow: nil and false are falsy, everything else is truthy.
func (v Value) Truthy() bool {
	switch v.Kind {
	case ValNil:
		return false
	case ValBool:
		return v.Bool
	default:
		return true
	}
}

func (v Value) String() string {
	switch v.Kind {
	case ValInt:
		return strconv.FormatInt(v.Int, 10)
	case ValFloat:
		return strconv.FormatFloat(v.Float, 'g', -1, 64)
	case ValBool:
		if v.Bool {
			return "true"
		}
		return "false"
	case ValString:
		return v.Str
	case ValKeyword:
		return ":" + v.Str
	case ValSymbol:
		return v.Str
	case ValNodeRef:
		return v.Str
	case ValFn:
		ps := strings.Join(v.Fn.Params, ", ")
		if v.Fn.RestParam != "" {
			if len(v.Fn.Params) > 0 {
				ps += ", & " + v.Fn.RestParam
			} else {
				ps = "& " + v.Fn.RestParam
			}
		}
		return fmt.Sprintf("<fn(%s)>", ps)
	case ValForm:
		ps := strings.Join(v.Fn.Params, ", ")
		if v.Fn.RestParam != "" {
			if len(v.Fn.Params) > 0 {
				ps += ", & " + v.Fn.RestParam
			} else {
				ps = "& " + v.Fn.RestParam
			}
		}
		return fmt.Sprintf("<form(%s)>", ps)
	case ValList:
		elems := *v.List
		parts := make([]string, len(elems))
		for i, e := range elems {
			switch e.Kind {
			case ValString:
				parts[i] = fmt.Sprintf("%q", e.Str)
			default:
				parts[i] = e.String()
			}
		}
		return "(list " + strings.Join(parts, " ") + ")"
	case ValMap:
		m := *v.Map
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys)*2)
		for _, k := range keys {
			parts = append(parts, fmt.Sprintf("%q", k))
			val := m[k]
			if val.Kind == ValString {
				parts = append(parts, fmt.Sprintf("%q", val.Str))
			} else {
				parts = append(parts, val.String())
			}
		}
		return "(dict " + strings.Join(parts, " ") + ")"
	case ValNil:
		return "nil"
	default:
		return fmt.Sprintf("<unknown:%d>", v.Kind)
	}
}

func (v Value) KindName() string {
	switch v.Kind {
	case ValInt:
		return "Int"
	case ValFloat:
		return "Float"
	case ValBool:
		return "Bool"
	case ValString:
		return "String"
	case ValFn:
		return "Fn"
	case ValList:
		return "List"
	case ValMap:
		return "Map"
	case ValNil:
		return "Nil"
	case ValKeyword:
		return "Keyword"
	case ValSymbol:
		return "Symbol"
	case ValNodeRef:
		return "NodeRef"
	case ValForm:
		return "Form"
	default:
		return "Unknown"
	}
}

// ValuesEqual compares two Values for deep equality.
func ValuesEqual(a, b Value) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case ValInt:
		return a.Int == b.Int
	case ValFloat:
		return a.Float == b.Float
	case ValBool:
		return a.Bool == b.Bool
	case ValString:
		return a.Str == b.Str
	case ValList:
		as, bs := *a.List, *b.List
		if len(as) != len(bs) {
			return false
		}
		for i := range as {
			if !ValuesEqual(as[i], bs[i]) {
				return false
			}
		}
		return true
	case ValMap:
		am, bm := *a.Map, *b.Map
		if len(am) != len(bm) {
			return false
		}
		for k, av := range am {
			bv, ok := bm[k]
			if !ok || !ValuesEqual(av, bv) {
				return false
			}
		}
		return true
	case ValNil:
		return true
	case ValKeyword:
		return a.Str == b.Str
	case ValSymbol:
		return a.Str == b.Str
	case ValNodeRef:
		return a.Str == b.Str
	}
	return false
}

// ValueToGo converts a logos Value to a native Go value for JSON serialization.
func ValueToGo(v Value) (any, error) {
	switch v.Kind {
	case ValInt:
		return v.Int, nil
	case ValFloat:
		return v.Float, nil
	case ValBool:
		return v.Bool, nil
	case ValString:
		return v.Str, nil
	case ValKeyword:
		return ":" + v.Str, nil
	case ValNil:
		return nil, nil
	case ValList:
		elems := *v.List
		arr := make([]any, len(elems))
		for i, e := range elems {
			j, err := ValueToGo(e)
			if err != nil {
				return nil, err
			}
			arr[i] = j
		}
		return arr, nil
	case ValMap:
		m := *v.Map
		obj := make(map[string]any, len(m))
		for k, val := range m {
			j, err := ValueToGo(val)
			if err != nil {
				return nil, err
			}
			obj[k] = j
		}
		return obj, nil
	case ValSymbol:
		return "sym:" + v.Str, nil
	case ValNodeRef:
		return v.Str, nil
	case ValFn:
		return nil, fmt.Errorf("cannot serialize Fn to JSON")
	case ValForm:
		return nil, fmt.Errorf("cannot serialize Form to JSON")
	default:
		return nil, fmt.Errorf("unknown value kind")
	}
}

// GoToValue converts a native Go value (from JSON) to a logos Value.
func GoToValue(v any) Value {
	switch val := v.(type) {
	case nil:
		return NilVal()
	case bool:
		return BoolVal(val)
	case float64:
		if val == float64(int64(val)) && val >= -1<<53 && val <= 1<<53 {
			return IntVal(int64(val))
		}
		return FloatVal(val)
	case string:
		if strings.HasPrefix(val, "node:") {
			return NodeRefVal(val)
		}
		if strings.HasPrefix(val, "sym:") {
			return SymbolVal(val[4:])
		}
		if len(val) > 1 && val[0] == ':' {
			return KeywordVal(val[1:])
		}
		return StringVal(val)
	case []any:
		elems := make([]Value, len(val))
		for i, e := range val {
			elems[i] = GoToValue(e)
		}
		return ListVal(elems)
	case map[string]any:
		m := make(map[string]Value, len(val))
		for k, e := range val {
			m[k] = GoToValue(e)
		}
		return MapVal(m)
	default:
		return NilVal()
	}
}
