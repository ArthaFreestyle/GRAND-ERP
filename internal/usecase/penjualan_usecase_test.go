package usecase_test

// Penjualan (isu #10) — the sixth document to write kartu_stok, and the first
// whose goods leave to an outside party with money moving on the other side. These
// run against a real PostgreSQL because most of what they assert lives there: HPP
// copied from the trigger's own RETURNING, the negative-stock guard, the closed-
// period trigger, and the advisory lock per (barang, ruang) that two concurrent
// postings for the same product would otherwise deadlock on.

import (
	"fmt"
	"sync"
	"testing"

	"Arthafreestyle/ERP/internal/model"
)

// penjualanSetup adds a customer to the purchase fixture, for the KREDIT-nota
// tests.
type penjualanSetup struct {
	fixture
	pelanggan int64
}

// stokAwalPenjualan posts a purchase of qty pcs at 10.000 each into f.ruang, and
// returns a setup with a customer ready.
func stokAwalPenjualan(t *testing.T, testApp *app, qty string) (*app, penjualanSetup) {
	t.Helper()

	f := pembelianFixture(t, testApp)

	beli, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
		ActorID: f.actor, Tanggal: "2026-08-11", IDSupplier: f.supplier, IDRuang: f.ruang,
		Detail: []model.PembelianDetailRequest{
			{IDProduct: f.product, IDSatuanInput: f.pcs, QtyFaktur: qty, HargaSatuanInput: "10000"},
		},
	})
	if err != nil {
		t.Fatalf("create pembelian: %v", err)
	}

	ajukanDanPosting(t, testApp, f, beli.ID)

	pelanggan, err := testApp.pelanggan.Create(ctx(), &model.CreatePelangganRequest{ActorID: f.actor, Nama: "Toko Maju"})
	if err != nil {
		t.Fatalf("create pelanggan: %v", err)
	}

	return testApp, penjualanSetup{fixture: f, pelanggan: pelanggan.ID}
}

// buatPenjualanTunai opens a DRAFT cash nota for one line of s.product.
func buatPenjualanTunai(t *testing.T, testApp *app, s penjualanSetup, qty, harga string) *model.PenjualanResponse {
	t.Helper()

	penjualan, err := testApp.penjualan.Create(ctx(), &model.CreatePenjualanRequest{
		ActorID: s.actor, Tanggal: "2026-08-15", IDRuang: s.ruang,
		Detail: []model.PenjualanDetailRequest{{
			IDProduct: s.product, IDSatuanInput: s.pcs, QtyInput: qty, HargaSatuanInput: harga,
		}},
	})
	if err != nil {
		t.Fatalf("create penjualan: %v", err)
	}

	return penjualan
}

func postingPenjualan(t *testing.T, testApp *app, s penjualanSetup, id int64) *model.PenjualanResponse {
	t.Helper()

	posted, err := testApp.penjualan.Posting(ctx(), &model.PostingPenjualanRequest{
		ID: id, ActorID: s.actor,
	})
	if err != nil {
		t.Fatalf("posting penjualan: %v", err)
	}

	return posted
}

