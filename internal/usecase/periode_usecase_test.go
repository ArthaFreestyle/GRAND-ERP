package usecase_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"Arthafreestyle/ERP/internal/model"
)

// Book closing (isu #6). These run against a real PostgreSQL because most of what
// they assert is enforced there: the trigger reading periode on every kartu_stok
// insert, the upsert keyed on (tahun, bulan), and the fact that a month with no row
// is open.

// awalBulanLalu returns the first day of a month safely in the past — two months
// back, so it is never the current one however close to a month boundary the suite
// runs.
//
// Computed rather than hardcoded on purpose. Half of what is proved below is about
// the difference between a document's period and the current one, and a literal date
// would quietly stop testing that on the month it happened to name.
func awalBulanLalu(t *testing.T) time.Time {
	t.Helper()

	now := time.Now()

	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -2, 0)
}

// draftBertanggal is draftSederhana with the date opened up: ten pieces at 10.000,
// dated wherever the caller needs.
func draftBertanggal(t *testing.T, testApp *app, f fixture, tanggal time.Time) *model.PembelianResponse {
	t.Helper()

	pembelian, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
		ActorID:    f.actor,
		Tanggal:    tanggal.Format("2006-01-02"),
		IDSupplier: f.supplier,
		IDRuang:    f.ruang,
		Detail: []model.PembelianDetailRequest{{
			IDProduct:        f.product,
			IDSatuanInput:    f.pcs,
			QtyFaktur:        "10",
			HargaSatuanInput: "10000",
		}},
	})
	if err != nil {
		t.Fatalf("create pembelian: %v", err)
	}

	return pembelian
}

func tutupPeriode(t *testing.T, testApp *app, actor int64, tahun, bulan int) {
	t.Helper()

	if _, err := testApp.periode.Tutup(ctx(), &model.TutupPeriodeRequest{
		Tahun: tahun, Bulan: bulan, ActorID: actor,
	}); err != nil {
		t.Fatalf("tutup periode %04d-%02d: %v", tahun, bulan, err)
	}
}

// assertPesanMemuat checks the operator-facing half of an error, not just its status.
// The whole point of the pre-check added in isu #6 is the wording, so a test that only
// looks at the kind would pass with the message it was meant to replace.
func assertPesanMemuat(t *testing.T, err error, potongan string) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected error containing %q, got nil", potongan)
	}

	var domainErr *model.Error
	if !errors.As(err, &domainErr) {
		t.Fatalf("expected *model.Error, got %T: %v", err, err)
	}

	if !strings.Contains(domainErr.Message, potongan) {
		t.Errorf("message = %q, want it to contain %q", domainErr.Message, potongan)
	}
}

// A month nobody has closed answers BUKA rather than 404. There is no row for it, and
// migration 000004 treats that as open — so "no row" and "open" are the same fact, and
// a 404 would invite a client to conclude the month does not exist.
func TestGetPeriodeTanpaBarisMenjawabBukaSintetis(t *testing.T) {
	testApp := newApp(t)

	periode, err := testApp.periode.Get(ctx(), &model.GetPeriodeRequest{Tahun: 2026, Bulan: 3})
	if err != nil {
		t.Fatalf("get periode: %v", err)
	}

	if periode.Status != "BUKA" {
		t.Errorf("status = %q, want BUKA", periode.Status)
	}

	if periode.Tahun != 2026 || periode.Bulan != 3 {
		t.Errorf("periode = %d-%d, want 2026-3", periode.Tahun, periode.Bulan)
	}

	// Nothing ever happened to this month, so none of the audit fields may claim
	// otherwise.
	if periode.DitutupOleh != nil || periode.TsTutup != nil {
		t.Error("bulan tanpa baris membawa jejak penutupan")
	}
}

