package repository

import (
	"context"
	"fmt"

	"Arthafreestyle/ERP/internal/entity"
)

// UserRepository owns every SQL statement touching users and user_role.
//
// user_role lives here rather than in RoleRepository because every write to it is
// driven by a user: granting and revoking happen while editing a user, never while
// editing a role.
type UserRepository struct{}

func NewUserRepository() *UserRepository {
	return &UserRepository{}
}

// userColumns is the write-side list, unqualified, for INSERT/UPDATE ... RETURNING.
// password is absent on purpose: it is written by name in Create and Update, and
// nothing ever needs to read it back until the auth module has a reason to.
const userColumns = `id, username, email, nama_lengkap, is_aktif,
	created_at, created_by, updated_at, updated_by`

// userReadColumns is the same list qualified with the alias the filter uses.
const userReadColumns = `u.id, u.username, u.email, u.nama_lengkap, u.is_aktif,
	u.created_at, u.created_by, u.updated_at, u.updated_by`

// userFilter is shared by the COUNT and the row query. Two copies of a filter
// eventually diverge and total_item starts lying about the data.
//
// Placeholder discipline: the filter owns $1..$3 and pagination follows after it.
// Inserting a new filter means shifting LIMIT/OFFSET too.
//
// The role filter is an EXISTS rather than a join: a join to user_role would
// return one row per matching role and silently multiply the page when a user
// holds several, breaking both LIMIT and total_item.
const userFilter = `
	WHERE ($1 = '' OR u.username ILIKE '%' || $1 || '%'
	                OR u.nama_lengkap ILIKE '%' || $1 || '%'
	                OR u.email ILIKE '%' || $1 || '%')
	  AND ($2::BOOLEAN IS NULL OR u.is_aktif = $2)
	  AND ($3::BIGINT IS NULL OR EXISTS (
	          SELECT 1 FROM user_role ur
	          WHERE ur.user_id = u.id AND ur.role_id = $3))`

// UserPatch carries a partial update. Nullable columns get a Set flag answering
// "was this key in the JSON body at all?" — without it, clearing a column is
// indistinguishable from leaving it alone.
//
// Password is a hash by the time it arrives here, and has no Set flag: the column
// is NOT NULL, so it can be changed but never cleared.
type UserPatch struct {
	Username       *string // NOT NULL: changeable, never clearable, so no flag
	SetEmail       bool
	Email          *string
	Password       *string
	SetNamaLengkap bool
	NamaLengkap    *string
	IsAktif        *bool
	SetUpdatedBy   bool
	UpdatedBy      *int64
}

func (r *UserRepository) Create(ctx context.Context, db DBTX, user *entity.User) error {
	const query = `
		INSERT INTO users (username, email, password, nama_lengkap, is_aktif, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING ` + userColumns

	err := db.QueryRowContext(
		ctx, query,
		user.Username, user.Email, user.Password, user.NamaLengkap,
		user.IsAktif, user.CreatedBy,
	).Scan(
		&user.ID, &user.Username, &user.Email, &user.NamaLengkap, &user.IsAktif,
		&user.CreatedAt, &user.CreatedBy, &user.UpdatedAt, &user.UpdatedBy,
	)
	if err != nil {
		return fmt.Errorf("insert users: %w", err)
	}

	return nil
}

// FindByID returns sql.ErrNoRows when the row is absent; the usecase maps that to
// a 404. Roles are not filled in here — the caller adds them with
// FindRolesByUserIDs, which serves one user and a whole page the same way.
func (r *UserRepository) FindByID(ctx context.Context, db DBTX, id int64) (*entity.User, error) {
	const query = `SELECT ` + userReadColumns + ` FROM users u WHERE u.id = $1`

	user := new(entity.User)

	err := db.QueryRowContext(ctx, query, id).Scan(
		&user.ID, &user.Username, &user.Email, &user.NamaLengkap, &user.IsAktif,
		&user.CreatedAt, &user.CreatedBy, &user.UpdatedAt, &user.UpdatedBy,
	)
	if err != nil {
		return nil, err
	}

	return user, nil
}

