package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"Arthafreestyle/ERP/internal/entity"
)

// DokumenRepository owns every SQL statement touching dokumen.
type DokumenRepository struct{}

func NewDokumenRepository() *DokumenRepository {
	return &DokumenRepository{}
}

// RefTableDokumen is the whitelist of documents an attachment may point at.
//
// A polymorphic reference has no foreign key behind it, so this map is the only
// thing standing between ref_table and an arbitrary string from a request body. It
// is a whitelist rather than a validation rule because the value is also
// interpolated into SQL by StatusRef — only a key of this map ever reaches a query,
// which is what makes that interpolation safe.
//
// The value is the column holding the document's status. Every entry has one today;
// a future document type without a status would need its own answer for "may this
// attachment still be removed", which is why the column is named here rather than
// assumed.
//
// Add an entry when a module starts accepting attachments. No migration is needed —
// deliberately: the schema does not carry this list, so a new module costs one line
// here and nothing else.
var RefTableDokumen = map[string]string{
	"pembelian":          "status",
	"penerimaan_susulan": "status",
	"retur_pembelian":    "status",
	"pembayaran_utang":   "status",
}

// ErrRefTableTidakDikenal reports a ref_table outside RefTableDokumen.
var ErrRefTableTidakDikenal = errors.New("ref_table tidak dikenal")

// lockPembersihanDokumen keys the advisory lock the cleanup job takes.
//
// One arbitrary but fixed number, because pg_advisory_lock's namespace is the whole
// database and any other job wanting a lock must pick a different one. The value has
// no meaning beyond being unique here.
const lockPembersihanDokumen int64 = 8_051_016

// dokumenColumns is the write-side list, unqualified, for INSERT ... RETURNING.
const dokumenColumns = `id, nama_asli, path_simpan, mime, ukuran_byte, checksum_sha256,
	ref_table, ref_id, created_by, created_at, deleted_at`

// dokumenReadColumns adds the uploader's name, resolved by the join in dokumenFrom.
// Fetching it per row would be an N+1: one query for the page plus one per file.
const dokumenReadColumns = `d.id, d.nama_asli, d.path_simpan, d.mime, d.ukuran_byte,
	d.checksum_sha256, d.ref_table, d.ref_id, d.created_by, d.created_at, d.deleted_at,
	u.nama_lengkap`

// dokumenFrom joins users INNER, unlike supplier's read: dokumen.created_by is
// NOT NULL and references users, so there is always a row to join to and an inner
// join cannot shorten the page.
const dokumenFrom = `
	FROM dokumen d
	JOIN users u ON u.id = d.created_by`

// dokumenFilter is shared by the COUNT and the row query — two copies of a filter
// eventually diverge and total_item starts lying about the data.
//
// It covers both modes of the list endpoint in one expression, which is why the
// two clauses are mutually exclusive on $1: with a ref_table it lists that
// document's attachments, without one it lists the caller's own orphans. Splitting
// them into two constants would mean two COUNTs and two row queries to keep in step.
//
// Placeholder discipline: the filter owns $1..$3 and pagination follows after it.
const dokumenFilter = `
	WHERE d.deleted_at IS NULL
	  AND ($1 = '' OR (d.ref_table = $1 AND d.ref_id = $2))
	  AND ($1 <> '' OR (d.ref_id IS NULL AND d.created_by = $3))`

func scanDokumen(row interface{ Scan(...any) error }, dokumen *entity.Dokumen) error {
	return row.Scan(
		&dokumen.ID, &dokumen.NamaAsli, &dokumen.PathSimpan, &dokumen.Mime,
		&dokumen.UkuranByte, &dokumen.ChecksumSHA256, &dokumen.RefTable, &dokumen.RefID,
		&dokumen.CreatedBy, &dokumen.CreatedAt, &dokumen.DeletedAt,
	)
}

// Create inserts one attachment row. It runs after the bytes are already on disk:
// a row pointing at a file that is not there cannot be repaired, while a file with
// no row is caught by the caller and removed.
func (r *DokumenRepository) Create(ctx context.Context, db DBTX, dokumen *entity.Dokumen) error {
	const query = `
		INSERT INTO dokumen (nama_asli, path_simpan, mime, ukuran_byte, checksum_sha256, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING ` + dokumenColumns

	// ref_table and ref_id are absent on purpose: an upload is always born an orphan.
	// Attaching is a separate step, because the document it belongs to usually does
	// not exist yet when the photo is taken.
	err := scanDokumen(db.QueryRowContext(
		ctx, query,
		dokumen.NamaAsli, dokumen.PathSimpan, dokumen.Mime,
		dokumen.UkuranByte, dokumen.ChecksumSHA256, dokumen.CreatedBy,
	), dokumen)
	if err != nil {
		return fmt.Errorf("insert dokumen: %w", err)
	}

	return nil
}

