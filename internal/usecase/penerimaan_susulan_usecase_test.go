package usecase_test

// The point of these is the arithmetic that spans two documents: a purchase and its
// follow-up receipts have to add up to exactly the invoice value, and the
// outstanding quantity has to be right after every combination of posting and
// voiding. All of that lives in the database — the moving average, the append-only
// trigger, the receipt cache — so it is exercised there.

import (
	"testing"

	"Arthafreestyle/ERP/internal/model"
)

// susulanFixture posts a purchase of 100 pcs at 10.000 with 50.000 of freight, of
// which only 95 arrived. That leaves 5 outstanding worth 10.500 each — the worked
// example from isu #4.
func susulanFixture(t *testing.T, testApp *app) (fixture, *model.PembelianResponse) {
	t.Helper()

	f := pembelianFixture(t, testApp)

	pembelian, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
		ActorID:      f.actor,
		Tanggal:      "2026-08-11",
		IDSupplier:   f.supplier,
		IDRuang:      f.ruang,
		TotalKoli:    ptr("1"),
		TarifPerKoli: ptr("50000"),
		Detail: []model.PembelianDetailRequest{{
			IDProduct: f.product, IDSatuanInput: f.pcs,
			QtyFaktur: "100", QtyDiterima: ptr("95"), HargaSatuanInput: "10000",
			JumlahKoli: "1", KeteranganSelisih: ptr("box kurang 5"),
		}},
	})
	if err != nil {
		t.Fatalf("create pembelian: %v", err)
	}

	return f, ajukanDanPosting(t, testApp, f, pembelian.ID)
}

// buatSusulan opens and posts a follow-up receipt for one line.
func buatSusulan(t *testing.T, testApp *app, f fixture, idPembelian, idPembelianDetail int64, qty string) *model.PenerimaanSusulanResponse {
	t.Helper()

	susulan, err := testApp.susulan.Create(ctx(), &model.CreatePenerimaanSusulanRequest{
		ActorID:     f.actor,
		IDPembelian: idPembelian,
		Tanggal:     "2026-08-20",
		Detail: []model.PenerimaanSusulanDetailRequest{{
			IDPembelianDetail: idPembelianDetail,
			IDSatuanInput:     f.pcs,
			QtyInput:          qty,
		}},
	})
	if err != nil {
		t.Fatalf("create penerimaan susulan: %v", err)
	}

	if _, err := testApp.susulan.Ajukan(ctx(), &model.AjukanPenerimaanSusulanRequest{
		ID: susulan.ID, ActorID: f.actor,
	}); err != nil {
		t.Fatalf("ajukan susulan: %v", err)
	}

	posted, err := testApp.susulan.Posting(ctx(), &model.PostingPenerimaanSusulanRequest{
		ID: susulan.ID, ActorID: f.actor,
	})
	if err != nil {
		t.Fatalf("posting susulan: %v", err)
	}

	return posted
}

