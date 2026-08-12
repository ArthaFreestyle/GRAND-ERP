package usecase_test

// The point of these is arithmetic that has to hold across three documents at once: an
// invoice, the payments allocated to it, and the returns that credit against it. All of it
// lives in the database — the effective-allocation predicate with its giro rule, the
// status_pembayaran cache, the row locks that serialise two payments over one invoice — so
// it is exercised there.

import (
	"testing"

	"Arthafreestyle/ERP/internal/model"
)

// utangFixture posts a purchase of 100 pcs at 10.000 with no freight, so the invoice total
// is a round 1.000.000 and every figure below is readable by eye.
func utangFixture(t *testing.T, testApp *app) (fixture, *model.PembelianResponse) {
	t.Helper()

	f := pembelianFixture(t, testApp)

	pembelian := draftSederhana(t, testApp, f, "100", nil, nil)

	return f, ajukanDanPosting(t, testApp, f, pembelian.ID)
}

// bayar opens and posts a cash payment against one invoice.
func bayar(t *testing.T, testApp *app, f fixture, idPembelian int64, jumlah string) *model.PembayaranUtangResponse {
	t.Helper()

	pembayaran, err := testApp.pembayaran.Create(ctx(), &model.CreatePembayaranUtangRequest{
		ActorID:    f.actor,
		IDSupplier: f.supplier,
		Tanggal:    "2026-08-20",
		Metode:     "TUNAI",
		Jumlah:     jumlah,
		Alokasi: []model.PembayaranUtangAlokasiRequest{{
			IDPembelian: idPembelian, Jumlah: jumlah,
		}},
	})
	if err != nil {
		t.Fatalf("create pembayaran utang: %v", err)
	}

	posted, err := testApp.pembayaran.Posting(ctx(), &model.PostingPembayaranUtangRequest{
		ID: pembayaran.ID, ActorID: f.actor,
	})
	if err != nil {
		t.Fatalf("posting pembayaran utang: %v", err)
	}

	return posted
}

// statusPembayaran reads the invoice's cache plus the three figures behind it.
func statusPembayaran(t *testing.T, testApp *app, idPembelian int64) *model.PembelianResponse {
	t.Helper()

	pembelian, err := testApp.pembelian.Get(ctx(), &model.GetPembelianRequest{ID: idPembelian})
	if err != nil {
		t.Fatalf("get pembelian: %v", err)
	}

	return pembelian
}

// The cache and the arithmetic behind it, across a part payment and then the rest.
func TestPembayaranMenggerakkanStatusPembayaran(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := utangFixture(t, testApp)

	awal := statusPembayaran(t, testApp, pembelian.ID)
	if awal.StatusPembayaran != "BELUM" || awal.SisaUtang != "1000000.00" {
		t.Fatalf("awal: status %q sisa_utang %s, want BELUM dan 1000000.00",
			awal.StatusPembayaran, awal.SisaUtang)
	}

	bayar(t, testApp, f, pembelian.ID, "400000")

	sebagian := statusPembayaran(t, testApp, pembelian.ID)
	if sebagian.StatusPembayaran != "SEBAGIAN" {
		t.Errorf("setelah bayar sebagian: status = %q, want SEBAGIAN", sebagian.StatusPembayaran)
	}

	if sebagian.JumlahDialokasikan != "400000.00" || sebagian.SisaUtang != "600000.00" {
		t.Errorf("dialokasikan %s sisa_utang %s, want 400000.00 dan 600000.00",
			sebagian.JumlahDialokasikan, sebagian.SisaUtang)
	}

	bayar(t, testApp, f, pembelian.ID, "600000")

	lunas := statusPembayaran(t, testApp, pembelian.ID)
	if lunas.StatusPembayaran != "LUNAS" || lunas.SisaUtang != "0.00" {
		t.Errorf("setelah lunas: status %q sisa_utang %s, want LUNAS dan 0.00",
			lunas.StatusPembayaran, lunas.SisaUtang)
	}
}

// An invoice may receive at most what it still owes, and two payments have to share one
// balance rather than each getting the whole of it.
func TestAlokasiTidakBolehMelebihiSisaUtang(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := utangFixture(t, testApp)

	buat := func(jumlah string) error {
		_, err := testApp.pembayaran.Create(ctx(), &model.CreatePembayaranUtangRequest{
			ActorID:    f.actor,
			IDSupplier: f.supplier,
			Tanggal:    "2026-08-20",
			Metode:     "TRANSFER",
			Jumlah:     jumlah,
			Alokasi: []model.PembayaranUtangAlokasiRequest{{
				IDPembelian: pembelian.ID, Jumlah: jumlah,
			}},
		})

		return err
	}

	assertKind(t, buat("1000001"), model.KindInvalid)

	bayar(t, testApp, f, pembelian.ID, "700000")

	assertKind(t, buat("300001"), model.KindInvalid)

	if err := buat("300000"); err != nil {
		t.Errorf("alokasi 300000 dari sisa 300000 ditolak: %v", err)
	}
}

