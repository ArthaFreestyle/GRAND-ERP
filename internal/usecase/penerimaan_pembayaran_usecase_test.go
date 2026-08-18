package usecase_test

// penerimaan_pembayaran (isu #20) — the receivable-side mirror of pembayaran_utang,
// money flowing the other way. Same reasoning as that file: the arithmetic that has
// to hold across three documents at once lives in the database — the effective-
// allocation predicate with its giro rule, the status_pembayaran cache, the row
// locks that serialise two payments over one nota — so it is exercised there.

import (
	"testing"

	"Arthafreestyle/ERP/internal/model"
)

// piutangFixture posts a KREDIT sale of qty pcs at 15.000, so the nota total is
// readable by eye, and returns the customer alongside it.
func piutangFixture(t *testing.T, testApp *app, qty string) (penjualanSetup, *model.PenjualanResponse) {
	t.Helper()

	_, s := stokAwalPenjualan(t, testApp, "1000")

	penjualan, err := testApp.penjualan.Create(ctx(), &model.CreatePenjualanRequest{
		ActorID: s.actor, Tanggal: "2026-08-15", IDRuang: s.ruang,
		IDPelanggan: &s.pelanggan, JenisPembayaran: "KREDIT",
		Detail: []model.PenjualanDetailRequest{{
			IDProduct: s.product, IDSatuanInput: s.pcs, QtyInput: qty, HargaSatuanInput: "15000",
		}},
	})
	if err != nil {
		t.Fatalf("create penjualan kredit: %v", err)
	}

	posted := postingPenjualan(t, testApp, s, penjualan.ID)

	return s, posted
}

// terima opens and posts a cash payment against one nota.
func terima(t *testing.T, testApp *app, s penjualanSetup, idPenjualan int64, jumlah string) *model.PenerimaanPembayaranResponse {
	t.Helper()

	pembayaran, err := testApp.penerimaan.Create(ctx(), &model.CreatePenerimaanPembayaranRequest{
		ActorID:     s.actor,
		IDPelanggan: s.pelanggan,
		Tanggal:     "2026-08-20",
		Metode:      "TUNAI",
		Jumlah:      jumlah,
		Alokasi: []model.PembayaranAlokasiRequest{{
			IDPenjualan: idPenjualan, Jumlah: jumlah,
		}},
	})
	if err != nil {
		t.Fatalf("create penerimaan pembayaran: %v", err)
	}

	posted, err := testApp.penerimaan.Posting(ctx(), &model.PostingPenerimaanPembayaranRequest{
		ID: pembayaran.ID, ActorID: s.actor,
	})
	if err != nil {
		t.Fatalf("posting penerimaan pembayaran: %v", err)
	}

	return posted
}

// statusPiutang reads the nota's cache.
func statusPiutang(t *testing.T, testApp *app, idPenjualan int64) *model.PenjualanResponse {
	t.Helper()

	penjualan, err := testApp.penjualan.Get(ctx(), &model.GetPenjualanRequest{ID: idPenjualan})
	if err != nil {
		t.Fatalf("get penjualan: %v", err)
	}

	return penjualan
}

// The cache and the arithmetic behind it, across a part payment and then the rest.
func TestPenerimaanMenggerakkanStatusPembayaran(t *testing.T) {
	testApp := newApp(t)
	s, penjualan := piutangFixture(t, testApp, "100")

	if penjualan.Total != "1500000.00" {
		t.Fatalf("total = %s, want 1500000.00", penjualan.Total)
	}

	awal := statusPiutang(t, testApp, penjualan.ID)
	if awal.StatusPembayaran != "BELUM" {
		t.Fatalf("awal: status = %q, want BELUM", awal.StatusPembayaran)
	}

	terima(t, testApp, s, penjualan.ID, "600000")

	sebagian := statusPiutang(t, testApp, penjualan.ID)
	if sebagian.StatusPembayaran != "SEBAGIAN" {
		t.Errorf("setelah bayar sebagian: status = %q, want SEBAGIAN", sebagian.StatusPembayaran)
	}

	terima(t, testApp, s, penjualan.ID, "900000")

	lunas := statusPiutang(t, testApp, penjualan.ID)
	if lunas.StatusPembayaran != "LUNAS" {
		t.Errorf("setelah lunas: status = %q, want LUNAS", lunas.StatusPembayaran)
	}
}

// A nota may receive at most what it still has outstanding, and two payments have
// to share one balance rather than each getting the whole of it.
func TestAlokasiTidakBolehMelebihiSisaPiutang(t *testing.T) {
	testApp := newApp(t)
	s, penjualan := piutangFixture(t, testApp, "100")

	buat := func(jumlah string) error {
		_, err := testApp.penerimaan.Create(ctx(), &model.CreatePenerimaanPembayaranRequest{
			ActorID:     s.actor,
			IDPelanggan: s.pelanggan,
			Tanggal:     "2026-08-20",
			Metode:      "TRANSFER",
			Jumlah:      jumlah,
			Alokasi: []model.PembayaranAlokasiRequest{{
				IDPenjualan: penjualan.ID, Jumlah: jumlah,
			}},
		})

		return err
	}

	assertKind(t, buat("1500001"), model.KindInvalid)

	terima(t, testApp, s, penjualan.ID, "1000000")

	assertKind(t, buat("500001"), model.KindInvalid)

	if err := buat("500000"); err != nil {
		t.Errorf("alokasi 500000 dari sisa 500000 ditolak: %v", err)
	}
}

