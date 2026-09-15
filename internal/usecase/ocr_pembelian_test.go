package usecase_test

// These run against a real PostgreSQL for the same reason every other transaction
// test in this package does: FindKatalogOCR and FindNomorFakturSupplier are SQL, and
// the "usulan submits unmodified" case is only proved by actually calling
// PembelianUseCase.Create with it. Gemini itself is never called — every case hands
// app.ocr.FakturReader a *fakeFakturReader (see main_test.go), the isu #39 decision
// that no usecase test may reach the real API.

import (
	"bytes"
	"testing"

	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/repository"
)

// minimalJPEG is just enough of a JPEG signature (the SOI + APP0 marker every real
// JPEG starts with) for http.DetectContentType to answer "image/jpeg" — content
// itself is never decoded anywhere in this pipeline, only sniffed and handed to the
// fake reader, so a real photo would add nothing a test could assert on.
var minimalJPEG = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00}

// hasilBersih is one recognized line on a faktur-kedatangan, checked off, reading
// cleanly against pembelianFixture's product/satuan/prices — the shape every OCR
// call in this file starts from before a test overrides one field.
func hasilBersih(f fixture) *repository.FakturReaderHasil {
	return &repository.FakturReaderHasil{
		NoFaktur:      ptr("INV/09/00123"),
		TanggalFaktur: ptr("2026-08-10"),
		NamaSupplier:  ptr("PT Sumber"),
		Total:         ptr("100000"),
		Baris: []repository.FakturReaderBaris{
			{
				Urutan:      1,
				TeksAsli:    "Kertas A4",
				IDProduct:   &f.product,
				IDSatuan:    &f.pcs,
				Qty:         ptr("10"),
				HargaSatuan: ptr("10000"),
				Dicentang:   ptr(true),
			},
		},
	}
}

func ocrRequest(f fixture) *model.OCRPembelianRequest {
	return &model.OCRPembelianRequest{
		ActorID:          f.actor,
		IDSupplier:       f.supplier,
		IDRuang:          f.ruang,
		Tanggal:          "2026-08-11",
		Gambar:           bytes.NewReader(minimalJPEG),
		UkuranDilaporkan: int64(len(minimalJPEG)),
	}
}

func countRows(t *testing.T, query string) int {
	t.Helper()

	var n int
	if err := testDB.QueryRowContext(ctx(), query).Scan(&n); err != nil {
		t.Fatalf("count rows (%s): %v", query, err)
	}

	return n
}

// Neither OCR endpoint writes anything — the issue's central decision. A call that
// reads cleanly and produces no warnings still leaves every table it could plausibly
// touch exactly as empty as truncateMaster left it.
func TestOCRPembelianTidakMenulisApaPun(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)
	testApp.ocr.FakturReader = &fakeFakturReader{Hasil: hasilBersih(f)}

	if _, err := testApp.ocr.FakturKedatangan(ctx(), ocrRequest(f)); err != nil {
		t.Fatalf("FakturKedatangan: %v", err)
	}

	for _, table := range []string{"pembelian", "pembelian_detail", "document_counter", "dokumen", "kartu_stok"} {
		if n := countRows(t, "SELECT COUNT(*) FROM "+table); n != 0 {
			t.Errorf("table %s has %d row(s) after an OCR call, want 0", table, n)
		}
	}
}

// The whole point of "usulan is shaped exactly like CreatePembelianRequest": one
// read cleanly off a photo, with nothing corrected by a human, still passes
// PembelianUseCase.Create unmodified.
func TestOCRUsulanBersihLolosCreate(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)
	testApp.ocr.FakturReader = &fakeFakturReader{Hasil: hasilBersih(f)}

	response, err := testApp.ocr.FakturKedatangan(ctx(), ocrRequest(f))
	if err != nil {
		t.Fatalf("FakturKedatangan: %v", err)
	}

	usulan := response.Usulan
	usulan.ActorID = f.actor

	if _, err := testApp.pembelian.Create(ctx(), &usulan); err != nil {
		t.Fatalf("POST /pembelian with an unmodified usulan: %v", err)
	}
}

// A checked line is read as fully received; the response never leaves qty_diterima
// nil, whatever the outcome, because POST /pembelian reads an omitted qty_diterima
// as "same as qty_faktur" — sending null on an unattended line would silently record
// it as complete.
func TestOCRFakturKedatanganCentang(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	cases := []struct {
		name              string
		dicentang         *bool
		tulisanTangan     *string
		wantQtyDiterima   string
		wantKeteranganNil bool
	}{
		{name: "dicentang", dicentang: ptr(true), wantQtyDiterima: "10.0000", wantKeteranganNil: true},
		{name: "tidak dicentang tanpa tulisan tangan", dicentang: ptr(false), wantQtyDiterima: "0.0000"},
		{name: "tidak dicentang dengan tulisan tangan", dicentang: ptr(false), tulisanTangan: ptr("7"), wantQtyDiterima: "7.0000"},
		{name: "ragu", dicentang: nil, wantQtyDiterima: "0.0000"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			hasil := hasilBersih(f)
			hasil.Baris[0].Dicentang = tt.dicentang
			hasil.Baris[0].QtyTulisanTangan = tt.tulisanTangan
			testApp.ocr.FakturReader = &fakeFakturReader{Hasil: hasil}

			response, err := testApp.ocr.FakturKedatangan(ctx(), ocrRequest(f))
			if err != nil {
				t.Fatalf("FakturKedatangan: %v", err)
			}

			if len(response.Usulan.Detail) != 1 {
				t.Fatalf("detail count = %d, want 1", len(response.Usulan.Detail))
			}

			baris := response.Usulan.Detail[0]

			if baris.QtyDiterima == nil {
				t.Fatalf("qty_diterima is nil, must always be explicit")
			}

			if *baris.QtyDiterima != tt.wantQtyDiterima {
				t.Errorf("qty_diterima = %s, want %s", *baris.QtyDiterima, tt.wantQtyDiterima)
			}

			if tt.wantKeteranganNil && baris.KeteranganSelisih != nil {
				t.Errorf("keterangan_selisih = %q, want nil", *baris.KeteranganSelisih)
			}

			if !tt.wantKeteranganNil && baris.KeteranganSelisih == nil {
				t.Errorf("keterangan_selisih is nil, want a note explaining the shortfall")
			}
		})
	}
}

