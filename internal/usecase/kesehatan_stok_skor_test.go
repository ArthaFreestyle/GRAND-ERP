package usecase

// Isu #37: the stock health score's arithmetic, pinned without a database — the
// same reason pembelian_alokasi_test.go is internal. What the database decides
// (which products and rooms are in scope, what is dead, which opname counts) lives in
// kesehatan_stok_test.go; this file is only what happens to those numbers after.

import (
	"testing"
	"time"

	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
)

func ptrKesehatan[T any](v T) *T { return &v }

func komponenKesehatan(t *testing.T, resp *model.KesehatanStokResponse, kode string) model.KesehatanStokKomponenResponse {
	t.Helper()

	for _, k := range resp.Komponen {
		if k.Kode == kode {
			return k
		}
	}

	t.Fatalf("komponen %s tidak ada", kode)

	return model.KesehatanStokKomponenResponse{}
}

// The unit score comes from the components' exact values. KETERSEDIAAN is exactly
// 80.5 (shown 81) and CAKUPAN_MINIMUM exactly 80.4 (shown 80); weighted 35:15 the
// exact figure is 80.47 → 80, while averaging the rounded components would give
// 80.7 → 81.
func TestKesehatanSkorUnitDariNilaiEksakBukanDariKomponenTerbulat(t *testing.T) {
	resp := susunKesehatanStok(entity.KesehatanStokProduk{
		ProdukDinilai: 200, Sehat: 161, Habis: 39,
		ProdukDipegang: 250, ProdukBerminimum: 201,
	}, nil, nil, nil, time.Now())

	if s := komponenKesehatan(t, resp, model.KomponenKetersediaan).Skor; s == nil || *s != 81 {
		t.Fatalf("skor ketersediaan = %v, want 81 (80.5 dibulatkan menjauhi nol)", s)
	}
	if s := komponenKesehatan(t, resp, model.KomponenCakupanMinimum).Skor; s == nil || *s != 80 {
		t.Fatalf("skor cakupan = %v, want 80", s)
	}
	if resp.Skor == nil || *resp.Skor != 80 {
		t.Fatalf("skor unit = %v, want 80 — dari nilai eksak 80.47, bukan 81 dari komponen terbulat", resp.Skor)
	}
}

// A scope with nothing in it is neither healthy nor sick.
func TestKesehatanTanpaApapunSkorNull(t *testing.T) {
	resp := susunKesehatanStok(entity.KesehatanStokProduk{}, nil, nil, nil, time.Now())

	if resp.Skor != nil || resp.Status != nil {
		t.Fatalf("skor/status = %v/%v, want nil/nil", resp.Skor, resp.Status)
	}
	if len(resp.Komponen) != 4 {
		t.Fatalf("len(komponen) = %d, want 4 — selalu keempatnya", len(resp.Komponen))
	}
	for _, k := range resp.Komponen {
		if k.Skor != nil {
			t.Errorf("komponen %s skor = %d, want nil", k.Kode, *k.Skor)
		}
	}
	if resp.Ruang == nil || len(resp.Ruang) != 0 {
		t.Errorf("ruang = %v, want [] (bukan null)", resp.Ruang)
	}
}

// KETERSEDIAAN does not apply when no product has a minimum, so the unit score is
// renormalised over the other three — but CAKUPAN_MINIMUM is 0 in exactly that case,
// so the neglect still shows. 25×100 + 25×0 + 15×0 over 65 = 38.46 → 38.
func TestKesehatanKomponenTidakBerlakuDinormalisasiUlang(t *testing.T) {
	resp := susunKesehatanStok(
		entity.KesehatanStokProduk{ProdukDipegang: 10},
		[]entity.KesehatanStokRuang{{
			IDRuang: 1, NamaRuang: "Gudang", TotalStok: 10,
			NilaiPersediaan: "1000.00", NilaiStokMati: "0.00",
		}},
		nil, nil, time.Now(),
	)

	if s := komponenKesehatan(t, resp, model.KomponenKetersediaan).Skor; s != nil {
		t.Fatalf("skor ketersediaan = %d, want nil", *s)
	}
	if resp.Skor == nil || *resp.Skor != 38 {
		t.Fatalf("skor unit = %v, want 38", resp.Skor)
	}
	if resp.Status == nil || *resp.Status != model.StatusKesehatanKritis {
		t.Fatalf("status = %v, want KRITIS", resp.Status)
	}
}