// A payment may not be allocated beyond its own amount — a different ceiling from
// the nota's, and a request can satisfy either one and fail the other.
//
// Under-allocation is not refused: the remainder is a credit sitting with the
// customer.
func TestAlokasiPenerimaanTidakBolehMelebihiJumlahPembayaran(t *testing.T) {
	testApp := newApp(t)
	s, penjualan := piutangFixture(t, testApp, "100")

	kedua, err := testApp.penjualan.Create(ctx(), &model.CreatePenjualanRequest{
		ActorID: s.actor, Tanggal: "2026-08-15", IDRuang: s.ruang,
		IDPelanggan: &s.pelanggan, JenisPembayaran: "KREDIT",
		Detail: []model.PenjualanDetailRequest{{
			IDProduct: s.product, IDSatuanInput: s.pcs, QtyInput: "50", HargaSatuanInput: "15000",
		}},
	})
	if err != nil {
		t.Fatalf("create penjualan kedua: %v", err)
	}

	lain := postingPenjualan(t, testApp, s, kedua.ID)

	// 500.000 diterima, dipecah 300.000 + 300.000 ke dua nota yang masing-masing
	// sanggup menampungnya sendiri, tapi bersama melebihi jumlah yang diterima.
	_, err = testApp.penerimaan.Create(ctx(), &model.CreatePenerimaanPembayaranRequest{
		ActorID:     s.actor,
		IDPelanggan: s.pelanggan,
		Tanggal:     "2026-08-20",
		Metode:      "TRANSFER",
		Jumlah:      "500000",
		Alokasi: []model.PembayaranAlokasiRequest{
			{IDPenjualan: penjualan.ID, Jumlah: "300000"},
			{IDPenjualan: lain.ID, Jumlah: "300000"},
		},
	})
	assertKind(t, err, model.KindInvalid)

	// Under-allocating is fine, and the remainder is reported.
	kredit, err := testApp.penerimaan.Create(ctx(), &model.CreatePenerimaanPembayaranRequest{
		ActorID:     s.actor,
		IDPelanggan: s.pelanggan,
		Tanggal:     "2026-08-20",
		Metode:      "TRANSFER",
		Jumlah:      "500000",
		Alokasi: []model.PembayaranAlokasiRequest{
			{IDPenjualan: penjualan.ID, Jumlah: "200000"},
		},
	})
	if err != nil {
		t.Fatalf("pembayaran dengan sisa titipan ditolak: %v", err)
	}

	if kredit.JumlahDialokasikan != "200000.00" || kredit.SisaBelumDialokasikan != "300000.00" {
		t.Errorf("dialokasikan %s sisa_belum_dialokasikan %s, want 200000.00 dan 300000.00",
			kredit.JumlahDialokasikan, kredit.SisaBelumDialokasikan)
	}

	// So is allocating nothing at all: money received before it is decided what it
	// covers.
	titipan, err := testApp.penerimaan.Create(ctx(), &model.CreatePenerimaanPembayaranRequest{
		ActorID:     s.actor,
		IDPelanggan: s.pelanggan,
		Tanggal:     "2026-08-20",
		Metode:      "TRANSFER",
		Jumlah:      "250000",
	})
	if err != nil {
		t.Fatalf("pembayaran tanpa alokasi ditolak: %v", err)
	}

	if titipan.JumlahDialokasikan != "0.00" || len(titipan.Alokasi) != 0 {
		t.Errorf("dialokasikan %s dengan %d alokasi, want 0.00 dan 0",
			titipan.JumlahDialokasikan, len(titipan.Alokasi))
	}
}

// **An uncashed giro is not a payment.** Posting one makes the document real and
// settles nothing; the receivable drops when it clears, and never if it bounces.
func TestGiroBelumCairTidakMengurangiPiutang(t *testing.T) {
	testApp := newApp(t)
	s, penjualan := piutangFixture(t, testApp, "100")

	giro, err := testApp.penerimaan.Create(ctx(), &model.CreatePenerimaanPembayaranRequest{
		ActorID:               s.actor,
		IDPelanggan:           s.pelanggan,
		Tanggal:               "2026-08-20",
		Metode:                "GIRO",
		NamaBank:              ptr("BCA"),
		NoReferensi:           ptr("GR-00123"),
		TanggalJatuhTempoGiro: ptr("2026-09-20"),
		Jumlah:                "1500000",
		Alokasi: []model.PembayaranAlokasiRequest{{
			IDPenjualan: penjualan.ID, Jumlah: "1500000",
		}},
	})
	if err != nil {
		t.Fatalf("create giro: %v", err)
	}

	if giro.StatusGiro == nil || *giro.StatusGiro != "BELUM_CAIR" {
		t.Fatalf("status_giro = %v, want BELUM_CAIR", giro.StatusGiro)
	}

	posted, err := testApp.penerimaan.Posting(ctx(), &model.PostingPenerimaanPembayaranRequest{
		ID: giro.ID, ActorID: s.actor,
	})
	if err != nil {
		t.Fatalf("posting giro: %v", err)
	}

	if posted.Status != "POSTED" || posted.JumlahDialokasikan != "1500000.00" {
		t.Fatalf("status %q dialokasikan %s, want POSTED dan 1500000.00",
			posted.Status, posted.JumlahDialokasikan)
	}

	belum := statusPiutang(t, testApp, penjualan.ID)
	if belum.StatusPembayaran != "BELUM" {
		t.Errorf("giro belum cair: status = %q, want BELUM", belum.StatusPembayaran)
	}

	// Clearing it is the moment the receivable drops.
	cair, err := testApp.penerimaan.CairkanGiro(ctx(), &model.CairkanGiroPelangganRequest{
		ID: giro.ID, ActorID: s.actor, TanggalCair: "2026-09-22",
	})
	if err != nil {
		t.Fatalf("cairkan giro: %v", err)
	}

	if cair.StatusGiro == nil || *cair.StatusGiro != "CAIR" || cair.TanggalCair == nil {
		t.Errorf("status_giro %v tanggal_cair %v, want CAIR dengan tanggal terisi",
			cair.StatusGiro, cair.TanggalCair)
	}

	lunas := statusPiutang(t, testApp, penjualan.ID)
	if lunas.StatusPembayaran != "LUNAS" {
		t.Errorf("setelah cair: status = %q, want LUNAS", lunas.StatusPembayaran)
	}

	// And it cannot clear twice.
	_, err = testApp.penerimaan.CairkanGiro(ctx(), &model.CairkanGiroPelangganRequest{
		ID: giro.ID, ActorID: s.actor, TanggalCair: "2026-09-23",
	})
	assertKind(t, err, model.KindConflict)
}

