package usecase_test

// What these prove lives in the database, not in Go: the moving average that values an
// outgoing row, the negative-stock guard, the append-only trigger, the period lock, and
// the advisory lock per (barang, ruang) that two opposite transfers of the same goods
// would otherwise deadlock on. A mock would agree with any of it.
//
// The first test is the one isu #7 called the most important: moving goods between rooms
// must not change what the inventory is worth.

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"Arthafreestyle/ERP/internal/model"
)

// mutasiFixture adds a second room to the purchase fixture and stocks the first one, so
// there is something to move and somewhere to move it to.
type mutasiSetup struct {
	fixture
	tujuan int64
}

// stokAwalMutasi posts a purchase of qty pcs at 10.000 each into f.ruang, then returns a
// setup with a destination room ready.
func stokAwalMutasi(t *testing.T, testApp *app, qty string) (*app, mutasiSetup) {
	t.Helper()

	f := pembelianFixture(t, testApp)

	unitTujuan, err := testApp.unitKerja.Create(ctx(), &model.CreateUnitKerjaRequest{Nama: "Unit Toko Depan"})
	if err != nil {
		t.Fatalf("create unit kerja tujuan: %v", err)
	}

	// Cross-unit transfers are allowed (isu #12 fase 1), so tujuan deliberately
	// gets its own unit_kerja rather than reusing the fixture's.
	tujuan, err := testApp.ruang.Create(ctx(), &model.CreateRuangRequest{
		NamaRuang:   "Toko Depan",
		IDUnitKerja: unitTujuan.ID,
	})
	if err != nil {
		t.Fatalf("create ruang tujuan: %v", err)
	}

	draft := draftSederhana(t, testApp, f, qty, nil, nil)
	ajukanDanPosting(t, testApp, f, draft.ID)

	return testApp, mutasiSetup{fixture: f, tujuan: tujuan.ID}
}

// buatMutasi opens a DRAFT transfer of one product from asal to tujuan.
func buatMutasi(t *testing.T, testApp *app, s mutasiSetup, qty string) *model.MutasiResponse {
	t.Helper()

	mutasi, err := testApp.mutasi.Create(ctx(), &model.CreateMutasiRequest{
		ActorID:       s.actor,
		Tanggal:       "2026-08-15",
		IDRuangAsal:   s.ruang,
		IDRuangTujuan: s.tujuan,
		Keterangan:    "pindah ke toko",
		Detail: []model.MutasiDetailRequest{{
			IDProduct:     s.product,
			IDSatuanInput: s.pcs,
			QtyInput:      qty,
		}},
	})
	if err != nil {
		t.Fatalf("create mutasi: %v", err)
	}

	return mutasi
}

func postingMutasi(t *testing.T, testApp *app, s mutasiSetup, id int64) *model.MutasiResponse {
	t.Helper()

	posted, err := testApp.mutasi.Posting(ctx(), &model.PostingMutasiRequest{
		ID: id, ActorID: s.actor,
	})
	if err != nil {
		t.Fatalf("posting mutasi: %v", err)
	}

	return posted
}

// totalNilaiPersediaan sums the tail of every balance chain for one product, across all
// rooms. This is the figure a transfer must not move.
func totalNilaiPersediaan(t *testing.T, productID int64) string {
	t.Helper()

	var total string

	err := testDB.QueryRow(`
		SELECT COALESCE(SUM(nilai_akhir), 0)::NUMERIC(20, 2)::TEXT
		FROM (
			SELECT DISTINCT ON (id_ruang) nilai_akhir
			FROM kartu_stok
			WHERE id_barang = $1
			ORDER BY id_ruang, id DESC
		) saldo
	`, productID).Scan(&total)
	if err != nil {
		t.Fatalf("total nilai persediaan: %v", err)
	}

	return total
}

