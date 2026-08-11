package usecase_test

// These run against a real PostgreSQL because almost everything they assert lives
// there: the append-only trigger on kartu_stok, the moving average it computes, the
// counter row that keeps two concurrent documents from sharing a number, and the
// partial unique index on no_faktur_supplier. A mock would agree with whatever the
// code did.

import (
	"testing"

	"Arthafreestyle/ERP/internal/model"
)

// pembelianFixture builds the master data one purchase needs: an actor (created_by
// is NOT NULL on both pembelian and kartu_stok), a room, a supplier, and a product
// sold in PCS with a 12-per DUS conversion.
type fixture struct {
	actor    int64
	ruang    int64
	supplier int64
	product  int64
	pcs      int64
	dus      int64
}

func pembelianFixture(t *testing.T, testApp *app) fixture {
	t.Helper()

	user, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		Username: "petugas_terima",
		Password: "rahasia123",
	})
	if err != nil {
		t.Fatalf("create actor: %v", err)
	}

	ruang, err := testApp.ruang.Create(ctx(), &model.CreateRuangRequest{NamaRuang: "Gudang Utama"})
	if err != nil {
		t.Fatalf("create ruang: %v", err)
	}

	supplier, err := testApp.supplier.Create(ctx(), &model.CreateSupplierRequest{Nama: "PT Sumber"})
	if err != nil {
		t.Fatalf("create supplier: %v", err)
	}

	pcs, err := testApp.satuan.Create(ctx(), &model.CreateSatuanRequest{Nama: "PCS"})
	if err != nil {
		t.Fatalf("create satuan PCS: %v", err)
	}

	dus, err := testApp.satuan.Create(ctx(), &model.CreateSatuanRequest{Nama: "DUS"})
	if err != nil {
		t.Fatalf("create satuan DUS: %v", err)
	}

	product, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID:       user.ID,
		KodeBarang:    "BRG-001",
		Nama:          "Kertas A4",
		IDSatuanDasar: pcs.ID,
		Satuan:        []model.CreateProductSatuanRequest{{IDSatuan: dus.ID, Faktor: 12}},
	})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}

	return fixture{
		actor:    user.ID,
		ruang:    ruang.ID,
		supplier: supplier.ID,
		product:  product.ID,
		pcs:      pcs.ID,
		dus:      dus.ID,
	}
}

// draftSederhana creates a one-line DRAFT: qty_faktur pcs at 10.000 each.
func draftSederhana(t *testing.T, testApp *app, f fixture, qtyFaktur string, qtyDiterima *string, keterangan *string) *model.PembelianResponse {
	t.Helper()

	pembelian, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
		ActorID:    f.actor,
		Tanggal:    "2026-08-11",
		IDSupplier: f.supplier,
		IDRuang:    f.ruang,
		Detail: []model.PembelianDetailRequest{{
			IDProduct:         f.product,
			IDSatuanInput:     f.pcs,
			QtyFaktur:         qtyFaktur,
			QtyDiterima:       qtyDiterima,
			HargaSatuanInput:  "10000",
			KeteranganSelisih: keterangan,
		}},
	})
	if err != nil {
		t.Fatalf("create pembelian: %v", err)
	}

	return pembelian
}

// Numbers reset per month and never repeat. Two documents in the same month get
// consecutive numbers; a document dated in another month starts that month's series
// at 1, so a July invoice typed in August still carries a July number.
func TestNomorDokumenBerurutanPerBulan(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	pertama := draftSederhana(t, testApp, f, "10", nil, nil)
	kedua := draftSederhana(t, testApp, f, "10", nil, nil)

	if pertama.Nomor != "BL/2026/08/0001" {
		t.Errorf("nomor pertama = %q, want BL/2026/08/0001", pertama.Nomor)
	}

	if kedua.Nomor != "BL/2026/08/0002" {
		t.Errorf("nomor kedua = %q, want BL/2026/08/0002", kedua.Nomor)
	}

	juli, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
		ActorID:    f.actor,
		Tanggal:    "2026-07-31",
		IDSupplier: f.supplier,
		IDRuang:    f.ruang,
		Detail: []model.PembelianDetailRequest{{
			IDProduct: f.product, IDSatuanInput: f.pcs,
			QtyFaktur: "5", HargaSatuanInput: "10000",
		}},
	})
	if err != nil {
		t.Fatalf("create pembelian juli: %v", err)
	}

	if juli.Nomor != "BL/2026/07/0001" {
		t.Errorf("nomor juli = %q, want BL/2026/07/0001", juli.Nomor)
	}
}

