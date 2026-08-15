package usecase_test

// Pemakaian internal (isu #9) — the fifth document to write kartu_stok, and the
// first to take goods out with no counterparty at all. These run against a real
// PostgreSQL because most of what they assert lives there: the negative-stock
// guard, the closed-period trigger, the append-only ledger, and the advisory lock
// per (barang, ruang) that two concurrent postings for the same product would
// otherwise deadlock on.

import (
	"fmt"
	"sync"
	"testing"

	"Arthafreestyle/ERP/internal/model"
)

// pemakaianSetup adds a requester and an approver to the purchase fixture — two
// distinct users, since pemakaian_penyetuju_check forbids a requester approving
// their own request — plus a second product, for the tests that need more than one
// line to expose the ABBA between two concurrent postings.
type pemakaianSetup struct {
	fixture
	pemohon   int64
	penyetuju int64
	productB  int64
}

func buatUserPolos(t *testing.T, testApp *app, username string) int64 {
	t.Helper()

	user, err := testApp.user.Create(ctx(), &model.CreateUserRequest{
		Username: username, Password: "rahasia123",
	})
	if err != nil {
		t.Fatalf("create user %s: %v", username, err)
	}

	return user.ID
}

// stokAwalPemakaian posts a purchase of qty pcs of two products at 10.000 each into
// f.ruang, and returns a setup with a requester and an approver ready.
func stokAwalPemakaian(t *testing.T, testApp *app, qty string) (*app, pemakaianSetup) {
	t.Helper()

	f := pembelianFixture(t, testApp)

	productB, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID:       f.actor,
		KodeBarang:    "BRG-002",
		Nama:          "Tinta Printer",
		IDSatuanDasar: f.pcs,
	})
	if err != nil {
		t.Fatalf("create product B: %v", err)
	}

	beli, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
		ActorID:    f.actor,
		Tanggal:    "2026-08-11",
		IDSupplier: f.supplier,
		IDRuang:    f.ruang,
		Detail: []model.PembelianDetailRequest{
			{IDProduct: f.product, IDSatuanInput: f.pcs, QtyFaktur: qty, HargaSatuanInput: "10000"},
			{IDProduct: productB.ID, IDSatuanInput: f.pcs, QtyFaktur: qty, HargaSatuanInput: "10000"},
		},
	})
	if err != nil {
		t.Fatalf("create pembelian: %v", err)
	}

	ajukanDanPosting(t, testApp, f, beli.ID)

	return testApp, pemakaianSetup{
		fixture:   f,
		pemohon:   buatUserPolos(t, testApp, "pemohon1"),
		penyetuju: buatUserPolos(t, testApp, "penyetuju1"),
		productB:  productB.ID,
	}
}

// buatPemakaian opens a DRAFT request for one line of s.product.
func buatPemakaian(t *testing.T, testApp *app, s pemakaianSetup, qty string) *model.PemakaianResponse {
	t.Helper()

	pemakaian, err := testApp.pemakaian.Create(ctx(), &model.CreatePemakaianRequest{
		ActorID:   s.actor,
		Tanggal:   "2026-08-15",
		IDRuang:   s.ruang,
		IDPemohon: s.pemohon,
		Keperluan: "dipakai perbaikan",
		Detail: []model.PemakaianDetailRequest{{
			IDProduct: s.product, IDSatuanInput: s.pcs, QtyInput: qty,
		}},
	})
	if err != nil {
		t.Fatalf("create pemakaian: %v", err)
	}

	return pemakaian
}

// ajukanPemakaian moves a DRAFT to DIAJUKAN.
func ajukanPemakaian(t *testing.T, testApp *app, s pemakaianSetup, id int64) *model.PemakaianResponse {
	t.Helper()

	diajukan, err := testApp.pemakaian.Ajukan(ctx(), &model.AjukanPemakaianRequest{
		ID: id, ActorID: s.actor,
	})
	if err != nil {
		t.Fatalf("ajukan pemakaian: %v", err)
	}

	return diajukan
}

// setujuiPenuh approves a DIAJUKAN request with every line granted in full — the
// ordinary case, where nothing is trimmed.
func setujuiPenuh(t *testing.T, testApp *app, s pemakaianSetup, id int64) *model.PemakaianResponse {
	t.Helper()

	disetujui, err := testApp.pemakaian.Setujui(ctx(), &model.SetujuiPemakaianRequest{
		ID: id, ActorID: s.penyetuju,
	})
	if err != nil {
		t.Fatalf("setujui pemakaian: %v", err)
	}

	return disetujui
}

