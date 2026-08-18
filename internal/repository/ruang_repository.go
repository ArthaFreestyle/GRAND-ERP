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

// ruangReadColumns adds the unit's name, resolved by the join in ruangFrom, and
// the nomor of whichever stok_opname is currently freezing the room (isu #15;
// nil when free). Fetching either per row instead would be an N+1.
const ruangReadColumns = `r.id, r.kode, r.nama_ruang, r.is_aktif, r.id_unit_kerja,
	r.created_at, r.created_by, r.updated_at, r.updated_by, uk.nama, so.nomor`

// ruangFrom joins unit_kerja INNER, not LEFT: id_unit_kerja is NOT NULL since
// migration 000019, so every ruang row has one, and an INNER join costs
// nothing here that a LEFT join would avoid.
//
// stok_opname is LEFT, and deliberately not a plain equality on id_ruang: only an
// open opname (DRAFT/DIAJUKAN) freezes the room, and stok_opname_ruang_terbuka_uidx
// already guarantees at most one such row, so the join can never multiply a ruang
// row.
const ruangFrom = `
	FROM ruang r
	JOIN unit_kerja uk ON uk.id = r.id_unit_kerja
	LEFT JOIN stok_opname so ON so.id_ruang = r.id AND so.status IN ('DRAFT', 'DIAJUKAN')`

func (r *RuangRepository) Create(ctx context.Context, db DBTX, ruang *entity.Ruang) error {
	const query = `
		INSERT INTO ruang (kode, nama_ruang, is_aktif, id_unit_kerja, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at, created_by, updated_at, updated_by`

	err := db.QueryRowContext(
		ctx, query, ruang.Kode, ruang.NamaRuang, ruang.IsAktif, ruang.IDUnitKerja, ruang.CreatedBy,
	).Scan(&ruang.ID, &ruang.CreatedAt, &ruang.CreatedBy, &ruang.UpdatedAt, &ruang.UpdatedBy)
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
		&ruang.ID, &ruang.Kode, &ruang.NamaRuang, &ruang.IsAktif, &ruang.IDUnitKerja,
		&ruang.CreatedAt, &ruang.CreatedBy, &ruang.UpdatedAt, &ruang.UpdatedBy,
		&ruang.NamaUnitKerja, &ruang.NomorOpnameBeku,
	)
	if err != nil {
		return nil, err
	}

	return ruang, nil
}

// RuangPatch carries a partial update for PATCH /api/v1/ruang/{id} — isu #23
// fase 2. Every nullable column gets a Set flag answering "was this key in the
// JSON body at all?", the same shape SupplierPatch uses.
//
// IDUnitKerja is deliberately absent: kartu_stok is partitioned by
// (id_barang, id_ruang), and isu #12 fase 6's read scoping reads it straight
// off ruang.id_unit_kerja. Moving a ruang to another unit would carry its whole
// history and balance across with it, with no kartu_stok row ever changing —
// the old unit's reports would lose stock that was really there, and the new
// one would gain stock that never arrived through any document. A ruang that
// truly changed unit is retired and re-created; goods that physically moved
// are a mutasi into a room the destination unit owns.
type RuangPatch struct {
	SetKode      bool
	Kode         *string
	NamaRuang    *string // NOT NULL: changeable, never clearable, so no flag
	IsAktif      *bool   // NOT NULL: changeable, never clearable, so no flag
	SetUpdatedBy bool
	UpdatedBy    *int64
}

