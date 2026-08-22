package zu

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// A byte string is spelled X'00AB' and is a sequence of octets that
// need not be text. The engine keeps it apart from a character string
// on purpose, and these are the tests that this client keeps it apart
// too rather than quietly deciding the octets say something.

func TestAByteStringComesBackAsOctetsAndNotAsText(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, `RETURN X'0041FF' AS b`)
	if !rows.Next() {
		t.Fatal("a statement that answered one row has none")
	}

	if got, err := rows.Row().Type(0); err != nil || got != TypeBytes {
		t.Errorf("the cell is %v (%v) and not bytes", got, err)
	}

	want := []byte{0x00, 0x41, 0xff}

	var b []byte
	if err := rows.Scan(&b); err != nil {
		t.Fatalf("a byte string does not scan into a []byte: %v", err)
	}
	if !bytes.Equal(b, want) {
		t.Errorf("X'0041FF' scanned as %#v", b)
	}

	got, err := rows.Row().Value(0)
	if err != nil {
		t.Fatalf("reading the cell: %v", err)
	}
	raw, ok := got.([]byte)
	if !ok {
		t.Fatalf("a byte string reads as %T rather than as octets", got)
	}
	if !bytes.Equal(raw, want) {
		t.Errorf("X'0041FF' read as %#v", raw)
	}
}

// X'0041' is two octets and the second of them is the code unit the
// letter A is written with in every encoding anybody uses. A client
// that let it scan into a string would be answering, on the caller's
// behalf, a question the engine keeps two types apart in order not to
// answer.
func TestOctetsDoNotScanIntoAStringJustBecauseTheyCouldBeRead(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, `RETURN X'0041' AS maybe`)
	if !rows.Next() {
		t.Fatal("no row")
	}

	var s string
	err := rows.Scan(&s)
	if err == nil {
		t.Fatalf("a byte string scanned into a string as %q", s)
	}
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("the failure is not one of ours: %#v", err)
	}
	for _, want := range []string{"maybe", "bytes", "string"} {
		if !strings.Contains(e.Message, want) {
			t.Errorf("the message %q does not say %q", e.Message, want)
		}
	}
	if s != "" {
		t.Errorf("the refused scan wrote %q anyway", s)
	}
}

// The other direction is fine, and is the reason [Rows.Scan] takes
// both. A character string is octets somebody has decided are text, so
// a caller asking for octets is asking for what is underneath.
func TestACharacterStringScansIntoOctetsBecauseThatIsWhatItIs(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, `RETURN "hi" AS s`)
	if !rows.Next() {
		t.Fatal("no row")
	}

	var b []byte
	if err := rows.Scan(&b); err != nil {
		t.Fatalf("a string does not scan into a []byte: %v", err)
	}
	if string(b) != "hi" {
		t.Errorf("\"hi\" scanned as %#v", b)
	}
}

// An empty byte string is a value. Handing it back as nil would make
// it indistinguishable from a null by the only test a caller has, which
// is whether the slice is nil.
func TestAByteStringOfNoOctetsIsNotTheSameAsNoByteString(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, `RETURN X'' AS empty, null AS nothing`)
	if !rows.Next() {
		t.Fatal("no row")
	}

	var empty, nothing []byte
	if err := rows.Scan(&empty, &nothing); err != nil {
		t.Fatalf("scanning the two: %v", err)
	}
	if empty == nil {
		t.Error("a byte string of no octets came back as nil, which reads as absent")
	}
	if len(empty) != 0 {
		t.Errorf("X'' came back as %#v", empty)
	}
	if nothing != nil {
		t.Errorf("a null came back as %#v rather than as nil", nothing)
	}

	// And through the untyped read, where the two are a []byte and an
	// absent value rather than two slices.
	got, err := rows.Row().Values()
	if err != nil {
		t.Fatalf("reading the row: %v", err)
	}
	if b, ok := got[0].([]byte); !ok || b == nil || len(b) != 0 {
		t.Errorf("X'' reads as %#v", got[0])
	}
	if got[1] != nil {
		t.Errorf("a null reads as %#v", got[1])
	}
}

// The engine lends the octets out of the result and they stop being
// there when it is freed. What comes back is a copy, so a caller who
// closed the result still holds what they read, and this is the test
// that says so rather than the comment.
func TestTheOctetsOutliveTheResultTheyCameFrom(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, `RETURN X'DEADBEEF' AS b`)
	if !rows.Next() {
		t.Fatal("no row")
	}
	var b []byte
	if err := rows.Scan(&b); err != nil {
		t.Fatalf("scanning: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("closing: %v", err)
	}

	// Something else runs on the same connection, so the memory the
	// first result had is a plausible thing to have been handed to it.
	other := query(t, conn, `RETURN X'00000000' AS b`)
	if !other.Next() {
		t.Fatal("no second row")
	}
	other.Close()

	if !bytes.Equal(b, []byte{0xde, 0xad, 0xbe, 0xef}) {
		t.Errorf("the octets read before the close are now %#v", b)
	}
}

// The tag is published, so it has a name in Go rather than a number,
// and the number is the ABI's.
func TestABytesCellSaysWhatItIs(t *testing.T) {
	if got := TypeBytes.String(); got != "bytes" {
		t.Errorf("the bytes type calls itself %q", got)
	}
	if TypeBytes != 13 {
		t.Errorf("the bytes tag is %d and the ABI's is 13", TypeBytes)
	}
}

// Every octet, because a client that copied with the wrong length or
// treated the borrowed bytes as NUL terminated would pass every test
// above and fail on the one octet that is zero.
func TestEveryOctetSurvivesTheCrossing(t *testing.T) {
	want := make([]byte, 256)
	var q strings.Builder
	q.WriteString("RETURN X'")
	const hex = "0123456789ABCDEF"
	for i := range want {
		want[i] = byte(i)
		q.WriteByte(hex[i>>4])
		q.WriteByte(hex[i&0xf])
	}
	q.WriteString("' AS b")

	conn := memory(t)
	rows := query(t, conn, q.String())
	if !rows.Next() {
		t.Fatal("no row")
	}
	var b []byte
	if err := rows.Scan(&b); err != nil {
		t.Fatalf("scanning 256 octets: %v", err)
	}
	if len(b) != len(want) {
		t.Fatalf("256 octets came back as %d", len(b))
	}
	for i := range want {
		if b[i] != want[i] {
			t.Fatalf("octet %d came back as %#x", i, b[i])
		}
	}
}
