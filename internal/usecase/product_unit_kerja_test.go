package usecase_test

// What these prove: the per-unit catalog (product_unit_kerja) is enforced where goods are
// actually posted, not only where a product is looked at.
//
// One file spanning every module that moves goods, because the behaviour is the same
// rule repeated — reading the repetitions together is what makes a deliberate exception
// (stok_opname's surplus-only check, mutasi's destination check) legible as a decision
// rather than an omission, the reasoning fase6_read_scope_test.go already gives for
// itself.

import (
	"strings"
	"testing"
	"time"

	"Arthafreestyle/ERP/internal/model"
)

// unitDiKatalog reads back which units carry a product, as ids in a set.
func unitDiKatalog(t *testing.T, testApp *app, idProduct int64) map[int64]bool {
	t.Helper()

	product, err := testApp.product.Get(ctx(), &model.GetProductRequest{ID: idProduct})
	if err != nil {
		t.Fatalf("get product: %v", err)
	}
	if product.UnitKerja == nil {
		t.Fatalf("detail read tidak membawa unit_kerja")
	}

	ada := make(map[int64]bool, len(*product.UnitKerja))
	for _, u := range *product.UnitKerja {
		ada[u.IDUnitKerja] = true
	}

	return ada
}

// setKatalog replaces a product's catalog and fails the test if that is refused.
func setKatalog(t *testing.T, testApp *app, f fixture, idProduct int64, unit ...int64) {
	t.Helper()

	if unit == nil {
		unit = []int64{}
	}

	if _, err := testApp.product.SetUnitKerja(ctx(), &model.SetProductUnitKerjaRequest{
		IDProduct: idProduct, ActorID: f.actor, IDUnitKerja: unit,
	}); err != nil {
		t.Fatalf("set unit_kerja: %v", err)
	}
}

func TestProductCreateKatalogAwal(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)
	unitB := createUnit(t, testApp, "Unit B Katalog Awal")

	buat := func(kode string, aktif *int64) (*model.ProductResponse, error) {
		return testApp.product.Create(ctx(), &model.CreateProductRequest{
			ActorID: f.actor, KodeBarang: kode, Nama: kode, IDSatuanDasar: f.pcs, AktifIDUnitKerja: aktif,
		})
	}

	// A session active in a unit starts the product in that unit only — what one outlet
	// adds is not silently sold at every other outlet.
	satu, err := buat("KAT-SATU", &unitB)
	if err != nil {
		t.Fatalf("create dengan unit aktif: %v", err)
	}
	if got := unitDiKatalog(t, testApp, satu.ID); !got[unitB] || len(got) != 1 {
		t.Fatalf("katalog = %v, want hanya unit aktif sesi (B)", got)
	}

	// A global session (no active unit) starts it in every active unit.
	semua, err := buat("KAT-SEMUA", nil)
	if err != nil {
		t.Fatalf("create sesi global: %v", err)
	}
	if got := unitDiKatalog(t, testApp, semua.ID); !got[f.unitKerja] || !got[unitB] || len(got) != 2 {
		t.Fatalf("katalog sesi global = %v, want kedua unit aktif", got)
	}

	// The unit embedded in a token can have been retired since; refusing it is the
	// usecase's job, since the foreign key cannot tell retired from live.
	gaib := int64(999999)
	_, err = buat("KAT-GAIB", &gaib)
	assertKind(t, err, model.KindInvalid)
}

