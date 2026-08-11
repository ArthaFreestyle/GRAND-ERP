package usecase_test

// The point of these is what spans two documents and one stock ledger: a return may
// only send back what actually arrived, a purchase and its return have to cancel out in
// inventory value, and the interaction with a follow-up receipt has to be right in both
// directions. All of it lives in the database — the moving average, the append-only
// trigger, the negative-stock guard — so it is exercised there.

import (
	"testing"

	"Arthafreestyle/ERP/internal/model"
)

// returFixture posts a purchase of 100 pcs at 10.000 all delivered, so the whole line
// is available to send back. No freight, so the cost per base unit is a round 10.000
// and the arithmetic below is readable.
func returFixture(t *testing.T, testApp *app) (fixture, *model.PembelianResponse) {
	t.Helper()

	f := pembelianFixture(t, testApp)

	pembelian := draftSederhana(t, testApp, f, "100", nil, nil)

	return f, ajukanDanPosting(t, testApp, f, pembelian.ID)
}

// buatRetur opens and posts a return for one line.
func buatRetur(t *testing.T, testApp *app, f fixture, idPembelian, idPembelianDetail int64, qty string) *model.ReturPembelianResponse {
	t.Helper()

	retur, err := testApp.retur.Create(ctx(), &model.CreateReturPembelianRequest{
		ActorID:     f.actor,
		IDPembelian: idPembelian,
		Tanggal:     "2026-08-15",
		Alasan:      "barang rusak saat diterima",
		Detail: []model.ReturPembelianDetailRequest{{
			IDPembelianDetail: idPembelianDetail,
			IDSatuanInput:     f.pcs,
			QtyInput:          qty,
		}},
	})
	if err != nil {
		t.Fatalf("create retur pembelian: %v", err)
	}

	if _, err := testApp.retur.Ajukan(ctx(), &model.AjukanReturPembelianRequest{
		ID: retur.ID, ActorID: f.actor,
	}); err != nil {
		t.Fatalf("ajukan retur: %v", err)
	}

	posted, err := testApp.retur.Posting(ctx(), &model.PostingReturPembelianRequest{
		ID: retur.ID, ActorID: f.actor,
	})
	if err != nil {
		t.Fatalf("posting retur: %v", err)
	}

	return posted
}

// The whole reason migration 000005 has harga_pokok_satuan_dasar on this table: a
// purchase and its return have to cancel out exactly. Return everything and both the
// quantity and the inventory value come back to zero, with nothing deleted.
func TestReturMenghapusPembelianDenganTepat(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := returFixture(t, testApp)

	stok, nilai := saldoStok(t, f.product, f.ruang)
	if stok != 100 || nilai != "1000000.00" {
		t.Fatalf("setelah pembelian: stok %d nilai %s, want 100 dan 1000000.00", stok, nilai)
	}

	retur := buatRetur(t, testApp, f, pembelian.ID, pembelian.Detail[0].ID, "100")

	if retur.Status != "POSTED" {
		t.Fatalf("status = %q, want POSTED", retur.Status)
	}

	// The cost is copied from the source line, not read off the moving average.
	baris := retur.Detail[0]
	if baris.HargaPokokSatuanDasar != "10000.0000" {
		t.Errorf("harga_pokok_satuan_dasar = %q, want 10000.0000", baris.HargaPokokSatuanDasar)
	}

	if baris.Nilai != "1000000.00" {
		t.Errorf("nilai = %q, want 1000000.00", baris.Nilai)
	}

	if retur.Total != "1000000.00" {
		t.Errorf("total = %q, want 1000000.00", retur.Total)
	}

	stok, nilai = saldoStok(t, f.product, f.ruang)
	if stok != 0 {
		t.Errorf("stok = %d, want 0", stok)
	}

	if nilai != "0.00" {
		t.Errorf("nilai persediaan = %q, want 0.00", nilai)
	}
}

