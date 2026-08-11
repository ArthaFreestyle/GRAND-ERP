package usecase_test

// Isu #4 fase 4. These run against a real PostgreSQL because what they assert is the
// query: DISTINCT ON collapsing many purchases into one row per supplier, NUMERIC
// division producing an exact per-base-unit price, and the POSTED filter excluding
// documents that were only typed or later withdrawn. A mock would agree with whatever
// SQL was written.

import (
	"testing"

	"Arthafreestyle/ERP/internal/model"
)

// beliPosted types a one-line purchase from one supplier and walks it to POSTED.
func beliPosted(t *testing.T, testApp *app, f fixture, idSupplier int64, tanggal, qty, harga string) *model.PembelianResponse {
	t.Helper()

	pembelian, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
		ActorID:    f.actor,
		Tanggal:    tanggal,
		IDSupplier: idSupplier,
		IDRuang:    f.ruang,
		Detail: []model.PembelianDetailRequest{{
			IDProduct:        f.product,
			IDSatuanInput:    f.pcs,
			QtyFaktur:        qty,
			HargaSatuanInput: harga,
		}},
	})
	if err != nil {
		t.Fatalf("create pembelian: %v", err)
	}

	return ajukanDanPosting(t, testApp, f, pembelian.ID)
}

// supplierBaru adds one more supplier, since the shared fixture only makes one and
// the whole point of this query is comparing several.
func supplierBaru(t *testing.T, testApp *app, nama string) int64 {
	t.Helper()

	supplier, err := testApp.supplier.Create(ctx(), &model.CreateSupplierRequest{Nama: nama})
	if err != nil {
		t.Fatalf("create supplier %s: %v", nama, err)
	}

	return supplier.ID
}

func riwayat(t *testing.T, testApp *app, f fixture, idSupplier int64) ([]model.RiwayatBeliResponse, *model.PageMetadata) {
	t.Helper()

	list, paging, err := testApp.product.RiwayatBeli(ctx(), &model.ListRiwayatBeliRequest{
		IDProduct:  f.product,
		IDSupplier: idSupplier,
	})
	if err != nil {
		t.Fatalf("riwayat beli: %v", err)
	}

	return list, paging
}

// One row per supplier, and it is that supplier's most recent purchase — not their
// first, and not one row per document. The list is newest first, which is the order
// the question is asked in.
func TestRiwayatBeliSatuBarisTerakhirPerSupplier(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)
	kedua := supplierBaru(t, testApp, "CV Aneka")

	beliPosted(t, testApp, f, f.supplier, "2026-08-01", "10", "9000")
	beliPosted(t, testApp, f, kedua, "2026-08-03", "10", "11000")
	terbaru := beliPosted(t, testApp, f, f.supplier, "2026-08-05", "10", "10000")

	list, paging := riwayat(t, testApp, f, 0)

	if len(list) != 2 {
		t.Fatalf("baris = %d, want 2 (satu per supplier)", len(list))
	}

	if paging.TotalItem != 2 {
		t.Errorf("total_item = %d, want 2", paging.TotalItem)
	}

	// Newest first: the 5 August document from the fixture supplier, then the
	// 3 August one from the other.
	if list[0].IDSupplier != f.supplier {
		t.Errorf("baris pertama dari supplier %d, want %d", list[0].IDSupplier, f.supplier)
	}

	if list[0].IDPembelian != terbaru.ID {
		t.Errorf("id_pembelian = %d, want %d (yang terakhir, bukan yang pertama)",
			list[0].IDPembelian, terbaru.ID)
	}

	if list[0].HargaSatuanDasar != "10000.0000" {
		t.Errorf("harga_satuan_dasar = %q, want 10000.0000", list[0].HargaSatuanDasar)
	}

	if list[1].IDSupplier != kedua {
		t.Errorf("baris kedua dari supplier %d, want %d", list[1].IDSupplier, kedua)
	}

	if list[1].NamaSupplier != "CV Aneka" {
		t.Errorf("nama_supplier = %q, want CV Aneka", list[1].NamaSupplier)
	}
}