// The whole reason this document exists. A purchase and its follow-up receipts must
// contribute exactly the invoice value to inventory — no more, because a second
// shipment does not make the goods cost more, and no less, because the remainder is
// still goods that were paid for.
func TestSusulanMelengkapiNilaiFakturPersis(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := susulanFixture(t, testApp)

	// After the first delivery: 95 at 10.500.
	stok, nilai := saldoStok(t, f.product, f.ruang)
	if stok != 95 || nilai != "997500.00" {
		t.Fatalf("setelah pembelian: stok %d nilai %s, want 95 dan 997500.00", stok, nilai)
	}

	susulan := buatSusulan(t, testApp, f, pembelian.ID, pembelian.Detail[0].ID, "5")

	if susulan.Status != "POSTED" {
		t.Fatalf("status = %q, want POSTED", susulan.Status)
	}

	// The cost is copied from the source line, not recomputed and not read off the
	// moving average: 5 x 10.500.
	baris := susulan.Detail[0]
	if baris.HargaPokokSatuanDasar != "10500.0000" {
		t.Errorf("harga_pokok_satuan_dasar = %q, want 10500.0000", baris.HargaPokokSatuanDasar)
	}

	if baris.Nilai != "52500.00" {
		t.Errorf("nilai = %q, want 52500.00", baris.Nilai)
	}

	if susulan.TotalNilai != "52500.00" {
		t.Errorf("total_nilai = %q, want 52500.00", susulan.TotalNilai)
	}

	// 997.500 + 52.500 = 1.050.000, exactly the invoice plus its freight. Not a
	// rupiah more.
	stok, nilai = saldoStok(t, f.product, f.ruang)
	if stok != 100 {
		t.Errorf("stok = %d, want 100", stok)
	}

	if nilai != "1050000.00" {
		t.Errorf("nilai persediaan = %q, want 1050000.00 (subtotal 1.000.000 + ongkir 50.000)", nilai)
	}

	// And the average never moved: every unit of this invoice cost the same.
	var hpp string
	if err := testDB.QueryRow(`
		SELECT harga_pokok_satuan::TEXT FROM kartu_stok
		WHERE id_barang = $1 AND id_ruang = $2 ORDER BY id DESC LIMIT 1
	`, f.product, f.ruang).Scan(&hpp); err != nil {
		t.Fatalf("baca hpp: %v", err)
	}

	if hpp != "10500.0000" {
		t.Errorf("harga_pokok_satuan = %q, want 10500.0000 (tidak bergeser)", hpp)
	}
}

// status_penerimaan is a cache, and a follow-up receipt is what changes the answer
// after the purchase is already POSTED and can no longer recompute it itself.
func TestSusulanMemperbaruiStatusPenerimaan(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := susulanFixture(t, testApp)

	if pembelian.StatusPenerimaan != "KURANG" {
		t.Fatalf("status_penerimaan awal = %q, want KURANG", pembelian.StatusPenerimaan)
	}

	// A partial follow-up leaves it KURANG.
	buatSusulan(t, testApp, f, pembelian.ID, pembelian.Detail[0].ID, "2")

	lagi, err := testApp.pembelian.Get(ctx(), &model.GetPembelianRequest{ID: pembelian.ID})
	if err != nil {
		t.Fatalf("get pembelian: %v", err)
	}

	if lagi.StatusPenerimaan != "KURANG" {
		t.Errorf("setelah susulan sebagian: status_penerimaan = %q, want KURANG", lagi.StatusPenerimaan)
	}

	if got := lagi.Detail[0]; got.QtySusulanDasar != 2 || got.SisaDasar != 3 {
		t.Errorf("qty_susulan_dasar = %d, sisa_dasar = %d; want 2 dan 3",
			got.QtySusulanDasar, got.SisaDasar)
	}

	// selisih_dasar is frozen at what the first delivery was short, whatever
	// arrives later.
	if lagi.Detail[0].SelisihDasar != 5 {
		t.Errorf("selisih_dasar = %d, want 5 (tidak berubah)", lagi.Detail[0].SelisihDasar)
	}

	// Completing it flips the cache.
	buatSusulan(t, testApp, f, pembelian.ID, pembelian.Detail[0].ID, "3")

	lagi, err = testApp.pembelian.Get(ctx(), &model.GetPembelianRequest{ID: pembelian.ID})
	if err != nil {
		t.Fatalf("get pembelian: %v", err)
	}

	if lagi.StatusPenerimaan != "LENGKAP" {
		t.Errorf("setelah lengkap: status_penerimaan = %q, want LENGKAP", lagi.StatusPenerimaan)
	}

	// And the line drops off the chase list entirely.
	sisa, err := testApp.pembelian.Sisa(ctx(), &model.GetPembelianRequest{ID: pembelian.ID})
	if err != nil {
		t.Fatalf("sisa: %v", err)
	}

	if len(sisa.Baris) != 0 {
		t.Errorf("sisa masih punya %d baris, want 0", len(sisa.Baris))
	}
}

