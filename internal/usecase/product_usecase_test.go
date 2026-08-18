package usecase_test

import (
	"testing"

	"Arthafreestyle/ERP/internal/model"
)

// productFixture creates the actor and units a product needs. product.created_by is
// NOT NULL with a foreign key to users, so a real user has to exist first — which is
// exactly why this module could not ship before authentication.
func productFixture(t *testing.T, testApp *app) (actorID, pcs, box int64) {
	t.Helper()

	bootstrap := testActor(t)

	user, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		ActorID:  bootstrap,
		Username: "petugas_gudang",
		Password: "rahasia123",
	})
	if err != nil {
		t.Fatalf("create actor: %v", err)
	}

	satuanPCS, err := testApp.satuan.Create(ctx(), &model.CreateSatuanRequest{ActorID: user.ID, Nama: "PCS"})
	if err != nil {
		t.Fatalf("create satuan PCS: %v", err)
	}

	satuanBOX, err := testApp.satuan.Create(ctx(), &model.CreateSatuanRequest{ActorID: user.ID, Nama: "BOX"})
	if err != nil {
		t.Fatalf("create satuan BOX: %v", err)
	}

	return user.ID, satuanPCS.ID, satuanBOX.ID
}

// Issue #1 DoD: the base unit is registered automatically with faktor = 1.
//
// Nothing in the schema enforces it, so a product created without it would look fine
// and then break every conversion built on top of it.
func TestProductRegistersBaseUnitWithFaktorOne(t *testing.T) {
	testApp := newApp(t)
	actor, pcs, box := productFixture(t, testApp)

	product, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID:       actor,
		KodeBarang:    "BRG-001",
		Nama:          "Kertas A4",
		IDSatuanDasar: pcs,
		Satuan: []model.CreateProductSatuanRequest{
			{IDSatuan: box, Faktor: 20, IsDefaultInput: true},
		},
	})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}

	// Ordered by faktor, so the base unit comes first.
	if len(product.Satuan) != 2 {
		t.Fatalf("satuan = %+v, want the base unit plus BOX", product.Satuan)
	}

	base := product.Satuan[0]
	if base.IDSatuan != pcs || base.Faktor != 1 {
		t.Errorf("base unit = {id_satuan %d, faktor %d}, want {%d, 1}", base.IDSatuan, base.Faktor, pcs)
	}

	if product.NamaSatuanDasar != "PCS" {
		t.Errorf("nama_satuan_dasar = %q, want PCS", product.NamaSatuanDasar)
	}

	// A product created without any satuan list still gets its base unit.
	bare, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID:       actor,
		KodeBarang:    "BRG-002",
		Nama:          "Pulpen",
		IDSatuanDasar: pcs,
	})
	if err != nil {
		t.Fatalf("create product without satuan: %v", err)
	}

	if len(bare.Satuan) != 1 || bare.Satuan[0].Faktor != 1 {
		t.Errorf("satuan = %+v, want exactly the base unit at faktor 1", bare.Satuan)
	}
}

// Listing the base unit again is the obvious thing for a caller to do, so it collapses
// into the automatic row instead of colliding on the unique index.
func TestProductBaseUnitSentTwiceCollapses(t *testing.T) {
	testApp := newApp(t)
	actor, pcs, _ := productFixture(t, testApp)

	product, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID:       actor,
		KodeBarang:    "BRG-003",
		Nama:          "Spidol",
		IDSatuanDasar: pcs,
		Satuan: []model.CreateProductSatuanRequest{
			{IDSatuan: pcs, Faktor: 1, IsDefaultInput: true},
		},
	})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}

	if len(product.Satuan) != 1 {
		t.Fatalf("satuan = %+v, want one row", product.Satuan)
	}

	if !product.Satuan[0].IsDefaultInput {
		t.Error("is_default_input was lost when the base unit collapsed")
	}

	// A base unit sent with any other factor is a contradiction, not something to
	// silently correct.
	_, err = testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID:       actor,
		KodeBarang:    "BRG-004",
		Nama:          "Penghapus",
		IDSatuanDasar: pcs,
		Satuan: []model.CreateProductSatuanRequest{
			{IDSatuan: pcs, Faktor: 12},
		},
	})

	assertKind(t, err, model.KindInvalid)
}

// Issue #1 DoD: duplicate kode barang answers 409, not 500.
func TestProductDuplicateKodeBarangIsConflict(t *testing.T) {
	testApp := newApp(t)
	actor, pcs, _ := productFixture(t, testApp)

	create := func(kode string) error {
		_, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
			ActorID:       actor,
			KodeBarang:    kode,
			Nama:          "Barang",
			IDSatuanDasar: pcs,
		})

		return err
	}

	if err := create("BRG-100"); err != nil {
		t.Fatalf("create product: %v", err)
	}

	assertKind(t, create("BRG-100"), model.KindConflict)
}

