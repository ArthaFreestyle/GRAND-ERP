package usecase_test

// PATCH /api/v1/ruang/{id} — isu #23 fase 2 and fase 3. These run against a real
// PostgreSQL because the two retirement guards read kartu_stok's own balance and
// stok_opname's own freeze, neither of which a mock could agree or disagree with
// meaningfully.

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"Arthafreestyle/ERP/internal/model"
)

func decodeRuangPatch(t *testing.T, id, actor int64, body string) *model.UpdateRuangRequest {
	t.Helper()

	request := new(model.UpdateRuangRequest)
	if err := json.Unmarshal([]byte(body), request); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}

	request.ID = id
	request.ActorID = actor

	return request
}

// ruang was the last master slice to gain created_by/updated_by (isu #23
// fase 1) — see created_by_master_test.go for the other seven.
func TestRuangCreateFillsCreatedByFromActor(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	created, err := testApp.ruang.Create(ctx(), &model.CreateRuangRequest{
		ActorID: f.actor, NamaRuang: "Gudang Baru", IDUnitKerja: f.unitKerja,
	})
	if err != nil {
		t.Fatalf("create ruang: %v", err)
	}
	if created.CreatedBy == nil || *created.CreatedBy != f.actor {
		t.Errorf("created_by = %v, want %d", created.CreatedBy, f.actor)
	}
}

// The three fields PATCH actually owns all move together.
func TestRuangPatchUpdatesKodeNamaAndIsAktif(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	created, err := testApp.ruang.Create(ctx(), &model.CreateRuangRequest{
		ActorID: f.actor, Kode: ptr("R1"), NamaRuang: "Awal", IDUnitKerja: f.unitKerja,
	})
	if err != nil {
		t.Fatalf("create ruang: %v", err)
	}

	updated, err := testApp.ruang.Update(ctx(), decodeRuangPatch(
		t, created.ID, f.actor, `{"kode":"R2","nama_ruang":"Diubah","is_aktif":false}`,
	))
	if err != nil {
		t.Fatalf("update ruang: %v", err)
	}

	if updated.Kode == nil || *updated.Kode != "R2" {
		t.Errorf("kode = %v, want R2", updated.Kode)
	}
	if updated.NamaRuang != "Diubah" {
		t.Errorf("nama_ruang = %q, want Diubah", updated.NamaRuang)
	}
	if updated.IsAktif {
		t.Error("is_aktif = true, want false")
	}
	if updated.UpdatedBy == nil || *updated.UpdatedBy != f.actor {
		t.Errorf("updated_by = %v, want %d", updated.UpdatedBy, f.actor)
	}
}

// nama_ruang is NOT NULL: changeable, never clearable.
func TestRuangPatchRejectsNullNamaRuang(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	created, err := testApp.ruang.Create(ctx(), &model.CreateRuangRequest{
		ActorID: f.actor, NamaRuang: "Awal", IDUnitKerja: f.unitKerja,
	})
	if err != nil {
		t.Fatalf("create ruang: %v", err)
	}

	_, err = testApp.ruang.Update(ctx(), decodeRuangPatch(t, created.ID, f.actor, `{"nama_ruang":null}`))
	assertKind(t, err, model.KindInvalid)
}

// kode is nullable in the schema, so PATCH must be able to clear it — unlike
// nama_ruang and is_aktif above.
func TestRuangPatchCanClearKode(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	created, err := testApp.ruang.Create(ctx(), &model.CreateRuangRequest{
		ActorID: f.actor, Kode: ptr("R1"), NamaRuang: "Awal", IDUnitKerja: f.unitKerja,
	})
	if err != nil {
		t.Fatalf("create ruang: %v", err)
	}

	updated, err := testApp.ruang.Update(ctx(), decodeRuangPatch(t, created.ID, f.actor, `{"kode":null}`))
	if err != nil {
		t.Fatalf("clear kode: %v", err)
	}
	if updated.Kode != nil {
		t.Errorf("kode = %v, want cleared", *updated.Kode)
	}
}

