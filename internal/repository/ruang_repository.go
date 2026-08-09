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

func (r *RuangRepository) Create(ctx context.Context, db DBTX, ruang *entity.Ruang) error {
	const query = `
		INSERT INTO ruang (kode, nama_ruang, is_aktif)
		VALUES ($1, $2, $3)
		RETURNING id`

	err := db.QueryRowContext(ctx, query, ruang.Kode, ruang.NamaRuang, ruang.IsAktif).Scan(&ruang.ID)
	if err != nil {
		return fmt.Errorf("insert ruang: %w", err)
	}

	return nil
}

// FindByID returns sql.ErrNoRows when the row is absent; the usecase maps that
// to a 404.
func (r *RuangRepository) FindByID(ctx context.Context, db DBTX, id int64) (*entity.Ruang, error) {
	const query = `
		SELECT id, kode, nama_ruang, is_aktif
		FROM ruang
		WHERE id = $1`

	ruang := new(entity.Ruang)

	err := db.QueryRowContext(ctx, query, id).Scan(
		&ruang.ID, &ruang.Kode, &ruang.NamaRuang, &ruang.IsAktif,
	)
	if err != nil {
		return nil, err
	}

	return ruang, nil
}

func (r *RuangRepository) ExistsByKode(ctx context.Context, db DBTX, kode string) (bool, error) {
	const query = `SELECT EXISTS (SELECT 1 FROM ruang WHERE kode = $1)`

	var exists bool
	if err := db.QueryRowContext(ctx, query, kode).Scan(&exists); err != nil {
		return false, fmt.Errorf("check ruang kode: %w", err)
	}

	return exists, nil
}

// Search returns one page of rows plus the total matching count. A nil isAktif
// means "no filter".
func (r *RuangRepository) Search(ctx context.Context, db DBTX, search string, isAktif *bool, limit, offset int) ([]entity.Ruang, int64, error) {
	const filter = `
		WHERE ($1 = '' OR nama_ruang ILIKE '%' || $1 || '%' OR kode ILIKE '%' || $1 || '%')
		  AND ($2::BOOLEAN IS NULL OR is_aktif = $2)`

	var total int64
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ruang`+filter, search, isAktif).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count ruang: %w", err)
	}

	query := `SELECT id, kode, nama_ruang, is_aktif FROM ruang` + filter + `
		ORDER BY nama_ruang
		LIMIT $3 OFFSET $4`

	rows, err := db.QueryContext(ctx, query, search, isAktif, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("select ruang: %w", err)
	}
	defer rows.Close()

	list := make([]entity.Ruang, 0, limit)

	for rows.Next() {
		var ruang entity.Ruang

		if err := rows.Scan(&ruang.ID, &ruang.Kode, &ruang.NamaRuang, &ruang.IsAktif); err != nil {
			return nil, 0, fmt.Errorf("scan ruang: %w", err)
		}

		list = append(list, ruang)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate ruang: %w", err)
	}

	return list, total, nil
}