// The conversion factor comes from product_satuan and is stored as a snapshot, and
// the base-unit quantity is derived rather than typed. A unit the product is not
// sold in is refused: no foreign key ties pembelian_detail.id_satuan_input to
// product_satuan, so nothing else would catch it.
func TestKonversiSatuanDanFaktorSnapshot(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	pembelian, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
		ActorID:    f.actor,
		Tanggal:    "2026-08-11",
		IDSupplier: f.supplier,
		IDRuang:    f.ruang,
		Detail: []model.PembelianDetailRequest{{
			IDProduct: f.product, IDSatuanInput: f.dus,
			QtyFaktur: "3", HargaSatuanInput: "120000",
		}},
	})
	if err != nil {
		t.Fatalf("create pembelian: %v", err)
	}

	baris := pembelian.Detail[0]
	if baris.FaktorKonversi != 12 {
		t.Errorf("faktor_konversi = %d, want 12", baris.FaktorKonversi)
	}

	if baris.QtyDasar != 36 {
		t.Errorf("qty_dasar = %d, want 36 (3 DUS x 12)", baris.QtyDasar)
	}

	// Not registered on this product: PCS and DUS are, a third unit is not.
	lain, err := testApp.satuan.Create(ctx(), &model.CreateSatuanRequest{Nama: "PALLET"})
	if err != nil {
		t.Fatalf("create satuan: %v", err)
	}

	_, err = testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
		ActorID:    f.actor,
		Tanggal:    "2026-08-11",
		IDSupplier: f.supplier,
		IDRuang:    f.ruang,
		Detail: []model.PembelianDetailRequest{{
			IDProduct: f.product, IDSatuanInput: lain.ID,
			QtyFaktur: "1", HargaSatuanInput: "100",
		}},
	})
	assertKind(t, err, model.KindInvalid)

	// qty_dasar is BIGINT, so a fractional conversion cannot be stored — and
	// silently rounding it would put a quantity into kartu_stok nobody asked for.
	_, err = testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
		ActorID:    f.actor,
		Tanggal:    "2026-08-11",
		IDSupplier: f.supplier,
		IDRuang:    f.ruang,
		Detail: []model.PembelianDetailRequest{{
			IDProduct: f.product, IDSatuanInput: f.dus,
			QtyFaktur: "0.5", HargaSatuanInput: "100",
		}},
	})
	if err != nil {
		t.Errorf("0.5 DUS x 12 = 6 pcs seharusnya diterima: %v", err)
	}
}

// Stock follows the goods, the payable follows the paper. A short delivery posts
// what arrived, and the shortfall shows up as selisih_dasar and status_penerimaan.
func TestSelisihTidakMemblokirPostingDanTercatat(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	pembelian := draftSederhana(t, testApp, f, "100", ptr("95"), ptr("box kurang 5, sudah WA supplier"))

	if pembelian.StatusPenerimaan != "KURANG" {
		t.Errorf("status_penerimaan = %q, want KURANG", pembelian.StatusPenerimaan)
	}

	if got := pembelian.Detail[0].SelisihDasar; got != 5 {
		t.Errorf("selisih_dasar = %d, want 5", got)
	}

	// The payable follows the invoice quantity, not the delivered one.
	if pembelian.Subtotal != "1000000.00" {
		t.Errorf("subtotal = %q, want 1000000.00 (100 x 10.000)", pembelian.Subtotal)
	}

	posted := ajukanDanPosting(t, testApp, f, pembelian.ID)

	if posted.Status != "POSTED" {
		t.Fatalf("status = %q, want POSTED", posted.Status)
	}

	// 95 pcs at 10.000 — the value that entered stock is proportional to what
	// arrived, not the full invoice.
	stok, nilai := saldoStok(t, f.product, f.ruang)
	if stok != 95 {
		t.Errorf("stok_akhir = %d, want 95", stok)
	}

	if nilai != "950000.00" {
		t.Errorf("nilai_akhir = %q, want 950000.00", nilai)
	}
}

