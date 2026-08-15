package usecase_test

// Isu #8 fase 1. These run against a real PostgreSQL because the resolver's whole
// argument is that product_harga_jual_no_overlap makes "which version applies" a
// plain WHERE rather than a DISTINCT ON — that only means something if the exclusion
// constraint itself is in play, which a mock cannot exercise.

import (
	"testing"
	"time"

	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/repository"
)

func mustParseDate(t *testing.T, s string) time.Time {
	t.Helper()

	parsed, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatalf("parse date fixture %q: %v", s, err)
	}

	return parsed
}

func TestHargaBerlakuResolvesVersionInForceOnDate(t *testing.T) {
	testApp := newApp(t)
	actor, pcs, _ := productFixture(t, testApp)

	product, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID:       actor,
		KodeBarang:    "BRG-500",
		Nama:          "Gunting",
		IDSatuanDasar: pcs,
	})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}

	for _, harga := range []struct{ harga, dari string }{
		{"15000.00", "2026-01-01"},
		{"18000.00", "2026-06-01"},
	} {
		if _, err := testApp.product.AddHargaJual(ctx(), &model.AddProductHargaJualRequest{
			IDProduct:   product.ID,
			ActorID:     actor,
			IDSatuan:    pcs,
			Harga:       harga.harga,
			BerlakuDari: harga.dari,
		}); err != nil {
			t.Fatalf("price from %s: %v", harga.dari, err)
		}
	}

	// Before the second version opens: the first still applies.
	before, err := testApp.product.HargaBerlaku(ctx(), &model.ListHargaJualBerlakuRequest{
		IDProduct: product.ID, Tanggal: "2026-03-01",
	})
	if err != nil {
		t.Fatalf("harga berlaku 2026-03-01: %v", err)
	}
	if len(before) != 1 || before[0].Harga != "15000.00" {
		t.Fatalf("harga berlaku 2026-03-01 = %+v, want one row at 15000.00", before)
	}

	// Exactly on the second version's start date: the range is '[)', so it already
	// applies from this day.
	onBoundary, err := testApp.product.HargaBerlaku(ctx(), &model.ListHargaJualBerlakuRequest{
		IDProduct: product.ID, Tanggal: "2026-06-01",
	})
	if err != nil {
		t.Fatalf("harga berlaku 2026-06-01: %v", err)
	}
	if len(onBoundary) != 1 || onBoundary[0].Harga != "18000.00" {
		t.Fatalf("harga berlaku 2026-06-01 = %+v, want one row at 18000.00", onBoundary)
	}

	if onBoundary[0].IDHargaJual == 0 {
		t.Error("id_harga_jual is zero; penjualan_detail.id_harga_jual needs a real id")
	}
	if onBoundary[0].BerlakuSampai != nil {
		t.Errorf("berlaku_sampai = %q, want open-ended for the current version", *onBoundary[0].BerlakuSampai)
	}
}

// Before any version's berlaku_dari, and for a product with no price at all: an
// empty list, never an error. "No price yet" and "unknown product" are different
// facts, same argument riwayat_beli makes.
func TestHargaBerlakuNoVersionInForceIsEmptyList(t *testing.T) {
	testApp := newApp(t)
	actor, pcs, _ := productFixture(t, testApp)

	product, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID:       actor,
		KodeBarang:    "BRG-501",
		Nama:          "Stapler",
		IDSatuanDasar: pcs,
	})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}

	list, err := testApp.product.HargaBerlaku(ctx(), &model.ListHargaJualBerlakuRequest{IDProduct: product.ID})
	if err != nil {
		t.Fatalf("harga berlaku on a priceless product: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("harga berlaku = %+v, want an empty list", list)
	}

	if _, err := testApp.product.AddHargaJual(ctx(), &model.AddProductHargaJualRequest{
		IDProduct:   product.ID,
		ActorID:     actor,
		IDSatuan:    pcs,
		Harga:       "5000.00",
		BerlakuDari: "2026-06-01",
	}); err != nil {
		t.Fatalf("add price: %v", err)
	}

	before, err := testApp.product.HargaBerlaku(ctx(), &model.ListHargaJualBerlakuRequest{
		IDProduct: product.ID, Tanggal: "2026-01-01",
	})
	if err != nil {
		t.Fatalf("harga berlaku before berlaku_dari: %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("harga berlaku before berlaku_dari = %+v, want empty", before)
	}
}

func TestHargaBerlakuUnknownProductIsNotFound(t *testing.T) {
	testApp := newApp(t)

	_, err := testApp.product.HargaBerlaku(ctx(), &model.ListHargaJualBerlakuRequest{IDProduct: 999_999})

	assertKind(t, err, model.KindNotFound)
}

func TestHargaBerlakuInvalidTanggalIsInvalid(t *testing.T) {
	testApp := newApp(t)
	actor, pcs, _ := productFixture(t, testApp)

	product, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID:       actor,
		KodeBarang:    "BRG-502",
		Nama:          "Map",
		IDSatuanDasar: pcs,
	})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}

	// Caught by the datetime tag before the usecase's own parse ever runs — the
	// tag's own validator.ValidationErrors surfaces here, same as an equally
	// malformed berlaku_dari does on AddHargaJual.
	if _, err = testApp.product.HargaBerlaku(ctx(), &model.ListHargaJualBerlakuRequest{
		IDProduct: product.ID, Tanggal: "not-a-date",
	}); err == nil {
		t.Fatal("expected an error for a malformed tanggal, got nil")
	}
}

