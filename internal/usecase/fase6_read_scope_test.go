package usecase_test

// isu #12 fase 6: read-path scoping. A caller with a non-global active unit_kerja
// must not be able to Get, list, or (for pembelian) read the outstanding-lines
// report for a document or room outside that unit — it has to answer exactly as if
// the resource did not exist (404), never 403, since a scoped read is supposed to
// withhold even the fact that the resource exists. List endpoints must simply omit
// what falls outside the unit, with no error at all.
//
// mutasi is the one exception worth its own tests: only id_ruang_asal is ever
// checked, mirroring the write-side rule from fase 5, so a document whose
// destination room sits outside the active unit — but whose source room does not —
// must stay fully visible.
//
// pemakaian and penjualan (isu #21 fase 2) get both halves in this same file: the
// write-side check isu #9/#10 originally opted out of, and the read-side scope every
// other kartu_stok-writing module already has. Their tests carry both the write and
// read assertions together, plus one write-path re-read guard neither of the five
// modules above happened to need spelled out before: a caller's AktifIDUnitKerja on
// an Update that never touches id_ruang must not scope that Update's own response,
// the same way every write-path call to detail() passes nil regardless of who is
// calling.

import (
	"testing"
	"time"

	"Arthafreestyle/ERP/internal/model"
)

func TestRuangGetHidesRoomOutsideActiveUnit(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	unitLain := createUnit(t, testApp, "Unit Lain Ruang Get")

	_, err := testApp.ruang.Get(ctx(), &model.GetRuangRequest{
		ID: f.ruang, AktifIDUnitKerja: &unitLain,
	})

	assertKind(t, err, model.KindNotFound)
}

func TestRuangGetAllowsRoomInsideActiveUnit(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	response, err := testApp.ruang.Get(ctx(), &model.GetRuangRequest{
		ID: f.ruang, AktifIDUnitKerja: &f.unitKerja,
	})
	if err != nil {
		t.Fatalf("get ruang inside active unit: %v", err)
	}
	if response.ID != f.ruang {
		t.Fatalf("got room %d, want %d", response.ID, f.ruang)
	}
}

func TestRuangGetAllowsAnyRoomWithGlobalActiveContext(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	_, err := testApp.ruang.Get(ctx(), &model.GetRuangRequest{ID: f.ruang, AktifIDUnitKerja: nil})
	if err != nil {
		t.Fatalf("get ruang with a global active context: %v", err)
	}
}

func TestRuangListOnlyShowsActiveUnitRooms(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	unitLain := createUnit(t, testApp, "Unit Lain Ruang List")
	ruangLain, err := testApp.ruang.Create(ctx(), &model.CreateRuangRequest{ActorID: f.actor, 
		NamaRuang: "Gudang Lain", IDUnitKerja: unitLain,
	})
	if err != nil {
		t.Fatalf("create ruang lain: %v", err)
	}

	list, _, err := testApp.ruang.Search(ctx(), &model.ListRuangRequest{AktifIDUnitKerja: &f.unitKerja})
	if err != nil {
		t.Fatalf("search ruang: %v", err)
	}

	for _, r := range list {
		if r.ID == ruangLain.ID {
			t.Fatalf("room from a different unit leaked into a scoped list")
		}
	}

	found := false
	for _, r := range list {
		if r.ID == f.ruang {
			found = true
		}
	}
	if !found {
		t.Fatal("room inside the active unit is missing from its own scoped list")
	}
}

func TestPembelianGetHidesDocumentOutsideActiveUnit(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)
	pembelian := draftSederhana(t, testApp, f, "10", nil, nil)

	unitLain := createUnit(t, testApp, "Unit Lain Pembelian Get")

	_, err := testApp.pembelian.Get(ctx(), &model.GetPembelianRequest{
		ID: pembelian.ID, AktifIDUnitKerja: &unitLain,
	})

	assertKind(t, err, model.KindNotFound)
}

func TestPembelianGetAllowsDocumentInsideActiveUnit(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)
	pembelian := draftSederhana(t, testApp, f, "10", nil, nil)

	_, err := testApp.pembelian.Get(ctx(), &model.GetPembelianRequest{
		ID: pembelian.ID, AktifIDUnitKerja: &f.unitKerja,
	})
	if err != nil {
		t.Fatalf("get pembelian inside active unit: %v", err)
	}
}

func TestPembelianSisaHidesDocumentOutsideActiveUnit(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)
	pembelian := draftSederhana(t, testApp, f, "10", nil, nil)

	unitLain := createUnit(t, testApp, "Unit Lain Pembelian Sisa")

	_, err := testApp.pembelian.Sisa(ctx(), &model.GetPembelianRequest{
		ID: pembelian.ID, AktifIDUnitKerja: &unitLain,
	})

	assertKind(t, err, model.KindNotFound)
}

