package usecase_test

// The freeze (isu #15) is the one behaviour in this codebase enforced identically
// across six different modules by a single trigger rather than by six separate
// calls, and reading the six together is what makes that shape legible — the same
// reasoning fase6_read_scope_test.go and ruang_unit_scope_test.go already follow
// for their own cross-cutting behaviours. Nothing here can be proven with a mock:
// the refusal happens inside kartu_stok_hitung_saldo, under the ruang: advisory
// lock, on every INSERT regardless of which module issued it.

import (
	"testing"
	"time"

	"Arthafreestyle/ERP/internal/model"
)

// bekuFixture posts one purchase of 100 pcs, of which only 95 arrived, into
// f.ruang — POSTED already, giving penerimaan_susulan a quota of 5 and
// retur_pembelian 95 to send back, and 95 pcs on the shelf for mutasi, pemakaian,
// and penjualan to draw from.
func bekuFixture(t *testing.T, testApp *app) (fixture, *model.PembelianResponse) {
	t.Helper()

	f := pembelianFixture(t, testApp)

	awal, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
		ActorID: f.actor, Tanggal: "2026-08-11", IDSupplier: f.supplier, IDRuang: f.ruang,
		Detail: []model.PembelianDetailRequest{{
			IDProduct: f.product, IDSatuanInput: f.pcs,
			QtyFaktur: "100", QtyDiterima: ptr("95"), HargaSatuanInput: "10000",
			KeteranganSelisih: ptr("box kurang 5"),
		}},
	})
	if err != nil {
		t.Fatalf("create pembelian awal: %v", err)
	}

	return f, ajukanDanPosting(t, testApp, f, awal.ID)
}

