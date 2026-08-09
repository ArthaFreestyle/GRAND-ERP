// Package converter holds entity -> model translation. Keeping it here means
// handlers never see an entity and usecases never build JSON shapes by hand.
package converter

import (
	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
)

func RuangToResponse(ruang *entity.Ruang) *model.RuangResponse {
	return &model.RuangResponse{
		ID:        ruang.ID,
		Kode:      ruang.Kode,
		NamaRuang: ruang.NamaRuang,
		IsAktif:   ruang.IsAktif,
	}
}

func RuangToResponses(list []entity.Ruang) []model.RuangResponse {
	responses := make([]model.RuangResponse, len(list))
	for i := range list {
		responses[i] = *RuangToResponse(&list[i])
	}

	return responses
}