func TestProductSetUnitKerjaMenggantiSeluruhHimpunan(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)
	unitB := createUnit(t, testApp, "Unit B Set")

	masukKatalog(t, f.product, unitB)

	setKatalog(t, testApp, f, f.product, unitB)
	if got := unitDiKatalog(t, testApp, f.product); !got[unitB] || len(got) != 1 {
		t.Fatalf("setelah PUT [B]: %v, want hanya B", got)
	}

	setKatalog(t, testApp, f, f.product)
	if got := unitDiKatalog(t, testApp, f.product); len(got) != 0 {
		t.Fatalf("setelah PUT []: %v, want kosong", got)
	}

	// Duplicates collapse instead of failing the active-unit count.
	setKatalog(t, testApp, f, f.product, f.unitKerja, f.unitKerja, unitB)
	if got := unitDiKatalog(t, testApp, f.product); len(got) != 2 {
		t.Fatalf("setelah PUT dengan duplikat: %v, want dua unit", got)
	}

	// No "leave alone" exists for the endpoint's only field: null/absent is rejected.
	if _, err := testApp.product.SetUnitKerja(ctx(), &model.SetProductUnitKerjaRequest{
		IDProduct: f.product, ActorID: f.actor, IDUnitKerja: nil,
	}); err == nil {
		t.Fatalf("id_unit_kerja nil harus ditolak")
	}

	_, err := testApp.product.SetUnitKerja(ctx(), &model.SetProductUnitKerjaRequest{
		IDProduct: f.product, ActorID: f.actor, IDUnitKerja: []int64{999999},
	})
	assertKind(t, err, model.KindInvalid)

	_, err = testApp.product.SetUnitKerja(ctx(), &model.SetProductUnitKerjaRequest{
		IDProduct: 999999, ActorID: f.actor, IDUnitKerja: []int64{unitB},
	})
	assertKind(t, err, model.KindNotFound)
}

// A product cannot leave a unit's catalog while goods of it remain in that unit's rooms —
// stock no catalog admits could not be sold, counted, or moved by anything.
func TestProductKeluarDariKatalogDitolakSelamaMasihAdaStok(t *testing.T) {
	testApp := newApp(t)
	testApp, s := stokAwalMutasi(t, testApp, "10")

	var unitTujuan int64
	if err := testDB.QueryRow(`SELECT id_unit_kerja FROM ruang WHERE id = $1`, s.tujuan).Scan(&unitTujuan); err != nil {
		t.Fatalf("unit tujuan: %v", err)
	}

	// 10 pcs sit in the fixture's room: taking the product out of that unit is refused,
	// and refusing must leave the catalog exactly as it was.
	_, err := testApp.product.SetUnitKerja(ctx(), &model.SetProductUnitKerjaRequest{
		IDProduct: s.product, ActorID: s.actor, IDUnitKerja: []int64{unitTujuan},
	})
	assertKind(t, err, model.KindConflict)

	if got := unitDiKatalog(t, testApp, s.product); !got[s.unitKerja] || !got[unitTujuan] {
		t.Fatalf("katalog berubah walau ditolak: %v", got)
	}

	// Move it all across — the remedy the message names — and the same request passes.
	mutasi := buatMutasi(t, testApp, s, "10")
	postingMutasi(t, testApp, s, mutasi.ID)

	setKatalog(t, testApp, s.fixture, s.product, unitTujuan)
	if got := unitDiKatalog(t, testApp, s.product); got[s.unitKerja] || !got[unitTujuan] {
		t.Fatalf("katalog = %v, want hanya unit tujuan", got)
	}

	// And the mirror: the destination now holds the stock and cannot be dropped.
	_, err = testApp.product.SetUnitKerja(ctx(), &model.SetProductUnitKerjaRequest{
		IDProduct: s.product, ActorID: s.actor, IDUnitKerja: []int64{},
	})
	assertKind(t, err, model.KindConflict)
}