// A short delivery without an explanation is refused. That note is the only record
// of what was said to the supplier, and it is written while the box is open.
func TestSelisihWajibDiberiKeterangan(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	_, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
		ActorID:    f.actor,
		Tanggal:    "2026-08-11",
		IDSupplier: f.supplier,
		IDRuang:    f.ruang,
		Detail: []model.PembelianDetailRequest{{
			IDProduct: f.product, IDSatuanInput: f.pcs,
			QtyFaktur: "100", QtyDiterima: ptr("95"), HargaSatuanInput: "10000",
		}},
	})
	assertKind(t, err, model.KindInvalid)
}

// Over-delivery is refused. Goods that were never invoiced carry no value, so the
// proportional nilai_masuk would exceed the invoice and the moving average would be
// wrong from then on — permanently, since kartu_stok is append-only.
func TestQtyDiterimaTidakBolehMelebihiFaktur(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	_, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
		ActorID:    f.actor,
		Tanggal:    "2026-08-11",
		IDSupplier: f.supplier,
		IDRuang:    f.ruang,
		Detail: []model.PembelianDetailRequest{{
			IDProduct: f.product, IDSatuanInput: f.pcs,
			QtyFaktur: "100", QtyDiterima: ptr("105"), HargaSatuanInput: "10000",
			KeteranganSelisih: ptr("supplier kirim lebih"),
		}},
	})
	assertKind(t, err, model.KindInvalid)
}

// The state machine. Every transition needs the state before it, and a posted
// document never becomes editable again.
func TestAlurPersetujuan(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	pembelian := draftSederhana(t, testApp, f, "10", nil, nil)

	// Posting a DRAFT: the approval step has not happened.
	_, err := testApp.pembelian.Posting(ctx(), &model.PostingPembelianRequest{
		ID: pembelian.ID, ActorID: f.actor,
	})
	assertKind(t, err, model.KindConflict)

	diajukan, err := testApp.pembelian.Ajukan(ctx(), &model.AjukanPembelianRequest{
		ID: pembelian.ID, ActorID: f.actor,
	})
	if err != nil {
		t.Fatalf("ajukan: %v", err)
	}

	if diajukan.Status != "DIAJUKAN" || diajukan.DiajukanOleh == nil {
		t.Errorf("status = %q, diajukan_oleh = %v; want DIAJUKAN dengan pengaju terisi",
			diajukan.Status, diajukan.DiajukanOleh)
	}

	// A submitted document is closed to edits: that is the point of submitting it.
	_, err = testApp.pembelian.Update(ctx(), &model.UpdatePembelianRequest{
		ID: pembelian.ID, ActorID: f.actor,
		PPN: model.Optional[string]{Present: true, Value: ptr("1000")},
	})
	assertKind(t, err, model.KindConflict)

	ditolak, err := testApp.pembelian.Tolak(ctx(), &model.TolakPembelianRequest{
		ID: pembelian.ID, ActorID: f.actor, Alasan: "harga tidak sesuai kesepakatan WA",
	})
	if err != nil {
		t.Fatalf("tolak: %v", err)
	}

	if ditolak.Status != "DRAFT" || ditolak.AlasanTolak == nil {
		t.Errorf("status = %q, alasan_tolak = %v; want DRAFT dengan alasan terisi",
			ditolak.Status, ditolak.AlasanTolak)
	}

	// A rejected document is editable again, and can be resubmitted.
	posted := ajukanDanPosting(t, testApp, f, pembelian.ID)

	if posted.DisetujuiOleh == nil || posted.PostedAt == nil {
		t.Errorf("disetujui_oleh = %v, posted_at = %v; want keduanya terisi",
			posted.DisetujuiOleh, posted.PostedAt)
	}

	// Posted documents reject every edit, including a wholesale line replacement.
	_, err = testApp.pembelian.ReplaceDetail(ctx(), &model.ReplacePembelianDetailRequest{
		ID: pembelian.ID, ActorID: f.actor,
		Detail: []model.PembelianDetailRequest{{
			IDProduct: f.product, IDSatuanInput: f.pcs,
			QtyFaktur: "1", HargaSatuanInput: "1",
		}},
	})
	assertKind(t, err, model.KindConflict)

	// And posting twice is refused, so stock cannot be doubled.
	_, err = testApp.pembelian.Posting(ctx(), &model.PostingPembelianRequest{
		ID: pembelian.ID, ActorID: f.actor,
	})
	assertKind(t, err, model.KindConflict)
}

