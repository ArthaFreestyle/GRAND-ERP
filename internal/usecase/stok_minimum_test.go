package usecase_test

// isu #22 fase 2: GET /product/stok-minimum. The three decisions the issue calls out
// by name — stok_minimum = 0 never appears, the threshold is <=, and scoping by the
// caller's active unit_kerja — each get their own test.

import (
	"testing"

	"Arthafreestyle/ERP/internal/model"
)

// stokMinimumFixture creates a product with stok_minimum set and posts qtyMasuk pcs
// of it into f.ruang, so its current stock is known and controllable.
func stokMinimumFixture(t *testing.T, testApp *app, stokMinimum int64, qtyMasuk string) fixture {
	t.Helper()

	f := pembelianFixture(t, testApp)

	if _, err := testApp.product.Update(ctx(), &model.UpdateProductRequest{
		ID: f.product, ActorID: f.actor,
		StokMinimum: model.Optional[int64]{Present: true, Value: ptr(stokMinimum)},
	}); err != nil {
		t.Fatalf("set stok_minimum: %v", err)
	}

	if qtyMasuk != "" {
		draft := draftSederhana(t, testApp, f, qtyMasuk, nil, nil)
		ajukanDanPosting(t, testApp, f, draft.ID)
	}

	return f
}

// A product whose stok_minimum is still the column's default (0) never appears, no
// matter how low its stock is — 0 means "never configured", not "may run out".
func TestStokMinimumNolTidakPernahMuncul(t *testing.T) {
	testApp := newApp(t)
	f := stokMinimumFixture(t, testApp, 0, "")

	list, _, err := testApp.product.StokMinimum(ctx(), &model.ListStokMinimumRequest{
		PageRequest:      model.PageRequest{Page: 1, Size: 20},
		AktifIDUnitKerja: &f.unitKerja,
	})
	if err != nil {
		t.Fatalf("stok minimum: %v", err)
	}

	for _, baris := range list {
		if baris.IDProduct == f.product {
			t.Fatalf("produk dengan stok_minimum = 0 seharusnya tidak pernah muncul")
		}
	}
}

// The threshold is total <= stok_minimum, not <: a product sitting exactly at its
// reorder point is exactly when reordering should happen.
func TestStokMinimumTepatSamaMuncul(t *testing.T) {
	testApp := newApp(t)
	// stok_minimum 50, 50 pcs on the shelf: exactly at the line.
	f := stokMinimumFixture(t, testApp, 50, "50")

	list, _, err := testApp.product.StokMinimum(ctx(), &model.ListStokMinimumRequest{
		PageRequest:      model.PageRequest{Page: 1, Size: 20},
		AktifIDUnitKerja: &f.unitKerja,
	})
	if err != nil {
		t.Fatalf("stok minimum: %v", err)
	}

	found := false
	for _, baris := range list {
		if baris.IDProduct == f.product {
			found = true
			if baris.TotalStok != 50 {
				t.Errorf("total_stok = %d, want 50", baris.TotalStok)
			}
			if baris.Selisih != 0 {
				t.Errorf("selisih = %d, want 0", baris.Selisih)
			}
		}
	}
	if !found {
		t.Fatal("produk tepat di titik minimum seharusnya muncul")
	}
}

// A product sitting comfortably above its minimum never appears.
func TestStokMinimumDiAtasTidakMuncul(t *testing.T) {
	testApp := newApp(t)
	f := stokMinimumFixture(t, testApp, 10, "100")

	list, _, err := testApp.product.StokMinimum(ctx(), &model.ListStokMinimumRequest{
		PageRequest:      model.PageRequest{Page: 1, Size: 20},
		AktifIDUnitKerja: &f.unitKerja,
	})
	if err != nil {
		t.Fatalf("stok minimum: %v", err)
	}

	for _, baris := range list {
		if baris.IDProduct == f.product {
			t.Fatalf("produk yang stoknya jauh di atas minimum seharusnya tidak muncul")
		}
	}
}

// A room outside the caller's active unit_kerja does not count toward the total —
// isu #12 fase 6 applied to this new read. A product whose only stock sits in a room
// outside the active unit reads as a total of 0, and a stok_minimum > 0 then flags it
// as if it were completely empty within that unit.
func TestStokMinimumRuangDiLuarUnitAktifTidakDihitung(t *testing.T) {
	testApp := newApp(t)
	f := stokMinimumFixture(t, testApp, 5, "100")

	unitLain := createUnit(t, testApp, "Unit Lain Stok Minimum")

	list, _, err := testApp.product.StokMinimum(ctx(), &model.ListStokMinimumRequest{
		PageRequest:      model.PageRequest{Page: 1, Size: 20},
		AktifIDUnitKerja: &unitLain,
	})
	if err != nil {
		t.Fatalf("stok minimum dari unit lain: %v", err)
	}

	for _, baris := range list {
		if baris.IDProduct == f.product && baris.TotalStok != 0 {
			t.Fatalf("stok di ruang luar unit aktif tidak seharusnya ikut terhitung, got total_stok=%d", baris.TotalStok)
		}
	}

	// And with the fixture's own unit active, the 100 pcs is visible and comfortably
	// above the minimum of 5 — proof this is a scoping effect, not a fixture bug.
	insideList, _, err := testApp.product.StokMinimum(ctx(), &model.ListStokMinimumRequest{
		PageRequest:      model.PageRequest{Page: 1, Size: 20},
		AktifIDUnitKerja: &f.unitKerja,
	})
	if err != nil {
		t.Fatalf("stok minimum dari unit sendiri: %v", err)
	}
	for _, baris := range insideList {
		if baris.IDProduct == f.product {
			t.Fatalf("dengan unit aktif yang benar, produk ini tidak seharusnya di bawah minimum")
		}
	}
}

// Only is_aktif products are considered — a retired product needs no reordering.
func TestStokMinimumProdukTidakAktifTidakMuncul(t *testing.T) {
	testApp := newApp(t)
	f := stokMinimumFixture(t, testApp, 100, "5")

	if _, err := testApp.product.Update(ctx(), &model.UpdateProductRequest{
		ID: f.product, ActorID: f.actor, IsAktif: model.Optional[bool]{Present: true, Value: ptr(false)},
	}); err != nil {
		t.Fatalf("retire product: %v", err)
	}

	list, _, err := testApp.product.StokMinimum(ctx(), &model.ListStokMinimumRequest{
		PageRequest:      model.PageRequest{Page: 1, Size: 20},
		AktifIDUnitKerja: &f.unitKerja,
	})
	if err != nil {
		t.Fatalf("stok minimum: %v", err)
	}

	for _, baris := range list {
		if baris.IDProduct == f.product {
			t.Fatalf("produk yang sudah dipensiunkan tidak seharusnya muncul")
		}
	}
}