func postingPemakaian(t *testing.T, testApp *app, s pemakaianSetup, id int64) *model.PemakaianResponse {
	t.Helper()

	posted, err := testApp.pemakaian.Posting(ctx(), &model.PostingPemakaianRequest{
		ID: id, ActorID: s.actor,
	})
	if err != nil {
		t.Fatalf("posting pemakaian: %v", err)
	}

	return posted
}

// The trap the issue calls the most expensive one to get wrong: only
// qty_disetujui_dasar reaches the ledger, never qty_dasar. The approver trims 60 of
// the 100 requested pieces, and posting must take out exactly 60.
func TestPemakaianSetujuiMemangkasQtyYangDiposting(t *testing.T) {
	testApp, s := stokAwalPemakaian(t, newApp(t), "100")

	pemakaian := buatPemakaian(t, testApp, s, "100")
	ajukanPemakaian(t, testApp, s, pemakaian.ID)

	disetujui, err := testApp.pemakaian.Setujui(ctx(), &model.SetujuiPemakaianRequest{
		ID: pemakaian.ID, ActorID: s.penyetuju,
		Detail: []model.SetujuiPemakaianDetailRequest{
			{IDDetail: pemakaian.Detail[0].ID, QtyDisetujuiDasar: 60},
		},
	})
	if err != nil {
		t.Fatalf("setujui dengan pemangkasan: %v", err)
	}

	if got := *disetujui.Detail[0].QtyDisetujuiDasar; got != 60 {
		t.Fatalf("qty_disetujui_dasar = %d, want 60", got)
	}

	// qty_dasar (the request) is untouched — it is planning data and must survive the
	// approval unchanged, so the trail of what was originally asked for is not erased.
	if disetujui.Detail[0].QtyDasar != 100 {
		t.Errorf("qty_dasar = %d setelah disetujui, want tetap 100", disetujui.Detail[0].QtyDasar)
	}

	posted := postingPemakaian(t, testApp, s, pemakaian.ID)

	if stok, _ := saldoStok(t, s.product, s.ruang); stok != 40 {
		t.Errorf("stok ruang = %d setelah posting, want 40 (100 - 60)", stok)
	}

	if posted.TotalHPP == nil || *posted.TotalHPP != "600000.00" {
		t.Errorf("total_hpp = %v, want 600000.00 (60 x 10.000)", posted.TotalHPP)
	}
}

// A line approved at zero is skipped at posting, not inserted as a kartu_stok row
// moving nothing — the table must never carry a movement of zero.
func TestPemakaianBarisDisetujuiNolDilewatiSaatPosting(t *testing.T) {
	testApp, s := stokAwalPemakaian(t, newApp(t), "100")

	pemakaian, err := testApp.pemakaian.Create(ctx(), &model.CreatePemakaianRequest{
		ActorID: s.actor, Tanggal: "2026-08-15", IDRuang: s.ruang, IDPemohon: s.pemohon,
		Keperluan: "dua barang, satu ditolak",
		Detail: []model.PemakaianDetailRequest{
			{IDProduct: s.product, IDSatuanInput: s.pcs, QtyInput: "10"},
			{IDProduct: s.productB, IDSatuanInput: s.pcs, QtyInput: "5"},
		},
	})
	if err != nil {
		t.Fatalf("create pemakaian dua baris: %v", err)
	}

	ajukanPemakaian(t, testApp, s, pemakaian.ID)

	// The second line is refused on its own even though the document as a whole is
	// approved.
	disetujui, err := testApp.pemakaian.Setujui(ctx(), &model.SetujuiPemakaianRequest{
		ID: pemakaian.ID, ActorID: s.penyetuju,
		Detail: []model.SetujuiPemakaianDetailRequest{
			{IDDetail: pemakaian.Detail[1].ID, QtyDisetujuiDasar: 0},
		},
	})
	if err != nil {
		t.Fatalf("setujui dengan satu baris nol: %v", err)
	}

	if got := *disetujui.Detail[1].QtyDisetujuiDasar; got != 0 {
		t.Fatalf("qty_disetujui_dasar baris kedua = %d, want 0", got)
	}

	postingPemakaian(t, testApp, s, pemakaian.ID)

	var barisB int
	if err := testDB.QueryRow(
		`SELECT COUNT(*) FROM kartu_stok WHERE ref_table = 'pemakaian' AND ref_id_transaksi = $1 AND id_barang = $2`,
		pemakaian.ID, s.productB,
	).Scan(&barisB); err != nil {
		t.Fatalf("hitung baris kartu stok produk B: %v", err)
	}

	if barisB != 0 {
		t.Errorf("baris kartu_stok untuk produk yang disetujui nol = %d, want 0", barisB)
	}

	if stok, _ := saldoStok(t, s.productB, s.ruang); stok != 100 {
		t.Errorf("stok produk B = %d, want tetap 100 (tidak terpakai)", stok)
	}

	if stok, _ := saldoStok(t, s.product, s.ruang); stok != 90 {
		t.Errorf("stok produk A = %d, want 90 (100 - 10)", stok)
	}
}