// A payment may not be allocated beyond its own amount — a different ceiling from the
// invoice's, and a request can satisfy either one and fail the other.
//
// Under-allocation is not refused: the remainder is a credit sitting with the supplier.
func TestAlokasiTidakBolehMelebihiJumlahPembayaran(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := utangFixture(t, testApp)

	kedua := draftSederhana(t, testApp, f, "100", nil, nil)
	lain := ajukanDanPosting(t, testApp, f, kedua.ID)

	// 500.000 paid, split 300.000 + 300.000 across two invoices that could each absorb it.
	// Each allocation fits its invoice; together they exceed the payment.
	_, err := testApp.pembayaran.Create(ctx(), &model.CreatePembayaranUtangRequest{
		ActorID:    f.actor,
		IDSupplier: f.supplier,
		Tanggal:    "2026-08-20",
		Metode:     "TRANSFER",
		Jumlah:     "500000",
		Alokasi: []model.PembayaranUtangAlokasiRequest{
			{IDPembelian: pembelian.ID, Jumlah: "300000"},
			{IDPembelian: lain.ID, Jumlah: "300000"},
		},
	})
	assertKind(t, err, model.KindInvalid)

	// Under-allocating is fine, and the remainder is reported.
	kredit, err := testApp.pembayaran.Create(ctx(), &model.CreatePembayaranUtangRequest{
		ActorID:    f.actor,
		IDSupplier: f.supplier,
		Tanggal:    "2026-08-20",
		Metode:     "TRANSFER",
		Jumlah:     "500000",
		Alokasi: []model.PembayaranUtangAlokasiRequest{
			{IDPembelian: pembelian.ID, Jumlah: "200000"},
		},
	})
	if err != nil {
		t.Fatalf("pembayaran dengan sisa titipan ditolak: %v", err)
	}

	if kredit.JumlahDialokasikan != "200000.00" || kredit.SisaBelumDialokasikan != "300000.00" {
		t.Errorf("dialokasikan %s sisa_belum_dialokasikan %s, want 200000.00 dan 300000.00",
			kredit.JumlahDialokasikan, kredit.SisaBelumDialokasikan)
	}

	// So is allocating nothing at all: money paid before it is decided what it covers.
	titipan, err := testApp.pembayaran.Create(ctx(), &model.CreatePembayaranUtangRequest{
		ActorID:    f.actor,
		IDSupplier: f.supplier,
		Tanggal:    "2026-08-20",
		Metode:     "TRANSFER",
		Jumlah:     "250000",
	})
	if err != nil {
		t.Fatalf("pembayaran tanpa alokasi ditolak: %v", err)
	}

	if titipan.JumlahDialokasikan != "0.00" || len(titipan.Alokasi) != 0 {
		t.Errorf("dialokasikan %s dengan %d alokasi, want 0.00 dan 0",
			titipan.JumlahDialokasikan, len(titipan.Alokasi))
	}
}

// **An uncashed giro is not a payment.** Posting one makes the document real and settles
// nothing; the payable drops when it clears, and never if it bounces.
func TestGiroBelumCairTidakMengurangiUtang(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := utangFixture(t, testApp)

	giro, err := testApp.pembayaran.Create(ctx(), &model.CreatePembayaranUtangRequest{
		ActorID:               f.actor,
		IDSupplier:            f.supplier,
		Tanggal:               "2026-08-20",
		Metode:                "GIRO",
		NamaBank:              ptr("BCA"),
		NoReferensi:           ptr("GR-00123"),
		TanggalJatuhTempoGiro: ptr("2026-09-20"),
		Jumlah:                "1000000",
		Alokasi: []model.PembayaranUtangAlokasiRequest{{
			IDPembelian: pembelian.ID, Jumlah: "1000000",
		}},
	})
	if err != nil {
		t.Fatalf("create giro: %v", err)
	}

	if giro.StatusGiro == nil || *giro.StatusGiro != "BELUM_CAIR" {
		t.Fatalf("status_giro = %v, want BELUM_CAIR", giro.StatusGiro)
	}

	posted, err := testApp.pembayaran.Posting(ctx(), &model.PostingPembayaranUtangRequest{
		ID: giro.ID, ActorID: f.actor,
	})
	if err != nil {
		t.Fatalf("posting giro: %v", err)
	}

	// The document is POSTED and fully allocated, and the invoice is untouched. That
	// combination is the whole rule.
	if posted.Status != "POSTED" || posted.JumlahDialokasikan != "1000000.00" {
		t.Fatalf("status %q dialokasikan %s, want POSTED dan 1000000.00",
			posted.Status, posted.JumlahDialokasikan)
	}

	belum := statusPembayaran(t, testApp, pembelian.ID)
	if belum.StatusPembayaran != "BELUM" || belum.SisaUtang != "1000000.00" {
		t.Errorf("giro belum cair: status %q sisa_utang %s, want BELUM dan 1000000.00",
			belum.StatusPembayaran, belum.SisaUtang)
	}

	// Clearing it is the moment the payable drops.
	cair, err := testApp.pembayaran.CairkanGiro(ctx(), &model.CairkanGiroRequest{
		ID: giro.ID, ActorID: f.actor, TanggalCair: "2026-09-22",
	})
	if err != nil {
		t.Fatalf("cairkan giro: %v", err)
	}

	if cair.StatusGiro == nil || *cair.StatusGiro != "CAIR" || cair.TanggalCair == nil {
		t.Errorf("status_giro %v tanggal_cair %v, want CAIR dengan tanggal terisi",
			cair.StatusGiro, cair.TanggalCair)
	}

	lunas := statusPembayaran(t, testApp, pembelian.ID)
	if lunas.StatusPembayaran != "LUNAS" || lunas.SisaUtang != "0.00" {
		t.Errorf("setelah cair: status %q sisa_utang %s, want LUNAS dan 0.00",
			lunas.StatusPembayaran, lunas.SisaUtang)
	}

	// And it cannot clear twice.
	_, err = testApp.pembayaran.CairkanGiro(ctx(), &model.CairkanGiroRequest{
		ID: giro.ID, ActorID: f.actor, TanggalCair: "2026-09-23",
	})
	assertKind(t, err, model.KindConflict)
}

