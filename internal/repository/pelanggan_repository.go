package repository

import (
	"context"
	"fmt"

	"Arthafreestyle/ERP/internal/entity"
)

// PelangganRepository owns every SQL statement touching pelanggan.
type PelangganRepository struct{}

func NewPelangganRepository() *PelangganRepository {
	return &PelangganRepository{}
}

// pelangganColumns is the write-side list, unqualified, for INSERT ... RETURNING.
//
// plafon_kredit is cast to TEXT on the way out so the exact decimal PostgreSQL
// stored is what Go receives. Scanning NUMERIC into a float would reintroduce
// binary rounding into a money column.
const pelangganColumns = `id, kode, nama, telepon, alamat, npwp, plafon_kredit::TEXT,
	is_aktif, created_at, created_by, updated_at, updated_by`

// pelangganReadColumns adds the creator's name, resolved by the join in
// pelangganFrom rather than by one query per row.
const pelangganReadColumns = `p.id, p.kode, p.nama, p.telepon, p.alamat, p.npwp,
	p.plafon_kredit::TEXT, p.is_aktif, p.created_at, p.created_by,
	p.updated_at, p.updated_by, u.nama_lengkap`

// pelangganFrom joins users LEFT, not INNER: pelanggan.created_by is nullable, and
// an inner join would silently drop every customer without a creator, leaving the
// page shorter than total_item claims.
const pelangganFrom = `
	FROM pelanggan p
	LEFT JOIN users u ON u.id = p.created_by`

// pelangganFilter is shared by the COUNT and the row query, so the two can never
// disagree. The filter owns $1..$2; pagination follows after it.
const pelangganFilter = `
	WHERE ($1 = '' OR p.nama ILIKE '%' || $1 || '%' OR p.kode ILIKE '%' || $1 || '%')
	  AND ($2::BOOLEAN IS NULL OR p.is_aktif = $2)`

// PelangganPatch carries a partial update. Every nullable column gets a Set flag
// answering "was this key in the JSON body at all?".
//
// SetPlafonKredit with a nil PlafonKredit is a meaningful request: it lifts the
// credit limit rather than setting it to zero.
type PelangganPatch struct {
	SetKode         bool
	Kode            *string
	Nama            *string // NOT NULL: changeable, never clearable, so no flag
	SetTelepon      bool
	Telepon         *string
	SetAlamat       bool
	Alamat          *string
	SetNPWP         bool
	NPWP            *string
	SetPlafonKredit bool
	PlafonKredit    *string
	IsAktif         *bool
	SetUpdatedBy    bool
	UpdatedBy       *int64
}

func (r *PelangganRepository) Create(ctx context.Context, db DBTX, pelanggan *entity.Pelanggan) error {
	const query = `
		INSERT INTO pelanggan (kode, nama, telepon, alamat, npwp, plafon_kredit, is_aktif, created_by)
		VALUES ($1, $2, $3, $4, $5, $6::NUMERIC, $7, $8)
		RETURNING ` + pelangganColumns

	err := db.QueryRowContext(
		ctx, query,
		pelanggan.Kode, pelanggan.Nama, pelanggan.Telepon, pelanggan.Alamat,
		pelanggan.NPWP, pelanggan.PlafonKredit, pelanggan.IsAktif, pelanggan.CreatedBy,
	).Scan(
		&pelanggan.ID, &pelanggan.Kode, &pelanggan.Nama, &pelanggan.Telepon,
		&pelanggan.Alamat, &pelanggan.NPWP, &pelanggan.PlafonKredit, &pelanggan.IsAktif,
		&pelanggan.CreatedAt, &pelanggan.CreatedBy, &pelanggan.UpdatedAt, &pelanggan.UpdatedBy,
	)
	if err != nil {
		return fmt.Errorf("insert pelanggan: %w", err)
	}

	return nil
}

// FindByID returns sql.ErrNoRows when the row is absent; the usecase maps that
// to a 404.
func (r *PelangganRepository) FindByID(ctx context.Context, db DBTX, id int64) (*entity.Pelanggan, error) {
	const query = `SELECT ` + pelangganReadColumns + pelangganFrom + ` WHERE p.id = $1`

	pelanggan := new(entity.Pelanggan)

	err := db.QueryRowContext(ctx, query, id).Scan(
		&pelanggan.ID, &pelanggan.Kode, &pelanggan.Nama, &pelanggan.Telepon,
		&pelanggan.Alamat, &pelanggan.NPWP, &pelanggan.PlafonKredit, &pelanggan.IsAktif,
		&pelanggan.CreatedAt, &pelanggan.CreatedBy, &pelanggan.UpdatedAt, &pelanggan.UpdatedBy,
		&pelanggan.NamaPembuat,
	)
	if err != nil {
		return nil, err
	}

	return pelanggan, nil
}

