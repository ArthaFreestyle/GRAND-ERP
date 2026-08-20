package usecase_test

// Rekonsiliasi rantai kartu_stok (isu #25) — the worker's second job, and the
// first that never writes anything: it reports whether the balance chain
// kartu_stok_hitung_saldo built is what the trigger should have produced, and
// leaves any repair to a real document (stok_opname). These run against a real
// PostgreSQL because the whole point lives there: the window-function comparison
// against NUMERIC columns, the trigger's own arithmetic, and the append-only
// guard this test has to switch off deliberately, the same licence
// truncateMaster uses, to prove a corrupted row is actually caught.

import (
	"fmt"
	"sync"
	"testing"

	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/repository"
	"Arthafreestyle/ERP/internal/usecase"

	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
)

// lastKartuStokID reads the tail row's id for one (product, room) chain — the row
// the corruption test needs to target and the row a consistent chain should never
// flag.
func lastKartuStokID(t *testing.T, idBarang, idRuang int64) int64 {
	t.Helper()

	var id int64
	if err := testDB.QueryRow(
		`SELECT id FROM kartu_stok WHERE id_barang = $1 AND id_ruang = $2 ORDER BY id DESC LIMIT 1`,
		idBarang, idRuang,
	).Scan(&id); err != nil {
		t.Fatalf("last kartu_stok id: %v", err)
	}

	return id
}

// A chain built from a mix of every writer kartu_stok has — purchase, transfer,
// internal use, sale, a cancellation, and a posted physical count — must reconcile
// to zero. This is the shape the issue's own Definition of Done asks for: nothing
// here is a targeted edge case, it is ordinary traffic across six modules.
func TestRekonsiliasiRantaiKonsistenMelaporNol(t *testing.T) {
	testApp := newApp(t)
	testApp, s := stokAwalMutasi(t, testApp, "200")

	mutasi := buatMutasi(t, testApp, s, "30")
	postingMutasi(t, testApp, s, mutasi.ID)

	pemohon, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		ActorID: s.actor, Username: "pemohon_rekon", Password: "rahasia123",
	})
	if err != nil {
		t.Fatalf("create pemohon: %v", err)
	}

	pemakaian, err := testApp.pemakaian.Create(ctx(), &model.CreatePemakaianRequest{
		ActorID: s.actor, Tanggal: "2026-08-16", IDRuang: s.ruang,
		IDPemohon: pemohon.ID, Keperluan: "dipakai rutin",
		Detail: []model.PemakaianDetailRequest{{
			IDProduct: s.product, IDSatuanInput: s.pcs, QtyInput: "10",
		}},
	})
	if err != nil {
		t.Fatalf("create pemakaian: %v", err)
	}
	if _, err := testApp.pemakaian.Ajukan(ctx(), &model.AjukanPemakaianRequest{
		ID: pemakaian.ID, ActorID: s.actor,
	}); err != nil {
		t.Fatalf("ajukan pemakaian: %v", err)
	}
	if _, err := testApp.pemakaian.Setujui(ctx(), &model.SetujuiPemakaianRequest{
		ID: pemakaian.ID, ActorID: s.actor,
	}); err != nil {
		t.Fatalf("setujui pemakaian: %v", err)
	}
	if _, err := testApp.pemakaian.Posting(ctx(), &model.PostingPemakaianRequest{
		ID: pemakaian.ID, ActorID: s.actor,
	}); err != nil {
		t.Fatalf("posting pemakaian: %v", err)
	}

	penjualan, err := testApp.penjualan.Create(ctx(), &model.CreatePenjualanRequest{
		ActorID: s.actor, Tanggal: "2026-08-16", IDRuang: s.ruang,
		Detail: []model.PenjualanDetailRequest{{
			IDProduct: s.product, IDSatuanInput: s.pcs, QtyInput: "10", HargaSatuanInput: "15000",
		}},
	})
	if err != nil {
		t.Fatalf("create penjualan: %v", err)
	}
	if _, err := testApp.penjualan.Posting(ctx(), &model.PostingPenjualanRequest{
		ID: penjualan.ID, ActorID: s.actor,
	}); err != nil {
		t.Fatalf("posting penjualan: %v", err)
	}

	// The pembatalan half of the mix: void the transfer, which writes a reversing
	// row into both rooms it touched.
	if _, err := testApp.mutasi.Batal(ctx(), &model.BatalMutasiRequest{
		ID: mutasi.ID, ActorID: s.actor, AlasanBatal: "salah ruang tujuan",
	}); err != nil {
		t.Fatalf("batal mutasi: %v", err)
	}

	opname := buatOpname(t, testApp, s.fixture)
	hasil := tarikSaldo(t, testApp, s.fixture, opname.ID)
	baris := detailByProduct(t, hasil, s.product)

	// Counted to match the balance TarikSaldo already froze, so the count posts
	// with no selisih rows of its own — this test is about the mix of writers
	// reconciling cleanly, not about exercising stok_opname's own arithmetic.
	stokSaatIni, _ := saldoStok(t, s.product, s.ruang)
	patchStokSO(t, testApp, s.fixture, opname.ID, baris.ID, stokSaatIni)

	ajukanOpname(t, testApp, s.fixture, opname.ID)
	postingOpname(t, testApp, s.fixture, opname.ID)

	selisih, err := testApp.rekonsiliasi.PeriksaRantaiSaldo(ctx())
	if err != nil {
		t.Fatalf("periksa rantai saldo: %v", err)
	}
	if selisih != 0 {
		t.Fatalf("selisih = %d, want 0 atas rantai yang seharusnya konsisten", selisih)
	}
}

