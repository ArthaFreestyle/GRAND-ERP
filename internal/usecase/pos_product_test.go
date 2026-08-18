package usecase_test

// Isu #11: the POS catalog read. These run against a real PostgreSQL because what
// they pin lives in the query, not in Go — total_item counting products rather than
// (product, satuan) combinations, EscapeLike neutralising a literal % or _, the
// exact-kode_barang tiebreaker in ORDER BY, and stok_akhir actually being scoped to
// the room asked for rather than summed across every room the product has ever been
// in. A mock would happily agree with a wrong query.

import (
	"testing"

	"Arthafreestyle/ERP/internal/model"
)

// posRuangFixture creates one unit_kerja and one ruang under it — everything the POS
// catalog's required id_ruang needs, independent of pembelianFixture's own room,
// since several of these tests have no purchase to post at all.
func posRuangFixture(t *testing.T, testApp *app) int64 {
	t.Helper()

	actor := testActor(t)

	unit, err := testApp.unitKerja.Create(ctx(), &model.CreateUnitKerjaRequest{ActorID: actor, Nama: "Unit POS"})
	if err != nil {
		t.Fatalf("create unit kerja: %v", err)
	}

	ruang, err := testApp.ruang.Create(ctx(), &model.CreateRuangRequest{
		ActorID: actor, NamaRuang: "Kasir", IDUnitKerja: unit.ID,
	})
	if err != nil {
		t.Fatalf("create ruang: %v", err)
	}

	return ruang.ID
}

// total_item must count products, not (product, satuan) rows. A product with three
// satuan eating three slots of a paged catalog is exactly the bug the issue names by
// example: a cashier searching "aqua" and seeing "20 hasil" would expect to scroll
// past twenty products, not find seven.
func TestPOSTotalItemMenghitungProdukBukanKombinasi(t *testing.T) {
	testApp := newApp(t)
	actor, pcs, box := productFixture(t, testApp)
	ruang := posRuangFixture(t, testApp)

	satu, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID: actor, KodeBarang: "POS-001", Nama: "Satu Satuan", IDSatuanDasar: pcs,
	})
	if err != nil {
		t.Fatalf("create product with one satuan: %v", err)
	}

	dua, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID: actor, KodeBarang: "POS-002", Nama: "Dua Satuan", IDSatuanDasar: pcs,
		Satuan: []model.CreateProductSatuanRequest{{IDSatuan: box, Faktor: 6}},
	})
	if err != nil {
		t.Fatalf("create product with two satuan: %v", err)
	}

	list, paging, err := testApp.product.POS(ctx(), &model.ListPosProductRequest{IDRuang: ruang})
	if err != nil {
		t.Fatalf("pos: %v", err)
	}

	if paging.TotalItem != 2 || len(list) != 2 {
		t.Fatalf("baris = %d, total_item = %d, want 2 dan 2 (produk, bukan kombinasi satuan)",
			len(list), paging.TotalItem)
	}

	for _, row := range list {
		switch row.ID {
		case satu.ID:
			if len(row.Satuan) != 1 {
				t.Errorf("produk satu satuan punya %d baris satuan, want 1", len(row.Satuan))
			}
		case dua.ID:
			if len(row.Satuan) != 2 {
				t.Errorf("produk dua satuan punya %d baris satuan, want 2", len(row.Satuan))
			}
		default:
			t.Errorf("produk tak dikenal %d di hasil", row.ID)
		}
	}
}

