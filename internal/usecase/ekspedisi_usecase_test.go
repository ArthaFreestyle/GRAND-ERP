package usecase_test

import (
	"encoding/json"
	"testing"

	"Arthafreestyle/ERP/internal/model"
)

// Decision 2 of the issue: ekspedisi.nama had no UNIQUE constraint, and now it
// does — case-insensitively, like every other master code.
func TestEkspedisiDuplicateNamaIsConflict(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)

	if _, err := a.ekspedisi.Create(ctx(), &model.CreateEkspedisiRequest{
		ActorID: actor,
		Nama:    "JNE",
		Telepon: ptr("021-100"),
	}); err != nil {
		t.Fatalf("create first: %v", err)
	}

	for _, nama := range []string{"JNE", "jne"} {
		_, err := a.ekspedisi.Create(ctx(), &model.CreateEkspedisiRequest{ActorID: actor, Nama: nama})
		assertKind(t, err, model.KindConflict)
	}
}

func TestEkspedisiPatchClearsTelepon(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)

	created, err := a.ekspedisi.Create(ctx(), &model.CreateEkspedisiRequest{
		ActorID: actor,
		Nama:    "Tiki",
		Telepon: ptr("021-200"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Absent key: the number stays.
	patched := patchEkspedisi(t, a, created.ID, actor, `{"nama":"TIKI Pusat"}`)
	if patched.Telepon == nil || *patched.Telepon != "021-200" {
		t.Fatalf("telepon = %v after a body that omitted it, want 021-200", patched.Telepon)
	}

	if patched.Nama != "TIKI Pusat" {
		t.Errorf("nama = %q, want TIKI Pusat", patched.Nama)
	}

	// Explicit null: the number goes.
	patched = patchEkspedisi(t, a, created.ID, actor, `{"telepon":null}`)
	if patched.Telepon != nil {
		t.Fatalf("telepon = %q after an explicit null, want null", *patched.Telepon)
	}

	reread, err := a.ekspedisi.Get(ctx(), &model.GetEkspedisiRequest{ID: created.ID})
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if reread.Telepon != nil {
		t.Errorf("telepon = %q after re-reading, want null", *reread.Telepon)
	}
}

func TestEkspedisiPatchRejectsNullNama(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)

	created, err := a.ekspedisi.Create(ctx(), &model.CreateEkspedisiRequest{ActorID: actor, Nama: "SiCepat"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err = a.ekspedisi.Update(ctx(), decodeEkspedisiPatch(t, created.ID, actor, `{"nama":null}`))
	assertKind(t, err, model.KindInvalid)
}

// The search filter covers telepon as well as nama, and the escaping applies to
// both.
func TestEkspedisiSearchMatchesTelepon(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)

	if _, err := a.ekspedisi.Create(ctx(), &model.CreateEkspedisiRequest{
		ActorID: actor,
		Nama:    "Pos Indonesia",
		Telepon: ptr("021-300"),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := a.ekspedisi.Create(ctx(), &model.CreateEkspedisiRequest{
		ActorID: actor,
		Nama:    "Wahana",
		Telepon: ptr("021-400"),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	responses, paging, err := a.ekspedisi.Search(ctx(), &model.ListEkspedisiRequest{Search: "021-300"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if len(responses) != 1 || responses[0].Nama != "Pos Indonesia" {
		t.Fatalf("got %d rows %v, want only Pos Indonesia", len(responses), responses)
	}

	if paging.TotalItem != 1 {
		t.Errorf("total_item = %d, want 1", paging.TotalItem)
	}
}

func decodeEkspedisiPatch(t *testing.T, id, actor int64, body string) *model.UpdateEkspedisiRequest {
	t.Helper()

	request := new(model.UpdateEkspedisiRequest)
	if err := json.Unmarshal([]byte(body), request); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}

	request.ID = id
	request.ActorID = actor

	return request
}

func patchEkspedisi(t *testing.T, a *app, id, actor int64, body string) *model.EkspedisiResponse {
	t.Helper()

	response, err := a.ekspedisi.Update(ctx(), decodeEkspedisiPatch(t, id, actor, body))
	if err != nil {
		t.Fatalf("patch %s: %v", body, err)
	}

	return response
}
