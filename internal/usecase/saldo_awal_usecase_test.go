package usecase_test

// Saldo awal (isu #43) — the eighth document to write kartu_stok, and the only one whose
// cost is typed. What these prove lives in the database and in the order of locks: the
// fence (one (barang, ruang) for life) reads kartu_stok's own history, the trigger owns
// the running balance, and the unique index stops two lines for one product. A mock
// would agree with any of it.

import (
	"strings"
	"sync"
	"testing"

	"Arthafreestyle/ERP/internal/model"
)

type saldoAwalSetup struct {
	fixture
	productB int64
}

// saldoAwalFixture is the purchase fixture plus a second product, in a room with no
// history at all — the situation saldo_awal exists for.
func saldoAwalFixture(t *testing.T) (*app, saldoAwalSetup) {
	t.Helper()

	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	productB, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID: f.actor, KodeBarang: "BRG-002", Nama: "Tinta Printer", IDSatuanDasar: f.pcs,
	})
	if err != nil {
		t.Fatalf("create product B: %v", err)
	}

	return testApp, saldoAwalSetup{fixture: f, productB: productB.ID}
}

func barisSA(idProduct, idSatuan int64, qty, harga string) model.SaldoAwalDetailRequest {
	return model.SaldoAwalDetailRequest{
		IDProduct: idProduct, IDSatuanInput: idSatuan, QtyInput: qty, HargaSatuanInput: harga,
	}
}

func buatSaldoAwal(t *testing.T, testApp *app, s saldoAwalSetup, tanggal string, baris ...model.SaldoAwalDetailRequest) *model.SaldoAwalResponse {
	t.Helper()

	saldoAwal, err := testApp.saldoAwal.Create(ctx(), &model.CreateSaldoAwalRequest{
		ActorID: s.actor, Tanggal: tanggal, IDRuang: s.ruang,
		Alasan: "migrasi unit baru", Detail: baris,
	})
	if err != nil {
		t.Fatalf("create saldo_awal: %v", err)
	}

	return saldoAwal
}

func ajukanSaldoAwal(t *testing.T, testApp *app, s saldoAwalSetup, id int64) {
	t.Helper()

	if _, err := testApp.saldoAwal.Ajukan(ctx(), &model.AjukanSaldoAwalRequest{ID: id, ActorID: s.actor}); err != nil {
		t.Fatalf("ajukan saldo_awal: %v", err)
	}
}

func postingSaldoAwalRaw(testApp *app, s saldoAwalSetup, id int64) (*model.SaldoAwalResponse, error) {
	return testApp.saldoAwal.Posting(ctx(), &model.PostingSaldoAwalRequest{ID: id, ActorID: s.actor})
}

// ajukanDanPostingSaldoAwal takes a draft all the way to POSTED.
func ajukanDanPostingSaldoAwal(t *testing.T, testApp *app, s saldoAwalSetup, id int64) *model.SaldoAwalResponse {
	t.Helper()

	ajukanSaldoAwal(t, testApp, s, id)

	posted, err := postingSaldoAwalRaw(testApp, s, id)
	if err != nil {
		t.Fatalf("posting saldo_awal: %v", err)
	}

	return posted
}

func batalSaldoAwal(testApp *app, s saldoAwalSetup, id int64) (*model.SaldoAwalResponse, error) {
	return testApp.saldoAwal.Batal(ctx(), &model.BatalSaldoAwalRequest{
		ID: id, ActorID: s.actor, AlasanBatal: "salah ketik",
	})
}

func jumlahKartuStokRef(t *testing.T, refTable string, refID int64) int {
	t.Helper()

	var n int
	if err := testDB.QueryRow(
		`SELECT COUNT(*) FROM kartu_stok WHERE ref_table = $1 AND ref_id_transaksi = $2`, refTable, refID,
	).Scan(&n); err != nil {
		t.Fatalf("count kartu_stok: %v", err)
	}

	return n
}

