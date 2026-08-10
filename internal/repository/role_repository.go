package repository

import (
	"context"
	"fmt"

	"Arthafreestyle/ERP/internal/entity"
)

// RoleRepository owns every SQL statement touching role.
type RoleRepository struct{}

func NewRoleRepository() *RoleRepository {
	return &RoleRepository{}
}

// roleColumns is the column list for INSERT/UPDATE ... RETURNING. Declared once so
// Scan order cannot drift from it when a migration adds a column.
const roleColumns = `id, nama, is_aktif,
	created_at, created_by, updated_at, updated_by`

// roleFilter is shared by the COUNT and the row query. Two copies of a filter
// eventually diverge and total_item starts lying about the data.
//
// Placeholder discipline: the filter owns $1..$2 and pagination follows after it.
const roleFilter = `
	WHERE ($1 = '' OR nama ILIKE '%' || $1 || '%')
	  AND ($2::BOOLEAN IS NULL OR is_aktif = $2)`

// RolePatch carries a partial update. Both columns are NOT NULL, so neither needs
// a presence flag — a nil pointer means "absent" and COALESCE keeps the old value,
// which is exactly right here. Clearing is rejected in the usecase.
type RolePatch struct {
	Nama         *string
	IsAktif      *bool
	SetUpdatedBy bool
	UpdatedBy    *int64
}

func (r *RoleRepository) Create(ctx context.Context, db DBTX, role *entity.Role) error {
	const query = `
		INSERT INTO role (nama, is_aktif, created_by)
		VALUES ($1, $2, $3)
		RETURNING ` + roleColumns

	err := db.QueryRowContext(ctx, query, role.Nama, role.IsAktif, role.CreatedBy).Scan(
		&role.ID, &role.Nama, &role.IsAktif,
		&role.CreatedAt, &role.CreatedBy, &role.UpdatedAt, &role.UpdatedBy,
	)
	if err != nil {
		return fmt.Errorf("insert role: %w", err)
	}

	return nil
}

// FindByID returns sql.ErrNoRows when the row is absent; the usecase maps that to
// a 404.
func (r *RoleRepository) FindByID(ctx context.Context, db DBTX, id int64) (*entity.Role, error) {
	const query = `SELECT ` + roleColumns + ` FROM role WHERE id = $1`

	role := new(entity.Role)

	err := db.QueryRowContext(ctx, query, id).Scan(
		&role.ID, &role.Nama, &role.IsAktif,
		&role.CreatedAt, &role.CreatedBy, &role.UpdatedAt, &role.UpdatedBy,
	)
	if err != nil {
		return nil, err
	}

	return role, nil
}

// ExistsByNama matches case-insensitively to mirror role_nama_lower_uidx.
// exceptID skips one row so an update does not collide with itself; pass 0 when
// creating.
//
// nama is NOT NULL here, so unlike the master-data kode checks there is no
// multiple-NULL caveat: this answer is always meaningful.
func (r *RoleRepository) ExistsByNama(ctx context.Context, db DBTX, nama string, exceptID int64) (bool, error) {
	const query = `
		SELECT EXISTS (
			SELECT 1 FROM role
			WHERE lower(nama) = lower($1)
			  AND ($2 = 0 OR id <> $2)
		)`

	var exists bool
	if err := db.QueryRowContext(ctx, query, nama, exceptID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check role nama: %w", err)
	}

	return exists, nil
}

// CountActiveByIDs reports how many of the given ids are rows that exist and are
// still active. The usecase compares that against the number of distinct ids it
// asked about, which is what turns an unknown or retired role id into a 400
// naming the problem instead of a foreign-key 500.
//
// = ANY($1) takes a Go []int64 directly: pgx's stdlib driver implements
// CheckNamedValue, so database/sql passes the slice through untouched instead of
// rejecting it as an unsupported driver.Value.
func (r *RoleRepository) CountActiveByIDs(ctx context.Context, db DBTX, ids []int64) (int64, error) {
	const query = `SELECT COUNT(*) FROM role WHERE id = ANY($1) AND is_aktif`

	var count int64
	if err := db.QueryRowContext(ctx, query, ids).Scan(&count); err != nil {
		return 0, fmt.Errorf("count active role: %w", err)
	}

	return count, nil
}

// Update applies a patch and returns the stored row. RETURNING saves a second
// SELECT; sql.ErrNoRows means the id does not exist, so there is no separate
// existence check — that would be two queries and still racy.
func (r *RoleRepository) Update(ctx context.Context, db DBTX, id int64, patch RolePatch) (*entity.Role, error) {
	// Both nama and is_aktif are NOT NULL, so COALESCE is correct for both.
	// updated_at is left to the role_set_updated_at trigger.
	const query = `
		UPDATE role SET
			nama       = COALESCE($2, nama),
			is_aktif   = COALESCE($3, is_aktif),
			updated_by = CASE WHEN $4::BOOLEAN THEN $5 ELSE updated_by END
		WHERE id = $1
		RETURNING ` + roleColumns

	role := new(entity.Role)

	err := db.QueryRowContext(
		ctx, query, id,
		patch.Nama, patch.IsAktif,
		patch.SetUpdatedBy, patch.UpdatedBy,
	).Scan(
		&role.ID, &role.Nama, &role.IsAktif,
		&role.CreatedAt, &role.CreatedBy, &role.UpdatedAt, &role.UpdatedBy,
	)
	if err != nil {
		return nil, err
	}

	return role, nil
}

// Search returns one page of rows plus the total matching count. A nil isAktif
// means "no filter".
func (r *RoleRepository) Search(ctx context.Context, db DBTX, search string, isAktif *bool, limit, offset int) ([]entity.Role, int64, error) {
	search = EscapeLike(search)

	var total int64
	if err := db.QueryRowContext(
		ctx, `SELECT COUNT(*) FROM role`+roleFilter, search, isAktif,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count role: %w", err)
	}

	if total == 0 {
		return []entity.Role{}, 0, nil
	}

	// ORDER BY ends in a unique column: nama alone leaves ties in an unspecified
	// order, which lets one row appear on two pages while another is never
	// returned at all.
	query := `SELECT ` + roleColumns + ` FROM role` + roleFilter + `
		ORDER BY nama, id
		LIMIT $3 OFFSET $4`

	rows, err := db.QueryContext(ctx, query, search, isAktif, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("select role: %w", err)
	}
	defer rows.Close()

	list := make([]entity.Role, 0, limit)

	for rows.Next() {
		var role entity.Role

		if err := rows.Scan(
			&role.ID, &role.Nama, &role.IsAktif,
			&role.CreatedAt, &role.CreatedBy, &role.UpdatedAt, &role.UpdatedBy,
		); err != nil {
			return nil, 0, fmt.Errorf("scan role: %w", err)
		}

		list = append(list, role)
	}

	// Without this, an error partway through iteration passes silently and the
	// caller gets a truncated page.
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate role: %w", err)
	}

	return list, total, nil
}
