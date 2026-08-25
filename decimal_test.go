package zu

import (
	"errors"
	"math"
	"math/big"
	"strings"
	"testing"
)

// A decimal is an exact number and a float64 is not, which is the whole
// reason the engine grew the type: CAST('1.20' AS DECIMAL(5, 2)) used to
// come back as a binary64, and by the time it printed, neither the value
// nor the two places that were asked for had survived. These are the
// tests that this client carries the digits the rest of the way.

func TestADecimalComesBackWithTheDigitsItWasWrittenWith(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, `RETURN CAST('1.20' AS DECIMAL(5, 2)) AS v`)
	if !rows.Next() {
		t.Fatal("a statement that answered one row has none")
	}

	if got, err := rows.Row().Type(0); err != nil || got != TypeDecimal {
		t.Errorf("the cell is %v (%v) and not a decimal", got, err)
	}

	var d Decimal
	if err := rows.Scan(&d); err != nil {
		t.Fatalf("a decimal does not scan into a Decimal: %v", err)
	}
	if d.Scale != 2 {
		t.Errorf("two places went in and %d came back", d.Scale)
	}
	if d.Unscaled.Int64() != 120 {
		t.Errorf("the unscaled integer is %v and not 120", d.Unscaled)
	}
	// The point of the exercise. A float64 prints this as 1.2 at best,
	// and the case asked for two places.
	if d.String() != "1.20" {
		t.Errorf("1.20 came back as %q", d)
	}

	got, err := rows.Row().Value(0)
	if err != nil {
		t.Fatalf("reading the cell: %v", err)
	}
	if _, ok := got.(Decimal); !ok {
		t.Fatalf("a decimal reads as %T rather than as one", got)
	}
}

// The engine holds the unscaled integer in 128 bits, and hands it over
// in two halves because C has no portable type that wide. A negative
// number is where a client that got the halves wrong finds out, since
// the top half of one is all ones and the arithmetic that rebuilds it
// has to be two's complement rather than a sum of two magnitudes.
func TestANegativeDecimalKeepsItsSign(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, `RETURN CAST('-0.05' AS DECIMAL(5, 2)) AS v`)
	if !rows.Next() {
		t.Fatal("no row")
	}

	var d Decimal
	if err := rows.Scan(&d); err != nil {
		t.Fatalf("scanning a negative decimal: %v", err)
	}
	if d.String() != "-0.05" {
		t.Errorf("-0.05 came back as %q", d)
	}
	if d.Unscaled.Sign() >= 0 {
		t.Errorf("the unscaled integer %v is not negative", d.Unscaled)
	}
}

// Thirty eight digits is the largest precision DECIMAL(p, s) accepts and
// the largest an i128 holds, so a number with all of them is the widest
// value that can arrive. It does not fit an int64 and it does not fit a
// float64 either, which is why Unscaled is a big.Int.
func TestADecimalWiderThanAnInt64Arrives(t *testing.T) {
	conn := memory(t)
	const digits = "12345678901234567890123456789012345678"
	rows := query(t, conn, `RETURN CAST('`+digits+`' AS DECIMAL(38, 0)) AS v`)
	if !rows.Next() {
		t.Fatal("no row")
	}

	var d Decimal
	if err := rows.Scan(&d); err != nil {
		t.Fatalf("scanning a thirty eight digit decimal: %v", err)
	}
	if d.String() != digits {
		t.Errorf("%s came back as %q", digits, d)
	}
}

// The rebuild, on the edges where a client that shifted the wrong half
// or sign extended the bottom one goes wrong. Two to the sixty four
// minus one and minus one have the same bottom half and differ only in
// the top, so a client that ignored either half passes one of these and
// fails the other.
func TestTheUnscaledIntegerIsRebuiltFromBothHalves(t *testing.T) {
	const allOnes = math.MaxUint64

	for _, c := range []struct {
		hi   int64
		lo   uint64
		want string
	}{
		{0, 0, "0"},
		{0, 1, "1"},
		{-1, allOnes, "-1"},
		{0, allOnes, "18446744073709551615"},
		{1, 0, "18446744073709551616"},
		{-1, 0, "-18446744073709551616"},
		{math.MaxInt64, allOnes, "170141183460469231731687303715884105727"},
		{math.MinInt64, 0, "-170141183460469231731687303715884105728"},
	} {
		got := decimal(c.hi, c.lo, 0)
		if got.Unscaled.String() != c.want {
			t.Errorf("hi %d and lo %d rebuilt as %v and not %s", c.hi, c.lo, got.Unscaled, c.want)
		}
	}
}