// A return may only send back what physically arrived — not what the invoice billed.
// Goods that never turned up are chased with a follow-up receipt, not returned.
func TestReturTerbatasPadaYangBenarBenarDatang(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	// 100 invoiced, 95 arrived.
	draft := draftSederhana(t, testApp, f, "100", ptr("95"), ptr("box kurang 5"))
	pembelian := ajukanDanPosting(t, testApp, f, draft.ID)

	if got := pembelian.Detail[0]; got.QtyDapatDiretur != 95 || got.SisaDasar != 5 {
		t.Fatalf("qty_dapat_diretur %d sisa_dasar %d, want 95 dan 5",
			got.QtyDapatDiretur, got.SisaDasar)
	}

	buat := func(qty string) error {
		_, err := testApp.retur.Create(ctx(), &model.CreateReturPembelianRequest{
			ActorID:     f.actor,
			IDPembelian: pembelian.ID,
			Tanggal:     "2026-08-15",
			Alasan:      "rusak",
			Detail: []model.ReturPembelianDetailRequest{{
				IDPembelianDetail: pembelian.Detail[0].ID, IDSatuanInput: f.pcs, QtyInput: qty,
			}},
		})

		return err
	}

	// The invoice quantity is not the ceiling; what arrived is.
	assertKind(t, buat("100"), model.KindInvalid)
	assertKind(t, buat("96"), model.KindInvalid)

	if err := buat("95"); err != nil {
		t.Errorf("retur 95 dari 95 yang datang ditolak: %v", err)
	}
}

// Returning goods does not reopen what the supplier still owes, and it does not make a
// complete delivery incomplete. The two quotas share a source line and nothing else.
func TestReturTidakMengubahStatusPenerimaan(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := returFixture(t, testApp)

	if pembelian.StatusPenerimaan != "LENGKAP" {
		t.Fatalf("status_penerimaan awal = %q, want LENGKAP", pembelian.StatusPenerimaan)
	}

	buatRetur(t, testApp, f, pembelian.ID, pembelian.Detail[0].ID, "40")

	lagi, err := testApp.pembelian.Get(ctx(), &model.GetPembelianRequest{ID: pembelian.ID})
	if err != nil {
		t.Fatalf("get pembelian: %v", err)
	}

	if lagi.StatusPenerimaan != "LENGKAP" {
		t.Errorf("status_penerimaan = %q, want LENGKAP (retur bukan kekurangan kiriman)",
			lagi.StatusPenerimaan)
	}

	baris := lagi.Detail[0]
	if baris.QtyReturDasar != 40 {
		t.Errorf("qty_retur_dasar = %d, want 40", baris.QtyReturDasar)
	}

	if baris.QtyDapatDiretur != 60 {
		t.Errorf("qty_dapat_diretur = %d, want 60", baris.QtyDapatDiretur)
	}

	// The invoice was fully delivered, so nothing is outstanding however much went back.
	if baris.SisaDasar != 0 {
		t.Errorf("sisa_dasar = %d, want 0", baris.SisaDasar)
	}

	sisa, err := testApp.pembelian.Sisa(ctx(), &model.GetPembelianRequest{ID: pembelian.ID})
	if err != nil {
		t.Fatalf("sisa: %v", err)
	}

	if len(sisa.Baris) != 0 {
		t.Errorf("sisa punya %d baris, want 0", len(sisa.Baris))
	}
}

// Goods that turned up on a follow-up receipt are returnable too: they arrived, whatever
// document carried them in.
func TestReturBolehAtasBarangDariSusulan(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := susulanFixture(t, testApp)

	idBaris := pembelian.Detail[0].ID

	// 95 arrived first, 5 later. All 100 may go back.
	buatSusulan(t, testApp, f, pembelian.ID, idBaris, "5")

	lagi, err := testApp.pembelian.Get(ctx(), &model.GetPembelianRequest{ID: pembelian.ID})
	if err != nil {
		t.Fatalf("get pembelian: %v", err)
	}

	if lagi.Detail[0].QtyDapatDiretur != 100 {
		t.Fatalf("qty_dapat_diretur = %d, want 100", lagi.Detail[0].QtyDapatDiretur)
	}

	buatRetur(t, testApp, f, pembelian.ID, idBaris, "100")

	stok, nilai := saldoStok(t, f.product, f.ruang)
	if stok != 0 || nilai != "0.00" {
		t.Errorf("stok %d nilai %s, want 0 dan 0.00", stok, nilai)
	}
}