func TestPembelianListOnlyShowsActiveUnitDocuments(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)
	inside := draftSederhana(t, testApp, f, "10", nil, nil)

	unitLain := createUnit(t, testApp, "Unit Lain Pembelian List")
	masukKatalog(t, f.product, unitLain)
	ruangLain, err := testApp.ruang.Create(ctx(), &model.CreateRuangRequest{ActorID: f.actor, 
		NamaRuang: "Gudang Lain Pembelian", IDUnitKerja: unitLain,
	})
	if err != nil {
		t.Fatalf("create ruang lain: %v", err)
	}
	outside, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
		ActorID: f.actor, Tanggal: "2026-08-11", IDSupplier: f.supplier, IDRuang: ruangLain.ID,
		Detail: []model.PembelianDetailRequest{{
			IDProduct: f.product, IDSatuanInput: f.pcs, QtyFaktur: "5", HargaSatuanInput: "10000",
		}},
	})
	if err != nil {
		t.Fatalf("create pembelian di unit lain: %v", err)
	}

	list, _, err := testApp.pembelian.Search(ctx(), &model.ListPembelianRequest{AktifIDUnitKerja: &f.unitKerja})
	if err != nil {
		t.Fatalf("search pembelian: %v", err)
	}

	seen := map[int64]bool{}
	for _, p := range list {
		seen[p.ID] = true
	}
	if !seen[inside.ID] {
		t.Fatal("document inside the active unit is missing from its own scoped list")
	}
	if seen[outside.ID] {
		t.Fatal("document from a different unit leaked into a scoped list")
	}
}

func TestSusulanGetHidesDocumentOutsideActiveUnit(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := susulanFixture(t, testApp)
	susulan := buatSusulan(t, testApp, f, pembelian.ID, pembelian.Detail[0].ID, "3")

	unitLain := createUnit(t, testApp, "Unit Lain Susulan Get")

	_, err := testApp.susulan.Get(ctx(), &model.GetPenerimaanSusulanRequest{
		ID: susulan.ID, AktifIDUnitKerja: &unitLain,
	})

	assertKind(t, err, model.KindNotFound)
}

func TestSusulanGetAllowsDocumentInsideActiveUnit(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := susulanFixture(t, testApp)
	susulan := buatSusulan(t, testApp, f, pembelian.ID, pembelian.Detail[0].ID, "3")

	_, err := testApp.susulan.Get(ctx(), &model.GetPenerimaanSusulanRequest{
		ID: susulan.ID, AktifIDUnitKerja: &f.unitKerja,
	})
	if err != nil {
		t.Fatalf("get susulan inside active unit: %v", err)
	}
}

func TestSusulanListOnlyShowsActiveUnitDocuments(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := susulanFixture(t, testApp)
	inside := buatSusulan(t, testApp, f, pembelian.ID, pembelian.Detail[0].ID, "3")

	unitLain := createUnit(t, testApp, "Unit Lain Susulan List")

	list, _, err := testApp.susulan.Search(ctx(), &model.ListPenerimaanSusulanRequest{AktifIDUnitKerja: &f.unitKerja})
	if err != nil {
		t.Fatalf("search susulan: %v", err)
	}
	seen := map[int64]bool{}
	for _, s := range list {
		seen[s.ID] = true
	}
	if !seen[inside.ID] {
		t.Fatal("susulan inside the active unit is missing from its own scoped list")
	}

	listLain, _, err := testApp.susulan.Search(ctx(), &model.ListPenerimaanSusulanRequest{AktifIDUnitKerja: &unitLain})
	if err != nil {
		t.Fatalf("search susulan (unit lain): %v", err)
	}
	for _, s := range listLain {
		if s.ID == inside.ID {
			t.Fatal("susulan leaked into a different unit's scoped list")
		}
	}
}

func TestReturGetHidesDocumentOutsideActiveUnit(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := returFixture(t, testApp)
	retur := buatRetur(t, testApp, f, pembelian.ID, pembelian.Detail[0].ID, "10")

	unitLain := createUnit(t, testApp, "Unit Lain Retur Get")

	_, err := testApp.retur.Get(ctx(), &model.GetReturPembelianRequest{
		ID: retur.ID, AktifIDUnitKerja: &unitLain,
	})

	assertKind(t, err, model.KindNotFound)
}

func TestReturGetAllowsDocumentInsideActiveUnit(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := returFixture(t, testApp)
	retur := buatRetur(t, testApp, f, pembelian.ID, pembelian.Detail[0].ID, "10")

	_, err := testApp.retur.Get(ctx(), &model.GetReturPembelianRequest{
		ID: retur.ID, AktifIDUnitKerja: &f.unitKerja,
	})
	if err != nil {
		t.Fatalf("get retur inside active unit: %v", err)
	}
}

func TestReturListOnlyShowsActiveUnitDocuments(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := returFixture(t, testApp)
	inside := buatRetur(t, testApp, f, pembelian.ID, pembelian.Detail[0].ID, "10")

	unitLain := createUnit(t, testApp, "Unit Lain Retur List")

	listLain, _, err := testApp.retur.Search(ctx(), &model.ListReturPembelianRequest{AktifIDUnitKerja: &unitLain})
	if err != nil {
		t.Fatalf("search retur (unit lain): %v", err)
	}
	for _, r := range listLain {
		if r.ID == inside.ID {
			t.Fatal("retur leaked into a different unit's scoped list")
		}
	}

	list, _, err := testApp.retur.Search(ctx(), &model.ListReturPembelianRequest{AktifIDUnitKerja: &f.unitKerja})
	if err != nil {
		t.Fatalf("search retur: %v", err)
	}
	seen := map[int64]bool{}
	for _, r := range list {
		seen[r.ID] = true
	}
	if !seen[inside.ID] {
		t.Fatal("retur inside the active unit is missing from its own scoped list")
	}
}