// A retired product must never appear here, with or without a search term — this
// screen is what a cashier sells from, and a switch to reveal it would be a switch
// to sell it.
func TestPOSBarangNonAktifTidakPernahMuncul(t *testing.T) {
	testApp := newApp(t)
	actor, pcs, _ := productFixture(t, testApp)
	ruang := posRuangFixture(t, testApp)

	aktif, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID: actor, KodeBarang: "POS-010", Nama: "Aktif", IDSatuanDasar: pcs,
	})
	if err != nil {
		t.Fatalf("create active product: %v", err)
	}

	nonaktif, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID: actor, KodeBarang: "POS-011", Nama: "Nonaktif", IDSatuanDasar: pcs,
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

	list, paging, err := testApp.product.POS(ctx(), &model.ListPosProductRequest{IDRuang: ruang})
	if err != nil {
		t.Fatalf("pos tanpa search: %v", err)
	}
	if paging.TotalItem != 1 || len(list) != 1 || list[0].ID != aktif.ID {
		t.Fatalf("hasil tanpa search = %+v (total %d), want hanya produk aktif", list, paging.TotalItem)
	}

	// The retired product's own name has to be searchable-shaped, or a search that
	// simply finds nothing would not prove the is_aktif filter did the excluding.
	list, paging, err = testApp.product.POS(ctx(), &model.ListPosProductRequest{
		IDRuang: ruang, Search: "Nonaktif",
	})
	if err != nil {
		t.Fatalf("pos dengan search: %v", err)
	}
	if paging.TotalItem != 0 || len(list) != 0 {
		t.Fatalf("search \"Nonaktif\" = %+v (total %d), want kosong", list, paging.TotalItem)
	}
}

// A product with no price version in force still has to appear, with harga null on
// every satuan — a price is a proposal, never a requirement, and filtering priceless
// products out here would quietly undo that decision (isu #8). A room the product has
// never moved through answers stok_akhir: 0, not a missing row — hiding it would tell
// a cashier the product does not exist, when the true answer is that it is out.
func TestPOSBarangTanpaHargaDanTanpaPergerakan(t *testing.T) {
	testApp := newApp(t)
	actor, pcs, _ := productFixture(t, testApp)
	ruang := posRuangFixture(t, testApp)

	product, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID: actor, KodeBarang: "POS-020", Nama: "Belum Berharga", IDSatuanDasar: pcs,
	})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}

	list, _, err := testApp.product.POS(ctx(), &model.ListPosProductRequest{IDRuang: ruang})
	if err != nil {
		t.Fatalf("pos: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("baris = %d, want 1", len(list))
	}

	row := list[0]
	if row.ID != product.ID {
		t.Fatalf("id = %d, want %d", row.ID, product.ID)
	}
	if row.StokAkhir != 0 {
		t.Errorf("stok_akhir = %d, want 0 (belum pernah bergerak)", row.StokAkhir)
	}
	if len(row.Satuan) != 1 {
		t.Fatalf("satuan = %d baris, want 1 (satuan dasar saja)", len(row.Satuan))
	}
	if row.Satuan[0].IDHargaJual != nil || row.Satuan[0].Harga != nil {
		t.Errorf("satuan dasar = %+v, want id_harga_jual dan harga null", row.Satuan[0])
	}
	if row.Satuan[0].Faktor != 1 {
		t.Errorf("faktor satuan dasar = %d, want 1", row.Satuan[0].Faktor)
	}
}

// stok_akhir belongs to the room actually asked for, not the sum across every room —
// the same product posted into two different rooms must answer two different figures.
func TestPOSStokMilikRuangYangDiminta(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	ruangKedua, err := testApp.ruang.Create(ctx(), &model.CreateRuangRequest{
		ActorID: f.actor, NamaRuang: "Gudang Kedua", IDUnitKerja: f.unitKerja,
	})
	if err != nil {
		t.Fatalf("create ruang kedua: %v", err)
	}

	// 10 pcs into the fixture's own room.
	pertama, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
		ActorID: f.actor, Tanggal: "2026-08-01", IDSupplier: f.supplier, IDRuang: f.ruang,
		Detail: []model.PembelianDetailRequest{{
			IDProduct: f.product, IDSatuanInput: f.pcs, QtyFaktur: "10", HargaSatuanInput: "10000",
		}},
	})
	if err != nil {
		t.Fatalf("create pembelian ruang pertama: %v", err)
	}
	ajukanDanPosting(t, testApp, f, pertama.ID)

	// 4 pcs into the second room.
	kedua, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
		ActorID: f.actor, Tanggal: "2026-08-01", IDSupplier: f.supplier, IDRuang: ruangKedua.ID,
		Detail: []model.PembelianDetailRequest{{
			IDProduct: f.product, IDSatuanInput: f.pcs, QtyFaktur: "4", HargaSatuanInput: "10000",
		}},
	})
	if err != nil {
		t.Fatalf("create pembelian ruang kedua: %v", err)
	}
	ajukanDanPosting(t, testApp, f, kedua.ID)

	listPertama, _, err := testApp.product.POS(ctx(), &model.ListPosProductRequest{IDRuang: f.ruang})
	if err != nil {
		t.Fatalf("pos ruang pertama: %v", err)
	}
	if len(listPertama) != 1 || listPertama[0].StokAkhir != 10 {
		t.Fatalf("ruang pertama = %+v, want stok_akhir 10", listPertama)
	}

	listKedua, _, err := testApp.product.POS(ctx(), &model.ListPosProductRequest{IDRuang: ruangKedua.ID})
	if err != nil {
		t.Fatalf("pos ruang kedua: %v", err)
	}
	if len(listKedua) != 1 || listKedua[0].StokAkhir != 4 {
		t.Fatalf("ruang kedua = %+v, want stok_akhir 4", listKedua)
	}
}