// A bounced giro never reduced anything, so nothing has to be given back — and the
// nota it was aimed at was never settled in the first place.
func TestGiroDitolakTidakPernahMengurangiPiutang(t *testing.T) {
	testApp := newApp(t)
	s, penjualan := piutangFixture(t, testApp, "100")

	giro, err := testApp.penerimaan.Create(ctx(), &model.CreatePenerimaanPembayaranRequest{
		ActorID:     s.actor,
		IDPelanggan: s.pelanggan,
		Tanggal:     "2026-08-20",
		Metode:      "GIRO",
		NamaBank:    ptr("BCA"),
		Jumlah:      "1500000",
		Alokasi: []model.PembayaranAlokasiRequest{{
			IDPenjualan: penjualan.ID, Jumlah: "1500000",
		}},
	})
	if err != nil {
		t.Fatalf("create giro: %v", err)
	}

	if _, err := testApp.penerimaan.Posting(ctx(), &model.PostingPenerimaanPembayaranRequest{
		ID: giro.ID, ActorID: s.actor,
	}); err != nil {
		t.Fatalf("posting giro: %v", err)
	}

	ditolak, err := testApp.penerimaan.TolakGiro(ctx(), &model.TolakGiroPelangganRequest{
		ID: giro.ID, ActorID: s.actor, Alasan: ptr("dana tidak cukup"),
	})
	if err != nil {
		t.Fatalf("tolak giro: %v", err)
	}

	if ditolak.StatusGiro == nil || *ditolak.StatusGiro != "TOLAK" {
		t.Errorf("status_giro = %v, want TOLAK", ditolak.StatusGiro)
	}

	if ditolak.TanggalCair != nil {
		t.Errorf("tanggal_cair = %v, want nil pada giro yang ditolak", ditolak.TanggalCair)
	}

	tetap := statusPiutang(t, testApp, penjualan.ID)
	if tetap.StatusPembayaran != "BELUM" {
		t.Errorf("setelah giro ditolak: status = %q, want BELUM", tetap.StatusPembayaran)
	}

	// A rejected giro cannot then be cleared.
	_, err = testApp.penerimaan.CairkanGiro(ctx(), &model.CairkanGiroPelangganRequest{
		ID: giro.ID, ActorID: s.actor, TanggalCair: "2026-09-22",
	})
	assertKind(t, err, model.KindConflict)

	// And the nota is still collectible by something else, for the full amount.
	terima(t, testApp, s, penjualan.ID, "1500000")

	lunas := statusPiutang(t, testApp, penjualan.ID)
	if lunas.StatusPembayaran != "LUNAS" {
		t.Errorf("status = %q, want LUNAS", lunas.StatusPembayaran)
	}
}

// A giro's allocations count for nothing while it is outstanding, so the nota can
// be settled by something else in the meantime. That is a real sequence rather
// than a race — a giro can sit for weeks — and clearing it then has to fail.
func TestCairkanGiroDiperiksaUlangTerhadapSisaPiutang(t *testing.T) {
	testApp := newApp(t)
	s, penjualan := piutangFixture(t, testApp, "100")

	giro, err := testApp.penerimaan.Create(ctx(), &model.CreatePenerimaanPembayaranRequest{
		ActorID:     s.actor,
		IDPelanggan: s.pelanggan,
		Tanggal:     "2026-08-20",
		Metode:      "GIRO",
		NamaBank:    ptr("BCA"),
		Jumlah:      "1500000",
		Alokasi: []model.PembayaranAlokasiRequest{{
			IDPenjualan: penjualan.ID, Jumlah: "1500000",
		}},
	})
	if err != nil {
		t.Fatalf("create giro: %v", err)
	}

	if _, err := testApp.penerimaan.Posting(ctx(), &model.PostingPenerimaanPembayaranRequest{
		ID: giro.ID, ActorID: s.actor,
	}); err != nil {
		t.Fatalf("posting giro: %v", err)
	}

	// Pelanggan bayar tunai selagi giro masih beredar. Legal: giro tidak
	// mengurangi apa pun, jadi nota masih terbuka penuh.
	terima(t, testApp, s, penjualan.ID, "1500000")

	_, err = testApp.penerimaan.CairkanGiro(ctx(), &model.CairkanGiroPelangganRequest{
		ID: giro.ID, ActorID: s.actor, TanggalCair: "2026-09-22",
	})
	assertKind(t, err, model.KindInvalid)

	masih := statusPiutang(t, testApp, penjualan.ID)
	if masih.StatusPembayaran != "LUNAS" {
		t.Errorf("status = %q, want LUNAS", masih.StatusPembayaran)
	}
}