// One incoming row per line, valued at exactly qty_input × harga_satuan_input as typed.
//
// The DUS line is the one that matters: 3 DUS at 100 is 300.00 of value entering stock,
// while 36 base units at the derived 8.3333 each would be 299.99. The document must
// carry the typed figure, not the quotient's.
func TestSaldoAwalPostingMenulisSatuBarisMasukPerBarisDenganNilaiKetikan(t *testing.T) {
	testApp, s := saldoAwalFixture(t)

	draft := buatSaldoAwal(t, testApp, s, "2026-08-01",
		barisSA(s.product, s.dus, "3", "100"),    // 3 DUS × 12 = 36 pcs, nilai 300.00
		barisSA(s.productB, s.pcs, "5", "10.50"), // 5 pcs, nilai 52.50
	)

	if draft.Status != "DRAFT" || draft.TotalNilai != nil {
		t.Fatalf("draft = %s / total %v, want DRAFT and no total yet", draft.Status, draft.TotalNilai)
	}
	if !strings.HasPrefix(draft.Nomor, "SA/FIX/") {
		t.Errorf("nomor = %q, want SA/<kode unit>/...", draft.Nomor)
	}
	if got := draft.Detail[0].HargaPokokSatuanDasar; got != "8.3333" {
		t.Errorf("harga_pokok_satuan_dasar = %s, want 8.3333 (derived, rounded once)", got)
	}

	posted := ajukanDanPostingSaldoAwal(t, testApp, s, draft.ID)

	if posted.Status != "POSTED" || posted.TotalNilai == nil || *posted.TotalNilai != "352.50" {
		t.Fatalf("posted = %s / total %v, want POSTED and 352.50", posted.Status, posted.TotalNilai)
	}

	if n := jumlahKartuStokRef(t, "saldo_awal", draft.ID); n != 2 {
		t.Fatalf("kartu_stok rows = %d, want one per line (2)", n)
	}

	rows, err := testDB.Query(`
		SELECT id_barang, jenis_transaksi::TEXT, stok_masuk, stok_keluar, nilai_masuk::TEXT, id_ruang
		FROM kartu_stok WHERE ref_table = 'saldo_awal' AND ref_id_transaksi = $1 ORDER BY id`, draft.ID)
	if err != nil {
		t.Fatalf("select kartu_stok: %v", err)
	}
	defer rows.Close()

	want := []struct {
		barang int64
		masuk  int64
		nilai  string
	}{{s.product, 36, "300.00"}, {s.productB, 5, "52.50"}}

	for i := range want {
		if !rows.Next() {
			t.Fatalf("missing kartu_stok row %d", i)
		}

		var barang, masuk, keluar, ruang int64
		var jenis, nilai string
		if err := rows.Scan(&barang, &jenis, &masuk, &keluar, &nilai, &ruang); err != nil {
			t.Fatalf("scan: %v", err)
		}

		if barang != want[i].barang || jenis != "SALDO_AWAL" || masuk != want[i].masuk ||
			keluar != 0 || nilai != want[i].nilai || ruang != s.ruang {
			t.Errorf("row %d = (%d %s in=%d out=%d %s room %d), want (%d SALDO_AWAL in=%d out=0 %s room %d)",
				i, barang, jenis, masuk, keluar, nilai, ruang,
				want[i].barang, want[i].masuk, want[i].nilai, s.ruang)
		}
	}

	stok, nilai := saldoStok(t, s.product, s.ruang)
	if stok != 36 || nilai != "300.00" {
		t.Errorf("saldo = %d / %s, want 36 / 300.00", stok, nilai)
	}

	for i := range posted.Detail {
		if posted.Detail[i].IDKartuStok == nil {
			t.Errorf("detail %d has no id_kartu_stok after posting", i)
		}
	}
}

// The fence, part one: any existing history refuses the pair — from a purchase, from a
// transfer, and from an earlier saldo_awal. The refusal is 409 and names the product,
// the room, and stok_opname as the way forward.
func TestSaldoAwalDitolakBilaBarangSudahPunyaRiwayatDariPembelian(t *testing.T) {
	testApp, s := saldoAwalFixture(t)

	draft := draftSederhana(t, testApp, s.fixture, "10", nil, nil)
	ajukanDanPosting(t, testApp, s.fixture, draft.ID)

	_, err := testApp.saldoAwal.Create(ctx(), &model.CreateSaldoAwalRequest{
		ActorID: s.actor, Tanggal: "2026-08-01", IDRuang: s.ruang, Alasan: "migrasi",
		Detail: []model.SaldoAwalDetailRequest{barisSA(s.product, s.pcs, "5", "10")},
	})

	assertKind(t, err, model.KindConflict)
	assertPesanMemuat(t, err, "BRG-001")
	assertPesanMemuat(t, err, "Gudang Utama")
	assertPesanMemuat(t, err, "stok_opname")
}

