package usecase_test

// isu #12 fase 5: id_ruang on a write path is validated against the caller's
// active unit_kerja, not merely defaulted from it. pembelian and mutasi are
// the only two modules whose request body names a room directly — susulan and
// retur_pembelian copy id_ruang from their parent pembelian and so need no
// check of their own, and periksaRuangUnitAktif is the one function both
// modules under test share.

import (
	"testing"

	"Arthafreestyle/ERP/internal/model"
)

// A write naming a room outside the caller's active unit is refused 403 —
// checked here on pembelian, the simplest of the two modules with an id_ruang
// in the body.
func TestPembelianCreateRejectsRuangOutsideActiveUnit(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	unitLain := createUnit(t, testApp, "Unit Lain Pembelian")

	_, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
		ActorID:          f.actor,
		AktifIDUnitKerja: &unitLain,
		Tanggal:          "2026-08-11",
		IDSupplier:       f.supplier,
		IDRuang:          f.ruang,
		Detail: []model.PembelianDetailRequest{{
			IDProduct:        f.product,
			IDSatuanInput:    f.pcs,
			QtyFaktur:        "10",
			HargaSatuanInput: "10000",
		}},
	})

	assertKind(t, err, model.KindForbidden)
}

// A write naming a room inside the caller's active unit succeeds — the
// ordinary case, and what proves the check compares rather than always
// refusing.
func TestPembelianCreateAllowsRuangInsideActiveUnit(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	_, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
		ActorID:          f.actor,
		AktifIDUnitKerja: &f.unitKerja,
		Tanggal:          "2026-08-11",
		IDSupplier:       f.supplier,
		IDRuang:          f.ruang,
		Detail: []model.PembelianDetailRequest{{
			IDProduct:        f.product,
			IDSatuanInput:    f.pcs,
			QtyFaktur:        "10",
			HargaSatuanInput: "10000",
		}},
	})
	if err != nil {
		t.Fatalf("create pembelian: %v", err)
	}
}

// A nil AktifIDUnitKerja — the caller's active grant applies to every unit,
// the SUPERADMIN shape — must not restrict id_ruang at all.
func TestPembelianCreateAllowsAnyRuangWithGlobalActiveContext(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	_, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
		ActorID:          f.actor,
		AktifIDUnitKerja: nil,
		Tanggal:          "2026-08-11",
		IDSupplier:       f.supplier,
		IDRuang:          f.ruang,
		Detail: []model.PembelianDetailRequest{{
			IDProduct:        f.product,
			IDSatuanInput:    f.pcs,
			QtyFaktur:        "10",
			HargaSatuanInput: "10000",
		}},
	})
	if err != nil {
		t.Fatalf("create pembelian with a global active context: %v", err)
	}
}

// mutasiScopeFixture builds a purchase fixture (source room, in its own unit)
// plus a destination room in a second, different unit — the shape every test
// below needs to exercise cross-unit transfers.
func mutasiScopeFixture(t *testing.T, testApp *app) (f fixture, unitTujuan, tujuan int64) {
	t.Helper()

	f = pembelianFixture(t, testApp)
	unitTujuan = createUnit(t, testApp, "Unit Tujuan Mutasi Scope")
	room, err := testApp.ruang.Create(ctx(), &model.CreateRuangRequest{
		ActorID:     f.actor,
		NamaRuang:   "Toko Tujuan Scope",
		IDUnitKerja: unitTujuan,
	})
	if err != nil {
		t.Fatalf("create ruang tujuan: %v", err)
	}

	return f, unitTujuan, room.ID
}

// id_ruang_asal outside the caller's active unit is refused — the caller is
// asserting authority over a room they do not hold, in the direction that
// actually matters: goods are said to be leaving that room under their name.
func TestMutasiCreateRejectsSourceRuangOutsideActiveUnit(t *testing.T) {
	testApp := newApp(t)
	f, _, tujuan := mutasiScopeFixture(t, testApp)

	unitLain := createUnit(t, testApp, "Unit Lain Mutasi Asal")

	_, err := testApp.mutasi.Create(ctx(), &model.CreateMutasiRequest{
		ActorID:          f.actor,
		AktifIDUnitKerja: &unitLain,
		Tanggal:          "2026-08-11",
		IDRuangAsal:      f.ruang,
		IDRuangTujuan:    tujuan,
	})

	assertKind(t, err, model.KindForbidden)
}