// Voiding a payment hands the nota back what it still has outstanding. Nothing is
// deleted, so what the payment claimed to settle stays readable next to
// alasan_batal.
func TestBatalPenerimaanMengembalikanPiutang(t *testing.T) {
	testApp := newApp(t)
	s, penjualan := piutangFixture(t, testApp, "100")

	pembayaran := terima(t, testApp, s, penjualan.ID, "1500000")

	if statusPiutang(t, testApp, penjualan.ID).StatusPembayaran != "LUNAS" {
		t.Fatal("nota belum LUNAS setelah dibayar penuh")
	}

	batal, err := testApp.penerimaan.Batal(ctx(), &model.BatalPenerimaanPembayaranRequest{
		ID: pembayaran.ID, ActorID: s.actor, AlasanBatal: "transfer ke rekening yang salah",
	})
	if err != nil {
		t.Fatalf("batal penerimaan pembayaran: %v", err)
	}

	if batal.Status != "BATAL" || batal.AlasanBatal == nil {
		t.Errorf("status %q alasan_batal %v, want BATAL dengan alasan terisi",
			batal.Status, batal.AlasanBatal)
	}

	// The allocation rows survive the void, which is what makes it auditable.
	if len(batal.Alokasi) != 1 {
		t.Errorf("alokasi = %d baris, want 1 (tidak dihapus)", len(batal.Alokasi))
	}

	lagi := statusPiutang(t, testApp, penjualan.ID)
	if lagi.StatusPembayaran != "BELUM" {
		t.Errorf("setelah batal: status = %q, want BELUM", lagi.StatusPembayaran)
	}

	// And the nota is collectible again, in full.
	terima(t, testApp, s, penjualan.ID, "1500000")

	if statusPiutang(t, testApp, penjualan.ID).StatusPembayaran != "LUNAS" {
		t.Error("nota tidak LUNAS setelah dibayar ulang")
	}
}

// A TUNAI nota never became a receivable — the money already changed hands at the
// counter — so it may not receive an allocation at all, and the message names the
// reason rather than reporting a balance that never existed.
func TestAlokasiMenolakNotaTunai(t *testing.T) {
	testApp, s := stokAwalPenjualan(t, newApp(t), "100")

	tunai := buatPenjualanTunai(t, testApp, s, "10", "15000")
	posted := postingPenjualan(t, testApp, s, tunai.ID)

	if posted.StatusPembayaran != "LUNAS" {
		t.Fatalf("nota tunai posted: status = %q, want LUNAS", posted.StatusPembayaran)
	}

	_, err := testApp.penerimaan.Create(ctx(), &model.CreatePenerimaanPembayaranRequest{
		ActorID:     s.actor,
		IDPelanggan: s.pelanggan,
		Tanggal:     "2026-08-20",
		Metode:      "TUNAI",
		Jumlah:      "150000",
		Alokasi: []model.PembayaranAlokasiRequest{{
			IDPenjualan: posted.ID, Jumlah: "150000",
		}},
	})
	assertKind(t, err, model.KindInvalid)
	assertPesanMemuat(t, err, "tidak pernah jadi piutang")
}

// Only a POSTED KREDIT nota belonging to this customer can be collected against. A
// DRAFT is a typed page rather than a receivable; a BATAL one was withdrawn;
// another customer's nota would make both ledgers wrong at once.
func TestAlokasiMenolakNotaYangTidakBolehDiterima(t *testing.T) {
	testApp := newApp(t)
	s, penjualan := piutangFixture(t, testApp, "100")

	draft, err := testApp.penjualan.Create(ctx(), &model.CreatePenjualanRequest{
		ActorID: s.actor, Tanggal: "2026-08-15", IDRuang: s.ruang,
		IDPelanggan: &s.pelanggan, JenisPembayaran: "KREDIT",
		Detail: []model.PenjualanDetailRequest{{
			IDProduct: s.product, IDSatuanInput: s.pcs, QtyInput: "5", HargaSatuanInput: "15000",
		}},
	})
	if err != nil {
		t.Fatalf("create draft penjualan: %v", err)
	}

	pelangganLain, err := testApp.pelanggan.Create(ctx(), &model.CreatePelangganRequest{ActorID: s.actor, Nama: "Toko Lain"})
	if err != nil {
		t.Fatalf("create pelanggan: %v", err)
	}

	buat := func(idPelanggan, idPenjualan int64) error {
		_, err := testApp.penerimaan.Create(ctx(), &model.CreatePenerimaanPembayaranRequest{
			ActorID:     s.actor,
			IDPelanggan: idPelanggan,
			Tanggal:     "2026-08-20",
			Metode:      "TUNAI",
			Jumlah:      "50000",
			Alokasi: []model.PembayaranAlokasiRequest{{
				IDPenjualan: idPenjualan, Jumlah: "50000",
			}},
		})

		return err
	}

	// A DRAFT nota.
	assertKind(t, buat(s.pelanggan, draft.ID), model.KindInvalid)

	// This customer's payment aimed at a nota belonging to nobody it deals with.
	assertKind(t, buat(pelangganLain.ID, penjualan.ID), model.KindInvalid)

	// The same nota twice in one payment: each row would pass the balance check
	// alone and together exceed it.
	_, err = testApp.penerimaan.Create(ctx(), &model.CreatePenerimaanPembayaranRequest{
		ActorID:     s.actor,
		IDPelanggan: s.pelanggan,
		Tanggal:     "2026-08-20",
		Metode:      "TUNAI",
		Jumlah:      "100000",
		Alokasi: []model.PembayaranAlokasiRequest{
			{IDPenjualan: penjualan.ID, Jumlah: "50000"},
			{IDPenjualan: penjualan.ID, Jumlah: "50000"},
		},
	})
	assertKind(t, err, model.KindInvalid)
}