// The most important test in isu #7.
//
// Goods change room; the total value of inventory does not move by a cent. That only
// holds because the incoming row is valued at exactly what the ledger recorded leaving —
// nilai_masuk (tujuan) = nilai_keluar (asal), read back from the outgoing row's
// RETURNING rather than recomputed in Go. Anything computed before the insert could be
// stale, and the trigger overwrites an application-supplied cost anyway.
func TestMutasiTidakMengubahNilaiPersediaan(t *testing.T) {
	testApp, s := stokAwalMutasi(t, newApp(t), "100")

	sebelum := totalNilaiPersediaan(t, s.product)
	if sebelum != "1000000.00" {
		t.Fatalf("nilai persediaan sebelum mutasi = %q, want 1000000.00", sebelum)
	}

	mutasi := buatMutasi(t, testApp, s, "30")
	posted := postingMutasi(t, testApp, s, mutasi.ID)

	if posted.Status != "POSTED" {
		t.Fatalf("status = %q, want POSTED", posted.Status)
	}

	if sesudah := totalNilaiPersediaan(t, s.product); sesudah != sebelum {
		t.Errorf("nilai persediaan = %q setelah mutasi, want tetap %q", sesudah, sebelum)
	}

	stok, nilai := saldoStok(t, s.product, s.ruang)
	if stok != 70 || nilai != "700000.00" {
		t.Errorf("ruang asal = (%d, %s), want (70, 700000.00)", stok, nilai)
	}

	stok, nilai = saldoStok(t, s.product, s.tujuan)
	if stok != 30 || nilai != "300000.00" {
		t.Errorf("ruang tujuan = (%d, %s), want (30, 300000.00)", stok, nilai)
	}

	// One line, two ledger rows — that is the whole shape of the module.
	var keluar, masuk int
	if err := testDB.QueryRow(`
		SELECT
			COUNT(*) FILTER (WHERE jenis_transaksi = 'MUTASI_KELUAR'),
			COUNT(*) FILTER (WHERE jenis_transaksi = 'MUTASI_MASUK')
		FROM kartu_stok WHERE ref_table = 'mutasi' AND ref_id_transaksi = $1
	`, mutasi.ID).Scan(&keluar, &masuk); err != nil {
		t.Fatalf("hitung baris kartu stok: %v", err)
	}

	if keluar != 1 || masuk != 1 {
		t.Errorf("baris kartu stok = %d keluar / %d masuk, want 1 dan 1", keluar, masuk)
	}
}

// The same invariant where it is actually at risk: a moving average that does not divide
// evenly. Two purchases at different prices leave a cost with four decimals, and the
// value leaving one room is rounded to two before it is booked into the other. If the
// incoming row were valued any other way — recomputed from the average, say — the two
// roundings would disagree and inventory would gain or lose a cent per transfer.
func TestMutasiKekalMeskiHargaPokokTidakBulat(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	unitTujuan, err := testApp.unitKerja.Create(ctx(), &model.CreateUnitKerjaRequest{Nama: "Unit Toko Depan"})
	if err != nil {
		t.Fatalf("create unit kerja tujuan: %v", err)
	}

	// Cross-unit transfers are allowed (isu #12 fase 1), so tujuan deliberately
	// gets its own unit_kerja rather than reusing the fixture's.
	tujuan, err := testApp.ruang.Create(ctx(), &model.CreateRuangRequest{
		NamaRuang:   "Toko Depan",
		IDUnitKerja: unitTujuan.ID,
	})
	if err != nil {
		t.Fatalf("create ruang tujuan: %v", err)
	}

	s := mutasiSetup{fixture: f, tujuan: tujuan.ID}

	// 3 @ 10.000 then 4 @ 10.001 — seven pieces worth 70.004, an average that repeats.
	for _, beli := range []struct{ qty, harga string }{
		{"3", "10000"},
		{"4", "10001"},
	} {
		draft, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
			ActorID:    f.actor,
			Tanggal:    "2026-08-11",
			IDSupplier: f.supplier,
			IDRuang:    f.ruang,
			Detail: []model.PembelianDetailRequest{{
				IDProduct:        f.product,
				IDSatuanInput:    f.pcs,
				QtyFaktur:        beli.qty,
				HargaSatuanInput: beli.harga,
			}},
		})
		if err != nil {
			t.Fatalf("create pembelian %s @ %s: %v", beli.qty, beli.harga, err)
		}

		ajukanDanPosting(t, testApp, f, draft.ID)
	}

	sebelum := totalNilaiPersediaan(t, s.product)
	if sebelum != "70004.00" {
		t.Fatalf("nilai persediaan sebelum mutasi = %q, want 70004.00", sebelum)
	}

	mutasi := buatMutasi(t, testApp, s, "3")
	posted := postingMutasi(t, testApp, s, mutasi.ID)

	if sesudah := totalNilaiPersediaan(t, s.product); sesudah != sebelum {
		t.Errorf("nilai persediaan = %q setelah mutasi, want tetap %q", sesudah, sebelum)
	}

	// The cost is written back onto the line at posting, from what the outgoing row
	// reported — a DRAFT has no answer for it, which is why the column is nullable.
	hpp := posted.Detail[0].HargaPokokSatuanDasar
	if hpp == nil {
		t.Fatalf("harga_pokok_satuan_dasar masih null setelah posting")
	}

	if *hpp != "10000.5714" {
		t.Errorf("harga_pokok_satuan_dasar = %q, want 10000.5714", *hpp)
	}
}