func TestSaldoAwalDitolakBilaBarangSudahPunyaRiwayatDariMutasi(t *testing.T) {
	testApp, m := stokAwalMutasi(t, newApp(t), "10")

	postingMutasi(t, testApp, m, buatMutasi(t, testApp, m, "4").ID) // goods now have history in tujuan

	s := saldoAwalSetup{fixture: m.fixture}

	_, err := testApp.saldoAwal.Create(ctx(), &model.CreateSaldoAwalRequest{
		ActorID: s.actor, Tanggal: "2026-08-01", IDRuang: m.tujuan, Alasan: "migrasi",
		Detail: []model.SaldoAwalDetailRequest{barisSA(s.product, s.pcs, "5", "10")},
	})

	assertKind(t, err, model.KindConflict)
	assertPesanMemuat(t, err, "Toko Depan")
}

func TestSaldoAwalDitolakBilaBarangSudahPunyaRiwayatDariSaldoAwalSebelumnya(t *testing.T) {
	testApp, s := saldoAwalFixture(t)

	pertama := buatSaldoAwal(t, testApp, s, "2026-08-01", barisSA(s.product, s.pcs, "5", "10"))
	ajukanDanPostingSaldoAwal(t, testApp, s, pertama.ID)

	_, err := testApp.saldoAwal.Create(ctx(), &model.CreateSaldoAwalRequest{
		ActorID: s.actor, Tanggal: "2026-08-02", IDRuang: s.ruang, Alasan: "lagi",
		Detail: []model.SaldoAwalDetailRequest{barisSA(s.product, s.pcs, "1", "10")},
	})

	assertKind(t, err, model.KindConflict)

	// A different product in the same room is untouched by the fence.
	kedua := buatSaldoAwal(t, testApp, s, "2026-08-02", barisSA(s.productB, s.pcs, "1", "10"))
	ajukanDanPostingSaldoAwal(t, testApp, s, kedua.ID)
}

// The fence, part two — the strict half: cancelling appends a reversing row, so the pair
// now HAS history and can never be given an opening balance again. The reversal is a
// row like any other; that is the fence working, not a side effect to repair.
func TestSaldoAwalSetelahDibatalkanPasanganTetapDitolak(t *testing.T) {
	testApp, s := saldoAwalFixture(t)

	pertama := buatSaldoAwal(t, testApp, s, "2026-08-01", barisSA(s.product, s.pcs, "5", "10"))
	ajukanDanPostingSaldoAwal(t, testApp, s, pertama.ID)

	batal, err := batalSaldoAwal(testApp, s, pertama.ID)
	if err != nil {
		t.Fatalf("batal saldo_awal: %v", err)
	}
	if batal.Status != "BATAL" {
		t.Fatalf("status = %s, want BATAL", batal.Status)
	}

	if stok, _ := saldoStok(t, s.product, s.ruang); stok != 0 {
		t.Errorf("stok = %d setelah pembatalan, want 0 (quantity always balances)", stok)
	}
	if n := jumlahKartuStokRef(t, "saldo_awal", pertama.ID); n != 2 {
		t.Errorf("kartu_stok rows = %d, want posting + reversal (2)", n)
	}

	var jenis string
	var asal *int64
	if err := testDB.QueryRow(`
		SELECT jenis_transaksi::TEXT, id_kartu_stok_asal FROM kartu_stok
		WHERE ref_table = 'saldo_awal' AND ref_id_transaksi = $1 ORDER BY id DESC LIMIT 1`, pertama.ID,
	).Scan(&jenis, &asal); err != nil {
		t.Fatalf("read reversal: %v", err)
	}
	if jenis != "PEMBATALAN_TRANSAKSI" || asal == nil {
		t.Errorf("reversal = %s / asal %v, want PEMBATALAN_TRANSAKSI pointing at the original", jenis, asal)
	}

	_, err = testApp.saldoAwal.Create(ctx(), &model.CreateSaldoAwalRequest{
		ActorID: s.actor, Tanggal: "2026-08-02", IDRuang: s.ruang, Alasan: "ulang",
		Detail: []model.SaldoAwalDetailRequest{barisSA(s.product, s.pcs, "5", "10")},
	})
	assertKind(t, err, model.KindConflict)
}

