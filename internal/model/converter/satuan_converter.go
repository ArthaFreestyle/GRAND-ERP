package converter

import (
	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
)

func SatuanToResponse(satuan *entity.Satuan) *model.SatuanResponse {
	return &model.SatuanResponse{
		ID:        satuan.ID,
		Nama:      satuan.Nama,
		IsAktif:   satuan.IsAktif,
		CreatedAt: satuan.CreatedAt,
		CreatedBy: satuan.CreatedBy,
		UpdatedAt: satuan.UpdatedAt,
		UpdatedBy: satuan.UpdatedBy,
	}
}

func SatuanToResponses(list []entity.Satuan) []model.SatuanResponse {
	// make, not var: a nil slice serialises to null instead of [].
	responses := make([]model.SatuanResponse, len(list))
	for i := range list {
		responses[i] = *SatuanToResponse(&list[i])
	}

	return responses
}
