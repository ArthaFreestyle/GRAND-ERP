package usecase

import (
	"math/big"
	"regexp"

	"Arthafreestyle/ERP/internal/model"
)

// Scales of the NUMERIC columns this module computes into. Rounding to the wrong
// one is not a display problem: the value is what gets stored, and kartu_stok is
// append-only.
const (
	// skalaUang matches NUMERIC(20,2) — every money column in pembelian and
	// kartu_stok.
	skalaUang = 2
	// skalaQty matches NUMERIC(18,4) — qty_faktur, qty_diterima, kartu_stok.qty_input.
	skalaQty = 4
	// skalaKoli matches NUMERIC(10,2) — pembelian_detail.jumlah_koli.
	skalaKoli = 2
	// skalaHPP matches NUMERIC(20,4) — harga_pokok_satuan_dasar.
	skalaHPP = 4
)

// desimalPattern is deliberately stricter than big.Rat.SetString, which also
// accepts "1/3" and "1e5". Neither is a value PostgreSQL would ever hand back for a
// NUMERIC, and accepting them here would let a fraction reach a column that cannot
// hold one.
var desimalPattern = regexp.MustCompile(`^[+-]?\d+(\.\d+)?$`)

// parseNumeric turns a NUMERIC rendered as text into an exact rational.
//
// big.Rat, not float64: money and quantities are decimal, and 0.1 has no exact
// binary representation. Every intermediate value in the freight and cost
// arithmetic stays exact until it is rounded once, deliberately, at the end.
func parseNumeric(text string) (*big.Rat, error) {
	if !desimalPattern.MatchString(text) {
		return nil, model.Invalid("nilai desimal tidak valid: " + text)
	}

	value, ok := new(big.Rat).SetString(text)
	if !ok {
		return nil, model.Invalid("nilai desimal tidak valid: " + text)
	}

	return value, nil
}

// mustParseNumeric is parseNumeric for values that came out of PostgreSQL, where a
// malformed decimal would mean the driver lied rather than that a caller typed
// something wrong. It answers zero instead of an error so a read path never has to
// carry one.
func mustParseNumeric(text string) *big.Rat {
	value, err := parseNumeric(text)
	if err != nil {
		return new(big.Rat)
	}

	return value
}

// formatNumeric renders an exact rational at the given scale.
//
// big.Rat.FloatString rounds the last digit to nearest with halves away from zero,
// which is the same rule PostgreSQL's ROUND applies to NUMERIC. Matching it matters:
// a value computed here and a value the database recomputes must agree, or a CHECK
// constraint comparing them fails on a rounding difference no one can see.
func formatNumeric(value *big.Rat, scale int) string {
	return value.FloatString(scale)
}

// roundNumeric rounds to a scale and returns the result as an exact rational, so
// further arithmetic continues from the stored value rather than from the unrounded
// one. Summing unrounded shares and then rounding gives a different total than
// summing rounded ones — and it is the rounded ones that get stored.
func roundNumeric(value *big.Rat, scale int) *big.Rat {
	rounded, _ := new(big.Rat).SetString(value.FloatString(scale))

	return rounded
}

// ratFromInt lifts a whole number into the same arithmetic as the decimals.
func ratFromInt(value int64) *big.Rat {
	return new(big.Rat).SetInt64(value)
}

// isWholeNumber reports whether a rational has no fractional part.
//
// Used where a NUMERIC has to land in a BIGINT column: qty_faktur x
// faktor_konversi must be a whole number of base units, because qty_dasar is
// BIGINT. 2.5 boxes of 5 is 12.5 base units, which cannot be stored — and silently
// rounding it would put a quantity into kartu_stok that no one asked for.
func isWholeNumber(value *big.Rat) bool {
	return value.IsInt()
}