// A mutasi whose SOURCE room sits outside the active unit is hidden, exactly like
// every other document — this is the ordinary case, checked here rather than
// re-derived from the mutasiScopeFixture tests in ruang_unit_scope_test.go, which
// only exercise the write path.
func TestMutasiGetHidesDocumentWhenSourceRuangOutsideActiveUnit(t *testing.T) {
	testApp := newApp(t)
	f, _, tujuan := mutasiScopeFixture(t, testApp)

	mutasi, err := testApp.mutasi.Create(ctx(), &model.CreateMutasiRequest{
		ActorID: f.actor, Tanggal: "2026-08-11", IDRuangAsal: f.ruang, IDRuangTujuan: tujuan,
	})
	if err != nil {
		t.Fatalf("create mutasi: %v", err)
	}

	unitLain := createUnit(t, testApp, "Unit Lain Mutasi Get Asal")

	_, err = testApp.mutasi.Get(ctx(), &model.GetMutasiRequest{
		ID: mutasi.ID, AktifIDUnitKerja: &unitLain,
	})

	assertKind(t, err, model.KindNotFound)
}

// The asymmetry that matters: a mutasi's DESTINATION room falling outside the
// active unit must NOT hide it. Only id_ruang_asal is ever checked, mirroring the
// write-side rule from fase 5 — cross-unit transfers are allowed by design (isu #12
// fase 1), and the caller who owns the source room may still see where the goods
// went.
func TestMutasiGetVisibleWhenOnlyDestinationRuangOutsideActiveUnit(t *testing.T) {
	testApp := newApp(t)
	f, unitTujuan, tujuan := mutasiScopeFixture(t, testApp)

	if f.unitKerja == unitTujuan {
		t.Fatal("fixture bug: source and destination must be in different units")
	}

	mutasi, err := testApp.mutasi.Create(ctx(), &model.CreateMutasiRequest{
		ActorID: f.actor, Tanggal: "2026-08-11", IDRuangAsal: f.ruang, IDRuangTujuan: tujuan,
	})
	if err != nil {
		t.Fatalf("create mutasi: %v", err)
	}

	// Active unit is the SOURCE room's unit, not the destination's.
	response, err := testApp.mutasi.Get(ctx(), &model.GetMutasiRequest{
		ID: mutasi.ID, AktifIDUnitKerja: &f.unitKerja,
	})
	if err != nil {
		t.Fatalf("get mutasi whose destination sits in a different unit: %v", err)
	}
	if response.ID != mutasi.ID {
		t.Fatalf("got mutasi %d, want %d", response.ID, mutasi.ID)
	}
}

func TestMutasiListOnlyFiltersBySourceRuangUnit(t *testing.T) {
	testApp := newApp(t)
	f, unitTujuan, tujuan := mutasiScopeFixture(t, testApp)

	mutasi, err := testApp.mutasi.Create(ctx(), &model.CreateMutasiRequest{
		ActorID: f.actor, Tanggal: "2026-08-11", IDRuangAsal: f.ruang, IDRuangTujuan: tujuan,
	})
	if err != nil {
		t.Fatalf("create mutasi: %v", err)
	}

	// Scoped to the SOURCE room's unit: visible.
	list, _, err := testApp.mutasi.Search(ctx(), &model.ListMutasiRequest{AktifIDUnitKerja: &f.unitKerja})
	if err != nil {
		t.Fatalf("search mutasi (unit asal): %v", err)
	}
	found := false
	for _, m := range list {
		if m.ID == mutasi.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("mutasi missing from a list scoped to its own source room's unit")
	}

	// Scoped to the DESTINATION room's unit instead: must NOT appear, since only
	// id_ruang_asal is ever checked.
	listTujuan, _, err := testApp.mutasi.Search(ctx(), &model.ListMutasiRequest{AktifIDUnitKerja: &unitTujuan})
	if err != nil {
		t.Fatalf("search mutasi (unit tujuan): %v", err)
	}
	for _, m := range listTujuan {
		if m.ID == mutasi.ID {
			t.Fatal("mutasi visible in a list scoped only to its destination room's unit")
		}
	}
}

func TestStokPerRuangOnlyShowsActiveUnitRooms(t *testing.T) {
	testApp := newApp(t)
	f, _, tujuan := mutasiScopeFixture(t, testApp)

	mutasi, err := testApp.mutasi.Create(ctx(), &model.CreateMutasiRequest{
		ActorID: f.actor, Tanggal: "2026-08-11", IDRuangAsal: f.ruang, IDRuangTujuan: tujuan,
		Detail: []model.MutasiDetailRequest{{IDProduct: f.product, IDSatuanInput: f.pcs, QtyInput: "10"}},
	})
	if err != nil {
		t.Fatalf("create mutasi: %v", err)
	}

	// Gives the source room actual stock to move: without a posted purchase behind
	// it, the mutasi posting below is refused by the negative-stock guard.
	pembelian := draftSederhana(t, testApp, f, "10", nil, nil)
	ajukanDanPosting(t, testApp, f, pembelian.ID)

	if _, err := testApp.mutasi.Posting(ctx(), &model.PostingMutasiRequest{ID: mutasi.ID, ActorID: f.actor}); err != nil {
		t.Fatalf("posting mutasi: %v", err)
	}

	// Scoped to the source room's unit: only the source room appears.
	list, err := testApp.product.Stok(ctx(), &model.ListStokProductRequest{
		IDProduct: f.product, AktifIDUnitKerja: &f.unitKerja,
	})
	if err != nil {
		t.Fatalf("stok per ruang (unit asal): %v", err)
	}
	for _, s := range list {
		if s.IDRuang == tujuan {
			t.Fatal("destination room from a different unit leaked into a scoped stok list")
		}
	}
	foundAsal := false
	for _, s := range list {
		if s.IDRuang == f.ruang {
			foundAsal = true
		}
	}
	if !foundAsal {
		t.Fatal("source room missing from its own scoped stok list")
	}

	// A global active context sees both rooms.
	listGlobal, err := testApp.product.Stok(ctx(), &model.ListStokProductRequest{IDProduct: f.product})
	if err != nil {
		t.Fatalf("stok per ruang (global): %v", err)
	}
	seen := map[int64]bool{}
	for _, s := range listGlobal {
		seen[s.IDRuang] = true
	}
	if !seen[f.ruang] || !seen[tujuan] {
		t.Fatal("an unscoped stok read must see every room the product has moved through")
	}
}

