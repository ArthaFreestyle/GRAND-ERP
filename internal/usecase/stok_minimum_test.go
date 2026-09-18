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

// search runs in SQL, alongside the stok_minimum comparison, so a product that falls
// on page two is still findable by name. Filtering the already-loaded page in the
// browser could only ever find one that happened to land on page one — the gap this
// parameter exists to close.
//
// The COUNT query shares the same filter constant as the row query, so total_item has
// to agree with the rows; asserting it here is what pins that.
func TestStokMinimumSearchMenyaringDiSQLBukanDiHalaman(t *testing.T) {
	testApp := newApp(t)
	f := stokMinimumFixture(t, testApp, 100, "5")

	// A second product, also below its minimum, whose name shares nothing with the
	// fixture's "Kertas A4".
	kedua, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID:       f.actor,
		KodeBarang:    "BRG-777",
		Nama:          "Tinta Printer",
		IDSatuanDasar: f.pcs,
		StokMinimum:   50,
	})
	if err != nil {
		t.Fatalf("create produk kedua: %v", err)
	}

	// Page size 1: unsearched, both are flagged but only one row fits on a page.
	semua, paging, err := testApp.product.StokMinimum(ctx(), &model.ListStokMinimumRequest{
		PageRequest:      model.PageRequest{Page: 1, Size: 1},
		AktifIDUnitKerja: &f.unitKerja,
	})
	if err != nil {
		t.Fatalf("stok minimum tanpa search: %v", err)
	}
	if paging.TotalItem != 2 {
		t.Fatalf("total_item tanpa search = %d, want 2", paging.TotalItem)
	}
	if len(semua) != 1 {
		t.Fatalf("baris tanpa search = %d, want 1 (ukuran halaman)", len(semua))
	}

	// Searched, the product that did not fit on that page is the one returned — and
	// total_item follows the filter rather than the unfiltered count.
	hasil, paging, err := testApp.product.StokMinimum(ctx(), &model.ListStokMinimumRequest{
		PageRequest:      model.PageRequest{Page: 1, Size: 1},
		Search:           "Tinta",
		AktifIDUnitKerja: &f.unitKerja,
	})
	if err != nil {
		t.Fatalf("stok minimum dengan search: %v", err)
	}
	if paging.TotalItem != 1 {
		t.Errorf("total_item dengan search = %d, want 1", paging.TotalItem)
	}
	if len(hasil) != 1 || hasil[0].IDProduct != kedua.ID {
		t.Fatalf("search seharusnya menemukan produk di luar halaman pertama, got %+v", hasil)
	}
}

// The same pair GET /product matches on: nama or kode_barang.
func TestStokMinimumSearchCocokKodeBarang(t *testing.T) {
	testApp := newApp(t)
	f := stokMinimumFixture(t, testApp, 100, "5")

	hasil, _, err := testApp.product.StokMinimum(ctx(), &model.ListStokMinimumRequest{
		PageRequest:      model.PageRequest{Page: 1, Size: 20},
		Search:           "brg-001",
		AktifIDUnitKerja: &f.unitKerja,
	})
	if err != nil {
		t.Fatalf("stok minimum: %v", err)
	}

	if len(hasil) != 1 || hasil[0].IDProduct != f.product {
		t.Fatalf("search atas kode_barang seharusnya menemukan produk fixture, got %+v", hasil)
	}
}