// The full cycle, and what each step is allowed to overwrite.
//
// ditutup_oleh/ts_tutup survive a reopening and ts_buka survives the next closing:
// together they are the only record that this month was ever reopened, which is the
// entire reason migration 000017 added the second pair rather than reusing the first.
func TestTutupBukaDanTutupLagiMenyimpanKeduaJejak(t *testing.T) {
	testApp := newApp(t)

	// A user with a nama_lengkap, unlike pembelianFixture's: the column is nullable,
	// and without a name filled in the join below would answer NULL for a reason that
	// has nothing to do with the join being right.
	penutup, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		Username:    "kepala_toko",
		Password:    "rahasia123",
		NamaLengkap: ptr("Bu Ratna"),
	})
	if err != nil {
		t.Fatalf("create actor: %v", err)
	}

	actor := penutup.ID

	ditutup, err := testApp.periode.Tutup(ctx(), &model.TutupPeriodeRequest{
		Tahun: 2026, Bulan: 5, ActorID: actor,
	})
	if err != nil {
		t.Fatalf("tutup: %v", err)
	}

	if ditutup.Status != "TUTUP" {
		t.Errorf("status = %q setelah tutup, want TUTUP", ditutup.Status)
	}
	if ditutup.DitutupOleh == nil || *ditutup.DitutupOleh != actor {
		t.Error("ditutup_oleh tidak terisi dari sesi")
	}
	if ditutup.TsTutup == nil {
		t.Error("ts_tutup tidak terisi")
	}
	if ditutup.NamaPenutup == nil || *ditutup.NamaPenutup != "Bu Ratna" {
		t.Error("nama_penutup tidak ikut di-join")
	}
	if ditutup.DibukaOleh != nil || ditutup.TsBuka != nil {
		t.Error("penutupan pertama sudah membawa jejak pembukaan")
	}

	// Closing an already-closed month changes nothing, so it is a conflict rather
	// than a silent success that would let a caller think they had just closed it.
	_, err = testApp.periode.Tutup(ctx(), &model.TutupPeriodeRequest{
		Tahun: 2026, Bulan: 5, ActorID: actor,
	})
	assertKind(t, err, model.KindConflict)

	dibuka, err := testApp.periode.Buka(ctx(), &model.BukaPeriodeRequest{
		Tahun: 2026, Bulan: 5, ActorID: actor,
	})
	if err != nil {
		t.Fatalf("buka: %v", err)
	}

	if dibuka.Status != "BUKA" {
		t.Errorf("status = %q setelah buka, want BUKA", dibuka.Status)
	}
	if dibuka.DibukaOleh == nil || *dibuka.DibukaOleh != actor {
		t.Error("dibuka_oleh tidak terisi dari sesi")
	}
	if dibuka.TsBuka == nil {
		t.Error("ts_buka tidak terisi")
	}
	// The closing that was undone is still part of this month's history.
	if dibuka.DitutupOleh == nil || dibuka.TsTutup == nil {
		t.Error("membuka kembali menghapus jejak penutupan")
	}

	// Reopening an open month is refused for the same reason closing a closed one is.
	_, err = testApp.periode.Buka(ctx(), &model.BukaPeriodeRequest{
		Tahun: 2026, Bulan: 5, ActorID: actor,
	})
	assertKind(t, err, model.KindConflict)

	lagi, err := testApp.periode.Tutup(ctx(), &model.TutupPeriodeRequest{
		Tahun: 2026, Bulan: 5, ActorID: actor,
	})
	if err != nil {
		t.Fatalf("tutup lagi: %v", err)
	}

	if lagi.Status != "TUTUP" {
		t.Errorf("status = %q setelah tutup lagi, want TUTUP", lagi.Status)
	}
	// This is the assertion the extra columns exist for: after closing again, the
	// month still says it was reopened once, and by whom.
	if lagi.TsBuka == nil || lagi.DibukaOleh == nil {
		t.Error("menutup lagi menghapus jejak pembukaan")
	}
}

// Reopening a month that was never closed is refused rather than treated as a no-op.
// It has no row, and a month with no row is already open.
func TestBukaPeriodeYangTidakPernahDitutupDitolak(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	_, err := testApp.periode.Buka(ctx(), &model.BukaPeriodeRequest{
		Tahun: 2026, Bulan: 4, ActorID: f.actor,
	})
	assertKind(t, err, model.KindConflict)

	// And no row was invented on the way to refusing.
	var jumlah int
	if err := testDB.QueryRow(
		`SELECT COUNT(*) FROM periode WHERE tahun = 2026 AND bulan = 4`,
	).Scan(&jumlah); err != nil {
		t.Fatalf("hitung periode: %v", err)
	}

	if jumlah != 0 {
		t.Errorf("baris periode = %d setelah buka ditolak, want 0", jumlah)
	}
}