// Only prices somebody actually paid. A DRAFT is a typed page and a BATAL is a
// purchase that was withdrawn; neither belongs in a comparison of what things cost.
func TestRiwayatBeliHanyaDokumenPosted(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	dibatalkan := beliPosted(t, testApp, f, f.supplier, "2026-08-02", "10", "12000")

	if _, err := testApp.pembelian.Batal(ctx(), &model.BatalPembelianRequest{
		ID: dibatalkan.ID, ActorID: f.actor, AlasanBatal: "salah nota",
	}); err != nil {
		t.Fatalf("batal: %v", err)
	}

	// A draft from the same supplier, left where it is.
	draftSederhana(t, testApp, f, "10", nil, nil)

	if list, paging := riwayat(t, testApp, f, 0); len(list) != 0 || paging.TotalItem != 0 {
		t.Fatalf("baris = %d, total_item = %d, want 0 dan 0", len(list), paging.TotalItem)
	}

	// One posted document later, and the same supplier appears — proving the filter
	// is on status and not on something about the supplier.
	posted := beliPosted(t, testApp, f, f.supplier, "2026-08-04", "10", "10000")

	list, _ := riwayat(t, testApp, f, 0)
	if len(list) != 1 || list[0].IDPembelian != posted.ID {
		t.Fatalf("riwayat = %+v, want satu baris untuk pembelian %d", list, posted.ID)
	}
}

// The two prices answer different questions and must not collapse into one. The
// invoice price is what a supplier's next quote is compared against; the cost price
// is what the goods were worth on the shelf, freight included.
//
// 10 DUS of 12 at 120.000 per DUS is 1.200.000 for 120 pcs — 10.000 per pcs on the
// paper. A 60.000 carrier bill on top makes it 10.500 per pcs once landed.
func TestRiwayatBeliMembedakanHargaFakturDanHargaPokok(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	totalKoli := "10"
	tarif := "6000"

	pembelian, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
		ActorID:      f.actor,
		Tanggal:      "2026-08-11",
		IDSupplier:   f.supplier,
		IDRuang:      f.ruang,
		TotalKoli:    &totalKoli,
		TarifPerKoli: &tarif,
		Detail: []model.PembelianDetailRequest{{
			IDProduct:        f.product,
			IDSatuanInput:    f.dus,
			QtyFaktur:        "10",
			HargaSatuanInput: "120000",
			JumlahKoli:       "10",
		}},
	})
	if err != nil {
		t.Fatalf("create pembelian: %v", err)
	}

	ajukanDanPosting(t, testApp, f, pembelian.ID)

	list, _ := riwayat(t, testApp, f, 0)
	if len(list) != 1 {
		t.Fatalf("baris = %d, want 1", len(list))
	}

	baris := list[0]

	// Quantities keep both units: the paper says 10 DUS, the ledger says 120 PCS.
	if baris.QtyFaktur != "10.0000" || baris.NamaSatuan != "DUS" {
		t.Errorf("qty_faktur = %q %s, want 10.0000 DUS", baris.QtyFaktur, baris.NamaSatuan)
	}

	if baris.QtyDasar != 120 || baris.NamaSatuanDasar != "PCS" {
		t.Errorf("qty_dasar = %d %s, want 120 PCS", baris.QtyDasar, baris.NamaSatuanDasar)
	}

	if baris.FaktorKonversi != 12 {
		t.Errorf("faktor_konversi = %d, want 12", baris.FaktorKonversi)
	}

	// Per input unit — what is printed on the nota, and not comparable across
	// suppliers who quote per PCS.
	if baris.HargaSatuanInput != "120000.00" {
		t.Errorf("harga_satuan_input = %q, want 120000.00", baris.HargaSatuanInput)
	}

	if baris.HargaSatuanDasar != "10000.0000" {
		t.Errorf("harga_satuan_dasar = %q, want 10000.0000", baris.HargaSatuanDasar)
	}

	if baris.AlokasiBiaya != "60000.00" {
		t.Errorf("alokasi_biaya = %q, want 60000.00", baris.AlokasiBiaya)
	}

	if baris.HargaPokokSatuanDasar == nil || *baris.HargaPokokSatuanDasar != "10500.0000" {
		t.Fatalf("harga_pokok_satuan_dasar = %v, want 10500.0000", baris.HargaPokokSatuanDasar)
	}
}