// A follow-up receipt may never exceed what the supplier still owes. Two of them in
// sequence have to share one remainder, not each get the whole of it.
func TestSusulanTidakBolehMelebihiSisa(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := susulanFixture(t, testApp)

	idBaris := pembelian.Detail[0].ID

	buat := func(qty string) error {
		_, err := testApp.susulan.Create(ctx(), &model.CreatePenerimaanSusulanRequest{
			ActorID:     f.actor,
			IDPembelian: pembelian.ID,
			Tanggal:     "2026-08-20",
			Detail: []model.PenerimaanSusulanDetailRequest{{
				IDPembelianDetail: idBaris, IDSatuanInput: f.pcs, QtyInput: qty,
			}},
		})

		return err
	}

	// Sisa is 5.
	assertKind(t, buat("6"), model.KindInvalid)

	// Three arrive, leaving two.
	buatSusulan(t, testApp, f, pembelian.ID, idBaris, "3")

	assertKind(t, buat("3"), model.KindInvalid)

	if err := buat("2"); err != nil {
		t.Errorf("sisa 2 ditolak: %v", err)
	}

	// Once the line is complete, nothing more may be received against it.
	buatSusulan(t, testApp, f, pembelian.ID, idBaris, "2")

	assertKind(t, buat("1"), model.KindInvalid)
}

// The remainder check that counts runs at posting, under the purchase's row lock.
// The one at create time only produces a friendlier error sooner: two drafts can be
// written against the same remainder, and the second must fail when it tries to
// consume what the first already took.
func TestSisaDiperiksaUlangSaatPosting(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := susulanFixture(t, testApp)

	idBaris := pembelian.Detail[0].ID

	// Two drafts, each claiming 4 of the 5 outstanding. Both are legal as drafts —
	// a draft is not a delivery, so neither reduces the remainder.
	var draft [2]*model.PenerimaanSusulanResponse
	for i := range draft {
		d, err := testApp.susulan.Create(ctx(), &model.CreatePenerimaanSusulanRequest{
			ActorID:     f.actor,
			IDPembelian: pembelian.ID,
			Tanggal:     "2026-08-20",
			Detail: []model.PenerimaanSusulanDetailRequest{{
				IDPembelianDetail: idBaris, IDSatuanInput: f.pcs, QtyInput: "4",
			}},
		})
		if err != nil {
			t.Fatalf("draft %d: %v", i+1, err)
		}

		draft[i] = d

		if _, err := testApp.susulan.Ajukan(ctx(), &model.AjukanPenerimaanSusulanRequest{
			ID: d.ID, ActorID: f.actor,
		}); err != nil {
			t.Fatalf("ajukan draft %d: %v", i+1, err)
		}
	}

	if _, err := testApp.susulan.Posting(ctx(), &model.PostingPenerimaanSusulanRequest{
		ID: draft[0].ID, ActorID: f.actor,
	}); err != nil {
		t.Fatalf("posting draft pertama: %v", err)
	}

	// Only 1 is left, so the second cannot take its 4.
	_, err := testApp.susulan.Posting(ctx(), &model.PostingPenerimaanSusulanRequest{
		ID: draft[1].ID, ActorID: f.actor,
	})
	assertKind(t, err, model.KindInvalid)

	// Nothing was written for the failed posting.
	stok, _ := saldoStok(t, f.product, f.ruang)
	if stok != 99 {
		t.Errorf("stok = %d, want 99 (95 + 4)", stok)
	}
}

// A follow-up receipt needs a POSTED purchase: before that its lines carry no
// harga_pokok_satuan_dasar to copy, and no settled outstanding quantity.
func TestSusulanButuhPembelianPosted(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	draft := draftSederhana(t, testApp, f, "10", ptr("8"), ptr("kurang 2"))

	_, err := testApp.susulan.Create(ctx(), &model.CreatePenerimaanSusulanRequest{
		ActorID:     f.actor,
		IDPembelian: draft.ID,
		Tanggal:     "2026-08-20",
		Detail: []model.PenerimaanSusulanDetailRequest{{
			IDPembelianDetail: draft.Detail[0].ID, IDSatuanInput: f.pcs, QtyInput: "2",
		}},
	})
	assertKind(t, err, model.KindConflict)
}