// A submission approved with every line at zero has nothing left to post — that is
// a rejection, not an internal usage, and posting refuses it outright.
func TestPemakaianSemuaBarisNolDitolakSaatPosting(t *testing.T) {
	testApp, s := stokAwalPemakaian(t, newApp(t), "100")

	pemakaian := buatPemakaian(t, testApp, s, "10")
	ajukanPemakaian(t, testApp, s, pemakaian.ID)

	if _, err := testApp.pemakaian.Setujui(ctx(), &model.SetujuiPemakaianRequest{
		ID: pemakaian.ID, ActorID: s.penyetuju,
		Detail: []model.SetujuiPemakaianDetailRequest{
			{IDDetail: pemakaian.Detail[0].ID, QtyDisetujuiDasar: 0},
		},
	}); err != nil {
		t.Fatalf("setujui semua nol: %v", err)
	}

	_, err := testApp.pemakaian.Posting(ctx(), &model.PostingPemakaianRequest{
		ID: pemakaian.ID, ActorID: s.actor,
	})
	assertKind(t, err, model.KindInvalid)

	var baris int
	if err := testDB.QueryRow(
		`SELECT COUNT(*) FROM kartu_stok WHERE ref_table = 'pemakaian' AND ref_id_transaksi = $1`,
		pemakaian.ID,
	).Scan(&baris); err != nil {
		t.Fatalf("hitung baris kartu stok: %v", err)
	}

	if baris != 0 {
		t.Errorf("baris kartu_stok = %d setelah posting ditolak, want 0", baris)
	}
}

// More than the room holds is refused, and the message names the product and the
// room — this is the second module after mutasi where a negative-stock rejection is
// an everyday occurrence rather than a theoretical defence.
func TestPemakaianDitolakSaatSaldoTidakCukup(t *testing.T) {
	testApp, s := stokAwalPemakaian(t, newApp(t), "10")

	pemakaian := buatPemakaian(t, testApp, s, "11")
	ajukanPemakaian(t, testApp, s, pemakaian.ID)
	setujuiPenuh(t, testApp, s, pemakaian.ID)

	_, err := testApp.pemakaian.Posting(ctx(), &model.PostingPemakaianRequest{
		ID: pemakaian.ID, ActorID: s.actor,
	})
	assertKind(t, err, model.KindInvalid)
	assertPesanMemuat(t, err, "Kertas A4")
	assertPesanMemuat(t, err, fmt.Sprintf("ruang %d", s.ruang))

	if stok, _ := saldoStok(t, s.product, s.ruang); stok != 10 {
		t.Errorf("stok = %d setelah posting ditolak, want tetap 10", stok)
	}
}

// Posting into a closed month is refused and the refusal names the month, exactly
// as every other stock-writing module does.
func TestPostingPemakaianKePeriodeTutupMenyebutPeriodenya(t *testing.T) {
	testApp, s := stokAwalPemakaian(t, newApp(t), "100")

	lalu := awalBulanLalu(t)

	pemakaian, err := testApp.pemakaian.Create(ctx(), &model.CreatePemakaianRequest{
		ActorID: s.actor, Tanggal: lalu.Format("2006-01-02"), IDRuang: s.ruang,
		IDPemohon: s.pemohon, Keperluan: "perbaikan",
		Detail: []model.PemakaianDetailRequest{{
			IDProduct: s.product, IDSatuanInput: s.pcs, QtyInput: "10",
		}},
	})
	if err != nil {
		t.Fatalf("create pemakaian bertanggal lalu: %v", err)
	}

	ajukanPemakaian(t, testApp, s, pemakaian.ID)
	setujuiPenuh(t, testApp, s, pemakaian.ID)
	tutupPeriode(t, testApp, s.actor, lalu.Year(), int(lalu.Month()))

	_, err = testApp.pemakaian.Posting(ctx(), &model.PostingPemakaianRequest{
		ID: pemakaian.ID, ActorID: s.actor,
	})
	assertKind(t, err, model.KindInvalid)
	assertPesanMemuat(t, err, fmt.Sprintf("%04d-%02d", lalu.Year(), int(lalu.Month())))

	if stok, _ := saldoStok(t, s.product, s.ruang); stok != 100 {
		t.Errorf("stok = %d setelah posting ditolak, want tetap 100", stok)
	}
}