// FindByUsernameWithPassword is the only query that reads the password column, and
// it exists solely for login. Matching is case-insensitive to mirror
// users_username_lower_uidx: "Budi" and "budi" are one account, so the wrong casing
// must not look like a wrong username.
//
// It returns the row regardless of is_aktif. Deciding what a retired account means
// is the usecase's job, and answering "no such user" here would make a disabled
// account indistinguishable from a typo in the logs.
//
// The returned User carries a bcrypt hash in Password. Nothing may put it in a
// response — model.UserResponse has no field for it.
func (r *UserRepository) FindByUsernameWithPassword(ctx context.Context, db DBTX, username string) (*entity.User, error) {
	const query = `
		SELECT id, username, email, password, nama_lengkap, is_aktif,
		       created_at, created_by, updated_at, updated_by
		FROM users
		WHERE lower(username) = lower($1)`

	user := new(entity.User)

	err := db.QueryRowContext(ctx, query, username).Scan(
		&user.ID, &user.Username, &user.Email, &user.Password, &user.NamaLengkap,
		&user.IsAktif, &user.CreatedAt, &user.CreatedBy, &user.UpdatedAt, &user.UpdatedBy,
	)
	if err != nil {
		return nil, err
	}

	return user, nil
}

// FindByIDWithPassword is FindByUsernameWithPassword's counterpart keyed by
// id — isu #24 fase 1's self-service password change authenticates the
// caller by session (already know the id), not by username. Same rule as its
// sibling: returned regardless of is_aktif, and the bcrypt hash it carries
// must never reach a response.
func (r *UserRepository) FindByIDWithPassword(ctx context.Context, db DBTX, id int64) (*entity.User, error) {
	const query = `
		SELECT id, username, email, password, nama_lengkap, is_aktif,
		       created_at, created_by, updated_at, updated_by
		FROM users
		WHERE id = $1`

	user := new(entity.User)

	err := db.QueryRowContext(ctx, query, id).Scan(
		&user.ID, &user.Username, &user.Email, &user.Password, &user.NamaLengkap,
		&user.IsAktif, &user.CreatedAt, &user.CreatedBy, &user.UpdatedAt, &user.UpdatedBy,
	)
	if err != nil {
		return nil, err
	}

	return user, nil
}

// ExistsByUsername matches case-insensitively to mirror users_username_lower_uidx.
// exceptID skips one row so an update does not collide with itself; pass 0 when
// creating.
func (r *UserRepository) ExistsByUsername(ctx context.Context, db DBTX, username string, exceptID int64) (bool, error) {
	const query = `
		SELECT EXISTS (
			SELECT 1 FROM users
			WHERE lower(username) = lower($1)
			  AND ($2 = 0 OR id <> $2)
		)`

	var exists bool
	if err := db.QueryRowContext(ctx, query, username, exceptID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check users username: %w", err)
	}

	return exists, nil
}

// ExistsByEmail mirrors users_email_lower_uidx.
//
// Only call this when an email was actually supplied: the column is nullable, and
// lower(NULL) = lower(NULL) is never true, so a NULL check always answers false
// and tells you nothing — the same trap as the nullable master-data kode.
func (r *UserRepository) ExistsByEmail(ctx context.Context, db DBTX, email string, exceptID int64) (bool, error) {
	const query = `
		SELECT EXISTS (
			SELECT 1 FROM users
			WHERE lower(email) = lower($1)
			  AND ($2 = 0 OR id <> $2)
		)`

	var exists bool
	if err := db.QueryRowContext(ctx, query, email, exceptID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check users email: %w", err)
	}

	return exists, nil
}

