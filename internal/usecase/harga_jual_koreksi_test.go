package usecase_test

// Isu #8 fase 2: correcting and deleting a price version. These run against a real
// PostgreSQL because what matters here is the actual date range left behind in
// product_harga_jual after a delete — a mock could not tell a real gap from a closed
// one.

import (
	"testing"

	"Arthafreestyle/ERP/internal/model"
)

// hargaJualFixture opens one price version and returns its id alongside the fixture
// used to create it.
func hargaJualFixture(t *testing.T, testApp *app, f fixture, harga, dari string) int64 {
	t.Helper()

	product, err := testApp.product.AddHargaJual(ctx(), &model.AddProductHargaJualRequest{
		IDProduct:   f.product,
		ActorID:     f.actor,
		IDSatuan:    f.pcs,
		Harga:       harga,
		BerlakuDari: dari,
	})
	if err != nil {
		t.Fatalf("add harga jual %s from %s: %v", harga, dari, err)
	}

	// Newest first: the version just opened is index 0.
	return product.HargaJual[0].ID
}

func TestUpdateHargaJualChangesPriceOnly(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	id := hargaJualFixture(t, testApp, f, "10000.00", "2026-01-01")

	updated, err := testApp.product.UpdateHargaJual(ctx(), &model.UpdateProductHargaJualRequest{
		ID: id, IDProduct: f.product, Harga: "12500.00",
	})
	if err != nil {
		t.Fatalf("update harga jual: %v", err)
	}

	if len(updated.HargaJual) != 1 {
		t.Fatalf("harga_jual = %+v, want exactly one version", updated.HargaJual)
	}

	got := updated.HargaJual[0]
	if got.Harga != "12500.00" {
		t.Errorf("harga = %q, want 12500.00", got.Harga)
	}
	if got.IDSatuan != f.pcs {
		t.Errorf("id_satuan = %d, want unchanged at %d", got.IDSatuan, f.pcs)
	}
	if got.BerlakuDari != "2026-01-01" {
		t.Errorf("berlaku_dari = %q, want unchanged at 2026-01-01", got.BerlakuDari)
	}
}

func TestUpdateHargaJualUnknownIsNotFound(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	_, err := testApp.product.UpdateHargaJual(ctx(), &model.UpdateProductHargaJualRequest{
		ID: 999_999, IDProduct: f.product, Harga: "1000.00",
	})

	assertKind(t, err, model.KindNotFound)
}

// Deleting the currently open-ended version has to hand berlaku_sampai back to NULL
// on whatever came before it — otherwise the product is left mid-history with an
// open version deleted and no open version left at all.
func TestDeleteHargaJualReopensPreviousToOpenEnded(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	pertama := hargaJualFixture(t, testApp, f, "10000.00", "2026-01-01")
	kedua := hargaJualFixture(t, testApp, f, "12000.00", "2026-06-01")

	updated, err := testApp.product.DeleteHargaJual(ctx(), &model.DeleteProductHargaJualRequest{
		ID: kedua, IDProduct: f.product,
	})
	if err != nil {
		t.Fatalf("delete harga jual: %v", err)
	}

	if len(updated.HargaJual) != 1 {
		t.Fatalf("harga_jual = %+v, want only the first version left", updated.HargaJual)
	}

	sisa := updated.HargaJual[0]
	if sisa.ID != pertama {
		t.Fatalf("remaining version id = %d, want %d", sisa.ID, pertama)
	}
	if sisa.BerlakuSampai != nil {
		t.Errorf("berlaku_sampai = %q, want reopened to open-ended (nil)", *sisa.BerlakuSampai)
	}

	// The resolver must agree: a date that used to belong to the deleted version now
	// resolves to the reopened one, with no gap in between.
	resolved, err := testApp.product.HargaBerlaku(ctx(), &model.ListHargaJualBerlakuRequest{
		IDProduct: f.product, Tanggal: "2026-08-01",
	})
	if err != nil {
		t.Fatalf("harga berlaku after delete: %v", err)
	}
	if len(resolved) != 1 || resolved[0].Harga != "10000.00" {
		t.Fatalf("harga berlaku 2026-08-01 = %+v, want the reopened 10000.00 version", resolved)
	}
}