// A line belonging to another purchase would let one document draw down a remainder
// it has no claim on.
func TestSusulanMenolakBarisDariPembelianLain(t *testing.T) {
	testApp := newApp(t)
	f, pertama := susulanFixture(t, testApp)

	kedua := draftSederhana(t, testApp, f, "10", ptr("7"), ptr("kurang 3"))
	ajukanDanPosting(t, testApp, f, kedua.ID)

	lengkap, err := testApp.pembelian.Get(ctx(), &model.GetPembelianRequest{ID: kedua.ID})
	if err != nil {
		t.Fatalf("get pembelian kedua: %v", err)
	}

	_, err = testApp.susulan.Create(ctx(), &model.CreatePenerimaanSusulanRequest{
		ActorID:     f.actor,
		IDPembelian: pertama.ID,
		Tanggal:     "2026-08-20",
		Detail: []model.PenerimaanSusulanDetailRequest{{
			IDPembelianDetail: lengkap.Detail[0].ID, IDSatuanInput: f.pcs, QtyInput: "1",
		}},
	})
	assertKind(t, err, model.KindInvalid)
}

// The same source line twice in one request would pass the remainder check twice and
// together exceed it. Caught in Go so the message names the mistake rather than an
// index.
func TestSusulanMenolakBarisSumberGanda(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := susulanFixture(t, testApp)

	idBaris := pembelian.Detail[0].ID

	_, err := testApp.susulan.Create(ctx(), &model.CreatePenerimaanSusulanRequest{
		ActorID:     f.actor,
		IDPembelian: pembelian.ID,
		Tanggal:     "2026-08-20",
		Detail: []model.PenerimaanSusulanDetailRequest{
			{IDPembelianDetail: idBaris, IDSatuanInput: f.pcs, QtyInput: "3"},
			{IDPembelianDetail: idBaris, IDSatuanInput: f.pcs, QtyInput: "3"},
		},
	})
	assertKind(t, err, model.KindInvalid)
}

// Quantities may be counted in a different unit than the invoice used — five loose
// pieces short of a line typed in boxes.
func TestSusulanBolehSatuanBerbeda(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	// 3 DUS of 12 = 36 pcs invoiced, 24 arrived. One DUS outstanding.
	pembelian, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
		ActorID: f.actor, Tanggal: "2026-08-11",
		IDSupplier: f.supplier, IDRuang: f.ruang,
		Detail: []model.PembelianDetailRequest{{
			IDProduct: f.product, IDSatuanInput: f.dus,
			QtyFaktur: "3", QtyDiterima: ptr("2"), HargaSatuanInput: "120000",
			KeteranganSelisih: ptr("1 dus menyusul"),
		}},
	})
	if err != nil {
		t.Fatalf("create pembelian: %v", err)
	}

	posted := ajukanDanPosting(t, testApp, f, pembelian.ID)

	if posted.Detail[0].SisaDasar != 12 {
		t.Fatalf("sisa_dasar = %d, want 12", posted.Detail[0].SisaDasar)
	}

	// The follow-up arrives as 12 loose pieces rather than a box.
	susulan := buatSusulan(t, testApp, f, pembelian.ID, posted.Detail[0].ID, "12")

	baris := susulan.Detail[0]
	if baris.FaktorKonversi != 1 || baris.QtyDasar != 12 {
		t.Errorf("faktor %d qty_dasar %d, want 1 dan 12", baris.FaktorKonversi, baris.QtyDasar)
	}

	// Cost per base unit still comes from the invoice line: 360.000 / 36.
	if baris.HargaPokokSatuanDasar != "10000.0000" {
		t.Errorf("harga_pokok_satuan_dasar = %q, want 10000.0000", baris.HargaPokokSatuanDasar)
	}

	lagi, err := testApp.pembelian.Get(ctx(), &model.GetPembelianRequest{ID: pembelian.ID})
	if err != nil {
		t.Fatalf("get pembelian: %v", err)
	}

	if lagi.StatusPenerimaan != "LENGKAP" {
		t.Errorf("status_penerimaan = %q, want LENGKAP", lagi.StatusPenerimaan)
	}
}