// The nota endpoint has no centang convention at all: dicentang is ignored outright,
// even when the fake reader (wrongly, as a faktur-kedatangan reading might) sets it
// to false.
func TestOCRNotaMengabaikanCentang(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	hasil := hasilBersih(f)
	hasil.Baris[0].Dicentang = ptr(false)
	testApp.ocr.FakturReader = &fakeFakturReader{Hasil: hasil}

	response, err := testApp.ocr.Nota(ctx(), ocrRequest(f))
	if err != nil {
		t.Fatalf("Nota: %v", err)
	}

	if len(response.Usulan.Detail) != 1 {
		t.Fatalf("detail count = %d, want 1", len(response.Usulan.Detail))
	}

	baris := response.Usulan.Detail[0]
	if baris.QtyDiterima == nil || *baris.QtyDiterima != "10.0000" {
		t.Errorf("qty_diterima = %v, want 10.0000 — nota reads every recognized line as fully received", baris.QtyDiterima)
	}

	if baris.KeteranganSelisih != nil {
		t.Errorf("keterangan_selisih = %q, want nil on a nota", *baris.KeteranganSelisih)
	}
}

// A hallucinated id_product — one Gemini named that the catalog it was given does
// not contain — drops that one line to unrecognized rather than failing the whole
// call, and every other line still proposes normally.
func TestOCRProdukHalusinasiDiabaikan(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	hasil := hasilBersih(f)
	idHalu := int64(999999)
	hasil.Baris = append(hasil.Baris, repository.FakturReaderBaris{
		Urutan:      2,
		TeksAsli:    "Barang Tidak Dikenal",
		IDProduct:   &idHalu,
		IDSatuan:    &f.pcs,
		Qty:         ptr("5"),
		HargaSatuan: ptr("1000"),
		Dicentang:   ptr(true),
	})
	testApp.ocr.FakturReader = &fakeFakturReader{Hasil: hasil}

	response, err := testApp.ocr.FakturKedatangan(ctx(), ocrRequest(f))
	if err != nil {
		t.Fatalf("FakturKedatangan: %v", err)
	}

	if len(response.Usulan.Detail) != 1 {
		t.Fatalf("detail count = %d, want 1 (the hallucinated line must not be proposed)", len(response.Usulan.Detail))
	}

	if len(response.OCR.Baris) != 2 {
		t.Fatalf("ocr.baris count = %d, want 2 (every line, recognized or not)", len(response.OCR.Baris))
	}

	kedua := response.OCR.Baris[1]
	if kedua.IndeksUsulan != nil {
		t.Errorf("indeks_usulan = %d, want nil for the hallucinated line", *kedua.IndeksUsulan)
	}

	adaPeringatan := false

	for _, p := range response.OCR.Peringatan {
		if p != "" {
			adaPeringatan = true
		}
	}

	if !adaPeringatan {
		t.Error("expected at least one peringatan for the hallucinated id_product")
	}
}

// A no_faktur_supplier that another non-BATAL pembelian for the same supplier
// already carries is a peringatan naming that document's own nomor, never a 409 —
// the duplicate has to surface before a human corrects forty lines, not only at
// submit.
func TestOCRNoFakturSupplierDuplikatJadiPeringatan(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	existing, err := testApp.pembelian.Create(ctx(), &model.CreatePembelianRequest{
		ActorID:          f.actor,
		Tanggal:          "2026-08-05",
		IDSupplier:       f.supplier,
		IDRuang:          f.ruang,
		NoFakturSupplier: ptr("INV/09/00123"),
		Detail: []model.PembelianDetailRequest{{
			IDProduct: f.product, IDSatuanInput: f.pcs,
			QtyFaktur: "1", HargaSatuanInput: "1000",
		}},
	})
	if err != nil {
		t.Fatalf("create existing pembelian: %v", err)
	}

	testApp.ocr.FakturReader = &fakeFakturReader{Hasil: hasilBersih(f)}

	response, err := testApp.ocr.FakturKedatangan(ctx(), ocrRequest(f))
	if err != nil {
		t.Fatalf("FakturKedatangan: %v", err)
	}

	cocok := false

	for _, p := range response.OCR.Peringatan {
		if p == "no_faktur_supplier INV/09/00123 sudah dipakai pembelian "+existing.Nomor+" untuk supplier ini" {
			cocok = true
		}
	}

	if !cocok {
		t.Errorf("peringatan = %v, want one naming pembelian %s", response.OCR.Peringatan, existing.Nomor)
	}
}

// isu #12 fase 5: id_ruang is checked against the caller's active unit_kerja before
// a single line is read off the photo, the same guard CreatePembelianRequest itself
// goes through.
func TestOCRRuangDiLuarUnitAktifDitolak(t *testing.T) {
	testApp := newApp(t)
	f := pembelianFixture(t, testApp)

	unitLain := createUnit(t, testApp, "Unit Lain OCR")

	request := ocrRequest(f)
	request.AktifIDUnitKerja = &unitLain

	_, err := testApp.ocr.FakturKedatangan(ctx(), request)

	assertKind(t, err, model.KindForbidden)
}
