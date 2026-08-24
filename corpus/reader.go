// Package corpus reads and runs the shared conformance corpus against
// this client.
//
// The corpus is a directory of YAML files, versioned with the engine
// and shipped to every client in the family. A case is a statement and
// what running it must produce, which is deliberately the whole of it:
// every client in every language can run a statement and look at the
// rows that come back, so a corpus written in those terms is one every
// client can run, and a corpus written in terms of a client's own API
// would be nine corpora.
//
// What this prints is what the reference runner in Rust prints, line
// for line, so that a disagreement between two clients is a diff and
// not a reading exercise.
package corpus

import (
	"fmt"
	"strings"
)

// The subset of YAML the corpus is written in.
//
// YAML is a large language and the corpus needs a small corner of it:
// block mappings, block sequences, and scalars. Everything else is
// refused with a line number. The files are hand written and are read
// by people in nine repositories who did not write them, so a construct
// a reader quietly reinterpreted would be a case that says one thing to
// a reviewer and another to the runner.
//
// So: two space indentation and no tabs, "- " with exactly one space,
// plain, single quoted and double quoted scalars on one line, and
// comments. No flow collections, no block scalars, no anchors, no
// aliases, no tags, no document markers, no multi document streams.
//
// This is the fourth implementation of that subset, after
// crates/zu-corpus/src/yaml.rs in the engine, conformance/c/yaml.c
// beside it and conformance/reader.py in zu-python. There is a YAML
// package for Go that would read these files, and would read a good
// deal more besides: it would take a flow sequence, a block scalar and
// an anchor, none of which a case may use. What the corpus needs is a
// reader that refuses, and the cheapest way to have one is to write it.
//
// Whether a scalar was quoted survives parsing, because the value
// encoding turns on it. An INT64 written bare is a number some reader
// in some language will round, and refusing it is the whole point of
// the encoding.

// A CorpusError is a file the corpus will not read, with the line it
// gave up on. It is a type of its own rather than a plain error so that
// the command can tell a corpus it cannot read from a case that did not
// pass, which are two different exits.
type CorpusError struct {
	// Message is the whole of the refusal, which opens with the line it
	// happened on unless the file has no line to blame.
	Message string
}

// Error is the message, without a prefix, because a runner prints it
// beside a case name that already says where it came from.
func (e *CorpusError) Error() string { return e.Message }

// refuse is every failure in this file and the two beside it. The
// message is the whole of it: a reader that also carried a stack would
// be printing the shape of this package at somebody trying to fix a
// case.
func refuse(format string, a ...any) error {
	return &CorpusError{Message: fmt.Sprintf(format, a...)}
}

// Quote is a string the way Rust's {:?} writes one.
//
// Every refusal in the corpus is written in four languages and diffed
// across them, so a value quoted one way here and another way there
// would be a difference in the report that is not a difference in the
// answer. Go's %q escapes every rune it thinks is unprintable and Rust
// does not, so the quoting is written out rather than borrowed.
func Quote(text string) string {
	var out strings.Builder
	out.WriteByte('"')
	for _, c := range text {
		switch c {
		case '"', '\\':
			out.WriteByte('\\')
			out.WriteRune(c)
		case '\n':
			out.WriteString(`\n`)
		case '\r':
			out.WriteString(`\r`)
		case '\t':
			out.WriteString(`\t`)
		default:
			out.WriteRune(c)
		}
	}
	out.WriteByte('"')
	return out.String()
}

// A Kind is which of the four shapes a node is.
type Kind int

// The four kinds. Empty is a key with nothing under it: it is a node
// rather than an error because a case that expects no rows back writes
// "rows:" and stops, and that is a real expectation which needs a
// spelling. Every accessor says no to it, so a "name:" left blank is
// still caught by whoever wanted a name.
const (
	KindScalar Kind = iota
	KindSeq
	KindMap
	KindEmpty
)

// A Pair is one entry of a mapping, kept in the order it was written
// rather than in a map, because the order is what a report cites and
// what a parameter list means.
type Pair struct {
	// Key is the name left of the colon.
	Key string
	// Value is what is right of it, or what is indented under it.
	Value Node
}

// A Node is one node of a document, with the line it started on.
type Node struct {
	kind   Kind
	line   int
	text   string
	quoted bool
	items  []Node
	pairs  []Pair
}