// Update applies a patch and returns the stored row. RETURNING saves a second
// SELECT; sql.ErrNoRows means the id does not exist, so there is no separate
// existence check — that would be two queries and still racy.
func (r *UserRepository) Update(ctx context.Context, db DBTX, id int64, patch UserPatch) (*entity.User, error) {
	// COALESCE is right for NOT NULL columns only. On a nullable column it turns
	// `"email": null` into a no-op, so presence is passed as its own argument.
	// updated_at is left to the users_set_updated_at trigger.
	const query = `
		UPDATE users SET
			username     = COALESCE($2, username),
			email        = CASE WHEN $3::BOOLEAN THEN $4 ELSE email END,
			password     = COALESCE($5, password),
			nama_lengkap = CASE WHEN $6::BOOLEAN THEN $7 ELSE nama_lengkap END,
			is_aktif     = COALESCE($8, is_aktif),
			updated_by   = CASE WHEN $9::BOOLEAN THEN $10 ELSE updated_by END
		WHERE id = $1
		RETURNING ` + userColumns

	user := new(entity.User)

	err := db.QueryRowContext(
		ctx, query, id,
		patch.Username,
		patch.SetEmail, patch.Email,
		patch.Password,
		patch.SetNamaLengkap, patch.NamaLengkap,
		patch.IsAktif,
		patch.SetUpdatedBy, patch.UpdatedBy,
	).Scan(
		&user.ID, &user.Username, &user.Email, &user.NamaLengkap, &user.IsAktif,
		&user.CreatedAt, &user.CreatedBy, &user.UpdatedAt, &user.UpdatedBy,
	)
	if err != nil {
		return nil, err
	}

	return user, nil
}

// Touch bumps a user's row so the updated_at trigger fires and updated_by is
// recorded, without changing any other column.
//
// It exists for the one patch that changes nothing on users itself: a body that
// only replaces role_ids. Without it, reassigning roles would leave the user's
// updated_at pointing at whenever some unrelated field last changed. It also
// yields sql.ErrNoRows for an unknown id, which is how a roles-only patch still
// answers 404.
func (r *UserRepository) Touch(ctx context.Context, db DBTX, id int64, setUpdatedBy bool, updatedBy *int64) (*entity.User, error) {
	const query = `
		UPDATE users SET
			updated_by = CASE WHEN $2::BOOLEAN THEN $3 ELSE updated_by END
		WHERE id = $1
		RETURNING ` + userColumns

	user := new(entity.User)

	err := db.QueryRowContext(ctx, query, id, setUpdatedBy, updatedBy).Scan(
		&user.ID, &user.Username, &user.Email, &user.NamaLengkap, &user.IsAktif,
		&user.CreatedAt, &user.CreatedBy, &user.UpdatedAt, &user.UpdatedBy,
	)
	if err != nil {
		return nil, err
	}

	return user, nil
}

// Grant is one role a user holds, and the unit_kerja it applies to — isu #12
// fase 3. A nil IDUnitKerja means the grant applies to every unit.
type Grant struct {
	RoleID      int64
	IDUnitKerja *int64
}