// The balance check that counts runs at posting, under the nota's row lock. The
// one at draft time only produces a friendlier error sooner: two drafts can both
// aim at the same balance, and the second must fail when it tries to take what the
// first already took.
func TestSisaPiutangDiperiksaUlangSaatPosting(t *testing.T) {
	testApp := newApp(t)
	s, penjualan := piutangFixture(t, testApp, "100")

	// Two drafts, each claiming 1.200.000 of the 1.500.000. Both are legal as
	// drafts — a draft settles nothing, so neither reduces the balance.
	var draft [2]*model.PenerimaanPembayaranResponse
	for i := range draft {
		d, err := testApp.penerimaan.Create(ctx(), &model.CreatePenerimaanPembayaranRequest{
			ActorID:     s.actor,
			IDPelanggan: s.pelanggan,
			Tanggal:     "2026-08-20",
			Metode:      "TRANSFER",
			Jumlah:      "1200000",
			Alokasi: []model.PembayaranAlokasiRequest{{
				IDPenjualan: penjualan.ID, Jumlah: "1200000",
			}},
		})
		if err != nil {
			t.Fatalf("draft %d: %v", i+1, err)
		}

		draft[i] = d
	}

	if _, err := testApp.penerimaan.Posting(ctx(), &model.PostingPenerimaanPembayaranRequest{
		ID: draft[0].ID, ActorID: s.actor,
	}); err != nil {
		t.Fatalf("posting draft pertama: %v", err)
	}

	// Only 300.000 tersisa, jadi draft kedua tidak bisa mengambil 1.200.000-nya.
	_, err := testApp.penerimaan.Posting(ctx(), &model.PostingPenerimaanPembayaranRequest{
		ID: draft[1].ID, ActorID: s.actor,
	})
	assertKind(t, err, model.KindInvalid)

	lagi := statusPiutang(t, testApp, penjualan.ID)
	if lagi.StatusPembayaran != "SEBAGIAN" {
		t.Errorf("status = %q, want SEBAGIAN", lagi.StatusPembayaran)
	}
}

// Cancelling a nota is refused while a POSTED payment points at it: the money
// arrived, and where it should go instead is a decision rather than an arithmetic
// step.
func TestPenjualanTidakBisaDibatalkanSaatSudahDibayar(t *testing.T) {
	testApp := newApp(t)
	s, penjualan := piutangFixture(t, testApp, "100")

	pembayaran := terima(t, testApp, s, penjualan.ID, "600000")

	_, err := testApp.penjualan.Batal(ctx(), &model.BatalPenjualanRequest{
		ID: penjualan.ID, ActorID: s.actor, AlasanBatal: "salah pelanggan",
	})
	assertKind(t, err, model.KindConflict)

	// Voiding the payment first clears the way.
	if _, err := testApp.penerimaan.Batal(ctx(), &model.BatalPenerimaanPembayaranRequest{
		ID: pembayaran.ID, ActorID: s.actor, AlasanBatal: "ikut dibatalkan",
	}); err != nil {
		t.Fatalf("batal penerimaan pembayaran: %v", err)
	}

	if _, err := testApp.penjualan.Batal(ctx(), &model.BatalPenjualanRequest{
		ID: penjualan.ID, ActorID: s.actor, AlasanBatal: "salah pelanggan",
	}); err != nil {
		t.Fatalf("batal penjualan setelah pembayaran dibatalkan: %v", err)
	}
}

// An uncashed giro reduces no receivable but is still paper pointed at the nota, so
// it blocks cancellation too — otherwise it would be unexplainable when it clears.
func TestPenjualanTidakBisaDibatalkanSaatAdaGiroBelumCair(t *testing.T) {
	testApp := newApp(t)
	s, penjualan := piutangFixture(t, testApp, "100")

	giro, err := testApp.penerimaan.Create(ctx(), &model.CreatePenerimaanPembayaranRequest{
		ActorID:     s.actor,
		IDPelanggan: s.pelanggan,
		Tanggal:     "2026-08-20",
		Metode:      "GIRO",
		NamaBank:    ptr("BCA"),
		Jumlah:      "1500000",
		Alokasi: []model.PembayaranAlokasiRequest{{
			IDPenjualan: penjualan.ID, Jumlah: "1500000",
		}},
	})
	if err != nil {
		t.Fatalf("create giro: %v", err)
	}

	if _, err := testApp.penerimaan.Posting(ctx(), &model.PostingPenerimaanPembayaranRequest{
		ID: giro.ID, ActorID: s.actor,
	}); err != nil {
		t.Fatalf("posting giro: %v", err)
	}

	if statusPiutang(t, testApp, penjualan.ID).StatusPembayaran != "BELUM" {
		t.Fatal("giro belum cair seharusnya tidak mengubah status_pembayaran")
	}

	_, err = testApp.penjualan.Batal(ctx(), &model.BatalPenjualanRequest{
		ID: penjualan.ID, ActorID: s.actor, AlasanBatal: "salah pelanggan",
	})
	assertKind(t, err, model.KindConflict)
}