// Kind is which of the four shapes this node is.
func (n Node) Kind() Kind { return n.kind }

// Line is the line the node started on, which is what a refusal cites.
func (n Node) Line() int { return n.line }

// What is what kind of node this is, for an error that has to say what
// it found instead of what it wanted.
func (n Node) What() string {
	switch n.kind {
	case KindScalar:
		return "a scalar"
	case KindSeq:
		return "a sequence"
	case KindMap:
		return "a mapping"
	default:
		return "nothing"
	}
}

// Scalar is the text of a scalar and whether it was written in quotes.
// The second result is false for anything that is not a scalar.
func (n Node) Scalar() (text string, quoted bool, ok bool) {
	if n.kind != KindScalar {
		return "", false, false
	}
	return n.text, n.quoted, true
}

// Str is the text of a scalar, for a caller the quoting does not
// concern.
func (n Node) Str() (string, bool) {
	if n.kind != KindScalar {
		return "", false
	}
	return n.text, true
}

// Seq is the items of a sequence.
func (n Node) Seq() ([]Node, bool) {
	if n.kind != KindSeq {
		return nil, false
	}
	return n.items, true
}

// SeqOrEmpty is a sequence, counting a key with nothing under it as the
// empty one. Only a caller for whom empty is a meaningful answer should
// reach for this; the rest want [Node.Seq], so that a list somebody
// left unfinished is refused rather than read as none.
func (n Node) SeqOrEmpty() ([]Node, bool) {
	if n.kind == KindEmpty {
		return nil, true
	}
	return n.Seq()
}

// Map is the entries of a mapping, in the order they were written.
func (n Node) Map() ([]Pair, bool) {
	if n.kind != KindMap {
		return nil, false
	}
	return n.pairs, true
}

// Get is the value under one key, or nil when this is not a mapping or
// the key is not in it.
func (n Node) Get(key string) *Node {
	if n.kind != KindMap {
		return nil
	}
	for i := range n.pairs {
		if n.pairs[i].Key == key {
			return &n.pairs[i].Value
		}
	}
	return nil
}

// Unknown is the keys that are not in known, so a caller can refuse a
// typo rather than drop the field on the floor.
func (n Node) Unknown(known ...string) []string {
	if n.kind != KindMap {
		return nil
	}
	var out []string
	for _, p := range n.pairs {
		found := false
		for _, k := range known {
			found = found || k == p.Key
		}
		if !found {
			out = append(out, p.Key)
		}
	}
	return out
}

// A line is one meaningful line: its indent, whether a "- " opened it,
// what is left after that, and where it was.
type line struct {
	indent int
	dash   bool
	text   string
	no     int
}

// Parse is a document, or the first thing in it this reader will not
// read.
func Parse(text string) (Node, error) {
	lines, err := lex(text)
	if err != nil {
		return Node{}, err
	}
	if len(lines) == 0 {
		return Node{}, refuse("the file has nothing in it")
	}
	if lines[0].indent != 0 {
		return Node{}, refuse("line %d: the first line is indented", lines[0].no)
	}
	at := &cursor{lines: lines}
	node, err := parseNode(at, 0)
	if err != nil {
		return Node{}, err
	}
	if at.i < len(lines) {
		return Node{}, refuse("line %d: this belongs to nothing above it", lines[at.i].no)
	}
	return node, nil
}

// A cursor is where the parser is, which the recursive calls share.
type cursor struct {
	lines []line
	i     int
}

// at is the line the cursor is on plus an offset, or nil past the end.
func (c *cursor) at(offset int) *line {
	j := c.i + offset
	if j >= len(c.lines) {
		return nil
	}
	return &c.lines[j]
}

