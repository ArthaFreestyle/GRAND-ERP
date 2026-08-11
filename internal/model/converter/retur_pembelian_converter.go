package converter

import (
	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
)

func ReturPembelianToResponse(retur *entity.ReturPembelian) *model.ReturPembelianResponse {
	response := &model.ReturPembelianResponse{
		ID:             retur.ID,
		Nomor:          retur.Nomor,
		Tanggal:        retur.Tanggal,
		IDPembelian:    retur.IDPembelian,
		NomorPembelian: retur.NomorPembelian,
		IDSupplier:     retur.IDSupplier,
		NamaSupplier:   retur.NamaSupplier,
		IDRuang:        retur.IDRuang,
		NamaRuang:      retur.NamaRuang,
		Alasan:         retur.Alasan,
		Total:          retur.Total,
		Status:         retur.Status,

		CreatedBy: retur.CreatedBy,
		CreatedAt: retur.CreatedAt,

		DiajukanOleh:  retur.DiajukanOleh,
		DiajukanPada:  retur.DiajukanPada,
		DisetujuiOleh: retur.DisetujuiOleh,
		DisetujuiPada: retur.DisetujuiPada,
		PostedAt:      retur.PostedAt,

		DibatalkanOleh: retur.DibatalkanOleh,
		AlasanBatal:    retur.AlasanBatal,
		AlasanTolak:    retur.AlasanTolak,
	}

	// Left nil on list reads, where the lines are not fetched, so `omitempty` drops
	// the key rather than claiming the document has none.
	if retur.Detail != nil {
		response.Detail = ReturPembelianDetailToResponses(retur.Detail)
	}

	return response
}

func ReturPembelianToResponses(list []entity.ReturPembelian) []model.ReturPembelianResponse {
	// make, not var: a nil slice serialises to null instead of [].
	responses := make([]model.ReturPembelianResponse, len(list))
	for i := range list {
		responses[i] = *ReturPembelianToResponse(&list[i])
	}

	return responses
}

func ReturPembelianDetailToResponse(detail *entity.ReturPembelianDetail) *model.ReturPembelianDetailResponse {
	return &model.ReturPembelianDetailResponse{
		ID:                detail.ID,
		IDPembelianDetail: detail.IDPembelianDetail,
		IDProduct:         detail.IDProduct,
		KodeBarang:        detail.KodeBarang,
		NamaProduct:       detail.NamaProduct,

		QtyInput:        detail.QtyInput,
		IDSatuanInput:   detail.IDSatuanInput,
		NamaSatuan:      detail.NamaSatuan,
		FaktorKonversi:  detail.FaktorKonversi,
		QtyDasar:        detail.QtyDasar,
		NamaSatuanDasar: detail.NamaSatuanDasar,

		HargaPokokSatuanDasar: detail.HargaPokokSatuanDasar,
		Nilai:                 detail.Nilai,
	}
}

func ReturPembelianDetailToResponses(list []entity.ReturPembelianDetail) []model.ReturPembelianDetailResponse {
	responses := make([]model.ReturPembelianDetailResponse, len(list))
	for i := range list {
		responses[i] = *ReturPembelianDetailToResponse(&list[i])
	}

	return responses
}