// ---------------------------------------------------------------------------
// pemakaian — isu #21 fase 2. Write side first (fase 5's rule, which isu #9
// originally opted out of), then read side (fase 6's), then the write-path
// re-read guard.
// ---------------------------------------------------------------------------

func TestPemakaianCreateRejectsRuangOutsideActiveUnit(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)
	pemohon := buatUserPolos(t, testApp, "pemohon_scope_create_reject")

	unitLain := createUnit(t, testApp, "Unit Lain Pemakaian Create")

	_, err := testApp.pemakaian.Create(ctx(), &model.CreatePemakaianRequest{
		ActorID: f.actor, AktifIDUnitKerja: &unitLain,
		Tanggal: "2026-08-11", IDRuang: f.ruang, IDPemohon: pemohon, Keperluan: "perbaikan",
	})

	assertKind(t, err, model.KindForbidden)
}

func TestPemakaianCreateAllowsRuangInsideActiveUnit(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)
	pemohon := buatUserPolos(t, testApp, "pemohon_scope_create_allow")

	_, err := testApp.pemakaian.Create(ctx(), &model.CreatePemakaianRequest{
		ActorID: f.actor, AktifIDUnitKerja: &f.unitKerja,
		Tanggal: "2026-08-11", IDRuang: f.ruang, IDPemohon: pemohon, Keperluan: "perbaikan",
	})
	if err != nil {
		t.Fatalf("create pemakaian di dalam unit aktif: %v", err)
	}
}

func TestPemakaianCreateAllowsAnyRuangWithGlobalActiveContext(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)
	pemohon := buatUserPolos(t, testApp, "pemohon_scope_create_global")

	_, err := testApp.pemakaian.Create(ctx(), &model.CreatePemakaianRequest{
		ActorID: f.actor, AktifIDUnitKerja: nil,
		Tanggal: "2026-08-11", IDRuang: f.ruang, IDPemohon: pemohon, Keperluan: "perbaikan",
	})
	if err != nil {
		t.Fatalf("create pemakaian dengan konteks aktif global: %v", err)
	}
}

func TestPemakaianUpdateRejectsMovingRuangOutsideActiveUnit(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)
	pemohon := buatUserPolos(t, testApp, "pemohon_scope_update_reject")

	created, err := testApp.pemakaian.Create(ctx(), &model.CreatePemakaianRequest{
		ActorID: f.actor, Tanggal: "2026-08-11", IDRuang: f.ruang, IDPemohon: pemohon, Keperluan: "perbaikan",
	})
	if err != nil {
		t.Fatalf("create pemakaian: %v", err)
	}

	unitLain := createUnit(t, testApp, "Unit Lain Pemakaian Update")
	ruangLain, err := testApp.ruang.Create(ctx(), &model.CreateRuangRequest{ActorID: f.actor, 
		NamaRuang: "Gudang Lain Pemakaian Update", IDUnitKerja: unitLain,
	})
	if err != nil {
		t.Fatalf("create ruang lain: %v", err)
	}

	_, err = testApp.pemakaian.Update(ctx(), &model.UpdatePemakaianRequest{
		ID: created.ID, ActorID: f.actor, AktifIDUnitKerja: &f.unitKerja,
		IDRuang: model.Optional[int64]{Present: true, Value: &ruangLain.ID},
	})

	assertKind(t, err, model.KindForbidden)
}

func TestPemakaianGetHidesDocumentOutsideActiveUnit(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)
	pemohon := buatUserPolos(t, testApp, "pemohon_scope_get_hide")

	created, err := testApp.pemakaian.Create(ctx(), &model.CreatePemakaianRequest{
		ActorID: f.actor, Tanggal: "2026-08-11", IDRuang: f.ruang, IDPemohon: pemohon, Keperluan: "perbaikan",
	})
	if err != nil {
		t.Fatalf("create pemakaian: %v", err)
	}

	unitLain := createUnit(t, testApp, "Unit Lain Pemakaian Get")

	_, err = testApp.pemakaian.Get(ctx(), &model.GetPemakaianRequest{
		ID: created.ID, AktifIDUnitKerja: &unitLain,
	})

	assertKind(t, err, model.KindNotFound)
}

func TestPemakaianGetAllowsDocumentInsideActiveUnit(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)
	pemohon := buatUserPolos(t, testApp, "pemohon_scope_get_allow")

	created, err := testApp.pemakaian.Create(ctx(), &model.CreatePemakaianRequest{
		ActorID: f.actor, Tanggal: "2026-08-11", IDRuang: f.ruang, IDPemohon: pemohon, Keperluan: "perbaikan",
	})
	if err != nil {
		t.Fatalf("create pemakaian: %v", err)
	}

	response, err := testApp.pemakaian.Get(ctx(), &model.GetPemakaianRequest{
		ID: created.ID, AktifIDUnitKerja: &f.unitKerja,
	})
	if err != nil {
		t.Fatalf("get pemakaian di dalam unit aktif: %v", err)
	}
	if response.ID != created.ID {
		t.Fatalf("got pemakaian %d, want %d", response.ID, created.ID)
	}
}