// A requester may not approve their own request. pemakaian_penyetuju_check
// guarantees this at the database too, but the message here is what names the
// reason instead of arriving as an unlabelled CHECK violation.
func TestPemakaianPemohonTidakBisaMenyetujuiSendiri(t *testing.T) {
	testApp, s := stokAwalPemakaian(t, newApp(t), "100")

	pemakaian := buatPemakaian(t, testApp, s, "10")
	ajukanPemakaian(t, testApp, s, pemakaian.ID)

	_, err := testApp.pemakaian.Setujui(ctx(), &model.SetujuiPemakaianRequest{
		ID: pemakaian.ID, ActorID: s.pemohon,
	})
	assertKind(t, err, model.KindInvalid)
	assertPesanMemuat(t, err, "pemohon")

	// And the same rule holds for a rejection: refusing your own request is not a
	// safer version of approving it.
	_, err = testApp.pemakaian.Tolak(ctx(), &model.TolakPemakaianRequest{
		ID: pemakaian.ID, ActorID: s.pemohon, Alasan: "berubah pikiran",
	})
	assertKind(t, err, model.KindInvalid)
}

// DITOLAK is terminal, unlike pembelian's rejection which returns to DRAFT. A
// refused request stays refused; the requester submits a new one if the goods are
// still needed.
func TestPemakaianDitolakTidakKembaliKeDraft(t *testing.T) {
	testApp, s := stokAwalPemakaian(t, newApp(t), "100")

	pemakaian := buatPemakaian(t, testApp, s, "10")
	ajukanPemakaian(t, testApp, s, pemakaian.ID)

	ditolak, err := testApp.pemakaian.Tolak(ctx(), &model.TolakPemakaianRequest{
		ID: pemakaian.ID, ActorID: s.penyetuju, Alasan: "tidak ada anggaran",
	})
	if err != nil {
		t.Fatalf("tolak pemakaian: %v", err)
	}

	if ditolak.Status != "DITOLAK" {
		t.Fatalf("status = %q, want DITOLAK", ditolak.Status)
	}

	if ditolak.CatatanPersetujuan == nil || *ditolak.CatatanPersetujuan != "tidak ada anggaran" {
		t.Errorf("catatan_persetujuan = %v, want %q", ditolak.CatatanPersetujuan, "tidak ada anggaran")
	}

	// Terminal: submitting it again is refused, because it is no longer DRAFT.
	_, err = testApp.pemakaian.Ajukan(ctx(), &model.AjukanPemakaianRequest{
		ID: pemakaian.ID, ActorID: s.actor,
	})
	assertKind(t, err, model.KindConflict)
}

// Voiding a posted request returns the goods to the room they left from, and the
// closed-period reversal-lands-in-the-current-month rule applies here exactly as it
// does for every other stock writer.
func TestBatalPemakaianMengembalikanStok(t *testing.T) {
	testApp, s := stokAwalPemakaian(t, newApp(t), "100")

	pemakaian := buatPemakaian(t, testApp, s, "30")
	ajukanPemakaian(t, testApp, s, pemakaian.ID)
	setujuiPenuh(t, testApp, s, pemakaian.ID)
	postingPemakaian(t, testApp, s, pemakaian.ID)

	if stok, _ := saldoStok(t, s.product, s.ruang); stok != 70 {
		t.Fatalf("stok setelah posting = %d, want 70", stok)
	}

	dibatalkan, err := testApp.pemakaian.Batal(ctx(), &model.BatalPemakaianRequest{
		ID: pemakaian.ID, ActorID: s.actor, AlasanBatal: "salah input",
	})
	if err != nil {
		t.Fatalf("batal pemakaian: %v", err)
	}

	if dibatalkan.Status != "BATAL" {
		t.Fatalf("status = %q, want BATAL", dibatalkan.Status)
	}

	if stok, _ := saldoStok(t, s.product, s.ruang); stok != 100 {
		t.Errorf("stok setelah batal = %d, want kembali 100", stok)
	}
}