// Issue #1 DoD: an overlapping price period answers 409, not 500.
//
// Only the GiST exclusion constraint can catch this — the check spans rows, so no
// pre-check in Go could.
func TestProductOverlappingHargaJualIsConflict(t *testing.T) {
	testApp := newApp(t)
	actor, pcs, _ := productFixture(t, testApp)

	product, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID:       actor,
		KodeBarang:    "BRG-200",
		Nama:          "Kertas F4",
		IDSatuanDasar: pcs,
	})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}

	addHarga := func(harga, dari string) error {
		_, err := testApp.product.AddHargaJual(ctx(), &model.AddProductHargaJualRequest{
			IDProduct:   product.ID,
			ActorID:     actor,
			IDSatuan:    pcs,
			Harga:       harga,
			BerlakuDari: dari,
		})

		return err
	}

	if err := addHarga("50000.00", "2026-01-01"); err != nil {
		t.Fatalf("first price: %v", err)
	}

	// Same start date, so the ranges overlap however the old one is closed.
	assertKind(t, addHarga("55000.00", "2026-01-01"), model.KindConflict)
}

// Opening a new version closes the old one at the new start date. The range is '[)',
// so that leaves neither gap nor overlap.
func TestProductNewHargaJualClosesPrevious(t *testing.T) {
	testApp := newApp(t)
	actor, pcs, _ := productFixture(t, testApp)

	product, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID:       actor,
		KodeBarang:    "BRG-300",
		Nama:          "Tinta",
		IDSatuanDasar: pcs,
	})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}

	for _, price := range []struct{ harga, dari string }{
		{"10000.00", "2026-01-01"},
		{"12000.00", "2026-03-01"},
	} {
		if _, err := testApp.product.AddHargaJual(ctx(), &model.AddProductHargaJualRequest{
			IDProduct:   product.ID,
			ActorID:     actor,
			IDSatuan:    pcs,
			Harga:       price.harga,
			BerlakuDari: price.dari,
		}); err != nil {
			t.Fatalf("price from %s: %v", price.dari, err)
		}
	}

	fetched, err := testApp.product.Get(ctx(), &model.GetProductRequest{ID: product.ID})
	if err != nil {
		t.Fatalf("get product: %v", err)
	}

	if len(fetched.HargaJual) != 2 {
		t.Fatalf("harga_jual = %+v, want two versions", fetched.HargaJual)
	}

	// Newest first.
	current, previous := fetched.HargaJual[0], fetched.HargaJual[1]

	if current.BerlakuSampai != nil {
		t.Errorf("current version is closed at %q, want open-ended", *current.BerlakuSampai)
	}

	if previous.BerlakuSampai == nil {
		t.Fatal("previous version is still open; two prices are valid at once")
	}

	if *previous.BerlakuSampai != "2026-03-01" {
		t.Errorf("previous closed at %q, want 2026-03-01 — the new version's start date, "+
			"since the range is half-open", *previous.BerlakuSampai)
	}

	// NUMERIC(20,2) has to survive the round trip exactly; a float would not.
	if current.Harga != "12000.00" {
		t.Errorf("harga = %q, want 12000.00", current.Harga)
	}
}

// A price for a unit the product is not sold in would never appear on any input screen.
// No foreign key ties product_harga_jual.id_satuan to product_satuan, so the usecase has
// to reject it.
func TestProductHargaJualRejectsUnregisteredSatuan(t *testing.T) {
	testApp := newApp(t)
	actor, pcs, box := productFixture(t, testApp)

	product, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID:       actor,
		KodeBarang:    "BRG-400",
		Nama:          "Amplop",
		IDSatuanDasar: pcs,
	})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}

	_, err = testApp.product.AddHargaJual(ctx(), &model.AddProductHargaJualRequest{
		IDProduct:   product.ID,
		ActorID:     actor,
		IDSatuan:    box, // never registered on this product
		Harga:       "1000.00",
		BerlakuDari: "2026-01-01",
	})

	assertKind(t, err, model.KindInvalid)
}

// product_satuan_default_uidx allows one default per product, so marking a second unit
// as default has to move the flag rather than fail.
func TestProductDefaultSatuanMoves(t *testing.T) {
	testApp := newApp(t)
	actor, pcs, box := productFixture(t, testApp)

	product, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID:       actor,
		KodeBarang:    "BRG-500",
		Nama:          "Lakban",
		IDSatuanDasar: pcs,
		Satuan: []model.CreateProductSatuanRequest{
			{IDSatuan: pcs, Faktor: 1, IsDefaultInput: true},
		},
	})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}

	updated, err := testApp.product.AddSatuan(ctx(), &model.AddProductSatuanRequest{
		IDProduct:      product.ID,
		ActorID:        actor,
		IDSatuan:       box,
		Faktor:         6,
		IsDefaultInput: true,
	})
	if err != nil {
		t.Fatalf("add satuan as default: %v", err)
	}

	defaults := 0
	for _, satuan := range updated.Satuan {
		if satuan.IsDefaultInput {
			defaults++

			if satuan.IDSatuan != box {
				t.Errorf("default is id_satuan %d, want %d", satuan.IDSatuan, box)
			}
		}
	}

	if defaults != 1 {
		t.Errorf("%d units flagged default, want exactly 1", defaults)
	}
}