// The corrupted-row case the issue's Definition of Done names explicitly: a row
// forced out of step with the trigger's own arithmetic, using the same licence
// truncateMaster gets to disable kartu_stok_append_only, must be caught and named
// — not just counted.
func TestRekonsiliasiMendeteksiBarisYangDirusak(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)
	draft := draftSederhana(t, testApp, f, "50", nil, nil)
	ajukanDanPosting(t, testApp, f, draft.ID)

	rusakID := lastKartuStokID(t, f.product, f.ruang)

	if _, err := testDB.Exec(`ALTER TABLE kartu_stok DISABLE TRIGGER kartu_stok_append_only`); err != nil {
		t.Fatalf("disable kartu_stok guard: %v", err)
	}
	defer func() {
		if _, err := testDB.Exec(`ALTER TABLE kartu_stok ENABLE TRIGGER kartu_stok_append_only`); err != nil {
			t.Fatalf("re-enable kartu_stok guard: %v", err)
		}
	}()

	if _, err := testDB.Exec(
		`UPDATE kartu_stok SET stok_akhir = stok_akhir + 5, nilai_akhir = nilai_akhir + 50000 WHERE id = $1`,
		rusakID,
	); err != nil {
		t.Fatalf("corrupt kartu_stok row: %v", err)
	}

	// A logger of its own, with a hook, rather than testApp.rekonsiliasi's — newApp
	// sets the shared logger to PanicLevel to keep expected-error tests quiet, which
	// would swallow the Error entry this test exists to inspect.
	logger, hook := test.NewNullLogger()
	logger.SetLevel(logrus.DebugLevel)

	rekon := usecase.NewRekonsiliasiUseCase(testDB, logger, repository.NewKartuStokRepository())

	selisih, err := rekon.PeriksaRantaiSaldo(ctx())
	if err != nil {
		t.Fatalf("periksa rantai saldo: %v", err)
	}
	if selisih != 1 {
		t.Fatalf("selisih = %d, want 1 (satu baris yang dirusak)", selisih)
	}

	var ditemukan *logrus.Entry

	for _, entry := range hook.AllEntries() {
		if entry.Level == logrus.ErrorLevel {
			ditemukan = entry

			break
		}
	}

	if ditemukan == nil {
		t.Fatalf("tidak ada log Error, padahal rantai seharusnya menyimpang")
	}

	if got := ditemukan.Data["id_barang"]; got != f.product {
		t.Errorf("log id_barang = %v, want %d", got, f.product)
	}
	if got := ditemukan.Data["id_ruang"]; got != f.ruang {
		t.Errorf("log id_ruang = %v, want %d", got, f.ruang)
	}
	if got := ditemukan.Data["id"]; got != rusakID {
		t.Errorf("log id = %v, want %d (baris yang dirusak)", got, rusakID)
	}
}

// A row written to an existing, already-consistent chain while the reconciliation
// query is in flight must never surface as a false discrepancy. PeriksaRantai
// takes its upper id bound inside the same statement precisely so a concurrent
// insert can never appear as a disconnected tail in a partition already walked —
// this exercises that under real concurrent writers rather than trusting the
// single-statement argument on paper.
func TestRekonsiliasiTidakSalahAlarmSaatAdaPenulisanBersamaan(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	const ronde = 15

	mulai := make(chan struct{})
	gagal := make(chan error, ronde*2)

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-mulai

		for range ronde {
			draft, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
				ActorID: f.actor, Tanggal: "2026-08-11", IDSupplier: f.supplier, IDRuang: f.ruang,
				Detail: []model.PembelianDetailRequest{{
					IDProduct: f.product, IDSatuanInput: f.pcs, QtyFaktur: "1", HargaSatuanInput: "10000",
				}},
			})
			if err != nil {
				gagal <- fmt.Errorf("create pembelian: %w", err)

				return
			}
			if _, err := testApp.pembelian.Ajukan(ctx(), &model.AjukanPembelianRequest{
				ID: draft.ID, ActorID: f.actor,
			}); err != nil {
				gagal <- fmt.Errorf("ajukan pembelian: %w", err)

				return
			}
			if _, err := testApp.pembelian.Posting(ctx(), &model.PostingPembelianRequest{
				ID: draft.ID, ActorID: f.actor,
			}); err != nil {
				gagal <- fmt.Errorf("posting pembelian: %w", err)

				return
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		<-mulai

		for range ronde {
			selisih, err := testApp.rekonsiliasi.PeriksaRantaiSaldo(ctx())
			if err != nil {
				gagal <- fmt.Errorf("periksa rantai saldo: %w", err)

				return
			}
			if selisih != 0 {
				gagal <- fmt.Errorf("selisih palsu saat penulisan bersamaan: %d", selisih)

				return
			}
		}
	}()

	close(mulai)
	wg.Wait()
	close(gagal)

	for err := range gagal {
		t.Error(err)
	}
}