// Two pemakaian documents naming the same two products in reversed line order,
// posted at the same moment, must not deadlock — the ABBA the issue calls out
// explicitly, and it can happen inside a single room because the trigger takes one
// advisory lock per insert rather than one per document.
//
// Against the fixed code (KunciSaldo taken before the first insert, in a canonical
// order) this passes every time. Without the pre-lock it fails intermittently,
// which is the honest description of the bug the test exists to catch.
func TestDuaPemakaianBersamaanProdukSamaTidakDeadlock(t *testing.T) {
	testApp, s := stokAwalPemakaian(t, newApp(t), "200")

	const ronde = 8

	ids := make([]int64, 0, ronde*2)

	buat := func(urutan [2]int64) int64 {
		pemakaian, err := testApp.pemakaian.Create(ctx(), &model.CreatePemakaianRequest{
			ActorID: s.actor, Tanggal: "2026-08-20", IDRuang: s.ruang,
			IDPemohon: s.pemohon, Keperluan: "pemakaian rutin",
			Detail: []model.PemakaianDetailRequest{
				{IDProduct: urutan[0], IDSatuanInput: s.pcs, QtyInput: "1"},
				{IDProduct: urutan[1], IDSatuanInput: s.pcs, QtyInput: "1"},
			},
		})
		if err != nil {
			t.Fatalf("create pemakaian: %v", err)
		}

		ajukanPemakaian(t, testApp, s, pemakaian.ID)
		setujuiPenuh(t, testApp, s, pemakaian.ID)

		return pemakaian.ID
	}

	for range ronde {
		// One document lists A then B; the other lists B then A. Each inserts its own
		// two kartu_stok rows in line order, so the two transactions take the
		// (barang, ruang) advisory lock in opposite order unless KunciSaldo sorts them
		// first.
		ids = append(ids, buat([2]int64{s.product, s.productB}))
		ids = append(ids, buat([2]int64{s.productB, s.product}))
	}

	mulai := make(chan struct{})
	gagal := make(chan error, len(ids))

	var wg sync.WaitGroup

	for _, id := range ids {
		wg.Add(1)

		go func(id int64) {
			defer wg.Done()

			<-mulai

			if _, err := testApp.pemakaian.Posting(ctx(), &model.PostingPemakaianRequest{
				ID: id, ActorID: s.actor,
			}); err != nil {
				gagal <- fmt.Errorf("posting pemakaian %d: %w", id, err)
			}
		}(id)
	}

	close(mulai)
	wg.Wait()
	close(gagal)

	for err := range gagal {
		t.Errorf("%v", err)
	}

	// ronde*2 documents, one piece of each product apiece: 2*ronde consumed from a
	// starting balance of 200.
	if stok, _ := saldoStok(t, s.product, s.ruang); stok != 200-int64(ronde*2) {
		t.Errorf("stok produk A = %d, want %d", stok, 200-int64(ronde*2))
	}

	if stok, _ := saldoStok(t, s.productB, s.ruang); stok != 200-int64(ronde*2) {
		t.Errorf("stok produk B = %d, want %d", stok, 200-int64(ronde*2))
	}
}

// The approval queue: status=DIAJUKAN with terlama_dulu=true is what an approver
// works through, oldest submission first — the same shape GET /supplier/{id}/utang
// uses.
func TestDaftarPemakaianDiajukanBisaDiurutTerlamaDulu(t *testing.T) {
	testApp, s := stokAwalPemakaian(t, newApp(t), "100")

	for _, tanggal := range []string{"2026-08-13", "2026-08-11", "2026-08-12"} {
		pemakaian, err := testApp.pemakaian.Create(ctx(), &model.CreatePemakaianRequest{
			ActorID: s.actor, Tanggal: tanggal, IDRuang: s.ruang,
			IDPemohon: s.pemohon, Keperluan: "rutin",
			Detail: []model.PemakaianDetailRequest{{
				IDProduct: s.product, IDSatuanInput: s.pcs, QtyInput: "1",
			}},
		})
		if err != nil {
			t.Fatalf("create pemakaian %s: %v", tanggal, err)
		}

		ajukanPemakaian(t, testApp, s, pemakaian.ID)
	}

	antre, _, err := testApp.pemakaian.Search(ctx(), &model.ListPemakaianRequest{
		Status: "DIAJUKAN", TerlamaDulu: true,
	})
	if err != nil {
		t.Fatalf("search pemakaian: %v", err)
	}

	if len(antre) != 3 {
		t.Fatalf("antrean = %d dokumen, want 3", len(antre))
	}

	for i, want := range []string{"2026-08-11", "2026-08-12", "2026-08-13"} {
		if got := antre[i].Tanggal.Format("2006-01-02"); got != want {
			t.Errorf("antrean[%d] bertanggal %s, want %s", i, got, want)
		}
	}
}
