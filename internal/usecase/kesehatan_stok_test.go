package usecase_test

// Isu #37: GET /laporan/kesehatan-stok. A read that is not a module, so the file is
// named after the read. These pin what the database decides — which products and
// rooms are in scope, what counts as dead stock, which opname lines count. What
// happens to the numbers afterwards (rounding, renormalisation, weighting) is pinned
// without a database in kesehatan_stok_skor_test.go.

import (
	"testing"
	"time"

	"Arthafreestyle/ERP/internal/model"
)

func tanggalHariLalu(hari int) string {
	return time.Now().AddDate(0, 0, -hari).Format("2006-01-02")
}

func kesehatanStok(t *testing.T, testApp *app, unit, idRuang *int64) *model.KesehatanStokResponse {
	t.Helper()

	resp, err := testApp.laporan.KesehatanStok(ctx(), &model.KesehatanStokRequest{
		IDRuang: idRuang, AktifIDUnitKerja: unit,
	})
	if err != nil {
		t.Fatalf("kesehatan stok: %v", err)
	}

	return resp
}

func komponenStok(t *testing.T, resp *model.KesehatanStokResponse, kode string) model.KesehatanStokKomponenResponse {
	t.Helper()

	for _, k := range resp.Komponen {
		if k.Kode == kode {
			return k
		}
	}

	t.Fatalf("komponen %s tidak ada", kode)

	return model.KesehatanStokKomponenResponse{}
}

func assertSkorKomponen(t *testing.T, resp *model.KesehatanStokResponse, kode string, want int64) {
	t.Helper()

	got := komponenStok(t, resp, kode).Skor
	if got == nil || *got != want {
		t.Fatalf("skor %s = %v, want %d", kode, got, want)
	}
}

// beliKe posts a one-line purchase of qty pcs at 10.000 each into a room, dated
// tanggal — the posting is dated on the document, which is how these tests put
// arrivals outside the look-back window.
func beliKe(t *testing.T, testApp *app, f fixture, idRuang, idProduct int64, tanggal, qty string) {
	t.Helper()

	pembelian, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
		ActorID: f.actor, Tanggal: tanggal, IDSupplier: f.supplier, IDRuang: idRuang,
		Detail: []model.PembelianDetailRequest{{
			IDProduct: idProduct, IDSatuanInput: f.pcs, QtyFaktur: qty, HargaSatuanInput: "10000",
		}},
	})
	if err != nil {
		t.Fatalf("create pembelian: %v", err)
	}

	ajukanDanPosting(t, testApp, f, pembelian.ID)
}

func jualDari(t *testing.T, testApp *app, f fixture, tanggal, qty string) *model.PenjualanResponse {
	t.Helper()

	nota, err := testApp.penjualan.Create(ctx(), &model.CreatePenjualanRequest{
		ActorID: f.actor, Tanggal: tanggal, IDRuang: f.ruang,
		Detail: []model.PenjualanDetailRequest{{
			IDProduct: f.product, IDSatuanInput: f.pcs, QtyInput: qty, HargaSatuanInput: "15000",
		}},
	})
	if err != nil {
		t.Fatalf("create penjualan: %v", err)
	}

	posted, err := testApp.penjualan.Posting(ctx(), &model.PostingPenjualanRequest{ID: nota.ID, ActorID: f.actor})
	if err != nil {
		t.Fatalf("posting penjualan: %v", err)
	}

	return posted
}

func mutasiKe(t *testing.T, testApp *app, f fixture, tanggal string, asal, tujuan int64, qty string) {
	t.Helper()

	mutasi, err := testApp.mutasi.Create(ctx(), &model.CreateMutasiRequest{
		ActorID: f.actor, Tanggal: tanggal, IDRuangAsal: asal, IDRuangTujuan: tujuan,
		Detail: []model.MutasiDetailRequest{{IDProduct: f.product, IDSatuanInput: f.pcs, QtyInput: qty}},
	})
	if err != nil {
		t.Fatalf("create mutasi: %v", err)
	}

	if _, err := testApp.mutasi.Posting(ctx(), &model.PostingMutasiRequest{ID: mutasi.ID, ActorID: f.actor}); err != nil {
		t.Fatalf("posting mutasi: %v", err)
	}
}

func setStokMinimum(t *testing.T, testApp *app, f fixture, idProduct, minimum int64) {
	t.Helper()

	if _, err := testApp.product.Update(ctx(), &model.UpdateProductRequest{
		ID: idProduct, ActorID: f.actor, StokMinimum: model.Optional[int64]{Present: true, Value: ptr(minimum)},
	}); err != nil {
		t.Fatalf("set stok_minimum: %v", err)
	}
}