// Two returns in sequence share one returnable quantity rather than each getting the
// whole of it.
func TestReturBerturutMembagiKuota(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := returFixture(t, testApp)

	idBaris := pembelian.Detail[0].ID

	buatRetur(t, testApp, f, pembelian.ID, idBaris, "60")

	buat := func(qty string) error {
		_, err := testApp.retur.Create(ctx(), &model.CreateReturPembelianRequest{
			ActorID:     f.actor,
			IDPembelian: pembelian.ID,
			Tanggal:     "2026-08-16",
			Alasan:      "rusak juga",
			Detail: []model.ReturPembelianDetailRequest{{
				IDPembelianDetail: idBaris, IDSatuanInput: f.pcs, QtyInput: qty,
			}},
		})

		return err
	}

	assertKind(t, buat("41"), model.KindInvalid)

	if err := buat("40"); err != nil {
		t.Errorf("retur 40 dari 40 yang tersisa ditolak: %v", err)
	}

	// Nothing left once the line is fully returned.
	buatRetur(t, testApp, f, pembelian.ID, idBaris, "40")

	assertKind(t, buat("1"), model.KindInvalid)
}

// The quota check that counts runs at posting, under the purchase's row lock. The one at
// create time only produces a friendlier error sooner: two drafts can be written against
// the same goods, and the second must fail when it tries to take what the first already
// took.
func TestKuotaReturDiperiksaUlangSaatPosting(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := returFixture(t, testApp)

	idBaris := pembelian.Detail[0].ID

	// Two drafts, each claiming 80 of the 100 on the shelf. Both are legal as drafts — a
	// draft is a packing list, not a shipment, so neither reduces the quota.
	var draft [2]*model.ReturPembelianResponse
	for i := range draft {
		d, err := testApp.retur.Create(ctx(), &model.CreateReturPembelianRequest{
			ActorID:     f.actor,
			IDPembelian: pembelian.ID,
			Tanggal:     "2026-08-15",
			Alasan:      "rusak",
			Detail: []model.ReturPembelianDetailRequest{{
				IDPembelianDetail: idBaris, IDSatuanInput: f.pcs, QtyInput: "80",
			}},
		})
		if err != nil {
			t.Fatalf("draft %d: %v", i+1, err)
		}

		draft[i] = d

		if _, err := testApp.retur.Ajukan(ctx(), &model.AjukanReturPembelianRequest{
			ID: d.ID, ActorID: f.actor,
		}); err != nil {
			t.Fatalf("ajukan draft %d: %v", i+1, err)
		}
	}

	if _, err := testApp.retur.Posting(ctx(), &model.PostingReturPembelianRequest{
		ID: draft[0].ID, ActorID: f.actor,
	}); err != nil {
		t.Fatalf("posting draft pertama: %v", err)
	}

	// Only 20 are left, so the second cannot take its 80.
	_, err := testApp.retur.Posting(ctx(), &model.PostingReturPembelianRequest{
		ID: draft[1].ID, ActorID: f.actor,
	})
	assertKind(t, err, model.KindInvalid)

	// Nothing was written for the failed posting.
	stok, _ := saldoStok(t, f.product, f.ruang)
	if stok != 20 {
		t.Errorf("stok = %d, want 20 (100 - 80)", stok)
	}
}

// A return needs a POSTED purchase: before that its lines carry no
// harga_pokok_satuan_dasar to copy, and nothing has arrived to send back.
func TestReturButuhPembelianPosted(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	draft := draftSederhana(t, testApp, f, "10", nil, nil)

	_, err := testApp.retur.Create(ctx(), &model.CreateReturPembelianRequest{
		ActorID:     f.actor,
		IDPembelian: draft.ID,
		Tanggal:     "2026-08-15",
		Alasan:      "rusak",
		Detail: []model.ReturPembelianDetailRequest{{
			IDPembelianDetail: draft.Detail[0].ID, IDSatuanInput: f.pcs, QtyInput: "2",
		}},
	})
	assertKind(t, err, model.KindConflict)
}