// A bounced giro never reduced anything, so nothing has to be given back — and the invoice
// it was aimed at was never settled in the first place.
func TestGiroDitolakTidakPernahMengurangiUtang(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := utangFixture(t, testApp)

	giro, err := testApp.pembayaran.Create(ctx(), &model.CreatePembayaranUtangRequest{
		ActorID:    f.actor,
		IDSupplier: f.supplier,
		Tanggal:    "2026-08-20",
		Metode:     "GIRO",
		NamaBank:   ptr("BCA"),
		Jumlah:     "1000000",
		Alokasi: []model.PembayaranUtangAlokasiRequest{{
			IDPembelian: pembelian.ID, Jumlah: "1000000",
		}},
	})
	if err != nil {
		t.Fatalf("create giro: %v", err)
	}

	if _, err := testApp.pembayaran.Posting(ctx(), &model.PostingPembayaranUtangRequest{
		ID: giro.ID, ActorID: f.actor,
	}); err != nil {
		t.Fatalf("posting giro: %v", err)
	}

	ditolak, err := testApp.pembayaran.TolakGiro(ctx(), &model.TolakGiroRequest{
		ID: giro.ID, ActorID: f.actor, Alasan: ptr("dana tidak cukup"),
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

	tetap := statusPembayaran(t, testApp, pembelian.ID)
	if tetap.StatusPembayaran != "BELUM" || tetap.SisaUtang != "1000000.00" {
		t.Errorf("setelah giro ditolak: status %q sisa_utang %s, want BELUM dan 1000000.00",
			tetap.StatusPembayaran, tetap.SisaUtang)
	}

	// A rejected giro cannot then be cleared.
	_, err = testApp.pembayaran.CairkanGiro(ctx(), &model.CairkanGiroRequest{
		ID: giro.ID, ActorID: f.actor, TanggalCair: "2026-09-22",
	})
	assertKind(t, err, model.KindConflict)

	// And the invoice is still payable by something else, for the full amount.
	bayar(t, testApp, f, pembelian.ID, "1000000")

	lunas := statusPembayaran(t, testApp, pembelian.ID)
	if lunas.StatusPembayaran != "LUNAS" {
		t.Errorf("status = %q, want LUNAS", lunas.StatusPembayaran)
	}
}

// A giro's allocations count for nothing while it is outstanding, so the invoice can be
// settled by something else in the meantime. That is a real sequence rather than a race —
// a giro can sit for weeks — and clearing it then has to fail.
func TestCairkanGiroDiperiksaUlangTerhadapSisaUtang(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := utangFixture(t, testApp)

	giro, err := testApp.pembayaran.Create(ctx(), &model.CreatePembayaranUtangRequest{
		ActorID:    f.actor,
		IDSupplier: f.supplier,
		Tanggal:    "2026-08-20",
		Metode:     "GIRO",
		NamaBank:   ptr("BCA"),
		Jumlah:     "1000000",
		Alokasi: []model.PembayaranUtangAlokasiRequest{{
			IDPembelian: pembelian.ID, Jumlah: "1000000",
		}},
	})
	if err != nil {
		t.Fatalf("create giro: %v", err)
	}

	if _, err := testApp.pembayaran.Posting(ctx(), &model.PostingPembayaranUtangRequest{
		ID: giro.ID, ActorID: f.actor,
	}); err != nil {
		t.Fatalf("posting giro: %v", err)
	}

	// Somebody transfers the money while the cheque is still out. Legal: the giro reduced
	// nothing, so the invoice was still fully open.
	bayar(t, testApp, f, pembelian.ID, "1000000")

	_, err = testApp.pembayaran.CairkanGiro(ctx(), &model.CairkanGiroRequest{
		ID: giro.ID, ActorID: f.actor, TanggalCair: "2026-09-22",
	})
	assertKind(t, err, model.KindInvalid)

	// The refused clearing changed nothing: the giro is still outstanding and the invoice
	// is settled exactly once.
	masih := statusPembayaran(t, testApp, pembelian.ID)
	if masih.JumlahDialokasikan != "1000000.00" || masih.StatusPembayaran != "LUNAS" {
		t.Errorf("dialokasikan %s status %q, want 1000000.00 dan LUNAS",
			masih.JumlahDialokasikan, masih.StatusPembayaran)
	}
}

// Voiding a payment hands the invoice back what it owed. Nothing is deleted, so what the
// payment claimed to settle stays readable next to alasan_batal.
func TestBatalPembayaranMengembalikanUtang(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := utangFixture(t, testApp)

	pembayaran := bayar(t, testApp, f, pembelian.ID, "1000000")

	if statusPembayaran(t, testApp, pembelian.ID).StatusPembayaran != "LUNAS" {
		t.Fatal("faktur belum LUNAS setelah dibayar penuh")
	}

	batal, err := testApp.pembayaran.Batal(ctx(), &model.BatalPembayaranUtangRequest{
		ID: pembayaran.ID, ActorID: f.actor, AlasanBatal: "transfer ke rekening yang salah",
	})
	if err != nil {
		t.Fatalf("batal pembayaran: %v", err)
	}

	if batal.Status != "BATAL" || batal.AlasanBatal == nil {
		t.Errorf("status %q alasan_batal %v, want BATAL dengan alasan terisi",
			batal.Status, batal.AlasanBatal)
	}

	// The allocation rows survive the void, which is what makes it auditable.
	if len(batal.Alokasi) != 1 {
		t.Errorf("alokasi = %d baris, want 1 (tidak dihapus)", len(batal.Alokasi))
	}

	lagi := statusPembayaran(t, testApp, pembelian.ID)
	if lagi.StatusPembayaran != "BELUM" || lagi.SisaUtang != "1000000.00" {
		t.Errorf("setelah batal: status %q sisa_utang %s, want BELUM dan 1000000.00",
			lagi.StatusPembayaran, lagi.SisaUtang)
	}

	// And the invoice is payable again, in full.
	bayar(t, testApp, f, pembelian.ID, "1000000")

	if statusPembayaran(t, testApp, pembelian.ID).StatusPembayaran != "LUNAS" {
		t.Error("faktur tidak LUNAS setelah dibayar ulang")
	}
}

// A return credits against the payable, and the figure is **not** the return's own total.
//
// This is the deferral fase 5 left open. total is the inventory value at cost, and cost
// carries the freight share paid to the carrier — money the supplier never received.
func TestReturMengkreditUtangBukanSebesarTotalnya(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	// 100 pcs at 10.000 = 1.000.000 invoiced, plus 50.000 of freight billed by the
	// carrier. The supplier is owed 1.000.000; inventory carries 1.050.000.
	draft, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
		ActorID:      f.actor,
		Tanggal:      "2026-08-11",
		IDSupplier:   f.supplier,
		IDRuang:      f.ruang,
		TotalKoli:    ptr("1"),
		TarifPerKoli: ptr("50000"),
		Detail: []model.PembelianDetailRequest{{
			IDProduct: f.product, IDSatuanInput: f.pcs,
			QtyFaktur: "100", HargaSatuanInput: "10000", JumlahKoli: "1",
		}},
	})
	if err != nil {
		t.Fatalf("create pembelian: %v", err)
	}

	pembelian := ajukanDanPosting(t, testApp, f, draft.ID)

	if pembelian.Total != "1000000.00" {
		t.Fatalf("total = %s, want 1000000.00 (ongkir bukan bagian dari total)", pembelian.Total)
	}

	retur := buatRetur(t, testApp, f, pembelian.ID, pembelian.Detail[0].ID, "100")

	// Inventory value at cost: 100 x 10.500, freight included.
	if retur.Total != "1050000.00" {
		t.Errorf("total retur = %s, want 1050000.00 (nilai persediaan)", retur.Total)
	}

	// What the supplier is credited: the invoice value, freight excluded.
	if retur.NilaiKreditUtang != "1000000.00" {
		t.Errorf("nilai_kredit_utang = %s, want 1000000.00 (nilai faktur)", retur.NilaiKreditUtang)
	}

	lunas := statusPembayaran(t, testApp, pembelian.ID)
	if lunas.NilaiKreditRetur != "1000000.00" || lunas.SisaUtang != "0.00" {
		t.Errorf("kredit_retur %s sisa_utang %s, want 1000000.00 dan 0.00",
			lunas.NilaiKreditRetur, lunas.SisaUtang)
	}

	// Nothing left owing, so the invoice reads LUNAS — settled by goods rather than money.
	if lunas.StatusPembayaran != "LUNAS" {
		t.Errorf("status_pembayaran = %q, want LUNAS", lunas.StatusPembayaran)
	}
}