// The state machine and the number series. There is no DIAJUKAN, deliberately.
func TestAlurPenerimaanPembayaran(t *testing.T) {
	testApp := newApp(t)
	s, penjualan := piutangFixture(t, testApp, "100")

	pembayaran, err := testApp.penerimaan.Create(ctx(), &model.CreatePenerimaanPembayaranRequest{
		ActorID:     s.actor,
		IDPelanggan: s.pelanggan,
		Tanggal:     "2026-08-20",
		Metode:      "TRANSFER",
		NamaBank:    ptr("Mandiri"),
		Jumlah:      "600000",
		Alokasi: []model.PembayaranAlokasiRequest{{
			IDPenjualan: penjualan.ID, Jumlah: "600000",
		}},
	})
	if err != nil {
		t.Fatalf("create pembayaran: %v", err)
	}

	// Its own series, independent of BL, PS, RB, PU, MT, PM, PJ, dan SO.
	if pembayaran.Nomor != "PP/2026/08/0001" {
		t.Errorf("nomor = %q, want PP/2026/08/0001", pembayaran.Nomor)
	}

	if pembayaran.Status != "DRAFT" {
		t.Errorf("status = %q, want DRAFT", pembayaran.Status)
	}

	if pembayaran.StatusGiro != nil || pembayaran.TanggalCair != nil {
		t.Errorf("status_giro %v tanggal_cair %v, want keduanya nil untuk TRANSFER",
			pembayaran.StatusGiro, pembayaran.TanggalCair)
	}

	if statusPiutang(t, testApp, penjualan.ID).StatusPembayaran != "BELUM" {
		t.Error("draft pembayaran seharusnya tidak mengubah status_pembayaran")
	}

	_, err = testApp.penerimaan.Batal(ctx(), &model.BatalPenerimaanPembayaranRequest{
		ID: pembayaran.ID, ActorID: s.actor, AlasanBatal: "belum diposting",
	})
	assertKind(t, err, model.KindConflict)

	_, err = testApp.penerimaan.Update(ctx(), &model.UpdatePenerimaanPembayaranRequest{
		ID: pembayaran.ID, ActorID: s.actor,
		TanggalJatuhTempoGiro: model.Optional[string]{Present: true, Value: ptr("2026-09-20")},
	})
	assertKind(t, err, model.KindInvalid)

	_, err = testApp.penerimaan.Update(ctx(), &model.UpdatePenerimaanPembayaranRequest{
		ID: pembayaran.ID, ActorID: s.actor,
		Jumlah: model.Optional[string]{Present: true, Value: ptr("500000")},
	})
	assertKind(t, err, model.KindInvalid)

	naik, err := testApp.penerimaan.Update(ctx(), &model.UpdatePenerimaanPembayaranRequest{
		ID: pembayaran.ID, ActorID: s.actor,
		Jumlah: model.Optional[string]{Present: true, Value: ptr("700000")},
	})
	if err != nil {
		t.Fatalf("naikkan jumlah: %v", err)
	}

	if naik.SisaBelumDialokasikan != "100000.00" {
		t.Errorf("sisa_belum_dialokasikan = %s, want 100000.00", naik.SisaBelumDialokasikan)
	}

	posted, err := testApp.penerimaan.Posting(ctx(), &model.PostingPenerimaanPembayaranRequest{
		ID: pembayaran.ID, ActorID: s.actor,
	})
	if err != nil {
		t.Fatalf("posting: %v", err)
	}

	if posted.PostedAt == nil {
		t.Error("posted_at kosong setelah posting")
	}

	_, err = testApp.penerimaan.Posting(ctx(), &model.PostingPenerimaanPembayaranRequest{
		ID: pembayaran.ID, ActorID: s.actor,
	})
	assertKind(t, err, model.KindConflict)

	_, err = testApp.penerimaan.Update(ctx(), &model.UpdatePenerimaanPembayaranRequest{
		ID: pembayaran.ID, ActorID: s.actor,
		Keterangan: model.Optional[string]{Present: true, Value: ptr("catatan")},
	})
	assertKind(t, err, model.KindConflict)

	_, err = testApp.penerimaan.ReplaceAlokasi(ctx(), &model.ReplacePenerimaanPembayaranAlokasiRequest{
		ID: pembayaran.ID, ActorID: s.actor,
		Alokasi: []model.PembayaranAlokasiRequest{{
			IDPenjualan: penjualan.ID, Jumlah: "100000",
		}},
	})
	assertKind(t, err, model.KindConflict)

	_, err = testApp.penerimaan.CairkanGiro(ctx(), &model.CairkanGiroPelangganRequest{
		ID: pembayaran.ID, ActorID: s.actor, TanggalCair: "2026-08-21",
	})
	assertKind(t, err, model.KindInvalid)
}

