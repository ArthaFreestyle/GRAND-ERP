package repository

import (
	"context"
	"fmt"

	"Arthafreestyle/ERP/internal/entity"
)

// SupplierRepository owns every SQL statement touching supplier.
type SupplierRepository struct{}

func NewSupplierRepository() *SupplierRepository {
	return &SupplierRepository{}
}

// supplierColumns is the write-side list, unqualified, for INSERT ... RETURNING.
const supplierColumns = `id, kode, nama, telepon, alamat, npwp, is_aktif,
	created_at, created_by, updated_at, updated_by`

// supplierReadColumns adds the creator's name, resolved by the join in
// supplierFrom. Fetching it per row instead would be an N+1: one query for the
// page plus one per supplier.
const supplierReadColumns = `s.id, s.kode, s.nama, s.telepon, s.alamat, s.npwp, s.is_aktif,
	s.created_at, s.created_by, s.updated_at, s.updated_by, u.nama_lengkap`

// supplierFrom joins users LEFT, not INNER: supplier.created_by is nullable, and
// an inner join would silently drop every supplier without a creator, leaving the
// page shorter than total_item claims.
const supplierFrom = `
	FROM supplier s
	LEFT JOIN users u ON u.id = s.created_by`

// supplierFilter is shared by the COUNT and the row query. Two copies of a filter
// eventually diverge and total_item starts lying about the data.
//
// Placeholder discipline: the filter owns $1..$2 and pagination follows after it.
// Inserting a new filter means shifting LIMIT/OFFSET too.
const supplierFilter = `
	WHERE ($1 = '' OR s.nama ILIKE '%' || $1 || '%' OR s.kode ILIKE '%' || $1 || '%')
	  AND ($2::BOOLEAN IS NULL OR s.is_aktif = $2)`

// SupplierPatch carries a partial update. Every nullable column gets a Set flag
// answering "was this key in the JSON body at all?" — without it, clearing a
// column is indistinguishable from leaving it alone.
type SupplierPatch struct {
	SetKode      bool
	Kode         *string
	Nama         *string // NOT NULL: changeable, never clearable, so no flag
	SetTelepon   bool
	Telepon      *string
	SetAlamat    bool
	Alamat       *string
	SetNPWP      bool
	NPWP         *string
	IsAktif      *bool
	SetUpdatedBy bool
	UpdatedBy    *int64
}

func (r *SupplierRepository) Create(ctx context.Context, db DBTX, supplier *entity.Supplier) error {
	const query = `
		INSERT INTO supplier (kode, nama, telepon, alamat, npwp, is_aktif, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING ` + supplierColumns

	err := db.QueryRowContext(
		ctx, query,
		supplier.Kode, supplier.Nama, supplier.Telepon, supplier.Alamat,
		supplier.NPWP, supplier.IsAktif, supplier.CreatedBy,
	).Scan(
		&supplier.ID, &supplier.Kode, &supplier.Nama, &supplier.Telepon,
		&supplier.Alamat, &supplier.NPWP, &supplier.IsAktif,
		&supplier.CreatedAt, &supplier.CreatedBy, &supplier.UpdatedAt, &supplier.UpdatedBy,
	)
	if err != nil {
		return fmt.Errorf("insert supplier: %w", err)
	}

	return nil
}

// FindByID returns sql.ErrNoRows when the row is absent; the usecase maps that
// to a 404.
func (r *SupplierRepository) FindByID(ctx context.Context, db DBTX, id int64) (*entity.Supplier, error) {
	const query = `SELECT ` + supplierReadColumns + supplierFrom + ` WHERE s.id = $1`

	supplier := new(entity.Supplier)

	err := db.QueryRowContext(ctx, query, id).Scan(
		&supplier.ID, &supplier.Kode, &supplier.Nama, &supplier.Telepon,
		&supplier.Alamat, &supplier.NPWP, &supplier.IsAktif,
		&supplier.CreatedAt, &supplier.CreatedBy, &supplier.UpdatedAt, &supplier.UpdatedBy,
		&supplier.NamaPembuat,
	)
	if err != nil {
		return nil, err
	}

	return supplier, nil
}

