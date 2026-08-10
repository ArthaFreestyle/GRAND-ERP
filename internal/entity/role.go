package entity

import "time"

// Role maps the role table. Referenced by user_role, so rows are retired with
// is_aktif rather than deleted — dropping a role that was once granted would
// erase the record of who used to be allowed what.
type Role struct {
	ID        int64
	Nama      string
	IsAktif   bool
	CreatedAt time.Time
	CreatedBy *int64
	UpdatedAt time.Time
	UpdatedBy *int64
}

// RoleAssignment is one user_role row joined to its role: the shape needed to
// attach roles to a page of users without querying per user.
type RoleAssignment struct {
	UserID int64
	Role   Role
}
