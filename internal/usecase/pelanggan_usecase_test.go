package usecase_test

import (
	"encoding/json"
	"testing"

	"Arthafreestyle/ERP/internal/model"
)

// plafon_kredit = NULL means "no credit limit", which is not a limit of zero.
// Normalising it would turn an unlimited customer into one that may not owe a
// single rupiah, so the null has to survive a write and a re-read untouched.
func TestPelangganNullPlafonKreditStaysNull(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)

	created, err := a.pelanggan.Create(ctx(), &model.CreatePelangganRequest{ActorID: actor, Nama: "Tanpa Plafon"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if created.PlafonKredit != nil {
		t.Errorf("plafon_kredit = %q on create, want null", *created.PlafonKredit)
	}

	reread, err := a.pelanggan.Get(ctx(), &model.GetPelangganRequest{ID: created.ID})
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if reread.PlafonKredit != nil {
		t.Errorf("plafon_kredit = %q after re-reading, want null", *reread.PlafonKredit)
	}

	// The list projection goes through a different query, so check it too.
	responses, _, err := a.pelanggan.Search(ctx(), &model.ListPelangganRequest{})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if len(responses) != 1 {
		t.Fatalf("got %d rows, want 1", len(responses))
	}

	if responses[0].PlafonKredit != nil {
		t.Errorf("plafon_kredit = %q in list, want null", *responses[0].PlafonKredit)
	}
}

// The value is carried as a decimal string, so what PostgreSQL stored is what the
// client sees — no binary rounding in a money column.
func TestPelangganPlafonKreditRoundTripsExactly(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)

	created, err := a.pelanggan.Create(ctx(), &model.CreatePelangganRequest{
		ActorID:      actor,
		Nama:         "Dengan Plafon",
		PlafonKredit: ptr("15000000.55"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if created.PlafonKredit == nil || *created.PlafonKredit != "15000000.55" {
		t.Fatalf("plafon_kredit = %v, want 15000000.55", created.PlafonKredit)
	}

	reread, err := a.pelanggan.Get(ctx(), &model.GetPelangganRequest{ID: created.ID})
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if reread.PlafonKredit == nil || *reread.PlafonKredit != "15000000.55" {
		t.Errorf("plafon_kredit = %v after re-reading, want 15000000.55", reread.PlafonKredit)
	}
}

// Raising a limit, then lifting it entirely. The second step is the one a
// COALESCE-based UPDATE could not express.
func TestPelangganPatchCanSetAndClearPlafonKredit(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)

	created, err := a.pelanggan.Create(ctx(), &model.CreatePelangganRequest{
		ActorID:      actor,
		Nama:         "Naik Turun",
		PlafonKredit: ptr("1000.00"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	patched := patchPelanggan(t, a, created.ID, actor, `{"plafon_kredit":"2500.75"}`)
	if patched.PlafonKredit == nil || *patched.PlafonKredit != "2500.75" {
		t.Fatalf("plafon_kredit = %v, want 2500.75", patched.PlafonKredit)
	}

	// Absent key: the limit must stay where it was.
	patched = patchPelanggan(t, a, created.ID, actor, `{"nama":"Naik Turun Lagi"}`)
	if patched.PlafonKredit == nil || *patched.PlafonKredit != "2500.75" {
		t.Fatalf("plafon_kredit = %v after a body that omitted it, want 2500.75", patched.PlafonKredit)
	}

	// Explicit null: unlimited credit again.
	patched = patchPelanggan(t, a, created.ID, actor, `{"plafon_kredit":null}`)
	if patched.PlafonKredit != nil {
		t.Fatalf("plafon_kredit = %q after an explicit null, want null", *patched.PlafonKredit)
	}

	reread, err := a.pelanggan.Get(ctx(), &model.GetPelangganRequest{ID: created.ID})
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if reread.PlafonKredit != nil {
		t.Errorf("plafon_kredit = %q after re-reading, want null", *reread.PlafonKredit)
	}
}

// pelanggan_plafon_check would reject this at the database, which would surface as
// an opaque 500. Catching the sign early keeps it a 400.
func TestPelangganNegativePlafonKreditIsInvalid(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)

	_, err := a.pelanggan.Create(ctx(), &model.CreatePelangganRequest{
		ActorID:      actor,
		Nama:         "Plafon Minus",
		PlafonKredit: ptr("-1"),
	})
	assertKind(t, err, model.KindInvalid)

	created, err := a.pelanggan.Create(ctx(), &model.CreatePelangganRequest{ActorID: actor, Nama: "Plafon Wajar"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	request := decodePelangganPatch(t, created.ID, actor, `{"plafon_kredit":"-500.00"}`)

	_, err = a.pelanggan.Update(ctx(), request)
	assertKind(t, err, model.KindInvalid)
}

// A value that is not a number at all must be a validation failure, not a
// database error.
func TestPelangganNonNumericPlafonKreditIsInvalid(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)

	_, err := a.pelanggan.Create(ctx(), &model.CreatePelangganRequest{
		ActorID:      actor,
		Nama:         "Plafon Ngawur",
		PlafonKredit: ptr("banyak"),
	})

	if err == nil {
		t.Fatal("expected a validation error for a non-numeric plafon_kredit")
	}
}

func TestPelangganDuplicateKodeIsConflict(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)

	if _, err := a.pelanggan.Create(ctx(), &model.CreatePelangganRequest{
		ActorID: actor,
		Kode:    ptr("PLG-01"),
		Nama:    "Pelanggan Satu",
	}); err != nil {
		t.Fatalf("create first: %v", err)
	}

	_, err := a.pelanggan.Create(ctx(), &model.CreatePelangganRequest{
		ActorID: actor,
		Kode:    ptr("plg-01"),
		Nama:    "Pelanggan Dua",
	})
	assertKind(t, err, model.KindConflict)
}

func TestPelangganManyNullKodeAreAllowed(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)

	for _, nama := range []string{"Tanpa Kode A", "Tanpa Kode B"} {
		if _, err := a.pelanggan.Create(ctx(), &model.CreatePelangganRequest{ActorID: actor, Nama: nama}); err != nil {
			t.Fatalf("create %q: %v", nama, err)
		}
	}

	_, paging, err := a.pelanggan.Search(ctx(), &model.ListPelangganRequest{})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if paging.TotalItem != 2 {
		t.Errorf("total_item = %d, want 2", paging.TotalItem)
	}
}

func decodePelangganPatch(t *testing.T, id, actor int64, body string) *model.UpdatePelangganRequest {
	t.Helper()

	request := new(model.UpdatePelangganRequest)
	if err := json.Unmarshal([]byte(body), request); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}

	request.ID = id
	request.ActorID = actor

	return request
}

func patchPelanggan(t *testing.T, a *app, id, actor int64, body string) *model.PelangganResponse {
	t.Helper()

	response, err := a.pelanggan.Update(ctx(), decodePelangganPatch(t, id, actor, body))
	if err != nil {
		t.Fatalf("patch %s: %v", body, err)
	}

	return response
}
