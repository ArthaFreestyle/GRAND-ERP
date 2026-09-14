package usecase

import (
	"math/big"
	"sort"
	"strconv"
	"time"

	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/model/converter"
)

// The stock health score's arithmetic — isu #37 — kept pure and apart from the I/O,
// the way pembelian_alokasi.go is, so its rounding and renormalisation rules are
// tested without a fixture.
//
// Every intermediate value is an exact big.Rat. The inputs are NUMERIC money, and a
// score assembled from float64 shares can land one point away from the same score
// recomputed in SQL while debugging. Rounding happens once, at the very end, with
// formatNumeric's rule (halves away from zero — PostgreSQL's ROUND).

// Weights, windows, and thresholds are constants, not config, on purpose: a score
// whose formula differs per deployment cannot be compared across outlets, and a
// change to it should leave a commit behind rather than one env var changed quietly.
const (
	bobotKetersediaan   int64 = 35
	bobotStokMati       int64 = 25
	bobotAkurasiOpname  int64 = 25
	bobotCakupanMinimum int64 = 15

	// hariStokMati: stock with no sale or usage in the unit for this many days, and
	// that first arrived longer ago than that, is dead.
	hariStokMati = 90
	// hariAkurasiOpname: a room holding stock without a POSTED opname cut off within
	// this many days scores 0 for accuracy.
	hariAkurasiOpname = 90

	ambangSehat          int64 = 80
	ambangPerluPerhatian int64 = 60
)

var seratus = big.NewRat(100, 1)

// skorKetersediaan is 100 × (sehat + ½ menipis) / produk_dinilai, or nil when no
// product qualifies — there is nothing to be available.
func skorKetersediaan(p entity.KesehatanStokProduk) *big.Rat {
	if p.ProdukDinilai == 0 {
		return nil
	}

	nilai := new(big.Rat).Add(ratFromInt(p.Sehat), big.NewRat(p.Menipis, 2))
	nilai.Quo(nilai, ratFromInt(p.ProdukDinilai))

	return nilai.Mul(nilai, seratus)
}

// skorCakupanMinimum is 100 × produk_berminimum / produk_dipegang, or nil when the
// scope holds no product at all.
func skorCakupanMinimum(p entity.KesehatanStokProduk) *big.Rat {
	if p.ProdukDipegang == 0 {
		return nil
	}

	nilai := new(big.Rat).SetFrac64(p.ProdukBerminimum, p.ProdukDipegang)

	return nilai.Mul(nilai, seratus)
}

// skorStokMati is 100 × (1 − nilai_stok_mati / nilai_persediaan) — measured by value,
// not by item count, so a hundred dead items worth a thousand rupiah never weigh more
// than one dead item worth fifty million. Nil when there is no value to judge.
func skorStokMati(nilaiPersediaan, nilaiStokMati *big.Rat) *big.Rat {
	if nilaiPersediaan.Sign() <= 0 {
		return nil
	}

	rasio := new(big.Rat).Quo(nilaiStokMati, nilaiPersediaan)

	return klampSkor(new(big.Rat).Mul(new(big.Rat).Sub(big.NewRat(1, 1), rasio), seratus))
}

// skorAkurasiRuang scores one room's latest opname in the window.
//
// No opname is 0, never "skipped": skipping would give a unit that never counts its
// stock a perfect accuracy score, the exact opposite of the incentive wanted.
//
// Otherwise 100 × max(0, 1 − selisih / dihitung). A counted value of zero (every
// counted line had a frozen stok_awal of 0) has no ratio: no selisih is 100, any
// selisih — goods found where the books said none — is 0.
func skorAkurasiRuang(baris entity.KesehatanStokRuang) *big.Rat {
	if baris.IDStokOpname == nil {
		return new(big.Rat)
	}

	selisih := ratOrZero(baris.NilaiSelisihOpname)
	dihitung := ratOrZero(baris.NilaiDihitungOpname)

	if dihitung.Sign() <= 0 {
		if selisih.Sign() == 0 {
			return new(big.Rat).Set(seratus)
		}

		return new(big.Rat)
	}

	rasio := new(big.Rat).Quo(selisih, dihitung)

	return klampSkor(new(big.Rat).Mul(new(big.Rat).Sub(big.NewRat(1, 1), rasio), seratus))
}