func produkKedua(t *testing.T, testApp *app, f fixture, kode string, minimum int64) int64 {
	t.Helper()

	product, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID: f.actor, KodeBarang: kode, Nama: "Barang " + kode,
		IDSatuanDasar: f.pcs, StokMinimum: minimum,
	})
	if err != nil {
		t.Fatalf("create product %s: %v", kode, err)
	}

	return product.ID
}

func ruangLain(t *testing.T, testApp *app, f fixture, nama string, unit int64) int64 {
	t.Helper()

	ruang, err := testApp.ruang.Create(ctx(), &model.CreateRuangRequest{
		ActorID: f.actor, NamaRuang: nama, IDUnitKerja: unit,
	})
	if err != nil {
		t.Fatalf("create ruang %s: %v", nama, err)
	}

	return ruang.ID
}

func TestKesehatanStokUnitTanpaStokNull(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	resp := kesehatanStok(t, testApp, &f.unitKerja, nil)

	if resp.Skor != nil || resp.Status != nil {
		t.Fatalf("skor/status = %v/%v, want null — unit tanpa stok tidak sehat maupun sakit", resp.Skor, resp.Status)
	}
	for _, k := range resp.Komponen {
		if k.Skor != nil {
			t.Errorf("komponen %s skor = %d, want null", k.Kode, *k.Skor)
		}
	}
}

// Stock exactly at its minimum is menipis, not sehat — and the same product shows up
// on GET /product/stok-minimum for the same scope, so the two reads agree.
func TestKesehatanStokSamaDenganMinimumMenipisDanMunculDiStokMinimum(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	beliKe(t, testApp, f, f.ruang, f.product, tanggalHariLalu(0), "100")
	setStokMinimum(t, testApp, f, f.product, 100)

	resp := kesehatanStok(t, testApp, &f.unitKerja, nil)

	rincian := komponenStok(t, resp, model.KomponenKetersediaan).Rincian.(model.KetersediaanRincian)
	if rincian.ProdukDinilai != 1 || rincian.Menipis != 1 || rincian.Sehat != 0 {
		t.Fatalf("ketersediaan = %+v, want 1 produk menipis", rincian)
	}
	assertSkorKomponen(t, resp, model.KomponenKetersediaan, 50)

	daftar, _, err := testApp.product.StokMinimum(ctx(), &model.ListStokMinimumRequest{AktifIDUnitKerja: &f.unitKerja})
	if err != nil {
		t.Fatalf("stok minimum: %v", err)
	}
	if len(daftar) != 1 || daftar[0].IDProduct != f.product {
		t.Fatalf("stok-minimum = %+v, want produk yang sama dengan yang dinilai menipis", daftar)
	}
}

// A product with a minimum that this unit never carried does not lower KETERSEDIAAN —
// even though GET /product/stok-minimum, a shopping list, deliberately still lists it.
func TestKesehatanStokProdukTakPernahMasukUnitTidakDinilai(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	beliKe(t, testApp, f, f.ruang, f.product, tanggalHariLalu(0), "100")
	setStokMinimum(t, testApp, f, f.product, 10)
	belumPernah := produkKedua(t, testApp, f, "BRG-TAK-DIJUAL", 50)

	resp := kesehatanStok(t, testApp, &f.unitKerja, nil)

	rincian := komponenStok(t, resp, model.KomponenKetersediaan).Rincian.(model.KetersediaanRincian)
	if rincian.ProdukDinilai != 1 || rincian.Sehat != 1 {
		t.Fatalf("ketersediaan = %+v, want hanya produk yang dipegang unit ini", rincian)
	}
	assertSkorKomponen(t, resp, model.KomponenKetersediaan, 100)

	daftar, _, err := testApp.product.StokMinimum(ctx(), &model.ListStokMinimumRequest{AktifIDUnitKerja: &f.unitKerja})
	if err != nil {
		t.Fatalf("stok minimum: %v", err)
	}
	if len(daftar) != 1 || daftar[0].IDProduct != belumPernah {
		t.Fatalf("stok-minimum = %+v, want produk yang belum pernah masuk tetap terdaftar di sana", daftar)
	}
}

// Goods bought long ago and only shuttled between rooms since are still dead:
// MUTASI_KELUAR is not demand, and a move inside the unit is not an arrival.
func TestKesehatanStokMutasiBolakBalikTetapStokMati(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)
	toko := ruangLain(t, testApp, f, "Toko", f.unitKerja)

	beliKe(t, testApp, f, f.ruang, f.product, tanggalHariLalu(120), "100")
	mutasiKe(t, testApp, f, tanggalHariLalu(0), f.ruang, toko, "40")
	mutasiKe(t, testApp, f, tanggalHariLalu(0), toko, f.ruang, "40")

	resp := kesehatanStok(t, testApp, &f.unitKerja, nil)

	rincian := komponenStok(t, resp, model.KomponenStokMati).Rincian.(model.StokMatiRincian)
	if rincian.NilaiStokMati != "1000000.00" || rincian.NilaiPersediaan != "1000000.00" {
		t.Fatalf("stok mati = %+v, want seluruh 1000000.00 mati", rincian)
	}
	assertSkorKomponen(t, resp, model.KomponenStokMati, 0)
}

