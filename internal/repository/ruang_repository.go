package repository

import (
	"context"
	"fmt"

	"Arthafreestyle/ERP/internal/entity"
)

// RuangRepository owns every SQL statement touching ruang. Placeholders are
// $1, $2, ... and inserts use RETURNING — the pgx driver has no LastInsertId.
type RuangRepository struct{}

func NewRuangRepository() *RuangRepository {
	return &RuangRepository{}
}

// ruangReadColumns adds the unit's name, resolved by the join in ruangFrom.
// Fetching it per row instead would be an N+1.
const ruangReadColumns = `r.id, r.kode, r.nama_ruang, r.is_aktif, r.id_unit_kerja, uk.nama`

// ruangFrom joins unit_kerja INNER, not LEFT: id_unit_kerja is NOT NULL since
// migration 000019, so every ruang row has one, and an INNER join costs
// nothing here that a LEFT join would avoid.
const ruangFrom = `
	FROM ruang r
	JOIN unit_kerja uk ON uk.id = r.id_unit_kerja`

func (r *RuangRepository) Create(ctx context.Context, db DBTX, ruang *entity.Ruang) error {
	const query = `
		INSERT INTO ruang (kode, nama_ruang, is_aktif, id_unit_kerja)
		VALUES ($1, $2, $3, $4)
		RETURNING id`

	err := db.QueryRowContext(
		ctx, query, ruang.Kode, ruang.NamaRuang, ruang.IsAktif, ruang.IDUnitKerja,
	).Scan(&ruang.ID)
	if err != nil {
		return fmt.Errorf("insert ruang: %w", err)
	}

	return nil
}

// FindByID returns sql.ErrNoRows when the row is absent; the usecase maps that
// to a 404.
func (r *RuangRepository) FindByID(ctx context.Context, db DBTX, id int64) (*entity.Ruang, error) {
	const query = `SELECT ` + ruangReadColumns + ruangFrom + ` WHERE r.id = $1`

	ruang := new(entity.Ruang)

	err := db.QueryRowContext(ctx, query, id).Scan(
		&ruang.ID, &ruang.Kode, &ruang.NamaRuang, &ruang.IsAktif,
		&ruang.IDUnitKerja, &ruang.NamaUnitKerja,
	)
	if err != nil {
		return nil, err
	}

	return ruang, nil
}

// IDUnitKerjaByID returns the unit_kerja a ruang belongs to, without the rest
// of the row. isu #12 fase 5 uses this to check a write's id_ruang against the
// caller's active unit — a plain column read is all that question needs, not
// FindByID's join for a name nobody asked for here. sql.ErrNoRows means the id
// does not exist; the caller decides what that means (usually: let the
// foreign key answer it).
func (r *RuangRepository) IDUnitKerjaByID(ctx context.Context, db DBTX, id int64) (int64, error) {
	const query = `SELECT id_unit_kerja FROM ruang WHERE id = $1`

	var idUnitKerja int64
	if err := db.QueryRowContext(ctx, query, id).Scan(&idUnitKerja); err != nil {
		return 0, err
	}

	return idUnitKerja, nil
}

// ExistsByKode matches case-insensitively, mirroring the ruang_kode_lower_uidx
// index installed in migration 000009 — comparing with = here would report
// "available" for a kode the index then rejects.
func (r *RuangRepository) ExistsByKode(ctx context.Context, db DBTX, kode string) (bool, error) {
	const query = `SELECT EXISTS (SELECT 1 FROM ruang WHERE lower(kode) = lower($1))`

	var exists bool
	if err := db.QueryRowContext(ctx, query, kode).Scan(&exists); err != nil {
		return false, fmt.Errorf("check ruang kode: %w", err)
	}

	return exists, nil
}

// ruangFilter is shared by the COUNT and the row query, same reasoning as
// every other master slice: two copies eventually diverge and total_item
// starts lying about the data.
//
// $3 is the active-unit scope (isu #12 fase 6): NULL means unrestricted (a
// global grant, or no active context), matching the same rule
// periksaRuangUnitAktif applies on the write side.
const ruangFilter = `
	WHERE ($1 = '' OR r.nama_ruang ILIKE '%' || $1 || '%' OR r.kode ILIKE '%' || $1 || '%')
	  AND ($2::BOOLEAN IS NULL OR r.is_aktif = $2)
	  AND ($3::BIGINT IS NULL OR r.id_unit_kerja = $3)`

// Search returns one page of rows plus the total matching count. A nil isAktif
// means "no filter"; a nil aktifIDUnitKerja means "no unit scoping".
func (r *RuangRepository) Search(ctx context.Context, db DBTX, search string, isAktif *bool, aktifIDUnitKerja *int64, limit, offset int) ([]entity.Ruang, int64, error) {
	search = EscapeLike(search)

	var total int64
	if err := db.QueryRowContext(
		ctx, `SELECT COUNT(*) FROM ruang r`+ruangFilter, search, isAktif, aktifIDUnitKerja,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count ruang: %w", err)
	}

	if total == 0 {
		return []entity.Ruang{}, 0, nil
	}

	// nama_ruang is not unique, so it cannot order a page on its own: PostgreSQL
	// is free to return tied rows in a different order per query, which makes a
	// row show up on both page 1 and page 2 while another disappears. Every
	// ORDER BY paired with LIMIT/OFFSET ends in a unique column.
	query := `SELECT ` + ruangReadColumns + ruangFrom + ruangFilter + `
		ORDER BY r.nama_ruang, r.id
		LIMIT $4 OFFSET $5`

	rows, err := db.QueryContext(ctx, query, search, isAktif, aktifIDUnitKerja, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("select ruang: %w", err)
	}
	defer rows.Close()

	list := make([]entity.Ruang, 0, limit)

	for rows.Next() {
		var ruang entity.Ruang

		if err := rows.Scan(
			&ruang.ID, &ruang.Kode, &ruang.NamaRuang, &ruang.IsAktif,
			&ruang.IDUnitKerja, &ruang.NamaUnitKerja,
		); err != nil {
			return nil, 0, fmt.Errorf("scan ruang: %w", err)
		}

		list = append(list, ruang)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate ruang: %w", err)
	}

	return list, total, nil
}