func TestPemakaianListOnlyShowsActiveUnitDocuments(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)
	pemohon := buatUserPolos(t, testApp, "pemohon_scope_list")

	inside, err := testApp.pemakaian.Create(ctx(), &model.CreatePemakaianRequest{
		ActorID: f.actor, Tanggal: "2026-08-11", IDRuang: f.ruang, IDPemohon: pemohon, Keperluan: "perbaikan",
	})
	if err != nil {
		t.Fatalf("create pemakaian di dalam unit: %v", err)
	}

	unitLain := createUnit(t, testApp, "Unit Lain Pemakaian List")
	ruangLain, err := testApp.ruang.Create(ctx(), &model.CreateRuangRequest{ActorID: f.actor, 
		NamaRuang: "Gudang Lain Pemakaian List", IDUnitKerja: unitLain,
	})
	if err != nil {
		t.Fatalf("create ruang lain: %v", err)
	}

	outside, err := testApp.pemakaian.Create(ctx(), &model.CreatePemakaianRequest{
		ActorID: f.actor, Tanggal: "2026-08-11", IDRuang: ruangLain.ID, IDPemohon: pemohon,
		Keperluan: "perbaikan lain",
	})
	if err != nil {
		t.Fatalf("create pemakaian di unit lain: %v", err)
	}

	list, _, err := testApp.pemakaian.Search(ctx(), &model.ListPemakaianRequest{AktifIDUnitKerja: &f.unitKerja})
	if err != nil {
		t.Fatalf("search pemakaian: %v", err)
	}

	seen := map[int64]bool{}
	for _, p := range list {
		seen[p.ID] = true
	}
	if !seen[inside.ID] {
		t.Fatal("document inside the active unit is missing from its own scoped list")
	}
	if seen[outside.ID] {
		t.Fatal("document from a different unit leaked into a scoped list")
	}
}

// A write action's own re-read of the document it just touched is never scoped
// by AktifIDUnitKerja — every write-path call to detail() passes nil regardless
// of who is calling. Patching a field that has nothing to do with id_ruang,
// while AktifIDUnitKerja names a wholly unrelated unit, must still return the
// document rather than 404: the caller who just acted on it is by construction
// allowed to see the result of their own action, and the patch itself must not
// be refused either, since id_ruang was never touched.
func TestPemakaianUpdateReReadNotScopedByActiveUnit(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)
	pemohon := buatUserPolos(t, testApp, "pemohon_scope_reread")

	created, err := testApp.pemakaian.Create(ctx(), &model.CreatePemakaianRequest{
		ActorID: f.actor, Tanggal: "2026-08-11", IDRuang: f.ruang, IDPemohon: pemohon, Keperluan: "awal",
	})
	if err != nil {
		t.Fatalf("create pemakaian: %v", err)
	}

	unitLain := createUnit(t, testApp, "Unit Lain Pemakaian ReRead")

	response, err := testApp.pemakaian.Update(ctx(), &model.UpdatePemakaianRequest{
		ID: created.ID, ActorID: f.actor, AktifIDUnitKerja: &unitLain,
		Keperluan: model.Optional[string]{Present: true, Value: ptr("revisi")},
	})
	if err != nil {
		t.Fatalf("update pemakaian dengan AktifIDUnitKerja tak terkait: %v", err)
	}
	if response.ID != created.ID {
		t.Fatalf("got pemakaian %d, want %d", response.ID, created.ID)
	}
}

// ---------------------------------------------------------------------------
// penjualan — isu #21 fase 2. Same shape as pemakaian above.
// ---------------------------------------------------------------------------

func TestPenjualanCreateRejectsRuangOutsideActiveUnit(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	unitLain := createUnit(t, testApp, "Unit Lain Penjualan Create")

	_, err := testApp.penjualan.Create(ctx(), &model.CreatePenjualanRequest{
		ActorID: f.actor, AktifIDUnitKerja: &unitLain,
		Tanggal: "2026-08-11", IDRuang: f.ruang,
	})

	assertKind(t, err, model.KindForbidden)
}

func TestPenjualanCreateAllowsRuangInsideActiveUnit(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	_, err := testApp.penjualan.Create(ctx(), &model.CreatePenjualanRequest{
		ActorID: f.actor, AktifIDUnitKerja: &f.unitKerja,
		Tanggal: "2026-08-11", IDRuang: f.ruang,
	})
	if err != nil {
		t.Fatalf("create penjualan di dalam unit aktif: %v", err)
	}
}

func TestPenjualanCreateAllowsAnyRuangWithGlobalActiveContext(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	_, err := testApp.penjualan.Create(ctx(), &model.CreatePenjualanRequest{
		ActorID: f.actor, AktifIDUnitKerja: nil,
		Tanggal: "2026-08-11", IDRuang: f.ruang,
	})
	if err != nil {
		t.Fatalf("create penjualan dengan konteks aktif global: %v", err)
	}
}