// The reversal trap: a sale 100 days ago cancelled today leaves a
// PEMBATALAN_TRANSAKSI row dated today. That is not the goods moving, and it must
// not revive stock that has not sold in the window.
func TestKesehatanStokPembatalanPenjualanLamaTidakMenghidupkanStokMati(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	beliKe(t, testApp, f, f.ruang, f.product, tanggalHariLalu(150), "100")
	nota := jualDari(t, testApp, f, tanggalHariLalu(100), "10")

	if _, err := testApp.penjualan.Batal(ctx(), &model.BatalPenjualanRequest{
		ID: nota.ID, ActorID: f.actor, AlasanBatal: "salah nota, dibatalkan jauh sesudahnya",
	}); err != nil {
		t.Fatalf("batal penjualan: %v", err)
	}

	resp := kesehatanStok(t, testApp, &f.unitKerja, nil)

	rincian := komponenStok(t, resp, model.KomponenStokMati).Rincian.(model.StokMatiRincian)
	if rincian.NilaiStokMati != rincian.NilaiPersediaan {
		t.Fatalf("stok mati = %+v, want seluruh persediaan mati — pembalik hari ini bukan permintaan", rincian)
	}
	assertSkorKomponen(t, resp, model.KomponenStokMati, 0)
}

// Demand is judged across the unit: a warehouse holding reserve for a shop that
// sold the product last week is not dead stock, even though the warehouse itself
// only ever moved it by mutasi — and the old in-unit transfer did not make the goods
// "new" either.
func TestKesehatanStokPenjualanDiRuangLainDalamUnitMenghidupkanCadangan(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)
	cadangan := ruangLain(t, testApp, f, "Gudang Cadangan", f.unitKerja)

	beliKe(t, testApp, f, f.ruang, f.product, tanggalHariLalu(150), "100")
	mutasiKe(t, testApp, f, tanggalHariLalu(120), f.ruang, cadangan, "50")
	jualDari(t, testApp, f, tanggalHariLalu(10), "5")

	resp := kesehatanStok(t, testApp, &f.unitKerja, nil)

	assertSkorKomponen(t, resp, model.KomponenStokMati, 100)
	for _, r := range resp.Ruang {
		if r.NilaiStokMati != "0.00" {
			t.Errorf("ruang %s nilai stok mati = %s, want 0.00", r.NamaRuang, r.NilaiStokMati)
		}
	}
}

// Goods that arrived inside the window have not had the chance to sell yet.
func TestKesehatanStokBarangBaruBelumLakuBukanStokMati(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	beliKe(t, testApp, f, f.ruang, f.product, tanggalHariLalu(0), "100")

	resp := kesehatanStok(t, testApp, &f.unitKerja, nil)

	assertSkorKomponen(t, resp, model.KomponenStokMati, 100)
}

// A room holding stock with no opname in the window scores 0 for accuracy, and the
// whole score still comes out: KETERSEDIAAN does not apply (no minimum anywhere) and
// is renormalised away, CAKUPAN_MINIMUM is 0, STOK_MATI is 100 —
// 25×100 / 65 = 38.46 → 38, KRITIS.
func TestKesehatanStokRuangTanpaOpnameSkorNol(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	beliKe(t, testApp, f, f.ruang, f.product, tanggalHariLalu(0), "100")

	resp := kesehatanStok(t, testApp, &f.unitKerja, nil)

	assertSkorKomponen(t, resp, model.KomponenAkurasiOpname, 0)

	rincian := komponenStok(t, resp, model.KomponenAkurasiOpname).Rincian.(model.AkurasiOpnameRincian)
	if rincian.RuangDinilai != 1 || rincian.RuangTanpaOpname != 1 {
		t.Fatalf("akurasi = %+v, want 1 ruang tanpa opname", rincian)
	}
	if len(resp.Ruang) != 1 || resp.Ruang[0].NomorOpname != nil || resp.Ruang[0].SkorAkurasiOpname != 0 {
		t.Fatalf("ruang = %+v, want tanpa nomor opname dan skor akurasi 0", resp.Ruang)
	}

	if resp.Skor == nil || *resp.Skor != 38 {
		t.Fatalf("skor = %v, want 38", resp.Skor)
	}
	if resp.Status == nil || *resp.Status != model.StatusKesehatanKritis {
		t.Fatalf("status = %v, want KRITIS", resp.Status)
	}
}