// Voiding a follow-up appends reversing rows and hands the quantity back.
func TestBatalSusulanMengembalikanSisa(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := susulanFixture(t, testApp)

	susulan := buatSusulan(t, testApp, f, pembelian.ID, pembelian.Detail[0].ID, "5")

	lagi, err := testApp.pembelian.Get(ctx(), &model.GetPembelianRequest{ID: pembelian.ID})
	if err != nil {
		t.Fatalf("get pembelian: %v", err)
	}

	if lagi.StatusPenerimaan != "LENGKAP" {
		t.Fatalf("status_penerimaan = %q, want LENGKAP", lagi.StatusPenerimaan)
	}

	batal, err := testApp.susulan.Batal(ctx(), &model.BatalPenerimaanSusulanRequest{
		ID: susulan.ID, ActorID: f.actor, AlasanBatal: "ternyata barang orang lain",
	})
	if err != nil {
		t.Fatalf("batal susulan: %v", err)
	}

	if batal.Status != "BATAL" {
		t.Errorf("status = %q, want BATAL", batal.Status)
	}

	stok, _ := saldoStok(t, f.product, f.ruang)
	if stok != 95 {
		t.Errorf("stok setelah batal = %d, want 95", stok)
	}

	// The outstanding quantity is owed again.
	lagi, err = testApp.pembelian.Get(ctx(), &model.GetPembelianRequest{ID: pembelian.ID})
	if err != nil {
		t.Fatalf("get pembelian: %v", err)
	}

	if lagi.StatusPenerimaan != "KURANG" {
		t.Errorf("status_penerimaan setelah batal = %q, want KURANG", lagi.StatusPenerimaan)
	}

	if got := lagi.Detail[0]; got.QtySusulanDasar != 0 || got.SisaDasar != 5 {
		t.Errorf("qty_susulan_dasar %d sisa_dasar %d, want 0 dan 5",
			got.QtySusulanDasar, got.SisaDasar)
	}

	// Nothing was removed: the original row and its reversal both survive.
	var baris, berpasangan int
	if err := testDB.QueryRow(`
		SELECT COUNT(*), COUNT(id_kartu_stok_asal)
		FROM kartu_stok WHERE ref_table = 'penerimaan_susulan' AND ref_id_transaksi = $1
	`, susulan.ID).Scan(&baris, &berpasangan); err != nil {
		t.Fatalf("hitung kartu_stok: %v", err)
	}

	if baris != 2 || berpasangan != 1 {
		t.Errorf("kartu_stok = %d baris dengan %d pembalik, want 2 dan 1", baris, berpasangan)
	}
}

// Cancelling a purchase reverses only the rows the purchase itself wrote, so a
// posted follow-up's stock would survive with nothing explaining it — and it could
// not be voided afterwards either, because its own cancellation path needs a
// purchase that is still POSTED. Void the follow-up first.
func TestPembelianTidakBisaDibatalkanSaatAdaSusulanPosted(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := susulanFixture(t, testApp)

	susulan := buatSusulan(t, testApp, f, pembelian.ID, pembelian.Detail[0].ID, "5")

	_, err := testApp.pembelian.Batal(ctx(), &model.BatalPembelianRequest{
		ID: pembelian.ID, ActorID: f.actor, AlasanBatal: "salah supplier",
	})
	assertKind(t, err, model.KindConflict)

	// Voiding the follow-up first clears the way.
	if _, err := testApp.susulan.Batal(ctx(), &model.BatalPenerimaanSusulanRequest{
		ID: susulan.ID, ActorID: f.actor, AlasanBatal: "ikut dibatalkan",
	}); err != nil {
		t.Fatalf("batal susulan: %v", err)
	}

	if _, err := testApp.pembelian.Batal(ctx(), &model.BatalPembelianRequest{
		ID: pembelian.ID, ActorID: f.actor, AlasanBatal: "salah supplier",
	}); err != nil {
		t.Fatalf("batal pembelian setelah susulan dibatalkan: %v", err)
	}

	stok, nilai := saldoStok(t, f.product, f.ruang)
	if stok != 0 {
		t.Errorf("stok = %d, want 0", stok)
	}

	if nilai != "0.00" {
		t.Errorf("nilai = %q, want 0.00", nilai)
	}
}