// Cancelling a posted document appends reversing rows rather than editing or
// deleting anything, and the stock balance returns to where it started.
func TestBatalMenulisBarisPembalik(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	pembelian := draftSederhana(t, testApp, f, "40", nil, nil)
	ajukanDanPosting(t, testApp, f, pembelian.ID)

	if stok, _ := saldoStok(t, f.product, f.ruang); stok != 40 {
		t.Fatalf("stok setelah posting = %d, want 40", stok)
	}

	batal, err := testApp.pembelian.Batal(ctx(), &model.BatalPembelianRequest{
		ID: pembelian.ID, ActorID: f.actor, AlasanBatal: "salah supplier",
	})
	if err != nil {
		t.Fatalf("batal: %v", err)
	}

	if batal.Status != "BATAL" || batal.AlasanBatal == nil {
		t.Errorf("status = %q, alasan_batal = %v; want BATAL dengan alasan terisi",
			batal.Status, batal.AlasanBatal)
	}

	stok, nilai := saldoStok(t, f.product, f.ruang)
	if stok != 0 {
		t.Errorf("stok setelah batal = %d, want 0", stok)
	}

	// Stock reaching zero forces the value to exactly zero, so rounding residue
	// cannot accumulate as rupiah in an empty warehouse.
	if nilai != "0.00" {
		t.Errorf("nilai setelah batal = %q, want 0.00", nilai)
	}

	// Nothing was removed: the original row and its reversal both survive, and the
	// reversal points back at what it undid.
	var baris, berpasangan int
	if err := testDB.QueryRow(`
		SELECT COUNT(*), COUNT(id_kartu_stok_asal)
		FROM kartu_stok WHERE ref_table = 'pembelian' AND ref_id_transaksi = $1
	`, pembelian.ID).Scan(&baris, &berpasangan); err != nil {
		t.Fatalf("hitung kartu_stok: %v", err)
	}

	if baris != 2 || berpasangan != 1 {
		t.Errorf("kartu_stok = %d baris dengan %d pembalik, want 2 dan 1", baris, berpasangan)
	}
}

// Freight is shared out by koli and has to add up exactly, or some of it would be
// allocated to nobody. bagi-rata-koli is what makes that reachable without a
// calculator.
func TestBagiRataKoliDanValidasiJumlahKoli(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	kedua, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID: f.actor, KodeBarang: "BRG-002", Nama: "Tinta", IDSatuanDasar: f.pcs,
	})
	if err != nil {
		t.Fatalf("create product kedua: %v", err)
	}

	pembelian, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
		ActorID:      f.actor,
		Tanggal:      "2026-08-11",
		IDSupplier:   f.supplier,
		IDRuang:      f.ruang,
		TotalKoli:    ptr("3"),
		TarifPerKoli: ptr("50000"),
		Detail: []model.PembelianDetailRequest{
			{IDProduct: f.product, IDSatuanInput: f.pcs, QtyFaktur: "10", HargaSatuanInput: "10000"},
			{IDProduct: kedua.ID, IDSatuanInput: f.pcs, QtyFaktur: "20", HargaSatuanInput: "10000"},
		},
	})
	if err != nil {
		t.Fatalf("create pembelian: %v", err)
	}

	if pembelian.BiayaAngkut != "150000.00" {
		t.Errorf("biaya_angkut = %q, want 150000.00 (3 x 50.000)", pembelian.BiayaAngkut)
	}

	// jumlah_koli is still zero on every line, so the totals disagree and posting
	// must refuse it.
	if _, err := testApp.pembelian.Ajukan(ctx(), &model.AjukanPembelianRequest{
		ID: pembelian.ID, ActorID: f.actor,
	}); err != nil {
		t.Fatalf("ajukan: %v", err)
	}

	_, err = testApp.pembelian.Posting(ctx(), &model.PostingPembelianRequest{
		ID: pembelian.ID, ActorID: f.actor,
	})
	assertKind(t, err, model.KindInvalid)

	// Back to DRAFT so the warehouse can fill the koli in.
	if _, err := testApp.pembelian.Tolak(ctx(), &model.TolakPembelianRequest{
		ID: pembelian.ID, ActorID: f.actor, Alasan: "koli belum diisi",
	}); err != nil {
		t.Fatalf("tolak: %v", err)
	}

	dibagi, err := testApp.pembelian.BagiRataKoli(ctx(), &model.BagiRataKoliRequest{
		ID: pembelian.ID, ActorID: f.actor,
	})
	if err != nil {
		t.Fatalf("bagi rata koli: %v", err)
	}

	// 10 and 20 base units split 3 koli as 1.00 and 2.00.
	if got := dibagi.Detail[0].JumlahKoli; got != "1.00" {
		t.Errorf("jumlah_koli[0] = %q, want 1.00", got)
	}

	if got := dibagi.Detail[1].JumlahKoli; got != "2.00" {
		t.Errorf("jumlah_koli[1] = %q, want 2.00", got)
	}

	posted := ajukanDanPosting(t, testApp, f, pembelian.ID)

	// Freight follows the koli: 50.000 and 100.000, summing to exactly 150.000.
	if got := posted.Detail[0].AlokasiBiaya; got != "50000.00" {
		t.Errorf("alokasi_biaya[0] = %q, want 50000.00", got)
	}

	if got := posted.Detail[1].AlokasiBiaya; got != "100000.00" {
		t.Errorf("alokasi_biaya[1] = %q, want 100000.00", got)
	}

	// Cost per unit carries the freight: (100.000 + 50.000) / 10.
	if got := posted.Detail[0].HargaPokokSatuanDasar; got == nil || *got != "15000.0000" {
		t.Errorf("harga_pokok_satuan_dasar[0] = %v, want 15000.0000", got)
	}
}