// rataBerbobot averages the non-nil scores by their weights. Nil scores drop out and
// the remaining weights are renormalised — that is what makes "not applicable"
// different from "bad". If every remaining weight is zero it falls back to a plain
// average of the remaining scores; nil only when no score is left at all.
func rataBerbobot(skor, bobot []*big.Rat) *big.Rat {
	jumlah := new(big.Rat)
	totalBobot := new(big.Rat)
	jumlahPolos := new(big.Rat)
	berlaku := int64(0)

	for i := range skor {
		if skor[i] == nil {
			continue
		}

		berlaku++
		jumlahPolos.Add(jumlahPolos, skor[i])
		jumlah.Add(jumlah, new(big.Rat).Mul(skor[i], bobot[i]))
		totalBobot.Add(totalBobot, bobot[i])
	}

	if berlaku == 0 {
		return nil
	}

	if totalBobot.Sign() == 0 {
		return jumlahPolos.Quo(jumlahPolos, ratFromInt(berlaku))
	}

	return jumlah.Quo(jumlah, totalBobot)
}

// bulatkanSkor rounds an exact score to a whole number, halves away from zero. A nil
// score stays nil.
func bulatkanSkor(skor *big.Rat) *int64 {
	if skor == nil {
		return nil
	}

	nilai, err := strconv.ParseInt(formatNumeric(skor, 0), 10, 64)
	if err != nil {
		// FloatString(0) of a value clamped to [0, 100] is always a small integer.
		return nil
	}

	return &nilai
}

// statusKesehatan labels a rounded score, so a client never needs the thresholds to
// pick a colour. Applied to the rounded figure — the one the caller sees — so 79.6
// shown as 80 is also labelled SEHAT.
func statusKesehatan(skor *int64) *string {
	if skor == nil {
		return nil
	}

	status := model.StatusKesehatanKritis
	switch {
	case *skor >= ambangSehat:
		status = model.StatusKesehatanSehat
	case *skor >= ambangPerluPerhatian:
		status = model.StatusKesehatanPerluPerhatian
	}

	return &status
}