// A DRAFT carries no cost, because the cost does not exist yet: it is the source room's
// moving average at the moment of posting, and only the trigger knows it.
func TestDraftMutasiBelumPunyaHargaPokok(t *testing.T) {
	testApp, s := stokAwalMutasi(t, newApp(t), "100")

	mutasi := buatMutasi(t, testApp, s, "10")

	if got := mutasi.Detail[0].HargaPokokSatuanDasar; got != nil {
		t.Errorf("harga_pokok_satuan_dasar pada DRAFT = %q, want null", *got)
	}

	if mutasi.Status != "DRAFT" {
		t.Errorf("status = %q, want DRAFT", mutasi.Status)
	}

	// No stock has moved: a draft is a typed page.
	if stok, _ := saldoStok(t, s.product, s.tujuan); stok != 0 {
		t.Errorf("stok ruang tujuan = %d pada DRAFT, want 0", stok)
	}
}

// More than the source room holds is refused, and nothing is left behind — not a single
// kartu_stok row, which matters because they could not be deleted afterwards.
//
// The message names the product and both figures. The Go pre-check exists only for that:
// the trigger is the guard, and its RAISE cannot say whether the problem is the stock or
// a closed period.
func TestMutasiDitolakSaatSaldoTidakCukup(t *testing.T) {
	testApp, s := stokAwalMutasi(t, newApp(t), "10")

	mutasi := buatMutasi(t, testApp, s, "11")

	_, err := testApp.mutasi.Posting(ctx(), &model.PostingMutasiRequest{
		ID: mutasi.ID, ActorID: s.actor,
	})
	assertKind(t, err, model.KindInvalid)
	assertPesanMemuat(t, err, "saldo ruang asal 10")

	var baris int
	if err := testDB.QueryRow(
		`SELECT COUNT(*) FROM kartu_stok WHERE ref_table = 'mutasi' AND ref_id_transaksi = $1`,
		mutasi.ID,
	).Scan(&baris); err != nil {
		t.Fatalf("hitung baris kartu stok: %v", err)
	}

	if baris != 0 {
		t.Errorf("baris kartu stok = %d setelah posting ditolak, want 0", baris)
	}

	if stok, _ := saldoStok(t, s.product, s.ruang); stok != 10 {
		t.Errorf("stok ruang asal = %d setelah posting ditolak, want 10", stok)
	}
}