// ExistsByKode matches case-insensitively to mirror supplier_kode_lower_uidx.
// exceptID skips one row so an update does not collide with itself; pass 0 when
// creating.
//
// Only call this when a kode was actually supplied: kode is nullable, and
// lower(NULL) = lower(NULL) is never true, so a NULL check always answers false
// and tells you nothing.
func (r *SupplierRepository) ExistsByKode(ctx context.Context, db DBTX, kode string, exceptID int64) (bool, error) {
	const query = `
		SELECT EXISTS (
			SELECT 1 FROM supplier
			WHERE lower(kode) = lower($1)
			  AND ($2 = 0 OR id <> $2)
		)`

	var exists bool
	if err := db.QueryRowContext(ctx, query, kode, exceptID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check supplier kode: %w", err)
	}

	return exists, nil
}

// Update applies a patch and returns the stored row. RETURNING saves a second
// SELECT and avoids reading back another transaction's write; sql.ErrNoRows means
// the id does not exist, so there is no separate existence check — that would be
// two queries and still racy.
func (r *SupplierRepository) Update(ctx context.Context, db DBTX, id int64, patch SupplierPatch) (*entity.Supplier, error) {
	// COALESCE is right for NOT NULL columns only. On a nullable column it turns
	// `"telepon": null` into a no-op, so presence is passed as its own argument.
	// updated_at is left to the supplier_set_updated_at trigger.
	const query = `
		UPDATE supplier SET
			kode       = CASE WHEN $2::BOOLEAN THEN $3 ELSE kode END,
			nama       = COALESCE($4, nama),
			telepon    = CASE WHEN $5::BOOLEAN THEN $6 ELSE telepon END,
			alamat     = CASE WHEN $7::BOOLEAN THEN $8 ELSE alamat END,
			npwp       = CASE WHEN $9::BOOLEAN THEN $10 ELSE npwp END,
			is_aktif   = COALESCE($11, is_aktif),
			updated_by = CASE WHEN $12::BOOLEAN THEN $13 ELSE updated_by END
		WHERE id = $1
		RETURNING ` + supplierColumns

	supplier := new(entity.Supplier)

	err := db.QueryRowContext(
		ctx, query, id,
		patch.SetKode, patch.Kode,
		patch.Nama,
		patch.SetTelepon, patch.Telepon,
		patch.SetAlamat, patch.Alamat,
		patch.SetNPWP, patch.NPWP,
		patch.IsAktif,
		patch.SetUpdatedBy, patch.UpdatedBy,
	).Scan(
		&supplier.ID, &supplier.Kode, &supplier.Nama, &supplier.Telepon,
		&supplier.Alamat, &supplier.NPWP, &supplier.IsAktif,
		&supplier.CreatedAt, &supplier.CreatedBy, &supplier.UpdatedAt, &supplier.UpdatedBy,
	)
	if err != nil {
		return nil, err
	}

	return supplier, nil
}

// Search returns one page of rows plus the total matching count. A nil isAktif
// means "no filter". Exactly two queries, whatever the row count — the creator's
// name rides along on the join.
func (r *SupplierRepository) Search(ctx context.Context, db DBTX, search string, isAktif *bool, limit, offset int) ([]entity.Supplier, int64, error) {
	search = EscapeLike(search)

	// COUNT skips the join: nothing in the filter needs users.
	var total int64
	if err := db.QueryRowContext(
		ctx, `SELECT COUNT(*) FROM supplier s`+supplierFilter, search, isAktif,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count supplier: %w", err)
	}

	if total == 0 {
		return []entity.Supplier{}, 0, nil
	}

	// ORDER BY ends in a unique column: nama alone leaves ties in an unspecified
	// order, which lets one row appear on two pages while another is never
	// returned at all.
	query := `SELECT ` + supplierReadColumns + supplierFrom + supplierFilter + `
		ORDER BY s.nama, s.id
		LIMIT $3 OFFSET $4`

	rows, err := db.QueryContext(ctx, query, search, isAktif, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("select supplier: %w", err)
	}
	defer rows.Close()

	list := make([]entity.Supplier, 0, limit)

	for rows.Next() {
		var supplier entity.Supplier

		if err := rows.Scan(
			&supplier.ID, &supplier.Kode, &supplier.Nama, &supplier.Telepon,
			&supplier.Alamat, &supplier.NPWP, &supplier.IsAktif,
			&supplier.CreatedAt, &supplier.CreatedBy, &supplier.UpdatedAt, &supplier.UpdatedBy,
			&supplier.NamaPembuat,
		); err != nil {
			return nil, 0, fmt.Errorf("scan supplier: %w", err)
		}

		list = append(list, supplier)
	}

	// Without this, an error partway through iteration passes silently and the
	// caller gets a truncated page.
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate supplier: %w", err)
	}

	return list, total, nil
}