// lex is lines, with blanks and comments dropped and every "- " split
// into the item it opens and the content that followed it on the same
// line. Splitting here rather than in the parser is what lets
// "- name: x" and a "name: x" on its own line be the same shape by the
// time anything looks at them.
func lex(text string) ([]line, error) {
	var out []line
	for n, raw := range strings.Split(text, "\n") {
		no := n + 1
		if tab := strings.IndexByte(raw, '\t'); tab >= 0 {
			return nil, refuse("line %d: a tab at column %d, and indentation here is spaces", no, tab+1)
		}
		content := strings.TrimRight(stripComment(raw), " \r\v\f")
		rest := strings.TrimLeft(content, " ")
		indent := len(content) - len(rest)
		if rest == "" {
			continue
		}
		if rest == "---" || rest == "..." {
			return nil, refuse("line %d: %s opens or closes a document, and a file here holds one",
				no, Quote(rest))
		}
		if indent%2 != 0 {
			return nil, refuse("line %d: indented %d, and indentation here goes two spaces at a time",
				no, indent)
		}

		if rest != "-" && !strings.HasPrefix(rest, "- ") {
			out = append(out, line{indent: indent, text: rest, no: no})
			continue
		}
		rest = rest[1:]
		if strings.HasPrefix(rest, "  ") {
			return nil, refuse("line %d: a `- ` takes exactly one space, so that what follows it "+
				"lines up with the lines under it", no)
		}
		rest = strings.TrimLeft(rest, " ")
		if strings.HasPrefix(rest, "- ") {
			return nil, refuse("line %d: a sequence opening straight into another one, which "+
				"nothing here needs", no)
		}
		out = append(out, line{indent: indent, dash: true, no: no})
		if rest != "" {
			out = append(out, line{indent: indent + 2, text: rest, no: no})
		}
	}
	return out, nil
}

// stripComment drops everything from an unquoted " #" on.
//
// Three rules keep this from eating content. A # starts a comment only
// with whitespace before it, because one inside a word is part of the
// word. A quote opens a quoted run only with whitespace before it,
// because a quote inside a word is part of the word too, which is what
// lets a "doc:" say "it's" without opening a run that never closes. And
// a quote that opens nothing that closes was not a run at all, which is
// what lets a "query:" hold cast('  42  ' AS INT64).
func stripComment(text string) string {
	for i := 0; i < len(text); i++ {
		c := text[i]
		opens := i == 0 || space(text[i-1])
		if c == '#' && opens {
			return text[:i]
		}
		if (c == '"' || c == '\'') && opens {
			if end, found := closingQuote(text[i+1:], c); found {
				i += 1 + end
			}
		}
	}
	return text
}

// space is whether a byte is one of the ones that can stand before a
// comment or a quote. Bytes rather than runes because every byte of a
// multi byte rune is above 0x7f and so is none of these.
func space(c byte) bool {
	return c == ' ' || c == '\t' || c == '\r' || c == '\n' || c == '\v' || c == '\f'
}

// closingQuote is the offset of the quote that closes a run whose
// opening quote has already been passed, and whether the line ends
// first.
//
// The two styles hide a quote differently: a double quoted run escapes
// with a backslash, and a single quoted run doubles the quote, which is
// the only escape it has.
func closingQuote(rest string, mark byte) (int, bool) {
	for i := 0; i < len(rest); i++ {
		if rest[i] == '\\' && mark == '"' {
			i++
			continue
		}
		if rest[i] == mark {
			if mark == '\'' && i+1 < len(rest) && rest[i+1] == '\'' {
				i++
				continue
			}
			return i, true
		}
	}
	return 0, false
}

// parseNode is the node that starts where the cursor is and is indented
// indent, leaving the cursor on the first line that is not part of it.
func parseNode(at *cursor, indent int) (Node, error) {
	here := at.at(0)
	if here.dash {
		return parseSeq(at, indent)
	}
	// A mapping key is a bare word and a ":". Anything else at this
	// position is a scalar standing on its own, which is what the items
	// of a sequence of scalars are.
	if _, _, ok := splitKey(here.text); ok {
		return parseMap(at, indent)
	}
	at.i++
	return parseScalar(here.text, here.no)
}

func parseSeq(at *cursor, indent int) (Node, error) {
	start := at.at(0).no
	var items []Node
	for {
		here := at.at(0)
		if here == nil || !here.dash || here.indent != indent {
			break
		}
		opened := here.no
		at.i++
		next := at.at(0)
		switch {
		case next != nil && next.indent == indent+2:
			item, err := parseNode(at, indent+2)
			if err != nil {
				return Node{}, err
			}
			items = append(items, item)
		case next != nil && next.indent > indent:
			return Node{}, refuse("line %d: indented %d, where an item of the sequence on line %d "+
				"is indented %d", next.no, next.indent, opened, indent+2)
		default:
			return Node{}, refuse("line %d: a `-` with nothing after it", opened)
		}
	}
	return Node{kind: KindSeq, line: start, items: items}, nil
}