// Two lines for one product are refused with the lines named; the unique index is the
// backstop for anything that reaches the table another way.
func TestSaldoAwalDuaBarisProdukSamaDitolak(t *testing.T) {
	testApp, s := saldoAwalFixture(t)

	_, err := testApp.saldoAwal.Create(ctx(), &model.CreateSaldoAwalRequest{
		ActorID: s.actor, Tanggal: "2026-08-01", IDRuang: s.ruang, Alasan: "migrasi",
		Detail: []model.SaldoAwalDetailRequest{
			barisSA(s.product, s.pcs, "5", "10"),
			barisSA(s.product, s.dus, "1", "100"),
		},
	})
	assertKind(t, err, model.KindInvalid)
	assertPesanMemuat(t, err, "baris 2")

	draft := buatSaldoAwal(t, testApp, s, "2026-08-01", barisSA(s.product, s.pcs, "5", "10"))

	_, dbErr := testDB.Exec(`
		INSERT INTO saldo_awal_detail (id_saldo_awal, id_product, qty_input, id_satuan_input,
			faktor_konversi, qty_dasar, harga_satuan_input, harga_pokok_satuan_dasar, nilai_masuk)
		VALUES ($1, $2, 1, $3, 1, 1, 10, 10, 10)`, draft.ID, s.product, s.pcs)
	if dbErr == nil {
		t.Fatal("saldo_awal_detail_baris_uidx let a second line for the same product in")
	}
}

// Two drafts may both claim the same pair — a draft is not a posting — and the fence at
// Posting is what decides: the first posts, the second is refused there, not at draft.
func TestSaldoAwalDuaDraftMengklaimPasanganSama(t *testing.T) {
	testApp, s := saldoAwalFixture(t)

	pertama := buatSaldoAwal(t, testApp, s, "2026-08-01", barisSA(s.product, s.pcs, "5", "10"))
	kedua := buatSaldoAwal(t, testApp, s, "2026-08-01", barisSA(s.product, s.pcs, "7", "12"))

	ajukanSaldoAwal(t, testApp, s, pertama.ID)
	ajukanSaldoAwal(t, testApp, s, kedua.ID)

	if _, err := postingSaldoAwalRaw(testApp, s, pertama.ID); err != nil {
		t.Fatalf("posting pertama: %v", err)
	}

	_, err := postingSaldoAwalRaw(testApp, s, kedua.ID)
	assertKind(t, err, model.KindConflict)

	if stok, nilai := saldoStok(t, s.product, s.ruang); stok != 5 || nilai != "50.00" {
		t.Errorf("saldo = %d / %s, want only the first document's 5 / 50.00", stok, nilai)
	}
}

// The same race, actually concurrent: the fence is read only after the pair's balance
// lock is held, so exactly one of two simultaneous postings can win. Read before the
// lock, both would see "no history" and both would write.
func TestSaldoAwalDuaPostingBersamaanHanyaSatuYangLolos(t *testing.T) {
	testApp, s := saldoAwalFixture(t)

	a := buatSaldoAwal(t, testApp, s, "2026-08-01", barisSA(s.product, s.pcs, "5", "10"))
	b := buatSaldoAwal(t, testApp, s, "2026-08-01", barisSA(s.product, s.pcs, "7", "12"))
	ajukanSaldoAwal(t, testApp, s, a.ID)
	ajukanSaldoAwal(t, testApp, s, b.ID)

	var wg sync.WaitGroup
	errs := make([]error, 2)

	for i, id := range []int64{a.ID, b.ID} {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_, errs[i] = postingSaldoAwalRaw(testApp, s, id)
		}()
	}

	wg.Wait()

	menang := 0
	for _, err := range errs {
		if err == nil {
			menang++
		}
	}
	if menang != 1 {
		t.Fatalf("%d of 2 concurrent postings succeeded (errors: %v), want exactly 1", menang, errs)
	}

	var n int
	if err := testDB.QueryRow(
		`SELECT COUNT(*) FROM kartu_stok WHERE id_barang = $1 AND id_ruang = $2`, s.product, s.ruang,
	).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("kartu_stok rows for the pair = %d, want 1", n)
	}
}