// The jebakan utama the issue calls the most expensive one to get wrong: HPP is
// never typed, it is copied from what the outgoing kartu_stok row's RETURNING
// actually reported. Ten pieces leave a room whose moving average is 10.000/pcs, so
// hpp_satuan_dasar, hpp_total, and total_hpp all have to agree with nilai_keluar to
// the cent — not with a figure recomputed in Go.
func TestPenjualanHPPBarisDanTotalHPPSamaDenganKartuStok(t *testing.T) {
	testApp, s := stokAwalPenjualan(t, newApp(t), "100")

	penjualan := buatPenjualanTunai(t, testApp, s, "10", "15000")
	posted := postingPenjualan(t, testApp, s, penjualan.ID)

	// nilai_akhir is the room's ending balance after the sale (90 pcs left at
	// 10.000 = 900000.00), not what this line's own kartu_stok row recorded
	// leaving — that figure is nilai_keluar, read directly off the row this
	// posting wrote.
	var nilaiKeluar string
	if err := testDB.QueryRow(
		`SELECT nilai_keluar::TEXT FROM kartu_stok WHERE ref_table = 'penjualan' AND ref_id_transaksi = $1`,
		penjualan.ID,
	).Scan(&nilaiKeluar); err != nil {
		t.Fatalf("read nilai_keluar: %v", err)
	}

	if posted.Detail[0].HPPSatuanDasar == nil || *posted.Detail[0].HPPSatuanDasar != "10000.0000" {
		t.Errorf("hpp_satuan_dasar = %v, want 10000.0000", posted.Detail[0].HPPSatuanDasar)
	}

	if posted.Detail[0].HPPTotal == nil {
		t.Fatalf("hpp_total = nil, want %s", nilaiKeluar)
	}
	if *posted.Detail[0].HPPTotal != nilaiKeluar {
		t.Errorf("hpp_total = %s, want sama dengan nilai_keluar kartu_stok %s", *posted.Detail[0].HPPTotal, nilaiKeluar)
	}

	if posted.TotalHPP == nil || *posted.TotalHPP != "100000.00" {
		t.Errorf("total_hpp = %v, want 100000.00 (10 x 10.000)", posted.TotalHPP)
	}

	// Margin falls straight out of the two totals — the whole reason total_hpp
	// exists at all.
	if posted.Total != "150000.00" {
		t.Fatalf("total = %s, want 150000.00 (10 x 15.000)", posted.Total)
	}
}

// More than the room holds is refused, the message names the product and the room,
// and nothing is left in kartu_stok — this is the second module after pemakaian
// where a negative-stock rejection is an everyday counter event, not a theoretical
// defence.
func TestPenjualanDitolakSaatSaldoTidakCukup(t *testing.T) {
	testApp, s := stokAwalPenjualan(t, newApp(t), "10")

	penjualan := buatPenjualanTunai(t, testApp, s, "11", "15000")

	_, err := testApp.penjualan.Posting(ctx(), &model.PostingPenjualanRequest{
		ID: penjualan.ID, ActorID: s.actor,
	})
	assertKind(t, err, model.KindInvalid)
	assertPesanMemuat(t, err, "Kertas A4")
	assertPesanMemuat(t, err, fmt.Sprintf("ruang %d", s.ruang))

	if stok, _ := saldoStok(t, s.product, s.ruang); stok != 10 {
		t.Errorf("stok = %d setelah posting ditolak, want tetap 10", stok)
	}
}

// A TUNAI nota that posts is settled the instant it does — the money changed hands
// at the counter, and RecalculateStatusPembayaran says LUNAS without ever looking
// for an allocation, because a cash sale has none.
func TestPenjualanTunaiPostedLangsungLunas(t *testing.T) {
	testApp, s := stokAwalPenjualan(t, newApp(t), "100")

	penjualan := buatPenjualanTunai(t, testApp, s, "5", "15000")

	if penjualan.StatusPembayaran != "BELUM" {
		t.Fatalf("status_pembayaran draft = %q, want BELUM", penjualan.StatusPembayaran)
	}

	posted := postingPenjualan(t, testApp, s, penjualan.ID)

	if posted.StatusPembayaran != "LUNAS" {
		t.Errorf("status_pembayaran = %q setelah posting TUNAI, want LUNAS", posted.StatusPembayaran)
	}
}

// A KREDIT nota that posts stays BELUM: penerimaan_pembayaran does not exist yet,
// so there is nothing that could have settled it.
func TestPenjualanKreditPostedTetapBelum(t *testing.T) {
	testApp, s := stokAwalPenjualan(t, newApp(t), "100")

	penjualan, err := testApp.penjualan.Create(ctx(), &model.CreatePenjualanRequest{
		ActorID: s.actor, Tanggal: "2026-08-15", IDRuang: s.ruang,
		IDPelanggan: &s.pelanggan, JenisPembayaran: "KREDIT",
		Detail: []model.PenjualanDetailRequest{{
			IDProduct: s.product, IDSatuanInput: s.pcs, QtyInput: "5", HargaSatuanInput: "15000",
		}},
	})
	if err != nil {
		t.Fatalf("create penjualan kredit: %v", err)
	}

	posted := postingPenjualan(t, testApp, s, penjualan.ID)

	if posted.StatusPembayaran != "BELUM" {
		t.Errorf("status_pembayaran = %q setelah posting KREDIT, want tetap BELUM", posted.StatusPembayaran)
	}
}