// susunKesehatanStok assembles the whole response from the two reads.
//
// The unit score is computed from the components' EXACT values and rounded once;
// each component's own rounded skor is for display only. Averaging the rounded
// components instead can land one point off the true figure.
//
// STOK_MATI for the unit is Σ nilai_stok_mati / Σ nilai_persediaan over the rooms —
// the value-weighted figure, identical to weighting each room's score by its value.
// AKURASI_OPNAME weights each room's score by its nilai_persediaan, so a small shelf
// at the till never counts as much as the main warehouse.
//
// The per-room list is ordered worst first by the plain mean of that room's two
// scores (the two components carry equal weight), ties by name and id.
func susunKesehatanStok(
	produk entity.KesehatanStokProduk,
	ruang []entity.KesehatanStokRuang,
	idUnitKerja, idRuang *int64,
	sekarang time.Time,
) *model.KesehatanStokResponse {
	nilaiPersediaan := new(big.Rat)
	nilaiStokMati := new(big.Rat)
	nilaiSelisih := new(big.Rat)
	nilaiDihitung := new(big.Rat)
	tanpaOpname := int64(0)

	skorAkurasi := make([]*big.Rat, 0, len(ruang))
	bobotAkurasi := make([]*big.Rat, 0, len(ruang))

	type ruangTerurut struct {
		response model.KesehatanStokRuangResponse
		gabungan *big.Rat
	}
	daftarRuang := make([]ruangTerurut, 0, len(ruang))

	for _, baris := range ruang {
		persediaan := mustParseNumeric(baris.NilaiPersediaan)
		mati := mustParseNumeric(baris.NilaiStokMati)

		nilaiPersediaan.Add(nilaiPersediaan, persediaan)
		nilaiStokMati.Add(nilaiStokMati, mati)

		if baris.IDStokOpname == nil {
			tanpaOpname++
		} else {
			nilaiSelisih.Add(nilaiSelisih, ratOrZero(baris.NilaiSelisihOpname))
			nilaiDihitung.Add(nilaiDihitung, ratOrZero(baris.NilaiDihitungOpname))
		}

		skorMatiRuang := skorStokMati(persediaan, mati)
		skorAkurasiRuangIni := skorAkurasiRuang(baris)

		skorAkurasi = append(skorAkurasi, skorAkurasiRuangIni)
		bobotAkurasi = append(bobotAkurasi, persediaan)

		daftarRuang = append(daftarRuang, ruangTerurut{
			response: converter.KesehatanStokRuangToResponse(
				baris, bulatkanSkor(skorMatiRuang), *bulatkanSkor(skorAkurasiRuangIni),
			),
			gabungan: rataBerbobot(
				[]*big.Rat{skorMatiRuang, skorAkurasiRuangIni},
				[]*big.Rat{big.NewRat(1, 1), big.NewRat(1, 1)},
			),
		})
	}

	sort.SliceStable(daftarRuang, func(i, j int) bool {
		if cmp := daftarRuang[i].gabungan.Cmp(daftarRuang[j].gabungan); cmp != 0 {
			return cmp < 0
		}
		if daftarRuang[i].response.NamaRuang != daftarRuang[j].response.NamaRuang {
			return daftarRuang[i].response.NamaRuang < daftarRuang[j].response.NamaRuang
		}

		return daftarRuang[i].response.IDRuang < daftarRuang[j].response.IDRuang
	})

	responsRuang := make([]model.KesehatanStokRuangResponse, len(daftarRuang))
	for i := range daftarRuang {
		responsRuang[i] = daftarRuang[i].response
	}

	var akurasi *big.Rat
	if len(ruang) > 0 {
		akurasi = rataBerbobot(skorAkurasi, bobotAkurasi)
	}

	ketersediaan := skorKetersediaan(produk)
	stokMati := skorStokMati(nilaiPersediaan, nilaiStokMati)
	cakupan := skorCakupanMinimum(produk)

	unit := rataBerbobot(
		[]*big.Rat{ketersediaan, stokMati, akurasi, cakupan},
		[]*big.Rat{
			ratFromInt(bobotKetersediaan), ratFromInt(bobotStokMati),
			ratFromInt(bobotAkurasiOpname), ratFromInt(bobotCakupanMinimum),
		},
	)
	skor := bulatkanSkor(unit)

	return &model.KesehatanStokResponse{
		IDUnitKerja:  idUnitKerja,
		IDRuang:      idRuang,
		Skor:         skor,
		Status:       statusKesehatan(skor),
		DihitungPada: sekarang,
		Komponen: []model.KesehatanStokKomponenResponse{
			{
				Kode: model.KomponenKetersediaan, Bobot: bobotKetersediaan, Skor: bulatkanSkor(ketersediaan),
				Rincian: model.KetersediaanRincian{
					ProdukDinilai: produk.ProdukDinilai, Sehat: produk.Sehat,
					Menipis: produk.Menipis, Habis: produk.Habis,
				},
			},
			{
				Kode: model.KomponenStokMati, Bobot: bobotStokMati, Skor: bulatkanSkor(stokMati),
				Rincian: model.StokMatiRincian{
					NilaiPersediaan: formatNumeric(nilaiPersediaan, skalaUang),
					NilaiStokMati:   formatNumeric(nilaiStokMati, skalaUang),
					HariAmbang:      hariStokMati,
				},
			},
			{
				Kode: model.KomponenAkurasiOpname, Bobot: bobotAkurasiOpname, Skor: bulatkanSkor(akurasi),
				Rincian: model.AkurasiOpnameRincian{
					RuangDinilai:     int64(len(ruang)),
					RuangTanpaOpname: tanpaOpname,
					NilaiSelisih:     formatNumeric(nilaiSelisih, skalaUang),
					NilaiDihitung:    formatNumeric(nilaiDihitung, skalaUang),
					HariAmbang:       hariAkurasiOpname,
				},
			},
			{
				Kode: model.KomponenCakupanMinimum, Bobot: bobotCakupanMinimum, Skor: bulatkanSkor(cakupan),
				Rincian: model.CakupanMinimumRincian{
					ProdukDipegang: produk.ProdukDipegang, ProdukBerminimum: produk.ProdukBerminimum,
				},
			},
		},
		Ruang: responsRuang,
	}
}

func klampSkor(skor *big.Rat) *big.Rat {
	if skor.Sign() < 0 {
		return new(big.Rat)
	}

	if skor.Cmp(seratus) > 0 {
		return new(big.Rat).Set(seratus)
	}

	return skor
}

func ratOrZero(text *string) *big.Rat {
	if text == nil {
		return new(big.Rat)
	}

	return mustParseNumeric(*text)
}
