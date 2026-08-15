package usecase_test

// Isu #8 fase 3: the cross-product price list. These run against a real PostgreSQL
// because the point of the query is the LEFT JOIN — a product with no price in force
// still has to produce a row, and only the database can prove that actually happens
// rather than silently degrading to an inner join.

import (
	"testing"

	"Arthafreestyle/ERP/internal/model"
)

// A product that has never had a price still has to appear — this is the question
// fase 3 exists to answer ("which products have no price at all"), so it cannot be
// the row that the LEFT JOIN quietly drops.
func TestDaftarHargaJualIncludesProductWithNoPrice(t *testing.T) {
	testApp := newApp(t)
	actor, pcs, _ := productFixture(t, testApp)

	priced, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID: actor, KodeBarang: "BRG-600", Nama: "Lem", IDSatuanDasar: pcs,
	})
	if err != nil {
		t.Fatalf("create priced product: %v", err)
	}
	if _, err := testApp.product.AddHargaJual(ctx(), &model.AddProductHargaJualRequest{
		IDProduct: priced.ID, ActorID: actor, IDSatuan: pcs,
		Harga: "3000.00", BerlakuDari: "2026-01-01",
	}); err != nil {
		t.Fatalf("price it: %v", err)
	}

	unpriced, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID: actor, KodeBarang: "BRG-601", Nama: "Penggaris", IDSatuanDasar: pcs,
	})
	if err != nil {
		t.Fatalf("create unpriced product: %v", err)
	}

	list, paging, err := testApp.product.DaftarHargaJual(ctx(), &model.ListDaftarHargaJualRequest{})
	if err != nil {
		t.Fatalf("daftar harga jual: %v", err)
	}

	if paging.TotalItem != 2 || len(list) != 2 {
		t.Fatalf("total_item = %d, len = %d, want 2 and 2", paging.TotalItem, len(list))
	}

	var found bool
	for _, row := range list {
		if row.IDProduct == unpriced.ID {
			found = true
			if row.IDSatuan != nil || row.IDHargaJual != nil || row.Harga != nil {
				t.Errorf("unpriced product row = %+v, want every harga field nil", row)
			}
		}
		if row.IDProduct == priced.ID {
			if row.Harga == nil || *row.Harga != "3000.00" {
				t.Errorf("priced product row harga = %v, want 3000.00", row.Harga)
			}
		}
	}
	if !found {
		t.Fatal("the product with no price at all is missing from the list — LEFT JOIN " +
			"must have silently become an inner join")
	}
}

// A product priced in two satuan produces two rows, one per satuan — "satu baris per
// produk + satuan" per the issue, not one row per product.
func TestDaftarHargaJualOneRowPerProductAndSatuan(t *testing.T) {
	testApp := newApp(t)
	actor, pcs, box := productFixture(t, testApp)

	product, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID: actor, KodeBarang: "BRG-602", Nama: "Sabun", IDSatuanDasar: pcs,
		Satuan: []model.CreateProductSatuanRequest{{IDSatuan: box, Faktor: 6}},
	})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}

	if _, err := testApp.product.AddHargaJual(ctx(), &model.AddProductHargaJualRequest{
		IDProduct: product.ID, ActorID: actor, IDSatuan: pcs,
		Harga: "2000.00", BerlakuDari: "2026-01-01",
	}); err != nil {
		t.Fatalf("price pcs: %v", err)
	}
	if _, err := testApp.product.AddHargaJual(ctx(), &model.AddProductHargaJualRequest{
		IDProduct: product.ID, ActorID: actor, IDSatuan: box,
		Harga: "11000.00", BerlakuDari: "2026-01-01",
	}); err != nil {
		t.Fatalf("price box: %v", err)
	}

	list, paging, err := testApp.product.DaftarHargaJual(ctx(), &model.ListDaftarHargaJualRequest{})
	if err != nil {
		t.Fatalf("daftar harga jual: %v", err)
	}

	if paging.TotalItem != 2 || len(list) != 2 {
		t.Fatalf("total_item = %d, len = %d, want 2 rows for one product in two satuan",
			paging.TotalItem, len(list))
	}

	for _, row := range list {
		if row.IDProduct != product.ID {
			t.Errorf("unexpected product id %d in result", row.IDProduct)
		}
	}
}