// The credit is scaled to the invoice's total, so it carries the nota discount with it.
// Crediting the raw line value would hand back the discount that reduced the bill.
func TestKreditReturIkutMemperhitungkanDiskonNota(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	// 100 pcs at 10.000 with a 100.000 nota discount: the supplier is owed 900.000.
	draft, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
		ActorID:    f.actor,
		Tanggal:    "2026-08-11",
		IDSupplier: f.supplier,
		IDRuang:    f.ruang,
		DiskonNota: "100000",
		Detail: []model.PembelianDetailRequest{{
			IDProduct: f.product, IDSatuanInput: f.pcs,
			QtyFaktur: "100", HargaSatuanInput: "10000",
		}},
	})
	if err != nil {
		t.Fatalf("create pembelian: %v", err)
	}

	pembelian := ajukanDanPosting(t, testApp, f, draft.ID)

	if pembelian.Total != "900000.00" {
		t.Fatalf("total = %s, want 900000.00", pembelian.Total)
	}

	// Half the goods go back: half of 900.000, not half of the 1.000.000 line value.
	retur := buatRetur(t, testApp, f, pembelian.ID, pembelian.Detail[0].ID, "50")

	if retur.NilaiKreditUtang != "450000.00" {
		t.Errorf("nilai_kredit_utang = %s, want 450000.00 (setengah dari total, bukan dari subtotal)",
			retur.NilaiKreditUtang)
	}

	sebagian := statusPembayaran(t, testApp, pembelian.ID)
	if sebagian.SisaUtang != "450000.00" || sebagian.StatusPembayaran != "SEBAGIAN" {
		t.Errorf("sisa_utang %s status %q, want 450000.00 dan SEBAGIAN",
			sebagian.SisaUtang, sebagian.StatusPembayaran)
	}

	// The rest of the goods go back too, and the credits add up to exactly the total.
	buatRetur(t, testApp, f, pembelian.ID, pembelian.Detail[0].ID, "50")

	habis := statusPembayaran(t, testApp, pembelian.ID)
	if habis.NilaiKreditRetur != "900000.00" || habis.SisaUtang != "0.00" {
		t.Errorf("kredit_retur %s sisa_utang %s, want 900000.00 dan 0.00 (persis total)",
			habis.NilaiKreditRetur, habis.SisaUtang)
	}
}

