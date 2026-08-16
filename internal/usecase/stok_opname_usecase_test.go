package usecase_test

// Stok opname (isu #15) — the seventh document to write kartu_stok, and the first
// that moves nothing to or from anywhere. These run against a real PostgreSQL
// because the point of the module lives there: the partial unique index that
// allows at most one open count per room, the moving average a surplus must not
// disturb, and the append-only ledger a deficit writes into. Cross-module freeze
// behavior lives in its own file, ruang_beku_test.go — this one is the module's
// own arithmetic and lifecycle.

import (
	"testing"

	"Arthafreestyle/ERP/internal/model"
)

// stokAwalOpname posts a purchase of qty pcs at 10.000 each into f.ruang, so
// there is a balance for TarikSaldo to pull.
func stokAwalOpname(t *testing.T, testApp *app, qty string) (*app, fixture) {
	t.Helper()

	f := pembelianFixture(t, testApp)
	draft := draftSederhana(t, testApp, f, qty, nil, nil)
	ajukanDanPosting(t, testApp, f, draft.ID)

	return testApp, f
}

func buatOpname(t *testing.T, testApp *app, f fixture) *model.StokOpnameResponse {
	t.Helper()

	opname, err := testApp.stokOpname.Create(ctx(), &model.CreateStokOpnameRequest{
		ActorID: f.actor, IDRuang: f.ruang,
	})
	if err != nil {
		t.Fatalf("create stok_opname: %v", err)
	}

	return opname
}

func tarikSaldo(t *testing.T, testApp *app, f fixture, id int64) *model.StokOpnameResponse {
	t.Helper()

	r, err := testApp.stokOpname.TarikSaldo(ctx(), &model.TarikSaldoStokOpnameRequest{
		ID: id, ActorID: f.actor,
	})
	if err != nil {
		t.Fatalf("tarik saldo: %v", err)
	}

	return r
}

func patchStokSO(t *testing.T, testApp *app, f fixture, id, idDetail, stokSO int64) *model.StokOpnameResponse {
	t.Helper()

	r, err := testApp.stokOpname.UpdateDetail(ctx(), &model.UpdateStokOpnameDetailRequest{
		ID: id, IDDetail: idDetail, ActorID: f.actor,
		StokSO: model.Optional[int64]{Present: true, Value: ptr(stokSO)},
	})
	if err != nil {
		t.Fatalf("patch detail: %v", err)
	}

	return r
}

func ajukanOpname(t *testing.T, testApp *app, f fixture, id int64) *model.StokOpnameResponse {
	t.Helper()

	r, err := testApp.stokOpname.Ajukan(ctx(), &model.AjukanStokOpnameRequest{
		ID: id, ActorID: f.actor,
	})
	if err != nil {
		t.Fatalf("ajukan stok_opname: %v", err)
	}

	return r
}

func postingOpname(t *testing.T, testApp *app, f fixture, id int64) *model.StokOpnameResponse {
	t.Helper()

	r, err := testApp.stokOpname.Posting(ctx(), &model.PostingStokOpnameRequest{
		ID: id, ActorID: f.actor,
	})
	if err != nil {
		t.Fatalf("posting stok_opname: %v", err)
	}

	return r
}

// detailByProduct finds one line by its product id — TarikSaldo's own ordering
// is by idstok_opname_detail, which has nothing to do with the order the fixture
// created products in.
func detailByProduct(t *testing.T, resp *model.StokOpnameResponse, idProduct int64) model.StokOpnameDetailResponse {
	t.Helper()

	for _, d := range resp.Detail {
		if d.IDProduct == idProduct {
			return d
		}
	}

	t.Fatalf("no detail row for product %d", idProduct)

	return model.StokOpnameDetailResponse{}
}

// TarikSaldo is what makes this document mean anything: it snapshots the room's
// balance at the moment the count starts. StokSO comes back nil — not counted
// yet — and StokAwal is frozen at exactly what kartu_stok held.
func TestTarikSaldoMengisiDariSaldoRuang(t *testing.T) {
	testApp, f := stokAwalOpname(t, newApp(t), "100")

	opname := buatOpname(t, testApp, f)
	hasil := tarikSaldo(t, testApp, f, opname.ID)

	if len(hasil.Detail) != 1 {
		t.Fatalf("jumlah baris = %d, want 1", len(hasil.Detail))
	}

	baris := hasil.Detail[0]
	if baris.StokAwal != 100 {
		t.Errorf("stok_awal = %d, want 100", baris.StokAwal)
	}
	if baris.StokSO != nil {
		t.Errorf("stok_so = %v, want nil (belum dihitung)", *baris.StokSO)
	}
	if baris.StokSelisihLebih != 0 || baris.StokSelisihKurang != 0 {
		t.Errorf("selisih = (+%d/-%d), want (0/0) sebelum dihitung",
			baris.StokSelisihLebih, baris.StokSelisihKurang)
	}
}

