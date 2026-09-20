package usecase

import (
	"math/big"
	"regexp"
	"strings"

	"Arthafreestyle/ERP/internal/model"
)

// parseAngkaIndonesia normalizes a figure exactly as Gemini transcribed it off a
// photo — "1.254.000,00", "10.500", plain "52000", or already our own decimal
// format — into the same big.Rat parseNumeric produces, never through a JSON number
// and never through float64 (isu #39 fase 1).
//
// The normalization is a pure function with no database in sight, the same reason
// hitungPosting lives apart from its I/O in pembelian_alokasi.go: a mistake here is
// the difference between a correct usulan and a silently wrong one, so it has to be
// testable without a fixture.
//
// Ambiguity is resolved by these rules, in order:
//
//  1. Neither ',' nor '.' present — a plain integer, used as-is.
//  2. Both ',' and '.' present — whichever comes LAST is the decimal separator, the
//     other is stripped as thousands grouping. Matches "1.254.000,00" and the
//     English-style "1,254,000.00" alike.
//  3. Only ',' present — more than one means every comma is thousands grouping and
//     is stripped ("1,254,000" -> 1254000); exactly one is read as the decimal
//     separator ("10,5" -> 10.5).
//  4. Only '.' present — more than one is stripped as grouping. Exactly one is
//     genuinely ambiguous ("10.500" could be ten-and-a-half or ten thousand five
//     hundred); this project's money is always whole rupiah, so a dot followed by
//     exactly three digits is read as thousands grouping and stripped, and any other
//     digit count is kept as a decimal point — which also covers our own
//     already-normalized "1254000.00" shape, whose fractional part is always two
//     digits.
func parseAngkaIndonesia(text string) (*big.Rat, error) {
	bersih := strings.TrimSpace(text)
	bersih = strings.TrimSpace(strings.TrimPrefix(strings.ToUpper(bersih), "RP"))
	bersih = strings.ReplaceAll(bersih, " ", "")

	if bersih == "" {
		return nil, model.Invalid("angka kosong")
	}

	negatif := false
	switch {
	case strings.HasPrefix(bersih, "-"):
		negatif = true
		bersih = bersih[1:]
	case strings.HasPrefix(bersih, "+"):
		bersih = bersih[1:]
	}

	adaKoma := strings.Contains(bersih, ",")
	adaTitik := strings.Contains(bersih, ".")

	switch {
	case adaKoma && adaTitik:
		if strings.LastIndex(bersih, ",") > strings.LastIndex(bersih, ".") {
			bersih = strings.Replace(strings.ReplaceAll(bersih, ".", ""), ",", ".", 1)
		} else {
			bersih = strings.ReplaceAll(bersih, ",", "")
		}

	case adaKoma:
		if strings.Count(bersih, ",") > 1 {
			bersih = strings.ReplaceAll(bersih, ",", "")
		} else {
			bersih = strings.Replace(bersih, ",", ".", 1)
		}

	case adaTitik:
		if strings.Count(bersih, ".") > 1 {
			bersih = strings.ReplaceAll(bersih, ".", "")
		} else if bagian := strings.SplitN(bersih, ".", 2); len(bagian[1]) == 3 {
			bersih = bagian[0] + bagian[1]
		}
	}

	if negatif {
		bersih = "-" + bersih
	}

	return parseNumeric(bersih)
}

var (
	// uangDesimalKosong matches a trailing zero-only fraction (",00", ".0") that carries
	// no value and is simply dropped.
	uangDesimalKosong = regexp.MustCompile(`[.,]0{1,2}$`)
	// uangDesimalKoma matches a trailing Indonesian decimal comma with one or two digits.
	uangDesimalKoma = regexp.MustCompile(`,\d{1,2}$`)
)

// parseUangIndonesia normalizes a rupiah figure (harga, diskon, PPN, total) as it is
// written on paper. Unlike parseAngkaIndonesia it never guesses that a dot is a
// decimal point: money on these documents is whole rupiah, so "5.000" is 5000 and
// "5.50" is 550 — every dot is thousands grouping.
//
// Rules, in order:
//
//  1. A trailing zero-only fraction (",00", ".00", ".0") is dropped.
//  2. A trailing ",d" or ",dd" is a decimal comma (Indonesian style) and kept.
//  3. Every remaining '.' and ',' is thousands grouping and stripped.
//
// Quantities must keep using parseAngkaIndonesia, where 2.5 kg is legitimate.
func parseUangIndonesia(text string) (*big.Rat, error) {
	bersih := strings.TrimSpace(text)
	bersih = strings.TrimSpace(strings.TrimPrefix(strings.ToUpper(bersih), "RP"))
	bersih = strings.ReplaceAll(bersih, " ", "")

	if bersih == "" {
		return nil, model.Invalid("angka kosong")
	}

	negatif := false

	switch {
	case strings.HasPrefix(bersih, "-"):
		negatif = true
		bersih = bersih[1:]
	case strings.HasPrefix(bersih, "+"):
		bersih = bersih[1:]
	}

	pecahan := ""

	switch {
	case uangDesimalKosong.MatchString(bersih):
		bersih = uangDesimalKosong.ReplaceAllString(bersih, "")
	case uangDesimalKoma.MatchString(bersih):
		i := strings.LastIndex(bersih, ",")
		pecahan = "." + bersih[i+1:]
		bersih = bersih[:i]
	}

	bersih = strings.NewReplacer(".", "", ",", "").Replace(bersih) + pecahan

	if negatif {
		bersih = "-" + bersih
	}

	return parseNumeric(bersih)
}