// Ten pcs short on one product and ten pcs over on another net to zero, but they
// are two counting errors: 200.000 of selisih against 2.000.000 counted scores 90.
func TestKesehatanStokOpnameSurplusDefisitSamaNilaiTidakSaling(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)
	kedua := produkKedua(t, testApp, f, "BRG-002", 0)

	beliKe(t, testApp, f, f.ruang, f.product, tanggalHariLalu(0), "100")
	beliKe(t, testApp, f, f.ruang, kedua, tanggalHariLalu(0), "100")

	opname := buatOpname(t, testApp, f)
	hasil := tarikSaldo(t, testApp, f, opname.ID)
	patchStokSO(t, testApp, f, opname.ID, detailByProduct(t, hasil, f.product).ID, 90)
	patchStokSO(t, testApp, f, opname.ID, detailByProduct(t, hasil, kedua).ID, 110)
	ajukanOpname(t, testApp, f, opname.ID)
	postingOpname(t, testApp, f, opname.ID)

	resp := kesehatanStok(t, testApp, &f.unitKerja, nil)

	rincian := komponenStok(t, resp, model.KomponenAkurasiOpname).Rincian.(model.AkurasiOpnameRincian)
	if rincian.NilaiSelisih != "200000.00" || rincian.NilaiDihitung != "2000000.00" {
		t.Fatalf("akurasi = %+v, want selisih 200000.00 dari 2000000.00", rincian)
	}
	assertSkorKomponen(t, resp, model.KomponenAkurasiOpname, 90)
}

// A line left uncounted (stok_so NULL) is out of both sums — not counted is not zero.
func TestKesehatanStokBarisBelumDihitungTidakMemengaruhiAkurasi(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)
	kedua := produkKedua(t, testApp, f, "BRG-002", 0)

	beliKe(t, testApp, f, f.ruang, f.product, tanggalHariLalu(0), "100")
	beliKe(t, testApp, f, f.ruang, kedua, tanggalHariLalu(0), "100")

	opname := buatOpname(t, testApp, f)
	hasil := tarikSaldo(t, testApp, f, opname.ID)
	patchStokSO(t, testApp, f, opname.ID, detailByProduct(t, hasil, f.product).ID, 100)
	ajukanOpname(t, testApp, f, opname.ID)
	postingOpname(t, testApp, f, opname.ID)

	resp := kesehatanStok(t, testApp, &f.unitKerja, nil)

	rincian := komponenStok(t, resp, model.KomponenAkurasiOpname).Rincian.(model.AkurasiOpnameRincian)
	if rincian.NilaiDihitung != "1000000.00" || rincian.NilaiSelisih != "0.00" {
		t.Fatalf("akurasi = %+v, want hanya baris terhitung (1000000.00) tanpa selisih", rincian)
	}
	assertSkorKomponen(t, resp, model.KomponenAkurasiOpname, 100)
}

// Rooms of another unit are never scored, and naming one through id_ruang yields an
// empty scope — skor null — rather than a 404 or a 403.
func TestKesehatanStokRuangUnitLainTidakIkut(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	unitLain, err := testApp.unitKerja.Create(ctx(), &model.CreateUnitKerjaRequest{
		ActorID: f.actor, Kode: ptr("LAIN"), Nama: "Unit Lain",
	})
	if err != nil {
		t.Fatalf("create unit kerja: %v", err)
	}
	ruangUnitLain := ruangLain(t, testApp, f, "Gudang Unit Lain", unitLain.ID)

	beliKe(t, testApp, f, f.ruang, f.product, tanggalHariLalu(0), "100")
	beliKe(t, testApp, f, ruangUnitLain, f.product, tanggalHariLalu(0), "30")

	sendiri := kesehatanStok(t, testApp, &f.unitKerja, nil)
	if len(sendiri.Ruang) != 1 || sendiri.Ruang[0].IDRuang != f.ruang {
		t.Fatalf("ruang = %+v, want hanya ruang milik unit aktif", sendiri.Ruang)
	}
	rincian := komponenStok(t, sendiri, model.KomponenStokMati).Rincian.(model.StokMatiRincian)
	if rincian.NilaiPersediaan != "1000000.00" {
		t.Fatalf("nilai persediaan = %s, want 1000000.00 — stok unit lain tidak ikut", rincian.NilaiPersediaan)
	}

	menyeberang := kesehatanStok(t, testApp, &f.unitKerja, &ruangUnitLain)
	if menyeberang.Skor != nil || len(menyeberang.Ruang) != 0 {
		t.Fatalf("id_ruang di luar unit aktif: skor = %v, ruang = %+v, want null dan kosong", menyeberang.Skor, menyeberang.Ruang)
	}
}
