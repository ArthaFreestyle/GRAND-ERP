package usecase_test

// isu #22 fase 3: three reports over material that was already computed and stored
// somewhere else. The one behaviour worth its own dedicated test — the date trap
// CLAUDE.md names by name — gets one: a document posted in one period and cancelled
// today must surface in today's movement recap, never the recap covering the period
// it was originally posted in.

import (
	"testing"
	"time"

	"Arthafreestyle/ERP/internal/model"
)

// Nilai persediaan sums the last row of every (product, room) chain, and a retired
// room still holding stock still holds its value.
func TestNilaiPersediaanJumlahkanBarisTerakhirDanRuangPensiun(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	// 100 pcs at 10.000 posts a moving average of exactly 10.000, so the value in
	// stock is simply qty x harga — no averaging across multiple receipts to reason
	// through.
	draft := draftSederhana(t, testApp, f, "100", nil, nil)
	ajukanDanPosting(t, testApp, f, draft.ID)

	// Retire the room the stock actually sits in. ruang has no PATCH, but is_aktif
	// is settable at create — a second room retired from birth stands in for "was
	// retired after it held stock" just as well for this query, which only ever
	// reads is_aktif, never when it changed.
	retired, err := testApp.ruang.Create(ctx(), &model.CreateRuangRequest{
		ActorID: f.actor, NamaRuang: "Gudang Pensiun", IDUnitKerja: f.unitKerja,
	})
	if err != nil {
		t.Fatalf("create ruang: %v", err)
	}

	mutasiKeRetired, err := testApp.mutasi.Create(ctx(), &model.CreateMutasiRequest{
		ActorID: f.actor, Tanggal: "2026-08-15",
		IDRuangAsal: f.ruang, IDRuangTujuan: retired.ID,
		Detail: []model.MutasiDetailRequest{{
			IDProduct: f.product, IDSatuanInput: f.pcs, QtyInput: "40",
		}},
	})
	if err != nil {
		t.Fatalf("create mutasi: %v", err)
	}
	if _, err := testApp.mutasi.Posting(ctx(), &model.PostingMutasiRequest{
		ID: mutasiKeRetired.ID, ActorID: f.actor,
	}); err != nil {
		t.Fatalf("posting mutasi: %v", err)
	}

	list, err := testApp.laporan.NilaiPersediaan(ctx(), &model.ListNilaiPersediaanRequest{
		AktifIDUnitKerja: &f.unitKerja,
	})
	if err != nil {
		t.Fatalf("nilai persediaan: %v", err)
	}

	nilai := map[int64]string{}
	for _, baris := range list {
		nilai[baris.IDRuang] = baris.TotalNilai
	}

	sumber, ada := nilai[f.ruang]
	if !ada {
		t.Fatalf("ruang sumber tidak muncul di laporan")
	}
	if sumber != "600000.00" {
		t.Errorf("nilai persediaan ruang sumber = %s, want 600000.00 (60 pcs x 10000)", sumber)
	}

	tujuan, ada := nilai[retired.ID]
	if !ada {
		t.Fatalf("ruang yang dipensiunkan seharusnya tetap muncul di laporan nilai persediaan")
	}
	if tujuan != "400000.00" {
		t.Errorf("nilai persediaan ruang pensiun = %s, want 400000.00 (40 pcs x 10000)", tujuan)
	}
}

