package usecase_test

import (
	"encoding/json"
	"testing"

	"Arthafreestyle/ERP/internal/model"
)

func TestSupplierDuplicateKodeIsConflict(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)

	if _, err := a.supplier.Create(ctx(), &model.CreateSupplierRequest{
		ActorID: actor,
		Kode:    ptr("SUP-01"),
		Nama:    "Supplier Satu",
	}); err != nil {
		t.Fatalf("create first: %v", err)
	}

	_, err := a.supplier.Create(ctx(), &model.CreateSupplierRequest{
		ActorID: actor,
		Kode:    ptr("SUP-01"),
		Nama:    "Supplier Dua",
	})
	assertKind(t, err, model.KindConflict)
}

// Decision 1 of the issue: uniqueness ignores case, so SUP-01 and sup-01 are the
// same code rather than two suppliers an operator cannot tell apart.
func TestSupplierDuplicateKodeIgnoresCase(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)

	if _, err := a.supplier.Create(ctx(), &model.CreateSupplierRequest{
		ActorID: actor,
		Kode:    ptr("SUP-01"),
		Nama:    "Supplier Satu",
	}); err != nil {
		t.Fatalf("create first: %v", err)
	}

	_, err := a.supplier.Create(ctx(), &model.CreateSupplierRequest{
		ActorID: actor,
		Kode:    ptr("sup-01"),
		Nama:    "Supplier Dua",
	})
	assertKind(t, err, model.KindConflict)
}

// A unique index does not constrain NULLs, so any number of suppliers may have no
// code at all. Rejecting the second one would be wrong.
func TestSupplierManyNullKodeAreAllowed(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)

	for i, nama := range []string{"Tanpa Kode A", "Tanpa Kode B", "Tanpa Kode C"} {
		response, err := a.supplier.Create(ctx(), &model.CreateSupplierRequest{ActorID: actor, Nama: nama})
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}

		if response.Kode != nil {
			t.Errorf("kode = %v, want nil", *response.Kode)
		}
	}

	_, paging, err := a.supplier.Search(ctx(), &model.ListSupplierRequest{})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if paging.TotalItem != 3 {
		t.Errorf("total_item = %d, want 3", paging.TotalItem)
	}
}