// A product literally named or coded with a % or _ must not act as an ILIKE
// wildcard. Unescaped, the first test would match every product in the catalog and
// the second would match any single-character difference — both are the search a
// cashier actually types at a counter, scanning or typing a code with a symbol in it.
func TestPOSSearchMemperlakukanPercentDanUnderscoreSecaraHarfiah(t *testing.T) {
	testApp := newApp(t)
	actor, pcs, _ := productFixture(t, testApp)
	ruang := posRuangFixture(t, testApp)

	if _, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID: actor, KodeBarang: "POS-030", Nama: "Diskon 100%", IDSatuanDasar: pcs,
	}); err != nil {
		t.Fatalf("create product named with a percent sign: %v", err)
	}
	if _, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID: actor, KodeBarang: "POS-031", Nama: "Barang Biasa", IDSatuanDasar: pcs,
	}); err != nil {
		t.Fatalf("create unrelated product: %v", err)
	}

	_, paging, err := testApp.product.POS(ctx(), &model.ListPosProductRequest{
		IDRuang: ruang, Search: "100%",
	})
	if err != nil {
		t.Fatalf("search percent: %v", err)
	}
	if paging.TotalItem != 1 {
		t.Fatalf(`search "100%%" matched %d rows, want exactly 1 — an unescaped %% matches everything`,
			paging.TotalItem)
	}

	if _, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID: actor, KodeBarang: "A_B", Nama: "Kode Bergaris Bawah", IDSatuanDasar: pcs,
	}); err != nil {
		t.Fatalf("create product with underscore kode: %v", err)
	}
	if _, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID: actor, KodeBarang: "AXB", Nama: "Kode Mirip", IDSatuanDasar: pcs,
	}); err != nil {
		t.Fatalf("create similar-kode product: %v", err)
	}

	_, paging, err = testApp.product.POS(ctx(), &model.ListPosProductRequest{
		IDRuang: ruang, Search: "A_B",
	})
	if err != nil {
		t.Fatalf("search underscore: %v", err)
	}
	if paging.TotalItem != 1 {
		t.Fatalf(`search "A_B" matched %d rows, want exactly 1 — an unescaped _ matches any character`,
			paging.TotalItem)
	}
}

// An exact kode_barang match sorts first, ahead of alphabetical order by nama — a
// cashier who scans or types a full code expects that one row at the top, not buried
// in a page sorted by name.
func TestPOSKecocokanPersisKodeBarangDiBarisPertama(t *testing.T) {
	testApp := newApp(t)
	actor, pcs, _ := productFixture(t, testApp)
	ruang := posRuangFixture(t, testApp)

	// "Apple" sorts before "Zebra" alphabetically, so without the exact-match
	// tiebreaker this product would win the ordering on nama alone.
	if _, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID: actor, KodeBarang: "100X", Nama: "Apple", IDSatuanDasar: pcs,
	}); err != nil {
		t.Fatalf("create substring-matching product: %v", err)
	}

	exact, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID: actor, KodeBarang: "100", Nama: "Zebra", IDSatuanDasar: pcs,
	})
	if err != nil {
		t.Fatalf("create exact-match product: %v", err)
	}

	list, paging, err := testApp.product.POS(ctx(), &model.ListPosProductRequest{
		IDRuang: ruang, Search: "100",
	})
	if err != nil {
		t.Fatalf("pos: %v", err)
	}
	if paging.TotalItem != 2 || len(list) != 2 {
		t.Fatalf("baris = %d, total_item = %d, want 2 dan 2", len(list), paging.TotalItem)
	}
	if list[0].ID != exact.ID {
		t.Fatalf("baris pertama = %+v, want kode_barang persis \"100\" di puncak", list[0])
	}
}