// A return and a payment together cannot settle more than the invoice: the credit eats into
// the same balance a payment draws from.
func TestReturDanPembayaranBerbagiSatuSisaUtang(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := utangFixture(t, testApp)

	// 30 of 100 go back, crediting 300.000 of the 1.000.000.
	buatRetur(t, testApp, f, pembelian.ID, pembelian.Detail[0].ID, "30")

	sisa := statusPembayaran(t, testApp, pembelian.ID)
	if sisa.NilaiKreditRetur != "300000.00" || sisa.SisaUtang != "700000.00" {
		t.Fatalf("kredit_retur %s sisa_utang %s, want 300000.00 dan 700000.00",
			sisa.NilaiKreditRetur, sisa.SisaUtang)
	}

	// Paying the full invoice is now too much.
	_, err := testApp.pembayaran.Create(ctx(), &model.CreatePembayaranUtangRequest{
		ActorID:    f.actor,
		IDSupplier: f.supplier,
		Tanggal:    "2026-08-20",
		Metode:     "TUNAI",
		Jumlah:     "1000000",
		Alokasi: []model.PembayaranUtangAlokasiRequest{{
			IDPembelian: pembelian.ID, Jumlah: "1000000",
		}},
	})
	assertKind(t, err, model.KindInvalid)

	bayar(t, testApp, f, pembelian.ID, "700000")

	lunas := statusPembayaran(t, testApp, pembelian.ID)
	if lunas.StatusPembayaran != "LUNAS" || lunas.SisaUtang != "0.00" {
		t.Errorf("status %q sisa_utang %s, want LUNAS dan 0.00",
			lunas.StatusPembayaran, lunas.SisaUtang)
	}

	// Voiding the return puts the 300.000 back, and the invoice is only partly settled
	// again.
	returLagi, _, err := testApp.retur.Search(ctx(), &model.ListReturPembelianRequest{
		IDPembelian: pembelian.ID, Status: "POSTED",
	})
	if err != nil {
		t.Fatalf("search retur: %v", err)
	}

	if len(returLagi) != 1 {
		t.Fatalf("retur POSTED = %d, want 1", len(returLagi))
	}

	if _, err := testApp.retur.Batal(ctx(), &model.BatalReturPembelianRequest{
		ID: returLagi[0].ID, ActorID: f.actor, AlasanBatal: "supplier menolak returnya",
	}); err != nil {
		t.Fatalf("batal retur: %v", err)
	}

	kembali := statusPembayaran(t, testApp, pembelian.ID)
	if kembali.NilaiKreditRetur != "0.00" || kembali.SisaUtang != "300000.00" {
		t.Errorf("kredit_retur %s sisa_utang %s, want 0.00 dan 300000.00",
			kembali.NilaiKreditRetur, kembali.SisaUtang)
	}

	if kembali.StatusPembayaran != "SEBAGIAN" {
		t.Errorf("status = %q, want SEBAGIAN", kembali.StatusPembayaran)
	}
}

// Only a POSTED invoice belonging to this supplier can be paid. A DRAFT is a typed page
// rather than a debt; a BATAL one is a debt that was withdrawn; another supplier's invoice
// would make both ledgers wrong at once.
func TestAlokasiMenolakFakturYangTidakBolehDibayar(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := utangFixture(t, testApp)

	draft := draftSederhana(t, testApp, f, "10", nil, nil)

	supplierLain, err := testApp.supplier.Create(ctx(), &model.CreateSupplierRequest{Nama: "PT Lain"})
	if err != nil {
		t.Fatalf("create supplier: %v", err)
	}

	buat := func(idSupplier, idPembelian int64) error {
		_, err := testApp.pembayaran.Create(ctx(), &model.CreatePembayaranUtangRequest{
			ActorID:    f.actor,
			IDSupplier: idSupplier,
			Tanggal:    "2026-08-20",
			Metode:     "TUNAI",
			Jumlah:     "50000",
			Alokasi: []model.PembayaranUtangAlokasiRequest{{
				IDPembelian: idPembelian, Jumlah: "50000",
			}},
		})

		return err
	}

	// A DRAFT invoice.
	assertKind(t, buat(f.supplier, draft.ID), model.KindInvalid)

	// This supplier's payment aimed at an invoice belonging to nobody it deals with.
	assertKind(t, buat(supplierLain.ID, pembelian.ID), model.KindInvalid)

	// The same invoice twice in one payment: each row would pass the balance check alone
	// and together exceed it.
	_, err = testApp.pembayaran.Create(ctx(), &model.CreatePembayaranUtangRequest{
		ActorID:    f.actor,
		IDSupplier: f.supplier,
		Tanggal:    "2026-08-20",
		Metode:     "TUNAI",
		Jumlah:     "100000",
		Alokasi: []model.PembayaranUtangAlokasiRequest{
			{IDPembelian: pembelian.ID, Jumlah: "50000"},
			{IDPembelian: pembelian.ID, Jumlah: "50000"},
		},
	})
	assertKind(t, err, model.KindInvalid)
}

