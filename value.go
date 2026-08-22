package zu

/*
#include <zu.h>
*/
import "C"

import (
	"strconv"
)

// A Type is what one cell holds. Every cell has one, and a null cell
// is a value too rather than the absence of one.
type Type int32

// The types, with the same numbers as the C ABI.
const (
	TypeNull         Type = C.ZU_TYPE_NULL
	TypeBool         Type = C.ZU_TYPE_BOOL
	TypeInt          Type = C.ZU_TYPE_INT
	TypeFloat        Type = C.ZU_TYPE_FLOAT
	TypeString       Type = C.ZU_TYPE_STR
	TypeNode         Type = C.ZU_TYPE_NODE
	TypeRel          Type = C.ZU_TYPE_REL
	TypeList         Type = C.ZU_TYPE_LIST
	TypePath         Type = C.ZU_TYPE_PATH
	TypeTemporal     Type = C.ZU_TYPE_TEMPORAL
	TypeRecord       Type = C.ZU_TYPE_RECORD
	TypeGraph        Type = C.ZU_TYPE_GRAPH
	TypeBindingTable Type = C.ZU_TYPE_BINDING_TABLE
)

// String is the type in the language's own word for it.
func (t Type) String() string {
	switch t {
	case TypeNull:
		return "null"
	case TypeBool:
		return "bool"
	case TypeInt:
		return "int"
	case TypeFloat:
		return "float"
	case TypeString:
		return "string"
	case TypeNode:
		return "node"
	case TypeRel:
		return "rel"
	case TypeList:
		return "list"
	case TypePath:
		return "path"
	case TypeTemporal:
		return "temporal"
	case TypeRecord:
		return "record"
	case TypeGraph:
		return "graph"
	case TypeBindingTable:
		return "binding table"
	default:
		return "type " + strconv.Itoa(int(t))
	}
}

// A Node is one row of a node table. Both parts are here because
// neither identifies a node on its own: two tables number their rows
// from zero, so the table and the offset together are the identity and
// either alone is a coincidence waiting to happen.
//
// The offset is the engine's own row number and not a property. A
// program that wants an application key reads the property it wrote.
type Node struct {
	// Table is which node table the row is in.
	Table uint32
	// Offset is the row's number within that table, counted from
	// zero.
	Offset uint64
}

// String is the node as a table and a row.
func (n Node) String() string {
	return "node(" + strconv.FormatUint(uint64(n.Table), 10) + ":" + strconv.FormatUint(n.Offset, 10) + ")"
}

// A Rel is one edge: the table it belongs to and the two node offsets
// it runs between.
type Rel struct {
	// Table is which relationship table the edge is in.
	Table uint32
	// Src is the offset of the node the edge runs from.
	Src uint64
	// Dst is the offset of the node the edge runs to.
	Dst uint64
}

// String is the edge as a table and its two ends.
func (r Rel) String() string {
	return "rel(" + strconv.FormatUint(uint64(r.Table), 10) + ":" +
		strconv.FormatUint(r.Src, 10) + "->" + strconv.FormatUint(r.Dst, 10) + ")"
}

// A Path is the walk a match found, as the nodes and the edges it went
// through in the order it went through them. It is a slice of values
// rather than two slices because that is the order the walk happened
// in, and losing it would be losing which edge joined which pair.
type Path []any

// Nodes is the nodes of the walk, in order.
func (p Path) Nodes() []Node {
	out := make([]Node, 0, len(p)/2+1)
	for _, v := range p {
		if n, ok := v.(Node); ok {
			out = append(out, n)
		}
	}
	return out
}

// Rels is the edges of the walk, in order.
func (p Path) Rels() []Rel {
	out := make([]Rel, 0, len(p)/2)
	for _, v := range p {
		if r, ok := v.(Rel); ok {
			out = append(out, r)
		}
	}
	return out
}

// A Record is a value with named fields. A name appears once, which is
// what makes two records written in different orders one value, so a
// map loses nothing by holding them.
type Record map[string]any

// A Graph is a reference to a graph. Neither this nor [BindingTable]
// reads through an accessor: a handle has no contents to hand over, so
// the type is the whole of what a client can say about the cell.
type Graph struct{}

// String names the value for a program printing a row.
func (Graph) String() string { return "graph" }

// A BindingTable is a reference to a binding table. See [Graph] for
// why it is empty.
type BindingTable struct{}

