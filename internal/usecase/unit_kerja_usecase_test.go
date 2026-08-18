package usecase_test

import (
	"encoding/json"
	"testing"

	"Arthafreestyle/ERP/internal/model"
)

func TestUnitKerjaDuplicateKodeIsConflict(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)

	if _, err := a.unitKerja.Create(ctx(), &model.CreateUnitKerjaRequest{
		ActorID: actor,
		Kode:    ptr("PUSAT"),
		Nama:    "Unit Satu",
	}); err != nil {
		t.Fatalf("create first: %v", err)
	}

	_, err := a.unitKerja.Create(ctx(), &model.CreateUnitKerjaRequest{
		ActorID: actor,
		Kode:    ptr("PUSAT"),
		Nama:    "Unit Dua",
	})
	assertKind(t, err, model.KindConflict)
}

// Decision 1 of isu #2, reused here: uniqueness ignores case, so PUSAT and pusat
// are the same code rather than two units an operator cannot tell apart.
func TestUnitKerjaDuplicateKodeIgnoresCase(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)

	if _, err := a.unitKerja.Create(ctx(), &model.CreateUnitKerjaRequest{
		ActorID: actor,
		Kode:    ptr("PUSAT"),
		Nama:    "Unit Satu",
	}); err != nil {
		t.Fatalf("create first: %v", err)
	}

	_, err := a.unitKerja.Create(ctx(), &model.CreateUnitKerjaRequest{
		ActorID: actor,
		Kode:    ptr("pusat"),
		Nama:    "Unit Dua",
	})
	assertKind(t, err, model.KindConflict)
}

// A unique index does not constrain NULLs, so any number of units may have no
// code at all.
func TestUnitKerjaManyNullKodeAreAllowed(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)

	for i, nama := range []string{"Tanpa Kode A", "Tanpa Kode B", "Tanpa Kode C"} {
		response, err := a.unitKerja.Create(ctx(), &model.CreateUnitKerjaRequest{ActorID: actor, Nama: nama})
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}

		if response.Kode != nil {
			t.Errorf("kode = %v, want nil", *response.Kode)
		}
	}

	_, paging, err := a.unitKerja.Search(ctx(), &model.ListUnitKerjaRequest{})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if paging.TotalItem != 3 {
		t.Errorf("total_item = %d, want 3", paging.TotalItem)
	}
}

// Same PATCH promise as supplier: an explicit null clears kode, an absent key
// leaves it alone.
func TestUnitKerjaPatchDistinguishesNullFromAbsent(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)

	created, err := a.unitKerja.Create(ctx(), &model.CreateUnitKerjaRequest{
		ActorID: actor,
		Kode:    ptr("OUT-01"),
		Nama:    "Outlet Satu",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	patched := patchUnitKerja(t, a, created.ID, actor, `{"nama":"Outlet Satu Baru"}`)

	if patched.Kode == nil || *patched.Kode != "OUT-01" {
		t.Errorf("kode = %v after a body that omitted it, want OUT-01", patched.Kode)
	}

	patched = patchUnitKerja(t, a, created.ID, actor, `{"kode":null}`)

	if patched.Kode != nil {
		t.Errorf("kode = %v after an explicit null, want nil", *patched.Kode)
	}

	if patched.Nama != "Outlet Satu Baru" {
		t.Errorf("nama = %q after a body that omitted it, want Outlet Satu Baru", patched.Nama)
	}
}

func TestUnitKerjaPatchRejectsNullOnNotNullColumn(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)

	created, err := a.unitKerja.Create(ctx(), &model.CreateUnitKerjaRequest{ActorID: actor, Nama: "Unit Nama"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	request := decodeUnitKerjaPatch(t, created.ID, actor, `{"nama":null}`)

	_, err = a.unitKerja.Update(ctx(), request)
	assertKind(t, err, model.KindInvalid)
}

// A PATCH may not move a row to a code another unit already holds, and it must
// not collide with the row's own code either.
func TestUnitKerjaPatchKodeConflicts(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)

	if _, err := a.unitKerja.Create(ctx(), &model.CreateUnitKerjaRequest{
		ActorID: actor,
		Kode:    ptr("UNIT-A"),
		Nama:    "Unit A",
	}); err != nil {
		t.Fatalf("create first: %v", err)
	}

	second, err := a.unitKerja.Create(ctx(), &model.CreateUnitKerjaRequest{
		ActorID: actor,
		Kode:    ptr("UNIT-B"),
		Nama:    "Unit B",
	})
	if err != nil {
		t.Fatalf("create second: %v", err)
	}

	_, err = a.unitKerja.Update(ctx(), decodeUnitKerjaPatch(t, second.ID, actor, `{"kode":"UNIT-A"}`))
	assertKind(t, err, model.KindConflict)

	// Re-sending its own code is not a conflict: exceptID skips the row itself.
	patched := patchUnitKerja(t, a, second.ID, actor, `{"kode":"UNIT-B"}`)
	if patched.Kode == nil || *patched.Kode != "UNIT-B" {
		t.Errorf("kode = %v, want UNIT-B", patched.Kode)
	}
}

// ruang.id_unit_kerja is required and validated active — isu #12 fase 2. An
// unknown id must not reach the foreign key as a 500.
func TestRuangCreateRejectsUnknownUnitKerja(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)

	_, err := a.ruang.Create(ctx(), &model.CreateRuangRequest{
		ActorID:     actor,
		NamaRuang:   "Gudang Tanpa Unit",
		IDUnitKerja: 999_999,
	})
	assertKind(t, err, model.KindInvalid)
}