// A line belonging to another purchase would let one document draw down a quantity it has
// no claim on, and the header's supplier and room would not match it.
func TestReturMenolakBarisDariPembelianLain(t *testing.T) {
	testApp := newApp(t)
	f, pertama := returFixture(t, testApp)

	kedua := draftSederhana(t, testApp, f, "10", nil, nil)
	lengkap := ajukanDanPosting(t, testApp, f, kedua.ID)

	_, err := testApp.retur.Create(ctx(), &model.CreateReturPembelianRequest{
		ActorID:     f.actor,
		IDPembelian: pertama.ID,
		Tanggal:     "2026-08-15",
		Alasan:      "rusak",
		Detail: []model.ReturPembelianDetailRequest{{
			IDPembelianDetail: lengkap.Detail[0].ID, IDSatuanInput: f.pcs, QtyInput: "1",
		}},
	})
	assertKind(t, err, model.KindInvalid)
}

// The same source line twice in one request would pass the quota check twice and together
// exceed it. Caught in Go so the message names the mistake rather than an index.
func TestReturMenolakBarisSumberGanda(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := returFixture(t, testApp)

	idBaris := pembelian.Detail[0].ID

	_, err := testApp.retur.Create(ctx(), &model.CreateReturPembelianRequest{
		ActorID:     f.actor,
		IDPembelian: pembelian.ID,
		Tanggal:     "2026-08-15",
		Alasan:      "rusak",
		Detail: []model.ReturPembelianDetailRequest{
			{IDPembelianDetail: idBaris, IDSatuanInput: f.pcs, QtyInput: "60"},
			{IDPembelianDetail: idBaris, IDSatuanInput: f.pcs, QtyInput: "60"},
		},
	})
	assertKind(t, err, model.KindInvalid)
}

// Quantities may be counted in a different unit than the invoice used — a whole box out
// of a line typed in pieces.
func TestReturBolehSatuanBerbeda(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	// 36 pcs invoiced and delivered, priced per piece.
	pembelian, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
		ActorID: f.actor, Tanggal: "2026-08-11",
		IDSupplier: f.supplier, IDRuang: f.ruang,
		Detail: []model.PembelianDetailRequest{{
			IDProduct: f.product, IDSatuanInput: f.pcs,
			QtyFaktur: "36", HargaSatuanInput: "10000",
		}},
	})
	if err != nil {
		t.Fatalf("create pembelian: %v", err)
	}

	posted := ajukanDanPosting(t, testApp, f, pembelian.ID)

	// One DUS of 12 goes back.
	retur, err := testApp.retur.Create(ctx(), &model.CreateReturPembelianRequest{
		ActorID:     f.actor,
		IDPembelian: posted.ID,
		Tanggal:     "2026-08-15",
		Alasan:      "satu dus penyok",
		Detail: []model.ReturPembelianDetailRequest{{
			IDPembelianDetail: posted.Detail[0].ID, IDSatuanInput: f.dus, QtyInput: "1",
		}},
	})
	if err != nil {
		t.Fatalf("create retur: %v", err)
	}

	baris := retur.Detail[0]
	if baris.FaktorKonversi != 12 || baris.QtyDasar != 12 {
		t.Errorf("faktor %d qty_dasar %d, want 12 dan 12", baris.FaktorKonversi, baris.QtyDasar)
	}

	// Cost per base unit still comes from the invoice line, which was typed per piece.
	if baris.HargaPokokSatuanDasar != "10000.0000" {
		t.Errorf("harga_pokok_satuan_dasar = %q, want 10000.0000", baris.HargaPokokSatuanDasar)
	}

	if baris.Nilai != "120000.00" {
		t.Errorf("nilai = %q, want 120000.00", baris.Nilai)
	}
}

