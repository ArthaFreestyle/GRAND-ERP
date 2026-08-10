package converter

import (
	"Arthafreestyle/ERP/internal/entity"
	"Arthafreestyle/ERP/internal/model"
)

// UserToResponse drops Password on the floor. That omission is the whole reason a
// hash cannot leak: model.UserResponse has no field for it.
func UserToResponse(user *entity.User) *model.UserResponse {
	return &model.UserResponse{
		ID:          user.ID,
		Username:    user.Username,
		Email:       user.Email,
		NamaLengkap: user.NamaLengkap,
		IsAktif:     user.IsAktif,
		Roles:       RoleToRefs(user.Roles),
		CreatedAt:   user.CreatedAt,
		CreatedBy:   user.CreatedBy,
		UpdatedAt:   user.UpdatedAt,
		UpdatedBy:   user.UpdatedBy,
	}
}

func UserToResponses(list []entity.User) []model.UserResponse {
	// make, not var: a nil slice serialises to null instead of [].
	responses := make([]model.UserResponse, len(list))
	for i := range list {
		responses[i] = *UserToResponse(&list[i])
	}

	return responses
}