// The balance check that counts runs at posting, under each invoice's row lock. The one at
// draft time only produces a friendlier error sooner: two drafts can both aim at the same
// balance, and the second must fail when it tries to take what the first already took.
func TestSisaUtangDiperiksaUlangSaatPosting(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := utangFixture(t, testApp)

	// Two drafts, each claiming 800.000 of the 1.000.000. Both are legal as drafts — a
	// draft settles nothing, so neither reduces the balance.
	var draft [2]*model.PembayaranUtangResponse
	for i := range draft {
		d, err := testApp.pembayaran.Create(ctx(), &model.CreatePembayaranUtangRequest{
			ActorID:    f.actor,
			IDSupplier: f.supplier,
			Tanggal:    "2026-08-20",
			Metode:     "TRANSFER",
			Jumlah:     "800000",
			Alokasi: []model.PembayaranUtangAlokasiRequest{{
				IDPembelian: pembelian.ID, Jumlah: "800000",
			}},
		})
		if err != nil {
			t.Fatalf("draft %d: %v", i+1, err)
		}

		draft[i] = d
	}

	if _, err := testApp.pembayaran.Posting(ctx(), &model.PostingPembayaranUtangRequest{
		ID: draft[0].ID, ActorID: f.actor,
	}); err != nil {
		t.Fatalf("posting draft pertama: %v", err)
	}

	// Only 200.000 is left, so the second cannot take its 800.000.
	_, err := testApp.pembayaran.Posting(ctx(), &model.PostingPembayaranUtangRequest{
		ID: draft[1].ID, ActorID: f.actor,
	})
	assertKind(t, err, model.KindInvalid)

	// Nothing was applied for the failed posting.
	lagi := statusPembayaran(t, testApp, pembelian.ID)
	if lagi.JumlahDialokasikan != "800000.00" || lagi.StatusPembayaran != "SEBAGIAN" {
		t.Errorf("dialokasikan %s status %q, want 800000.00 dan SEBAGIAN",
			lagi.JumlahDialokasikan, lagi.StatusPembayaran)
	}
}

// Cancelling a purchase is refused while a POSTED payment points at it: the money left the
// bank, and where it should go instead is a decision rather than an arithmetic step.
func TestPembelianTidakBisaDibatalkanSaatSudahDibayar(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := utangFixture(t, testApp)

	pembayaran := bayar(t, testApp, f, pembelian.ID, "400000")

	_, err := testApp.pembelian.Batal(ctx(), &model.BatalPembelianRequest{
		ID: pembelian.ID, ActorID: f.actor, AlasanBatal: "salah supplier",
	})
	assertKind(t, err, model.KindConflict)

	// Voiding the payment first clears the way.
	if _, err := testApp.pembayaran.Batal(ctx(), &model.BatalPembayaranUtangRequest{
		ID: pembayaran.ID, ActorID: f.actor, AlasanBatal: "ikut dibatalkan",
	}); err != nil {
		t.Fatalf("batal pembayaran: %v", err)
	}

	if _, err := testApp.pembelian.Batal(ctx(), &model.BatalPembelianRequest{
		ID: pembelian.ID, ActorID: f.actor, AlasanBatal: "salah supplier",
	}); err != nil {
		t.Fatalf("batal pembelian setelah pembayaran dibatalkan: %v", err)
	}
}

// An uncashed giro reduces no payable but is still paper pointed at the invoice, so it
// blocks cancellation too — otherwise it would be unexplainable when it clears.
func TestPembelianTidakBisaDibatalkanSaatAdaGiroBelumCair(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := utangFixture(t, testApp)

	giro, err := testApp.pembayaran.Create(ctx(), &model.CreatePembayaranUtangRequest{
		ActorID:    f.actor,
		IDSupplier: f.supplier,
		Tanggal:    "2026-08-20",
		Metode:     "GIRO",
		NamaBank:   ptr("BCA"),
		Jumlah:     "1000000",
		Alokasi: []model.PembayaranUtangAlokasiRequest{{
			IDPembelian: pembelian.ID, Jumlah: "1000000",
		}},
	})
	if err != nil {
		t.Fatalf("create giro: %v", err)
	}

	if _, err := testApp.pembayaran.Posting(ctx(), &model.PostingPembayaranUtangRequest{
		ID: giro.ID, ActorID: f.actor,
	}); err != nil {
		t.Fatalf("posting giro: %v", err)
	}

	// The invoice still reads BELUM, and cancelling it is still refused.
	if statusPembayaran(t, testApp, pembelian.ID).StatusPembayaran != "BELUM" {
		t.Fatal("giro belum cair seharusnya tidak mengubah status_pembayaran")
	}

	_, err = testApp.pembelian.Batal(ctx(), &model.BatalPembelianRequest{
		ID: pembelian.ID, ActorID: f.actor, AlasanBatal: "salah supplier",
	})
	assertKind(t, err, model.KindConflict)
}