// Replacing the allocation set wholesale, and the empty case that means "allocate
// none".
func TestReplaceAlokasiPenerimaanMenggantiSeluruhnya(t *testing.T) {
	testApp := newApp(t)
	s, penjualan := piutangFixture(t, testApp, "100")

	kedua, err := testApp.penjualan.Create(ctx(), &model.CreatePenjualanRequest{
		ActorID: s.actor, Tanggal: "2026-08-15", IDRuang: s.ruang,
		IDPelanggan: &s.pelanggan, JenisPembayaran: "KREDIT",
		Detail: []model.PenjualanDetailRequest{{
			IDProduct: s.product, IDSatuanInput: s.pcs, QtyInput: "50", HargaSatuanInput: "15000",
		}},
	})
	if err != nil {
		t.Fatalf("create penjualan kedua: %v", err)
	}

	lain := postingPenjualan(t, testApp, s, kedua.ID)

	pembayaran, err := testApp.penerimaan.Create(ctx(), &model.CreatePenerimaanPembayaranRequest{
		ActorID:     s.actor,
		IDPelanggan: s.pelanggan,
		Tanggal:     "2026-08-20",
		Metode:      "TRANSFER",
		Jumlah:      "500000",
		Alokasi: []model.PembayaranAlokasiRequest{{
			IDPenjualan: penjualan.ID, Jumlah: "500000",
		}},
	})
	if err != nil {
		t.Fatalf("create pembayaran: %v", err)
	}

	diganti, err := testApp.penerimaan.ReplaceAlokasi(ctx(), &model.ReplacePenerimaanPembayaranAlokasiRequest{
		ID: pembayaran.ID, ActorID: s.actor,
		Alokasi: []model.PembayaranAlokasiRequest{
			{IDPenjualan: penjualan.ID, Jumlah: "200000"},
			{IDPenjualan: lain.ID, Jumlah: "300000"},
		},
	})
	if err != nil {
		t.Fatalf("replace alokasi: %v", err)
	}

	if len(diganti.Alokasi) != 2 || diganti.JumlahDialokasikan != "500000.00" {
		t.Errorf("%d alokasi dengan total %s, want 2 dan 500000.00",
			len(diganti.Alokasi), diganti.JumlahDialokasikan)
	}

	if diganti.Alokasi[0].NomorPenjualan == "" {
		t.Error("nomor_penjualan kosong pada alokasi")
	}

	kosong, err := testApp.penerimaan.ReplaceAlokasi(ctx(), &model.ReplacePenerimaanPembayaranAlokasiRequest{
		ID: pembayaran.ID, ActorID: s.actor,
	})
	if err != nil {
		t.Fatalf("replace alokasi kosong: %v", err)
	}

	if len(kosong.Alokasi) != 0 || kosong.JumlahDialokasikan != "0.00" {
		t.Errorf("%d alokasi dengan total %s, want 0 dan 0.00",
			len(kosong.Alokasi), kosong.JumlahDialokasikan)
	}

	if kosong.SisaBelumDialokasikan != "500000.00" {
		t.Errorf("sisa_belum_dialokasikan = %s, want 500000.00", kosong.SisaBelumDialokasikan)
	}
}

// GET /pelanggan/{id}/piutang is the working list for building a payment: open
// notas, oldest first, with what each still has outstanding — and, since isu #20,
// the figure is real rather than always equal to total.
func TestPiutangPelangganMelaporkanSisaSebenarnya(t *testing.T) {
	testApp := newApp(t)
	s, pertama := piutangFixture(t, testApp, "100")

	kedua, err := testApp.penjualan.Create(ctx(), &model.CreatePenjualanRequest{
		ActorID: s.actor, Tanggal: "2026-08-16", IDRuang: s.ruang,
		IDPelanggan: &s.pelanggan, JenisPembayaran: "KREDIT",
		Detail: []model.PenjualanDetailRequest{{
			IDProduct: s.product, IDSatuanInput: s.pcs, QtyInput: "50", HargaSatuanInput: "15000",
		}},
	})
	if err != nil {
		t.Fatalf("create penjualan kedua: %v", err)
	}

	lain := postingPenjualan(t, testApp, s, kedua.ID)

	// A DRAFT nota is not a receivable and must not appear.
	if _, err := testApp.penjualan.Create(ctx(), &model.CreatePenjualanRequest{
		ActorID: s.actor, Tanggal: "2026-08-17", IDRuang: s.ruang,
		IDPelanggan: &s.pelanggan, JenisPembayaran: "KREDIT",
		Detail: []model.PenjualanDetailRequest{{
			IDProduct: s.product, IDSatuanInput: s.pcs, QtyInput: "10", HargaSatuanInput: "15000",
		}},
	}); err != nil {
		t.Fatalf("create draft: %v", err)
	}

	daftar, paging, err := testApp.pelanggan.Piutang(ctx(), &model.ListPiutangPelangganRequest{
		IDPelanggan: s.pelanggan,
	})
	if err != nil {
		t.Fatalf("piutang pelanggan: %v", err)
	}

	if paging.TotalItem != 2 {
		t.Fatalf("total_item = %d, want 2 (draft tidak ikut)", paging.TotalItem)
	}

	// Oldest first: this list is a queue to work through, not a history to read.
	if daftar[0].IDPenjualan != pertama.ID || daftar[1].IDPenjualan != lain.ID {
		t.Errorf("urutan = %d, %d; want %d, %d (terlama dulu)",
			daftar[0].IDPenjualan, daftar[1].IDPenjualan, pertama.ID, lain.ID)
	}

	if daftar[0].SisaPiutang != "1500000.00" || daftar[1].SisaPiutang != "750000.00" {
		t.Errorf("sisa_piutang = %s, %s; want 1500000.00 dan 750000.00",
			daftar[0].SisaPiutang, daftar[1].SisaPiutang)
	}

	// Settling one partially moves its own figure and leaves the other alone.
	terima(t, testApp, s, pertama.ID, "500000")

	setelah, _, err := testApp.pelanggan.Piutang(ctx(), &model.ListPiutangPelangganRequest{
		IDPelanggan: s.pelanggan,
	})
	if err != nil {
		t.Fatalf("piutang pelanggan: %v", err)
	}

	if setelah[0].SisaPiutang != "1000000.00" || setelah[1].SisaPiutang != "750000.00" {
		t.Errorf("sisa_piutang = %s, %s; want 1000000.00 dan 750000.00",
			setelah[0].SisaPiutang, setelah[1].SisaPiutang)
	}
}