func parseMap(at *cursor, indent int) (Node, error) {
	start := at.at(0).no
	var pairs []Pair
	for {
		here := at.at(0)
		if here == nil || here.dash || here.indent != indent {
			break
		}
		key, rest, ok := splitKey(here.text)
		if !ok {
			break
		}
		opened := here.no
		at.i++

		var value Node
		var err error
		switch next := at.at(0); {
		case rest != "":
			value, err = parseScalar(rest, opened)
		case next != nil && next.indent == indent+2:
			value, err = parseNode(at, indent+2)
		case next != nil && next.indent > indent:
			err = refuse("line %d: indented %d, where what is under `%s:` on line %d is indented %d",
				next.no, next.indent, key, opened, indent+2)
		default:
			value = Node{kind: KindEmpty, line: opened}
		}
		if err != nil {
			return Node{}, err
		}
		for _, p := range pairs {
			if p.Key == key {
				return Node{}, refuse("line %d: %s is set twice in one mapping", opened, key)
			}
		}
		pairs = append(pairs, Pair{Key: key, Value: value})
	}
	return Node{kind: KindMap, line: start, pairs: pairs}, nil
}

// splitKey is the key and the rest of the line, when the line opens a
// mapping entry. A key is a bare word, and the ":" after it ends the
// line or has a space after it, so that a plain scalar holding a colon
// is still a scalar.
func splitKey(text string) (key, rest string, ok bool) {
	if before, after, found := strings.Cut(text, ": "); found {
		key, rest = before, strings.TrimLeft(after, " ")
	} else {
		if !strings.HasSuffix(text, ":") {
			return "", "", false
		}
		key, rest = text[:len(text)-1], ""
	}
	if key == "" {
		return "", "", false
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		bare := c == '_' || c == '-' ||
			('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9')
		if !bare {
			return "", "", false
		}
	}
	return key, rest, true
}

func parseScalar(text string, at int) (Node, error) {
	for _, mark := range []byte{'"', '\''} {
		if len(text) == 0 || text[0] != mark {
			continue
		}
		body := text[1:]
		// The closing quote is found by scanning rather than by taking
		// the last one on the line, so that `"a" and "b"` is refused
		// instead of read as one scalar with quotes in the middle.
		end, found := closingQuote(body, mark)
		if !found {
			return Node{}, refuse("line %d: a %c that opens and does not close on its line", at, mark)
		}
		if end+1 != len(body) {
			return Node{}, refuse("line %d: %s after the scalar ends", at, Quote(body[end+1:]))
		}
		inner := body[:end]
		if mark == '\'' {
			// A single quoted run has one escape, the doubled quote,
			// and a backslash in it is a backslash.
			return Node{kind: KindScalar, line: at, text: strings.ReplaceAll(inner, "''", "'"), quoted: true}, nil
		}
		value, err := unescape(inner, at)
		if err != nil {
			return Node{}, err
		}
		return Node{kind: KindScalar, line: at, text: value, quoted: true}, nil
	}
	if len(text) > 0 && strings.IndexByte("[]{}&*!|>%@`", text[0]) >= 0 {
		return Node{}, refuse("line %d: a plain scalar opening with '%c', which is a construct this "+
			"reader does not read", at, text[0])
	}
	return Node{kind: KindScalar, line: at, text: text}, nil
}

// escapes are the ones the corpus uses, which is a subset of YAML's.
// The ones that name a code point by its digits are not here, because
// the corpus writes those as the character itself and a case that wants
// the digits is testing the engine's own escapes inside a query rather
// than the file's.
var escapes = map[byte]byte{
	'"':  '"',
	'\\': '\\',
	'n':  '\n',
	'r':  '\r',
	't':  '\t',
	'0':  0,
	'b':  '\b',
	'f':  '\f',
}

func unescape(body string, at int) (string, error) {
	var out strings.Builder
	for i := 0; i < len(body); i++ {
		if body[i] != '\\' {
			out.WriteByte(body[i])
			continue
		}
		if i+1 >= len(body) {
			return "", refuse("line %d: a scalar ending in a backslash", at)
		}
		next := body[i+1]
		c, ok := escapes[next]
		if !ok {
			return "", refuse("line %d: \\%c is not an escape", at, next)
		}
		out.WriteByte(c)
		i++
	}
	return out.String(), nil
}