// Two lines for the same product are allowed — unlike the three documents that draw down
// a quota held on a parent line, where a per-document unique index forbids it. Here the
// limit is the source room's balance, so what has to be caught is the two lines
// exceeding it *together* while each fits on its own.
func TestDuaBarisProdukSamaDijumlahkanTerhadapSaldo(t *testing.T) {
	testApp, s := stokAwalMutasi(t, newApp(t), "10")

	buat := func(a, b string) (*model.MutasiResponse, error) {
		return testApp.mutasi.Create(ctx(), &model.CreateMutasiRequest{
			ActorID:       s.actor,
			Tanggal:       "2026-08-15",
			IDRuangAsal:   s.ruang,
			IDRuangTujuan: s.tujuan,
			Detail: []model.MutasiDetailRequest{
				{IDProduct: s.product, IDSatuanInput: s.pcs, QtyInput: a},
				{IDProduct: s.product, IDSatuanInput: s.pcs, QtyInput: b},
			},
		})
	}

	// 6 + 6 = 12 against a balance of 10. Each line fits; the pair does not.
	kebanyakan, err := buat("6", "6")
	if err != nil {
		t.Fatalf("create mutasi dua baris: %v", err)
	}

	_, err = testApp.mutasi.Posting(ctx(), &model.PostingMutasiRequest{
		ID: kebanyakan.ID, ActorID: s.actor,
	})
	assertKind(t, err, model.KindInvalid)

	// 6 + 4 = 10 is exactly the balance, and both lines survive as separate rows.
	pas, err := buat("6", "4")
	if err != nil {
		t.Fatalf("create mutasi dua baris pas: %v", err)
	}

	posted := postingMutasi(t, testApp, s, pas.ID)

	if len(posted.Detail) != 2 {
		t.Fatalf("detail = %d baris, want 2", len(posted.Detail))
	}

	if stok, nilai := saldoStok(t, s.product, s.tujuan); stok != 10 || nilai != "100000.00" {
		t.Errorf("ruang tujuan = (%d, %s), want (10, 100000.00)", stok, nilai)
	}
}

// Posting into a closed month is refused and the refusal names the month, exactly as the
// other three stock-writing modules do. The pre-check is what buys the wording; the
// trigger is still the guard.
func TestPostingMutasiKePeriodeTutupMenyebutPeriodenya(t *testing.T) {
	testApp, s := stokAwalMutasi(t, newApp(t), "100")

	mutasi := buatMutasi(t, testApp, s, "10")
	tutupPeriode(t, testApp, s.actor, 2026, 8)

	_, err := testApp.mutasi.Posting(ctx(), &model.PostingMutasiRequest{
		ID: mutasi.ID, ActorID: s.actor,
	})
	assertKind(t, err, model.KindInvalid)
	assertPesanMemuat(t, err, "2026-08")

	if stok, _ := saldoStok(t, s.product, s.tujuan); stok != 0 {
		t.Errorf("stok ruang tujuan = %d setelah posting ditolak, want 0", stok)
	}
}