func TestPenjualanUpdateRejectsMovingRuangOutsideActiveUnit(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	created, err := testApp.penjualan.Create(ctx(), &model.CreatePenjualanRequest{
		ActorID: f.actor, Tanggal: "2026-08-11", IDRuang: f.ruang,
	})
	if err != nil {
		t.Fatalf("create penjualan: %v", err)
	}

	unitLain := createUnit(t, testApp, "Unit Lain Penjualan Update")
	ruangLain, err := testApp.ruang.Create(ctx(), &model.CreateRuangRequest{ActorID: f.actor, 
		NamaRuang: "Gudang Lain Penjualan Update", IDUnitKerja: unitLain,
	})
	if err != nil {
		t.Fatalf("create ruang lain: %v", err)
	}

	_, err = testApp.penjualan.Update(ctx(), &model.UpdatePenjualanRequest{
		ID: created.ID, ActorID: f.actor, AktifIDUnitKerja: &f.unitKerja,
		IDRuang: model.Optional[int64]{Present: true, Value: &ruangLain.ID},
	})

	assertKind(t, err, model.KindForbidden)
}

func TestPenjualanGetHidesDocumentOutsideActiveUnit(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	created, err := testApp.penjualan.Create(ctx(), &model.CreatePenjualanRequest{
		ActorID: f.actor, Tanggal: "2026-08-11", IDRuang: f.ruang,
	})
	if err != nil {
		t.Fatalf("create penjualan: %v", err)
	}

	unitLain := createUnit(t, testApp, "Unit Lain Penjualan Get")

	_, err = testApp.penjualan.Get(ctx(), &model.GetPenjualanRequest{
		ID: created.ID, AktifIDUnitKerja: &unitLain,
	})

	assertKind(t, err, model.KindNotFound)
}

func TestPenjualanGetAllowsDocumentInsideActiveUnit(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	created, err := testApp.penjualan.Create(ctx(), &model.CreatePenjualanRequest{
		ActorID: f.actor, Tanggal: "2026-08-11", IDRuang: f.ruang,
	})
	if err != nil {
		t.Fatalf("create penjualan: %v", err)
	}

	response, err := testApp.penjualan.Get(ctx(), &model.GetPenjualanRequest{
		ID: created.ID, AktifIDUnitKerja: &f.unitKerja,
	})
	if err != nil {
		t.Fatalf("get penjualan di dalam unit aktif: %v", err)
	}
	if response.ID != created.ID {
		t.Fatalf("got penjualan %d, want %d", response.ID, created.ID)
	}
}

func TestPenjualanListOnlyShowsActiveUnitDocuments(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	inside, err := testApp.penjualan.Create(ctx(), &model.CreatePenjualanRequest{
		ActorID: f.actor, Tanggal: "2026-08-11", IDRuang: f.ruang,
	})
	if err != nil {
		t.Fatalf("create penjualan di dalam unit: %v", err)
	}

	unitLain := createUnit(t, testApp, "Unit Lain Penjualan List")
	ruangLain, err := testApp.ruang.Create(ctx(), &model.CreateRuangRequest{ActorID: f.actor, 
		NamaRuang: "Gudang Lain Penjualan List", IDUnitKerja: unitLain,
	})
	if err != nil {
		t.Fatalf("create ruang lain: %v", err)
	}

	outside, err := testApp.penjualan.Create(ctx(), &model.CreatePenjualanRequest{
		ActorID: f.actor, Tanggal: "2026-08-11", IDRuang: ruangLain.ID,
	})
	if err != nil {
		t.Fatalf("create penjualan di unit lain: %v", err)
	}

	list, _, err := testApp.penjualan.Search(ctx(), &model.ListPenjualanRequest{AktifIDUnitKerja: &f.unitKerja})
	if err != nil {
		t.Fatalf("search penjualan: %v", err)
	}

	seen := map[int64]bool{}
	for _, p := range list {
		seen[p.ID] = true
	}
	if !seen[inside.ID] {
		t.Fatal("document inside the active unit is missing from its own scoped list")
	}
	if seen[outside.ID] {
		t.Fatal("document from a different unit leaked into a scoped list")
	}
}

// See TestPemakaianUpdateReReadNotScopedByActiveUnit for the invariant being pinned.
func TestPenjualanUpdateReReadNotScopedByActiveUnit(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	created, err := testApp.penjualan.Create(ctx(), &model.CreatePenjualanRequest{
		ActorID: f.actor, Tanggal: "2026-08-11", IDRuang: f.ruang,
	})
	if err != nil {
		t.Fatalf("create penjualan: %v", err)
	}

	unitLain := createUnit(t, testApp, "Unit Lain Penjualan ReRead")

	response, err := testApp.penjualan.Update(ctx(), &model.UpdatePenjualanRequest{
		ID: created.ID, ActorID: f.actor, AktifIDUnitKerja: &unitLain,
		Pembulatan: model.Optional[string]{Present: true, Value: ptr("100")},
	})
	if err != nil {
		t.Fatalf("update penjualan dengan AktifIDUnitKerja tak terkait: %v", err)
	}
	if response.ID != created.ID {
		t.Fatalf("got penjualan %d, want %d", response.ID, created.ID)
	}
}