// The state machine and the number series. There is no DIAJUKAN, deliberately.
func TestAlurPembayaranUtang(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := utangFixture(t, testApp)

	pembayaran, err := testApp.pembayaran.Create(ctx(), &model.CreatePembayaranUtangRequest{
		ActorID:    f.actor,
		IDSupplier: f.supplier,
		Tanggal:    "2026-08-20",
		Metode:     "TRANSFER",
		NamaBank:   ptr("Mandiri"),
		Jumlah:     "400000",
		Alokasi: []model.PembayaranUtangAlokasiRequest{{
			IDPembelian: pembelian.ID, Jumlah: "400000",
		}},
	})
	if err != nil {
		t.Fatalf("create pembayaran: %v", err)
	}

	// Its own series, independent of BL, PS, and RB.
	if pembayaran.Nomor != "PU/2026/08/0001" {
		t.Errorf("nomor = %q, want PU/2026/08/0001", pembayaran.Nomor)
	}

	if pembayaran.Status != "DRAFT" {
		t.Errorf("status = %q, want DRAFT", pembayaran.Status)
	}

	// A non-giro payment carries no giro columns at all.
	if pembayaran.StatusGiro != nil || pembayaran.TanggalCair != nil {
		t.Errorf("status_giro %v tanggal_cair %v, want keduanya nil untuk TRANSFER",
			pembayaran.StatusGiro, pembayaran.TanggalCair)
	}

	// A draft settles nothing.
	if statusPembayaran(t, testApp, pembelian.ID).StatusPembayaran != "BELUM" {
		t.Error("draft pembayaran seharusnya tidak mengubah status_pembayaran")
	}

	// Cannot be voided before it is posted.
	_, err = testApp.pembayaran.Batal(ctx(), &model.BatalPembayaranUtangRequest{
		ID: pembayaran.ID, ActorID: f.actor, AlasanBatal: "belum diposting",
	})
	assertKind(t, err, model.KindConflict)

	// A due date on a non-giro payment describes nothing.
	_, err = testApp.pembayaran.Update(ctx(), &model.UpdatePembayaranUtangRequest{
		ID: pembayaran.ID, ActorID: f.actor,
		TanggalJatuhTempoGiro: model.Optional[string]{Present: true, Value: ptr("2026-09-20")},
	})
	assertKind(t, err, model.KindInvalid)

	// Lowering jumlah below what is already allocated is refused by name.
	_, err = testApp.pembayaran.Update(ctx(), &model.UpdatePembayaranUtangRequest{
		ID: pembayaran.ID, ActorID: f.actor,
		Jumlah: model.Optional[string]{Present: true, Value: ptr("300000")},
	})
	assertKind(t, err, model.KindInvalid)

	// Raising it is fine, and the remainder becomes a credit.
	naik, err := testApp.pembayaran.Update(ctx(), &model.UpdatePembayaranUtangRequest{
		ID: pembayaran.ID, ActorID: f.actor,
		Jumlah: model.Optional[string]{Present: true, Value: ptr("500000")},
	})
	if err != nil {
		t.Fatalf("naikkan jumlah: %v", err)
	}

	if naik.SisaBelumDialokasikan != "100000.00" {
		t.Errorf("sisa_belum_dialokasikan = %s, want 100000.00", naik.SisaBelumDialokasikan)
	}

	posted, err := testApp.pembayaran.Posting(ctx(), &model.PostingPembayaranUtangRequest{
		ID: pembayaran.ID, ActorID: f.actor,
	})
	if err != nil {
		t.Fatalf("posting: %v", err)
	}

	if posted.PostedAt == nil {
		t.Error("posted_at kosong setelah posting")
	}

	// Posting twice would settle the invoice twice.
	_, err = testApp.pembayaran.Posting(ctx(), &model.PostingPembayaranUtangRequest{
		ID: pembayaran.ID, ActorID: f.actor,
	})
	assertKind(t, err, model.KindConflict)

	// A posted payment rejects both an edit and a reallocation.
	_, err = testApp.pembayaran.Update(ctx(), &model.UpdatePembayaranUtangRequest{
		ID: pembayaran.ID, ActorID: f.actor,
		Keterangan: model.Optional[string]{Present: true, Value: ptr("catatan")},
	})
	assertKind(t, err, model.KindConflict)

	_, err = testApp.pembayaran.ReplaceAlokasi(ctx(), &model.ReplacePembayaranUtangAlokasiRequest{
		ID: pembayaran.ID, ActorID: f.actor,
		Alokasi: []model.PembayaranUtangAlokasiRequest{{
			IDPembelian: pembelian.ID, Jumlah: "100000",
		}},
	})
	assertKind(t, err, model.KindConflict)

	// The clearing endpoints are for giro only.
	_, err = testApp.pembayaran.CairkanGiro(ctx(), &model.CairkanGiroRequest{
		ID: pembayaran.ID, ActorID: f.actor, TanggalCair: "2026-08-21",
	})
	assertKind(t, err, model.KindInvalid)
}