// Voiding a transfer whose period has since closed still works, and the reversing rows
// land in the current period. Same decision as isu #6 made for pembelian, and it applies
// here unchanged: the alternative leaves a mistyped transfer from a closed month with no
// way out.
//
// The cost is the one already recorded for pembelian — the document reads BATAL while the
// closed month's ledger still carries its movements — so anything reporting per period
// has to read kartu_stok, never a document status.
func TestBatalMutasiPeriodeTutupMasukPeriodeBerjalan(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	unitTujuan, err := testApp.unitKerja.Create(ctx(), &model.CreateUnitKerjaRequest{Nama: "Unit Toko Depan"})
	if err != nil {
		t.Fatalf("create unit kerja tujuan: %v", err)
	}

	// Cross-unit transfers are allowed (isu #12 fase 1), so tujuan deliberately
	// gets its own unit_kerja rather than reusing the fixture's.
	tujuan, err := testApp.ruang.Create(ctx(), &model.CreateRuangRequest{
		NamaRuang:   "Toko Depan",
		IDUnitKerja: unitTujuan.ID,
	})
	if err != nil {
		t.Fatalf("create ruang tujuan: %v", err)
	}

	s := mutasiSetup{fixture: f, tujuan: tujuan.ID}

	// Both documents are dated in a month that is then closed, leaving the current one
	// open for the reversal to land in.
	lalu := awalBulanLalu(t)

	beli := draftBertanggal(t, testApp, f, lalu)
	ajukanDanPosting(t, testApp, f, beli.ID)

	mutasi, err := testApp.mutasi.Create(ctx(), &model.CreateMutasiRequest{
		ActorID: s.actor, Tanggal: lalu.Format("2006-01-02"),
		IDRuangAsal: s.ruang, IDRuangTujuan: s.tujuan,
		Detail: []model.MutasiDetailRequest{{
			IDProduct: s.product, IDSatuanInput: s.pcs, QtyInput: "4",
		}},
	})
	if err != nil {
		t.Fatalf("create mutasi: %v", err)
	}

	postingMutasi(t, testApp, s, mutasi.ID)

	tutupPeriode(t, testApp, s.actor, lalu.Year(), int(lalu.Month()))

	if _, err := testApp.mutasi.Batal(ctx(), &model.BatalMutasiRequest{
		ID: mutasi.ID, ActorID: s.actor, AlasanBatal: "salah ruang tujuan",
	}); err != nil {
		t.Fatalf("batal setelah periodenya ditutup: %v", err)
	}

	// The goods are back where they started, and the total is still what it was.
	if stok, _ := saldoStok(t, s.product, s.ruang); stok != 10 {
		t.Errorf("stok ruang asal = %d setelah batal, want 10", stok)
	}

	if stok, nilai := saldoStok(t, s.product, s.tujuan); stok != 0 || nilai != "0.00" {
		t.Errorf("ruang tujuan = (%d, %s) setelah batal, want (0, 0.00)", stok, nilai)
	}

	if total := totalNilaiPersediaan(t, s.product); total != "100000.00" {
		t.Errorf("nilai persediaan = %q setelah batal, want 100000.00", total)
	}

	// The closed month keeps exactly the two rows the transfer put there — nothing added,
	// nothing removed. That is the cost stated at PembelianUseCase.Batal: the document
	// reads BATAL while the closed month's ledger still carries its movements.
	var diBulanTutup int
	if err := testDB.QueryRow(`
		SELECT COUNT(*) FROM kartu_stok
		WHERE ref_table = 'mutasi' AND ref_id_transaksi = $1
		  AND EXTRACT(YEAR FROM tanggal_transaksi)::INT = $2
		  AND EXTRACT(MONTH FROM tanggal_transaksi)::INT = $3
	`, mutasi.ID, lalu.Year(), int(lalu.Month())).Scan(&diBulanTutup); err != nil {
		t.Fatalf("hitung baris di bulan tertutup: %v", err)
	}

	if diBulanTutup != 2 {
		t.Errorf("baris di bulan tertutup = %d, want 2 (hanya postingnya sendiri)", diBulanTutup)
	}

	// Two reversing rows, one per original movement, dated in the current month.
	var pembalik int
	var tanggal time.Time

	if err := testDB.QueryRow(`
		SELECT COUNT(*), MAX(tanggal_transaksi)
		FROM kartu_stok
		WHERE ref_table = 'mutasi' AND ref_id_transaksi = $1 AND id_kartu_stok_asal IS NOT NULL
	`, mutasi.ID).Scan(&pembalik, &tanggal); err != nil {
		t.Fatalf("baca baris pembalik: %v", err)
	}

	if pembalik != 2 {
		t.Errorf("baris pembalik = %d, want 2", pembalik)
	}

	now := time.Now()
	if tanggal.Year() != now.Year() || tanggal.Month() != now.Month() {
		t.Errorf(
			"baris pembalik bertanggal %s, want bulan berjalan %04d-%02d",
			tanggal.Format("2006-01-02"), now.Year(), int(now.Month()),
		)
	}
}

// Cancelling is refused once the goods have left the destination room, and the trigger is
// what refuses it: the reversing row would drive that room's balance negative.
//
// This is correct rather than unfortunate. Goods already sold out of the shop cannot be
// put back into the warehouse by voiding the transfer that brought them there — the
// remedy is another transfer, which is a document somebody has to actually mean.
func TestBatalMutasiDitolakSaatBarangSudahKeluarLagi(t *testing.T) {
	testApp, s := stokAwalMutasi(t, newApp(t), "100")

	mutasi := buatMutasi(t, testApp, s, "30")
	postingMutasi(t, testApp, s, mutasi.ID)

	// The goods move on from the destination room — back to the warehouse, which is the
	// cheapest way to empty it without another module.
	lanjut, err := testApp.mutasi.Create(ctx(), &model.CreateMutasiRequest{
		ActorID:       s.actor,
		Tanggal:       "2026-08-16",
		IDRuangAsal:   s.tujuan,
		IDRuangTujuan: s.ruang,
		Detail: []model.MutasiDetailRequest{{
			IDProduct: s.product, IDSatuanInput: s.pcs, QtyInput: "30",
		}},
	})
	if err != nil {
		t.Fatalf("create mutasi lanjutan: %v", err)
	}

	postingMutasi(t, testApp, s, lanjut.ID)

	_, err = testApp.mutasi.Batal(ctx(), &model.BatalMutasiRequest{
		ID: mutasi.ID, ActorID: s.actor, AlasanBatal: "salah ruang tujuan",
	})
	assertKind(t, err, model.KindInvalid)

	// Nothing was reversed, so the first transfer is still POSTED and the ledger is
	// untouched.
	tetap, err := testApp.mutasi.Get(ctx(), &model.GetMutasiRequest{ID: mutasi.ID})
	if err != nil {
		t.Fatalf("get mutasi: %v", err)
	}

	if tetap.Status != "POSTED" {
		t.Errorf("status = %q setelah batal ditolak, want POSTED", tetap.Status)
	}

	if total := totalNilaiPersediaan(t, s.product); total != "1000000.00" {
		t.Errorf("nilai persediaan = %q, want 1000000.00", total)
	}
}