// Update applies a patch and returns the stored row. RETURNING saves a second
// SELECT and avoids reading back another transaction's write; sql.ErrNoRows means
// the id does not exist, so there is no separate existence check — that would be
// two queries and still racy.
//
// The RETURNING list carries only ruang's own columns — NamaUnitKerja and
// NomorOpnameBeku need the join FindByID and Search use, and an UPDATE cannot
// join. The usecase re-reads through FindByID when it needs either, the same
// way Create does for NamaUnitKerja.
func (r *RuangRepository) Update(ctx context.Context, db DBTX, id int64, patch RuangPatch) (*entity.Ruang, error) {
	// COALESCE is right for NOT NULL columns only. On the nullable kode column it
	// would turn `"kode": null` into a no-op, so presence is passed as its own
	// argument. updated_at is left to the ruang_set_updated_at trigger.
	const query = `
		UPDATE ruang SET
			kode       = CASE WHEN $2::BOOLEAN THEN $3 ELSE kode END,
			nama_ruang = COALESCE($4, nama_ruang),
			is_aktif   = COALESCE($5, is_aktif),
			updated_by = CASE WHEN $6::BOOLEAN THEN $7 ELSE updated_by END
		WHERE id = $1
		RETURNING id, kode, nama_ruang, is_aktif, id_unit_kerja,
			created_at, created_by, updated_at, updated_by`

	ruang := new(entity.Ruang)

	err := db.QueryRowContext(
		ctx, query, id,
		patch.SetKode, patch.Kode,
		patch.NamaRuang,
		patch.IsAktif,
		patch.SetUpdatedBy, patch.UpdatedBy,
	).Scan(
		&ruang.ID, &ruang.Kode, &ruang.NamaRuang, &ruang.IsAktif, &ruang.IDUnitKerja,
		&ruang.CreatedAt, &ruang.CreatedBy, &ruang.UpdatedAt, &ruang.UpdatedBy,
	)
	if err != nil {
		return nil, err
	}

	return ruang, nil
}

// ruangLockKey is the advisory-lock key expression for one ruang, and the string
// it hashes has to come out identical to the one kartu_stok_hitung_saldo uses
// (migration 000023) — isu #15. Two different keys would lock two different
// things and neither side would ever wait for the other.
const ruangLockKey = `hashtextextended('ruang:' || $1::BIGINT::TEXT, 0)`

// Lock takes the exclusive advisory lock for one ruang, and it must be called
// inside a transaction: the lock is held until that transaction commits or rolls
// back.
//
// The kartu_stok trigger takes the shared side of this same lock before it reads
// stok_opname for that room. So opening or closing an opname (which is what
// changes whether the room is frozen) waits for every posting already in flight
// for that room, and every posting that starts afterwards waits for it — closing
// the READ COMMITTED window where a posting reads "not frozen", an opname opens,
// and the posting then lands after the cutoff with nothing noticing.
//
// Called by StokOpnameUseCase.Buka and by every transition that changes whether a
// room is frozen (Posting, Batal) — not Ajukan or Tolak, since a room stays frozen
// across both of those.
func (r *RuangRepository) Lock(ctx context.Context, db DBTX, idRuang int64) error {
	const query = `SELECT pg_advisory_xact_lock(` + ruangLockKey + `)`

	if _, err := db.ExecContext(ctx, query, idRuang); err != nil {
		return fmt.Errorf("lock ruang: %w", err)
	}

	return nil
}

// LockShared takes the shared side of the same advisory lock, which is what the
// kartu_stok trigger takes before it reads stok_opname for that room. It must be
// called inside a transaction.
//
// Nothing needs this to refuse a frozen room — the trigger still does that. What
// it is for is lock ordering, the same reasoning as PeriodeRepository.LockShared:
// every writer follows periode -> ruang -> (id_barang, id_ruang), because the
// trigger takes them in that order on every insert. A document that pre-locks its
// balances (KartuStokRepository.KunciSaldo) has to take this before that, or a
// closing/opening opname queued for the exclusive ruang lock is enough to close a
// deadlock cycle the same way an unordered periode lock would.
//
// Called by mutasi, pemakaian, and stok_opname's own posting — every module that
// pre-locks balances before writing kartu_stok — right before KunciSaldo, so the
// frozen status periksaRuangBeku reads afterwards cannot move underneath it.
func (r *RuangRepository) LockShared(ctx context.Context, db DBTX, idRuang int64) error {
	const query = `SELECT pg_advisory_xact_lock_shared(` + ruangLockKey + `)`

	if _, err := db.ExecContext(ctx, query, idRuang); err != nil {
		return fmt.Errorf("lock shared ruang: %w", err)
	}

	return nil
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
			&ruang.ID, &ruang.Kode, &ruang.NamaRuang, &ruang.IsAktif, &ruang.IDUnitKerja,
			&ruang.CreatedAt, &ruang.CreatedBy, &ruang.UpdatedAt, &ruang.UpdatedBy,
			&ruang.NamaUnitKerja, &ruang.NomorOpnameBeku,
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
