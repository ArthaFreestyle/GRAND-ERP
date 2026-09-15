package usecase

import (
	"math/big"
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