// Pulling the balance twice would be the cleanest way to end up with two
// snapshots inside one document — refused outright.
func TestTarikSaldoDuaKaliDitolak(t *testing.T) {
	testApp, f := stokAwalOpname(t, newApp(t), "100")

	opname := buatOpname(t, testApp, f)
	tarikSaldo(t, testApp, f, opname.ID)

	_, err := testApp.stokOpname.TarikSaldo(ctx(), &model.TarikSaldoStokOpnameRequest{
		ID: opname.ID, ActorID: f.actor,
	})

	assertKind(t, err, model.KindConflict)
}

// Two open counts against the same room would be two snapshot cutoffs over the
// same shelf, each posting its own selisih — the same correction booked twice.
// stok_opname_ruang_terbuka_uidx is what makes this a 409, not a race two
// requests could both slip past.
func TestOpnameKeduaDiRuangTerbukaDitolak(t *testing.T) {
	testApp, f := stokAwalOpname(t, newApp(t), "100")

	buatOpname(t, testApp, f)

	_, err := testApp.stokOpname.Create(ctx(), &model.CreateStokOpnameRequest{
		ActorID: f.actor, IDRuang: f.ruang,
	})

	assertKind(t, err, model.KindConflict)
}

// An opname with nothing counted at all is an empty document, not a count.
func TestAjukanTanpaBarisTerhitungDitolak(t *testing.T) {
	testApp, f := stokAwalOpname(t, newApp(t), "100")

	opname := buatOpname(t, testApp, f)
	tarikSaldo(t, testApp, f, opname.ID)

	_, err := testApp.stokOpname.Ajukan(ctx(), &model.AjukanStokOpnameRequest{
		ID: opname.ID, ActorID: f.actor,
	})

	assertKind(t, err, model.KindInvalid)
}

// A submitted count that turns out wrong goes back to DRAFT for a recount —
// unlike pemakaian, this is not a business refusal and the room stays frozen
// either way. A second opname on the same room must still be blocked afterwards,
// which is the proof the freeze survived the round trip.
func TestTolakKembaliKeDraftTetapBeku(t *testing.T) {
	testApp, f := stokAwalOpname(t, newApp(t), "100")

	opname := buatOpname(t, testApp, f)
	hasil := tarikSaldo(t, testApp, f, opname.ID)
	baris := detailByProduct(t, hasil, f.product)

	patchStokSO(t, testApp, f, opname.ID, baris.ID, 100)
	ajukanOpname(t, testApp, f, opname.ID)

	tolak, err := testApp.stokOpname.Tolak(ctx(), &model.TolakStokOpnameRequest{
		ID: opname.ID, ActorID: f.actor,
	})
	if err != nil {
		t.Fatalf("tolak stok_opname: %v", err)
	}

	if tolak.Status != "DRAFT" {
		t.Fatalf("status = %q, want DRAFT", tolak.Status)
	}

	_, err = testApp.stokOpname.Create(ctx(), &model.CreateStokOpnameRequest{
		ActorID: f.actor, IDRuang: f.ruang,
	})
	assertKind(t, err, model.KindConflict)
}

// stok_so = NULL is "not counted yet", never zero. A line left untouched must be
// skipped entirely at posting — no selisih, no kartu_stok row, no change to what
// the shelf is recorded as holding — while a counted line right next to it posts
// normally. Confusing the two would erase a product's whole recorded stock just
// because nobody reached its shelf yet.
func TestBarisBelumDihitungDilewatiSaatPosting(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	productB, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID: f.actor, KodeBarang: "BRG-002", Nama: "Tinta Printer", IDSatuanDasar: f.pcs,
	})
	if err != nil {
		t.Fatalf("create product B: %v", err)
	}

	beli, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
		ActorID: f.actor, Tanggal: "2026-08-11", IDSupplier: f.supplier, IDRuang: f.ruang,
		Detail: []model.PembelianDetailRequest{
			{IDProduct: f.product, IDSatuanInput: f.pcs, QtyFaktur: "100", HargaSatuanInput: "10000"},
			{IDProduct: productB.ID, IDSatuanInput: f.pcs, QtyFaktur: "50", HargaSatuanInput: "10000"},
		},
	})
	if err != nil {
		t.Fatalf("create pembelian: %v", err)
	}
	ajukanDanPosting(t, testApp, f, beli.ID)

	opname := buatOpname(t, testApp, f)
	hasil := tarikSaldo(t, testApp, f, opname.ID)

	// Only f.product is counted, with a surplus of 10; productB is left untouched.
	barisA := detailByProduct(t, hasil, f.product)
	patchStokSO(t, testApp, f, opname.ID, barisA.ID, 110)

	ajukanOpname(t, testApp, f, opname.ID)
	posted := postingOpname(t, testApp, f, opname.ID)

	respA := detailByProduct(t, posted, f.product)
	if respA.IDKartuStokPenyesuaian == nil {
		t.Errorf("baris A: id_kartu_stok_penyesuaian nil, want terisi (selisih +10)")
	}

	respB := detailByProduct(t, posted, productB.ID)
	if respB.StokSO != nil {
		t.Errorf("baris B: stok_so = %v, want tetap nil", *respB.StokSO)
	}
	if respB.IDKartuStokPenyesuaian != nil {
		t.Errorf("baris B: id_kartu_stok_penyesuaian terisi, want nil — tidak boleh ada baris kartu_stok")
	}

	stokB, _ := saldoStok(t, productB.ID, f.ruang)
	if stokB != 50 {
		t.Errorf("stok B setelah posting = %d, want tetap 50 (tidak tersentuh)", stokB)
	}
}