// Dated the document's tanggal, so posting into a closed month is refused and the
// refusal names the month.
func TestSaldoAwalPostingKePeriodeTutupDitolak(t *testing.T) {
	testApp, s := saldoAwalFixture(t)

	lalu := awalBulanLalu(t)

	draft := buatSaldoAwal(t, testApp, s, lalu.Format("2006-01-02"), barisSA(s.product, s.pcs, "5", "10"))
	ajukanSaldoAwal(t, testApp, s, draft.ID)
	tutupPeriode(t, testApp, s.actor, lalu.Year(), int(lalu.Month()))

	_, err := postingSaldoAwalRaw(testApp, s, draft.ID)
	assertKind(t, err, model.KindInvalid)
	assertPesanMemuat(t, err, lalu.Format("2006-01"))

	if n := jumlahKartuStokRef(t, "saldo_awal", draft.ID); n != 0 {
		t.Errorf("kartu_stok rows = %d after a refused posting, want 0", n)
	}
}

// A room frozen by an open stok_opname refuses both directions of this module, and the
// refusal is a 409: the request is fine, the room is not ready.
func TestSaldoAwalPostingDanBatalDitolakBilaRuangDibekukan(t *testing.T) {
	testApp, s := saldoAwalFixture(t)

	draft := buatSaldoAwal(t, testApp, s, "2026-08-01", barisSA(s.product, s.pcs, "5", "10"))
	ajukanSaldoAwal(t, testApp, s, draft.ID)

	opname := buatOpname(t, testApp, s.fixture)

	_, err := postingSaldoAwalRaw(testApp, s, draft.ID)
	assertKind(t, err, model.KindConflict)
	assertPesanMemuat(t, err, opname.Nomor)

	// Free the room, post, freeze it again: the cancellation is refused the same way.
	if _, err := testApp.stokOpname.Batal(ctx(), &model.BatalStokOpnameRequest{
		ID: opname.ID, ActorID: s.actor, AlasanBatal: "belum jadi",
	}); err != nil {
		t.Fatalf("batal opname: %v", err)
	}
	if _, err := postingSaldoAwalRaw(testApp, s, draft.ID); err != nil {
		t.Fatalf("posting setelah ruang bebas: %v", err)
	}

	buatOpname(t, testApp, s.fixture)

	_, err = batalSaldoAwal(testApp, s, draft.ID)
	assertKind(t, err, model.KindConflict)

	if stok, _ := saldoStok(t, s.product, s.ruang); stok != 5 {
		t.Errorf("stok = %d, want 5 (cancellation into a frozen room writes nothing)", stok)
	}
}

