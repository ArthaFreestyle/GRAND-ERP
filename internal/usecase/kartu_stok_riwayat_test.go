package usecase_test

// isu #22 fase 1: GET /product/{id}/kartu-stok. What these pin cannot be proven with
// a mock — the running balance and the CASE-translated document number both come out
// of a real query over a real kartu_stok chain that pembelian, mutasi, penjualan, and
// their reversals actually wrote.

import (
	"testing"

	"Arthafreestyle/ERP/internal/model"
)

func TestKartuStokRiwayatUrutanDanSaldoBerjalan(t *testing.T) {
	testApp := newApp(t)
	testApp, s := stokAwalMutasi(t, testApp, "100")

	mutasi := buatMutasi(t, testApp, s, "20")
	postingMutasi(t, testApp, s, mutasi.ID)

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

	list, paging, err := testApp.product.KartuStok(ctx(), &model.ListKartuStokRequest{
		PageRequest: model.PageRequest{Page: 1, Size: 20},
		IDProduct:   s.product,
		IDRuang:     s.ruang,
	})
	if err != nil {
		t.Fatalf("kartu stok: %v", err)
	}

	if paging.TotalItem != 3 {
		t.Fatalf("total_item = %d, want 3", paging.TotalItem)
	}
	if len(list) != 3 {
		t.Fatalf("len(list) = %d, want 3", len(list))
	}

	want := []struct {
		jenis  string
		masuk  int64
		keluar int64
		akhir  int64
	}{
		{"PEMBELIAN", 100, 0, 100},
		{"MUTASI_KELUAR", 0, 20, 80},
		{"PENJUALAN", 0, 10, 70},
	}

	for i, w := range want {
		baris := list[i]

		if baris.JenisTransaksi != w.jenis {
			t.Errorf("baris %d: jenis_transaksi = %q, want %q", i, baris.JenisTransaksi, w.jenis)
		}
		if baris.StokMasuk != w.masuk {
			t.Errorf("baris %d: stok_masuk = %d, want %d", i, baris.StokMasuk, w.masuk)
		}
		if baris.StokKeluar != w.keluar {
			t.Errorf("baris %d: stok_keluar = %d, want %d", i, baris.StokKeluar, w.keluar)
		}
		if baris.StokAkhir != w.akhir {
			t.Errorf("baris %d: stok_akhir = %d, want %d", i, baris.StokAkhir, w.akhir)
		}
		if i > 0 && baris.StokAwal != list[i-1].StokAkhir {
			t.Errorf("baris %d: stok_awal (%d) tidak menyambung dengan stok_akhir baris sebelumnya (%d)",
				i, baris.StokAwal, list[i-1].StokAkhir)
		}
		if baris.NomorDokumen == nil || *baris.NomorDokumen == "" {
			t.Errorf("baris %d: nomor_dokumen kosong, ref_table %q tidak diterjemahkan", i, baris.RefTable)
		}
		if baris.IDKartuStokAsal != nil {
			t.Errorf("baris %d: bukan pembalik, id_kartu_stok_asal seharusnya nil", i)
		}
	}
}

// A cancelled document's reversing row must be identifiable by id_kartu_stok_asal
// alone, without guessing from jenis_transaksi — the field the issue calls out by
// name.
func TestKartuStokRiwayatMenandaiBarisPembalik(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	draft := draftSederhana(t, testApp, f, "10", nil, nil)
	posted := ajukanDanPosting(t, testApp, f, draft.ID)

	if _, err := testApp.pembelian.Batal(ctx(), &model.BatalPembelianRequest{
		ID: posted.ID, ActorID: f.actor, AlasanBatal: "salah input",
	}); err != nil {
		t.Fatalf("batal pembelian: %v", err)
	}

	list, _, err := testApp.product.KartuStok(ctx(), &model.ListKartuStokRequest{
		PageRequest: model.PageRequest{Page: 1, Size: 20},
		IDProduct:   f.product,
		IDRuang:     f.ruang,
	})
	if err != nil {
		t.Fatalf("kartu stok: %v", err)
	}

	if len(list) != 2 {
		t.Fatalf("len(list) = %d, want 2 (posting + pembalik)", len(list))
	}

	if list[0].IDKartuStokAsal != nil {
		t.Errorf("baris asli tidak seharusnya menunjuk kemana pun")
	}
	if list[1].IDKartuStokAsal == nil || *list[1].IDKartuStokAsal != list[0].ID {
		t.Errorf("baris pembalik harus menunjuk ke baris asli (id %d), got %v", list[0].ID, list[1].IDKartuStokAsal)
	}
	if list[1].JenisTransaksi != "PEMBATALAN_TRANSAKSI" {
		t.Errorf("jenis_transaksi baris pembalik = %q, want PEMBATALAN_TRANSAKSI", list[1].JenisTransaksi)
	}
}

func TestKartuStokRiwayatRuangTidakDikenal404(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	_, _, err := testApp.product.KartuStok(ctx(), &model.ListKartuStokRequest{
		PageRequest: model.PageRequest{Page: 1, Size: 20},
		IDProduct:   f.product,
		IDRuang:     999_999,
	})

	assertKind(t, err, model.KindNotFound)
}

func TestKartuStokRiwayatProductTidakDikenal404(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	_, _, err := testApp.product.KartuStok(ctx(), &model.ListKartuStokRequest{
		PageRequest: model.PageRequest{Page: 1, Size: 20},
		IDProduct:   999_999,
		IDRuang:     f.ruang,
	})

	assertKind(t, err, model.KindNotFound)
}

// A room outside the caller's active unit_kerja answers an empty page, not 404 —
// isu #12 fase 6 applied to this new read, list-shaped rather than Get-shaped.
func TestKartuStokRiwayatRuangDiLuarUnitAktifKosong(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)
	draft := draftSederhana(t, testApp, f, "10", nil, nil)
	ajukanDanPosting(t, testApp, f, draft.ID)

	unitLain := createUnit(t, testApp, "Unit Lain Kartu Stok")

	list, paging, err := testApp.product.KartuStok(ctx(), &model.ListKartuStokRequest{
		PageRequest:      model.PageRequest{Page: 1, Size: 20},
		IDProduct:        f.product,
		IDRuang:          f.ruang,
		AktifIDUnitKerja: &unitLain,
	})
	if err != nil {
		t.Fatalf("kartu stok dari unit lain: %v", err)
	}
	if len(list) != 0 || paging.TotalItem != 0 {
		t.Errorf("ruang di luar unit aktif seharusnya kosong, got %d rows", len(list))
	}
}