// Voiding a posted return appends reversing rows: the goods come back into stock, and the
// quantity becomes returnable again.
func TestBatalReturMengembalikanBarang(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := returFixture(t, testApp)

	retur := buatRetur(t, testApp, f, pembelian.ID, pembelian.Detail[0].ID, "100")

	stok, _ := saldoStok(t, f.product, f.ruang)
	if stok != 0 {
		t.Fatalf("stok setelah retur = %d, want 0", stok)
	}

	batal, err := testApp.retur.Batal(ctx(), &model.BatalReturPembelianRequest{
		ID: retur.ID, ActorID: f.actor, AlasanBatal: "supplier menolak returnya",
	})
	if err != nil {
		t.Fatalf("batal retur: %v", err)
	}

	if batal.Status != "BATAL" {
		t.Errorf("status = %q, want BATAL", batal.Status)
	}

	stok, nilai := saldoStok(t, f.product, f.ruang)
	if stok != 100 {
		t.Errorf("stok setelah batal = %d, want 100", stok)
	}

	if nilai != "1000000.00" {
		t.Errorf("nilai setelah batal = %q, want 1000000.00", nilai)
	}

	lagi, err := testApp.pembelian.Get(ctx(), &model.GetPembelianRequest{ID: pembelian.ID})
	if err != nil {
		t.Fatalf("get pembelian: %v", err)
	}

	if got := lagi.Detail[0]; got.QtyReturDasar != 0 || got.QtyDapatDiretur != 100 {
		t.Errorf("qty_retur_dasar %d qty_dapat_diretur %d, want 0 dan 100",
			got.QtyReturDasar, got.QtyDapatDiretur)
	}

	// Nothing was removed: the original row and its reversal both survive.
	var baris, berpasangan int
	if err := testDB.QueryRow(`
		SELECT COUNT(*), COUNT(id_kartu_stok_asal)
		FROM kartu_stok WHERE ref_table = 'retur_pembelian' AND ref_id_transaksi = $1
	`, retur.ID).Scan(&baris, &berpasangan); err != nil {
		t.Fatalf("hitung kartu_stok: %v", err)
	}

	if baris != 2 || berpasangan != 1 {
		t.Errorf("kartu_stok = %d baris dengan %d pembalik, want 2 dan 1", baris, berpasangan)
	}
}

// Cancelling a purchase reverses the full received quantity, but a posted return has
// already taken part of it out — so the reversal would drive the balance negative, and
// even where it would not, the return would be left pointing at a BATAL purchase that
// already accounted for those goods. Void the return first.
func TestPembelianTidakBisaDibatalkanSaatAdaReturPosted(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := returFixture(t, testApp)

	retur := buatRetur(t, testApp, f, pembelian.ID, pembelian.Detail[0].ID, "30")

	_, err := testApp.pembelian.Batal(ctx(), &model.BatalPembelianRequest{
		ID: pembelian.ID, ActorID: f.actor, AlasanBatal: "salah supplier",
	})
	assertKind(t, err, model.KindConflict)

	// Voiding the return first clears the way.
	if _, err := testApp.retur.Batal(ctx(), &model.BatalReturPembelianRequest{
		ID: retur.ID, ActorID: f.actor, AlasanBatal: "ikut dibatalkan",
	}); err != nil {
		t.Fatalf("batal retur: %v", err)
	}

	if _, err := testApp.pembelian.Batal(ctx(), &model.BatalPembelianRequest{
		ID: pembelian.ID, ActorID: f.actor, AlasanBatal: "salah supplier",
	}); err != nil {
		t.Fatalf("batal pembelian setelah retur dibatalkan: %v", err)
	}

	stok, nilai := saldoStok(t, f.product, f.ruang)
	if stok != 0 {
		t.Errorf("stok = %d, want 0", stok)
	}

	if nilai != "0.00" {
		t.Errorf("nilai = %q, want 0.00", nilai)
	}
}

