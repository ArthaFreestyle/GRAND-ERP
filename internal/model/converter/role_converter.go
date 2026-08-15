package converter

import (
	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
)

func RoleToResponse(role *entity.Role) *model.RoleResponse {
	return &model.RoleResponse{
		ID:        role.ID,
		Nama:      role.Nama,
		IsAktif:   role.IsAktif,
		CreatedAt: role.CreatedAt,
		CreatedBy: role.CreatedBy,
		UpdatedAt: role.UpdatedAt,
		UpdatedBy: role.UpdatedBy,
	}
}

func RoleToResponses(list []entity.Role) []model.RoleResponse {
	// make, not var: a nil slice serialises to null instead of [].
	responses := make([]model.RoleResponse, len(list))
	for i := range list {
		responses[i] = *RoleToResponse(&list[i])
	}

	return responses
}

// RoleToRefs flattens a user's grants to the slim shape embedded in a
// UserResponse. Length zero still yields [], never null.
//
// Since isu #12 fase 3 the same role can appear more than once here — one entry
// per unit_kerja it was granted in — and that is intentional: each row is a
// distinct grant, not a duplicate to collapse.
func RoleToRefs(list []entity.RoleGrant) []model.RoleRef {
	refs := make([]model.RoleRef, len(list))
	for i := range list {
		refs[i] = model.RoleRef{
			ID:               list[i].Role.ID,
			Nama:             list[i].Role.Nama,
			IsAktif:          list[i].Role.IsAktif,
			IDUserRole:       list[i].ID,
			IDUnitKerja:      list[i].IDUnitKerja,
			NamaUnitKerja:    list[i].NamaUnitKerja,
			IsAktifUnitKerja: list[i].IsAktifUnitKerja,
		}
	}

	return refs
}
