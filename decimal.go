package zu

import (
	"math/big"
	"strings"
)

// A Decimal is an exact number: an integer, and how many of its digits
// are on the right of the point. The value is Unscaled divided by ten
// to the Scale, so 1234 at scale 2 is 12.34 and 1234 at scale 0 is
// 1234.
//
// It is not a float64. The engine keeps a decimal exact because that is
// what the type is for, and handing it over as binary floating point
// would lose the thing the caller asked for on the last step of the
// journey. A money column read through this comes back with the digits
// it was written with.
//
// Unscaled is a [math/big.Int] because the engine's is 128 bits and Go
// has no fixed width type that wide. That is the same choice the
// DuckDB driver makes, for the same reason, and it means a caller who
// already reaches for math/big is not converting between two spellings
// of the same number.
type Decimal struct {
	// Unscaled is the digits as one integer, sign and all. A zero
	// Decimal has it nil, which reads as zero everywhere here rather
	// than panicking, since a zero value of a struct is a thing Go
	// programs make by accident and a nil dereference is a poor way to
	// find out.
	Unscaled *big.Int
	// Scale is how many of those digits are after the point. It is
	// never negative: the engine's decimal has a scale in 0 to 38.
	Scale int32
}

// String is the number written out, with the point where the scale
// says it is and no exponent. This is the spelling the conformance
// corpus writes a decimal in, so it round trips through [ParseDecimal].
func (d Decimal) String() string {
	if d.Unscaled == nil {
		if d.Scale <= 0 {
			return "0"
		}
		return "0." + strings.Repeat("0", int(d.Scale))
	}
	digits := d.Unscaled.String()
	sign := ""
	if strings.HasPrefix(digits, "-") {
		sign, digits = "-", digits[1:]
	}
	if d.Scale <= 0 {
		return sign + digits
	}
	// A number with fewer digits than the scale is all fraction, and
	// the zeros in front of it are part of the answer: 5 at scale 3 is
	// 0.005 and not 5.000.
	if int(d.Scale) >= len(digits) {
		digits = strings.Repeat("0", int(d.Scale)-len(digits)+1) + digits
	}
	cut := len(digits) - int(d.Scale)
	return sign + digits[:cut] + "." + digits[cut:]
}

// Float64 is the number as a float64, and whether it survived. The
// second result is false when the conversion lost something, which is
// most decimals: it is here so that a caller who wants a float has one
// call for it and is told, rather than reaching into Unscaled and
// finding out later.
func (d Decimal) Float64() (float64, bool) {
	f := new(big.Float)
	if d.Unscaled != nil {
		f.SetInt(d.Unscaled)
	}
	if d.Scale > 0 {
		f.Quo(f, new(big.Float).SetInt(pow10(int(d.Scale))))
	}
	out, accuracy := f.Float64()
	return out, accuracy == big.Exact
}

// ParseDecimal reads the spelling [Decimal.String] writes: an optional
// sign, digits, and at most one point. The scale is how many digits
// followed the point, so "12.30" is 1230 at scale 2 and "12.3" is 123
// at scale 1, which are the same number and not the same decimal. The
// engine keeps them apart and so does this.
func ParseDecimal(s string) (Decimal, error) {
	whole, frac, pointed := strings.Cut(s, ".")
	if s == "" || strings.Contains(frac, ".") {
		return Decimal{}, misuse("a decimal is digits with at most one point in them, and " + s + " is not")
	}
	digits := whole + frac
	// SetString takes the sign off the front of the whole part, which
	// is where it has to be, and refuses everything else including an
	// empty string and an exponent.
	n, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return Decimal{}, misuse("a decimal is digits with at most one point in them, and " + s + " is not")
	}
	scale := int32(0)
	if pointed {
		scale = int32(len(frac))
	}
	return Decimal{Unscaled: n, Scale: scale}, nil
}

// pow10 is ten to the n as a big.Int.
func pow10(n int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
}

// decimal rebuilds the engine's 128 bit unscaled integer from the two
// halves the ABI hands it over in. It arrives split because it is two's
// complement and C has no portable type that wide: hi is the top 64
// signed and lo the bottom 64 unsigned.
//
// The shift and the or are done on big.Int, whose bitwise operators are
// defined as two's complement with the sign extended forever, which is
// exactly what the halves are halves of. A caller can check it on the
// edges: hi -1 and lo all ones is -1, and hi 0 with the same lo is two
// to the sixty four minus one.
func decimal(hi int64, lo uint64, scale int32) Decimal {
	n := new(big.Int).SetInt64(hi)
	n.Lsh(n, 64)
	n.Or(n, new(big.Int).SetUint64(lo))
	return Decimal{Unscaled: n, Scale: scale}
}