// A retired unit_kerja must be refused the same as an unknown one: is_aktif is
// what ExistsActiveByID checks, and the foreign key alone cannot see it.
func TestRuangCreateRejectsRetiredUnitKerja(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)

	unit, err := a.unitKerja.Create(ctx(), &model.CreateUnitKerjaRequest{ActorID: actor, Nama: "Unit Pensiun"})
	if err != nil {
		t.Fatalf("create unit: %v", err)
	}

	if _, err := a.unitKerja.Update(ctx(), &model.UpdateUnitKerjaRequest{
		ID:      unit.ID,
		ActorID: actor,
		IsAktif: model.Optional[bool]{Present: true, Value: ptr(false)},
	}); err != nil {
		t.Fatalf("retire unit: %v", err)
	}

	_, err = a.ruang.Create(ctx(), &model.CreateRuangRequest{
		ActorID:     actor,
		NamaRuang:   "Gudang Unit Pensiun",
		IDUnitKerja: unit.ID,
	})
	assertKind(t, err, model.KindInvalid)
}

// The response carries the unit's name from the JOIN, not just its id.
func TestRuangResponseIncludesUnitKerjaName(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)

	unit, err := a.unitKerja.Create(ctx(), &model.CreateUnitKerjaRequest{ActorID: actor, Nama: "Outlet Utama"})
	if err != nil {
		t.Fatalf("create unit: %v", err)
	}

	created, err := a.ruang.Create(ctx(), &model.CreateRuangRequest{
		ActorID:     actor,
		NamaRuang:   "Gudang Outlet",
		IDUnitKerja: unit.ID,
	})
	if err != nil {
		t.Fatalf("create ruang: %v", err)
	}

	if created.IDUnitKerja != unit.ID {
		t.Errorf("id_unit_kerja = %d, want %d", created.IDUnitKerja, unit.ID)
	}
	if created.NamaUnitKerja != "Outlet Utama" {
		t.Errorf("nama_unit_kerja = %q, want Outlet Utama", created.NamaUnitKerja)
	}

	reread, err := a.ruang.Get(ctx(), &model.GetRuangRequest{ID: created.ID})
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if reread.NamaUnitKerja != "Outlet Utama" {
		t.Errorf("nama_unit_kerja on Get = %q, want Outlet Utama", reread.NamaUnitKerja)
	}
}

func decodeUnitKerjaPatch(t *testing.T, id, actor int64, body string) *model.UpdateUnitKerjaRequest {
	t.Helper()

	request := new(model.UpdateUnitKerjaRequest)
	if err := json.Unmarshal([]byte(body), request); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}

	request.ID = id
	request.ActorID = actor

	return request
}

func patchUnitKerja(t *testing.T, a *app, id, actor int64, body string) *model.UnitKerjaResponse {
	t.Helper()

	response, err := a.unitKerja.Update(ctx(), decodeUnitKerjaPatch(t, id, actor, body))
	if err != nil {
		t.Fatalf("patch %s: %v", body, err)
	}

	return response
}