// penjualan_kredit_pelanggan_check is the database's own guard, but the message
// here is what names the field instead of arriving as an unlabelled CHECK
// violation.
func TestPenjualanKreditTanpaPelangganDitolak(t *testing.T) {
	testApp, s := stokAwalPenjualan(t, newApp(t), "100")

	_, err := testApp.penjualan.Create(ctx(), &model.CreatePenjualanRequest{
		ActorID: s.actor, Tanggal: "2026-08-15", IDRuang: s.ruang, JenisPembayaran: "KREDIT",
		Detail: []model.PenjualanDetailRequest{{
			IDProduct: s.product, IDSatuanInput: s.pcs, QtyInput: "5", HargaSatuanInput: "15000",
		}},
	})
	assertKind(t, err, model.KindInvalid)
	assertPesanMemuat(t, err, "id_pelanggan")
}

// Posting into a closed month is refused and the refusal names the month, exactly
// as every other stock-writing module does; cancelling a nota whose period has
// since closed still succeeds and lands in the current period.
func TestPostingPenjualanKePeriodeTutupMenyebutPeriodenya(t *testing.T) {
	testApp, s := stokAwalPenjualan(t, newApp(t), "100")

	lalu := awalBulanLalu(t)

	penjualan, err := testApp.penjualan.Create(ctx(), &model.CreatePenjualanRequest{
		ActorID: s.actor, Tanggal: lalu.Format("2006-01-02"), IDRuang: s.ruang,
		Detail: []model.PenjualanDetailRequest{{
			IDProduct: s.product, IDSatuanInput: s.pcs, QtyInput: "5", HargaSatuanInput: "15000",
		}},
	})
	if err != nil {
		t.Fatalf("create penjualan bertanggal lalu: %v", err)
	}

	tutupPeriode(t, testApp, s.actor, lalu.Year(), int(lalu.Month()))

	_, err = testApp.penjualan.Posting(ctx(), &model.PostingPenjualanRequest{
		ID: penjualan.ID, ActorID: s.actor,
	})
	assertKind(t, err, model.KindInvalid)
	assertPesanMemuat(t, err, fmt.Sprintf("%04d-%02d", lalu.Year(), int(lalu.Month())))

	if stok, _ := saldoStok(t, s.product, s.ruang); stok != 100 {
		t.Errorf("stok = %d setelah posting ditolak, want tetap 100", stok)
	}
}

// Cancellation is dated today rather than on the document, so a nota whose period
// has since closed can still be voided — into the current period.
func TestBatalPenjualanDariPeriodeTutupMasukPeriodeBerjalan(t *testing.T) {
	testApp, s := stokAwalPenjualan(t, newApp(t), "100")

	lalu := awalBulanLalu(t)

	penjualan, err := testApp.penjualan.Create(ctx(), &model.CreatePenjualanRequest{
		ActorID: s.actor, Tanggal: lalu.Format("2006-01-02"), IDRuang: s.ruang,
		Detail: []model.PenjualanDetailRequest{{
			IDProduct: s.product, IDSatuanInput: s.pcs, QtyInput: "5", HargaSatuanInput: "15000",
		}},
	})
	if err != nil {
		t.Fatalf("create penjualan bertanggal lalu: %v", err)
	}

	posted := postingPenjualan(t, testApp, s, penjualan.ID)

	tutupPeriode(t, testApp, s.actor, lalu.Year(), int(lalu.Month()))

	dibatalkan, err := testApp.penjualan.Batal(ctx(), &model.BatalPenjualanRequest{
		ID: posted.ID, ActorID: s.actor, AlasanBatal: "salah input",
	})
	if err != nil {
		t.Fatalf("batal penjualan dari periode tertutup: %v", err)
	}

	if dibatalkan.Status != "BATAL" {
		t.Fatalf("status = %q, want BATAL", dibatalkan.Status)
	}

	if stok, _ := saldoStok(t, s.product, s.ruang); stok != 100 {
		t.Errorf("stok setelah batal = %d, want kembali 100", stok)
	}
}