// The DoD's own words: lima modul (now enam, since penjualan was built after this
// issue was written), satu trigger — proof the freeze is cross-module rather than
// something bolted onto each writer separately. Every document is prepared up to
// the last step BEFORE the opname opens, since Create itself never touches
// kartu_stok; only the freeze opens, and only then is Posting attempted.
func TestPostingEnamModulKeRuangBekuDitolak409(t *testing.T) {
	testApp := newApp(t)
	f, awal := bekuFixture(t, testApp)

	// pembelian: a second, independent purchase into the same room.
	belanjaBaru := draftSederhana(t, testApp, f, "10", nil, nil)
	if _, err := testApp.pembelian.Ajukan(ctx(), &model.AjukanPembelianRequest{
		ID: belanjaBaru.ID, ActorID: f.actor,
	}); err != nil {
		t.Fatalf("ajukan belanja baru: %v", err)
	}

	// penerimaan_susulan: the 5 still outstanding on the first purchase.
	susulan, err := testApp.susulan.Create(ctx(), &model.CreatePenerimaanSusulanRequest{
		ActorID: f.actor, IDPembelian: awal.ID, Tanggal: "2026-08-20",
		Detail: []model.PenerimaanSusulanDetailRequest{{
			IDPembelianDetail: awal.Detail[0].ID, IDSatuanInput: f.pcs, QtyInput: "5",
		}},
	})
	if err != nil {
		t.Fatalf("create susulan: %v", err)
	}
	if _, err := testApp.susulan.Ajukan(ctx(), &model.AjukanPenerimaanSusulanRequest{
		ID: susulan.ID, ActorID: f.actor,
	}); err != nil {
		t.Fatalf("ajukan susulan: %v", err)
	}

	// retur_pembelian: some of what actually arrived.
	retur, err := testApp.retur.Create(ctx(), &model.CreateReturPembelianRequest{
		ActorID: f.actor, IDPembelian: awal.ID, Tanggal: "2026-08-15",
		Alasan: "sebagian rusak",
		Detail: []model.ReturPembelianDetailRequest{{
			IDPembelianDetail: awal.Detail[0].ID, IDSatuanInput: f.pcs, QtyInput: "5",
		}},
	})
	if err != nil {
		t.Fatalf("create retur: %v", err)
	}
	if _, err := testApp.retur.Ajukan(ctx(), &model.AjukanReturPembelianRequest{
		ID: retur.ID, ActorID: f.actor,
	}); err != nil {
		t.Fatalf("ajukan retur: %v", err)
	}

	// mutasi: out of the frozen room into a spare one. No DIAJUKAN to reach —
	// DRAFT is enough to post from.
	unitLain, err := testApp.unitKerja.Create(ctx(), &model.CreateUnitKerjaRequest{Nama: "Unit Tujuan Mutasi"})
	if err != nil {
		t.Fatalf("create unit lain: %v", err)
	}
	ruangTujuan, err := testApp.ruang.Create(ctx(), &model.CreateRuangRequest{
		NamaRuang: "Tujuan Mutasi", IDUnitKerja: unitLain.ID,
	})
	if err != nil {
		t.Fatalf("create ruang tujuan: %v", err)
	}
	mutasi, err := testApp.mutasi.Create(ctx(), &model.CreateMutasiRequest{
		ActorID: f.actor, Tanggal: "2026-08-15",
		IDRuangAsal: f.ruang, IDRuangTujuan: ruangTujuan.ID,
		Detail: []model.MutasiDetailRequest{{IDProduct: f.product, IDSatuanInput: f.pcs, QtyInput: "5"}},
	})
	if err != nil {
		t.Fatalf("create mutasi: %v", err)
	}

	// pemakaian: needs a distinct requester and approver.
	pemohon := buatUserPolos(t, testApp, "pemohon_beku")
	penyetuju := buatUserPolos(t, testApp, "penyetuju_beku")
	pemakaian, err := testApp.pemakaian.Create(ctx(), &model.CreatePemakaianRequest{
		ActorID: f.actor, Tanggal: "2026-08-15", IDRuang: f.ruang, IDPemohon: pemohon,
		Keperluan: "perbaikan", Detail: []model.PemakaianDetailRequest{{
			IDProduct: f.product, IDSatuanInput: f.pcs, QtyInput: "5",
		}},
	})
	if err != nil {
		t.Fatalf("create pemakaian: %v", err)
	}
	if _, err := testApp.pemakaian.Ajukan(ctx(), &model.AjukanPemakaianRequest{
		ID: pemakaian.ID, ActorID: f.actor,
	}); err != nil {
		t.Fatalf("ajukan pemakaian: %v", err)
	}
	if _, err := testApp.pemakaian.Setujui(ctx(), &model.SetujuiPemakaianRequest{
		ID: pemakaian.ID, ActorID: penyetuju,
	}); err != nil {
		t.Fatalf("setujui pemakaian: %v", err)
	}

	// penjualan: no DIAJUKAN either — a cashier's nota is created and posted in one
	// motion, so DRAFT is enough.
	penjualan, err := testApp.penjualan.Create(ctx(), &model.CreatePenjualanRequest{
		ActorID: f.actor, Tanggal: "2026-08-15", IDRuang: f.ruang,
		Detail: []model.PenjualanDetailRequest{{
			IDProduct: f.product, IDSatuanInput: f.pcs, QtyInput: "5", HargaSatuanInput: "15000",
		}},
	})
	if err != nil {
		t.Fatalf("create penjualan: %v", err)
	}

	// Everything above is DRAFT or DIAJUKAN, and none of it touched kartu_stok. Now
	// the room freezes.
	opname := buatOpname(t, testApp, f)

	_, err = testApp.pembelian.Posting(ctx(), &model.PostingPembelianRequest{
		ID: belanjaBaru.ID, ActorID: f.actor,
	})
	assertKind(t, err, model.KindConflict)

	_, err = testApp.susulan.Posting(ctx(), &model.PostingPenerimaanSusulanRequest{
		ID: susulan.ID, ActorID: f.actor,
	})
	assertKind(t, err, model.KindConflict)

	_, err = testApp.retur.Posting(ctx(), &model.PostingReturPembelianRequest{
		ID: retur.ID, ActorID: f.actor,
	})
	assertKind(t, err, model.KindConflict)

	_, err = testApp.mutasi.Posting(ctx(), &model.PostingMutasiRequest{
		ID: mutasi.ID, ActorID: f.actor,
	})
	assertKind(t, err, model.KindConflict)

	_, err = testApp.pemakaian.Posting(ctx(), &model.PostingPemakaianRequest{
		ID: pemakaian.ID, ActorID: f.actor,
	})
	assertKind(t, err, model.KindConflict)

	_, err = testApp.penjualan.Posting(ctx(), &model.PostingPenjualanRequest{
		ID: penjualan.ID, ActorID: f.actor,
	})
	assertKind(t, err, model.KindConflict)

	// The opname's own adjustment is the one thing that is allowed to touch this
	// room while it is frozen — the trigger's self-reference exception.
	hasil := tarikSaldo(t, testApp, f, opname.ID)
	baris := detailByProduct(t, hasil, f.product)
	patchStokSO(t, testApp, f, opname.ID, baris.ID, 90)
	ajukanOpname(t, testApp, f, opname.ID)
	posted := postingOpname(t, testApp, f, opname.ID)
	if posted.Status != "POSTED" {
		t.Fatalf("stok_opname sendiri gagal diposting ke ruang yang ia bekukan sendiri: status = %q", posted.Status)
	}
}