// The status guard is the primary defence against posting twice; HasRef is the one that
// does not depend on the status column being right. Forcing the column back to DRAFT
// behind the application's back is exactly the case it exists for — posting again would
// move the goods twice, and kartu_stok cannot be edited afterwards.
func TestPostingMutasiDuaKaliDitolakHasRef(t *testing.T) {
	testApp, s := stokAwalMutasi(t, newApp(t), "100")

	mutasi := buatMutasi(t, testApp, s, "30")
	postingMutasi(t, testApp, s, mutasi.ID)

	if _, err := testDB.Exec(`UPDATE mutasi SET status = 'DRAFT' WHERE id = $1`, mutasi.ID); err != nil {
		t.Fatalf("paksa status mundur: %v", err)
	}

	_, err := testApp.mutasi.Posting(ctx(), &model.PostingMutasiRequest{
		ID: mutasi.ID, ActorID: s.actor,
	})
	assertKind(t, err, model.KindConflict)

	if stok, _ := saldoStok(t, s.product, s.tujuan); stok != 30 {
		t.Errorf("stok ruang tujuan = %d, want 30 (tidak berpindah dua kali)", stok)
	}
}

// Two transfers of the same product going opposite ways, posted at the same moment, must
// not deadlock.
//
// This is the ABBA that arrives with this module and could not happen before, because
// every earlier document touched a single room. The trigger takes an advisory lock per
// (barang, ruang), and the order a transfer needs them in is decided by which way the
// goods are going — so one document takes (X, gudang) then (X, toko) while the other
// takes them the other way round. KunciSaldo takes both up front in a canonical order,
// which makes the cycle impossible instead of merely unlikely.
//
// Against the fixed code this passes every time. Against a version without the pre-lock
// it fails intermittently, which is the honest description of the bug: rounds are run so
// the window is hit rather than hoped for.
func TestMutasiBerlawananArahTidakDeadlock(t *testing.T) {
	testApp, s := stokAwalMutasi(t, newApp(t), "200")

	// Seed the destination room so both directions have something to move.
	seed := buatMutasi(t, testApp, s, "100")
	postingMutasi(t, testApp, s, seed.ID)

	const ronde = 8

	// Drafts are prepared first so the concurrent part is only the posting: what is being
	// tested is the lock order inside Posting, not the write of a draft.
	type pasangan struct{ keluar, masuk int64 }

	pasangans := make([]pasangan, 0, ronde)

	for i := range ronde {
		maju, err := testApp.mutasi.Create(ctx(), &model.CreateMutasiRequest{
			ActorID: s.actor, Tanggal: "2026-08-20",
			IDRuangAsal: s.ruang, IDRuangTujuan: s.tujuan,
			Detail: []model.MutasiDetailRequest{{
				IDProduct: s.product, IDSatuanInput: s.pcs, QtyInput: "1",
			}},
		})
		if err != nil {
			t.Fatalf("create mutasi maju %d: %v", i, err)
		}

		mundur, err := testApp.mutasi.Create(ctx(), &model.CreateMutasiRequest{
			ActorID: s.actor, Tanggal: "2026-08-20",
			IDRuangAsal: s.tujuan, IDRuangTujuan: s.ruang,
			Detail: []model.MutasiDetailRequest{{
				IDProduct: s.product, IDSatuanInput: s.pcs, QtyInput: "1",
			}},
		})
		if err != nil {
			t.Fatalf("create mutasi mundur %d: %v", i, err)
		}

		pasangans = append(pasangans, pasangan{keluar: maju.ID, masuk: mundur.ID})
	}

	mulai := make(chan struct{})
	gagal := make(chan error, ronde*2)

	var wg sync.WaitGroup

	posting := func(id int64) {
		defer wg.Done()

		<-mulai

		if _, err := testApp.mutasi.Posting(ctx(), &model.PostingMutasiRequest{
			ID: id, ActorID: s.actor,
		}); err != nil {
			gagal <- fmt.Errorf("posting mutasi %d: %w", id, err)
		}
	}

	for _, p := range pasangans {
		wg.Add(2)

		go posting(p.keluar)
		go posting(p.masuk)
	}

	close(mulai)
	wg.Wait()
	close(gagal)

	for err := range gagal {
		t.Errorf("%v", err)
	}

	// Every transfer moved exactly one piece each way, so the rooms end where they
	// started — and so does the total.
	if stok, _ := saldoStok(t, s.product, s.ruang); stok != 100 {
		t.Errorf("stok ruang asal = %d, want 100", stok)
	}

	if stok, _ := saldoStok(t, s.product, s.tujuan); stok != 100 {
		t.Errorf("stok ruang tujuan = %d, want 100", stok)
	}

	if total := totalNilaiPersediaan(t, s.product); total != "2000000.00" {
		t.Errorf("nilai persediaan = %q, want 2000000.00", total)
	}
}

