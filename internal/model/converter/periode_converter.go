package converter

import (
	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
)

func PeriodeToResponse(periode *entity.Periode) *model.PeriodeResponse {
	return &model.PeriodeResponse{
		Tahun:       periode.Tahun,
		Bulan:       periode.Bulan,
		Status:      periode.Status,
		DitutupOleh: periode.DitutupOleh,
		NamaPenutup: periode.NamaPenutup,
		TsTutup:     periode.TsTutup,
		DibukaOleh:  periode.DibukaOleh,
		NamaPembuka: periode.NamaPembuka,
		TsBuka:      periode.TsBuka,
	}
}

func PeriodeToResponses(list []entity.Periode) []model.PeriodeResponse {
	// make, not var: a nil slice serialises to null instead of [].
	responses := make([]model.PeriodeResponse, len(list))
	for i := range list {
		responses[i] = *PeriodeToResponse(&list[i])
	}

	return responses
}