// A date before any version's berlaku_dari resolves to no price in force, same as a
// product that was never priced — the row still has to appear with nulls, not vanish.
func TestDaftarHargaJualRespectsTanggal(t *testing.T) {
	testApp := newApp(t)
	actor, pcs, _ := productFixture(t, testApp)

	product, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID: actor, KodeBarang: "BRG-603", Nama: "Payung", IDSatuanDasar: pcs,
	})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}
	if _, err := testApp.product.AddHargaJual(ctx(), &model.AddProductHargaJualRequest{
		IDProduct: product.ID, ActorID: actor, IDSatuan: pcs,
		Harga: "25000.00", BerlakuDari: "2026-06-01",
	}); err != nil {
		t.Fatalf("price it: %v", err)
	}

	before, _, err := testApp.product.DaftarHargaJual(ctx(), &model.ListDaftarHargaJualRequest{
		Tanggal: "2026-01-01",
	})
	if err != nil {
		t.Fatalf("daftar harga jual before berlaku_dari: %v", err)
	}
	if len(before) != 1 || before[0].Harga != nil {
		t.Fatalf("row before berlaku_dari = %+v, want present with a nil harga", before)
	}

	after, _, err := testApp.product.DaftarHargaJual(ctx(), &model.ListDaftarHargaJualRequest{
		Tanggal: "2026-07-01",
	})
	if err != nil {
		t.Fatalf("daftar harga jual after berlaku_dari: %v", err)
	}
	if len(after) != 1 || after[0].Harga == nil || *after[0].Harga != "25000.00" {
		t.Fatalf("row after berlaku_dari = %+v, want harga 25000.00", after)
	}
}

func TestDaftarHargaJualIsAktifFilter(t *testing.T) {
	testApp := newApp(t)
	actor, pcs, _ := productFixture(t, testApp)

	aktif, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID: actor, KodeBarang: "BRG-604", Nama: "Aktif", IDSatuanDasar: pcs,
	})
	if err != nil {
		t.Fatalf("create active product: %v", err)
	}

	nonaktif, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID: actor, KodeBarang: "BRG-605", Nama: "Nonaktif", IDSatuanDasar: pcs,
	})
	if err != nil {
		t.Fatalf("create product to retire: %v", err)
	}
	falseVal := false
	if _, err := testApp.product.Update(ctx(), &model.UpdateProductRequest{
		ID: nonaktif.ID, ActorID: actor, IsAktif: model.Optional[bool]{Present: true, Value: &falseVal},
	}); err != nil {
		t.Fatalf("retire product: %v", err)
	}

	trueVal := true
	list, paging, err := testApp.product.DaftarHargaJual(ctx(), &model.ListDaftarHargaJualRequest{
		IsAktif: &trueVal,
	})
	if err != nil {
		t.Fatalf("daftar harga jual is_aktif=true: %v", err)
	}
	if paging.TotalItem != 1 || len(list) != 1 || list[0].IDProduct != aktif.ID {
		t.Fatalf("filtered list = %+v (total %d), want only the active product", list, paging.TotalItem)
	}
}

// A product literally named with a percent sign must not act as an ILIKE wildcard —
// repository.EscapeLike is what search goes through, same as every other list.
func TestDaftarHargaJualSearchEscapesPercent(t *testing.T) {
	testApp := newApp(t)
	actor, pcs, _ := productFixture(t, testApp)

	if _, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID: actor, KodeBarang: "BRG-606", Nama: "Diskon 100%", IDSatuanDasar: pcs,
	}); err != nil {
		t.Fatalf("create product named with a percent sign: %v", err)
	}
	if _, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID: actor, KodeBarang: "BRG-607", Nama: "Barang Biasa", IDSatuanDasar: pcs,
	}); err != nil {
		t.Fatalf("create unrelated product: %v", err)
	}

	list, paging, err := testApp.product.DaftarHargaJual(ctx(), &model.ListDaftarHargaJualRequest{
		Search: "100%",
	})
	if err != nil {
		t.Fatalf("daftar harga jual search: %v", err)
	}

	if paging.TotalItem != 1 || len(list) != 1 {
		t.Fatalf("search \"100%%\" matched %d rows, want exactly the one product literally "+
			"named with a percent sign — an unescaped %% would match everything", paging.TotalItem)
	}
}