func TestSkorAkurasiRuang(t *testing.T) {
	opname := func(selisih, dihitung string) entity.KesehatanStokRuang {
		return entity.KesehatanStokRuang{
			IDStokOpname:        ptrKesehatan(int64(1)),
			NilaiSelisihOpname:  ptrKesehatan(selisih),
			NilaiDihitungOpname: ptrKesehatan(dihitung),
		}
	}

	cases := []struct {
		nama  string
		baris entity.KesehatanStokRuang
		want  int64
	}{
		{"tanpa opname dinilai nol, bukan dilewati", entity.KesehatanStokRuang{}, 0},
		{"selisih 200 dari 1000", opname("200.00", "1000.0000"), 80},
		{"selisih melebihi nilai dihitung tidak negatif", opname("1500.00", "1000.0000"), 0},
		{"dihitung nol tanpa selisih", opname("0.00", "0.0000"), 100},
		{"dihitung nol dengan selisih", opname("5.00", "0.0000"), 0},
	}

	for _, c := range cases {
		got := bulatkanSkor(skorAkurasiRuang(c.baris))
		if got == nil || *got != c.want {
			t.Errorf("%s: skor = %v, want %d", c.nama, got, c.want)
		}
	}
}

// A perfectly counted main warehouse worth 9.000 and an uncounted shelf worth 1.000
// score 90 for accuracy, not the plain mean 50 — and the shelf, the worse room, is
// listed first.
func TestAkurasiOpnameBerbobotNilaiPersediaan(t *testing.T) {
	resp := susunKesehatanStok(entity.KesehatanStokProduk{}, []entity.KesehatanStokRuang{
		{
			IDRuang: 1, NamaRuang: "Gudang Utama", TotalStok: 900,
			NilaiPersediaan: "9000.00", NilaiStokMati: "0.00",
			IDStokOpname: ptrKesehatan(int64(7)), NomorOpname: ptrKesehatan("SO/1"),
			NilaiSelisihOpname: ptrKesehatan("0.00"), NilaiDihitungOpname: ptrKesehatan("9000.0000"),
		},
		{
			IDRuang: 2, NamaRuang: "Rak Kasir", TotalStok: 100,
			NilaiPersediaan: "1000.00", NilaiStokMati: "0.00",
		},
	}, nil, nil, time.Now())

	akurasi := komponenKesehatan(t, resp, model.KomponenAkurasiOpname)
	if akurasi.Skor == nil || *akurasi.Skor != 90 {
		t.Fatalf("skor akurasi = %v, want 90", akurasi.Skor)
	}

	rincian, ok := akurasi.Rincian.(model.AkurasiOpnameRincian)
	if !ok {
		t.Fatalf("rincian akurasi bertipe %T", akurasi.Rincian)
	}
	if rincian.RuangDinilai != 2 || rincian.RuangTanpaOpname != 1 {
		t.Errorf("ruang dinilai/tanpa opname = %d/%d, want 2/1", rincian.RuangDinilai, rincian.RuangTanpaOpname)
	}

	if len(resp.Ruang) != 2 || resp.Ruang[0].IDRuang != 2 {
		t.Fatalf("urutan ruang = %+v, want Rak Kasir (terburuk) dulu", resp.Ruang)
	}
}

func TestStatusKesehatanAmbang(t *testing.T) {
	cases := map[int64]string{
		100: model.StatusKesehatanSehat,
		80:  model.StatusKesehatanSehat,
		79:  model.StatusKesehatanPerluPerhatian,
		60:  model.StatusKesehatanPerluPerhatian,
		59:  model.StatusKesehatanKritis,
		0:   model.StatusKesehatanKritis,
	}

	for skor, want := range cases {
		if got := statusKesehatan(ptrKesehatan(skor)); got == nil || *got != want {
			t.Errorf("status(%d) = %v, want %s", skor, got, want)
		}
	}
}