// FindByID returns the row including soft-deleted ones — the caller decides what a
// deleted attachment means for what it is doing. sql.ErrNoRows when the id is
// absent.
func (r *DokumenRepository) FindByID(ctx context.Context, db DBTX, id int64) (*entity.Dokumen, error) {
	const query = `SELECT ` + dokumenReadColumns + dokumenFrom + ` WHERE d.id = $1`

	dokumen := new(entity.Dokumen)

	err := db.QueryRowContext(ctx, query, id).Scan(
		&dokumen.ID, &dokumen.NamaAsli, &dokumen.PathSimpan, &dokumen.Mime,
		&dokumen.UkuranByte, &dokumen.ChecksumSHA256, &dokumen.RefTable, &dokumen.RefID,
		&dokumen.CreatedBy, &dokumen.CreatedAt, &dokumen.DeletedAt, &dokumen.NamaPembuat,
	)
	if err != nil {
		return nil, err
	}

	return dokumen, nil
}

// LockByID takes a row lock before an attachment is attached or deleted.
//
// Same reason every pembelian transition takes one: without it, two concurrent
// requests both read ref_id IS NULL and both attach — to different documents, one
// of them silently losing. The guarded UPDATE below is the backstop; the lock is
// what makes the friendlier error the usual outcome.
func (r *DokumenRepository) LockByID(ctx context.Context, db DBTX, id int64) (*entity.Dokumen, error) {
	const query = `SELECT ` + dokumenColumns + ` FROM dokumen WHERE id = $1 FOR UPDATE`

	dokumen := new(entity.Dokumen)

	if err := scanDokumen(db.QueryRowContext(ctx, query, id), dokumen); err != nil {
		return nil, err
	}

	return dokumen, nil
}

// Tempel fills in the reference, repeating the orphan condition in the WHERE.
//
// ErrTransisiStatus means the row was attached or deleted between the read and this
// write — nothing failed, the guard did its job, and the caller can re-read.
func (r *DokumenRepository) Tempel(ctx context.Context, db DBTX, id int64, refTable string, refID int64) (*entity.Dokumen, error) {
	const query = `
		UPDATE dokumen
		SET ref_table = $2, ref_id = $3
		WHERE id = $1 AND ref_id IS NULL AND deleted_at IS NULL
		RETURNING ` + dokumenColumns

	dokumen := new(entity.Dokumen)

	err := scanDokumen(db.QueryRowContext(ctx, query, id, refTable, refID), dokumen)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTransisiStatus
	}
	if err != nil {
		return nil, fmt.Errorf("tempel dokumen: %w", err)
	}

	return dokumen, nil
}

// SoftDelete marks the row deleted and reports whether it was this call that did it.
//
// The row survives on purpose: it is the trace that an upload happened at all, and
// the reason the cleanup job can be re-run without wondering what it already
// handled. The file itself is gone by the time this is called.
//
// It returns the timestamp it wrote, so the caller can answer with a row that says
// it is deleted rather than with the snapshot it read a moment earlier.
func (r *DokumenRepository) SoftDelete(ctx context.Context, db DBTX, id int64) (time.Time, error) {
	const query = `
		UPDATE dokumen SET deleted_at = now()
		WHERE id = $1 AND deleted_at IS NULL
		RETURNING deleted_at`

	var deletedAt time.Time
	if err := db.QueryRowContext(ctx, query, id).Scan(&deletedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return time.Time{}, ErrTransisiStatus
		}

		return time.Time{}, fmt.Errorf("soft delete dokumen: %w", err)
	}

	return deletedAt, nil
}

// CountLampiran counts the live attachments already on one document, which is what
// the per-document maximum is checked against.
func (r *DokumenRepository) CountLampiran(ctx context.Context, db DBTX, refTable string, refID int64) (int64, error) {
	const query = `
		SELECT COUNT(*) FROM dokumen
		WHERE ref_table = $1 AND ref_id = $2 AND deleted_at IS NULL`

	var total int64
	if err := db.QueryRowContext(ctx, query, refTable, refID).Scan(&total); err != nil {
		return 0, fmt.Errorf("count lampiran: %w", err)
	}

	return total, nil
}

// FindDuplikat reports the newest live attachment carrying the same checksum, or
// sql.ErrNoRows when there is none.
//
// Advisory only. Two identical files are not an error — one scan legitimately
// belongs to two documents — but the same invoice photographed twice at the
// receiving desk is a mistake worth showing before it becomes two payables.
func (r *DokumenRepository) FindDuplikat(ctx context.Context, db DBTX, checksum string, exceptID int64) (int64, error) {
	const query = `
		SELECT id FROM dokumen
		WHERE checksum_sha256 = $1 AND deleted_at IS NULL AND id <> $2
		ORDER BY id DESC
		LIMIT 1`

	var id int64
	if err := db.QueryRowContext(ctx, query, checksum, exceptID).Scan(&id); err != nil {
		return 0, err
	}

	return id, nil
}

