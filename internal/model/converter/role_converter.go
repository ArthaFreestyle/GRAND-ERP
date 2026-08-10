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

// RoleToRefs flattens a user's roles to the slim shape embedded in a
// UserResponse. Length zero still yields [], never null.
func RoleToRefs(list []entity.Role) []model.RoleRef {
	refs := make([]model.RoleRef, len(list))
	for i := range list {
		refs[i] = model.RoleRef{ID: list[i].ID, Nama: list[i].Nama, IsAktif: list[i].IsAktif}
	}

	return refs
}
