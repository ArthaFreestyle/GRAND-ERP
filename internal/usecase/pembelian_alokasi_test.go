package usecase

// Internal test, unlike the rest of internal/usecase: this is the costing
// arithmetic, and it needs no database at all. Every rule it pins is one the schema
// cannot express — proportional value, exact-summing allocation, the QTY fallback —
// so a database round trip would only slow it down without proving more.

import (
	"math/big"
	"testing"

	"Arthafreestyle/ERP/internal/entity"
)

func rat(t *testing.T, text string) *big.Rat {
	t.Helper()

	value, err := parseNumeric(text)
	if err != nil {
		t.Fatalf("parseNumeric(%q): %v", text, err)
	}

	return value
}

// The worked example from isu #4: 100 pcs at 10.000 with 50.000 of freight
// allocated, of which 95 arrived.
//
// Sending the full invoice value in would price those 95 units at 11.052 rather
// than 10.500. That is not a figure that corrects itself when the rest of the
// shipment turns up: kartu_stok uses a moving average, outgoing rows lock in the
// cost in force at the time, and any sale in between books the inflated number
// permanently into cost of goods sold. The table is append-only, so it can only be
// reversed, never repaired.
func TestNilaiMasukProporsionalTerhadapQtyDiterima(t *testing.T) {
	baris := []barisPosting{{
		QtyDasar:         100,
		QtyDiterimaDasar: 95,
		Subtotal:         rat(t, "1000000"),
		JumlahKoli:       rat(t, "1"),
	}}

	hitungPosting(headerPosting{
		DiskonNota:  new(big.Rat),
		PPN:         new(big.Rat),
		BiayaAngkut: rat(t, "50000"),
		Metode:      entity.MetodeAlokasiKoli,
	}, baris)

	if got := formatNumeric(baris[0].NilaiMasuk, skalaUang); got != "997500.00" {
		t.Errorf("nilai_masuk = %s, want 997500.00 (95/100 x 1.050.000)", got)
	}

	// Cost per base unit is expressed against the invoice quantity, so the figure
	// is the same whether all 100 arrived or only 95 — which is what lets a
	// follow-up receipt take the rest at the same cost without recomputing.
	if got := formatNumeric(baris[0].HargaPokokSatuanDasar, skalaHPP); got != "10500.0000" {
		t.Errorf("harga_pokok_satuan_dasar = %s, want 10500.0000", got)
	}

	// The 95 units come in at 997.500, which is 10.500 each — not 11.052.
	hpp := new(big.Rat).Quo(baris[0].NilaiMasuk, ratFromInt(95))
	if got := formatNumeric(hpp, skalaHPP); got != "10500.0000" {
		t.Errorf("nilai_masuk / qty_diterima = %s, want 10500.0000", got)
	}
}

// A line where nothing arrived carries no value into stock, and posting writes it
// no kartu_stok row at all.
func TestBarisTanpaPenerimaanBernilaiNol(t *testing.T) {
	baris := []barisPosting{{
		QtyDasar:         50,
		QtyDiterimaDasar: 0,
		Subtotal:         rat(t, "500000"),
		JumlahKoli:       new(big.Rat),
	}}

	hitungPosting(headerPosting{
		DiskonNota:  new(big.Rat),
		PPN:         new(big.Rat),
		BiayaAngkut: new(big.Rat),
		Metode:      entity.MetodeAlokasiKoli,
	}, baris)

	if got := formatNumeric(baris[0].NilaiMasuk, skalaUang); got != "0.00" {
		t.Errorf("nilai_masuk = %s, want 0.00", got)
	}

	// The cost per unit is still known — it is what the invoice says the goods
	// cost, and a follow-up receipt needs it.
	if got := formatNumeric(baris[0].HargaPokokSatuanDasar, skalaHPP); got != "10000.0000" {
		t.Errorf("harga_pokok_satuan_dasar = %s, want 10000.0000", got)
	}
}

// Freight allocated across lines has to sum to exactly biaya_angkut. Rounding each
// share on its own does not: three equal lines splitting 100 give 33.33 three times,
// and the missing cent would quietly become inventory value nobody was billed for.
func TestAlokasiBiayaAngkutBerjumlahPersis(t *testing.T) {
	for _, tc := range []struct {
		nama  string
		total string
		basis []string
	}{
		{"tiga bagian sama", "100.00", []string{"1", "1", "1"}},
		{"tujuh bagian sama", "1000.00", []string{"1", "1", "1", "1", "1", "1", "1"}},
		{"basis desimal", "125000.00", []string{"0.33", "1.67", "2.5"}},
		{"satu baris", "999.99", []string{"4"}},
	} {
		t.Run(tc.nama, func(t *testing.T) {
			total := rat(t, tc.total)

			basis := make([]*big.Rat, len(tc.basis))
			for i := range tc.basis {
				basis[i] = rat(t, tc.basis[i])
			}

			bagian := bagiProporsional(total, basis, skalaUang)

			jumlah := new(big.Rat)
			for _, nilai := range bagian {
				jumlah.Add(jumlah, nilai)
			}

			if jumlah.Cmp(total) != 0 {
				t.Errorf("jumlah bagian = %s, want %s", formatNumeric(jumlah, skalaUang), tc.total)
			}
		})
	}
}