// The per-unit catalog is enforced where the lines are typed and AGAIN at posting — a
// product can leave a catalog between the two.
func TestSaldoAwalMenolakProdukDiLuarKatalogUnitRuang(t *testing.T) {
	testApp, s := saldoAwalFixture(t)

	unitLain := createUnit(t, testApp, "Unit Tanpa Katalog")
	ruangLain, err := testApp.ruang.Create(ctx(), &model.CreateRuangRequest{
		ActorID: s.actor, NamaRuang: "Gudang Tanpa Katalog", IDUnitKerja: unitLain,
	})
	if err != nil {
		t.Fatalf("create ruang lain: %v", err)
	}

	// Create.
	_, err = testApp.saldoAwal.Create(ctx(), &model.CreateSaldoAwalRequest{
		ActorID: s.actor, Tanggal: "2026-08-01", IDRuang: ruangLain.ID, Alasan: "migrasi",
		Detail: []model.SaldoAwalDetailRequest{barisSA(s.product, s.pcs, "5", "10")},
	})
	assertKind(t, err, model.KindInvalid)
	assertPesanMemuat(t, err, "katalog")

	// ReplaceDetail.
	kosong, err := testApp.saldoAwal.Create(ctx(), &model.CreateSaldoAwalRequest{
		ActorID: s.actor, Tanggal: "2026-08-01", IDRuang: ruangLain.ID, Alasan: "migrasi",
	})
	if err != nil {
		t.Fatalf("create draft kosong: %v", err)
	}
	_, err = testApp.saldoAwal.ReplaceDetail(ctx(), &model.ReplaceSaldoAwalDetailRequest{
		ID: kosong.ID, ActorID: s.actor,
		Detail: []model.SaldoAwalDetailRequest{barisSA(s.product, s.pcs, "5", "10")},
	})
	assertKind(t, err, model.KindInvalid)

	// Posting: admitted while typing, removed from the catalog before posting.
	masukKatalog(t, s.product, unitLain)
	ruangDraft, err := testApp.saldoAwal.Create(ctx(), &model.CreateSaldoAwalRequest{
		ActorID: s.actor, Tanggal: "2026-08-01", IDRuang: ruangLain.ID, Alasan: "migrasi",
		Detail: []model.SaldoAwalDetailRequest{barisSA(s.product, s.pcs, "5", "10")},
	})
	if err != nil {
		t.Fatalf("create draft dalam katalog: %v", err)
	}
	ajukanSaldoAwal(t, testApp, s, ruangDraft.ID)

	if _, err := testDB.Exec(
		`DELETE FROM product_unit_kerja WHERE id_product = $1 AND id_unit_kerja = $2`, s.product, unitLain,
	); err != nil {
		t.Fatalf("keluarkan dari katalog: %v", err)
	}

	_, err = postingSaldoAwalRaw(testApp, s, ruangDraft.ID)
	assertKind(t, err, model.KindInvalid)
	assertPesanMemuat(t, err, "katalog")
}

// The one document whose value is typed refuses a typed nothing: zero price, zero
// quantity, more precision than the column keeps, a blank reason.
func TestSaldoAwalMenolakHargaNolQtyNolDanAlasanKosong(t *testing.T) {
	testApp, s := saldoAwalFixture(t)

	kasus := map[string]model.SaldoAwalDetailRequest{
		"harga nol":        barisSA(s.product, s.pcs, "5", "0"),
		"qty nol":          barisSA(s.product, s.pcs, "0", "10"),
		"harga negatif":    barisSA(s.product, s.pcs, "5", "-10"),
		"harga 3 desimal":  barisSA(s.product, s.pcs, "5", "10.005"),
		"nilai membulat 0": barisSA(s.product, s.pcs, "0.0001", "0.01"),
		"pecahan dasar":    barisSA(s.product, s.dus, "0.3", "100"), // 0.3 DUS = 3.6 pcs
	}

	for nama, baris := range kasus {
		_, err := testApp.saldoAwal.Create(ctx(), &model.CreateSaldoAwalRequest{
			ActorID: s.actor, Tanggal: "2026-08-01", IDRuang: s.ruang, Alasan: "migrasi",
			Detail: []model.SaldoAwalDetailRequest{baris},
		})
		if err == nil {
			t.Errorf("%s: accepted, want refused", nama)

			continue
		}
		assertKind(t, err, model.KindInvalid)
	}

	for nama, alasan := range map[string]string{"kosong": "", "spasi": "   "} {
		_, err := testApp.saldoAwal.Create(ctx(), &model.CreateSaldoAwalRequest{
			ActorID: s.actor, Tanggal: "2026-08-01", IDRuang: s.ruang, Alasan: alasan,
		})
		if err == nil {
			t.Errorf("alasan %s: accepted, want refused", nama)
		}
	}
}