// The freeze's radius is one ruang, not the caller's whole unit or company: a
// warehouse being counted must not stop a shop in a different room, or even a
// different unit, from selling. And what is frozen is posting, not typing —
// drafts and submissions in other modules must still accumulate while the count
// runs, because the paperwork does not stop just because the shelf is being
// counted.
func TestPembekuanRadiusSatuRuangDanHanyaMenahanPosting(t *testing.T) {
	testApp := newApp(t)
	f, _ := bekuFixture(t, testApp)

	// A second room in the SAME unit, stocked independently.
	ruangLain, err := testApp.ruang.Create(ctx(), &model.CreateRuangRequest{
		NamaRuang: "Toko Sebelah", IDUnitKerja: f.unitKerja,
	})
	if err != nil {
		t.Fatalf("create ruang lain: %v", err)
	}
	belanjaRuangLain, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
		ActorID: f.actor, Tanggal: "2026-08-11", IDSupplier: f.supplier, IDRuang: ruangLain.ID,
		Detail: []model.PembelianDetailRequest{{
			IDProduct: f.product, IDSatuanInput: f.pcs, QtyFaktur: "20", HargaSatuanInput: "10000",
		}},
	})
	if err != nil {
		t.Fatalf("create pembelian ruang lain: %v", err)
	}
	if _, err := testApp.pembelian.Ajukan(ctx(), &model.AjukanPembelianRequest{
		ID: belanjaRuangLain.ID, ActorID: f.actor,
	}); err != nil {
		t.Fatalf("ajukan belanja ruang lain: %v", err)
	}

	// A DRAFT purchase into the room that is ABOUT to freeze — prepared before the
	// freeze, posted after, to prove drafting is never blocked.
	draftDitengahOpname := draftSederhana(t, testApp, f, "10", nil, nil)

	buatOpname(t, testApp, f)

	// Posting to a different room, same unit: unaffected.
	if _, err := testApp.pembelian.Posting(ctx(), &model.PostingPembelianRequest{
		ID: belanjaRuangLain.ID, ActorID: f.actor,
	}); err != nil {
		t.Fatalf("posting ke ruang lain di unit yang sama ditolak: %v", err)
	}

	// Drafting and submitting into the frozen room: unaffected, only posting is.
	if _, err := testApp.pembelian.Ajukan(ctx(), &model.AjukanPembelianRequest{
		ID: draftDitengahOpname.ID, ActorID: f.actor,
	}); err != nil {
		t.Fatalf("mengajukan draft ke ruang yang beku ditolak, seharusnya hanya posting yang tertahan: %v", err)
	}

	_, err = testApp.pembelian.Posting(ctx(), &model.PostingPembelianRequest{
		ID: draftDitengahOpname.ID, ActorID: f.actor,
	})
	assertKind(t, err, model.KindConflict)
}

