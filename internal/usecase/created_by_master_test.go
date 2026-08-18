package usecase_test

// isu #23 fase 2 (the "Masalah 2" half of the issue): every master slice now
// fills created_by from the token at Create and updated_by at Update, the
// pattern product_controller.go established and every other slice had left
// unused. One file spanning every slice, the same reasoning
// fase6_read_scope_test.go and ruang_unit_scope_test.go already give for a
// behaviour repeated identically across several modules: reading the
// repetition together is what makes it legible as a decision, not an
// omission.

import (
	"testing"

	"Arthafreestyle/ERP/internal/model"
)

func TestSatuanCreateAndPatchFillActorColumns(t *testing.T) {
	testApp := newApp(t)
	creator := testActor(t)
	updater := testActor(t)

	created, err := testApp.satuan.Create(ctx(), &model.CreateSatuanRequest{ActorID: creator, Nama: "PCS"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.CreatedBy == nil || *created.CreatedBy != creator {
		t.Errorf("created_by = %v, want %d", created.CreatedBy, creator)
	}

	updated, err := testApp.satuan.Update(ctx(), decodeSatuanPatch(t, created.ID, updater, `{"nama":"BOX"}`))
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.UpdatedBy == nil || *updated.UpdatedBy != updater {
		t.Errorf("updated_by = %v, want %d", updated.UpdatedBy, updater)
	}
}

func TestEkspedisiCreateAndPatchFillActorColumns(t *testing.T) {
	testApp := newApp(t)
	creator := testActor(t)
	updater := testActor(t)

	created, err := testApp.ekspedisi.Create(ctx(), &model.CreateEkspedisiRequest{ActorID: creator, Nama: "JNE"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.CreatedBy == nil || *created.CreatedBy != creator {
		t.Errorf("created_by = %v, want %d", created.CreatedBy, creator)
	}

	updated := patchEkspedisi(t, testApp, created.ID, updater, `{"telepon":"021-999"}`)
	if updated.UpdatedBy == nil || *updated.UpdatedBy != updater {
		t.Errorf("updated_by = %v, want %d", updated.UpdatedBy, updater)
	}
}

func TestSupplierCreateAndPatchFillActorColumns(t *testing.T) {
	testApp := newApp(t)
	creator := testActor(t)
	updater := testActor(t)

	created, err := testApp.supplier.Create(ctx(), &model.CreateSupplierRequest{ActorID: creator, Nama: "PT Sumber"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.CreatedBy == nil || *created.CreatedBy != creator {
		t.Errorf("created_by = %v, want %d", created.CreatedBy, creator)
	}

	updated := patchSupplier(t, testApp, created.ID, updater, `{"telepon":"021-888"}`)
	if updated.UpdatedBy == nil || *updated.UpdatedBy != updater {
		t.Errorf("updated_by = %v, want %d", updated.UpdatedBy, updater)
	}
}

func TestPelangganCreateAndPatchFillActorColumns(t *testing.T) {
	testApp := newApp(t)
	creator := testActor(t)
	updater := testActor(t)

	created, err := testApp.pelanggan.Create(ctx(), &model.CreatePelangganRequest{ActorID: creator, Nama: "Budi"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.CreatedBy == nil || *created.CreatedBy != creator {
		t.Errorf("created_by = %v, want %d", created.CreatedBy, creator)
	}

	updated := patchPelanggan(t, testApp, created.ID, updater, `{"telepon":"0812-000"}`)
	if updated.UpdatedBy == nil || *updated.UpdatedBy != updater {
		t.Errorf("updated_by = %v, want %d", updated.UpdatedBy, updater)
	}
}

func TestUnitKerjaCreateAndPatchFillActorColumns(t *testing.T) {
	testApp := newApp(t)
	creator := testActor(t)
	updater := testActor(t)

	created, err := testApp.unitKerja.Create(ctx(), &model.CreateUnitKerjaRequest{ActorID: creator, Nama: "Unit Pusat"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.CreatedBy == nil || *created.CreatedBy != creator {
		t.Errorf("created_by = %v, want %d", created.CreatedBy, creator)
	}

	updated := patchUnitKerja(t, testApp, created.ID, updater, `{"nama":"Unit Pusat Baru"}`)
	if updated.UpdatedBy == nil || *updated.UpdatedBy != updater {
		t.Errorf("updated_by = %v, want %d", updated.UpdatedBy, updater)
	}
}

func TestRoleCreateAndPatchFillActorColumns(t *testing.T) {
	testApp := newApp(t)
	creator := testActor(t)
	updater := testActor(t)

	created, err := testApp.role.Create(ctx(), &model.CreateRoleRequest{ActorID: creator, Nama: "GUDANG"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.CreatedBy == nil || *created.CreatedBy != creator {
		t.Errorf("created_by = %v, want %d", created.CreatedBy, creator)
	}

	updated, err := testApp.role.Update(ctx(), &model.UpdateRoleRequest{
		ID: created.ID, ActorID: updater,
		Nama: model.Optional[string]{Present: true, Value: ptr("GUDANG UTAMA")},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.UpdatedBy == nil || *updated.UpdatedBy != updater {
		t.Errorf("updated_by = %v, want %d", updated.UpdatedBy, updater)
	}
}

func TestUserCreateFillsCreatedByFromActor(t *testing.T) {
	testApp := newApp(t)
	creator := testActor(t)

	created, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		ActorID: creator, Username: "kasir_baru", Password: "rahasia123",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.CreatedBy == nil || *created.CreatedBy != creator {
		t.Errorf("created_by = %v, want %d", created.CreatedBy, creator)
	}
}

// A patch that touches an ordinary column, not just grants, is the easy case —
// it goes through UserRepository.Update, whose patch already carried
// SetUpdatedBy/UpdatedBy once wired.
func TestUserOrdinaryPatchFillsUpdatedBy(t *testing.T) {
	testApp := newApp(t)
	creator := testActor(t)
	updater := testActor(t)

	created, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		ActorID: creator, Username: "kasir_biasa", Password: "rahasia123",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	updated, err := testApp.user.Update(ctx(), &model.UpdateUserRequest{
		ID: created.ID, ActorID: updater,
		NamaLengkap: model.Optional[string]{Present: true, Value: ptr("Kasir Biasa")},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.UpdatedBy == nil || *updated.UpdatedBy != updater {
		t.Errorf("updated_by = %v, want %d", updated.UpdatedBy, updater)
	}
}

// The case the issue calls out by name: a patch that ONLY replaces grants
// touches no ordinary column of users, so it runs through UserRepository.Touch
// instead of Update — and without isu #23's fix, Touch wrote no updated_by at
// all, making a role revocation the one change to a user that left no trace of
// who did it.
func TestUserGrantsOnlyPatchFillsUpdatedBy(t *testing.T) {
	testApp := newApp(t)
	creator := testActor(t)
	updater := testActor(t)

	role, err := testApp.role.Create(ctx(), &model.CreateRoleRequest{ActorID: creator, Nama: "KASIR_GRANT_TEST"})
	if err != nil {
		t.Fatalf("create role: %v", err)
	}

	created, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		ActorID: creator, Username: "kasir_grant_only", Password: "rahasia123",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	updated, err := testApp.user.Update(ctx(), &model.UpdateUserRequest{
		ID: created.ID, ActorID: updater,
		Grants: model.Optional[[]model.GrantRequest]{
			Present: true,
			Value:   &[]model.GrantRequest{{IDRole: role.ID}},
		},
	})
	if err != nil {
		t.Fatalf("grants-only update: %v", err)
	}
	if updated.UpdatedBy == nil || *updated.UpdatedBy != updater {
		t.Errorf("updated_by = %v, want %d after a grants-only patch", updated.UpdatedBy, updater)
	}
}