// String names the value for a program printing a row.
func (BindingTable) String() string { return "binding table" }

// value reads one cell into the Go value that means the same thing.
// The cell is borrowed from the result and everything built here is a
// copy, so what comes back outlives [Rows.Close].
//
//   - null is nil
//   - bool, int and float are bool, int64 and float64
//   - a string is a string
//   - a node is a [Node], an edge a [Rel], a walk a [Path]
//   - a list is a []any and a record a [Record]
//   - a temporal is one of the seven temporal types
func value(sc *scratch, v *C.zu_value) (any, error) {
	switch Type(C.zu_value_type(v)) {
	case TypeNull:
		return nil, nil
	case TypeBool:
		if err := fail(C.zu_value_bool(v, &sc.i32), nil); err != nil {
			return nil, err
		}
		return sc.i32 != 0, nil
	case TypeInt:
		if err := fail(C.zu_value_i64(v, &sc.i64), nil); err != nil {
			return nil, err
		}
		return int64(sc.i64), nil
	case TypeFloat:
		if err := fail(C.zu_value_f64(v, &sc.f64), nil); err != nil {
			return nil, err
		}
		return float64(sc.f64), nil
	case TypeString:
		return str(sc, v)
	case TypeNode:
		return node(sc, v)
	case TypeRel:
		return rel(sc, v)
	case TypeList:
		return parts(sc, v)
	case TypePath:
		items, err := parts(sc, v)
		if err != nil {
			return nil, err
		}
		return Path(items), nil
	case TypeTemporal:
		if err := fail(C.zu_value_temporal(v, &sc.kind, &sc.i64, &sc.off), nil); err != nil {
			return nil, err
		}
		return temporal(TemporalKind(sc.kind), int64(sc.i64), int32(sc.off))
	case TypeRecord:
		return record(sc, v)
	case TypeGraph:
		return Graph{}, nil
	case TypeBindingTable:
		return BindingTable{}, nil
	default:
		return nil, misuse("the engine gave back a value of a type this client does not know")
	}
}

// str reads a string cell. The engine lends its own bytes and this is
// the copy that makes them a Go string.
func str(sc *scratch, v *C.zu_value) (string, error) {
	if err := fail(C.zu_value_str(v, &sc.txt, &sc.size), nil); err != nil {
		return "", err
	}
	return text(sc.txt, sc.size), nil
}

// node reads a node cell.
func node(sc *scratch, v *C.zu_value) (Node, error) {
	if err := fail(C.zu_value_node(v, &sc.u32, &sc.src), nil); err != nil {
		return Node{}, err
	}
	return Node{Table: uint32(sc.u32), Offset: uint64(sc.src)}, nil
}

// rel reads an edge cell.
func rel(sc *scratch, v *C.zu_value) (Rel, error) {
	if err := fail(C.zu_value_rel(v, &sc.u32, &sc.src, &sc.dst), nil); err != nil {
		return Rel{}, err
	}
	return Rel{Table: uint32(sc.u32), Src: uint64(sc.src), Dst: uint64(sc.dst)}, nil
}

// parts reads the values inside a list or a walk, in order.
func parts(sc *scratch, v *C.zu_value) ([]any, error) {
	n := uint64(C.zu_value_len(v))
	out := make([]any, 0, n)
	for i := uint64(0); i < n; i++ {
		if err := fail(C.zu_value_at(v, C.uint64_t(i), &sc.val), nil); err != nil {
			return nil, err
		}
		got, err := value(sc, sc.val)
		if err != nil {
			return nil, err
		}
		out = append(out, got)
	}
	return out, nil
}

// record reads a value with named fields. The name is copied out
// before the field is read, because reading the field is what writes
// over the borrowed bytes the name is still pointing at.
func record(sc *scratch, v *C.zu_value) (Record, error) {
	n := uint64(C.zu_value_len(v))
	out := make(Record, n)
	for i := uint64(0); i < n; i++ {
		if err := fail(C.zu_value_field(v, C.uint64_t(i), &sc.txt, &sc.size), nil); err != nil {
			return nil, err
		}
		name := text(sc.txt, sc.size)
		if err := fail(C.zu_value_at(v, C.uint64_t(i), &sc.val), nil); err != nil {
			return nil, err
		}
		got, err := value(sc, sc.val)
		if err != nil {
			return nil, err
		}
		out[name] = got
	}
	return out, nil
}