// A room cannot transfer to itself. mutasi_ruang_check would refuse it too, but a CHECK
// violation names a constraint and cannot say which field is wrong — the Go check exists
// for the message, exactly as ExistsByKode does against a unique index.
func TestMutasiKeRuangYangSamaDitolak(t *testing.T) {
	testApp, s := stokAwalMutasi(t, newApp(t), "10")

	_, err := testApp.mutasi.Create(ctx(), &model.CreateMutasiRequest{
		ActorID:       s.actor,
		Tanggal:       "2026-08-15",
		IDRuangAsal:   s.ruang,
		IDRuangTujuan: s.ruang,
		Detail: []model.MutasiDetailRequest{{
			IDProduct: s.product, IDSatuanInput: s.pcs, QtyInput: "1",
		}},
	})
	assertKind(t, err, model.KindInvalid)
	assertPesanMemuat(t, err, "id_ruang_asal")

	// And the same collision reached through a patch that moves only one of the two: the
	// check has to be against the stored row, not against the patch alone.
	mutasi := buatMutasi(t, testApp, s, "1")

	_, err = testApp.mutasi.Update(ctx(), &model.UpdateMutasiRequest{
		ID:            mutasi.ID,
		ActorID:       s.actor,
		IDRuangTujuan: model.Optional[int64]{Present: true, Value: &s.ruang},
	})
	assertKind(t, err, model.KindInvalid)
}

// Posting a document with no lines is refused. Lines are optional at create time — a
// transfer is opened while the trolley is still being loaded — so this is the only place
// the emptiness has to be caught.
func TestMutasiTanpaBarisTidakBisaDiposting(t *testing.T) {
	testApp, s := stokAwalMutasi(t, newApp(t), "10")

	kosong, err := testApp.mutasi.Create(ctx(), &model.CreateMutasiRequest{
		ActorID:       s.actor,
		Tanggal:       "2026-08-15",
		IDRuangAsal:   s.ruang,
		IDRuangTujuan: s.tujuan,
	})
	if err != nil {
		t.Fatalf("create mutasi kosong: %v", err)
	}

	_, err = testApp.mutasi.Posting(ctx(), &model.PostingMutasiRequest{
		ID: kosong.ID, ActorID: s.actor,
	})
	assertKind(t, err, model.KindInvalid)
}