// Two units flagged default in one create request is ambiguous; the usecase says so
// rather than letting the index reject it with a message naming an index.
func TestProductTwoDefaultSatuanIsInvalid(t *testing.T) {
	testApp := newApp(t)
	actor, pcs, box := productFixture(t, testApp)

	_, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID:       actor,
		KodeBarang:    "BRG-600",
		Nama:          "Stapler",
		IDSatuanDasar: pcs,
		Satuan: []model.CreateProductSatuanRequest{
			{IDSatuan: pcs, Faktor: 1, IsDefaultInput: true},
			{IDSatuan: box, Faktor: 4, IsDefaultInput: true},
		},
	})

	assertKind(t, err, model.KindInvalid)
}

// An unknown id_satuan_dasar is the caller's mistake, so it is a 400 rather than the
// 500 a bare foreign-key error would produce.
func TestProductUnknownSatuanDasarIsInvalid(t *testing.T) {
	testApp := newApp(t)
	actor, pcs, _ := productFixture(t, testApp)

	_, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID:       actor,
		KodeBarang:    "BRG-700",
		Nama:          "Gunting",
		IDSatuanDasar: pcs + 100_000,
	})

	assertKind(t, err, model.KindInvalid)
}

func TestProductPatchAndRetire(t *testing.T) {
	testApp := newApp(t)
	actor, pcs, _ := productFixture(t, testApp)

	product, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID:       actor,
		KodeBarang:    "BRG-800",
		Nama:          "Nama Lama",
		IDSatuanDasar: pcs,
		StokMinimum:   5,
	})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}

	patched, err := testApp.product.Update(ctx(), &model.UpdateProductRequest{
		ID:          product.ID,
		ActorID:     actor,
		Nama:        model.Optional[string]{Present: true, Value: ptr("Nama Baru")},
		StokMinimum: model.Optional[int64]{Present: true, Value: ptr(int64(10))},
	})
	if err != nil {
		t.Fatalf("patch product: %v", err)
	}

	if patched.Nama != "Nama Baru" || patched.StokMinimum != 10 {
		t.Errorf("patched = {%q, %d}, want {Nama Baru, 10}", patched.Nama, patched.StokMinimum)
	}

	// updated_by now carries a real actor, unlike the master-data slices.
	if patched.UpdatedBy == nil || *patched.UpdatedBy != actor {
		t.Errorf("updated_by = %v, want %d", patched.UpdatedBy, actor)
	}

	// kode_barang is untouched: it is absent from the update DTO entirely.
	if patched.KodeBarang != "BRG-800" {
		t.Errorf("kode_barang = %q, want BRG-800", patched.KodeBarang)
	}

	if _, err := testApp.product.Update(ctx(), &model.UpdateProductRequest{
		ID:      product.ID,
		ActorID: actor,
		IsAktif: model.Optional[bool]{Present: true, Value: ptr(false)},
	}); err != nil {
		t.Fatalf("retire product: %v", err)
	}

	active, _, err := testApp.product.Search(ctx(), &model.ListProductRequest{IsAktif: ptr(true)})
	if err != nil {
		t.Fatalf("search active: %v", err)
	}

	if len(active) != 0 {
		t.Errorf("retired product still listed under is_aktif=true: %d rows", len(active))
	}
}

func TestProductPatchEmptyBodyIsInvalid(t *testing.T) {
	testApp := newApp(t)
	actor, pcs, _ := productFixture(t, testApp)

	product, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID:       actor,
		KodeBarang:    "BRG-900",
		Nama:          "Kalkulator",
		IDSatuanDasar: pcs,
	})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}

	_, err = testApp.product.Update(ctx(), &model.UpdateProductRequest{
		ID:      product.ID,
		ActorID: actor,
	})

	assertKind(t, err, model.KindInvalid)
}

func TestProductGetUnknownIDIsNotFound(t *testing.T) {
	testApp := newApp(t)

	_, err := testApp.product.Get(ctx(), &model.GetProductRequest{ID: 999_999})

	assertKind(t, err, model.KindNotFound)
}

// List does not carry children, so the keys are absent rather than empty — fetching
// them per row would be an N+1 that grows with page size.
func TestProductListOmitsChildren(t *testing.T) {
	testApp := newApp(t)
	actor, pcs, box := productFixture(t, testApp)

	if _, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID:       actor,
		KodeBarang:    "BRG-950",
		Nama:          "Buku",
		IDSatuanDasar: pcs,
		Satuan: []model.CreateProductSatuanRequest{
			{IDSatuan: box, Faktor: 10},
		},
	}); err != nil {
		t.Fatalf("create product: %v", err)
	}

	list, paging, err := testApp.product.Search(ctx(), &model.ListProductRequest{})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if paging.TotalItem != 1 || len(list) != 1 {
		t.Fatalf("total_item = %d, len = %d, want 1 and 1", paging.TotalItem, len(list))
	}

	if list[0].Satuan != nil {
		t.Errorf("list carried satuan (%d rows); it should be omitted", len(list[0].Satuan))
	}

	if list[0].NamaSatuanDasar != "PCS" {
		t.Errorf("nama_satuan_dasar = %q, want PCS — it rides along on the join",
			list[0].NamaSatuanDasar)
	}
}
