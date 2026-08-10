package entity

import "time"

// User maps the users table. Referenced by every created_by/updated_by column in
// the schema, so rows are retired with is_aktif rather than deleted.
//
// Password holds a bcrypt hash, never a plaintext password — the usecase hashes
// before this struct reaches the repository. It is deliberately absent from
// model.UserResponse, so the hash has no path to a client.
//
// role_active used to live here. Migration 000010 dropped it: user_role is the
// only place role ownership is recorded, and a user's permissions are the union
// of every role they hold.
type User struct {
	ID          int64
	Username    string
	Email       *string
	Password    string
	NamaLengkap *string
	IsAktif     bool
	CreatedAt   time.Time
	CreatedBy   *int64
	UpdatedAt   time.Time
	UpdatedBy   *int64

	// Roles is not a column of users. It comes from user_role joined to role and
	// is filled by the read queries, the same way Supplier.NamaPembuat is.
	Roles []Role
}
