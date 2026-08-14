package converter

import (
	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
)

func MutasiToResponse(mutasi *entity.Mutasi) *model.MutasiResponse {
	response := &model.MutasiResponse{
		ID:      mutasi.ID,
		Nomor:   mutasi.Nomor,
		Tanggal: mutasi.Tanggal,

		IDRuangAsal:     mutasi.IDRuangAsal,
		NamaRuangAsal:   mutasi.NamaRuangAsal,
		IDRuangTujuan:   mutasi.IDRuangTujuan,
		NamaRuangTujuan: mutasi.NamaRuangTujuan,

		Keterangan: mutasi.Keterangan,
		Status:     mutasi.Status,

		CreatedBy: mutasi.CreatedBy,
		CreatedAt: mutasi.CreatedAt,
		PostedAt:  mutasi.PostedAt,

		DibatalkanOleh: mutasi.DibatalkanOleh,
		AlasanBatal:    mutasi.AlasanBatal,
	}

	// Left nil on list reads, where the lines are not fetched, so `omitempty` drops the
	// key rather than claiming the document has none.
	if mutasi.Detail != nil {
		response.Detail = MutasiDetailToResponses(mutasi.Detail)
	}

	return response
}

func MutasiToResponses(list []entity.Mutasi) []model.MutasiResponse {
	// make, not var: a nil slice serialises to null instead of [].
	responses := make([]model.MutasiResponse, len(list))
	for i := range list {
		responses[i] = *MutasiToResponse(&list[i])
	}

	return responses
}

func MutasiDetailToResponse(detail *entity.MutasiDetail) *model.MutasiDetailResponse {
	return &model.MutasiDetailResponse{
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

		HargaPokokSatuanDasar: detail.HargaPokokSatuanDasar,
	}
}

func MutasiDetailToResponses(list []entity.MutasiDetail) []model.MutasiDetailResponse {
	responses := make([]model.MutasiDetailResponse, len(list))
	for i := range list {
		responses[i] = *MutasiDetailToResponse(&list[i])
	}

	return responses
}