// Goods that turn up again are goods that were always on the shelf — valuing the
// surplus at the room's own moving average is what keeps that average from
// shifting at all when they are recorded found.
func TestSurplusTidakMenggeserHPP(t *testing.T) {
	testApp, f := stokAwalOpname(t, newApp(t), "100")

	opname := buatOpname(t, testApp, f)
	hasil := tarikSaldo(t, testApp, f, opname.ID)
	baris := detailByProduct(t, hasil, f.product)

	patchStokSO(t, testApp, f, opname.ID, baris.ID, 110)
	ajukanOpname(t, testApp, f, opname.ID)
	postingOpname(t, testApp, f, opname.ID)

	stok, nilai := saldoStok(t, f.product, f.ruang)
	if stok != 110 {
		t.Fatalf("stok akhir = %d, want 110", stok)
	}
	if nilai != "1100000.00" {
		t.Fatalf("nilai akhir = %q, want 1100000.00 (HPP tetap 10.000/pcs)", nilai)
	}

	var hpp string
	if err := testDB.QueryRow(
		`SELECT harga_pokok_satuan::TEXT FROM kartu_stok
		 WHERE id_barang = $1 AND id_ruang = $2 ORDER BY id DESC LIMIT 1`,
		f.product, f.ruang,
	).Scan(&hpp); err != nil {
		t.Fatalf("read hpp: %v", err)
	}
	if hpp != "10000.0000" {
		t.Errorf("harga_pokok_satuan = %q, want 10000.0000 (tidak bergeser)", hpp)
	}
}

// A deficit is an ordinary outgoing row: it can drive stock to (but never below)
// zero, exactly what stok_so = 0 means when the shelf really is empty.
func TestDefisitMengeluarkanStokSampaiNol(t *testing.T) {
	testApp, f := stokAwalOpname(t, newApp(t), "100")

	opname := buatOpname(t, testApp, f)
	hasil := tarikSaldo(t, testApp, f, opname.ID)
	baris := detailByProduct(t, hasil, f.product)

	patchStokSO(t, testApp, f, opname.ID, baris.ID, 0)
	ajukanOpname(t, testApp, f, opname.ID)
	posted := postingOpname(t, testApp, f, opname.ID)

	respA := detailByProduct(t, posted, f.product)
	if respA.StokSelisihKurang != 100 {
		t.Errorf("stok_selisih_kurang = %d, want 100", respA.StokSelisihKurang)
	}
	if respA.IDKartuStokPenyesuaian == nil {
		t.Fatalf("id_kartu_stok_penyesuaian nil, want terisi")
	}

	stok, nilai := saldoStok(t, f.product, f.ruang)
	if stok != 0 {
		t.Errorf("stok akhir = %d, want 0", stok)
	}
	if nilai != "0.00" {
		t.Errorf("nilai akhir = %q, want 0.00", nilai)
	}
}

// If every line's selisih comes out zero, the document still posts — "nothing was
// wrong" is the best possible outcome of a count and has to be recordable, unlike
// pemakaian where an all-zero request is refused as pointless.
func TestSemuaSelisihNolTetapBolehDiposting(t *testing.T) {
	testApp, f := stokAwalOpname(t, newApp(t), "100")

	opname := buatOpname(t, testApp, f)
	hasil := tarikSaldo(t, testApp, f, opname.ID)
	baris := detailByProduct(t, hasil, f.product)

	patchStokSO(t, testApp, f, opname.ID, baris.ID, 100)
	ajukanOpname(t, testApp, f, opname.ID)
	posted := postingOpname(t, testApp, f, opname.ID)

	if posted.Status != "POSTED" {
		t.Fatalf("status = %q, want POSTED", posted.Status)
	}

	respA := detailByProduct(t, posted, f.product)
	if respA.IDKartuStokPenyesuaian != nil {
		t.Errorf("id_kartu_stok_penyesuaian terisi, want nil — selisih nol tidak menulis apa pun")
	}

	ada, err := testApp.stokOpname.Get(ctx(), &model.GetStokOpnameRequest{ID: opname.ID})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if ada.PostedAt == nil {
		t.Errorf("posted_at nil, want terisi")
	}
}
