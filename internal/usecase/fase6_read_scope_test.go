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

import (
	"testing"

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
	ruangLain, err := testApp.ruang.Create(ctx(), &model.CreateRuangRequest{
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
	ruangLain, err := testApp.ruang.Create(ctx(), &model.CreateRuangRequest{
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