// Posting twice is refused by HasRef even if the status column is somehow forced
// back — the backstop that does not depend on the column being right.
func TestPostingPenjualanDuaKaliDitolak(t *testing.T) {
	testApp, s := stokAwalPenjualan(t, newApp(t), "100")

	penjualan := buatPenjualanTunai(t, testApp, s, "5", "15000")
	postingPenjualan(t, testApp, s, penjualan.ID)

	if _, err := testDB.Exec(
		`UPDATE penjualan SET status = 'DRAFT' WHERE id = $1`, penjualan.ID,
	); err != nil {
		t.Fatalf("force status back to DRAFT: %v", err)
	}

	_, err := testApp.penjualan.Posting(ctx(), &model.PostingPenjualanRequest{
		ID: penjualan.ID, ActorID: s.actor,
	})
	assertKind(t, err, model.KindConflict)
}

// Voiding a posted nota returns the goods to the room they left from.
func TestBatalPenjualanMengembalikanStok(t *testing.T) {
	testApp, s := stokAwalPenjualan(t, newApp(t), "100")

	penjualan := buatPenjualanTunai(t, testApp, s, "30", "15000")
	postingPenjualan(t, testApp, s, penjualan.ID)

	if stok, _ := saldoStok(t, s.product, s.ruang); stok != 70 {
		t.Fatalf("stok setelah posting = %d, want 70", stok)
	}

	dibatalkan, err := testApp.penjualan.Batal(ctx(), &model.BatalPenjualanRequest{
		ID: penjualan.ID, ActorID: s.actor, AlasanBatal: "salah input",
	})
	if err != nil {
		t.Fatalf("batal penjualan: %v", err)
	}

	if dibatalkan.Status != "BATAL" {
		t.Fatalf("status = %q, want BATAL", dibatalkan.Status)
	}

	if stok, _ := saldoStok(t, s.product, s.ruang); stok != 100 {
		t.Errorf("stok setelah batal = %d, want kembali 100", stok)
	}
}

// The same product on two lines with different input units — a legitimate way to
// type a nota — has its quota summed before the room's balance is checked, exactly
// as mutasi and pemakaian already do.
func TestPenjualanSatuProdukDuaBarisSatuanBerbedaKuotaDijumlahkan(t *testing.T) {
	testApp, s := stokAwalPenjualan(t, newApp(t), "100")

	penjualan, err := testApp.penjualan.Create(ctx(), &model.CreatePenjualanRequest{
		ActorID: s.actor, Tanggal: "2026-08-15", IDRuang: s.ruang,
		Detail: []model.PenjualanDetailRequest{
			{IDProduct: s.product, IDSatuanInput: s.pcs, QtyInput: "6", HargaSatuanInput: "15000"},
			{IDProduct: s.product, IDSatuanInput: s.dus, QtyInput: "8", HargaSatuanInput: "150000"},
		},
	})
	if err != nil {
		t.Fatalf("create penjualan dua baris: %v", err)
	}

	// 6 pcs + 8 dus (12 pcs/dus) = 6 + 96 = 102, which the 100-piece room cannot
	// cover — each line alone fits, only the sum does not.
	_, err = testApp.penjualan.Posting(ctx(), &model.PostingPenjualanRequest{
		ID: penjualan.ID, ActorID: s.actor,
	})
	assertKind(t, err, model.KindInvalid)

	if stok, _ := saldoStok(t, s.product, s.ruang); stok != 100 {
		t.Errorf("stok = %d setelah posting ditolak, want tetap 100", stok)
	}
}