// Replacing the allocation set wholesale, and the empty case that means "allocate none".
func TestReplaceAlokasiMenggantiSeluruhnya(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := utangFixture(t, testApp)

	kedua := draftSederhana(t, testApp, f, "100", nil, nil)
	lain := ajukanDanPosting(t, testApp, f, kedua.ID)

	pembayaran, err := testApp.pembayaran.Create(ctx(), &model.CreatePembayaranUtangRequest{
		ActorID:    f.actor,
		IDSupplier: f.supplier,
		Tanggal:    "2026-08-20",
		Metode:     "TRANSFER",
		Jumlah:     "500000",
		Alokasi: []model.PembayaranUtangAlokasiRequest{{
			IDPembelian: pembelian.ID, Jumlah: "500000",
		}},
	})
	if err != nil {
		t.Fatalf("create pembayaran: %v", err)
	}

	diganti, err := testApp.pembayaran.ReplaceAlokasi(ctx(), &model.ReplacePembayaranUtangAlokasiRequest{
		ID: pembayaran.ID, ActorID: f.actor,
		Alokasi: []model.PembayaranUtangAlokasiRequest{
			{IDPembelian: pembelian.ID, Jumlah: "200000"},
			{IDPembelian: lain.ID, Jumlah: "300000"},
		},
	})
	if err != nil {
		t.Fatalf("replace alokasi: %v", err)
	}

	if len(diganti.Alokasi) != 2 || diganti.JumlahDialokasikan != "500000.00" {
		t.Errorf("%d alokasi dengan total %s, want 2 dan 500000.00",
			len(diganti.Alokasi), diganti.JumlahDialokasikan)
	}

	// The invoice numbers come back joined, so a payment screen can name what it settles.
	if diganti.Alokasi[0].NomorPembelian == "" {
		t.Error("nomor_pembelian kosong pada alokasi")
	}

	// Emptying it is legal: the whole amount becomes a credit with the supplier.
	kosong, err := testApp.pembayaran.ReplaceAlokasi(ctx(), &model.ReplacePembayaranUtangAlokasiRequest{
		ID: pembayaran.ID, ActorID: f.actor,
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

// GET /supplier/{id}/utang is the working list for building a payment: open invoices,
// oldest first, with what each still owes.
func TestUtangSupplierHanyaFakturYangMasihTerbuka(t *testing.T) {
	testApp := newApp(t)
	f, pertama := utangFixture(t, testApp)

	kedua := draftSederhana(t, testApp, f, "50", nil, nil)
	lain := ajukanDanPosting(t, testApp, f, kedua.ID)

	// A DRAFT invoice is not a debt and must not appear.
	draftSederhana(t, testApp, f, "10", nil, nil)

	daftar, paging, err := testApp.supplier.Utang(ctx(), &model.ListUtangSupplierRequest{
		IDSupplier: f.supplier,
	})
	if err != nil {
		t.Fatalf("utang supplier: %v", err)
	}

	if paging.TotalItem != 2 {
		t.Fatalf("total_item = %d, want 2 (draft tidak ikut)", paging.TotalItem)
	}

	// Oldest first: this list is a queue to work through, not a history to read.
	if daftar[0].IDPembelian != pertama.ID || daftar[1].IDPembelian != lain.ID {
		t.Errorf("urutan = %d, %d; want %d, %d (terlama dulu)",
			daftar[0].IDPembelian, daftar[1].IDPembelian, pertama.ID, lain.ID)
	}

	if daftar[0].SisaUtang != "1000000.00" || daftar[1].SisaUtang != "500000.00" {
		t.Errorf("sisa_utang = %s, %s; want 1000000.00 dan 500000.00",
			daftar[0].SisaUtang, daftar[1].SisaUtang)
	}

	// Settling one takes it off the list.
	bayar(t, testApp, f, pertama.ID, "1000000")

	terbuka, paging, err := testApp.supplier.Utang(ctx(), &model.ListUtangSupplierRequest{
		IDSupplier: f.supplier,
	})
	if err != nil {
		t.Fatalf("utang supplier: %v", err)
	}

	if paging.TotalItem != 1 || terbuka[0].IDPembelian != lain.ID {
		t.Errorf("%d faktur terbuka, pertama %d; want 1 dan %d",
			paging.TotalItem, terbuka[0].IDPembelian, lain.ID)
	}

	// termasuk_lunas brings the settled one back, and it reports zero owing.
	semua, paging, err := testApp.supplier.Utang(ctx(), &model.ListUtangSupplierRequest{
		IDSupplier: f.supplier, TermasukLunas: true,
	})
	if err != nil {
		t.Fatalf("utang supplier termasuk lunas: %v", err)
	}

	if paging.TotalItem != 2 {
		t.Fatalf("total_item = %d, want 2", paging.TotalItem)
	}

	if semua[0].SisaUtang != "0.00" || semua[0].StatusPembayaran != "LUNAS" {
		t.Errorf("faktur lunas: sisa_utang %s status %q, want 0.00 dan LUNAS",
			semua[0].SisaUtang, semua[0].StatusPembayaran)
	}

	// An unknown supplier is a 404, not an empty page: "owes nothing" and "does not exist"
	// are different facts.
	_, _, err = testApp.supplier.Utang(ctx(), &model.ListUtangSupplierRequest{
		IDSupplier: f.supplier + 99999,
	})
	assertKind(t, err, model.KindNotFound)
}

// The list filters and pages, and its ordering ends in a unique column.
func TestListPembayaranUtangFilterDanPaginasi(t *testing.T) {
	testApp := newApp(t)
	f, pembelian := utangFixture(t, testApp)

	// Five drafts of 1.000 each, all dated the same day, so the tiebreaker is exercised.
	for range 5 {
		if _, err := testApp.pembayaran.Create(ctx(), &model.CreatePembayaranUtangRequest{
			ActorID:    f.actor,
			IDSupplier: f.supplier,
			Tanggal:    "2026-08-20",
			Metode:     "TUNAI",
			Jumlah:     "1000",
			Alokasi: []model.PembayaranUtangAlokasiRequest{{
				IDPembelian: pembelian.ID, Jumlah: "1000",
			}},
		}); err != nil {
			t.Fatalf("create pembayaran: %v", err)
		}
	}

	_, paging, err := testApp.pembayaran.Search(ctx(), &model.ListPembayaranUtangRequest{})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	if paging.TotalItem != 5 {
		t.Errorf("total_item = %d, want 5", paging.TotalItem)
	}

	kosong, _, err := testApp.pembayaran.Search(ctx(), &model.ListPembayaranUtangRequest{
		Status: "POSTED",
	})
	if err != nil {
		t.Fatalf("search POSTED: %v", err)
	}

	if len(kosong) != 0 {
		t.Errorf("filter POSTED mengembalikan %d, want 0", len(kosong))
	}

	tunai, _, err := testApp.pembayaran.Search(ctx(), &model.ListPembayaranUtangRequest{
		Metode: "TUNAI",
	})
	if err != nil {
		t.Fatalf("search TUNAI: %v", err)
	}

	if len(tunai) != 5 {
		t.Errorf("filter TUNAI mengembalikan %d, want 5", len(tunai))
	}

	// A list read carries no alokasi key at all rather than an empty array — which for
	// this document would be a meaningful and wrong claim.
	if tunai[0].Alokasi != nil {
		t.Error("list membawa alokasi, want kunci hilang")
	}

	giroSaja, _, err := testApp.pembayaran.Search(ctx(), &model.ListPembayaranUtangRequest{
		StatusGiro: "BELUM_CAIR",
	})
	if err != nil {
		t.Fatalf("search status_giro: %v", err)
	}

	if len(giroSaja) != 0 {
		t.Errorf("filter status_giro mengembalikan %d, want 0", len(giroSaja))
	}

	terlihat := map[int64]bool{}
	for halaman := 1; halaman <= 3; halaman++ {
		batch, _, err := testApp.pembayaran.Search(ctx(), &model.ListPembayaranUtangRequest{
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
