package repository

import (
	"context"
	"fmt"

	"Arthafreestyle/ERP/internal/entity"
)

// EkspedisiRepository owns every SQL statement touching ekspedisi.
type EkspedisiRepository struct{}

func NewEkspedisiRepository() *EkspedisiRepository {
	return &EkspedisiRepository{}
}

// Written once and reused so Scan order can never drift from the SELECT list.
const ekspedisiColumns = `id, nama, telepon, is_aktif, created_at, created_by, updated_at, updated_by`

// Shared by the COUNT and the row query — two copies eventually diverge and
// total_item starts lying. Filter owns $1..$2; pagination follows.
const ekspedisiFilter = `
	WHERE ($1 = '' OR nama ILIKE '%' || $1 || '%' OR telepon ILIKE '%' || $1 || '%')
	  AND ($2::BOOLEAN IS NULL OR is_aktif = $2)`

// EkspedisiPatch carries a partial update. Set* flags mark "key was present in
// the JSON body", which is what lets a nullable column be cleared.
type EkspedisiPatch struct {
	Nama         *string // NOT NULL: changeable, never clearable, so no flag
	SetTelepon   bool
	Telepon      *string
	IsAktif      *bool
	SetUpdatedBy bool
	UpdatedBy    *int64
}

func (r *EkspedisiRepository) Create(ctx context.Context, db DBTX, ekspedisi *entity.Ekspedisi) error {
	const query = `
		INSERT INTO ekspedisi (nama, telepon, is_aktif, created_by)
		VALUES ($1, $2, $3, $4)
		RETURNING ` + ekspedisiColumns

	err := db.QueryRowContext(
		ctx, query, ekspedisi.Nama, ekspedisi.Telepon, ekspedisi.IsAktif, ekspedisi.CreatedBy,
	).Scan(
		&ekspedisi.ID, &ekspedisi.Nama, &ekspedisi.Telepon, &ekspedisi.IsAktif,
		&ekspedisi.CreatedAt, &ekspedisi.CreatedBy, &ekspedisi.UpdatedAt, &ekspedisi.UpdatedBy,
	)
	if err != nil {
		return fmt.Errorf("insert ekspedisi: %w", err)
	}

	return nil
}

// FindByID returns sql.ErrNoRows when the row is absent; the usecase maps that
// to a 404.
func (r *EkspedisiRepository) FindByID(ctx context.Context, db DBTX, id int64) (*entity.Ekspedisi, error) {
	const query = `SELECT ` + ekspedisiColumns + ` FROM ekspedisi WHERE id = $1`

	ekspedisi := new(entity.Ekspedisi)

	err := db.QueryRowContext(ctx, query, id).Scan(
		&ekspedisi.ID, &ekspedisi.Nama, &ekspedisi.Telepon, &ekspedisi.IsAktif,
		&ekspedisi.CreatedAt, &ekspedisi.CreatedBy, &ekspedisi.UpdatedAt, &ekspedisi.UpdatedBy,
	)
	if err != nil {
		return nil, err
	}

	return ekspedisi, nil
}

// ExistsByNama matches case-insensitively to mirror ekspedisi_nama_lower_uidx.
// exceptID skips one row so an update does not collide with itself; pass 0 when
// creating.
func (r *EkspedisiRepository) ExistsByNama(ctx context.Context, db DBTX, nama string, exceptID int64) (bool, error) {
	const query = `
		SELECT EXISTS (
			SELECT 1 FROM ekspedisi
			WHERE lower(nama) = lower($1)
			  AND ($2 = 0 OR id <> $2)
		)`

	var exists bool
	if err := db.QueryRowContext(ctx, query, nama, exceptID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check ekspedisi nama: %w", err)
	}

	return exists, nil
}

// Update applies a patch and returns the stored row. RETURNING saves a second
// SELECT and avoids reading back another transaction's write; sql.ErrNoRows
// means the id does not exist.
func (r *EkspedisiRepository) Update(ctx context.Context, db DBTX, id int64, patch EkspedisiPatch) (*entity.Ekspedisi, error) {
	// COALESCE is right for NOT NULL columns only. For telepon it would make
	// `"telepon": null` a no-op, so presence is passed explicitly instead.
	// updated_at is left to the ekspedisi_set_updated_at trigger.
	const query = `
		UPDATE ekspedisi SET
			nama       = COALESCE($2, nama),
			telepon    = CASE WHEN $3::BOOLEAN THEN $4 ELSE telepon END,
			is_aktif   = COALESCE($5, is_aktif),
			updated_by = CASE WHEN $6::BOOLEAN THEN $7 ELSE updated_by END
		WHERE id = $1
		RETURNING ` + ekspedisiColumns

	ekspedisi := new(entity.Ekspedisi)

	err := db.QueryRowContext(
		ctx, query, id,
		patch.Nama,
		patch.SetTelepon, patch.Telepon,
		patch.IsAktif,
		patch.SetUpdatedBy, patch.UpdatedBy,
	).Scan(
		&ekspedisi.ID, &ekspedisi.Nama, &ekspedisi.Telepon, &ekspedisi.IsAktif,
		&ekspedisi.CreatedAt, &ekspedisi.CreatedBy, &ekspedisi.UpdatedAt, &ekspedisi.UpdatedBy,
	)
	if err != nil {
		return nil, err
	}

	return ekspedisi, nil
}

// Search returns one page of rows plus the total matching count. A nil isAktif
// means "no filter". Exactly two queries, whatever the row count.
func (r *EkspedisiRepository) Search(ctx context.Context, db DBTX, search string, isAktif *bool, limit, offset int) ([]entity.Ekspedisi, int64, error) {
	search = EscapeLike(search)

	var total int64
	if err := db.QueryRowContext(
		ctx, `SELECT COUNT(*) FROM ekspedisi`+ekspedisiFilter, search, isAktif,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count ekspedisi: %w", err)
	}

	if total == 0 {
		return []entity.Ekspedisi{}, 0, nil
	}

	// ORDER BY ends in id so tied names cannot shuffle between pages.
	query := `SELECT ` + ekspedisiColumns + ` FROM ekspedisi` + ekspedisiFilter + `
		ORDER BY nama, id
		LIMIT $3 OFFSET $4`

	rows, err := db.QueryContext(ctx, query, search, isAktif, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("select ekspedisi: %w", err)
	}
	defer rows.Close()

	list := make([]entity.Ekspedisi, 0, limit)

	for rows.Next() {
		var ekspedisi entity.Ekspedisi

		if err := rows.Scan(
			&ekspedisi.ID, &ekspedisi.Nama, &ekspedisi.Telepon, &ekspedisi.IsAktif,
			&ekspedisi.CreatedAt, &ekspedisi.CreatedBy, &ekspedisi.UpdatedAt, &ekspedisi.UpdatedBy,
		); err != nil {
			return nil, 0, fmt.Errorf("scan ekspedisi: %w", err)
		}

		list = append(list, ekspedisi)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate ekspedisi: %w", err)
	}

	return list, total, nil
}