// Freight already inside the supplier's invoice must not be allocated again, or the
// same rupiah lands in cost of goods twice.
func TestDitanggungSupplierTidakDialokasikan(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	pembelian, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
		ActorID:            f.actor,
		Tanggal:            "2026-08-11",
		IDSupplier:         f.supplier,
		IDRuang:            f.ruang,
		TotalKoli:          ptr("2"),
		TarifPerKoli:       ptr("50000"),
		DitanggungSupplier: true,
		Detail: []model.PembelianDetailRequest{{
			IDProduct: f.product, IDSatuanInput: f.pcs,
			QtyFaktur: "10", HargaSatuanInput: "10000", JumlahKoli: "2",
		}},
	})
	if err != nil {
		t.Fatalf("create pembelian: %v", err)
	}

	if pembelian.BiayaAngkut != "0.00" {
		t.Errorf("biaya_angkut = %q, want 0.00", pembelian.BiayaAngkut)
	}

	posted := ajukanDanPosting(t, testApp, f, pembelian.ID)

	if got := posted.Detail[0].AlokasiBiaya; got != "0.00" {
		t.Errorf("alokasi_biaya = %q, want 0.00", got)
	}

	if got := posted.Detail[0].HargaPokokSatuanDasar; got == nil || *got != "10000.0000" {
		t.Errorf("harga_pokok_satuan_dasar = %v, want 10000.0000 (tanpa ongkir)", got)
	}
}

// Without a purchase order this document is the only trace of a supplier's invoice,
// so entering the same paper twice would raise stock twice. Uniqueness is per
// supplier and case-insensitive, and a cancelled document releases the number so a
// mistyped nota can be re-entered.
func TestNoFakturSupplierUnikPerSupplier(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	buat := func(faktur string, supplier int64) error {
		_, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
			ActorID:          f.actor,
			Tanggal:          "2026-08-11",
			IDSupplier:       supplier,
			IDRuang:          f.ruang,
			NoFakturSupplier: &faktur,
			Detail: []model.PembelianDetailRequest{{
				IDProduct: f.product, IDSatuanInput: f.pcs,
				QtyFaktur: "1", HargaSatuanInput: "10000",
			}},
		})

		return err
	}

	if err := buat("INV-2026-001", f.supplier); err != nil {
		t.Fatalf("faktur pertama: %v", err)
	}

	err := buat("inv-2026-001", f.supplier)
	assertKind(t, err, model.KindConflict)

	// Another supplier may well use the same numbering.
	lain, err := testApp.supplier.Create(ctx(), &model.CreateSupplierRequest{Nama: "CV Lain"})
	if err != nil {
		t.Fatalf("create supplier lain: %v", err)
	}

	if err := buat("INV-2026-001", lain.ID); err != nil {
		t.Errorf("faktur sama dari supplier lain ditolak: %v", err)
	}

	// A supplier's invoice number is optional, and any number of documents may have
	// none — the unique index is partial, so NULLs do not collide.
	for range 2 {
		if _, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
			ActorID: f.actor, Tanggal: "2026-08-11",
			IDSupplier: f.supplier, IDRuang: f.ruang,
			Detail: []model.PembelianDetailRequest{{
				IDProduct: f.product, IDSatuanInput: f.pcs,
				QtyFaktur: "1", HargaSatuanInput: "10000",
			}},
		}); err != nil {
			t.Fatalf("pembelian tanpa no faktur: %v", err)
		}
	}
}

