package usecase

import (
	"math/big"

	"Arthafreestyle/ERP/internal/entity"
)

// barisPosting is one detail line as the costing arithmetic sees it. The first four
// fields are inputs read from pembelian_detail; the last three are what posting
// computes and writes back.
type barisPosting struct {
	QtyDasar         int64
	QtyDiterimaDasar int64
	Subtotal         *big.Rat
	JumlahKoli       *big.Rat

	AlokasiBiaya          *big.Rat
	NilaiMasuk            *big.Rat
	HargaPokokSatuanDasar *big.Rat
}

// headerPosting carries the document-level figures that have to reach individual
// lines before a cost per unit exists.
type headerPosting struct {
	DiskonNota     *big.Rat
	PPN            *big.Rat
	PPNDikreditkan bool
	// BiayaAngkut is already zero when the supplier bears the freight — the caller
	// zeroes it, because freight inside the supplier's invoice is part of the line
	// prices and allocating it again would count it twice.
	BiayaAngkut *big.Rat
	Metode      string
}

// hitungPosting fills AlokasiBiaya, NilaiMasuk, and HargaPokokSatuanDasar on every
// line. It is pure arithmetic on exact rationals: no database, no rounding until
// each figure is stored.
//
// The value that reaches kartu_stok is proportional to what actually arrived, not
// the full invoice value. This is the single most expensive thing to get wrong in
// the module. kartu_stok uses a moving average and outgoing rows lock in the cost
// in force at the time, so a purchase of 100 that received 95 and sent the full
// invoice value in would price those 95 units at 11.052 instead of 10.500 — and any
// sale between that receipt and the follow-up shipment books the inflated figure
// permanently into cost of goods sold. kartu_stok is append-only; that cannot be
// repaired, only reversed.
//
// Cost per base unit is deliberately computed against qty_dasar, not
// qty_diterima_dasar. Both numerator and denominator scale by the same ratio, so the
// per-unit figure is identical either way — but expressing it against the invoice
// quantity is what makes the remaining value available to a follow-up receipt
// (isu #4 fase 3) without recomputing anything.
func hitungPosting(header headerPosting, baris []barisPosting) {
	if len(baris) == 0 {
		return
	}

	subtotals := make([]*big.Rat, len(baris))
	for i := range baris {
		subtotals[i] = baris[i].Subtotal
	}

	alokasi := bagiProporsional(header.BiayaAngkut, basisAlokasi(header.Metode, baris), skalaUang)

	// A nota-level discount belongs in cost of goods: it is money not paid for
	// these items. Spread over subtotal, since that is what it was calculated
	// against.
	diskon := bagiProporsional(header.DiskonNota, subtotals, skalaUang)

	// ppn_dikreditkan = true makes the PPN an input tax that never touches cost of
	// goods. False makes it part of what the item cost, so it has to reach the
	// lines.
	ppn := make([]*big.Rat, len(baris))
	for i := range ppn {
		ppn[i] = new(big.Rat)
	}

	if !header.PPNDikreditkan {
		ppn = bagiProporsional(header.PPN, subtotals, skalaUang)
	}

	for i := range baris {
		nilaiPenuh := new(big.Rat).Set(baris[i].Subtotal)
		nilaiPenuh.Sub(nilaiPenuh, diskon[i])
		nilaiPenuh.Add(nilaiPenuh, ppn[i])
		nilaiPenuh.Add(nilaiPenuh, alokasi[i])

		qtyDasar := ratFromInt(baris[i].QtyDasar)

		baris[i].AlokasiBiaya = alokasi[i]
		baris[i].HargaPokokSatuanDasar = roundNumeric(
			new(big.Rat).Quo(nilaiPenuh, qtyDasar), skalaHPP,
		)

		nilaiMasuk := new(big.Rat).Mul(nilaiPenuh, ratFromInt(baris[i].QtyDiterimaDasar))
		nilaiMasuk.Quo(nilaiMasuk, qtyDasar)

		baris[i].NilaiMasuk = roundNumeric(nilaiMasuk, skalaUang)
	}
}

// basisAlokasi picks what freight is shared out in proportion to.
//
// Koli is the stored default since migration 000008, and it is the honest basis: a
// carrier charges for the space taken, not for the count of items in it. But
// jumlah_koli is filled by the warehouse while unpacking, so a document can reach
// posting with every line still at zero — dividing by that total is the divide-by-
// zero the issue calls out. QTY is the fallback, and it uses qty_diterima_dasar
// because freight was paid to move what actually arrived.
func basisAlokasi(metode string, baris []barisPosting) []*big.Rat {
	if metode == entity.MetodeAlokasiKoli {
		total := new(big.Rat)
		for i := range baris {
			total.Add(total, baris[i].JumlahKoli)
		}

		if total.Sign() > 0 {
			basis := make([]*big.Rat, len(baris))
			for i := range baris {
				basis[i] = baris[i].JumlahKoli
			}

			return basis
		}
	}

	basis := make([]*big.Rat, len(baris))
	for i := range baris {
		basis[i] = ratFromInt(baris[i].QtyDiterimaDasar)
	}

	return basis
}

// bagiProporsional splits total across basis so the parts sum to exactly total.
//
// Rounding each share independently does not add up: three lines splitting 100 at
// two decimals give 33.33 three times, which is 99.99. The missing cent has to go
// somewhere, and it goes on the largest share — the line where it is proportionally
// least visible. Without this the freight allocated across details would disagree
// with biaya_angkut, and the difference would silently become inventory value that
// nobody was billed for.
//
// A zero total or an all-zero basis yields all zeros rather than dividing by zero.
func bagiProporsional(total *big.Rat, basis []*big.Rat, scale int) []*big.Rat {
	hasil := make([]*big.Rat, len(basis))
	for i := range hasil {
		hasil[i] = new(big.Rat)
	}

	if len(basis) == 0 || total.Sign() == 0 {
		return hasil
	}

	jumlahBasis := new(big.Rat)
	for _, nilai := range basis {
		jumlahBasis.Add(jumlahBasis, nilai)
	}

	if jumlahBasis.Sign() == 0 {
		return hasil
	}

	terbagi := new(big.Rat)

	for i, nilai := range basis {
		bagian := new(big.Rat).Mul(total, nilai)
		bagian.Quo(bagian, jumlahBasis)

		hasil[i] = roundNumeric(bagian, scale)
		terbagi.Add(terbagi, hasil[i])
	}

	if sisa := new(big.Rat).Sub(total, terbagi); sisa.Sign() != 0 {
		terbesar := indeksTerbesar(basis)
		hasil[terbesar].Add(hasil[terbesar], sisa)
	}

	return hasil
}

// indeksTerbesar returns the position of the largest value, earliest one on a tie.
// Deterministic on purpose: the same document must allocate the same way every
// time it is recomputed, or a reposting would move a cent between lines.
func indeksTerbesar(basis []*big.Rat) int {
	terbesar := 0
	for i := 1; i < len(basis); i++ {
		if basis[i].Cmp(basis[terbesar]) > 0 {
			terbesar = i
		}
	}

	return terbesar
}