// ExistsByKode matches case-insensitively to mirror pelanggan_kode_lower_uidx.
// exceptID skips one row so an update does not collide with itself; pass 0 when
// creating.
//
// Only call this when a kode was actually supplied: kode is nullable, and a NULL
// never equals a NULL, so the answer would always be false and mean nothing.
func (r *PelangganRepository) ExistsByKode(ctx context.Context, db DBTX, kode string, exceptID int64) (bool, error) {
	const query = `
		SELECT EXISTS (
			SELECT 1 FROM pelanggan
			WHERE lower(kode) = lower($1)
			  AND ($2 = 0 OR id <> $2)
		)`

	var exists bool
	if err := db.QueryRowContext(ctx, query, kode, exceptID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check pelanggan kode: %w", err)
	}

	return exists, nil
}

// Update applies a patch and returns the stored row. RETURNING saves a second
// SELECT and avoids reading back another transaction's write; sql.ErrNoRows means
// the id does not exist.
func (r *PelangganRepository) Update(ctx context.Context, db DBTX, id int64, patch PelangganPatch) (*entity.Pelanggan, error) {
	// COALESCE is right for NOT NULL columns only. On a nullable column it turns
	// an explicit null into a no-op, which for plafon_kredit would mean the credit
	// limit can never be lifted again. updated_at is left to the trigger.
	const query = `
		UPDATE pelanggan SET
			kode          = CASE WHEN $2::BOOLEAN  THEN $3           ELSE kode END,
			nama          = COALESCE($4, nama),
			telepon       = CASE WHEN $5::BOOLEAN  THEN $6           ELSE telepon END,
			alamat        = CASE WHEN $7::BOOLEAN  THEN $8           ELSE alamat END,
			npwp          = CASE WHEN $9::BOOLEAN  THEN $10          ELSE npwp END,
			plafon_kredit = CASE WHEN $11::BOOLEAN THEN $12::NUMERIC ELSE plafon_kredit END,
			is_aktif      = COALESCE($13, is_aktif),
			updated_by    = CASE WHEN $14::BOOLEAN THEN $15          ELSE updated_by END
		WHERE id = $1
		RETURNING ` + pelangganColumns

	pelanggan := new(entity.Pelanggan)

	err := db.QueryRowContext(
		ctx, query, id,
		patch.SetKode, patch.Kode,
		patch.Nama,
		patch.SetTelepon, patch.Telepon,
		patch.SetAlamat, patch.Alamat,
		patch.SetNPWP, patch.NPWP,
		patch.SetPlafonKredit, patch.PlafonKredit,
		patch.IsAktif,
		patch.SetUpdatedBy, patch.UpdatedBy,
	).Scan(
		&pelanggan.ID, &pelanggan.Kode, &pelanggan.Nama, &pelanggan.Telepon,
		&pelanggan.Alamat, &pelanggan.NPWP, &pelanggan.PlafonKredit, &pelanggan.IsAktif,
		&pelanggan.CreatedAt, &pelanggan.CreatedBy, &pelanggan.UpdatedAt, &pelanggan.UpdatedBy,
	)
	if err != nil {
		return nil, err
	}

	return pelanggan, nil
}

// Search returns one page of rows plus the total matching count. A nil isAktif
// means "no filter". Exactly two queries, whatever the row count.
func (r *PelangganRepository) Search(ctx context.Context, db DBTX, search string, isAktif *bool, limit, offset int) ([]entity.Pelanggan, int64, error) {
	search = EscapeLike(search)

	// COUNT skips the join: nothing in the filter needs users.
	var total int64
	if err := db.QueryRowContext(
		ctx, `SELECT COUNT(*) FROM pelanggan p`+pelangganFilter, search, isAktif,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count pelanggan: %w", err)
	}

	if total == 0 {
		return []entity.Pelanggan{}, 0, nil
	}

	// ORDER BY ends in a unique column so tied names cannot shuffle between pages.
	query := `SELECT ` + pelangganReadColumns + pelangganFrom + pelangganFilter + `
		ORDER BY p.nama, p.id
		LIMIT $3 OFFSET $4`

	rows, err := db.QueryContext(ctx, query, search, isAktif, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("select pelanggan: %w", err)
	}
	defer rows.Close()

	list := make([]entity.Pelanggan, 0, limit)

	for rows.Next() {
		var pelanggan entity.Pelanggan

		if err := rows.Scan(
			&pelanggan.ID, &pelanggan.Kode, &pelanggan.Nama, &pelanggan.Telepon,
			&pelanggan.Alamat, &pelanggan.NPWP, &pelanggan.PlafonKredit, &pelanggan.IsAktif,
			&pelanggan.CreatedAt, &pelanggan.CreatedBy, &pelanggan.UpdatedAt, &pelanggan.UpdatedBy,
			&pelanggan.NamaPembuat,
		); err != nil {
			return nil, 0, fmt.Errorf("scan pelanggan: %w", err)
		}

		list = append(list, pelanggan)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate pelanggan: %w", err)
	}

	return list, total, nil
}
