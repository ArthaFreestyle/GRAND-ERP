package repository

import (
	"context"
	"fmt"

	"Arthafreestyle/ERP/internal/entity"
)

// SatuanRepository owns every SQL statement touching satuan. Placeholders are
// $1, $2, ... and inserts use RETURNING — the pgx driver has no LastInsertId.
type SatuanRepository struct{}

func NewSatuanRepository() *SatuanRepository {
	return &SatuanRepository{}
}

// satuanColumns is written once and reused by every statement so the Scan
// argument order can never drift from the SELECT list. Never SELECT * — the
// next migration that adds a column would break every Scan.
const satuanColumns = `id, nama, is_aktif, created_at, created_by, updated_at, updated_by`

// satuanFilter is shared by the COUNT and the row query. Written twice, the two
// eventually diverge and total_item starts lying about the data.
//
// Placeholder discipline: the filter always uses $1..$2 and pagination follows
// after it. Inserting a new filter means shifting LIMIT/OFFSET too.
const satuanFilter = `
	WHERE ($1 = '' OR nama ILIKE '%' || $1 || '%')
	  AND ($2::BOOLEAN IS NULL OR is_aktif = $2)`

// SatuanPatch carries a partial update. For every nullable column the Set flag
// answers "was this key in the JSON body at all?" — without it, clearing a
// column is indistinguishable from leaving it alone.
//
// Nama is *string because the column is NOT NULL: it can be changed but never
// cleared, so no flag is needed.
type SatuanPatch struct {
	Nama         *string
	IsAktif      *bool
	SetUpdatedBy bool
	UpdatedBy    *int64
}

func (r *SatuanRepository) Create(ctx context.Context, db DBTX, satuan *entity.Satuan) error {
	const query = `
		INSERT INTO satuan (nama, is_aktif, created_by)
		VALUES ($1, $2, $3)
		RETURNING ` + satuanColumns

	err := db.QueryRowContext(ctx, query, satuan.Nama, satuan.IsAktif, satuan.CreatedBy).Scan(
		&satuan.ID, &satuan.Nama, &satuan.IsAktif,
		&satuan.CreatedAt, &satuan.CreatedBy, &satuan.UpdatedAt, &satuan.UpdatedBy,
	)
	if err != nil {
		return fmt.Errorf("insert satuan: %w", err)
	}

	return nil
}

// FindByID returns sql.ErrNoRows when the row is absent; the usecase maps that
// to a 404.
func (r *SatuanRepository) FindByID(ctx context.Context, db DBTX, id int64) (*entity.Satuan, error) {
	const query = `SELECT ` + satuanColumns + ` FROM satuan WHERE id = $1`

	satuan := new(entity.Satuan)

	err := db.QueryRowContext(ctx, query, id).Scan(
		&satuan.ID, &satuan.Nama, &satuan.IsAktif,
		&satuan.CreatedAt, &satuan.CreatedBy, &satuan.UpdatedAt, &satuan.UpdatedBy,
	)
	if err != nil {
		return nil, err
	}

	return satuan, nil
}

// ExistsByNama matches case-insensitively to mirror satuan_nama_lower_uidx.
// exceptID skips one row so an update does not collide with itself; pass 0 when
// creating.
func (r *SatuanRepository) ExistsByNama(ctx context.Context, db DBTX, nama string, exceptID int64) (bool, error) {
	const query = `
		SELECT EXISTS (
			SELECT 1 FROM satuan
			WHERE lower(nama) = lower($1)
			  AND ($2 = 0 OR id <> $2)
		)`

	var exists bool
	if err := db.QueryRowContext(ctx, query, nama, exceptID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check satuan nama: %w", err)
	}

	return exists, nil
}

// Update applies a patch and returns the stored row. RETURNING avoids a second
// SELECT, and with it a chance of reading back what another transaction wrote.
// sql.ErrNoRows means the id does not exist — no separate existence check, which
// would be two queries and still racy.
func (r *SatuanRepository) Update(ctx context.Context, db DBTX, id int64, patch SatuanPatch) (*entity.Satuan, error) {
	// updated_at is left to the satuan_set_updated_at trigger.
	const query = `
		UPDATE satuan SET
			nama       = COALESCE($2, nama),
			is_aktif   = COALESCE($3, is_aktif),
			updated_by = CASE WHEN $4::BOOLEAN THEN $5 ELSE updated_by END
		WHERE id = $1
		RETURNING ` + satuanColumns

	satuan := new(entity.Satuan)

	err := db.QueryRowContext(
		ctx, query, id, patch.Nama, patch.IsAktif, patch.SetUpdatedBy, patch.UpdatedBy,
	).Scan(
		&satuan.ID, &satuan.Nama, &satuan.IsAktif,
		&satuan.CreatedAt, &satuan.CreatedBy, &satuan.UpdatedAt, &satuan.UpdatedBy,
	)
	if err != nil {
		return nil, err
	}

	return satuan, nil
}

// Search returns one page of rows plus the total matching count. A nil isAktif
// means "no filter". Exactly two queries, whatever the row count.
func (r *SatuanRepository) Search(ctx context.Context, db DBTX, search string, isAktif *bool, limit, offset int) ([]entity.Satuan, int64, error) {
	search = EscapeLike(search)

	var total int64
	if err := db.QueryRowContext(
		ctx, `SELECT COUNT(*) FROM satuan`+satuanFilter, search, isAktif,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count satuan: %w", err)
	}

	// Skip the row query when the page is certainly empty.
	if total == 0 {
		return []entity.Satuan{}, 0, nil
	}

	// ORDER BY ends in id: nama alone leaves ties in an unspecified order, which
	// lets a row appear on two pages while another is never returned.
	query := `SELECT ` + satuanColumns + ` FROM satuan` + satuanFilter + `
		ORDER BY nama, id
		LIMIT $3 OFFSET $4`

	rows, err := db.QueryContext(ctx, query, search, isAktif, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("select satuan: %w", err)
	}
	defer rows.Close()

	list := make([]entity.Satuan, 0, limit)

	for rows.Next() {
		var satuan entity.Satuan

		if err := rows.Scan(
			&satuan.ID, &satuan.Nama, &satuan.IsAktif,
			&satuan.CreatedAt, &satuan.CreatedBy, &satuan.UpdatedAt, &satuan.UpdatedBy,
		); err != nil {
			return nil, 0, fmt.Errorf("scan satuan: %w", err)
		}

		list = append(list, satuan)
	}

	// Without this, an error partway through iteration passes silently and the
	// caller gets a truncated page.
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate satuan: %w", err)
	}

	return list, total, nil
}
