package converter

import (
	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
)

func PenjualanToResponse(penjualan *entity.Penjualan) *model.PenjualanResponse {
	response := &model.PenjualanResponse{
		ID:      penjualan.ID,
		Nomor:   penjualan.Nomor,
		Tanggal: penjualan.Tanggal,

		IDRuang:   penjualan.IDRuang,
		NamaRuang: penjualan.NamaRuang,

		IDPelanggan:   penjualan.IDPelanggan,
		NamaPelanggan: penjualan.NamaPelanggan,

		Subtotal:   penjualan.Subtotal,
		DiskonNota: penjualan.DiskonNota,
		Pembulatan: penjualan.Pembulatan,
		Total:      penjualan.Total,
		TotalHPP:   penjualan.TotalHPP,

		JenisPembayaran:  penjualan.JenisPembayaran,
		StatusPembayaran: penjualan.StatusPembayaran,
		Status:           penjualan.Status,

		CreatedBy: penjualan.CreatedBy,
		CreatedAt: penjualan.CreatedAt,
		PostedAt:  penjualan.PostedAt,

		DibatalkanOleh: penjualan.DibatalkanOleh,
		AlasanBatal:    penjualan.AlasanBatal,
	}

	// Left nil on list reads, where the lines are not fetched, so `omitempty` drops the
	// key rather than claiming the document has none.
	if penjualan.Detail != nil {
		response.Detail = PenjualanDetailToResponses(penjualan.Detail)
	}

	return response
}

func PenjualanToResponses(list []entity.Penjualan) []model.PenjualanResponse {
	// make, not var: a nil slice serialises to null instead of [].
	responses := make([]model.PenjualanResponse, len(list))
	for i := range list {
		responses[i] = *PenjualanToResponse(&list[i])
	}

	return responses
}

func PenjualanDetailToResponse(detail *entity.PenjualanDetail) *model.PenjualanDetailResponse {
	return &model.PenjualanDetailResponse{
		ID:          detail.ID,
		IDProduct:   detail.IDProduct,
		KodeBarang:  detail.KodeBarang,
		NamaProduct: detail.NamaProduct,

		QtyInput:        detail.QtyInput,
		IDSatuanInput:   detail.IDSatuanInput,
		NamaSatuan:      detail.NamaSatuan,
		FaktorKonversi:  detail.FaktorKonversi,
		QtyDasar:        detail.QtyDasar,
		NamaSatuanDasar: detail.NamaSatuanDasar,

		IDHargaJual:      detail.IDHargaJual,
		HargaSatuanInput: detail.HargaSatuanInput,
		DiskonBaris:      detail.DiskonBaris,
		Subtotal:         detail.Subtotal,

		HPPSatuanDasar: detail.HPPSatuanDasar,
		HPPTotal:       detail.HPPTotal,
	}
}

func PenjualanDetailToResponses(list []entity.PenjualanDetail) []model.PenjualanDetailResponse {
	responses := make([]model.PenjualanDetailResponse, len(list))
	for i := range list {
		responses[i] = *PenjualanDetailToResponse(&list[i])
	}

	return responses
}

func PiutangPelangganToResponse(piutang *entity.PiutangPelanggan) *model.PiutangPelangganResponse {
	return &model.PiutangPelangganResponse{
		IDPenjualan: piutang.IDPenjualan,
		Nomor:       piutang.Nomor,
		Tanggal:     piutang.Tanggal,
		Total:       piutang.Total,
		SisaPiutang: piutang.SisaPiutang,
	}
}

func PiutangPelangganToResponses(list []entity.PiutangPelanggan) []model.PiutangPelangganResponse {
	responses := make([]model.PiutangPelangganResponse, len(list))
	for i := range list {
		responses[i] = *PiutangPelangganToResponse(&list[i])
	}

	return responses
}