// Deleting a version in the middle of the history hands its own berlaku_sampai back
// to the version before it — never NULL, or the version after the deleted one would
// end up overlapping the reopened one.
func TestDeleteHargaJualReopensPreviousToDeletedVersionsEnd(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	pertama := hargaJualFixture(t, testApp, f, "10000.00", "2026-01-01")
	kedua := hargaJualFixture(t, testApp, f, "12000.00", "2026-03-01")
	_ = hargaJualFixture(t, testApp, f, "15000.00", "2026-06-01")

	updated, err := testApp.product.DeleteHargaJual(ctx(), &model.DeleteProductHargaJualRequest{
		ID: kedua, IDProduct: f.product,
	})
	if err != nil {
		t.Fatalf("delete harga jual: %v", err)
	}

	if len(updated.HargaJual) != 2 {
		t.Fatalf("harga_jual = %+v, want two versions left", updated.HargaJual)
	}

	var sisaPertama *model.ProductHargaJualResponse
	for i := range updated.HargaJual {
		if updated.HargaJual[i].ID == pertama {
			sisaPertama = &updated.HargaJual[i]
		}
	}
	if sisaPertama == nil {
		t.Fatalf("first version disappeared: %+v", updated.HargaJual)
	}

	if sisaPertama.BerlakuSampai == nil || *sisaPertama.BerlakuSampai != "2026-06-01" {
		t.Errorf("first version berlaku_sampai = %v, want 2026-06-01 — the deleted "+
			"version's own end date, not the deleted version's start date and not NULL",
			sisaPertama.BerlakuSampai)
	}

	// The date range the deleted (middle) version used to cover must resolve to the
	// reopened first version now — no gap.
	resolved, err := testApp.product.HargaBerlaku(ctx(), &model.ListHargaJualBerlakuRequest{
		IDProduct: f.product, Tanggal: "2026-04-01",
	})
	if err != nil {
		t.Fatalf("harga berlaku after delete: %v", err)
	}
	if len(resolved) != 1 || resolved[0].Harga != "10000.00" {
		t.Fatalf("harga berlaku 2026-04-01 = %+v, want the reopened 10000.00 version", resolved)
	}
}

// The very first version a product ever had has nothing before it to reopen —
// ReopenPreviousHargaJual affecting zero rows is not an error, it is the honest
// answer for "there was never a price before this one".
func TestDeleteHargaJualEarliestVersionHasNothingToReopen(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	satu := hargaJualFixture(t, testApp, f, "10000.00", "2026-01-01")

	updated, err := testApp.product.DeleteHargaJual(ctx(), &model.DeleteProductHargaJualRequest{
		ID: satu, IDProduct: f.product,
	})
	if err != nil {
		t.Fatalf("delete the only version: %v", err)
	}

	if len(updated.HargaJual) != 0 {
		t.Fatalf("harga_jual = %+v, want none left", updated.HargaJual)
	}
}

func TestDeleteHargaJualUnknownIsNotFound(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	_, err := testApp.product.DeleteHargaJual(ctx(), &model.DeleteProductHargaJualRequest{
		ID: 999_999, IDProduct: f.product,
	})

	assertKind(t, err, model.KindNotFound)
}

// jualDenganHarga inserts one minimal penjualan + penjualan_detail row directly with
// SQL, referencing the given id_harga_jual. There is no penjualan module yet — the
// tables exist since migration 000006 — so this is the only way to prove the fase 2
// guard actually looks at penjualan_detail rather than trusting an empty table by
// accident.
func jualDenganHarga(t *testing.T, f fixture, idHargaJual int64) {
	t.Helper()

	var idPenjualan int64
	err := testDB.QueryRowContext(ctx(), `
		INSERT INTO penjualan (nomor, tanggal, id_ruang, created_by)
		VALUES ($1, now(), $2, $3)
		RETURNING id`,
		"PJ-TEST-0001", f.ruang, f.actor,
	).Scan(&idPenjualan)
	if err != nil {
		t.Fatalf("insert fixture penjualan: %v", err)
	}

	_, err = testDB.ExecContext(ctx(), `
		INSERT INTO penjualan_detail (
			id_penjualan, id_product, qty_input, id_satuan_input, faktor_konversi,
			qty_dasar, id_harga_jual, harga_satuan_input, subtotal
		) VALUES ($1, $2, 1, $3, 1, 1, $4, 10000.00, 10000.00)`,
		idPenjualan, f.product, f.pcs, idHargaJual,
	)
	if err != nil {
		t.Fatalf("insert fixture penjualan_detail: %v", err)
	}
}

func TestUpdateHargaJualUsedByDocumentIsConflict(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	id := hargaJualFixture(t, testApp, f, "10000.00", "2026-01-01")
	jualDenganHarga(t, f, id)

	_, err := testApp.product.UpdateHargaJual(ctx(), &model.UpdateProductHargaJualRequest{
		ID: id, IDProduct: f.product, Harga: "99999.00",
	})

	assertKind(t, err, model.KindConflict)

	// Untouched: the conflict must have been raised before any write, not after one
	// that then had to be rolled back and happened to leave the old value in place.
	product, err := testApp.product.Get(ctx(), &model.GetProductRequest{ID: f.product})
	if err != nil {
		t.Fatalf("get product: %v", err)
	}
	if product.HargaJual[0].Harga != "10000.00" {
		t.Errorf("harga = %q after a rejected update, want it unchanged at 10000.00",
			product.HargaJual[0].Harga)
	}
}

func TestDeleteHargaJualUsedByDocumentIsConflict(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	id := hargaJualFixture(t, testApp, f, "10000.00", "2026-01-01")
	jualDenganHarga(t, f, id)

	_, err := testApp.product.DeleteHargaJual(ctx(), &model.DeleteProductHargaJualRequest{
		ID: id, IDProduct: f.product,
	})

	assertKind(t, err, model.KindConflict)

	product, err := testApp.product.Get(ctx(), &model.GetProductRequest{ID: f.product})
	if err != nil {
		t.Fatalf("get product: %v", err)
	}
	if len(product.HargaJual) != 1 {
		t.Fatalf("harga_jual = %+v, want the referenced version still present", product.HargaJual)
	}
}