// UpdateRuangRequest carries no id_unit_kerja field at all, so a client sending
// one in the body has no way to reach it — the unit a room belongs to cannot be
// changed through this endpoint. See UpdateRuangRequest for why: moving a ruang
// would carry its whole kartu_stok history and balance across without a single
// kartu_stok row changing.
func TestRuangPatchIgnoresIDUnitKerjaInBody(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	unitLain, err := testApp.unitKerja.Create(ctx(), &model.CreateUnitKerjaRequest{
		ActorID: f.actor, Nama: "Unit Lain",
	})
	if err != nil {
		t.Fatalf("create unit lain: %v", err)
	}

	created, err := testApp.ruang.Create(ctx(), &model.CreateRuangRequest{
		ActorID: f.actor, NamaRuang: "Awal", IDUnitKerja: f.unitKerja,
	})
	if err != nil {
		t.Fatalf("create ruang: %v", err)
	}

	// id_unit_kerja rides along in the JSON body a real client might send; the DTO
	// simply has no field to catch it in, the same way json.Unmarshal silently
	// drops any other unknown key.
	patched, err := testApp.ruang.Update(ctx(), decodeRuangPatch(
		t, created.ID, f.actor, `{"nama_ruang":"Baru","id_unit_kerja":`+strconv.FormatInt(unitLain.ID, 10)+`}`,
	))
	if err != nil {
		t.Fatalf("update ruang: %v", err)
	}

	if patched.NamaRuang != "Baru" {
		t.Errorf("nama_ruang = %q, want Baru", patched.NamaRuang)
	}
	if patched.IDUnitKerja != f.unitKerja {
		t.Errorf("id_unit_kerja = %d, moved despite not being in the DTO; want unchanged %d", patched.IDUnitKerja, f.unitKerja)
	}
}

// Fase 3: retiring a room still holding stock is refused 409 — the inventory
// value report never filters is_aktif, so a retirement over live stock would
// leave goods on no room list yet still on the balance sheet.
func TestRuangPatchDeactivateRejectedWhenHoldingStock(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	draft := draftSederhana(t, testApp, f, "10", nil, nil)
	ajukanDanPosting(t, testApp, f, draft.ID)

	_, err := testApp.ruang.Update(ctx(), decodeRuangPatch(t, f.ruang, f.actor, `{"is_aktif":false}`))
	assertKind(t, err, model.KindConflict)
}

// A room that never held stock, or has since been emptied, may be retired freely.
func TestRuangPatchDeactivateAllowedWhenEmpty(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	kosong, err := testApp.ruang.Create(ctx(), &model.CreateRuangRequest{
		ActorID: f.actor, NamaRuang: "Ruang Kosong", IDUnitKerja: f.unitKerja,
	})
	if err != nil {
		t.Fatalf("create ruang kosong: %v", err)
	}

	updated, err := testApp.ruang.Update(ctx(), decodeRuangPatch(t, kosong.ID, f.actor, `{"is_aktif":false}`))
	if err != nil {
		t.Fatalf("retire empty ruang: %v", err)
	}
	if updated.IsAktif {
		t.Error("is_aktif = true, want false after retiring an empty ruang")
	}
}

// Fase 3's second guard: a room frozen by an open stok_opname cannot be retired
// either, and the message names the freezing opname's own nomor — the same
// figure RuangResponse.NomorOpnameBeku already carries on every read.
func TestRuangPatchDeactivateRejectedWhenFrozenByOpname(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	opname := buatOpname(t, testApp, f)

	_, err := testApp.ruang.Update(ctx(), decodeRuangPatch(t, f.ruang, f.actor, `{"is_aktif":false}`))
	assertKind(t, err, model.KindConflict)

	var domainErr *model.Error
	if !errors.As(err, &domainErr) {
		t.Fatalf("expected *model.Error, got %T: %v", err, err)
	}
	if !strings.Contains(domainErr.Message, opname.Nomor) {
		t.Errorf("message %q does not name the freezing opname %q", domainErr.Message, opname.Nomor)
	}
}

// Re-affirming is_aktif=false on a room already inactive is a no-op the guards
// still have to tolerate — it must not be treated as a fresh retirement request
// against a room with residual stock left behind by an earlier, legitimate
// deactivation.
func TestRuangPatchReaffirmingInactiveIsIdempotent(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	kosong, err := testApp.ruang.Create(ctx(), &model.CreateRuangRequest{
		ActorID: f.actor, NamaRuang: "Ruang Kosong", IDUnitKerja: f.unitKerja,
	})
	if err != nil {
		t.Fatalf("create ruang kosong: %v", err)
	}

	if _, err := testApp.ruang.Update(ctx(), decodeRuangPatch(t, kosong.ID, f.actor, `{"is_aktif":false}`)); err != nil {
		t.Fatalf("first retire: %v", err)
	}

	updated, err := testApp.ruang.Update(ctx(), decodeRuangPatch(t, kosong.ID, f.actor, `{"is_aktif":false}`))
	if err != nil {
		t.Fatalf("re-affirming an already-inactive ruang: %v", err)
	}
	if updated.IsAktif {
		t.Error("is_aktif = true after re-affirming false")
	}
}