// The rounding remainder goes on the largest share, where it is proportionally
// least visible.
func TestSisaPembulatanKeBasisTerbesar(t *testing.T) {
	basis := []*big.Rat{rat(t, "1"), rat(t, "1"), rat(t, "8")}

	bagian := bagiProporsional(rat(t, "100.00"), basis, skalaUang)

	if got := formatNumeric(bagian[0], skalaUang); got != "10.00" {
		t.Errorf("bagian[0] = %s, want 10.00", got)
	}

	if got := formatNumeric(bagian[2], skalaUang); got != "80.00" {
		t.Errorf("bagian[2] = %s, want 80.00", got)
	}

	// A case where a remainder actually exists: 1/3 of 10 at two decimals.
	sisa := bagiProporsional(rat(t, "10.00"), []*big.Rat{rat(t, "1"), rat(t, "1"), rat(t, "1")}, skalaUang)

	// 3.33 + 3.33 + 3.34 — the extra cent lands on the last of the tied largest,
	// which is the first one by the earliest-wins rule, so check the sum instead.
	jumlah := new(big.Rat)
	for _, nilai := range sisa {
		jumlah.Add(jumlah, nilai)
	}

	if got := formatNumeric(jumlah, skalaUang); got != "10.00" {
		t.Errorf("jumlah = %s, want 10.00", got)
	}

	if got := formatNumeric(sisa[0], skalaUang); got != "3.34" {
		t.Errorf("sisa[0] = %s, want 3.34 (sisa jatuh ke basis terbesar pertama)", got)
	}
}

// Zero freight and an all-zero basis both have to answer zeros rather than divide
// by zero.
func TestAlokasiTidakMembagiDenganNol(t *testing.T) {
	nol := bagiProporsional(new(big.Rat), []*big.Rat{rat(t, "1"), rat(t, "2")}, skalaUang)
	for i, nilai := range nol {
		if nilai.Sign() != 0 {
			t.Errorf("total nol: bagian[%d] = %s, want 0", i, formatNumeric(nilai, skalaUang))
		}
	}

	basisNol := bagiProporsional(rat(t, "100"), []*big.Rat{new(big.Rat), new(big.Rat)}, skalaUang)
	for i, nilai := range basisNol {
		if nilai.Sign() != 0 {
			t.Errorf("basis nol: bagian[%d] = %s, want 0", i, formatNumeric(nilai, skalaUang))
		}
	}

	if kosong := bagiProporsional(rat(t, "100"), nil, skalaUang); len(kosong) != 0 {
		t.Errorf("basis kosong menghasilkan %d bagian, want 0", len(kosong))
	}
}

// jumlah_koli is filled by the warehouse while unpacking, so a document can reach
// posting with every line still at zero. Allocating on that basis would divide by
// zero; the fallback is QTY, weighted by what actually arrived.
func TestKoliSemuaNolJatuhKeMetodeQty(t *testing.T) {
	baris := []barisPosting{
		{QtyDasar: 10, QtyDiterimaDasar: 10, Subtotal: rat(t, "100000"), JumlahKoli: new(big.Rat)},
		{QtyDasar: 30, QtyDiterimaDasar: 30, Subtotal: rat(t, "300000"), JumlahKoli: new(big.Rat)},
	}

	hitungPosting(headerPosting{
		DiskonNota:  new(big.Rat),
		PPN:         new(big.Rat),
		BiayaAngkut: rat(t, "40000"),
		Metode:      entity.MetodeAlokasiKoli,
	}, baris)

	// 10 and 30 base units split 40.000 as 10.000 and 30.000.
	if got := formatNumeric(baris[0].AlokasiBiaya, skalaUang); got != "10000.00" {
		t.Errorf("alokasi_biaya[0] = %s, want 10000.00", got)
	}

	if got := formatNumeric(baris[1].AlokasiBiaya, skalaUang); got != "30000.00" {
		t.Errorf("alokasi_biaya[1] = %s, want 30000.00", got)
	}
}

// Freight follows what was carried, not what was invoiced: the fallback basis is
// qty_diterima_dasar, so a line that arrived short pays a smaller share.
func TestAlokasiQtyMengikutiQtyDiterima(t *testing.T) {
	baris := []barisPosting{
		{QtyDasar: 100, QtyDiterimaDasar: 0, Subtotal: rat(t, "100000"), JumlahKoli: new(big.Rat)},
		{QtyDasar: 100, QtyDiterimaDasar: 100, Subtotal: rat(t, "100000"), JumlahKoli: new(big.Rat)},
	}

	hitungPosting(headerPosting{
		DiskonNota:  new(big.Rat),
		PPN:         new(big.Rat),
		BiayaAngkut: rat(t, "60000"),
		Metode:      entity.MetodeAlokasiQty,
	}, baris)

	if got := formatNumeric(baris[0].AlokasiBiaya, skalaUang); got != "0.00" {
		t.Errorf("baris yang tidak datang dapat alokasi %s, want 0.00", got)
	}

	if got := formatNumeric(baris[1].AlokasiBiaya, skalaUang); got != "60000.00" {
		t.Errorf("alokasi_biaya[1] = %s, want 60000.00", got)
	}
}