// Every module moving goods into or out of a room holds its lines to that room's unit.
func TestDokumenMenolakProdukDiLuarKatalogUnitRuang(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)
	unitLain := createUnit(t, testApp, "Unit Lain Katalog Dokumen")

	// Carried by the other unit only; nothing has moved, so nothing blocks the removal.
	masukKatalog(t, f.product, unitLain)
	setKatalog(t, testApp, f, f.product, unitLain)

	harusDitolak := func(t *testing.T, err error) {
		t.Helper()

		assertKind(t, err, model.KindInvalid)
		if !strings.Contains(err.Error(), "BRG-001") {
			t.Fatalf("pesan harus menyebut kode barang BRG-001, got: %v", err)
		}
	}

	t.Run("pembelian", func(t *testing.T) {
		_, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
			ActorID: f.actor, Tanggal: "2026-08-11", IDSupplier: f.supplier, IDRuang: f.ruang,
			Detail: []model.PembelianDetailRequest{{
				IDProduct: f.product, IDSatuanInput: f.pcs, QtyFaktur: "5", HargaSatuanInput: "10000",
			}},
		})
		harusDitolak(t, err)
	})

	t.Run("penjualan", func(t *testing.T) {
		_, err := testApp.penjualan.Create(ctx(), &model.CreatePenjualanRequest{
			ActorID: f.actor, Tanggal: "2026-08-11", IDRuang: f.ruang, JenisPembayaran: "TUNAI",
			Detail: []model.PenjualanDetailRequest{{
				IDProduct: f.product, IDSatuanInput: f.pcs, QtyInput: "1", HargaSatuanInput: "15000",
			}},
		})
		harusDitolak(t, err)
	})

	t.Run("pemakaian", func(t *testing.T) {
		_, err := testApp.pemakaian.Create(ctx(), &model.CreatePemakaianRequest{
			ActorID: f.actor, Tanggal: "2026-08-11", IDRuang: f.ruang, IDPemohon: f.actor, Keperluan: "uji",
			Detail: []model.PemakaianDetailRequest{{
				IDProduct: f.product, IDSatuanInput: f.pcs, QtyInput: "1",
			}},
		})
		harusDitolak(t, err)
	})

	t.Run("mutasi sumber", func(t *testing.T) {
		tujuan, err := testApp.ruang.Create(ctx(), &model.CreateRuangRequest{
			ActorID: f.actor, NamaRuang: "Tujuan Katalog", IDUnitKerja: unitLain,
		})
		if err != nil {
			t.Fatalf("create ruang tujuan: %v", err)
		}

		_, err = testApp.mutasi.Create(ctx(), &model.CreateMutasiRequest{
			ActorID: f.actor, Tanggal: "2026-08-11", IDRuangAsal: f.ruang, IDRuangTujuan: tujuan.ID,
			Detail: []model.MutasiDetailRequest{{IDProduct: f.product, IDSatuanInput: f.pcs, QtyInput: "1"}},
		})
		harusDitolak(t, err)
	})

	// The destination is held to its catalog too: goods arriving in a unit that does not
	// carry the product would land as stock nothing could then move. Flip the setup so the
	// SOURCE carries the product and only the destination does not.
	t.Run("mutasi tujuan", func(t *testing.T) {
		setKatalog(t, testApp, f, f.product, f.unitKerja)

		tujuan, err := testApp.ruang.Create(ctx(), &model.CreateRuangRequest{
			ActorID: f.actor, NamaRuang: "Tujuan Tanpa Katalog", IDUnitKerja: unitLain,
		})
		if err != nil {
			t.Fatalf("create ruang tujuan: %v", err)
		}

		_, err = testApp.mutasi.Create(ctx(), &model.CreateMutasiRequest{
			ActorID: f.actor, Tanggal: "2026-08-11", IDRuangAsal: f.ruang, IDRuangTujuan: tujuan.ID,
			Detail: []model.MutasiDetailRequest{{IDProduct: f.product, IDSatuanInput: f.pcs, QtyInput: "1"}},
		})
		harusDitolak(t, err)
	})

	// stok_opname: only a hand-typed line can name a product the room has no history
	// with, so the check sits on ReplaceDetail.
	t.Run("stok opname", func(t *testing.T) {
		setKatalog(t, testApp, f, f.product, unitLain)

		opname, err := testApp.stokOpname.Create(ctx(), &model.CreateStokOpnameRequest{
			ActorID: f.actor, IDRuang: f.ruang,
		})
		if err != nil {
			t.Fatalf("create stok opname: %v", err)
		}

		_, err = testApp.stokOpname.ReplaceDetail(ctx(), &model.ReplaceStokOpnameDetailRequest{
			ID: opname.ID, ActorID: f.actor,
			Detail: []model.StokOpnameDetailRequest{{IDProduct: f.product}},
		})
		harusDitolak(t, err)
	})
}

