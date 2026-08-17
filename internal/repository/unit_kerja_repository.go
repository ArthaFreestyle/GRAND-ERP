package repository

import (
	"context"
	"fmt"

	"Arthafreestyle/ERP/internal/entity"
)

// UnitKerjaRepository owns every SQL statement touching unit_kerja.
type UnitKerjaRepository struct{}

func NewUnitKerjaRepository() *UnitKerjaRepository {
	return &UnitKerjaRepository{}
}

// unitKerjaColumns is the write-side list, unqualified, for INSERT ... RETURNING.
const unitKerjaColumns = `id, kode, nama, is_aktif, created_at, created_by, updated_at, updated_by`

// unitKerjaReadColumns adds the creator's name, resolved by the join in
// unitKerjaFrom. Fetching it per row instead would be an N+1: one query for the
// page plus one per unit.
const unitKerjaReadColumns = `uk.id, uk.kode, uk.nama, uk.is_aktif,
	uk.created_at, uk.created_by, uk.updated_at, uk.updated_by, u.nama_lengkap`

// unitKerjaFrom joins users LEFT, not INNER: unit_kerja.created_by is nullable,
// and an inner join would silently drop every unit without a creator, leaving
// the page shorter than total_item claims.
const unitKerjaFrom = `
	FROM unit_kerja uk
	LEFT JOIN users u ON u.id = uk.created_by`

// unitKerjaFilter is shared by the COUNT and the row query. Two copies of a
// filter eventually diverge and total_item starts lying about the data.
//
// Placeholder discipline: the filter owns $1..$2 and pagination follows after
// it. Inserting a new filter means shifting LIMIT/OFFSET too.
const unitKerjaFilter = `
	WHERE ($1 = '' OR uk.nama ILIKE '%' || $1 || '%' OR uk.kode ILIKE '%' || $1 || '%')
	  AND ($2::BOOLEAN IS NULL OR uk.is_aktif = $2)`

// UnitKerjaPatch carries a partial update. Every nullable column gets a Set
// flag answering "was this key in the JSON body at all?" — without it,
// clearing a column is indistinguishable from leaving it alone.
type UnitKerjaPatch struct {
	SetKode      bool
	Kode         *string
	Nama         *string // NOT NULL: changeable, never clearable, so no flag
	IsAktif      *bool
	SetUpdatedBy bool
	UpdatedBy    *int64
}

func (r *UnitKerjaRepository) Create(ctx context.Context, db DBTX, unitKerja *entity.UnitKerja) error {
	const query = `
		INSERT INTO unit_kerja (kode, nama, is_aktif, created_by)
		VALUES ($1, $2, $3, $4)
		RETURNING ` + unitKerjaColumns

	err := db.QueryRowContext(
		ctx, query, unitKerja.Kode, unitKerja.Nama, unitKerja.IsAktif, unitKerja.CreatedBy,
	).Scan(
		&unitKerja.ID, &unitKerja.Kode, &unitKerja.Nama, &unitKerja.IsAktif,
		&unitKerja.CreatedAt, &unitKerja.CreatedBy, &unitKerja.UpdatedAt, &unitKerja.UpdatedBy,
	)
	if err != nil {
		return fmt.Errorf("insert unit_kerja: %w", err)
	}

	return nil
}

// FindByID returns sql.ErrNoRows when the row is absent; the usecase maps that
// to a 404.
func (r *UnitKerjaRepository) FindByID(ctx context.Context, db DBTX, id int64) (*entity.UnitKerja, error) {
	const query = `SELECT ` + unitKerjaReadColumns + unitKerjaFrom + ` WHERE uk.id = $1`

	unitKerja := new(entity.UnitKerja)

	err := db.QueryRowContext(ctx, query, id).Scan(
		&unitKerja.ID, &unitKerja.Kode, &unitKerja.Nama, &unitKerja.IsAktif,
		&unitKerja.CreatedAt, &unitKerja.CreatedBy, &unitKerja.UpdatedAt, &unitKerja.UpdatedBy,
		&unitKerja.NamaPembuat,
	)
	if err != nil {
		return nil, err
	}

	return unitKerja, nil
}

// KodeByID returns a unit's kode, without the rest of the row — isu #21 fase 1
// needs only this to build a document number. Nil means the unit has none yet,
// which nomorDokumen refuses to build a number against rather than silently
// falling back to the numeric id.
func (r *UnitKerjaRepository) KodeByID(ctx context.Context, db DBTX, id int64) (*string, error) {
	const query = `SELECT kode FROM unit_kerja WHERE id = $1`

	var kode *string
	if err := db.QueryRowContext(ctx, query, id).Scan(&kode); err != nil {
		return nil, err
	}

	return kode, nil
}