// id_harga_jual is a proposal, and only a version actually in force for this
// line's own product, satuan, and the document's date may be referenced.
func TestPenjualanIDHargaJualHarusBerlakuUntukBarisnya(t *testing.T) {
	testApp, s := stokAwalPenjualan(t, newApp(t), "100")

	produk, err := testApp.product.AddHargaJual(ctx(), &model.AddProductHargaJualRequest{
		IDProduct: s.product, ActorID: s.actor,
		IDSatuan: s.pcs, Harga: "15000", BerlakuDari: "2026-08-01",
	})
	if err != nil {
		t.Fatalf("add harga jual: %v", err)
	}

	idHarga := produk.HargaJual[len(produk.HargaJual)-1].ID

	// Valid: the version actually resolves for this product+satuan on this date.
	penjualan, err := testApp.penjualan.Create(ctx(), &model.CreatePenjualanRequest{
		ActorID: s.actor, Tanggal: "2026-08-15", IDRuang: s.ruang,
		Detail: []model.PenjualanDetailRequest{{
			IDProduct: s.product, IDSatuanInput: s.pcs, QtyInput: "1",
			IDHargaJual: &idHarga, HargaSatuanInput: "15000",
		}},
	})
	if err != nil {
		t.Fatalf("create penjualan dengan id_harga_jual valid: %v", err)
	}

	if penjualan.Detail[0].IDHargaJual == nil || *penjualan.Detail[0].IDHargaJual != idHarga {
		t.Errorf("id_harga_jual = %v, want %d", penjualan.Detail[0].IDHargaJual, idHarga)
	}

	// Invalid: a version id that exists but is for a date long before this document.
	_, err = testApp.penjualan.Create(ctx(), &model.CreatePenjualanRequest{
		ActorID: s.actor, Tanggal: "2020-01-01", IDRuang: s.ruang,
		Detail: []model.PenjualanDetailRequest{{
			IDProduct: s.product, IDSatuanInput: s.pcs, QtyInput: "1",
			IDHargaJual: &idHarga, HargaSatuanInput: "15000",
		}},
	})
	assertKind(t, err, model.KindInvalid)
	assertPesanMemuat(t, err, "id_harga_jual")
}

// Two penjualan documents naming the same two products in reversed line order,
// posted at the same moment, must not deadlock — a mutasi/pemakaian-shaped ABBA
// that can happen here too, because the trigger takes one advisory lock per insert
// rather than one per document.
func TestDuaPenjualanBersamaanProdukSamaTidakDeadlock(t *testing.T) {
	testApp, s := stokAwalPenjualan(t, newApp(t), "200")

	productB, err := testApp.product.Create(ctx(), &model.CreateProductRequest{
		ActorID: s.actor, KodeBarang: "BRG-002", Nama: "Tinta Printer", IDSatuanDasar: s.pcs,
	})
	if err != nil {
		t.Fatalf("create product B: %v", err)
	}

	beli, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
		ActorID: s.actor, Tanggal: "2026-08-11", IDSupplier: s.supplier, IDRuang: s.ruang,
		Detail: []model.PembelianDetailRequest{
			{IDProduct: productB.ID, IDSatuanInput: s.pcs, QtyFaktur: "200", HargaSatuanInput: "10000"},
		},
	})
	if err != nil {
		t.Fatalf("create pembelian product B: %v", err)
	}
	ajukanDanPosting(t, testApp, s.fixture, beli.ID)

	const ronde = 8

	ids := make([]int64, 0, ronde*2)

	buat := func(urutan [2]int64) int64 {
		penjualan, err := testApp.penjualan.Create(ctx(), &model.CreatePenjualanRequest{
			ActorID: s.actor, Tanggal: "2026-08-20", IDRuang: s.ruang,
			Detail: []model.PenjualanDetailRequest{
				{IDProduct: urutan[0], IDSatuanInput: s.pcs, QtyInput: "1", HargaSatuanInput: "15000"},
				{IDProduct: urutan[1], IDSatuanInput: s.pcs, QtyInput: "1", HargaSatuanInput: "15000"},
			},
		})
		if err != nil {
			t.Fatalf("create penjualan: %v", err)
		}

		return penjualan.ID
	}

	for range ronde {
		ids = append(ids, buat([2]int64{s.product, productB.ID}))
		ids = append(ids, buat([2]int64{productB.ID, s.product}))
	}

	mulai := make(chan struct{})
	gagal := make(chan error, len(ids))

	var wg sync.WaitGroup

	for _, id := range ids {
		wg.Add(1)

		go func(id int64) {
			defer wg.Done()

			<-mulai

			if _, err := testApp.penjualan.Posting(ctx(), &model.PostingPenjualanRequest{
				ID: id, ActorID: s.actor,
			}); err != nil {
				gagal <- fmt.Errorf("posting penjualan %d: %w", id, err)
			}
		}(id)
	}

	close(mulai)
	wg.Wait()
	close(gagal)

	for err := range gagal {
		t.Errorf("%v", err)
	}

	if stok, _ := saldoStok(t, s.product, s.ruang); stok != 200-int64(ronde*2) {
		t.Errorf("stok produk A = %d, want %d", stok, 200-int64(ronde*2))
	}

	if stok, _ := saldoStok(t, productB.ID, s.ruang); stok != 200-int64(ronde*2) {
		t.Errorf("stok produk B = %d, want %d", stok, 200-int64(ronde*2))
	}
}

