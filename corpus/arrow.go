package corpus

// What a result looks like on the way out through Arrow.
//
// A client that reads rows one at a time and a client that exports a
// million of them to a dataframe are the same client, and only one of
// those paths is covered by a case that asserts values. The other one
// has its own contract: a column of dates is a Date32 and not a string
// of digits, a year-month duration is a month-day-nano interval because
// that is the interval every reader implements, a node is a struct of
// the name of its table and the row it is, and a time with an offset is
// refused rather than quietly moved to UTC. None of that shows up in a
// row a case compares.
//
// So a case may say what the export gives as well as what the rows are,
// and the runner checks both against one statement. What it checks is
// the schema, field by field and into the nested types, and how many
// rows came back through the stream. The schema is spelled in the C
// Data Interface's own format strings, "l" for an int64 and "+s" for a
// struct, because that is the one spelling every language sees the same.
//
// The schema is read off the interface itself rather than out of
// arrow-go, for three reasons. The first is that a checkout running the
// corpus should pull nothing, and arrow-go is a tree. The second is that
// the C Data Interface is what the C runner has at this point too, so
// the two read the same bytes and report them in the same words, which
// is the whole reason a format string is what the case writes down. The
// third decided it: arrow-go's own ArrowSchema is a cgo type whose
// fields are C struct members and are therefore unexported outside the
// package that declares it, so the format string cannot be read off one
// at all. Writing a second mapping from arrow-go's DataType back to a
// format string would be a third opinion about the spelling, and a third
// opinion is what this file exists to avoid.
//
// Values are not read back here. A consumer that decoded every array by
// hand in each of nine languages would be nine new decoders under test,
// which is more of our own code and not more of the contract; the rows
// the case already asserts are the same values by another road.

/*
#include <stdint.h>
#include <stdlib.h>

// The C Data Interface, declared here rather than included from
// anywhere: it is nine fields that have not changed since Arrow 0.17
// and every producer in the world writes exactly this, which is what
// makes it an ABI. Copying it is how every consumer of it starts.
struct CorpusArrowSchema {
	const char* format;
	const char* name;
	const char* metadata;
	int64_t flags;
	int64_t n_children;
	struct CorpusArrowSchema** children;
	struct CorpusArrowSchema* dictionary;
	void (*release)(struct CorpusArrowSchema*);
	void* private_data;
};

// Only the length is read off an array: the values a case cares about
// it already asserts as rows.
struct CorpusArrowArray {
	int64_t length;
	int64_t null_count;
	int64_t offset;
	int64_t n_buffers;
	int64_t n_children;
	const void** buffers;
	struct CorpusArrowArray** children;
	struct CorpusArrowArray* dictionary;
	void (*release)(struct CorpusArrowArray*);
	void* private_data;
};

struct CorpusArrowStream {
	int (*get_schema)(struct CorpusArrowStream*, struct CorpusArrowSchema*);
	int (*get_next)(struct CorpusArrowStream*, struct CorpusArrowArray*);
	const char* (*get_last_error)(struct CorpusArrowStream*);
	void (*release)(struct CorpusArrowStream*);
	void* private_data;
};

// Go cannot call a C function pointer, so each of the four is called
// through a shim. They are static and they inline, which is the same
// thing the ctypes prototypes in the Python runner do at a cost.
static int corpus_get_schema(struct CorpusArrowStream* s, struct CorpusArrowSchema* out) {
	return s->get_schema(s, out);
}

static int corpus_get_next(struct CorpusArrowStream* s, struct CorpusArrowArray* out) {
	return s->get_next(s, out);
}

static const char* corpus_last_error(struct CorpusArrowStream* s) {
	return s->get_last_error ? s->get_last_error(s) : 0;
}

static void corpus_release_stream(struct CorpusArrowStream* s) {
	if (s->release) { s->release(s); }
}

static void corpus_release_schema(struct CorpusArrowSchema* s) {
	if (s->release) { s->release(s); }
}

static void corpus_release_array(struct CorpusArrowArray* a) {
	if (a->release) { a->release(a); }
}

// A released array is how the interface says the stream is done, so
// whether one is still held is a question the caller has to ask.
static int corpus_array_held(struct CorpusArrowArray* a) { return a->release != 0; }

static struct CorpusArrowSchema* corpus_child(struct CorpusArrowSchema* s, int64_t i) {
	return s->children[i];
}
*/
import "C"

