package converter

import (
	"math/big"

	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
)

func PembayaranUtangToResponse(pembayaran *entity.PembayaranUtang) *model.PembayaranUtangResponse {
	response := &model.PembayaranUtangResponse{
		ID:           pembayaran.ID,
		Nomor:        pembayaran.Nomor,
		Tanggal:      pembayaran.Tanggal,
		IDSupplier:   pembayaran.IDSupplier,
		NamaSupplier: pembayaran.NamaSupplier,

		Metode:      pembayaran.Metode,
		NoReferensi: pembayaran.NoReferensi,
		NamaBank:    pembayaran.NamaBank,
		StatusGiro:  pembayaran.StatusGiro,

		Jumlah:             pembayaran.Jumlah,
		JumlahDialokasikan: pembayaran.JumlahDialokasikan,
		// Computed here rather than left to the client: it is the figure that says
		// whether this payment still has room in it, and every caller would derive it
		// the same way.
		SisaBelumDialokasikan: selisihUang(pembayaran.Jumlah, pembayaran.JumlahDialokasikan),
		Status:                pembayaran.Status,
		Keterangan:            pembayaran.Keterangan,

		CreatedBy: pembayaran.CreatedBy,
		CreatedAt: pembayaran.CreatedAt,
		PostedAt:  pembayaran.PostedAt,

		DibatalkanOleh: pembayaran.DibatalkanOleh,
		AlasanBatal:    pembayaran.AlasanBatal,
	}

	// Both are DATE columns: no time and no zone, so rendering them as RFC 3339
	// timestamps would invent both.
	if pembayaran.TanggalJatuhTempoGiro != nil {
		tanggal := pembayaran.TanggalJatuhTempoGiro.Format(dateOnly)
		response.TanggalJatuhTempoGiro = &tanggal
	}

	if pembayaran.TanggalCair != nil {
		tanggal := pembayaran.TanggalCair.Format(dateOnly)
		response.TanggalCair = &tanggal
	}

	// Left nil on list reads, where the allocations are not fetched, so `omitempty`
	// drops the key rather than claiming the payment has none — which for this document
	// would be a meaningful and wrong claim, since an unallocated payment is a real
	// thing.
	if pembayaran.Alokasi != nil {
		response.Alokasi = PembayaranUtangAlokasiToResponses(pembayaran.Alokasi)
	}

	return response
}

func PembayaranUtangToResponses(list []entity.PembayaranUtang) []model.PembayaranUtangResponse {
	// make, not var: a nil slice serialises to null instead of [].
	responses := make([]model.PembayaranUtangResponse, len(list))
	for i := range list {
		responses[i] = *PembayaranUtangToResponse(&list[i])
	}

	return responses
}

func PembayaranUtangAlokasiToResponse(alokasi *entity.PembayaranUtangAlokasi) *model.PembayaranUtangAlokasiResponse {
	return &model.PembayaranUtangAlokasiResponse{
		ID:               alokasi.ID,
		IDPembelian:      alokasi.IDPembelian,
		NomorPembelian:   alokasi.NomorPembelian,
		NoFakturSupplier: alokasi.NoFakturSupplier,
		TotalPembelian:   alokasi.TotalPembelian,
		StatusPembayaran: alokasi.StatusPembayaran,
		Jumlah:           alokasi.Jumlah,
		CreatedAt:        alokasi.CreatedAt,
	}
}

func PembayaranUtangAlokasiToResponses(list []entity.PembayaranUtangAlokasi) []model.PembayaranUtangAlokasiResponse {
	responses := make([]model.PembayaranUtangAlokasiResponse, len(list))
	for i := range list {
		responses[i] = *PembayaranUtangAlokasiToResponse(&list[i])
	}

	return responses
}

func UtangSupplierToResponse(utang *entity.UtangSupplier) *model.UtangSupplierResponse {
	response := &model.UtangSupplierResponse{
		IDPembelian:      utang.IDPembelian,
		Nomor:            utang.Nomor,
		Tanggal:          utang.Tanggal,
		NoFakturSupplier: utang.NoFakturSupplier,
		JenisPembayaran:  utang.JenisPembayaran,
		StatusPembayaran: utang.StatusPembayaran,

		Total:              utang.Total,
		JumlahDialokasikan: utang.JumlahDialokasikan,
		NilaiKreditRetur:   utang.NilaiKreditRetur,
		SisaUtang:          utang.SisaUtang,
	}

	if utang.TanggalFaktur != nil {
		tanggal := utang.TanggalFaktur.Format(dateOnly)
		response.TanggalFaktur = &tanggal
	}

	return response
}

func UtangSupplierToResponses(list []entity.UtangSupplier) []model.UtangSupplierResponse {
	responses := make([]model.UtangSupplierResponse, len(list))
	for i := range list {
		responses[i] = *UtangSupplierToResponse(&list[i])
	}

	return responses
}

// selisihUang subtracts two NUMERIC-as-text money figures exactly.
//
// big.Rat rather than float64, for the same reason the usecase layer uses it: these are
// decimal amounts and 0.1 has no exact binary representation. Both operands came out of
// PostgreSQL, so a malformed one would mean the driver lied — it reads as zero rather
// than carrying an error up a conversion path that has nowhere to put one.
func selisihUang(a, b string) string {
	kiri, kiriOK := new(big.Rat).SetString(a)
	kanan, kananOK := new(big.Rat).SetString(b)

	if !kiriOK || !kananOK {
		return "0.00"
	}

	return kiri.Sub(kiri, kanan).FloatString(2)
}