// plafon_kredit is enforced at posting, under the document's own row lock, and the
// message names the limit and the running receivable — not just "ditolak".
func TestPenjualanPlafonKreditTerlampauiDitolak(t *testing.T) {
	testApp, s := stokAwalPenjualan(t, newApp(t), "100")

	if _, err := testApp.pelanggan.Update(ctx(), &model.UpdatePelangganRequest{
		ID:           s.pelanggan,
		ActorID:      s.actor,
		PlafonKredit: model.Optional[string]{Present: true, Value: ptr("100000")},
	}); err != nil {
		t.Fatalf("set plafon_kredit: %v", err)
	}

	penjualan, err := testApp.penjualan.Create(ctx(), &model.CreatePenjualanRequest{
		ActorID: s.actor, Tanggal: "2026-08-15", IDRuang: s.ruang,
		IDPelanggan: &s.pelanggan, JenisPembayaran: "KREDIT",
		Detail: []model.PenjualanDetailRequest{{
			IDProduct: s.product, IDSatuanInput: s.pcs, QtyInput: "10", HargaSatuanInput: "15000",
		}},
	})
	if err != nil {
		t.Fatalf("create penjualan kredit: %v", err)
	}

	// 10 x 15.000 = 150.000, which already exceeds the 100.000 limit on its own.
	_, err = testApp.penjualan.Posting(ctx(), &model.PostingPenjualanRequest{
		ID: penjualan.ID, ActorID: s.actor,
	})
	assertKind(t, err, model.KindInvalid)
	assertPesanMemuat(t, err, "plafon_kredit")

	if stok, _ := saldoStok(t, s.product, s.ruang); stok != 100 {
		t.Errorf("stok = %d setelah posting ditolak karena plafon, want tetap 100", stok)
	}
}

// plafon_kredit IS NULL means unlimited, and a nota of any size posts regardless.
func TestPenjualanPlafonKreditNullTidakPernahMenolak(t *testing.T) {
	testApp, s := stokAwalPenjualan(t, newApp(t), "100")

	penjualan, err := testApp.penjualan.Create(ctx(), &model.CreatePenjualanRequest{
		ActorID: s.actor, Tanggal: "2026-08-15", IDRuang: s.ruang,
		IDPelanggan: &s.pelanggan, JenisPembayaran: "KREDIT",
		Detail: []model.PenjualanDetailRequest{{
			IDProduct: s.product, IDSatuanInput: s.pcs, QtyInput: "90", HargaSatuanInput: "150000",
		}},
	})
	if err != nil {
		t.Fatalf("create penjualan kredit: %v", err)
	}

	posted, err := testApp.penjualan.Posting(ctx(), &model.PostingPenjualanRequest{
		ID: penjualan.ID, ActorID: s.actor,
	})
	if err != nil {
		t.Fatalf("posting seharusnya lolos tanpa plafon: %v", err)
	}

	if posted.Status != "POSTED" {
		t.Errorf("status = %q, want POSTED", posted.Status)
	}
}