// The DRAFT queue. Without a DIAJUKAN state there is no "this one is ready" signal, so
// the list filtered to DRAFT is the whole of SUPERADMIN's work queue — and a queue is
// read oldest first, because it is read to be worked through.
func TestDaftarMutasiDraftBisaDiurutTerlamaDulu(t *testing.T) {
	testApp, s := stokAwalMutasi(t, newApp(t), "100")

	for _, tanggal := range []string{"2026-08-13", "2026-08-11", "2026-08-12"} {
		if _, err := testApp.mutasi.Create(ctx(), &model.CreateMutasiRequest{
			ActorID: s.actor, Tanggal: tanggal,
			IDRuangAsal: s.ruang, IDRuangTujuan: s.tujuan,
			Detail: []model.MutasiDetailRequest{{
				IDProduct: s.product, IDSatuanInput: s.pcs, QtyInput: "1",
			}},
		}); err != nil {
			t.Fatalf("create mutasi %s: %v", tanggal, err)
		}
	}

	// One of them is posted, so the DRAFT filter has something to exclude.
	antre, _, err := testApp.mutasi.Search(ctx(), &model.ListMutasiRequest{
		Status: "DRAFT", TerlamaDulu: true,
	})
	if err != nil {
		t.Fatalf("search mutasi: %v", err)
	}

	if len(antre) != 3 {
		t.Fatalf("antrean = %d dokumen, want 3", len(antre))
	}

	for i, want := range []string{"2026-08-11", "2026-08-12", "2026-08-13"} {
		if got := antre[i].Tanggal.Format("2006-01-02"); got != want {
			t.Errorf("antrean[%d] bertanggal %s, want %s", i, got, want)
		}
	}

	postingMutasi(t, testApp, s, antre[0].ID)

	sisa, _, err := testApp.mutasi.Search(ctx(), &model.ListMutasiRequest{
		Status: "DRAFT", TerlamaDulu: true,
	})
	if err != nil {
		t.Fatalf("search mutasi setelah posting: %v", err)
	}

	if len(sisa) != 2 {
		t.Errorf("antrean setelah satu diposting = %d, want 2", len(sisa))
	}
}

// GET /product/{id}/stok — isu #7 fase 1, and the read the mutasi entry screen cannot
// work without. It is also the first thing in the codebase to read kartu_stok back.
func TestStokPerRuangMengikutiKartuStok(t *testing.T) {
	testApp, s := stokAwalMutasi(t, newApp(t), "100")

	mutasi := buatMutasi(t, testApp, s, "30")
	postingMutasi(t, testApp, s, mutasi.ID)

	stok, err := testApp.product.Stok(ctx(), &model.ListStokProductRequest{IDProduct: s.product})
	if err != nil {
		t.Fatalf("stok per ruang: %v", err)
	}

	if len(stok) != 2 {
		t.Fatalf("ruang terisi = %d, want 2", len(stok))
	}

	// Ordered by room name: "Gudang Utama" then "Toko Depan".
	if stok[0].NamaRuang != "Gudang Utama" || stok[0].StokAkhir != 70 {
		t.Errorf("stok[0] = %s %d, want Gudang Utama 70", stok[0].NamaRuang, stok[0].StokAkhir)
	}

	if stok[1].NamaRuang != "Toko Depan" || stok[1].StokAkhir != 30 {
		t.Errorf("stok[1] = %s %d, want Toko Depan 30", stok[1].NamaRuang, stok[1].StokAkhir)
	}

	if stok[1].NilaiAkhir != "300000.00" || stok[1].HargaPokokSatuan != "10000.0000" {
		t.Errorf(
			"stok[1] nilai %s hpp %s, want 300000.00 dan 10000.0000",
			stok[1].NilaiAkhir, stok[1].HargaPokokSatuan,
		)
	}

	// An unknown product is a 404, not an empty list. "Nobody has ever stocked this" and
	// "there is no such product" are different facts.
	_, err = testApp.product.Stok(ctx(), &model.ListStokProductRequest{IDProduct: s.product + 9999})
	assertKind(t, err, model.KindNotFound)
}

// A product that exists but has never moved answers an empty list rather than an error,
// and a room it has never been in simply does not appear. Absence is a value here, the
// same way a month with no periode row is open.
func TestStokPerRuangKosongUntukProdukBaru(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	stok, err := testApp.product.Stok(ctx(), &model.ListStokProductRequest{IDProduct: f.product})
	if err != nil {
		t.Fatalf("stok per ruang: %v", err)
	}

	if len(stok) != 0 {
		t.Errorf("ruang terisi = %d untuk produk yang belum pernah bergerak, want 0", len(stok))
	}
}