import (
	"strconv"
	"unsafe"

	zu "github.com/tamnd/zu-go"
)

// A Field is one field of the schema an export gives, and the fields
// under it when it is a struct or a list.
//
// A list has exactly one field under it, which Arrow names "item", and a
// case writes that out rather than leaving it implied: a client that
// named it "element" would export something no reader lines up with what
// another client wrote.
type Field struct {
	// Name is the field's name, which for a column is the column's name
	// and for the field under a list is "item".
	Name string
	// Format is the C Data Interface format string, "l" for an int64,
	// "u" for a string, "tsn:" for a timestamp in nanoseconds with no
	// zone.
	Format string
	// Children is the fields under this one, empty for everything that
	// is not a struct or a list.
	Children []Field
}

// An Export is what a case says about the way out through Arrow: the
// columns it gives, or that Arrow has no type for one of them.
//
// A refusal is a thing a statement can produce today. Arrow has a time
// and a timestamp and nothing in between, so a time with an offset has
// nowhere to go, and dropping the offset would move the value.
type Export struct {
	// Refused is whether the case says the export says no, in which
	// case there are no fields to compare.
	Refused bool
	// Fields is the schema's fields, one per column, in order.
	Fields []Field
}

// TheResult is how a report names the whole result, which is the place
// the columns of an export are in.
const TheResult = "the result"

// ArrowError is the stream saying no, with what it said. It is a type
// of its own so that a refusal on the way out is told apart from a
// schema that does not match, which is what a case writing `refused`
// turns on.
type ArrowError struct {
	// Message is what the stream said, which is the whole of it.
	Message string
}

// Error is the message, without a prefix, for the reason
// [CorpusError.Error] has none.
func (e *ArrowError) Error() string { return e.Message }

// ParseExport reads the `arrow:` of a case.
func ParseExport(node Node) (*Export, error) {
	if text, ok := node.Str(); ok {
		if text == "refused" {
			return &Export{Refused: true}, nil
		}
		return nil, refuse("line %d: `arrow:` is the columns the export gives, or `refused` for a "+
			"result Arrow has no type for, and this is %s", node.Line(), Quote(text))
	}
	fields, err := exportFields(node)
	if err != nil {
		return nil, err
	}
	return &Export{Fields: fields}, nil
}

func exportFields(node Node) ([]Field, error) {
	items, ok := node.Seq()
	if !ok {
		return nil, refuse("line %d: `arrow:` is a sequence of fields, and this is %s",
			node.Line(), node.What())
	}
	out := make([]Field, 0, len(items))
	for _, item := range items {
		one, err := exportField(item)
		if err != nil {
			return nil, err
		}
		out = append(out, one)
	}
	return out, nil
}

func exportField(node Node) (Field, error) {
	at := node.Line()
	if _, ok := node.Map(); !ok {
		return Field{}, refuse("line %d: an Arrow field is a mapping of `name` and `format`, and "+
			"this is %s", at, node.What())
	}
	if unknown := node.Unknown("name", "format", "children"); len(unknown) > 0 {
		return Field{}, refuse("line %d: an Arrow field has no key %s", at, Quote(unknown[0]))
	}
	text := func(key string) (string, error) {
		value := node.Get(key)
		if value == nil {
			return "", refuse("line %d: an Arrow field has a `%s:`", at, key)
		}
		spelled, ok := value.Str()
		if !ok {
			return "", refuse("line %d: an Arrow field has a `%s:`", at, key)
		}
		return spelled, nil
	}
	name, err := text("name")
	if err != nil {
		return Field{}, err
	}
	format, err := text("format")
	if err != nil {
		return Field{}, err
	}
	if format == "" {
		return Field{}, refuse("line %d: an empty format string is not a type Arrow has", at)
	}
	var children []Field
	if under := node.Get("children"); under != nil {
		children, err = exportFields(*under)
		if err != nil {
			return Field{}, err
		}
	}
	// A nested format is the one thing about a format string this reader
	// knows, and it is worth knowing here: a case that wrote the fields
	// of a struct under a "u" would be asserting something the export
	// cannot produce, and finding that out at load time says so with a
	// line number rather than as a failure in a report.
	nested := format[0] == '+'
	if nested && len(children) == 0 {
		return Field{}, refuse("line %d: %s is a nested type and the fields under it are part of it",
			at, Quote(format))
	}
	if !nested && len(children) > 0 {
		return Field{}, refuse("line %d: %s holds no fields, so nothing goes under it",
			at, Quote(format))
	}
	return Field{Name: name, Format: format, Children: children}, nil
}