// Search returns one page plus the total matching count.
//
// A refTable of "" lists actorID's own orphans instead of a document's attachments;
// see dokumenFilter.
func (r *DokumenRepository) Search(ctx context.Context, db DBTX, refTable string, refID, actorID int64, limit, offset int) ([]entity.Dokumen, int64, error) {
	var total int64
	if err := db.QueryRowContext(
		ctx, `SELECT COUNT(*) FROM dokumen d`+dokumenFilter, refTable, refID, actorID,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count dokumen: %w", err)
	}

	if total == 0 {
		return []entity.Dokumen{}, 0, nil
	}

	// Newest first: an attachment list is read as "what was just uploaded". The
	// ORDER BY ends in a unique column, without which one row could appear on two
	// pages while another never comes back at all.
	query := `SELECT ` + dokumenReadColumns + dokumenFrom + dokumenFilter + `
		ORDER BY d.created_at DESC, d.id DESC
		LIMIT $4 OFFSET $5`

	rows, err := db.QueryContext(ctx, query, refTable, refID, actorID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("select dokumen: %w", err)
	}
	defer rows.Close()

	list := make([]entity.Dokumen, 0, limit)

	for rows.Next() {
		var dokumen entity.Dokumen

		if err := rows.Scan(
			&dokumen.ID, &dokumen.NamaAsli, &dokumen.PathSimpan, &dokumen.Mime,
			&dokumen.UkuranByte, &dokumen.ChecksumSHA256, &dokumen.RefTable, &dokumen.RefID,
			&dokumen.CreatedBy, &dokumen.CreatedAt, &dokumen.DeletedAt, &dokumen.NamaPembuat,
		); err != nil {
			return nil, 0, fmt.Errorf("scan dokumen: %w", err)
		}

		list = append(list, dokumen)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate dokumen: %w", err)
	}

	return list, total, nil
}

// FindYatim lists attachments uploaded before sebelum that were never attached.
//
// This is what the cleanup job works from, and it is the whole reason ref_id is
// nullable: an orphan is one column being NULL, over a partial index that stays
// small because it only ever holds files nobody has attached yet.
//
// Batched with a limit rather than swept in one pass. Each file is a separate
// unlink, and a job that holds one lock across ten thousand of them is a job that
// gets killed halfway with nothing to show for it.
func (r *DokumenRepository) FindYatim(ctx context.Context, db DBTX, sebelum time.Time, limit int) ([]entity.Dokumen, error) {
	const query = `
		SELECT ` + dokumenColumns + ` FROM dokumen
		WHERE ref_id IS NULL AND deleted_at IS NULL AND created_at < $1
		ORDER BY created_at, id
		LIMIT $2`

	rows, err := db.QueryContext(ctx, query, sebelum, limit)
	if err != nil {
		return nil, fmt.Errorf("select dokumen yatim: %w", err)
	}
	defer rows.Close()

	list := make([]entity.Dokumen, 0, limit)

	for rows.Next() {
		var dokumen entity.Dokumen

		if err := scanDokumen(rows, &dokumen); err != nil {
			return nil, fmt.Errorf("scan dokumen yatim: %w", err)
		}

		list = append(list, dokumen)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dokumen yatim: %w", err)
	}

	return list, nil
}

// StatusRef reads the status of the document an attachment points at, and doubles
// as the existence check — sql.ErrNoRows means there is no such document.
//
// refTable is interpolated into the SQL, which is only safe because it is looked up
// in RefTableDokumen first: what reaches the query is a key of that map, never the
// caller's string. A placeholder cannot stand in for an identifier, so the
// alternative would be one method per table.
func (r *DokumenRepository) StatusRef(ctx context.Context, db DBTX, refTable string, refID int64) (string, error) {
	kolom, ok := RefTableDokumen[refTable]
	if !ok {
		return "", ErrRefTableTidakDikenal
	}

	query := `SELECT ` + kolom + ` FROM ` + refTable + ` WHERE id = $1`

	var status string
	if err := db.QueryRowContext(ctx, query, refID).Scan(&status); err != nil {
		return "", err
	}

	return status, nil
}

// TryLockPembersihan takes the cleanup job's advisory lock and reports whether it
// got it. A false is not a failure: another worker is already sweeping, and this one
// has nothing to do.
//
// Session-level rather than transaction-level (unlike the kartu_stok trigger's
// pg_advisory_xact_lock), because the job is not one transaction: it deletes files
// between statements, and holding a transaction open across that many unlink calls
// would pin a snapshot for no reason. It must therefore be released explicitly, and
// db has to be a single connection — a *sql.Conn — or the unlock can land on a
// different pooled connection than the lock did and quietly do nothing.
func (r *DokumenRepository) TryLockPembersihan(ctx context.Context, db DBTX) (bool, error) {
	var locked bool
	if err := db.QueryRowContext(
		ctx, `SELECT pg_try_advisory_lock($1)`, lockPembersihanDokumen,
	).Scan(&locked); err != nil {
		return false, fmt.Errorf("lock pembersihan dokumen: %w", err)
	}

	return locked, nil
}

// UnlockPembersihan releases the lock TryLockPembersihan took. Must run on the same
// connection.
func (r *DokumenRepository) UnlockPembersihan(ctx context.Context, db DBTX) error {
	if _, err := db.ExecContext(
		ctx, `SELECT pg_advisory_unlock($1)`, lockPembersihanDokumen,
	); err != nil {
		return fmt.Errorf("unlock pembersihan dokumen: %w", err)
	}

	return nil
}
