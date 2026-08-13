package repository

import (
	"context"
	"fmt"
	"time"

	"Arthafreestyle/ERP/internal/entity"
)

// PeriodeRepository owns every statement touching periode.
//
// There is no Delete, like almost everywhere else in this project: a month closed by
// mistake is reopened, not erased. Erasing the row would also erase the only record
// that it was ever closed.
type PeriodeRepository struct{}

func NewPeriodeRepository() *PeriodeRepository {
	return &PeriodeRepository{}
}

// periodeColumns is the write-side list, unqualified, for INSERT/UPDATE ... RETURNING.
const periodeColumns = `id, tahun, bulan, status, ditutup_oleh, ts_tutup, dibuka_oleh, ts_buka`

// periodeReadColumns adds the two actors' names, resolved by the joins in
// periodeFrom. Fetching them per row would be an N+1: one query for the page plus
// two per month.
const periodeReadColumns = `p.id, p.tahun, p.bulan, p.status,
	p.ditutup_oleh, p.ts_tutup, p.dibuka_oleh, p.ts_buka,
	tutup.nama_lengkap, buka.nama_lengkap`

// periodeFrom joins users LEFT twice: both actor columns are nullable, and an inner
// join would drop every period that has not been reopened — which is nearly all of
// them.
const periodeFrom = `
	FROM periode p
	LEFT JOIN users tutup ON tutup.id = p.ditutup_oleh
	LEFT JOIN users buka  ON buka.id = p.dibuka_oleh`

// periodeFilter is shared by the COUNT and the row query. Two copies of a filter
// eventually diverge and total_item starts lying about the data.
//
// Placeholder discipline: the filter owns $1..$2 and pagination follows after it.
const periodeFilter = `
	WHERE ($1 = 0 OR p.tahun = $1)
	  AND ($2 = '' OR p.status = $2)`

// periodeLockKey is the advisory-lock key expression, and the string it hashes has
// to come out identical to the one inside kartu_stok_hitung_saldo (migration
// 000017). Two different keys would lock two different things and neither side would
// ever wait for the other, which is the whole point of taking the lock.
//
// The ::INT before ::TEXT is not decoration. Casting a placeholder straight to TEXT
// makes the server describe it as a text parameter, and the driver would then be
// asked to send a Go int as text; going through INT keeps the parameter an integer
// and produces the same digits the trigger's INT variables do.
const periodeLockKey = `hashtextextended('periode:' || $1::INT::TEXT || '-' || $2::INT::TEXT, 0)`

// Lock takes the exclusive advisory lock for one month, and it must be called inside
// a transaction: the lock is held until that transaction commits or rolls back.
//
// The kartu_stok trigger takes the shared side of this same lock before it reads
// periode.status. So a book closing waits for every posting already in flight for
// that month, and every posting that starts afterwards waits for the closing — which
// closes the READ COMMITTED window where a transaction reads 'BUKA', the closing
// commits, and the posting then lands in a month whose books are shut.
//
// An advisory lock rather than SELECT ... FOR SHARE on the row, because on the first
// closing of a month there is no row yet — closing is what creates it.
func (r *PeriodeRepository) Lock(ctx context.Context, db DBTX, tahun, bulan int) error {
	const query = `SELECT pg_advisory_xact_lock(` + periodeLockKey + `)`

	if _, err := db.ExecContext(ctx, query, tahun, bulan); err != nil {
		return fmt.Errorf("lock periode: %w", err)
	}

	return nil
}

// FindByTahunBulan returns sql.ErrNoRows when the month has never been closed. The
// usecase turns that into a synthetic BUKA rather than a 404 — a month with no row is
// open, not missing.
func (r *PeriodeRepository) FindByTahunBulan(ctx context.Context, db DBTX, tahun, bulan int) (*entity.Periode, error) {
	const query = `SELECT ` + periodeReadColumns + periodeFrom + `
		WHERE p.tahun = $1 AND p.bulan = $2`

	periode := new(entity.Periode)

	err := db.QueryRowContext(ctx, query, tahun, bulan).Scan(
		&periode.ID, &periode.Tahun, &periode.Bulan, &periode.Status,
		&periode.DitutupOleh, &periode.TsTutup, &periode.DibukaOleh, &periode.TsBuka,
		&periode.NamaPenutup, &periode.NamaPembuka,
	)
	if err != nil {
		return nil, err
	}

	return periode, nil
}

// IsTutup answers whether the month containing a given date is closed.
//
// This is what the posting paths call for a better error message, and like
// ExistsByKode against a unique index it is not the guard. The guard is the
// kartu_stok trigger, which decides under the advisory lock. What this buys is an
// error naming one cause — "periode 2026-07 sudah TUTUP" — instead of the trigger's
// message, which cannot distinguish a closed month from insufficient stock because a
// RAISE carries no constraint name.
func (r *PeriodeRepository) IsTutup(ctx context.Context, db DBTX, tanggal time.Time) (bool, error) {
	const query = `
		SELECT EXISTS (
			SELECT 1 FROM periode
			WHERE tahun = $1 AND bulan = $2 AND status = 'TUTUP'
		)`

	var tutup bool
	if err := db.QueryRowContext(
		ctx, query, tanggal.Year(), int(tanggal.Month()),
	).Scan(&tutup); err != nil {
		return false, fmt.Errorf("check periode: %w", err)
	}

	return tutup, nil
}

