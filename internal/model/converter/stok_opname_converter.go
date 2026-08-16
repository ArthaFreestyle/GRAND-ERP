package converter

import (
	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
)

func StokOpnameToResponse(opname *entity.StokOpname) *model.StokOpnameResponse {
	response := &model.StokOpnameResponse{
		ID:        opname.ID,
		Nomor:     opname.Nomor,
		IDRuang:   opname.IDRuang,
		NamaRuang: opname.NamaRuang,
		TglBuka:   opname.TglBuka,
		TglTutup:  opname.TglTutup,
		TsCutoff:  opname.TsCutoff,
		UraianSO:  opname.UraianSO,
		Status:    opname.Status,

		CreatedBy: opname.CreatedBy,
		CreatedAt: opname.CreatedAt,

		VerifiedBy: opname.VerifiedBy,
		TsVerified: opname.TsVerified,
		PostedAt:   opname.PostedAt,

		DibatalkanOleh: opname.DibatalkanOleh,
		AlasanBatal:    opname.AlasanBatal,
		TsBatal:        opname.TsBatal,
	}

	// Left nil on list reads, where the lines are not fetched, so `omitempty`
	// drops the key rather than claiming the document has none.
	if opname.Detail != nil {
		response.Detail = StokOpnameDetailToResponses(opname.Detail)
		response.JumlahBaris = len(opname.Detail)

		for i := range opname.Detail {
			if opname.Detail[i].StokSO == nil {
				response.JumlahBelumDihitung++
			}
		}
	}

	return response
}

func StokOpnameToResponses(list []entity.StokOpname) []model.StokOpnameResponse {
	responses := make([]model.StokOpnameResponse, len(list))
	for i := range list {
		responses[i] = *StokOpnameToResponse(&list[i])
	}

	return responses
}

func StokOpnameDetailToResponse(detail *entity.StokOpnameDetail) *model.StokOpnameDetailResponse {
	return &model.StokOpnameDetailResponse{
		ID:              detail.ID,
		IDProduct:       detail.IDBarang,
		KodeBarang:      detail.KodeBarang,
		NamaProduct:     detail.NamaProduct,
		NamaSatuanDasar: detail.NamaSatuanDasar,

		StokAwal: detail.StokAwal,
		StokSO:   detail.StokSO,

		StokSelisihLebih:  detail.StokSelisihLebih,
		StokSelisihKurang: detail.StokSelisihKurang,
		Keterangan:        detail.Keterangan,

		IDKartuStokPenyesuaian: detail.IDKartuStokPenyesuaian,
	}
}

func StokOpnameDetailToResponses(list []entity.StokOpnameDetail) []model.StokOpnameDetailResponse {
	responses := make([]model.StokOpnameDetailResponse, len(list))
	for i := range list {
		responses[i] = *StokOpnameDetailToResponse(&list[i])
	}

	return responses
}