// Where the point goes, which is the part that is easy to get right for
// 12.34 and wrong for 0.005: a number with fewer digits than the scale
// is all fraction, and the zeros in front of it are part of the answer.
func TestDecimalStringPutsThePointWhereTheScaleSaysItIs(t *testing.T) {
	for _, c := range []struct {
		unscaled int64
		scale    int32
		want     string
	}{
		{1234, 0, "1234"},
		{1234, 2, "12.34"},
		{1234, 4, "0.1234"},
		{5, 3, "0.005"},
		{-5, 3, "-0.005"},
		{120, 2, "1.20"},
		{0, 2, "0.00"},
		{0, 0, "0"},
		{-1, 0, "-1"},
	} {
		got := Decimal{Unscaled: big.NewInt(c.unscaled), Scale: c.scale}.String()
		if got != c.want {
			t.Errorf("%d at scale %d printed as %q and not %q", c.unscaled, c.scale, got, c.want)
		}
	}
}

// A zero value of a struct is a thing Go programs make by accident, and
// a nil dereference is a poor way to find out. It reads as zero, at the
// scale it says.
func TestTheZeroDecimalIsZeroAndNotAPanic(t *testing.T) {
	var d Decimal
	if d.String() != "0" {
		t.Errorf("the zero Decimal printed as %q", d)
	}
	if f, exact := d.Float64(); f != 0 || !exact {
		t.Errorf("the zero Decimal is %v (exact %v)", f, exact)
	}
	if got := (Decimal{Scale: 3}).String(); got != "0.000" {
		t.Errorf("a zero at scale three printed as %q", got)
	}
}

// 12.30 and 12.3 are the same number and not the same decimal. The
// engine keeps them apart because the scale rides on the value, and a
// client that normalised either way would be throwing away the thing
// the caller wrote.
func TestParseDecimalKeepsTheScaleTheDigitsWereWrittenAt(t *testing.T) {
	for _, c := range []struct {
		text     string
		unscaled int64
		scale    int32
	}{
		{"12.30", 1230, 2},
		{"12.3", 123, 1},
		{"12", 12, 0},
		{"-0.05", -5, 2},
		{"+7.5", 75, 1},
		{"0.000", 0, 3},
	} {
		got, err := ParseDecimal(c.text)
		if err != nil {
			t.Errorf("%q does not parse: %v", c.text, err)
			continue
		}
		if got.Scale != c.scale || got.Unscaled.Int64() != c.unscaled {
			t.Errorf("%q parsed as %v at scale %d", c.text, got.Unscaled, got.Scale)
		}
	}
}

// Round tripping, which is what makes the spelling worth having: the
// conformance corpus writes a decimal this way, and a client reading a
// case has to get back the value the case wrote.
func TestADecimalRoundTripsThroughItsOwnSpelling(t *testing.T) {
	for _, text := range []string{"0", "0.00", "1.20", "-0.05", "1234", "-1234.5678", "0.005"} {
		d, err := ParseDecimal(text)
		if err != nil {
			t.Errorf("%q does not parse: %v", text, err)
			continue
		}
		if d.String() != text {
			t.Errorf("%q went round as %q", text, d)
		}
	}
}