// Paging stays stable when many products share the same nama: ORDER BY has to end in
// a unique column, or one product could appear on two pages while another never comes
// back at all.
func TestPOSPaginasiStabil(t *testing.T) {
	testApp := newApp(t)
	actor, pcs, _ := productFixture(t, testApp)
	ruang := posRuangFixture(t, testApp)

	ids := make(map[int64]bool, 3)
	for i, kode := range []string{"POS-040", "POS-041", "POS-042"} {
		product, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
			ActorID: actor, KodeBarang: kode, Nama: "Sama Persis", IDSatuanDasar: pcs,
		})
		if err != nil {
			t.Fatalf("create product %d: %v", i, err)
		}
		ids[product.ID] = false
	}

	terlihat := map[int64]bool{}

	for halaman := 1; halaman <= 3; halaman++ {
		list, paging, err := testApp.product.POS(ctx(), &model.ListPosProductRequest{
			PageRequest: model.PageRequest{Page: halaman, Size: 1},
			IDRuang:     ruang,
		})
		if err != nil {
			t.Fatalf("pos halaman %d: %v", halaman, err)
		}

		if paging.TotalItem != 3 || paging.TotalPage != 3 {
			t.Errorf("halaman %d: total_item = %d, total_page = %d, want 3 dan 3",
				halaman, paging.TotalItem, paging.TotalPage)
		}
		if len(list) != 1 {
			t.Fatalf("halaman %d: baris = %d, want 1", halaman, len(list))
		}
		if terlihat[list[0].ID] {
			t.Fatalf("produk %d muncul di dua halaman", list[0].ID)
		}
		terlihat[list[0].ID] = true
	}

	if len(terlihat) != 3 {
		t.Errorf("produk terlihat = %d, want 3", len(terlihat))
	}
}

// A mistyped id_ruang must answer 404, not a page where every row silently reads
// stok_akhir: 0 — that would be an expensive bug to ever notice.
func TestPOSIDRuangTidakDikenal404(t *testing.T) {
	testApp := newApp(t)
	_, _, err := testApp.product.POS(ctx(), &model.ListPosProductRequest{IDRuang: 999999})
	assertKind(t, err, model.KindNotFound)
}

// tanggal picks which price version is in force, the same resolver isu #8 built and
// this endpoint reuses rather than re-deciding.
func TestPOSTanggalMenentukanVersiHarga(t *testing.T) {
	testApp := newApp(t)
	actor, pcs, _ := productFixture(t, testApp)
	ruang := posRuangFixture(t, testApp)

	product, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID: actor, KodeBarang: "POS-050", Nama: "Berharga Nanti", IDSatuanDasar: pcs,
	})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}
	if _, err := testApp.product.AddHargaJual(ctx(), &model.AddProductHargaJualRequest{
		IDProduct: product.ID, ActorID: actor, IDSatuan: pcs,
		Harga: "25000.00", BerlakuDari: "2026-06-01",
	}); err != nil {
		t.Fatalf("open harga jual: %v", err)
	}

	before, _, err := testApp.product.POS(ctx(), &model.ListPosProductRequest{
		IDRuang: ruang, Tanggal: "2026-01-01",
	})
	if err != nil {
		t.Fatalf("pos before berlaku_dari: %v", err)
	}
	if len(before) != 1 || before[0].Satuan[0].Harga != nil {
		t.Fatalf("sebelum berlaku_dari = %+v, want harga null", before)
	}

	after, _, err := testApp.product.POS(ctx(), &model.ListPosProductRequest{
		IDRuang: ruang, Tanggal: "2026-07-01",
	})
	if err != nil {
		t.Fatalf("pos after berlaku_dari: %v", err)
	}
	if len(after) != 1 || after[0].Satuan[0].Harga == nil || *after[0].Satuan[0].Harga != "25000.00" {
		t.Fatalf("setelah berlaku_dari = %+v, want harga 25000.00", after)
	}
	if after[0].Satuan[0].IDHargaJual == nil {
		t.Error("id_harga_jual = nil, want terisi — penjualan_detail perlu tahu versi mana asalnya")
	}
}
