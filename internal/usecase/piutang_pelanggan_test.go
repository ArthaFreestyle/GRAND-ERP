package usecase_test

// GET /pelanggan/{id}/piutang (isu #10 fase 2) — a read that is not a module,
// following riwayat_beli_test.go and utang_supplier's own tests: no table, no
// migration, one query in penjualan_repository.go borrowed by PelangganUseCase.

import (
	"testing"

	"Arthafreestyle/ERP/internal/model"
)

// Only a POSTED KREDIT nota is a receivable. A DRAFT is a typed page, a BATAL one
// was withdrawn, and a TUNAI nota was never a receivable to begin with — it reads
// LUNAS the instant it posts.
func TestPiutangPelangganHanyaNotaKreditPosted(t *testing.T) {
	testApp, s := stokAwalPenjualan(t, newApp(t), "300")

	// TUNAI, POSTED — never a receivable.
	tunai := buatPenjualanTunai(t, testApp, s, "5", "15000")
	postingPenjualan(t, testApp, s, tunai.ID)

	// KREDIT, still DRAFT — not a receivable until it posts.
	if _, err := testApp.penjualan.Create(ctx(), &model.CreatePenjualanRequest{
		ActorID: s.actor, Tanggal: "2026-08-15", IDRuang: s.ruang,
		IDPelanggan: &s.pelanggan, JenisPembayaran: "KREDIT",
		Detail: []model.PenjualanDetailRequest{{
			IDProduct: s.product, IDSatuanInput: s.pcs, QtyInput: "5", HargaSatuanInput: "15000",
		}},
	}); err != nil {
		t.Fatalf("create penjualan kredit draft: %v", err)
	}

	// KREDIT, POSTED then BATAL — was a receivable, but was withdrawn.
	batalNanti, err := testApp.penjualan.Create(ctx(), &model.CreatePenjualanRequest{
		ActorID: s.actor, Tanggal: "2026-08-15", IDRuang: s.ruang,
		IDPelanggan: &s.pelanggan, JenisPembayaran: "KREDIT",
		Detail: []model.PenjualanDetailRequest{{
			IDProduct: s.product, IDSatuanInput: s.pcs, QtyInput: "5", HargaSatuanInput: "15000",
		}},
	})
	if err != nil {
		t.Fatalf("create penjualan kredit dibatalkan: %v", err)
	}
	postingPenjualan(t, testApp, s, batalNanti.ID)
	if _, err := testApp.penjualan.Batal(ctx(), &model.BatalPenjualanRequest{
		ID: batalNanti.ID, ActorID: s.actor, AlasanBatal: "batal",
	}); err != nil {
		t.Fatalf("batal penjualan kredit: %v", err)
	}

	// KREDIT, POSTED — the one nota that must appear.
	kredit, err := testApp.penjualan.Create(ctx(), &model.CreatePenjualanRequest{
		ActorID: s.actor, Tanggal: "2026-08-16", IDRuang: s.ruang,
		IDPelanggan: &s.pelanggan, JenisPembayaran: "KREDIT",
		Detail: []model.PenjualanDetailRequest{{
			IDProduct: s.product, IDSatuanInput: s.pcs, QtyInput: "8", HargaSatuanInput: "15000",
		}},
	})
	if err != nil {
		t.Fatalf("create penjualan kredit posted: %v", err)
	}
	postingPenjualan(t, testApp, s, kredit.ID)

	piutang, paging, err := testApp.pelanggan.Piutang(ctx(), &model.ListPiutangPelangganRequest{
		IDPelanggan: s.pelanggan,
	})
	if err != nil {
		t.Fatalf("piutang pelanggan: %v", err)
	}

	if paging.TotalItem != 1 {
		t.Fatalf("total_item = %d, want 1", paging.TotalItem)
	}

	if len(piutang) != 1 || piutang[0].IDPenjualan != kredit.ID {
		t.Fatalf("piutang = %+v, want satu baris untuk nota %d", piutang, kredit.ID)
	}

	if piutang[0].Total != "120000.00" || piutang[0].SisaPiutang != "120000.00" {
		t.Errorf("total/sisa_piutang = %s/%s, want 120000.00/120000.00", piutang[0].Total, piutang[0].SisaPiutang)
	}
}

// An unknown customer answers 404, not an empty page — "this customer owes
// nothing" and "there is no such customer" are different facts.
func TestPiutangPelangganUnknownIDReturns404(t *testing.T) {
	testApp := newApp(t)

	_, _, err := testApp.pelanggan.Piutang(ctx(), &model.ListPiutangPelangganRequest{
		IDPelanggan: 999999,
	})
	assertKind(t, err, model.KindNotFound)
}

// Oldest first, the same choice GET /supplier/{id}/utang makes: this is a queue to
// work through, not a history to read.
func TestPiutangPelangganTerlamaDulu(t *testing.T) {
	testApp, s := stokAwalPenjualan(t, newApp(t), "300")

	for _, tanggal := range []string{"2026-08-13", "2026-08-11", "2026-08-12"} {
		nota, err := testApp.penjualan.Create(ctx(), &model.CreatePenjualanRequest{
			ActorID: s.actor, Tanggal: tanggal, IDRuang: s.ruang,
			IDPelanggan: &s.pelanggan, JenisPembayaran: "KREDIT",
			Detail: []model.PenjualanDetailRequest{{
				IDProduct: s.product, IDSatuanInput: s.pcs, QtyInput: "1", HargaSatuanInput: "15000",
			}},
		})
		if err != nil {
			t.Fatalf("create penjualan %s: %v", tanggal, err)
		}

		postingPenjualan(t, testApp, s, nota.ID)
	}

	piutang, _, err := testApp.pelanggan.Piutang(ctx(), &model.ListPiutangPelangganRequest{
		IDPelanggan: s.pelanggan,
	})
	if err != nil {
		t.Fatalf("piutang pelanggan: %v", err)
	}

	if len(piutang) != 3 {
		t.Fatalf("piutang = %d baris, want 3", len(piutang))
	}

	for i, want := range []string{"2026-08-11", "2026-08-12", "2026-08-13"} {
		if got := piutang[i].Tanggal.Format("2006-01-02"); got != want {
			t.Errorf("piutang[%d] bertanggal %s, want %s", i, got, want)
		}
	}
}