// Closing does not have to be sequential: August may be closed while July is open.
// Requiring an order would mean closing every unused month first, and nothing can
// break from the gap — enforcement is per-month inside the trigger, not a running
// total.
func TestTutupTidakHarusBerurutan(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	tutupPeriode(t, testApp, f.actor, 2026, 9)

	juli, err := testApp.periode.Get(ctx(), &model.GetPeriodeRequest{Tahun: 2026, Bulan: 8})
	if err != nil {
		t.Fatalf("get periode 2026-08: %v", err)
	}

	if juli.Status != "BUKA" {
		t.Errorf("status 2026-08 = %q setelah 2026-09 ditutup, want BUKA", juli.Status)
	}
}

// The refusal names the period and only the period.
//
// The trigger is still the guard, but its RAISE carries no constraint name, so
// invalidOnCheck cannot tell a closed month from insufficient stock and every call
// site has to say "periode sudah TUTUP atau saldo tidak mencukupi". An operator
// reading that learns nothing about which of the two to fix. This is what the Go
// pre-check buys.
func TestPostingKePeriodeTutupMenyebutPeriodenya(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	// draftSederhana dates its document 2026-08-11.
	pembelian := draftSederhana(t, testApp, f, "10", nil, nil)
	tutupPeriode(t, testApp, f.actor, 2026, 8)

	if _, err := testApp.pembelian.Ajukan(ctx(), &model.AjukanPembelianRequest{
		ID: pembelian.ID, ActorID: f.actor,
	}); err != nil {
		t.Fatalf("ajukan: %v", err)
	}

	_, err := testApp.pembelian.Posting(ctx(), &model.PostingPembelianRequest{
		ID: pembelian.ID, ActorID: f.actor,
	})
	assertKind(t, err, model.KindInvalid)
	assertPesanMemuat(t, err, "2026-08")
	// The old combined message must not be what came back.
	assertPesanTidakMemuat(t, err, "saldo tidak mencukupi")

	if stok, _ := saldoStok(t, f.product, f.ruang); stok != 0 {
		t.Errorf("stok = %d setelah posting ditolak, want 0", stok)
	}
}

// Voiding a document whose period has since been closed still works, and the reversing
// rows land in the current period.
//
// This is the decision isu #6 asked to make explicit, and it is the one that gives
// "closing the books" its meaning here: a correction to a closed period is booked in
// the current one rather than by prising the closed one open. The alternative leaves a
// mistyped document from a closed month with no way out at all.
//
// What it costs is visible below and is the reason this is pinned rather than left to
// be rediscovered: the document reads BATAL while the closed month's ledger still
// carries its movement. Anything reporting per period has to read kartu_stok, not the
// document's status.
func TestBatalDokumenPeriodeTutupMasukPeriodeBerjalan(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	lalu := awalBulanLalu(t)

	pembelian := draftBertanggal(t, testApp, f, lalu)
	ajukanDanPosting(t, testApp, f, pembelian.ID)

	if stok, _ := saldoStok(t, f.product, f.ruang); stok != 10 {
		t.Fatalf("stok = %d setelah posting, want 10", stok)
	}

	// The books for the month the goods arrived in are now shut.
	tutupPeriode(t, testApp, f.actor, lalu.Year(), int(lalu.Month()))

	if _, err := testApp.pembelian.Batal(ctx(), &model.BatalPembelianRequest{
		ID: pembelian.ID, ActorID: f.actor, AlasanBatal: "faktur salah ketik",
	}); err != nil {
		t.Fatalf("batal setelah periodenya ditutup: %v", err)
	}

	if stok, nilai := saldoStok(t, f.product, f.ruang); stok != 0 || nilai != "0.00" {
		t.Errorf("saldo = (%d, %s) setelah batal, want (0, 0.00)", stok, nilai)
	}

	// The closed month's own rows are untouched: still exactly the one posting, with
	// nothing added to it and nothing taken away.
	var diBulanTutup int
	if err := testDB.QueryRow(`
		SELECT COUNT(*) FROM kartu_stok
		WHERE ref_id_transaksi = $1
		  AND EXTRACT(YEAR FROM tanggal_transaksi)::INT = $2
		  AND EXTRACT(MONTH FROM tanggal_transaksi)::INT = $3
	`, pembelian.ID, lalu.Year(), int(lalu.Month())).Scan(&diBulanTutup); err != nil {
		t.Fatalf("hitung baris di bulan tertutup: %v", err)
	}

	if diBulanTutup != 1 {
		t.Errorf("baris di bulan tertutup = %d, want 1 (hanya postingnya sendiri)", diBulanTutup)
	}

	// And the reversal is dated in the current month, which is the open one.
	var tanggalPembalik time.Time
	if err := testDB.QueryRow(`
		SELECT tanggal_transaksi FROM kartu_stok
		WHERE ref_id_transaksi = $1 AND id_kartu_stok_asal IS NOT NULL
	`, pembelian.ID).Scan(&tanggalPembalik); err != nil {
		t.Fatalf("baca baris pembalik: %v", err)
	}

	now := time.Now()
	if tanggalPembalik.Year() != now.Year() || tanggalPembalik.Month() != now.Month() {
		t.Errorf(
			"baris pembalik bertanggal %s, want bulan berjalan %04d-%02d",
			tanggalPembalik.Format("2006-01-02"), now.Year(), int(now.Month()),
		)
	}
}