// presensi (isu #40) is scoped by a column it already carries — presensi.id_unit_kerja,
// snapshotted from the session's active grant at tap time — so unlike the six modules
// above it needs no join to ruang and no extra read column. Every presensi read is
// list-shaped, so rows outside the active unit are omitted silently and nothing here
// ever answers 404 for scoping.
func TestPresensiListOnlyShowsActiveUnitRows(t *testing.T) {
	testApp := newApp(t)
	f := newPresensiFixture(t, testApp)

	masukPada(t, testApp, f, jamUji(2026, time.March, 17, 7, 5), &f.unitKerja)

	unitLain := createUnit(t, testApp, "Unit Lain Presensi")

	list, paging, err := testApp.presensi.Search(ctx(), &model.ListPresensiRequest{
		PageRequest: model.PageRequest{Page: 1, Size: 20}, AktifIDUnitKerja: &unitLain,
	})
	if err != nil {
		t.Fatalf("list presensi dari unit lain: %v", err)
	}

	if len(list) != 0 || paging.TotalItem != 0 {
		t.Fatalf("baris %d / total_item %d, want 0/0 — presensi unit lain bocor", len(list), paging.TotalItem)
	}

	// The COUNT has to agree with the rows: both run against the same FROM and the
	// same filter, which is exactly the correction four other modules needed when
	// fase 6 first landed.
	milikSendiri, paging, err := testApp.presensi.Search(ctx(), &model.ListPresensiRequest{
		PageRequest: model.PageRequest{Page: 1, Size: 20}, AktifIDUnitKerja: &f.unitKerja,
	})
	if err != nil {
		t.Fatalf("list presensi unit sendiri: %v", err)
	}

	if len(milikSendiri) != 1 || paging.TotalItem != 1 {
		t.Fatalf("baris %d / total_item %d, want 1/1", len(milikSendiri), paging.TotalItem)
	}
}

// The recap is scoped the same way, and it matters more there than in the list: a
// recap that silently counted another unit's days would be wrong in a number rather
// than in a row somebody can see.
func TestPresensiRekapOnlyCountsActiveUnitRows(t *testing.T) {
	testApp := newApp(t)
	f := newPresensiFixture(t, testApp)

	masukPada(t, testApp, f, jamUji(2026, time.March, 17, 7, 5), &f.unitKerja)

	unitLain := createUnit(t, testApp, "Unit Lain Rekap Presensi")

	list, _, err := testApp.presensi.Rekap(ctx(), &model.ListRekapPresensiRequest{
		PageRequest: model.PageRequest{Page: 1, Size: 20},
		Tahun:       2026, Bulan: 3, AktifIDUnitKerja: &unitLain,
	})
	if err != nil {
		t.Fatalf("rekap dari unit lain: %v", err)
	}

	if len(list) != 0 {
		t.Fatalf("rekap unit lain = %d baris, want 0", len(list))
	}
}

// A row whose id_unit_kerja is NULL — made by a caller holding a global grant, or
// one who never switched context — is visible only to a caller who is themselves
// global. Showing it to a unit-bound caller would hand them attendance from any
// unit at all, since the row itself does not say which one it belongs to.
func TestPresensiRowTanpaUnitHanyaTerlihatSecaraGlobal(t *testing.T) {
	testApp := newApp(t)
	f := newPresensiFixture(t, testApp)

	// A tap made under no active context (a global grant, or several grants
	// not yet switched to one) carries no unit at all — nil, exactly as
	// AktifIDUnitKerja is nil here.
	masukPada(t, testApp, f, jamUji(2026, time.March, 17, 7, 5), nil)

	terikat, _, err := testApp.presensi.Search(ctx(), &model.ListPresensiRequest{
		PageRequest: model.PageRequest{Page: 1, Size: 20}, AktifIDUnitKerja: &f.unitKerja,
	})
	if err != nil {
		t.Fatalf("list terikat unit: %v", err)
	}

	if len(terikat) != 0 {
		t.Fatalf("baris ber-unit NULL terlihat oleh pemanggil terikat unit: %d baris", len(terikat))
	}

	global, _, err := testApp.presensi.Search(ctx(), &model.ListPresensiRequest{
		PageRequest: model.PageRequest{Page: 1, Size: 20}, AktifIDUnitKerja: nil,
	})
	if err != nil {
		t.Fatalf("list global: %v", err)
	}

	if len(global) != 1 {
		t.Fatalf("baris terlihat secara global = %d, want 1", len(global))
	}
}

// GET /presensi/saya is deliberately NOT scoped: your own history is yours,
// including the days you were working at another unit. This is the one read in the
// project where leaving the scope off is the decision rather than the omission, so
// it gets a test of its own rather than being left to the controller's nil.
func TestPresensiSayaTidakDisaringUnitKerja(t *testing.T) {
	testApp := newApp(t)
	f := newPresensiFixture(t, testApp)

	unitLain := createUnit(t, testApp, "Unit Lain Presensi Saya")

	// A day worked at another unit entirely.
	masukPada(t, testApp, f, jamUji(2026, time.March, 17, 7, 5), &unitLain)

	// What the controller passes for /presensi/saya: id_user forced to the
	// caller, AktifIDUnitKerja forced to nil regardless of the caller's own
	// active unit.
	riwayat, _, err := testApp.presensi.Search(ctx(), &model.ListPresensiRequest{
		PageRequest: model.PageRequest{Page: 1, Size: 20},
		IDUser:      &f.karyawan, AktifIDUnitKerja: nil,
	})
	if err != nil {
		t.Fatalf("presensi saya: %v", err)
	}

	if len(riwayat) != 1 {
		t.Fatalf("riwayat sendiri kehilangan hari di unit lain: %d baris", len(riwayat))
	}
}