// mutasi is checked on BOTH rooms, unlike isu #12 fase 5's write-side scoping,
// which is source-only. A branch that restocks from a warehouse currently being
// counted must not receive the shipment, even though the branch's own room is
// perfectly free — the goods physically cannot leave a shelf mid-count either.
func TestMutasiDitolakSaatRuangTujuanBeku(t *testing.T) {
	testApp := newApp(t)
	f, _ := bekuFixture(t, testApp)

	// Kode is required: cabang becomes the source room of a pembelian and a mutasi
	// below, and isu #21 fase 1 keys every document number to the issuing unit's
	// kode.
	unitLain, err := testApp.unitKerja.Create(ctx(), &model.CreateUnitKerjaRequest{
		Kode: ptr("CABANG"), Nama: "Unit Cabang",
	})
	if err != nil {
		t.Fatalf("create unit lain: %v", err)
	}
	cabang, err := testApp.ruang.Create(ctx(), &model.CreateRuangRequest{
		NamaRuang: "Cabang", IDUnitKerja: unitLain.ID,
	})
	if err != nil {
		t.Fatalf("create ruang cabang: %v", err)
	}

	// Stock the source room — cabang, which is not being counted — so the mutasi
	// below has something to move.
	belanjaCabang, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
		ActorID: f.actor, Tanggal: "2026-08-11", IDSupplier: f.supplier, IDRuang: cabang.ID,
		Detail: []model.PembelianDetailRequest{{
			IDProduct: f.product, IDSatuanInput: f.pcs, QtyFaktur: "20", HargaSatuanInput: "10000",
		}},
	})
	if err != nil {
		t.Fatalf("create pembelian cabang: %v", err)
	}
	ajukanDanPosting(t, testApp, f, belanjaCabang.ID)

	// mutasi FROM cabang (free) TO f.ruang (about to freeze) — goods restocking the
	// warehouse from the branch.
	mutasi, err := testApp.mutasi.Create(ctx(), &model.CreateMutasiRequest{
		ActorID: f.actor, Tanggal: "2026-08-15",
		IDRuangAsal: cabang.ID, IDRuangTujuan: f.ruang,
		Detail: []model.MutasiDetailRequest{{IDProduct: f.product, IDSatuanInput: f.pcs, QtyInput: "1"}},
	})
	if err != nil {
		t.Fatalf("create mutasi: %v", err)
	}

	buatOpname(t, testApp, f)

	_, err = testApp.mutasi.Posting(ctx(), &model.PostingMutasiRequest{ID: mutasi.ID, ActorID: f.actor})
	assertKind(t, err, model.KindConflict)
}

// Abandoning a count before it is posted has to release the room immediately —
// otherwise an opname left open by mistake freezes a room forever with no way
// out. batal is checked here, not posting: it is the one action reachable from
// DRAFT, before anything has ever touched kartu_stok.
func TestBatalOpnameDariDraftMelepasBekuLangsung(t *testing.T) {
	testApp := newApp(t)
	f, _ := bekuFixture(t, testApp)

	draftTertahan := draftSederhana(t, testApp, f, "10", nil, nil)
	if _, err := testApp.pembelian.Ajukan(ctx(), &model.AjukanPembelianRequest{
		ID: draftTertahan.ID, ActorID: f.actor,
	}); err != nil {
		t.Fatalf("ajukan: %v", err)
	}

	opname := buatOpname(t, testApp, f)

	_, err := testApp.pembelian.Posting(ctx(), &model.PostingPembelianRequest{
		ID: draftTertahan.ID, ActorID: f.actor,
	})
	assertKind(t, err, model.KindConflict)

	if _, err := testApp.stokOpname.Batal(ctx(), &model.BatalStokOpnameRequest{
		ID: opname.ID, ActorID: f.actor, AlasanBatal: "salah buka ruang",
	}); err != nil {
		t.Fatalf("batal stok_opname: %v", err)
	}

	if _, err := testApp.pembelian.Posting(ctx(), &model.PostingPembelianRequest{
		ID: draftTertahan.ID, ActorID: f.actor,
	}); err != nil {
		t.Fatalf("posting setelah batal masih ditolak: %v", err)
	}
}