// The date trap: a document posted into an old period, then cancelled today, must
// surface in the movement recap covering today — where the reversal actually
// happened — never in the recap covering the period the original document was dated.
func TestPergerakanPembalikMunculDiPeriodeBerjalanBukanPeriodeDokumen(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	lama := time.Now().AddDate(0, -2, 0)
	tanggalLama := lama.Format("2006-01-02")

	pembelian, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
		ActorID: f.actor, Tanggal: tanggalLama, IDSupplier: f.supplier, IDRuang: f.ruang,
		Detail: []model.PembelianDetailRequest{{
			IDProduct: f.product, IDSatuanInput: f.pcs, QtyFaktur: "10", HargaSatuanInput: "10000",
		}},
	})
	if err != nil {
		t.Fatalf("create pembelian: %v", err)
	}
	posted := ajukanDanPosting(t, testApp, f, pembelian.ID)

	if _, err := testApp.pembelian.Batal(ctx(), &model.BatalPembelianRequest{
		ID: posted.ID, ActorID: f.actor, AlasanBatal: "salah input, dibatalkan bulan berikutnya",
	}); err != nil {
		t.Fatalf("batal pembelian: %v", err)
	}

	hariIni := time.Now().Format("2006-01-02")

	// The recap covering today must show the reversal (PEMBATALAN_TRANSAKSI).
	sekarang, err := testApp.laporan.Pergerakan(ctx(), &model.ListPergerakanRequest{
		Dari: &hariIni, Sampai: &hariIni,
		IDRuang: &f.ruang, IDProduct: &f.product,
	})
	if err != nil {
		t.Fatalf("pergerakan hari ini: %v", err)
	}

	adaPembalikHariIni := false
	for _, baris := range sekarang {
		if baris.JenisTransaksi == "PEMBATALAN_TRANSAKSI" {
			adaPembalikHariIni = true
			if baris.TotalKeluar != 10 {
				t.Errorf("total_keluar pembalik = %d, want 10", baris.TotalKeluar)
			}
		}
		if baris.JenisTransaksi == "PEMBELIAN" {
			t.Errorf("posting asli (periode lama) seharusnya tidak muncul di rekap hari ini")
		}
	}
	if !adaPembalikHariIni {
		t.Fatal("baris pembalik seharusnya muncul di rekap periode berjalan (hari ini), bukan periode dokumen aslinya")
	}

	// The recap covering the document's own (old) month must show the original
	// posting, and must NOT show the reversal — it is dated today, outside this
	// range.
	periodeDokumen, err := testApp.laporan.Pergerakan(ctx(), &model.ListPergerakanRequest{
		Dari: &tanggalLama, Sampai: &tanggalLama,
		IDRuang: &f.ruang, IDProduct: &f.product,
	})
	if err != nil {
		t.Fatalf("pergerakan periode dokumen: %v", err)
	}

	adaAsliDiPeriodeLama := false
	for _, baris := range periodeDokumen {
		if baris.JenisTransaksi == "PEMBELIAN" {
			adaAsliDiPeriodeLama = true
		}
		if baris.JenisTransaksi == "PEMBATALAN_TRANSAKSI" {
			t.Errorf("baris pembalik (dibatalkan hari ini) seharusnya tidak muncul di periode dokumen aslinya")
		}
	}
	if !adaAsliDiPeriodeLama {
		t.Fatal("posting asli seharusnya tetap muncul di rekap periode aslinya")
	}
}

// Laba kotor is a straight SUM(total) - SUM(total_hpp) over POSTED notas, grouped by
// month; a BATAL nota contributes nothing.
func TestLabaKotorMenjumlahkanNotaPostedSajaPerBulan(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	draft := draftSederhana(t, testApp, f, "100", nil, nil)
	ajukanDanPosting(t, testApp, f, draft.ID)

	tanggal := "2026-08-16"

	terjual, err := testApp.penjualan.Create(ctx(), &model.CreatePenjualanRequest{
		ActorID: f.actor, Tanggal: tanggal, IDRuang: f.ruang,
		Detail: []model.PenjualanDetailRequest{{
			IDProduct: f.product, IDSatuanInput: f.pcs, QtyInput: "10", HargaSatuanInput: "15000",
		}},
	})
	if err != nil {
		t.Fatalf("create penjualan: %v", err)
	}
	if _, err := testApp.penjualan.Posting(ctx(), &model.PostingPenjualanRequest{
		ID: terjual.ID, ActorID: f.actor,
	}); err != nil {
		t.Fatalf("posting penjualan: %v", err)
	}

	// A second nota, left DRAFT — never posted, and must not count.
	if _, err := testApp.penjualan.Create(ctx(), &model.CreatePenjualanRequest{
		ActorID: f.actor, Tanggal: tanggal, IDRuang: f.ruang,
		Detail: []model.PenjualanDetailRequest{{
			IDProduct: f.product, IDSatuanInput: f.pcs, QtyInput: "5", HargaSatuanInput: "15000",
		}},
	}); err != nil {
		t.Fatalf("create penjualan draft: %v", err)
	}

	dari, sampai := "2026-08-01", "2026-08-31"
	list, err := testApp.laporan.LabaKotor(ctx(), &model.ListLabaKotorRequest{
		Dari: &dari, Sampai: &sampai, AktifIDUnitKerja: &f.unitKerja,
	})
	if err != nil {
		t.Fatalf("laba kotor: %v", err)
	}

	if len(list) != 1 {
		t.Fatalf("len(list) = %d, want 1 bulan", len(list))
	}
	if list[0].Bulan != "2026-08" {
		t.Errorf("bulan = %s, want 2026-08", list[0].Bulan)
	}
	// 10 pcs x 15000 = 150000 total; HPP 10 pcs x 10000 = 100000; margin 50000.
	if list[0].TotalPenjualan != "150000.00" {
		t.Errorf("total_penjualan = %s, want 150000.00", list[0].TotalPenjualan)
	}
	if list[0].TotalHPP != "100000.0000" && list[0].TotalHPP != "100000.00" {
		t.Errorf("total_hpp = %s, want 100000 (some NUMERIC scale)", list[0].TotalHPP)
	}
	if list[0].LabaKotor != "50000.0000" && list[0].LabaKotor != "50000.00" {
		t.Errorf("laba_kotor = %s, want 50000 (some NUMERIC scale)", list[0].LabaKotor)
	}
}
