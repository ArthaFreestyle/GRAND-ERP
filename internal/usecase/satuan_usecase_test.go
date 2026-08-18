package usecase_test

import (
	"encoding/json"
	"testing"

	"Arthafreestyle/ERP/internal/model"
)

// satuan is unique on nama, and decision 1 makes that case-insensitive: PCS and
// Pcs are the same unit, not two.
func TestSatuanDuplicateNamaIsConflict(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)

	if _, err := a.satuan.Create(ctx(), &model.CreateSatuanRequest{ActorID: actor, Nama: "PCS"}); err != nil {
		t.Fatalf("create first: %v", err)
	}

	for _, nama := range []string{"PCS", "Pcs", "pcs"} {
		_, err := a.satuan.Create(ctx(), &model.CreateSatuanRequest{ActorID: actor, Nama: nama})
		assertKind(t, err, model.KindConflict)
	}
}

func TestSatuanRenameToExistingNamaIsConflict(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)

	if _, err := a.satuan.Create(ctx(), &model.CreateSatuanRequest{ActorID: actor, Nama: "BOX"}); err != nil {
		t.Fatalf("create first: %v", err)
	}

	second, err := a.satuan.Create(ctx(), &model.CreateSatuanRequest{ActorID: actor, Nama: "KARTON"})
	if err != nil {
		t.Fatalf("create second: %v", err)
	}

	_, err = a.satuan.Update(ctx(), decodeSatuanPatch(t, second.ID, actor, `{"nama":"box"}`))
	assertKind(t, err, model.KindConflict)

	// Renaming to its own name must be accepted — exceptID skips the row itself.
	renamed, err := a.satuan.Update(ctx(), decodeSatuanPatch(t, second.ID, actor, `{"nama":"KARTON"}`))
	if err != nil {
		t.Fatalf("rename to itself: %v", err)
	}

	if renamed.Nama != "KARTON" {
		t.Errorf("nama = %q, want KARTON", renamed.Nama)
	}
}

// Decision 3 added is_aktif so a mistyped unit can be retired: it cannot be
// deleted, because product_satuan and every *_detail reference it.
func TestSatuanCanBeRetiredAndFiltered(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)

	kept, err := a.satuan.Create(ctx(), &model.CreateSatuanRequest{ActorID: actor, Nama: "LUSIN"})
	if err != nil {
		t.Fatalf("create kept: %v", err)
	}

	typo, err := a.satuan.Create(ctx(), &model.CreateSatuanRequest{ActorID: actor, Nama: "LUSNI"})
	if err != nil {
		t.Fatalf("create typo: %v", err)
	}

	if !typo.IsAktif {
		t.Error("is_aktif = false on create, want true")
	}

	retired, err := a.satuan.Update(ctx(), decodeSatuanPatch(t, typo.ID, actor, `{"is_aktif":false}`))
	if err != nil {
		t.Fatalf("retire: %v", err)
	}

	if retired.IsAktif {
		t.Error("is_aktif = true after retiring, want false")
	}

	responses, paging, err := a.satuan.Search(ctx(), &model.ListSatuanRequest{IsAktif: ptr(true)})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if len(responses) != 1 || responses[0].ID != kept.ID {
		t.Fatalf("is_aktif=true returned %d rows, want only id %d", len(responses), kept.ID)
	}

	if paging.TotalItem != 1 {
		t.Errorf("total_item = %d, want 1", paging.TotalItem)
	}
}

// Decision 4 added updated_at, maintained by the trigger rather than by Go.
func TestSatuanUpdateBumpsUpdatedAt(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)

	created, err := a.satuan.Create(ctx(), &model.CreateSatuanRequest{ActorID: actor, Nama: "RIM"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	updated, err := a.satuan.Update(ctx(), decodeSatuanPatch(t, created.ID, actor, `{"nama":"REAM"}`))
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	if !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Errorf("updated_at = %v, want later than %v", updated.UpdatedAt, created.UpdatedAt)
	}
}

// An empty body would otherwise fire the updated_at trigger and record a change
// that never happened.
func TestSatuanPatchWithNoFieldsIsInvalid(t *testing.T) {
	a := newApp(t)
	actor := testActor(t)

	created, err := a.satuan.Create(ctx(), &model.CreateSatuanRequest{ActorID: actor, Nama: "SAK"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	_, err = a.satuan.Update(ctx(), decodeSatuanPatch(t, created.ID, actor, `{}`))
	assertKind(t, err, model.KindInvalid)
}

func decodeSatuanPatch(t *testing.T, id, actor int64, body string) *model.UpdateSatuanRequest {
	t.Helper()

	request := new(model.UpdateSatuanRequest)
	if err := json.Unmarshal([]byte(body), request); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}

	request.ID = id
	request.ActorID = actor

	return request
}