// A product can leave the catalog between typing a draft and posting it, so Posting
// checks again — a draft passing the check at Create proves nothing about posting day.
func TestPostingMengulangPemeriksaanKatalog(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)
	unitLain := createUnit(t, testApp, "Unit Lain Katalog Posting")
	masukKatalog(t, f.product, unitLain)

	draft := draftSederhana(t, testApp, f, "10", nil, nil)

	if _, err := testApp.pembelian.Ajukan(ctx(), &model.AjukanPembelianRequest{
		ID: draft.ID, ActorID: f.actor,
	}); err != nil {
		t.Fatalf("ajukan: %v", err)
	}

	// Nothing is posted yet, so nothing stops the product leaving the fixture's unit.
	setKatalog(t, testApp, f, f.product, unitLain)

	_, err := testApp.pembelian.Posting(ctx(), &model.PostingPembelianRequest{ID: draft.ID, ActorID: f.actor})
	assertKind(t, err, model.KindInvalid)
	if err == nil || !strings.Contains(err.Error(), "BRG-001") {
		t.Fatalf("pesan harus menyebut BRG-001, got: %v", err)
	}
}

// GET /pos/product offers exactly what the nota would then accept: the room's unit's
// catalog, not the caller's active unit.
func TestPOSHanyaMenawarkanProdukKatalogUnitRuang(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	tidakDibawa, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID: f.actor, KodeBarang: "POS-TIDAK", Nama: "Tidak Dibawa", IDSatuanDasar: f.pcs,
	})
	if err != nil {
		t.Fatalf("create product: %v", err)
	}
	setKatalog(t, testApp, f, tidakDibawa.ID)

	list, paging, err := testApp.product.POS(ctx(), &model.ListPosProductRequest{IDRuang: f.ruang})
	if err != nil {
		t.Fatalf("pos: %v", err)
	}

	if paging.TotalItem != 1 || len(list) != 1 || list[0].ID != f.product {
		t.Fatalf("pos = %+v (total %d), want hanya produk yang dibawa unit ruang ini", list, paging.TotalItem)
	}
}

// The catalog check in a posting reads its membership rows FOR SHARE, and the removal
// DELETEs them first: that ordering is what stops a posting slipping in between the
// removal's stock check and its commit. This holds one such share open and proves the
// removal waits for it instead of racing past.
func TestSetUnitKerjaMenungguPostingYangSedangBerjalan(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)
	unitLain := createUnit(t, testApp, "Unit Lain Katalog Kunci")
	masukKatalog(t, f.product, unitLain)

	tx, err := testDB.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	// What periksaKatalogRuang does inside a posting's transaction.
	if _, err := tx.Exec(
		`SELECT 1 FROM product_unit_kerja WHERE id_product = $1 AND id_unit_kerja = $2 FOR SHARE`,
		f.product, f.unitKerja,
	); err != nil {
		t.Fatalf("share lock: %v", err)
	}

	selesai := make(chan error, 1)

	go func() {
		_, err := testApp.product.SetUnitKerja(ctx(), &model.SetProductUnitKerjaRequest{
			IDProduct: f.product, ActorID: f.actor, IDUnitKerja: []int64{unitLain},
		})
		selesai <- err
	}()

	select {
	case err := <-selesai:
		t.Fatalf("pencabutan katalog selesai (%v) padahal posting masih memegang katalognya", err)
	case <-time.After(400 * time.Millisecond):
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	select {
	case err := <-selesai:
		if err != nil {
			t.Fatalf("pencabutan setelah posting selesai: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("pencabutan tidak jalan setelah kunci dilepas")
	}
}