// Mirrors TestTutupMenungguPostingYangSedangBerjalan for the ruang: lock rather
// than the periode: lock, and fails with "deadlock" or a false pass without it: a
// posting already in flight for a room must finish before an opname can open
// against it, or the cutoff would not really cover everything that happened
// before it.
func TestBukaOpnameMenungguPostingYangSedangBerjalan(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	// A second, unrelated room under the same actor — pembelianFixture creates a
	// fixed username, so it cannot be called twice in one test.
	// Kode is required: ruangLain is the id_ruang of a stok_opname.Create call
	// below, and isu #21 fase 1 keys every document number to the issuing unit's
	// kode.
	unitLain, err := testApp.unitKerja.Create(ctx(), &model.CreateUnitKerjaRequest{
		Kode: ptr("LAIN"), Nama: "Unit Lain",
	})
	if err != nil {
		t.Fatalf("create unit lain: %v", err)
	}
	ruangLain, err := testApp.ruang.Create(ctx(), &model.CreateRuangRequest{
		NamaRuang: "Ruang Lain", IDUnitKerja: unitLain.ID,
	})
	if err != nil {
		t.Fatalf("create ruang lain: %v", err)
	}

	tx, err := testDB.BeginTx(ctx(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() {
		_ = tx.Rollback() // no-op once committed
	}()

	// Takes the shared ruang: lock for f.ruang and holds it until this commits.
	if _, err := tx.ExecContext(ctx(), `
		INSERT INTO kartu_stok (
			id_barang, id_ruang, tanggal_transaksi, jenis_transaksi,
			stok_masuk, nilai_masuk, ref_table, ref_id_transaksi, created_by
		)
		VALUES ($1, $2, $3, 'PEMBELIAN', 5, 50000, 'pembelian', 1, $4)
	`, f.product, f.ruang, time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC), f.actor); err != nil {
		t.Fatalf("insert kartu_stok: %v", err)
	}

	// Negative control first: opening an opname on an unrelated room must not be
	// held up by any of this, or the lock would be a global stop-the-world rather
	// than per-ruang.
	selesai := make(chan error, 1)
	go func() {
		_, err := testApp.stokOpname.Create(ctx(), &model.CreateStokOpnameRequest{
			ActorID: f.actor, IDRuang: ruangLain.ID,
		})
		selesai <- err
	}()

	select {
	case err := <-selesai:
		if err != nil {
			t.Fatalf("buka opname ruang lain: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("membuka opname di ruang lain ikut terblokir; kuncinya tidak per-ruang")
	}

	// And now the room the uncommitted posting actually belongs to.
	tertahan := make(chan error, 1)
	go func() {
		_, err := testApp.stokOpname.Create(ctx(), &model.CreateStokOpnameRequest{
			ActorID: f.actor, IDRuang: f.ruang,
		})
		tertahan <- err
	}()

	select {
	case err := <-tertahan:
		t.Fatalf("buka opname selesai (err=%v) selagi posting ruang itu belum commit", err)
	case <-time.After(500 * time.Millisecond):
		// Still waiting, which is the point.
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit posting: %v", err)
	}

	select {
	case err := <-tertahan:
		if err != nil {
			t.Fatalf("buka opname setelah posting commit: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("buka opname tidak pernah selesai setelah postingnya commit")
	}
}