// What is not a decimal. An exponent is refused rather than read, for
// the reason the corpus refuses it: 1E2 and 100 are the same number at
// two scales, and something that means one of them should say which.
func TestParseDecimalRefusesWhatIsNotDigitsAndAPoint(t *testing.T) {
	for _, text := range []string{"", ".", "1.2.3", "1e2", "1E2", "abc", "1 2", "0x10", "--1", "1.2 "} {
		got, err := ParseDecimal(text)
		if err == nil {
			t.Errorf("%q parsed as %v", text, got)
			continue
		}
		var e *Error
		if !errors.As(err, &e) {
			t.Errorf("the failure for %q is not one of ours: %#v", text, err)
			continue
		}
		if e.Status != Misuse {
			t.Errorf("%q failed with %v rather than as a misuse", text, e.Status)
		}
		if !strings.Contains(e.Message, text) {
			t.Errorf("the message %q does not say what was handed in", e.Message)
		}
	}
}

// Float64 is one call for the conversion and an answer about whether it
// survived, which beats reaching into Unscaled and finding out later. A
// tenth is not a binary fraction and never will be, so the common case
// says so.
func TestFloat64SaysWhenTheConversionLostSomething(t *testing.T) {
	for _, c := range []struct {
		text  string
		want  float64
		exact bool
	}{
		{"1.25", 1.25, true},
		{"-0.5", -0.5, true},
		{"1234", 1234, true},
		{"0.1", 0.1, false},
		{"1.20", 1.2, false},
	} {
		d, err := ParseDecimal(c.text)
		if err != nil {
			t.Fatalf("%q does not parse: %v", c.text, err)
		}
		got, exact := d.Float64()
		if got != c.want || exact != c.exact {
			t.Errorf("%q is %v (exact %v) and not %v (exact %v)", c.text, got, exact, c.want, c.exact)
		}
	}
}

// A caller who scans a decimal into a *float64 has said which of the two
// they want, the same way an integer widens into one. It is the only
// place the exactness stops, and it stops because it was asked to.
func TestADecimalWidensIntoAFloat64BecauseTheDestinationAskedFor(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, `RETURN CAST('1.25' AS DECIMAL(5, 2)) AS v`)
	if !rows.Next() {
		t.Fatal("no row")
	}

	var f float64
	if err := rows.Scan(&f); err != nil {
		t.Fatalf("a decimal does not scan into a float64: %v", err)
	}
	if f != 1.25 {
		t.Errorf("1.25 widened to %v", f)
	}
}

// It does not widen into a string, which would be this client picking a
// spelling on the caller's behalf, and it does not turn up in a
// destination that holds a different type. Both failures name the column
// and both types, because either one could be the mistake.
func TestADecimalDoesNotScanIntoADestinationThatCannotHoldOne(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, `RETURN CAST('1.20' AS DECIMAL(5, 2)) AS money`)
	if !rows.Next() {
		t.Fatal("no row")
	}

	var s string
	err := rows.Scan(&s)
	if err == nil {
		t.Fatalf("a decimal scanned into a string as %q", s)
	}
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("the failure is not one of ours: %#v", err)
	}
	for _, want := range []string{"money", "decimal", "string"} {
		if !strings.Contains(e.Message, want) {
			t.Errorf("the message %q does not say %q", e.Message, want)
		}
	}
}

// The other direction: a Decimal destination takes a decimal and nothing
// else. A float is not one, however round it looks, because the engine
// held a binary64 and the digits it would print are not digits anybody
// wrote.
func TestADecimalDestinationRefusesAFloat(t *testing.T) {
	conn := memory(t)
	rows := query(t, conn, `RETURN 1.5 AS f`)
	if !rows.Next() {
		t.Fatal("no row")
	}

	var d Decimal
	if err := rows.Scan(&d); err == nil {
		t.Fatalf("a float scanned into a Decimal as %v", d)
	}
	if d.Unscaled != nil {
		t.Errorf("the refused scan wrote %v anyway", d.Unscaled)
	}
}

// The type name, which turns up in every mismatch message and in the
// conformance runner's output, so it is worth one line saying what it
// is rather than finding out from a diff.
func TestTheDecimalTypeIsNamedInAWayAMessageCanUse(t *testing.T) {
	if got := TypeDecimal.String(); got != "decimal" {
		t.Errorf("the decimal type is called %q", got)
	}
}