// alasan may be changed but never cleared: it is the only record of why inventory value
// was created without a document behind it.
func TestSaldoAwalPatchTidakBisaMengosongkanAlasan(t *testing.T) {
	testApp, s := saldoAwalFixture(t)

	draft := buatSaldoAwal(t, testApp, s, "2026-08-01", barisSA(s.product, s.pcs, "5", "10"))

	_, err := testApp.saldoAwal.Update(ctx(), &model.UpdateSaldoAwalRequest{
		ID: draft.ID, ActorID: s.actor, Alasan: model.Optional[string]{Present: true, Value: nil},
	})
	assertKind(t, err, model.KindInvalid)

	_, err = testApp.saldoAwal.Update(ctx(), &model.UpdateSaldoAwalRequest{
		ID: draft.ID, ActorID: s.actor, Alasan: model.Optional[string]{Present: true, Value: ptr("  ")},
	})
	assertKind(t, err, model.KindInvalid)

	diubah, err := testApp.saldoAwal.Update(ctx(), &model.UpdateSaldoAwalRequest{
		ID: draft.ID, ActorID: s.actor, Alasan: model.Optional[string]{Present: true, Value: ptr("berita acara 12")},
	})
	if err != nil {
		t.Fatalf("update alasan: %v", err)
	}
	if diubah.Alasan != "berita acara 12" {
		t.Errorf("alasan = %q, want the patched value", diubah.Alasan)
	}
}

// Cancelling balances quantity — and is refused outright when the goods have since left
// the room, with the remedy named. The document stays POSTED and nothing is written.
func TestSaldoAwalBatalDitolakBilaBarangSudahKeluarDariRuang(t *testing.T) {
	testApp, s := saldoAwalFixture(t)

	draft := buatSaldoAwal(t, testApp, s, "2026-08-01", barisSA(s.product, s.pcs, "10", "10"))
	ajukanDanPostingSaldoAwal(t, testApp, s, draft.ID)

	pemohon := buatUserPolos(t, testApp, "pemohon_sa")
	penyetuju := buatUserPolos(t, testApp, "penyetuju_sa")

	pakai, err := testApp.pemakaian.Create(ctx(), &model.CreatePemakaianRequest{
		ActorID: s.actor, Tanggal: "2026-08-15", IDRuang: s.ruang, IDPemohon: pemohon, Keperluan: "dipakai",
		Detail: []model.PemakaianDetailRequest{{IDProduct: s.product, IDSatuanInput: s.pcs, QtyInput: "3"}},
	})
	if err != nil {
		t.Fatalf("create pemakaian: %v", err)
	}
	if _, err := testApp.pemakaian.Ajukan(ctx(), &model.AjukanPemakaianRequest{ID: pakai.ID, ActorID: s.actor}); err != nil {
		t.Fatalf("ajukan pemakaian: %v", err)
	}
	if _, err := testApp.pemakaian.Setujui(ctx(), &model.SetujuiPemakaianRequest{ID: pakai.ID, ActorID: penyetuju}); err != nil {
		t.Fatalf("setujui pemakaian: %v", err)
	}
	if _, err := testApp.pemakaian.Posting(ctx(), &model.PostingPemakaianRequest{ID: pakai.ID, ActorID: s.actor}); err != nil {
		t.Fatalf("posting pemakaian: %v", err)
	}

	_, err = batalSaldoAwal(testApp, s, draft.ID)
	assertKind(t, err, model.KindInvalid)
	assertPesanMemuat(t, err, "BRG-001")

	if stok, _ := saldoStok(t, s.product, s.ruang); stok != 7 {
		t.Errorf("stok = %d, want 7 (a refused cancellation writes nothing)", stok)
	}

	got, err := testApp.saldoAwal.Get(ctx(), &model.GetSaldoAwalRequest{ID: draft.ID})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != "POSTED" {
		t.Errorf("status = %s, want POSTED", got.Status)
	}
}