// The list filters and pages, and its ordering ends in a unique column.
func TestListPenerimaanPembayaranFilterDanPaginasi(t *testing.T) {
	testApp := newApp(t)
	s, penjualan := piutangFixture(t, testApp, "1000")

	for range 5 {
		if _, err := testApp.penerimaan.Create(ctx(), &model.CreatePenerimaanPembayaranRequest{
			ActorID:     s.actor,
			IDPelanggan: s.pelanggan,
			Tanggal:     "2026-08-20",
			Metode:      "TUNAI",
			Jumlah:      "1000",
			Alokasi: []model.PembayaranAlokasiRequest{{
				IDPenjualan: penjualan.ID, Jumlah: "1000",
			}},
		}); err != nil {
			t.Fatalf("create pembayaran: %v", err)
		}
	}

	_, paging, err := testApp.penerimaan.Search(ctx(), &model.ListPenerimaanPembayaranRequest{})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if paging.TotalItem != 5 {
		t.Errorf("total_item = %d, want 5", paging.TotalItem)
	}

	kosong, _, err := testApp.penerimaan.Search(ctx(), &model.ListPenerimaanPembayaranRequest{
		Status: "POSTED",
	})
	if err != nil {
		t.Fatalf("search POSTED: %v", err)
	}

	if len(kosong) != 0 {
		t.Errorf("filter POSTED mengembalikan %d, want 0", len(kosong))
	}

	tunai, _, err := testApp.penerimaan.Search(ctx(), &model.ListPenerimaanPembayaranRequest{
		Metode: "TUNAI",
	})
	if err != nil {
		t.Fatalf("search TUNAI: %v", err)
	}

	if len(tunai) != 5 {
		t.Errorf("filter TUNAI mengembalikan %d, want 5", len(tunai))
	}

	if tunai[0].Alokasi != nil {
		t.Error("list membawa alokasi, want kunci hilang")
	}

	terlihat := map[int64]bool{}
	for halaman := 1; halaman <= 3; halaman++ {
		batch, _, err := testApp.penerimaan.Search(ctx(), &model.ListPenerimaanPembayaranRequest{
			PageRequest: model.PageRequest{Page: halaman, Size: 2},
		})
		if err != nil {
			t.Fatalf("search halaman %d: %v", halaman, err)
		}

		for _, row := range batch {
			if terlihat[row.ID] {
				t.Errorf("id %d muncul di dua halaman", row.ID)
			}

			terlihat[row.ID] = true
		}
	}

	if len(terlihat) != 5 {
		t.Errorf("paginasi mengembalikan %d dokumen unik, want 5", len(terlihat))
	}
}

// This is the reason the module exists: a customer who has paid a nota off in full
// is no longer counted against plafon_kredit for it, and may post a new nota up to
// the limit again. Before isu #20 this was a one-way ratchet — PiutangBerjalan
// summed raw totals with nothing to reduce them.
func TestPelangganYangSudahLunasBisaMemakaiPlafonLagi(t *testing.T) {
	testApp := newApp(t)
	s, penjualan := piutangFixture(t, testApp, "100")

	if penjualan.Total != "1500000.00" {
		t.Fatalf("total = %s, want 1500000.00", penjualan.Total)
	}

	if _, err := testApp.pelanggan.Update(ctx(), &model.UpdatePelangganRequest{
		ID:           s.pelanggan,
		ActorID:      s.actor,
		PlafonKredit: model.Optional[string]{Present: true, Value: ptr("1500000")},
	}); err != nil {
		t.Fatalf("set plafon_kredit: %v", err)
	}

	// The limit is already fully committed by the first nota: a second one of any
	// size is refused.
	kedua, err := testApp.penjualan.Create(ctx(), &model.CreatePenjualanRequest{
		ActorID: s.actor, Tanggal: "2026-08-16", IDRuang: s.ruang,
		IDPelanggan: &s.pelanggan, JenisPembayaran: "KREDIT",
		Detail: []model.PenjualanDetailRequest{{
			IDProduct: s.product, IDSatuanInput: s.pcs, QtyInput: "1", HargaSatuanInput: "15000",
		}},
	})
	if err != nil {
		t.Fatalf("create penjualan kedua: %v", err)
	}

	_, err = testApp.penjualan.Posting(ctx(), &model.PostingPenjualanRequest{
		ID: kedua.ID, ActorID: s.actor,
	})
	assertKind(t, err, model.KindInvalid)
	assertPesanMemuat(t, err, "plafon_kredit")

	// Paying the first nota off in full is what frees the limit back up.
	terima(t, testApp, s, penjualan.ID, "1500000")

	if statusPiutang(t, testApp, penjualan.ID).StatusPembayaran != "LUNAS" {
		t.Fatal("nota pertama belum LUNAS")
	}

	posted, err := testApp.penjualan.Posting(ctx(), &model.PostingPenjualanRequest{
		ID: kedua.ID, ActorID: s.actor,
	})
	if err != nil {
		t.Fatalf("posting nota kedua setelah nota pertama lunas: %v", err)
	}

	if posted.Status != "POSTED" {
		t.Errorf("status = %q, want POSTED", posted.Status)
	}
}