// The batch resolver isu #8 fase 1 builds for penjualan: one query for a whole basket
// of (product, satuan) pairs, mirroring FindFaktorBatch. Nothing calls it yet, so it
// is exercised directly against the repository the way riwayat_beli's query would be
// if penjualan already existed to call it.
func TestHargaBerlakuBatchResolvesWholeBasket(t *testing.T) {
	testApp := newApp(t)
	actor, pcs, box := productFixture(t, testApp)

	kertas, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID:       actor,
		KodeBarang:    "BRG-503",
		Nama:          "Kertas",
		IDSatuanDasar: pcs,
		Satuan: []model.CreateProductSatuanRequest{
			{IDSatuan: box, Faktor: 10},
		},
	})
	if err != nil {
		t.Fatalf("create kertas: %v", err)
	}

	tinta, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID:       actor,
		KodeBarang:    "BRG-504",
		Nama:          "Tinta",
		IDSatuanDasar: pcs,
	})
	if err != nil {
		t.Fatalf("create tinta: %v", err)
	}

	if _, err := testApp.product.AddHargaJual(ctx(), &model.AddProductHargaJualRequest{
		IDProduct: kertas.ID, ActorID: actor, IDSatuan: pcs,
		Harga: "1000.00", BerlakuDari: "2026-01-01",
	}); err != nil {
		t.Fatalf("price kertas/pcs: %v", err)
	}
	if _, err := testApp.product.AddHargaJual(ctx(), &model.AddProductHargaJualRequest{
		IDProduct: kertas.ID, ActorID: actor, IDSatuan: box,
		Harga: "9000.00", BerlakuDari: "2026-01-01",
	}); err != nil {
		t.Fatalf("price kertas/box: %v", err)
	}
	// tinta is left with no price at all — the basket asks for it anyway, and the
	// missing entry is how the caller learns there is none.

	basket, err := testApp.product.ProductRepository.FindHargaBerlakuBatch(
		ctx(), testApp.product.DB,
		[]int64{kertas.ID, kertas.ID, tinta.ID},
		[]int64{pcs, box, pcs},
		mustParseDate(t, "2026-06-01"),
	)
	if err != nil {
		t.Fatalf("find harga berlaku batch: %v", err)
	}

	if len(basket) != 2 {
		t.Fatalf("basket = %+v, want exactly the two priced pairs", basket)
	}

	pcsHarga, ok := basket[repository.HargaBerlakuKey{IDProduct: kertas.ID, IDSatuan: pcs}]
	if !ok || pcsHarga.Harga != "1000.00" {
		t.Errorf("kertas/pcs = %+v, ok=%v, want 1000.00", pcsHarga, ok)
	}

	boxHarga, ok := basket[repository.HargaBerlakuKey{IDProduct: kertas.ID, IDSatuan: box}]
	if !ok || boxHarga.Harga != "9000.00" {
		t.Errorf("kertas/box = %+v, ok=%v, want 9000.00", boxHarga, ok)
	}

	if _, ok := basket[repository.HargaBerlakuKey{IDProduct: tinta.ID, IDSatuan: pcs}]; ok {
		t.Error("tinta/pcs is present in the basket; it has no price and should be absent")
	}
}