// From DRAFT and DIAJUKAN a cancellation is a pure status change: no kartu_stok row was
// ever written, so the pair stays open for a corrected document.
func TestSaldoAwalBatalDariDraftDanDiajukanTidakMenulisKartuStok(t *testing.T) {
	testApp, s := saldoAwalFixture(t)

	draft := buatSaldoAwal(t, testApp, s, "2026-08-01", barisSA(s.product, s.pcs, "5", "10"))
	if _, err := batalSaldoAwal(testApp, s, draft.ID); err != nil {
		t.Fatalf("batal dari DRAFT: %v", err)
	}

	diajukan := buatSaldoAwal(t, testApp, s, "2026-08-01", barisSA(s.product, s.pcs, "5", "10"))
	ajukanSaldoAwal(t, testApp, s, diajukan.ID)
	batal, err := batalSaldoAwal(testApp, s, diajukan.ID)
	if err != nil {
		t.Fatalf("batal dari DIAJUKAN: %v", err)
	}
	if batal.Status != "BATAL" || batal.AlasanBatal == nil {
		t.Errorf("batal = %s / %v, want BATAL with a reason", batal.Status, batal.AlasanBatal)
	}

	var n int
	if err := testDB.QueryRow(`SELECT COUNT(*) FROM kartu_stok`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("kartu_stok rows = %d, want 0", n)
	}

	// The pair is still open.
	ulang := buatSaldoAwal(t, testApp, s, "2026-08-01", barisSA(s.product, s.pcs, "5", "10"))
	ajukanDanPostingSaldoAwal(t, testApp, s, ulang.ID)

	_, err = batalSaldoAwal(testApp, s, draft.ID)
	assertKind(t, err, model.KindConflict) // already BATAL
}

// The state machine: no edit past DRAFT, no posting straight from DRAFT, no submission
// of an empty document, and a rejection returns the document to DRAFT with its reason.
func TestSaldoAwalAlurStatus(t *testing.T) {
	testApp, s := saldoAwalFixture(t)

	kosong := buatSaldoAwal(t, testApp, s, "2026-08-01")
	_, err := testApp.saldoAwal.Ajukan(ctx(), &model.AjukanSaldoAwalRequest{ID: kosong.ID, ActorID: s.actor})
	assertKind(t, err, model.KindInvalid)

	draft := buatSaldoAwal(t, testApp, s, "2026-08-01", barisSA(s.product, s.pcs, "5", "10"))

	_, err = postingSaldoAwalRaw(testApp, s, draft.ID)
	assertKind(t, err, model.KindConflict)

	ajukanSaldoAwal(t, testApp, s, draft.ID)

	_, err = testApp.saldoAwal.ReplaceDetail(ctx(), &model.ReplaceSaldoAwalDetailRequest{
		ID: draft.ID, ActorID: s.actor,
		Detail: []model.SaldoAwalDetailRequest{barisSA(s.product, s.pcs, "9", "10")},
	})
	assertKind(t, err, model.KindConflict)

	ditolak, err := testApp.saldoAwal.Tolak(ctx(), &model.TolakSaldoAwalRequest{
		ID: draft.ID, ActorID: s.actor, Alasan: "hitung ulang rak 3",
	})
	if err != nil {
		t.Fatalf("tolak: %v", err)
	}
	if ditolak.Status != "DRAFT" || ditolak.AlasanTolak == nil || *ditolak.AlasanTolak != "hitung ulang rak 3" {
		t.Fatalf("ditolak = %s / %v, want DRAFT carrying the reason", ditolak.Status, ditolak.AlasanTolak)
	}
	if ditolak.DiajukanOleh != nil {
		t.Error("diajukan_oleh survived a rejection")
	}

	diganti, err := testApp.saldoAwal.ReplaceDetail(ctx(), &model.ReplaceSaldoAwalDetailRequest{
		ID: draft.ID, ActorID: s.actor,
		Detail: []model.SaldoAwalDetailRequest{barisSA(s.product, s.pcs, "9", "10")},
	})
	if err != nil {
		t.Fatalf("replace detail setelah ditolak: %v", err)
	}
	if len(diganti.Detail) != 1 || diganti.Detail[0].QtyDasar != 9 {
		t.Fatalf("detail = %+v, want the one replaced line of 9", diganti.Detail)
	}

	ajukanDanPostingSaldoAwal(t, testApp, s, draft.ID)

	_, err = postingSaldoAwalRaw(testApp, s, draft.ID)
	assertKind(t, err, model.KindConflict) // already POSTED
}