// The mirror of the case above, and the one place a closed period does stop a
// cancellation: when the *current* month is the closed one. There is then nowhere to
// book the correction until somebody reopens it, and the message says which month.
func TestBatalDitolakSaatPeriodeBerjalanTutup(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	lalu := awalBulanLalu(t)

	pembelian := draftBertanggal(t, testApp, f, lalu)
	ajukanDanPosting(t, testApp, f, pembelian.ID)

	now := time.Now()
	tutupPeriode(t, testApp, f.actor, now.Year(), int(now.Month()))

	_, err := testApp.pembelian.Batal(ctx(), &model.BatalPembelianRequest{
		ID: pembelian.ID, ActorID: f.actor, AlasanBatal: "faktur salah ketik",
	})
	assertKind(t, err, model.KindInvalid)

	// Nothing was reversed, so the goods are still on the shelf.
	if stok, _ := saldoStok(t, f.product, f.ruang); stok != 10 {
		t.Errorf("stok = %d setelah batal ditolak, want 10", stok)
	}
}

// Closing the books waits for a posting already in flight for that month, and does
// not wait for one in any other month.
//
// This is the race isu #6 asked to close, and it is not hypothetical: book closing is
// run in the late afternoon while the receiving desk is still typing. Under READ
// COMMITTED and without the lock, a posting transaction reads 'BUKA', the closing
// commits underneath it, and the posting then commits its row into a month whose books
// are shut — with nothing anywhere reporting that it happened.
//
// The lock is taken by the trigger (shared) and by Tutup (exclusive), so the only
// honest way to test it is with a real uncommitted transaction holding the shared
// side. The kartu_stok row is inserted directly rather than through a document,
// because a usecase owns its own transaction and there is no way to hold it open from
// out here.
func TestTutupMenungguPostingYangSedangBerjalan(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	const tahun, bulan = 2026, 6

	tx, err := testDB.BeginTx(ctx(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() {
		_ = tx.Rollback() // no-op once committed
	}()

	// Takes the shared advisory lock for 2026-06 and holds it until this commits.
	if _, err := tx.ExecContext(ctx(), `
		INSERT INTO kartu_stok (
			id_barang, id_ruang, tanggal_transaksi, jenis_transaksi,
			stok_masuk, nilai_masuk, ref_table, ref_id_transaksi, created_by
		)
		VALUES ($1, $2, $3, 'PEMBELIAN', 5, 50000, 'pembelian', 1, $4)
	`, f.product, f.ruang, time.Date(tahun, bulan, 15, 0, 0, 0, 0, time.UTC), f.actor); err != nil {
		t.Fatalf("insert kartu_stok: %v", err)
	}

	// The negative control, and it has to come first: a different month must not be
	// held up by any of this, or the "lock" would just be a global stop-the-world.
	selesai := make(chan error, 1)
	go func() {
		_, err := testApp.periode.Tutup(ctx(), &model.TutupPeriodeRequest{
			Tahun: tahun, Bulan: bulan + 1, ActorID: f.actor,
		})
		selesai <- err
	}()

	select {
	case err := <-selesai:
		if err != nil {
			t.Fatalf("tutup bulan lain: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("menutup bulan lain ikut terblokir; kuncinya tidak per-bulan")
	}

	// And now the month the uncommitted posting belongs to.
	tertahan := make(chan error, 1)
	go func() {
		_, err := testApp.periode.Tutup(ctx(), &model.TutupPeriodeRequest{
			Tahun: tahun, Bulan: bulan, ActorID: f.actor,
		})
		tertahan <- err
	}()

	select {
	case err := <-tertahan:
		t.Fatalf("tutup selesai (err=%v) selagi posting bulan itu belum commit", err)
	case <-time.After(500 * time.Millisecond):
		// Still waiting, which is the point.
	}

	if err := tx.Commit(); err != nil {
		t.Fatalf("commit posting: %v", err)
	}

	select {
	case err := <-tertahan:
		if err != nil {
			t.Fatalf("tutup setelah posting commit: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("tutup tidak pernah selesai setelah postingnya commit")
	}

	// The posting got in, and the month is shut behind it — which is the ordering the
	// lock exists to produce.
	if stok, _ := saldoStok(t, f.product, f.ruang); stok != 5 {
		t.Errorf("stok = %d, want 5 (postingnya harus tetap masuk)", stok)
	}

	periode, err := testApp.periode.Get(ctx(), &model.GetPeriodeRequest{Tahun: tahun, Bulan: bulan})
	if err != nil {
		t.Fatalf("get periode: %v", err)
	}

	if periode.Status != "TUTUP" {
		t.Errorf("status = %q, want TUTUP", periode.Status)
	}
}

// The list pages over stored rows only, filters on both keys, and orders newest
// first. (tahun, bulan) is unique, so the ordering is total — no row can appear on
// two pages while another is never returned.
func TestListPeriodeFilterDanUrutan(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	for _, bulan := range []int{1, 2, 3} {
		tutupPeriode(t, testApp, f.actor, 2025, bulan)
	}
	tutupPeriode(t, testApp, f.actor, 2026, 1)

	// Reopened, so this row exists and says BUKA — which is what makes the status
	// filter worth having at all.
	if _, err := testApp.periode.Buka(ctx(), &model.BukaPeriodeRequest{
		Tahun: 2025, Bulan: 2, ActorID: f.actor,
	}); err != nil {
		t.Fatalf("buka 2025-02: %v", err)
	}

	list, paging, err := testApp.periode.Search(ctx(), &model.ListPeriodeRequest{Tahun: 2025})
	if err != nil {
		t.Fatalf("list per tahun: %v", err)
	}

	if paging.TotalItem != 3 {
		t.Errorf("total_item = %d untuk tahun 2025, want 3", paging.TotalItem)
	}

	// Newest month first: the row an operator has just acted on is the one they are
	// looking for.
	if len(list) != 3 || list[0].Bulan != 3 || list[2].Bulan != 1 {
		t.Errorf("urutan bulan = %v, want 3,2,1", bulanDari(list))
	}

	tutup, _, err := testApp.periode.Search(ctx(), &model.ListPeriodeRequest{Status: "TUTUP"})
	if err != nil {
		t.Fatalf("list per status: %v", err)
	}

	for i := range tutup {
		if tutup[i].Status != "TUTUP" {
			t.Errorf("periode %d-%d berstatus %q di filter TUTUP", tutup[i].Tahun, tutup[i].Bulan, tutup[i].Status)
		}
	}

	if len(tutup) != 3 {
		t.Errorf("baris TUTUP = %d, want 3 (2025-02 sudah dibuka lagi)", len(tutup))
	}
}

func bulanDari(list []model.PeriodeResponse) []int {
	bulan := make([]int, len(list))
	for i := range list {
		bulan[i] = list[i].Bulan
	}

	return bulan
}

func assertPesanTidakMemuat(t *testing.T, err error, potongan string) {
	t.Helper()

	var domainErr *model.Error
	if !errors.As(err, &domainErr) {
		t.Fatalf("expected *model.Error, got %T: %v", err, err)
	}

	if strings.Contains(domainErr.Message, potongan) {
		t.Errorf("message = %q, want it NOT to contain %q", domainErr.Message, potongan)
	}
}
