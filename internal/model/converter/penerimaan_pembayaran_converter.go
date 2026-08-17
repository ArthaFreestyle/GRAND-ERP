package converter

import (
	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
)

func PenerimaanPembayaranToResponse(pembayaran *entity.PenerimaanPembayaran) *model.PenerimaanPembayaranResponse {
	response := &model.PenerimaanPembayaranResponse{
		ID:            pembayaran.ID,
		Nomor:         pembayaran.Nomor,
		Tanggal:       pembayaran.Tanggal,
		IDPelanggan:   pembayaran.IDPelanggan,
		NamaPelanggan: pembayaran.NamaPelanggan,

		Metode:      pembayaran.Metode,
		NoReferensi: pembayaran.NoReferensi,
		NamaBank:    pembayaran.NamaBank,
		StatusGiro:  pembayaran.StatusGiro,

		Jumlah:             pembayaran.Jumlah,
		JumlahDialokasikan: pembayaran.JumlahDialokasikan,
		// Computed here rather than left to the client: it is the figure that says
		// whether this payment still has room in it, and every caller would derive
		// it the same way.
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

	// Left nil on list reads, where the allocations are not fetched, so
	// `omitempty` drops the key rather than claiming the payment has none — which
	// for this document would be a meaningful and wrong claim, since an
	// unallocated payment is a real thing.
	if pembayaran.Alokasi != nil {
		response.Alokasi = PembayaranAlokasiToResponses(pembayaran.Alokasi)
	}

	return response
}

func PenerimaanPembayaranToResponses(list []entity.PenerimaanPembayaran) []model.PenerimaanPembayaranResponse {
	// make, not var: a nil slice serialises to null instead of [].
	responses := make([]model.PenerimaanPembayaranResponse, len(list))
	for i := range list {
		responses[i] = *PenerimaanPembayaranToResponse(&list[i])
	}

	return responses
}

func PembayaranAlokasiToResponse(alokasi *entity.PembayaranAlokasi) *model.PembayaranAlokasiResponse {
	return &model.PembayaranAlokasiResponse{
		ID:               alokasi.ID,
		IDPenjualan:      alokasi.IDPenjualan,
		NomorPenjualan:   alokasi.NomorPenjualan,
		Total:            alokasi.Total,
		StatusPembayaran: alokasi.StatusPembayaran,
		Jumlah:           alokasi.Jumlah,
		CreatedAt:        alokasi.CreatedAt,
	}
}

func PembayaranAlokasiToResponses(list []entity.PembayaranAlokasi) []model.PembayaranAlokasiResponse {
	responses := make([]model.PembayaranAlokasiResponse, len(list))
	for i := range list {
		responses[i] = *PembayaranAlokasiToResponse(&list[i])
	}

	return responses
}