// ---------------------------------------------------------------------------
// saldo_awal (isu #43) — same shape as pemakaian and penjualan: id_ruang checked on the
// write side (fase 5), the read side scoped by the room's unit (fase 6), and the write
// path's own re-read never scoped.
// ---------------------------------------------------------------------------

func TestSaldoAwalCreateRejectsRuangOutsideActiveUnit(t *testing.T) {
	testApp, s := saldoAwalFixture(t)
	unitLain := createUnit(t, testApp, "Unit Lain Saldo Awal Create")

	_, err := testApp.saldoAwal.Create(ctx(), &model.CreateSaldoAwalRequest{
		ActorID: s.actor, AktifIDUnitKerja: &unitLain,
		Tanggal: "2026-08-11", IDRuang: s.ruang, Alasan: "migrasi",
	})

	assertKind(t, err, model.KindForbidden)
}

func TestSaldoAwalUpdateRejectsMovingRuangOutsideActiveUnit(t *testing.T) {
	testApp, s := saldoAwalFixture(t)
	created := buatSaldoAwal(t, testApp, s, "2026-08-11")

	unitLain := createUnit(t, testApp, "Unit Lain Saldo Awal Update")
	ruangLain, err := testApp.ruang.Create(ctx(), &model.CreateRuangRequest{
		ActorID: s.actor, NamaRuang: "Gudang Lain Saldo Awal Update", IDUnitKerja: unitLain,
	})
	if err != nil {
		t.Fatalf("create ruang lain: %v", err)
	}

	_, err = testApp.saldoAwal.Update(ctx(), &model.UpdateSaldoAwalRequest{
		ID: created.ID, ActorID: s.actor, AktifIDUnitKerja: &s.unitKerja,
		IDRuang: model.Optional[int64]{Present: true, Value: &ruangLain.ID},
	})

	assertKind(t, err, model.KindForbidden)
}

func TestSaldoAwalGetHidesDocumentOutsideActiveUnit(t *testing.T) {
	testApp, s := saldoAwalFixture(t)
	created := buatSaldoAwal(t, testApp, s, "2026-08-11")

	unitLain := createUnit(t, testApp, "Unit Lain Saldo Awal Get")

	_, err := testApp.saldoAwal.Get(ctx(), &model.GetSaldoAwalRequest{
		ID: created.ID, AktifIDUnitKerja: &unitLain,
	})
	assertKind(t, err, model.KindNotFound)

	response, err := testApp.saldoAwal.Get(ctx(), &model.GetSaldoAwalRequest{
		ID: created.ID, AktifIDUnitKerja: &s.unitKerja,
	})
	if err != nil {
		t.Fatalf("get saldo_awal di dalam unit aktif: %v", err)
	}
	if response.ID != created.ID {
		t.Fatalf("got saldo_awal %d, want %d", response.ID, created.ID)
	}
}

func TestSaldoAwalListOnlyShowsActiveUnitDocuments(t *testing.T) {
	testApp, s := saldoAwalFixture(t)
	inside := buatSaldoAwal(t, testApp, s, "2026-08-11")

	unitLain := createUnit(t, testApp, "Unit Lain Saldo Awal List")
	ruangLain, err := testApp.ruang.Create(ctx(), &model.CreateRuangRequest{
		ActorID: s.actor, NamaRuang: "Gudang Lain Saldo Awal List", IDUnitKerja: unitLain,
	})
	if err != nil {
		t.Fatalf("create ruang lain: %v", err)
	}

	outside, err := testApp.saldoAwal.Create(ctx(), &model.CreateSaldoAwalRequest{
		ActorID: s.actor, Tanggal: "2026-08-11", IDRuang: ruangLain.ID, Alasan: "migrasi unit lain",
	})
	if err != nil {
		t.Fatalf("create saldo_awal di unit lain: %v", err)
	}

	list, paging, err := testApp.saldoAwal.Search(ctx(), &model.ListSaldoAwalRequest{AktifIDUnitKerja: &s.unitKerja})
	if err != nil {
		t.Fatalf("search saldo_awal: %v", err)
	}

	seen := map[int64]bool{}
	for _, d := range list {
		seen[d.ID] = true
	}
	if !seen[inside.ID] {
		t.Fatal("document inside the active unit is missing from its own scoped list")
	}
	if seen[outside.ID] {
		t.Fatal("document from a different unit leaked into a scoped list")
	}
	if paging.TotalItem != 1 {
		t.Fatalf("total_item = %d, want 1 (the COUNT must share the scoped FROM)", paging.TotalItem)
	}
}

// A write action's own re-read is never scoped by AktifIDUnitKerja: a caller who just
// acted on a document is by construction allowed to see the result of their own action.
func TestSaldoAwalWritePathReReadNotScopedByActiveUnit(t *testing.T) {
	testApp, s := saldoAwalFixture(t)
	created := buatSaldoAwal(t, testApp, s, "2026-08-11")

	unitLain := createUnit(t, testApp, "Unit Lain Saldo Awal ReRead")

	response, err := testApp.saldoAwal.Update(ctx(), &model.UpdateSaldoAwalRequest{
		ID: created.ID, ActorID: s.actor, AktifIDUnitKerja: &unitLain,
		Alasan: model.Optional[string]{Present: true, Value: ptr("revisi")},
	})
	if err != nil {
		t.Fatalf("update saldo_awal yang tidak menyentuh id_ruang: %v", err)
	}
	if response.Alasan != "revisi" {
		t.Errorf("alasan = %q, want revisi", response.Alasan)
	}
}