// Voiding a follow-up receipt whose goods have already been returned is refused by the
// ledger, and that is the right arbiter for it.
//
// This is the reachable half of the negative-stock guard. A return's own posting cannot
// currently drive a balance below zero: what a line may send back is exactly what it
// brought in, so the quota can never exceed the room's stock while purchase, follow-up
// receipt, and return are the only documents that move it. That changes the moment
// penjualan, mutasi, or pemakaian exist — which is why the guard is wired up now rather
// than left to be discovered then.
//
// Undoing a receipt, though, takes goods out against a quantity that a return has already
// spent, and no check in Go can decide it: the balance is computed inside the trigger
// under an advisory lock, precisely so no reader can decide it first. It arrives as a
// check violation and becomes a 400.
func TestBatalSusulanDitolakSaatBarangnyaSudahDiretur(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := susulanFixture(t, testApp)

	idBaris := pembelian.Detail[0].ID

	// 95 arrived, 5 followed. All 100 on the shelf, and all 100 returnable.
	susulan := buatSusulan(t, testApp, f, pembelian.ID, idBaris, "5")

	buatRetur(t, testApp, f, pembelian.ID, idBaris, "100")

	stok, _ := saldoStok(t, f.product, f.ruang)
	if stok != 0 {
		t.Fatalf("stok setelah retur = %d, want 0", stok)
	}

	// The reversal would need 5 pieces that are no longer in the room.
	_, err := testApp.susulan.Batal(ctx(), &model.BatalPenerimaanSusulanRequest{
		ID: susulan.ID, ActorID: f.actor, AlasanBatal: "ternyata barang orang lain",
	})
	assertKind(t, err, model.KindInvalid)

	// And nothing was written for the refused void: the follow-up is still POSTED with
	// only its own incoming row.
	var baris int
	if err := testDB.QueryRow(`
		SELECT COUNT(*) FROM kartu_stok
		WHERE ref_table = 'penerimaan_susulan' AND ref_id_transaksi = $1
	`, susulan.ID).Scan(&baris); err != nil {
		t.Fatalf("hitung kartu_stok: %v", err)
	}

	if baris != 1 {
		t.Errorf("kartu_stok susulan = %d baris, want 1", baris)
	}

	stok, _ = saldoStok(t, f.product, f.ruang)
	if stok != 0 {
		t.Errorf("stok = %d, want 0 (tidak berubah)", stok)
	}
}

