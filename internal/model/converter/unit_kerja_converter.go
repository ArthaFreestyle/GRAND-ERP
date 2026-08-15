package converter

import (
	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
)

func UnitKerjaToResponse(unitKerja *entity.UnitKerja) *model.UnitKerjaResponse {
	return &model.UnitKerjaResponse{
		ID:          unitKerja.ID,
		Kode:        unitKerja.Kode,
		Nama:        unitKerja.Nama,
		IsAktif:     unitKerja.IsAktif,
		CreatedAt:   unitKerja.CreatedAt,
		CreatedBy:   unitKerja.CreatedBy,
		NamaPembuat: unitKerja.NamaPembuat,
		UpdatedAt:   unitKerja.UpdatedAt,
		UpdatedBy:   unitKerja.UpdatedBy,
	}
}

func UnitKerjaToResponses(list []entity.UnitKerja) []model.UnitKerjaResponse {
	// make, not var: a nil slice serialises to null instead of [].
	responses := make([]model.UnitKerjaResponse, len(list))
	for i := range list {
		responses[i] = *UnitKerjaToResponse(&list[i])
	}

	return responses
}