// ppn_dikreditkan decides whether the tax is part of what the goods cost.
func TestPPNDikreditkanMenentukanHargaPokok(t *testing.T) {
	buat := func() []barisPosting {
		return []barisPosting{{
			QtyDasar:         10,
			QtyDiterimaDasar: 10,
			Subtotal:         rat(t, "100000"),
			JumlahKoli:       new(big.Rat),
		}}
	}

	masuk := buat()
	hitungPosting(headerPosting{
		DiskonNota:     new(big.Rat),
		PPN:            rat(t, "11000"),
		PPNDikreditkan: false,
		BiayaAngkut:    new(big.Rat),
		Metode:         entity.MetodeAlokasiQty,
	}, masuk)

	if got := formatNumeric(masuk[0].HargaPokokSatuanDasar, skalaHPP); got != "11100.0000" {
		t.Errorf("ppn_dikreditkan=false: hpp = %s, want 11100.0000", got)
	}

	keluar := buat()
	hitungPosting(headerPosting{
		DiskonNota:     new(big.Rat),
		PPN:            rat(t, "11000"),
		PPNDikreditkan: true,
		BiayaAngkut:    new(big.Rat),
		Metode:         entity.MetodeAlokasiQty,
	}, keluar)

	if got := formatNumeric(keluar[0].HargaPokokSatuanDasar, skalaHPP); got != "10000.0000" {
		t.Errorf("ppn_dikreditkan=true: hpp = %s, want 10000.0000", got)
	}
}

// A nota-level discount is money not paid for these goods, so it comes off cost.
func TestDiskonNotaTersebarKeHargaPokok(t *testing.T) {
	baris := []barisPosting{
		{QtyDasar: 10, QtyDiterimaDasar: 10, Subtotal: rat(t, "100000"), JumlahKoli: new(big.Rat)},
		{QtyDasar: 10, QtyDiterimaDasar: 10, Subtotal: rat(t, "300000"), JumlahKoli: new(big.Rat)},
	}

	hitungPosting(headerPosting{
		DiskonNota:  rat(t, "40000"),
		PPN:         new(big.Rat),
		BiayaAngkut: new(big.Rat),
		Metode:      entity.MetodeAlokasiQty,
	}, baris)

	// Spread over subtotal: 10.000 and 30.000.
	if got := formatNumeric(baris[0].NilaiMasuk, skalaUang); got != "90000.00" {
		t.Errorf("nilai_masuk[0] = %s, want 90000.00", got)
	}

	if got := formatNumeric(baris[1].NilaiMasuk, skalaUang); got != "270000.00" {
		t.Errorf("nilai_masuk[1] = %s, want 270000.00", got)
	}
}

// Money is decimal, and 0.1 has no exact binary representation. big.Rat keeps every
// intermediate exact so the only rounding is the deliberate one at the end.
func TestNumericTidakLewatFloat(t *testing.T) {
	jumlah := new(big.Rat)
	for range 10 {
		jumlah.Add(jumlah, rat(t, "0.1"))
	}

	if got := formatNumeric(jumlah, skalaUang); got != "1.00" {
		t.Errorf("0.1 sepuluh kali = %s, want 1.00", got)
	}

	// FloatString rounds halves away from zero, the same rule PostgreSQL's ROUND
	// applies to NUMERIC. They have to agree, or a value computed here and one the
	// database recomputes differ by a cent nobody can see.
	for _, tc := range []struct{ masuk, mau string }{
		{"0.005", "0.01"},
		{"0.015", "0.02"},
		{"-0.005", "-0.01"},
		{"2.344999", "2.34"},
	} {
		if got := formatNumeric(rat(t, tc.masuk), skalaUang); got != tc.mau {
			t.Errorf("formatNumeric(%s) = %s, want %s", tc.masuk, got, tc.mau)
		}
	}
}

// parseNumeric is stricter than big.Rat.SetString on purpose: a fraction or an
// exponent is not something PostgreSQL hands back for a NUMERIC, and letting one
// through would put a value into a column that cannot hold it.
func TestParseNumericMenolakBentukAneh(t *testing.T) {
	for _, text := range []string{"1/3", "1e5", "", "abc", "1.2.3", " 1", "0x10"} {
		if _, err := parseNumeric(text); err == nil {
			t.Errorf("parseNumeric(%q) diterima, want ditolak", text)
		}
	}

	for _, text := range []string{"0", "-1.25", "+3", "1000000.0000"} {
		if _, err := parseNumeric(text); err != nil {
			t.Errorf("parseNumeric(%q) ditolak: %v", text, err)
		}
	}
}