// Two receipts of the same product shift the moving average, and posting a purchase
// is what feeds it. This is the first document type that writes kartu_stok, so the
// engine is proved here.
func TestPostingMenggeserRataRataBergerak(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	// 10 pcs at 10.000.
	pertama := draftSederhana(t, testApp, f, "10", nil, nil)
	ajukanDanPosting(t, testApp, f, pertama.ID)

	// 10 pcs at 20.000.
	kedua, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
		ActorID: f.actor, Tanggal: "2026-08-11",
		IDSupplier: f.supplier, IDRuang: f.ruang,
		Detail: []model.PembelianDetailRequest{{
			IDProduct: f.product, IDSatuanInput: f.pcs,
			QtyFaktur: "10", HargaSatuanInput: "20000",
		}},
	})
	if err != nil {
		t.Fatalf("create pembelian kedua: %v", err)
	}

	ajukanDanPosting(t, testApp, f, kedua.ID)

	var stok int64
	var hpp string
	if err := testDB.QueryRow(`
		SELECT stok_akhir, harga_pokok_satuan::TEXT
		FROM kartu_stok WHERE id_barang = $1 AND id_ruang = $2
		ORDER BY id DESC LIMIT 1
	`, f.product, f.ruang).Scan(&stok, &hpp); err != nil {
		t.Fatalf("baca saldo: %v", err)
	}

	if stok != 20 {
		t.Errorf("stok_akhir = %d, want 20", stok)
	}

	if hpp != "15000.0000" {
		t.Errorf("harga_pokok_satuan = %q, want 15000.0000 (rata-rata 10.000 dan 20.000)", hpp)
	}
}

// kartu_stok is append-only, and the guard is the database's, not a convention this
// code follows. Nothing in the application should ever be able to rewrite history.
func TestKartuStokMenolakPerubahan(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	pembelian := draftSederhana(t, testApp, f, "10", nil, nil)
	ajukanDanPosting(t, testApp, f, pembelian.ID)

	if _, err := testDB.Exec(`UPDATE kartu_stok SET stok_masuk = 999 WHERE ref_id_transaksi = $1`, pembelian.ID); err == nil {
		t.Error("UPDATE kartu_stok berhasil, want ditolak trigger")
	}

	if _, err := testDB.Exec(`DELETE FROM kartu_stok WHERE ref_id_transaksi = $1`, pembelian.ID); err == nil {
		t.Error("DELETE kartu_stok berhasil, want ditolak trigger")
	}
}

// The trigger refuses a posting dated inside a closed period, and a month with no
// periode row counts as open.
func TestPostingKePeriodeTutupDitolak(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	pembelian := draftSederhana(t, testApp, f, "10", nil, nil)

	if _, err := testDB.Exec(
		`INSERT INTO periode (tahun, bulan, status) VALUES (2026, 8, 'TUTUP')`,
	); err != nil {
		t.Fatalf("tutup periode: %v", err)
	}
	defer func() {
		if _, err := testDB.Exec(`DELETE FROM periode WHERE tahun = 2026 AND bulan = 8`); err != nil {
			t.Fatalf("buka periode: %v", err)
		}
	}()

	if _, err := testApp.pembelian.Ajukan(ctx(), &model.AjukanPembelianRequest{
		ID: pembelian.ID, ActorID: f.actor,
	}); err != nil {
		t.Fatalf("ajukan: %v", err)
	}

	_, err := testApp.pembelian.Posting(ctx(), &model.PostingPembelianRequest{
		ID: pembelian.ID, ActorID: f.actor,
	})
	assertKind(t, err, model.KindInvalid)

	// Nothing was written, and the document is still awaiting approval.
	if stok, _ := saldoStok(t, f.product, f.ruang); stok != 0 {
		t.Errorf("stok = %d setelah posting gagal, want 0", stok)
	}

	lagi, err := testApp.pembelian.Get(ctx(), &model.GetPembelianRequest{ID: pembelian.ID})
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if lagi.Status != "DIAJUKAN" {
		t.Errorf("status = %q setelah posting gagal, want DIAJUKAN", lagi.Status)
	}
}