// ReplaceRoles makes the user's grants exactly grants. Pass an empty slice to
// revoke everything.
//
// It is a diff, not a delete-then-insert: rows already holding a grant that
// stays are left alone, so user_role.created_at keeps saying when the grant
// actually started instead of resetting every time an unrelated one is added.
// Callers must run this inside the same transaction as the users write — a
// crash between the statements would otherwise leave a user half-reassigned.
//
// grants must already be deduplicated by (RoleID, IDUnitKerja); ON CONFLICT
// covers the race but not a body that repeats a pair.
func (r *UserRepository) ReplaceRoles(ctx context.Context, db DBTX, userID int64, grants []Grant, createdBy *int64) error {
	roleIDs := make([]int64, len(grants))
	unitIDs := make([]*int64, len(grants))
	for i, grant := range grants {
		roleIDs[i] = grant.RoleID
		unitIDs[i] = grant.IDUnitKerja
	}

	// The comparison must be NULL-safe: two NULL id_unit_kerja are the same grant
	// (both mean "every unit"), but plain `=` treats NULL as never equal to
	// anything, including another NULL. `IS NOT DISTINCT FROM` is the operator
	// that agrees two NULLs are the same value, which is exactly the trap
	// `role_id <> ALL($2)` fell into for a plain []int64: a NULL array made the
	// whole comparison NULL and silently deleted nothing. unnest of two empty
	// arrays produces zero rows, so NOT EXISTS is unconditionally true and an
	// empty grants slice still revokes everything.
	const deleteQuery = `
		DELETE FROM user_role ur
		WHERE ur.user_id = $1
		  AND NOT EXISTS (
		          SELECT 1 FROM unnest($2::BIGINT[], $3::BIGINT[]) AS t(role_id, id_unit_kerja)
		          WHERE t.role_id = ur.role_id
		            AND t.id_unit_kerja IS NOT DISTINCT FROM ur.id_unit_kerja
		      )`

	if _, err := db.ExecContext(ctx, deleteQuery, userID, roleIDs, unitIDs); err != nil {
		return fmt.Errorf("delete user_role: %w", err)
	}

	if len(grants) == 0 {
		return nil
	}

	// Two statements, not one, because a single ON CONFLICT clause can only name
	// one arbiter index — and a NULL id_unit_kerja and a real one are governed by
	// two different unique indexes (user_role_grant_uidx and the partial
	// user_role_grant_global_uidx), for the same NULL-is-never-equal reason the
	// delete above has to work around. Each statement inserts only the rows the
	// other one's arbiter does not cover.
	const insertScoped = `
		INSERT INTO user_role (user_id, role_id, id_unit_kerja, created_by)
		SELECT $1, role_id, id_unit_kerja, $4
		FROM unnest($2::BIGINT[], $3::BIGINT[]) AS t(role_id, id_unit_kerja)
		WHERE id_unit_kerja IS NOT NULL
		ON CONFLICT (user_id, role_id, id_unit_kerja) DO NOTHING`

	if _, err := db.ExecContext(ctx, insertScoped, userID, roleIDs, unitIDs, createdBy); err != nil {
		return fmt.Errorf("insert user_role (scoped): %w", err)
	}

	const insertGlobal = `
		INSERT INTO user_role (user_id, role_id, id_unit_kerja, created_by)
		SELECT $1, role_id, NULL, $4
		FROM unnest($2::BIGINT[], $3::BIGINT[]) AS t(role_id, id_unit_kerja)
		WHERE id_unit_kerja IS NULL
		ON CONFLICT (user_id, role_id) WHERE id_unit_kerja IS NULL DO NOTHING`

	if _, err := db.ExecContext(ctx, insertGlobal, userID, roleIDs, unitIDs, createdBy); err != nil {
		return fmt.Errorf("insert user_role (global): %w", err)
	}

	return nil
}

// FindRolesByUserIDs returns every grant held by the given users, tagged with the
// user it belongs to.
//
// One query for a whole page, not one per user: attaching grants row by row
// would be an N+1 that grows with page size. Retired roles are included — the
// grant is still real and still needs revoking — and RoleRef.IsAktif tells them
// apart. A retired unit_kerja works the same way and needs no special handling
// here: id_unit_kerja is returned as-is either way, and the caller decides what
// a retired unit means.
//
// The same role can appear more than once for one user since isu #12 fase 3 —
// once per unit_kerja it was granted in — because a grant is now (role, unit),
// not just role.
func (r *UserRepository) FindRolesByUserIDs(ctx context.Context, db DBTX, userIDs []int64) ([]entity.RoleAssignment, error) {
	if len(userIDs) == 0 {
		return []entity.RoleAssignment{}, nil
	}

	// LEFT JOIN unit_kerja: id_unit_kerja is nullable (a global grant), and an
	// INNER join would silently drop those rows' unit name lookup instead of
	// simply leaving it NULL.
	//
	// ORDER BY ends in id_unit_kerja (NULLS FIRST, so a global grant sorts ahead
	// of a scoped one) so a user's grants come back in a stable order across
	// requests — (role, unit) pairs are unique per user, so this is a genuine
	// tiebreaker rather than an accidental one.
	const query = `
		SELECT ur.id, ur.user_id, r.id, r.nama, r.is_aktif,
		       r.created_at, r.created_by, r.updated_at, r.updated_by,
		       ur.id_unit_kerja, uk.nama, uk.is_aktif
		FROM user_role ur
		JOIN role r ON r.id = ur.role_id
		LEFT JOIN unit_kerja uk ON uk.id = ur.id_unit_kerja
		WHERE ur.user_id = ANY($1)
		ORDER BY ur.user_id, r.nama, r.id, ur.id_unit_kerja NULLS FIRST`

	rows, err := db.QueryContext(ctx, query, userIDs)
	if err != nil {
		return nil, fmt.Errorf("select user_role: %w", err)
	}
	defer rows.Close()

	assignments := make([]entity.RoleAssignment, 0, len(userIDs))

	for rows.Next() {
		var assignment entity.RoleAssignment

		if err := rows.Scan(
			&assignment.ID, &assignment.UserID,
			&assignment.Role.ID, &assignment.Role.Nama, &assignment.Role.IsAktif,
			&assignment.Role.CreatedAt, &assignment.Role.CreatedBy,
			&assignment.Role.UpdatedAt, &assignment.Role.UpdatedBy,
			&assignment.IDUnitKerja, &assignment.NamaUnitKerja, &assignment.IsAktifUnitKerja,
		); err != nil {
			return nil, fmt.Errorf("scan user_role: %w", err)
		}

		assignments = append(assignments, assignment)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user_role: %w", err)
	}

	return assignments, nil
}

