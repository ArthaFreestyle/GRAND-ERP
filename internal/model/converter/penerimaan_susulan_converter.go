package converter

import (
	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
)

func PenerimaanSusulanToResponse(susulan *entity.PenerimaanSusulan) *model.PenerimaanSusulanResponse {
	response := &model.PenerimaanSusulanResponse{
		ID:             susulan.ID,
		Nomor:          susulan.Nomor,
		Tanggal:        susulan.Tanggal,
		IDPembelian:    susulan.IDPembelian,
		NomorPembelian: susulan.NomorPembelian,
		IDSupplier:     susulan.IDSupplier,
		NamaSupplier:   susulan.NamaSupplier,
		IDRuang:        susulan.IDRuang,
		NamaRuang:      susulan.NamaRuang,
		Keterangan:     susulan.Keterangan,
		TotalNilai:     susulan.TotalNilai,
		Status:         susulan.Status,

		CreatedBy: susulan.CreatedBy,
		CreatedAt: susulan.CreatedAt,

		DiajukanOleh:  susulan.DiajukanOleh,
		DiajukanPada:  susulan.DiajukanPada,
		DisetujuiOleh: susulan.DisetujuiOleh,
		DisetujuiPada: susulan.DisetujuiPada,
		PostedAt:      susulan.PostedAt,

		DibatalkanOleh: susulan.DibatalkanOleh,
		AlasanBatal:    susulan.AlasanBatal,
		AlasanTolak:    susulan.AlasanTolak,
	}

	// Left nil on list reads, where the lines are not fetched, so `omitempty` drops
	// the key rather than claiming the document has none.
	if susulan.Detail != nil {
		response.Detail = PenerimaanSusulanDetailToResponses(susulan.Detail)
	}

	return response
}

func PenerimaanSusulanToResponses(list []entity.PenerimaanSusulan) []model.PenerimaanSusulanResponse {
	// make, not var: a nil slice serialises to null instead of [].
	responses := make([]model.PenerimaanSusulanResponse, len(list))
	for i := range list {
		responses[i] = *PenerimaanSusulanToResponse(&list[i])
	}

	return responses
}

func PenerimaanSusulanDetailToResponse(detail *entity.PenerimaanSusulanDetail) *model.PenerimaanSusulanDetailResponse {
	return &model.PenerimaanSusulanDetailResponse{
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

func PenerimaanSusulanDetailToResponses(list []entity.PenerimaanSusulanDetail) []model.PenerimaanSusulanDetailResponse {
	responses := make([]model.PenerimaanSusulanDetailResponse, len(list))
	for i := range list {
		responses[i] = *PenerimaanSusulanDetailToResponse(&list[i])
	}

	return responses
}