// The state machine, and the number series.
func TestAlurPersetujuanSusulan(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := susulanFixture(t, testApp)

	susulan, err := testApp.susulan.Create(ctx(), &model.CreatePenerimaanSusulanRequest{
		ActorID:     f.actor,
		IDPembelian: pembelian.ID,
		Tanggal:     "2026-08-20",
		Detail: []model.PenerimaanSusulanDetailRequest{{
			IDPembelianDetail: pembelian.Detail[0].ID, IDSatuanInput: f.pcs, QtyInput: "5",
		}},
	})
	if err != nil {
		t.Fatalf("create susulan: %v", err)
	}

	// Its own series, independent of the purchase's. FIX is pembelianFixture's
	// unit kode, inherited via the parent purchase's id_ruang (isu #21 fase 1).
	if susulan.Nomor != "PS/FIX/2026/08/0001" {
		t.Errorf("nomor = %q, want PS/FIX/2026/08/0001", susulan.Nomor)
	}

	if susulan.Status != "DRAFT" {
		t.Errorf("status = %q, want DRAFT", susulan.Status)
	}

	// Supplier and room are copied from the purchase, not chosen.
	if susulan.IDSupplier != f.supplier || susulan.IDRuang != f.ruang {
		t.Errorf("supplier %d ruang %d, want %d dan %d",
			susulan.IDSupplier, susulan.IDRuang, f.supplier, f.ruang)
	}

	// Posting a DRAFT: the approval step has not happened.
	_, err = testApp.susulan.Posting(ctx(), &model.PostingPenerimaanSusulanRequest{
		ID: susulan.ID, ActorID: f.actor,
	})
	assertKind(t, err, model.KindConflict)

	if _, err := testApp.susulan.Ajukan(ctx(), &model.AjukanPenerimaanSusulanRequest{
		ID: susulan.ID, ActorID: f.actor,
	}); err != nil {
		t.Fatalf("ajukan: %v", err)
	}

	// A submitted document is closed to edits.
	_, err = testApp.susulan.Update(ctx(), &model.UpdatePenerimaanSusulanRequest{
		ID: susulan.ID, ActorID: f.actor,
		Keterangan: model.Optional[string]{Present: true, Value: ptr("catatan")},
	})
	assertKind(t, err, model.KindConflict)

	ditolak, err := testApp.susulan.Tolak(ctx(), &model.TolakPenerimaanSusulanRequest{
		ID: susulan.ID, ActorID: f.actor, Alasan: "fotonya belum dilampirkan",
	})
	if err != nil {
		t.Fatalf("tolak: %v", err)
	}

	if ditolak.Status != "DRAFT" || ditolak.AlasanTolak == nil {
		t.Errorf("status %q alasan_tolak %v, want DRAFT dengan alasan terisi",
			ditolak.Status, ditolak.AlasanTolak)
	}

	if _, err := testApp.susulan.Ajukan(ctx(), &model.AjukanPenerimaanSusulanRequest{
		ID: susulan.ID, ActorID: f.actor,
	}); err != nil {
		t.Fatalf("ajukan ulang: %v", err)
	}

	posted, err := testApp.susulan.Posting(ctx(), &model.PostingPenerimaanSusulanRequest{
		ID: susulan.ID, ActorID: f.actor,
	})
	if err != nil {
		t.Fatalf("posting: %v", err)
	}

	if posted.DisetujuiOleh == nil || posted.PostedAt == nil {
		t.Errorf("disetujui_oleh %v posted_at %v, want keduanya terisi",
			posted.DisetujuiOleh, posted.PostedAt)
	}

	// Posting twice would double the stock.
	_, err = testApp.susulan.Posting(ctx(), &model.PostingPenerimaanSusulanRequest{
		ID: susulan.ID, ActorID: f.actor,
	})
	assertKind(t, err, model.KindConflict)

	// And a posted document rejects a line replacement.
	_, err = testApp.susulan.ReplaceDetail(ctx(), &model.ReplacePenerimaanSusulanDetailRequest{
		ID: susulan.ID, ActorID: f.actor,
		Detail: []model.PenerimaanSusulanDetailRequest{{
			IDPembelianDetail: pembelian.Detail[0].ID, IDSatuanInput: f.pcs, QtyInput: "1",
		}},
	})
	assertKind(t, err, model.KindConflict)
}