// ExistsByKode matches case-insensitively to mirror unit_kerja_kode_lower_uidx.
// exceptID skips one row so an update does not collide with itself; pass 0 when
// creating.
//
// Only call this when a kode was actually supplied: kode is nullable, and
// lower(NULL) = lower(NULL) is never true, so a NULL check always answers false
// and tells you nothing.
func (r *UnitKerjaRepository) ExistsByKode(ctx context.Context, db DBTX, kode string, exceptID int64) (bool, error) {
	const query = `
		SELECT EXISTS (
			SELECT 1 FROM unit_kerja
			WHERE lower(kode) = lower($1)
			  AND ($2 = 0 OR id <> $2)
		)`

	var exists bool
	if err := db.QueryRowContext(ctx, query, kode, exceptID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check unit_kerja kode: %w", err)
	}

	return exists, nil
}

// CountActiveByIDs reports how many of the given ids are rows that exist and are
// still active. Used to validate a grant's id_unit_kerja the same way
// RoleRepository.CountActiveByIDs validates id_role: the foreign key alone
// cannot tell a retired unit from a live one, and its message names a
// constraint rather than the field. Callers must deduplicate ids first, or a
// valid request can be wrongly rejected.
func (r *UnitKerjaRepository) CountActiveByIDs(ctx context.Context, db DBTX, ids []int64) (int64, error) {
	const query = `SELECT COUNT(*) FROM unit_kerja WHERE id = ANY($1) AND is_aktif`

	var count int64
	if err := db.QueryRowContext(ctx, query, ids).Scan(&count); err != nil {
		return 0, fmt.Errorf("count active unit_kerja: %w", err)
	}

	return count, nil
}

// Update applies a patch and returns the stored row. RETURNING saves a second
// SELECT and avoids reading back another transaction's write; sql.ErrNoRows
// means the id does not exist, so there is no separate existence check — that
// would be two queries and still racy.
func (r *UnitKerjaRepository) Update(ctx context.Context, db DBTX, id int64, patch UnitKerjaPatch) (*entity.UnitKerja, error) {
	// COALESCE is right for NOT NULL columns only. On a nullable column it turns
	// `"kode": null` into a no-op, so presence is passed as its own argument.
	// updated_at is left to the unit_kerja_set_updated_at trigger.
	const query = `
		UPDATE unit_kerja SET
			kode       = CASE WHEN $2::BOOLEAN THEN $3 ELSE kode END,
			nama       = COALESCE($4, nama),
			is_aktif   = COALESCE($5, is_aktif),
			updated_by = CASE WHEN $6::BOOLEAN THEN $7 ELSE updated_by END
		WHERE id = $1
		RETURNING ` + unitKerjaColumns

	unitKerja := new(entity.UnitKerja)

	err := db.QueryRowContext(
		ctx, query, id,
		patch.SetKode, patch.Kode,
		patch.Nama,
		patch.IsAktif,
		patch.SetUpdatedBy, patch.UpdatedBy,
	).Scan(
		&unitKerja.ID, &unitKerja.Kode, &unitKerja.Nama, &unitKerja.IsAktif,
		&unitKerja.CreatedAt, &unitKerja.CreatedBy, &unitKerja.UpdatedAt, &unitKerja.UpdatedBy,
	)
	if err != nil {
		return nil, err
	}

	return unitKerja, nil
}

// Search returns one page of rows plus the total matching count. A nil isAktif
// means "no filter". Exactly two queries, whatever the row count — the
// creator's name rides along on the join.
func (r *UnitKerjaRepository) Search(ctx context.Context, db DBTX, search string, isAktif *bool, limit, offset int) ([]entity.UnitKerja, int64, error) {
	search = EscapeLike(search)

	// COUNT skips the join: nothing in the filter needs users.
	var total int64
	if err := db.QueryRowContext(
		ctx, `SELECT COUNT(*) FROM unit_kerja uk`+unitKerjaFilter, search, isAktif,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count unit_kerja: %w", err)
	}

	if total == 0 {
		return []entity.UnitKerja{}, 0, nil
	}

	// ORDER BY ends in a unique column: nama alone leaves ties in an unspecified
	// order, which lets one row appear on two pages while another is never
	// returned at all.
	query := `SELECT ` + unitKerjaReadColumns + unitKerjaFrom + unitKerjaFilter + `
		ORDER BY uk.nama, uk.id
		LIMIT $3 OFFSET $4`

	rows, err := db.QueryContext(ctx, query, search, isAktif, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("select unit_kerja: %w", err)
	}
	defer rows.Close()

	list := make([]entity.UnitKerja, 0, limit)

	for rows.Next() {
		var unitKerja entity.UnitKerja

		if err := rows.Scan(
			&unitKerja.ID, &unitKerja.Kode, &unitKerja.Nama, &unitKerja.IsAktif,
			&unitKerja.CreatedAt, &unitKerja.CreatedBy, &unitKerja.UpdatedAt, &unitKerja.UpdatedBy,
			&unitKerja.NamaPembuat,
		); err != nil {
			return nil, 0, fmt.Errorf("scan unit_kerja: %w", err)
		}

		list = append(list, unitKerja)
	}

	// Without this, an error partway through iteration passes silently and the
	// caller gets a truncated page.
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate unit_kerja: %w", err)
	}

	return list, total, nil
}