// The purchase input screen already knows who the goods are coming from, so the
// useful question there is narrower than "what did everyone charge".
func TestRiwayatBeliFilterSupplier(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)
	kedua := supplierBaru(t, testApp, "CV Aneka")

	beliPosted(t, testApp, f, f.supplier, "2026-08-01", "10", "9000")
	beliPosted(t, testApp, f, kedua, "2026-08-03", "10", "11000")

	list, paging := riwayat(t, testApp, f, kedua)

	if len(list) != 1 || paging.TotalItem != 1 {
		t.Fatalf("baris = %d, total_item = %d, want 1 dan 1", len(list), paging.TotalItem)
	}

	if list[0].IDSupplier != kedua {
		t.Errorf("id_supplier = %d, want %d", list[0].IDSupplier, kedua)
	}

	if list[0].HargaSatuanDasar != "11000.0000" {
		t.Errorf("harga_satuan_dasar = %q, want 11000.0000", list[0].HargaSatuanDasar)
	}
}

// An id nobody ever bought from and an id that is not a product are different facts,
// and a client that cannot tell them apart shows the wrong message. The first is an
// empty page; the second is a 404.
func TestRiwayatBeliProdukKosongDanTidakAda(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	list, paging := riwayat(t, testApp, f, 0)

	if list == nil {
		t.Fatal("data = null, want [] — sebuah produk yang belum pernah dibeli tetap ada")
	}

	if len(list) != 0 || paging.TotalItem != 0 {
		t.Errorf("baris = %d, total_item = %d, want 0 dan 0", len(list), paging.TotalItem)
	}

	_, _, err := testApp.product.RiwayatBeli(ctx(), &model.ListRiwayatBeliRequest{
		IDProduct: f.product + 9999,
	})
	assertKind(t, err, model.KindNotFound)
}

// Paging is stable because the outer ORDER BY ends in id_supplier, which DISTINCT ON
// made unique across the result. Ordering on tanggal alone would let one supplier
// appear on both pages while another never comes back — two purchases on the same day
// is the ordinary case, not a contrived one.
func TestRiwayatBeliPaginasiStabil(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	pemasok := []int64{f.supplier, supplierBaru(t, testApp, "CV Aneka"), supplierBaru(t, testApp, "UD Jaya")}
	for _, id := range pemasok {
		beliPosted(t, testApp, f, id, "2026-08-06", "10", "10000")
	}

	terlihat := map[int64]bool{}

	for halaman := 1; halaman <= 3; halaman++ {
		list, paging, err := testApp.product.RiwayatBeli(ctx(), &model.ListRiwayatBeliRequest{
			PageRequest: model.PageRequest{Page: halaman, Size: 1},
			IDProduct:   f.product,
		})
		if err != nil {
			t.Fatalf("riwayat halaman %d: %v", halaman, err)
		}

		if paging.TotalItem != 3 || paging.TotalPage != 3 {
			t.Errorf("halaman %d: total_item = %d, total_page = %d, want 3 dan 3",
				halaman, paging.TotalItem, paging.TotalPage)
		}

		if len(list) != 1 {
			t.Fatalf("halaman %d: baris = %d, want 1", halaman, len(list))
		}

		if terlihat[list[0].IDSupplier] {
			t.Fatalf("supplier %d muncul di dua halaman", list[0].IDSupplier)
		}

		terlihat[list[0].IDSupplier] = true
	}

	if len(terlihat) != 3 {
		t.Errorf("supplier terlihat = %d, want 3", len(terlihat))
	}
}