// The whole reason PATCH DTOs use model.Optional: an explicit null must clear the
// column, while an absent key must not. Bodies are decoded from JSON here so the
// test covers the same path the Fiber binder takes.
func TestSupplierPatchDistinguishesNullFromAbsent(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)

	created, err := a.supplier.Create(ctx(), &model.CreateSupplierRequest{
		ActorID: actor,
		Kode:    ptr("SUP-09"),
		Nama:    "Supplier Sembilan",
		Telepon: ptr("0800-1111"),
		Alamat:  ptr("Jl. Awal 1"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Body without telepon: the number must survive.
	patched := patchSupplier(t, a, created.ID, actor, `{"alamat":"Jl. Baru 2"}`)

	if patched.Telepon == nil || *patched.Telepon != "0800-1111" {
		t.Errorf("telepon = %v after a body that omitted it, want 0800-1111", patched.Telepon)
	}

	if patched.Alamat == nil || *patched.Alamat != "Jl. Baru 2" {
		t.Errorf("alamat = %v, want Jl. Baru 2", patched.Alamat)
	}

	// Body with telepon: null — the number must be cleared. This is what COALESCE
	// would silently refuse to do.
	patched = patchSupplier(t, a, created.ID, actor, `{"telepon":null}`)

	if patched.Telepon != nil {
		t.Errorf("telepon = %v after an explicit null, want nil", *patched.Telepon)
	}

	if patched.Alamat == nil || *patched.Alamat != "Jl. Baru 2" {
		t.Errorf("alamat = %v after a body that omitted it, want Jl. Baru 2", patched.Alamat)
	}

	// Reading the row back proves the value was cleared in the database, not just
	// in the RETURNING projection.
	reread, err := a.supplier.Get(ctx(), &model.GetSupplierRequest{ID: created.ID})
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if reread.Telepon != nil {
		t.Errorf("telepon = %v after re-reading, want nil", *reread.Telepon)
	}
}

// nama is NOT NULL: it can be changed, never cleared. Without this check the null
// would reach the database and surface as a 500.
func TestSupplierPatchRejectsNullOnNotNullColumn(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)

	created, err := a.supplier.Create(ctx(), &model.CreateSupplierRequest{ActorID: actor, Nama: "Supplier Nama"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	request := decodeSupplierPatch(t, created.ID, actor, `{"nama":null}`)

	_, err = a.supplier.Update(ctx(), request)
	assertKind(t, err, model.KindInvalid)
}

func TestSupplierPatchUnknownIDIsNotFound(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)

	request := decodeSupplierPatch(t, 999_999, actor, `{"nama":"Tidak Ada"}`)

	_, err := a.supplier.Update(ctx(), request)
	assertKind(t, err, model.KindNotFound)
}

// A PATCH may not move a row to a code another supplier already holds, and it
// must not collide with the row's own code either.
func TestSupplierPatchKodeConflicts(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)

	if _, err := a.supplier.Create(ctx(), &model.CreateSupplierRequest{
		ActorID: actor,
		Kode:    ptr("SUP-A"),
		Nama:    "Supplier A",
	}); err != nil {
		t.Fatalf("create first: %v", err)
	}

	second, err := a.supplier.Create(ctx(), &model.CreateSupplierRequest{
		ActorID: actor,
		Kode:    ptr("SUP-B"),
		Nama:    "Supplier B",
	})
	if err != nil {
		t.Fatalf("create second: %v", err)
	}

	_, err = a.supplier.Update(ctx(), decodeSupplierPatch(t, second.ID, actor, `{"kode":"SUP-A"}`))
	assertKind(t, err, model.KindConflict)

	// Re-sending its own code is not a conflict: exceptID skips the row itself.
	patched := patchSupplier(t, a, second.ID, actor, `{"kode":"SUP-B"}`)
	if patched.Kode == nil || *patched.Kode != "SUP-B" {
		t.Errorf("kode = %v, want SUP-B", patched.Kode)
	}
}

// created_by is nullable, and the join must still surface a row that has none —
// an INNER JOIN would hide it. isu #23 fase 2 made SupplierUseCase.Create always
// fill created_by from the caller's token, so the only way left to produce a row
// with no creator is the same way the pre-auth data in a real database got
// there: a row written directly, bypassing the usecase entirely.
func TestSupplierListKeepsRowsWithoutCreator(t *testing.T) {
	a := newApp(t)

	if _, err := testDB.ExecContext(ctx(),
		`INSERT INTO supplier (nama, is_aktif) VALUES ($1, true)`, "Tanpa Pembuat",
	); err != nil {
		t.Fatalf("seed supplier without creator: %v", err)
	}

	responses, paging, err := a.supplier.Search(ctx(), &model.ListSupplierRequest{})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if len(responses) != 1 || paging.TotalItem != 1 {
		t.Fatalf("got %d rows and total_item %d, want 1 and 1", len(responses), paging.TotalItem)
	}

	if responses[0].CreatedBy != nil {
		t.Errorf("created_by = %v, want nil", *responses[0].CreatedBy)
	}

	if responses[0].NamaPembuat != nil {
		t.Errorf("nama_pembuat = %v, want nil", *responses[0].NamaPembuat)
	}
}

func TestSupplierIsAktifFilter(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)

	active, err := a.supplier.Create(ctx(), &model.CreateSupplierRequest{ActorID: actor, Nama: "Masih Aktif"})
	if err != nil {
		t.Fatalf("create active: %v", err)
	}

	retired, err := a.supplier.Create(ctx(), &model.CreateSupplierRequest{ActorID: actor, Nama: "Sudah Pensiun"})
	if err != nil {
		t.Fatalf("create retired: %v", err)
	}

	// No DELETE endpoint exists by design; retiring is what takes its place.
	if _, err := a.supplier.Update(ctx(), decodeSupplierPatch(t, retired.ID, actor, `{"is_aktif":false}`)); err != nil {
		t.Fatalf("retire: %v", err)
	}

	responses, paging, err := a.supplier.Search(ctx(), &model.ListSupplierRequest{IsAktif: ptr(true)})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if len(responses) != 1 || responses[0].ID != active.ID {
		t.Fatalf("is_aktif=true returned %d rows, want only id %d", len(responses), active.ID)
	}

	if paging.TotalItem != 1 {
		t.Errorf("total_item = %d, want 1", paging.TotalItem)
	}
}

// decodeSupplierPatch builds the request the way the controller does: decode the
// body, then set ID and ActorID the way the controller sets them from the path
// and the session rather than the body.
func decodeSupplierPatch(t *testing.T, id, actor int64, body string) *model.UpdateSupplierRequest {
	t.Helper()

	request := new(model.UpdateSupplierRequest)
	if err := json.Unmarshal([]byte(body), request); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}

	request.ID = id
	request.ActorID = actor

	return request
}

func patchSupplier(t *testing.T, a *app, id, actor int64, body string) *model.SupplierResponse {
	t.Helper()

	response, err := a.supplier.Update(ctx(), decodeSupplierPatch(t, id, actor, body))
	if err != nil {
		t.Fatalf("patch %s: %v", body, err)
	}

	return response
}