// id_ruang_tujuan in a different unit than the caller's active one must NOT
// be refused — isu #12 fase 1 already decided cross-unit mutasi is allowed,
// and fase 5's scoping only ever looks at id_ruang_asal.
func TestMutasiCreateAllowsCrossUnitDestination(t *testing.T) {
	testApp := newApp(t)
	f, unitTujuan, tujuan := mutasiScopeFixture(t, testApp)

	if f.unitKerja == unitTujuan {
		t.Fatal("fixture bug: source and destination must be in different units")
	}

	_, err := testApp.mutasi.Create(ctx(), &model.CreateMutasiRequest{
		ActorID:          f.actor,
		AktifIDUnitKerja: &f.unitKerja,
		Tanggal:          "2026-08-11",
		IDRuangAsal:      f.ruang,
		IDRuangTujuan:    tujuan,
	})
	if err != nil {
		t.Fatalf("create mutasi with a cross-unit destination: %v", err)
	}
}

// A global active context (nil) does not restrict id_ruang_asal either.
func TestMutasiCreateAllowsAnySourceWithGlobalActiveContext(t *testing.T) {
	testApp := newApp(t)
	f, _, tujuan := mutasiScopeFixture(t, testApp)

	_, err := testApp.mutasi.Create(ctx(), &model.CreateMutasiRequest{
		ActorID:          f.actor,
		AktifIDUnitKerja: nil,
		Tanggal:          "2026-08-11",
		IDRuangAsal:      f.ruang,
		IDRuangTujuan:    tujuan,
	})
	if err != nil {
		t.Fatalf("create mutasi with a global active context: %v", err)
	}
}

// Changing id_ruang_asal via PATCH to a room outside the active unit is
// refused the same way Create is — "both rooms may change while DRAFT" must
// not be a way around the check.
func TestMutasiUpdateRejectsMovingSourceRuangOutsideActiveUnit(t *testing.T) {
	testApp := newApp(t)
	f, _, tujuan := mutasiScopeFixture(t, testApp)

	created, err := testApp.mutasi.Create(ctx(), &model.CreateMutasiRequest{
		ActorID:       f.actor,
		Tanggal:       "2026-08-11",
		IDRuangAsal:   f.ruang,
		IDRuangTujuan: tujuan,
	})
	if err != nil {
		t.Fatalf("create mutasi: %v", err)
	}

	unitLain := createUnit(t, testApp, "Unit Lain Mutasi Patch")
	ruangLain, err := testApp.ruang.Create(ctx(), &model.CreateRuangRequest{
		ActorID:     f.actor,
		NamaRuang:   "Gudang Lain Patch",
		IDUnitKerja: unitLain,
	})
	if err != nil {
		t.Fatalf("create ruang lain: %v", err)
	}

	_, err = testApp.mutasi.Update(ctx(), &model.UpdateMutasiRequest{
		ID:               created.ID,
		ActorID:          f.actor,
		AktifIDUnitKerja: &f.unitKerja,
		IDRuangAsal:      model.Optional[int64]{Present: true, Value: &ruangLain.ID},
	})

	assertKind(t, err, model.KindForbidden)
}

// A PATCH that leaves id_ruang_asal untouched must not be re-validated
// against it — the stored room was already valid, and ruang has no PATCH of
// its own, so its unit cannot have changed since.
func TestMutasiUpdateDoesNotRevalidateUnchangedSourceRuang(t *testing.T) {
	testApp := newApp(t)
	f, _, tujuan := mutasiScopeFixture(t, testApp)

	created, err := testApp.mutasi.Create(ctx(), &model.CreateMutasiRequest{
		ActorID:       f.actor,
		Tanggal:       "2026-08-11",
		IDRuangAsal:   f.ruang,
		IDRuangTujuan: tujuan,
	})
	if err != nil {
		t.Fatalf("create mutasi: %v", err)
	}

	unitLain := createUnit(t, testApp, "Unit Lain Mutasi Keterangan")

	// AktifIDUnitKerja names a unit the stored id_ruang_asal is NOT in, but the
	// patch never touches id_ruang_asal — only keterangan — so nothing here
	// should be checked at all.
	_, err = testApp.mutasi.Update(ctx(), &model.UpdateMutasiRequest{
		ID:               created.ID,
		ActorID:          f.actor,
		AktifIDUnitKerja: &unitLain,
		Keterangan:       model.Optional[string]{Present: true, Value: ptr("catatan")},
	})
	if err != nil {
		t.Fatalf("patch keterangan alone should not trigger id_ruang_asal validation: %v", err)
	}
}