// The state machine, and the number series.
func TestAlurPersetujuanRetur(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := returFixture(t, testApp)

	retur, err := testApp.retur.Create(ctx(), &model.CreateReturPembelianRequest{
		ActorID:     f.actor,
		IDPembelian: pembelian.ID,
		Tanggal:     "2026-08-15",
		Alasan:      "sepuluh lembar tergores",
		Detail: []model.ReturPembelianDetailRequest{{
			IDPembelianDetail: pembelian.Detail[0].ID, IDSatuanInput: f.pcs, QtyInput: "10",
		}},
	})
	if err != nil {
		t.Fatalf("create retur: %v", err)
	}

	// Its own series, independent of the purchase's and the follow-up receipt's.
	if retur.Nomor != "RB/2026/08/0001" {
		t.Errorf("nomor = %q, want RB/2026/08/0001", retur.Nomor)
	}

	if retur.Status != "DRAFT" {
		t.Errorf("status = %q, want DRAFT", retur.Status)
	}

	// Supplier and room are copied from the purchase, not chosen.
	if retur.IDSupplier != f.supplier || retur.IDRuang != f.ruang {
		t.Errorf("supplier %d ruang %d, want %d dan %d",
			retur.IDSupplier, retur.IDRuang, f.supplier, f.ruang)
	}

	// Posting a DRAFT: the approval step has not happened.
	_, err = testApp.retur.Posting(ctx(), &model.PostingReturPembelianRequest{
		ID: retur.ID, ActorID: f.actor,
	})
	assertKind(t, err, model.KindConflict)

	if _, err := testApp.retur.Ajukan(ctx(), &model.AjukanReturPembelianRequest{
		ID: retur.ID, ActorID: f.actor,
	}); err != nil {
		t.Fatalf("ajukan: %v", err)
	}

	// A submitted document is closed to edits.
	_, err = testApp.retur.Update(ctx(), &model.UpdateReturPembelianRequest{
		ID: retur.ID, ActorID: f.actor,
		Alasan: model.Optional[string]{Present: true, Value: ptr("alasan lain")},
	})
	assertKind(t, err, model.KindConflict)

	ditolak, err := testApp.retur.Tolak(ctx(), &model.TolakReturPembelianRequest{
		ID: retur.ID, ActorID: f.actor, Alasan: "fotonya belum dilampirkan",
	})
	if err != nil {
		t.Fatalf("tolak: %v", err)
	}

	if ditolak.Status != "DRAFT" || ditolak.AlasanTolak == nil {
		t.Errorf("status %q alasan_tolak %v, want DRAFT dengan alasan terisi",
			ditolak.Status, ditolak.AlasanTolak)
	}

	if _, err := testApp.retur.Ajukan(ctx(), &model.AjukanReturPembelianRequest{
		ID: retur.ID, ActorID: f.actor,
	}); err != nil {
		t.Fatalf("ajukan ulang: %v", err)
	}

	posted, err := testApp.retur.Posting(ctx(), &model.PostingReturPembelianRequest{
		ID: retur.ID, ActorID: f.actor,
	})
	if err != nil {
		t.Fatalf("posting: %v", err)
	}

	if posted.DisetujuiOleh == nil || posted.PostedAt == nil {
		t.Errorf("disetujui_oleh %v posted_at %v, want keduanya terisi",
			posted.DisetujuiOleh, posted.PostedAt)
	}

	// Posting twice would take the goods out twice.
	_, err = testApp.retur.Posting(ctx(), &model.PostingReturPembelianRequest{
		ID: retur.ID, ActorID: f.actor,
	})
	assertKind(t, err, model.KindConflict)

	// And a posted document rejects a line replacement.
	_, err = testApp.retur.ReplaceDetail(ctx(), &model.ReplaceReturPembelianDetailRequest{
		ID: retur.ID, ActorID: f.actor,
		Detail: []model.ReturPembelianDetailRequest{{
			IDPembelianDetail: pembelian.Detail[0].ID, IDSatuanInput: f.pcs, QtyInput: "1",
		}},
	})
	assertKind(t, err, model.KindConflict)
}

// alasan is required at create and cannot be cleared by a patch. It is the only record
// of why goods already paid for went back, so a document without one could not have been
// created and must not be reachable by editing.
func TestAlasanReturWajibDanTidakBisaDikosongkan(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := returFixture(t, testApp)

	_, err := testApp.retur.Create(ctx(), &model.CreateReturPembelianRequest{
		ActorID:     f.actor,
		IDPembelian: pembelian.ID,
		Tanggal:     "2026-08-15",
		Detail: []model.ReturPembelianDetailRequest{{
			IDPembelianDetail: pembelian.Detail[0].ID, IDSatuanInput: f.pcs, QtyInput: "1",
		}},
	})
	if err == nil {
		t.Fatal("retur tanpa alasan diterima, want ditolak")
	}

	retur, err := testApp.retur.Create(ctx(), &model.CreateReturPembelianRequest{
		ActorID:     f.actor,
		IDPembelian: pembelian.ID,
		Tanggal:     "2026-08-15",
		Alasan:      "rusak",
		Detail: []model.ReturPembelianDetailRequest{{
			IDPembelianDetail: pembelian.Detail[0].ID, IDSatuanInput: f.pcs, QtyInput: "1",
		}},
	})
	if err != nil {
		t.Fatalf("create retur: %v", err)
	}

	_, err = testApp.retur.Update(ctx(), &model.UpdateReturPembelianRequest{
		ID: retur.ID, ActorID: f.actor,
		Alasan: model.Optional[string]{Present: true, Value: nil},
	})
	assertKind(t, err, model.KindInvalid)

	// An empty body changes nothing, so it is refused rather than silently accepted.
	_, err = testApp.retur.Update(ctx(), &model.UpdateReturPembelianRequest{
		ID: retur.ID, ActorID: f.actor,
	})
	assertKind(t, err, model.KindInvalid)

	diubah, err := testApp.retur.Update(ctx(), &model.UpdateReturPembelianRequest{
		ID: retur.ID, ActorID: f.actor,
		Alasan: model.Optional[string]{Present: true, Value: ptr("ternyata salah kirim")},
	})
	if err != nil {
		t.Fatalf("update alasan: %v", err)
	}

	if diubah.Alasan == nil || *diubah.Alasan != "ternyata salah kirim" {
		t.Errorf("alasan = %v, want %q", diubah.Alasan, "ternyata salah kirim")
	}
}