// Tutup closes a month, creating its row when there is none.
//
// The upsert is the whole shape of this module: a month with no row is open, so
// closing one is an INSERT the first time and an UPDATE only when it has been closed
// and reopened before. ON CONFLICT names the index expression (tahun, bulan) because
// that pair is the identity.
//
// dibuka_oleh and ts_buka are deliberately left alone. They record the last
// reopening, and a closing that erased them would erase the evidence that this month
// was ever reopened — which is the only thing they exist for.
func (r *PeriodeRepository) Tutup(ctx context.Context, db DBTX, tahun, bulan int, actorID int64) (*entity.Periode, error) {
	const query = `
		INSERT INTO periode (tahun, bulan, status, ditutup_oleh, ts_tutup)
		VALUES ($1, $2, 'TUTUP', $3, now())
		ON CONFLICT (tahun, bulan) DO UPDATE SET
			status       = 'TUTUP',
			ditutup_oleh = EXCLUDED.ditutup_oleh,
			ts_tutup     = EXCLUDED.ts_tutup
		RETURNING ` + periodeColumns

	periode := new(entity.Periode)

	err := db.QueryRowContext(ctx, query, tahun, bulan, actorID).Scan(
		&periode.ID, &periode.Tahun, &periode.Bulan, &periode.Status,
		&periode.DitutupOleh, &periode.TsTutup, &periode.DibukaOleh, &periode.TsBuka,
	)
	if err != nil {
		return nil, err
	}

	return periode, nil
}

// Buka reopens a closed month.
//
// UPDATE and not an upsert, and the asymmetry with Tutup is the point: reopening a
// month that has no row would insert a row saying 'BUKA', which is what every month
// without a row already means. The status is repeated in the WHERE so the statement
// matches nothing when another transaction reopened it first; sql.ErrNoRows then
// tells the usecase the state moved, exactly like the document modules' guarded
// transitions.
func (r *PeriodeRepository) Buka(ctx context.Context, db DBTX, tahun, bulan int, actorID int64) (*entity.Periode, error) {
	const query = `
		UPDATE periode SET
			status      = 'BUKA',
			dibuka_oleh = $3,
			ts_buka     = now()
		WHERE tahun = $1 AND bulan = $2 AND status = 'TUTUP'
		RETURNING ` + periodeColumns

	periode := new(entity.Periode)

	err := db.QueryRowContext(ctx, query, tahun, bulan, actorID).Scan(
		&periode.ID, &periode.Tahun, &periode.Bulan, &periode.Status,
		&periode.DitutupOleh, &periode.TsTutup, &periode.DibukaOleh, &periode.TsBuka,
	)
	if err != nil {
		return nil, err
	}

	return periode, nil
}

// Search returns one page of stored rows plus the total matching count. A tahun of 0
// and an empty status mean "no filter".
func (r *PeriodeRepository) Search(ctx context.Context, db DBTX, tahun int, status string, limit, offset int) ([]entity.Periode, int64, error) {
	// COUNT skips the joins: nothing in the filter needs users.
	var total int64
	if err := db.QueryRowContext(
		ctx, `SELECT COUNT(*) FROM periode p`+periodeFilter, tahun, status,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count periode: %w", err)
	}

	if total == 0 {
		return []entity.Periode{}, 0, nil
	}

	// Newest month first, which is the one anyone is about to close or has just
	// closed. (tahun, bulan) is unique, so the ordering is total and no row can
	// appear on two pages.
	query := `SELECT ` + periodeReadColumns + periodeFrom + periodeFilter + `
		ORDER BY p.tahun DESC, p.bulan DESC
		LIMIT $3 OFFSET $4`

	rows, err := db.QueryContext(ctx, query, tahun, status, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("select periode: %w", err)
	}
	defer rows.Close()

	list := make([]entity.Periode, 0, limit)

	for rows.Next() {
		var periode entity.Periode

		if err := rows.Scan(
			&periode.ID, &periode.Tahun, &periode.Bulan, &periode.Status,
			&periode.DitutupOleh, &periode.TsTutup, &periode.DibukaOleh, &periode.TsBuka,
			&periode.NamaPenutup, &periode.NamaPembuka,
		); err != nil {
			return nil, 0, fmt.Errorf("scan periode: %w", err)
		}

		list = append(list, periode)
	}

	// Without this, an error partway through iteration passes silently and the
	// caller gets a truncated page.
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate periode: %w", err)
	}

	return list, total, nil
}
