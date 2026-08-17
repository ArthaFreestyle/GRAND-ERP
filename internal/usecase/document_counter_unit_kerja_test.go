package usecase_test

// isu #21 fase 1: document_counter gained a per-unit series. These pin exactly
// what the issue's Definition of Done calls out by name: two units issuing in
// the same month never collide and both start at 1, the pre-migration global
// series keeps advancing rather than restarting, a unit with no kode is
// refused with a message naming it, and two concurrent requests for the same
// unit still queue rather than twin — the identical guarantee Next already
// held before this issue, now re-checked against the per-unit branch.

import (
	"strconv"
	"strings"
	"sync"
	"testing"

	"Arthafreestyle/ERP/internal/model"
)

// Two units issuing BL numbers in the same month get two independent series:
// neither collides with the other, and the second unit's series starts at 1
// even though the first unit's series is already ahead.
func TestDocumentCounterDuaUnitTidakBertabrakanDanMulaiDariSatu(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	unitKedua, err := testApp.unitKerja.Create(ctx(), &model.CreateUnitKerjaRequest{
		Kode: ptr("KEDUA"), Nama: "Unit Kedua Counter",
	})
	if err != nil {
		t.Fatalf("create unit kedua: %v", err)
	}
	ruangKedua, err := testApp.ruang.Create(ctx(), &model.CreateRuangRequest{
		NamaRuang: "Gudang Kedua Counter", IDUnitKerja: unitKedua.ID,
	})
	if err != nil {
		t.Fatalf("create ruang kedua: %v", err)
	}

	pertamaFix := draftSederhana(t, testApp, f, "10", nil, nil)
	keduaFix := draftSederhana(t, testApp, f, "10", nil, nil)

	pertamaKedua, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
		ActorID: f.actor, Tanggal: "2026-08-11", IDSupplier: f.supplier, IDRuang: ruangKedua.ID,
		Detail: []model.PembelianDetailRequest{{
			IDProduct: f.product, IDSatuanInput: f.pcs, QtyFaktur: "5", HargaSatuanInput: "10000",
		}},
	})
	if err != nil {
		t.Fatalf("create pembelian unit kedua: %v", err)
	}

	if pertamaFix.Nomor != "BL/FIX/2026/08/0001" {
		t.Errorf("nomor FIX pertama = %q, want BL/FIX/2026/08/0001", pertamaFix.Nomor)
	}
	if keduaFix.Nomor != "BL/FIX/2026/08/0002" {
		t.Errorf("nomor FIX kedua = %q, want BL/FIX/2026/08/0002", keduaFix.Nomor)
	}
	// KEDUA's own series starts at 1, unaffected by FIX already being at 2 —
	// the whole point of keying the series to (prefix, id_unit_kerja, ...)
	// rather than (prefix, ...) alone.
	if pertamaKedua.Nomor != "BL/KEDUA/2026/08/0001" {
		t.Errorf("nomor unit kedua = %q, want BL/KEDUA/2026/08/0001", pertamaKedua.Nomor)
	}
}

// A document_counter row from before this migration (id_unit_kerja NULL) keeps
// advancing rather than restarting at 1 the moment a caller with a global
// active context (or no active context at all) issues a new number in that
// series.
func TestDocumentCounterSeriGlobalLanjutTanpaMengulang(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	// Simulates a pre-#21 counter row: a PU series already five numbers deep,
	// with no unit attached.
	if _, err := testDB.Exec(`
		INSERT INTO document_counter (prefix, tahun, bulan, last_number)
		VALUES ('PU', 2026, 8, 5)
	`); err != nil {
		t.Fatalf("seed seri global lama: %v", err)
	}

	pembayaran, err := testApp.pembayaran.Create(ctx(), &model.CreatePembayaranUtangRequest{
		ActorID: f.actor, IDSupplier: f.supplier, Tanggal: "2026-08-20",
		Metode: "TRANSFER", NamaBank: ptr("Mandiri"), Jumlah: "100000",
	})
	if err != nil {
		t.Fatalf("create pembayaran: %v", err)
	}

	if pembayaran.Nomor != "PU/2026/08/0006" {
		t.Errorf(
			"nomor = %q, want PU/2026/08/0006 (seri global lanjut dari baris lama, tidak mengulang dari 1)",
			pembayaran.Nomor,
		)
	}
}

// A unit with no kode cannot issue a document number at all: the refusal
// names the unit rather than silently falling back to its numeric id.
func TestDocumentCounterUnitTanpaKodeDitolakSaatMenerbitkanNomor(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	unitTanpaKode, err := testApp.unitKerja.Create(ctx(), &model.CreateUnitKerjaRequest{
		Nama: "Unit Tanpa Kode",
	})
	if err != nil {
		t.Fatalf("create unit tanpa kode: %v", err)
	}
	ruangTanpaKode, err := testApp.ruang.Create(ctx(), &model.CreateRuangRequest{
		NamaRuang: "Gudang Tanpa Kode", IDUnitKerja: unitTanpaKode.ID,
	})
	if err != nil {
		t.Fatalf("create ruang tanpa kode: %v", err)
	}

	_, err = testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
		ActorID: f.actor, Tanggal: "2026-08-11", IDSupplier: f.supplier, IDRuang: ruangTanpaKode.ID,
		Detail: []model.PembelianDetailRequest{{
			IDProduct: f.product, IDSatuanInput: f.pcs, QtyFaktur: "5", HargaSatuanInput: "10000",
		}},
	})

	assertKind(t, err, model.KindInvalid)
	if err == nil || !strings.Contains(err.Error(), strconv.FormatInt(unitTanpaKode.ID, 10)) {
		t.Errorf("pesan error harus menyebut unit_kerja (id %d), got: %v", unitTanpaKode.ID, err)
	}
}

// Two concurrent requests for the same unit's series still queue rather than
// produce twin numbers — the identical guarantee Next already held before
// this issue (one row lock, one winner at a time), now exercised through the
// per-unit branch rather than the global one.
func TestDocumentCounterDuaPermintaanBersamaanUnitSamaTetapAntre(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	const n = 5

	mulai := make(chan struct{})
	nomor := make(chan string, n)
	gagal := make(chan error, n)

	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()
			<-mulai

			resp, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
				ActorID: f.actor, Tanggal: "2026-08-11", IDSupplier: f.supplier, IDRuang: f.ruang,
				Detail: []model.PembelianDetailRequest{{
					IDProduct: f.product, IDSatuanInput: f.pcs, QtyFaktur: "1", HargaSatuanInput: "10000",
				}},
			})
			if err != nil {
				gagal <- err
				return
			}

			nomor <- resp.Nomor
		}()
	}

	close(mulai)
	wg.Wait()
	close(nomor)
	close(gagal)

	for err := range gagal {
		t.Errorf("create pembelian bersamaan: %v", err)
	}

	seen := make(map[string]bool, n)
	for nm := range nomor {
		if seen[nm] {
			t.Errorf("nomor %s terbit dua kali — seri tidak antre", nm)
		}
		seen[nm] = true
	}
	if len(seen) != n {
		t.Errorf("got %d nomor unik, want %d", len(seen), n)
	}

	for _, want := range []string{
		"BL/FIX/2026/08/0001", "BL/FIX/2026/08/0002", "BL/FIX/2026/08/0003",
		"BL/FIX/2026/08/0004", "BL/FIX/2026/08/0005",
	} {
		if !seen[want] {
			t.Errorf("nomor %s tidak pernah terbit; seri diduga melompat", want)
		}
	}
}