// The list filters, and its pagination is stable: ORDER BY ends in a unique column,
// so a page boundary in the middle of same-day documents cannot repeat or skip one.
func TestListPembelianFilterDanPaginasi(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	for range 5 {
		draftSederhana(t, testApp, f, "10", nil, nil)
	}

	kurang := draftSederhana(t, testApp, f, "10", ptr("8"), ptr("kurang 2"))

	semua, paging, err := testApp.pembelian.Search(ctx(), &model.ListPembelianRequest{})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if paging.TotalItem != 6 {
		t.Errorf("total_item = %d, want 6", paging.TotalItem)
	}

	if len(semua) != 6 {
		t.Errorf("len(data) = %d, want 6", len(semua))
	}

	hanyaKurang, _, err := testApp.pembelian.Search(ctx(), &model.ListPembelianRequest{
		StatusPenerimaan: "KURANG",
	})
	if err != nil {
		t.Fatalf("search KURANG: %v", err)
	}

	if len(hanyaKurang) != 1 || hanyaKurang[0].ID != kurang.ID {
		t.Errorf("filter status_penerimaan mengembalikan %d baris, want 1 (id %d)",
			len(hanyaKurang), kurang.ID)
	}

	// Every document is dated 2026-08-11, so this is exactly the tie the unique
	// tiebreaker exists for: paging through must yield six distinct ids.
	terlihat := map[int64]bool{}
	for halaman := 1; halaman <= 3; halaman++ {
		batch, _, err := testApp.pembelian.Search(ctx(), &model.ListPembelianRequest{
			PageRequest: model.PageRequest{Page: halaman, Size: 2},
		})
		if err != nil {
			t.Fatalf("search halaman %d: %v", halaman, err)
		}

		for _, row := range batch {
			if terlihat[row.ID] {
				t.Errorf("id %d muncul di dua halaman", row.ID)
			}

			terlihat[row.ID] = true
		}
	}

	if len(terlihat) != 6 {
		t.Errorf("paginasi mengembalikan %d dokumen unik, want 6", len(terlihat))
	}

	// The date range is inclusive of both whole days.
	sehari, _, err := testApp.pembelian.Search(ctx(), &model.ListPembelianRequest{
		TanggalDari: ptr("2026-08-11"), TanggalSampai: ptr("2026-08-11"),
	})
	if err != nil {
		t.Fatalf("search rentang tanggal: %v", err)
	}

	if len(sehari) != 6 {
		t.Errorf("rentang satu hari mengembalikan %d, want 6", len(sehari))
	}
}

// ajukanDanPosting walks a draft all the way to POSTED.
func ajukanDanPosting(t *testing.T, testApp *app, f fixture, id int64) *model.PembelianResponse {
	t.Helper()

	if _, err := testApp.pembelian.Ajukan(ctx(), &model.AjukanPembelianRequest{
		ID: id, ActorID: f.actor,
	}); err != nil {
		t.Fatalf("ajukan: %v", err)
	}

	posted, err := testApp.pembelian.Posting(ctx(), &model.PostingPembelianRequest{
		ID: id, ActorID: f.actor,
	})
	if err != nil {
		t.Fatalf("posting: %v", err)
	}

	return posted
}

// saldoStok reads the running balance straight from the stock card, which is the
// only source of truth for it — no master table carries a stock column.
func saldoStok(t *testing.T, productID, ruangID int64) (int64, string) {
	t.Helper()

	var stok int64
	var nilai string

	err := testDB.QueryRow(`
		SELECT stok_akhir, nilai_akhir::TEXT
		FROM kartu_stok WHERE id_barang = $1 AND id_ruang = $2
		ORDER BY id DESC LIMIT 1
	`, productID, ruangID).Scan(&stok, &nilai)
	if err != nil {
		// No rows means nothing has ever moved for this pair, which is a balance of
		// zero rather than a failure.
		return 0, "0.00"
	}

	return stok, nilai
}