// Exported is the columns a result gives through Arrow and how many rows
// came out of the stream.
//
// The stream is taken once and both answers come out of that one taking,
// because a stream is consumed by reading it and a second export would
// be a second statement in all but name.
//
// A column Arrow cannot hold is found when the stream is asked for,
// before a row moves, and it comes back as an [ArrowError] so that the
// runner can tell it from a schema that came out wrong. The rows are
// spent either way, which is what the client's own ArrowStream promises.
func Exported(rows *zu.Rows) ([]Field, int64, error) {
	// Zeroed by Go, which is what the interface asks of a caller. It
	// stays where it is until this function returns, and the engine's
	// stream keeps its own state behind private_data rather than a
	// pointer back to this struct, which is what makes a struct on the
	// Go side the right place for it. The client's own Arrow module
	// does the same thing with arrow-go's copy of this declaration.
	var stream C.struct_CorpusArrowStream
	if err := rows.ArrowStream(unsafe.Pointer(&stream), 0); err != nil {
		return nil, 0, &ArrowError{Message: errorText(err)}
	}
	defer C.corpus_release_stream(&stream)

	said := func(code C.int) string {
		text := C.corpus_last_error(&stream)
		if text != nil {
			if message := C.GoString(text); message != "" {
				return message
			}
		}
		return "errno " + strconv.Itoa(int(code))
	}

	var out C.struct_CorpusArrowSchema
	if code := C.corpus_get_schema(&stream, &out); code != 0 {
		return nil, 0, &ArrowError{Message: said(code)}
	}
	// The stream's schema is a struct of the columns, so what the case
	// is compared against is the fields under it.
	top := walked(&out)
	C.corpus_release_schema(&out)

	var count int64
	for {
		var batch C.struct_CorpusArrowArray
		if code := C.corpus_get_next(&stream, &batch); code != 0 {
			return nil, 0, &ArrowError{Message: said(code)}
		}
		if C.corpus_array_held(&batch) == 0 {
			break
		}
		count += int64(batch.length)
		C.corpus_release_array(&batch)
	}
	return top.Children, count, nil
}

// walked is one field of an exported schema, and everything under it.
func walked(one *C.struct_CorpusArrowSchema) Field {
	out := Field{}
	if one.name != nil {
		out.Name = C.GoString(one.name)
	}
	if one.format != nil {
		out.Format = C.GoString(one.format)
	}
	for i := C.int64_t(0); i < one.n_children; i++ {
		out.Children = append(out.Children, walked(C.corpus_child(one, i)))
	}
	return out
}

// SchemaSays is what the export gave that the case did not want, or the
// empty string when the two agree.
//
// The comparison walks the schema and the case's fields together and
// stops at the first difference, for the reason the row comparison does:
// the first is nearly always the cause of the rest.
func SchemaSays(got, want []Field) string {
	return fieldsUnder("", got, want)
}

// fieldsUnder is the fields under one place, where the place is the
// dotted path of the field they are under and the empty one is the
// result itself.
func fieldsUnder(prefix string, got, want []Field) string {
	place := TheResult
	if prefix != "" {
		place = Quote(prefix)
	}
	if len(got) != len(want) {
		return "arrow gives " + strconv.Itoa(len(got)) + " fields in " + place +
			" where the case wants " + strconv.Itoa(len(want))
	}
	for i := range got {
		if got[i].Name != want[i].Name {
			return "arrow field " + strconv.Itoa(i+1) + " in " + place + " is named " +
				Quote(got[i].Name) + " where the case wants " + Quote(want[i].Name)
		}
		// The path is the case's own names joined with dots, which is how
		// a field inside a path inside a column is pointed at without
		// printing the whole schema at somebody.
		path := want[i].Name
		if prefix != "" {
			path = prefix + "." + want[i].Name
		}
		if got[i].Format != want[i].Format {
			return "arrow field " + Quote(path) + " is " + Quote(got[i].Format) +
				" where the case wants " + Quote(want[i].Format)
		}
		if why := fieldsUnder(path, got[i].Children, want[i].Children); why != "" {
			return why
		}
	}
	return ""
}
