package converter

import (
	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
)

func PemakaianToResponse(pemakaian *entity.Pemakaian) *model.PemakaianResponse {
	response := &model.PemakaianResponse{
		ID:        pemakaian.ID,
		Nomor:     pemakaian.Nomor,
		Tanggal:   pemakaian.Tanggal,
		IDRuang:   pemakaian.IDRuang,
		NamaRuang: pemakaian.NamaRuang,

		IDPemohon:   pemakaian.IDPemohon,
		NamaPemohon: pemakaian.NamaPemohon,
		Keperluan:   pemakaian.Keperluan,
		Status:      pemakaian.Status,

		DisetujuiOleh:      pemakaian.DisetujuiOleh,
		TsDisetujui:        pemakaian.TsDisetujui,
		CatatanPersetujuan: pemakaian.CatatanPersetujuan,
		TotalHPP:           pemakaian.TotalHPP,

		CreatedBy: pemakaian.CreatedBy,
		CreatedAt: pemakaian.CreatedAt,
		PostedAt:  pemakaian.PostedAt,

		DibatalkanOleh: pemakaian.DibatalkanOleh,
		AlasanBatal:    pemakaian.AlasanBatal,
	}

	// Left nil on list reads, where the lines are not fetched, so `omitempty` drops
	// the key rather than claiming the document has none.
	if pemakaian.Detail != nil {
		response.Detail = PemakaianDetailToResponses(pemakaian.Detail)
	}

	return response
}

func PemakaianToResponses(list []entity.Pemakaian) []model.PemakaianResponse {
	// make, not var: a nil slice serialises to null instead of [].
	responses := make([]model.PemakaianResponse, len(list))
	for i := range list {
		responses[i] = *PemakaianToResponse(&list[i])
	}

	return responses
}

func PemakaianDetailToResponse(detail *entity.PemakaianDetail) *model.PemakaianDetailResponse {
	return &model.PemakaianDetailResponse{
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

		QtyDisetujuiDasar: detail.QtyDisetujuiDasar,
		HPPSatuanDasar:    detail.HPPSatuanDasar,
		HPPTotal:          detail.HPPTotal,
		Keterangan:        detail.Keterangan,
	}
}

func PemakaianDetailToResponses(list []entity.PemakaianDetail) []model.PemakaianDetailResponse {
	responses := make([]model.PemakaianDetailResponse, len(list))
	for i := range list {
		responses[i] = *PemakaianDetailToResponse(&list[i])
	}

	return responses
}