// kartu_stok rows from a follow-up carry their own jenis_transaksi, so a stock
// movement report can tell goods that arrived on time from goods that turned up
// late without joining to the document tables.
func TestSusulanMenulisJenisTransaksiSendiri(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := susulanFixture(t, testApp)

	buatSusulan(t, testApp, f, pembelian.ID, pembelian.Detail[0].ID, "5")

	rows, err := testDB.Query(`
		SELECT jenis_transaksi::TEXT, ref_table, stok_masuk
		FROM kartu_stok WHERE id_barang = $1 ORDER BY id
	`, f.product)
	if err != nil {
		t.Fatalf("baca kartu_stok: %v", err)
	}
	defer rows.Close()

	type gerak struct {
		jenis, ref string
		masuk      int64
	}

	var hasil []gerak
	for rows.Next() {
		var g gerak
		if err := rows.Scan(&g.jenis, &g.ref, &g.masuk); err != nil {
			t.Fatalf("scan: %v", err)
		}

		hasil = append(hasil, g)
	}

	if err := rows.Err(); err != nil {
		t.Fatalf("iterate kartu_stok: %v", err)
	}

	want := []gerak{
		{"PEMBELIAN", "pembelian", 95},
		{"PENERIMAAN_SUSULAN", "penerimaan_susulan", 5},
	}

	if len(hasil) != len(want) {
		t.Fatalf("kartu_stok = %d baris, want %d", len(hasil), len(want))
	}

	for i := range want {
		if hasil[i] != want[i] {
			t.Errorf("baris %d = %+v, want %+v", i, hasil[i], want[i])
		}
	}
}

// The list filters and pages, and its ordering ends in a unique column.
func TestListSusulanFilterDanPaginasi(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := susulanFixture(t, testApp)

	idBaris := pembelian.Detail[0].ID

	// Five drafts of one piece each, all dated the same day.
	for range 5 {
		if _, err := testApp.susulan.Create(ctx(), &model.CreatePenerimaanSusulanRequest{
			ActorID:     f.actor,
			IDPembelian: pembelian.ID,
			Tanggal:     "2026-08-20",
			Detail: []model.PenerimaanSusulanDetailRequest{{
				IDPembelianDetail: idBaris, IDSatuanInput: f.pcs, QtyInput: "1",
			}},
		}); err != nil {
			t.Fatalf("create susulan: %v", err)
		}
	}

	_, paging, err := testApp.susulan.Search(ctx(), &model.ListPenerimaanSusulanRequest{})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if paging.TotalItem != 5 {
		t.Errorf("total_item = %d, want 5", paging.TotalItem)
	}

	draftSaja, _, err := testApp.susulan.Search(ctx(), &model.ListPenerimaanSusulanRequest{
		Status: "POSTED",
	})
	if err != nil {
		t.Fatalf("search POSTED: %v", err)
	}

	if len(draftSaja) != 0 {
		t.Errorf("filter POSTED mengembalikan %d, want 0", len(draftSaja))
	}

	// Same-day documents are exactly the tie the unique tiebreaker exists for.
	terlihat := map[int64]bool{}
	for halaman := 1; halaman <= 3; halaman++ {
		batch, _, err := testApp.susulan.Search(ctx(), &model.ListPenerimaanSusulanRequest{
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

	if len(terlihat) != 5 {
		t.Errorf("paginasi mengembalikan %d dokumen unik, want 5", len(terlihat))
	}
}