// kartu_stok rows from a return carry their own jenis_transaksi and their own ref_table,
// so a stock movement report can separate goods that went back from goods that were sold
// without joining to the document tables.
func TestReturMenulisJenisTransaksiSendiri(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := returFixture(t, testApp)

	buatRetur(t, testApp, f, pembelian.ID, pembelian.Detail[0].ID, "10")

	rows, err := testDB.Query(`
		SELECT jenis_transaksi::TEXT, ref_table, stok_masuk, stok_keluar
		FROM kartu_stok WHERE id_barang = $1 ORDER BY id
	`, f.product)
	if err != nil {
		t.Fatalf("baca kartu_stok: %v", err)
	}
	defer rows.Close()

	type gerak struct {
		jenis, ref    string
		masuk, keluar int64
	}

	var hasil []gerak
	for rows.Next() {
		var g gerak
		if err := rows.Scan(&g.jenis, &g.ref, &g.masuk, &g.keluar); err != nil {
			t.Fatalf("scan: %v", err)
		}

		hasil = append(hasil, g)
	}

	if err := rows.Err(); err != nil {
		t.Fatalf("iterate kartu_stok: %v", err)
	}

	want := []gerak{
		{"PEMBELIAN", "pembelian", 100, 0},
		{"RETUR_PEMBELIAN", "retur_pembelian", 0, 10},
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
func TestListReturFilterDanPaginasi(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := returFixture(t, testApp)

	idBaris := pembelian.Detail[0].ID

	// Five drafts of one piece each, all dated the same day.
	for range 5 {
		if _, err := testApp.retur.Create(ctx(), &model.CreateReturPembelianRequest{
			ActorID:     f.actor,
			IDPembelian: pembelian.ID,
			Tanggal:     "2026-08-15",
			Alasan:      "rusak",
			Detail: []model.ReturPembelianDetailRequest{{
				IDPembelianDetail: idBaris, IDSatuanInput: f.pcs, QtyInput: "1",
			}},
		}); err != nil {
			t.Fatalf("create retur: %v", err)
		}
	}

	_, paging, err := testApp.retur.Search(ctx(), &model.ListReturPembelianRequest{})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if paging.TotalItem != 5 {
		t.Errorf("total_item = %d, want 5", paging.TotalItem)
	}

	kosong, _, err := testApp.retur.Search(ctx(), &model.ListReturPembelianRequest{
		Status: "POSTED",
	})
	if err != nil {
		t.Fatalf("search POSTED: %v", err)
	}

	if len(kosong) != 0 {
		t.Errorf("filter POSTED mengembalikan %d, want 0", len(kosong))
	}

	// The supplier filter is on this document's own column, copied from the purchase.
	milikSupplier, _, err := testApp.retur.Search(ctx(), &model.ListReturPembelianRequest{
		IDSupplier: f.supplier,
	})
	if err != nil {
		t.Fatalf("search id_supplier: %v", err)
	}

	if len(milikSupplier) != 5 {
		t.Errorf("filter id_supplier mengembalikan %d, want 5", len(milikSupplier))
	}

	// Same-day documents are exactly the tie the unique tiebreaker exists for.
	terlihat := map[int64]bool{}
	for halaman := 1; halaman <= 3; halaman++ {
		batch, _, err := testApp.retur.Search(ctx(), &model.ListReturPembelianRequest{
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
