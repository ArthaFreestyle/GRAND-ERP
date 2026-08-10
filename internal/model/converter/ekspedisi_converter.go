package converter

import (
	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
)

func EkspedisiToResponse(ekspedisi *entity.Ekspedisi) *model.EkspedisiResponse {
	return &model.EkspedisiResponse{
		ID:        ekspedisi.ID,
		Nama:      ekspedisi.Nama,
		Telepon:   ekspedisi.Telepon,
		IsAktif:   ekspedisi.IsAktif,
		CreatedAt: ekspedisi.CreatedAt,
		CreatedBy: ekspedisi.CreatedBy,
		UpdatedAt: ekspedisi.UpdatedAt,
		UpdatedBy: ekspedisi.UpdatedBy,
	}
}

func EkspedisiToResponses(list []entity.Ekspedisi) []model.EkspedisiResponse {
	// make, not var: a nil slice serialises to null instead of [].
	responses := make([]model.EkspedisiResponse, len(list))
	for i := range list {
		responses[i] = *EkspedisiToResponse(&list[i])
	}

	return responses
}