// FindGrantByID returns the user_role row named by id, joined to its role and
// unit — isu #12 fase 4. This is the one place a session's claims are checked
// against the database rather than trusted: POST /auth/switch-context must
// refuse a grant that has since been revoked, or whose role or unit has been
// retired, or that never belonged to the caller in the first place, and none
// of that is visible in a stateless token. sql.ErrNoRows means the id does not
// exist; the usecase maps that the same way it maps "exists but is not the
// caller's, or not active" — one 403, not three.
func (r *UserRepository) FindGrantByID(ctx context.Context, db DBTX, id int64) (*entity.RoleAssignment, error) {
	const query = `
		SELECT ur.id, ur.user_id, r.id, r.nama, r.is_aktif,
		       r.created_at, r.created_by, r.updated_at, r.updated_by,
		       ur.id_unit_kerja, uk.nama, uk.is_aktif
		FROM user_role ur
		JOIN role r ON r.id = ur.role_id
		LEFT JOIN unit_kerja uk ON uk.id = ur.id_unit_kerja
		WHERE ur.id = $1`

	assignment := new(entity.RoleAssignment)

	err := db.QueryRowContext(ctx, query, id).Scan(
		&assignment.ID, &assignment.UserID,
		&assignment.Role.ID, &assignment.Role.Nama, &assignment.Role.IsAktif,
		&assignment.Role.CreatedAt, &assignment.Role.CreatedBy,
		&assignment.Role.UpdatedAt, &assignment.Role.UpdatedBy,
		&assignment.IDUnitKerja, &assignment.NamaUnitKerja, &assignment.IsAktifUnitKerja,
	)
	if err != nil {
		return nil, err
	}

	return assignment, nil
}

// Search returns one page of users plus the total matching count. A nil isAktif or
// roleID means "no filter". Roles are attached by the usecase.
func (r *UserRepository) Search(ctx context.Context, db DBTX, search string, isAktif *bool, roleID *int64, limit, offset int) ([]entity.User, int64, error) {
	search = EscapeLike(search)

	var total int64
	if err := db.QueryRowContext(
		ctx, `SELECT COUNT(*) FROM users u`+userFilter, search, isAktif, roleID,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count users: %w", err)
	}

	if total == 0 {
		return []entity.User{}, 0, nil
	}

	// ORDER BY ends in a unique column: username alone is unique today, but the
	// habit is what keeps a page from repeating a row once a non-unique sort key
	// is offered.
	query := `SELECT ` + userReadColumns + ` FROM users u` + userFilter + `
		ORDER BY u.username, u.id
		LIMIT $4 OFFSET $5`

	rows, err := db.QueryContext(ctx, query, search, isAktif, roleID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("select users: %w", err)
	}
	defer rows.Close()

	list := make([]entity.User, 0, limit)

	for rows.Next() {
		var user entity.User

		if err := rows.Scan(
			&user.ID, &user.Username, &user.Email, &user.NamaLengkap, &user.IsAktif,
			&user.CreatedAt, &user.CreatedBy, &user.UpdatedAt, &user.UpdatedBy,
		); err != nil {
			return nil, 0, fmt.Errorf("scan users: %w", err)
		}

		list = append(list, user)
	}

	// Without this, an error partway through iteration passes silently and the
	// caller gets a truncated page.
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate users: %w", err)
	}

	return list, total, nil
}
