package repository

import (
	"context"
	"fmt"
	"time"

	"Arthafreestyle/ERP/internal/entity"
)

// PresensiRepository owns every SQL statement touching presensi.
type PresensiRepository struct{}

func NewPresensiRepository() *PresensiRepository {
	return &PresensiRepository{}
}

// presensiColumns is the write-side list, unqualified, for INSERT/UPDATE ... RETURNING.
//
// ip_masuk/ip_pulang come back through host(), not a plain ::TEXT cast: INET
// decodes to netip.Prefix through pgx, which a *string cannot scan, and the cast
// would hand a client "172.18.0.1/32" for what is one address rather than a
// network.
const presensiColumns = `id, id_user, tanggal, shift, jam_masuk, jam_pulang, status,
	id_unit_kerja, sumber_masuk, sumber_pulang, host(ip_masuk), host(ip_pulang),
	dikoreksi_oleh, ts_koreksi, alasan_koreksi, created_at, updated_at`

// presensiReadColumns adds the employee's name and the unit's, resolved by the
// joins in presensiFrom. Fetching either per row would be an N+1: one query for
// the page plus one per attendance row.
//
// nama_lengkap is nullable, so it falls back to the username — a list of
// attendance with blank names in it is useless, and every user has a username
// by definition.
const presensiReadColumns = `p.id, p.id_user, p.tanggal, p.shift, p.jam_masuk, p.jam_pulang, p.status,
	p.id_unit_kerja, p.sumber_masuk, p.sumber_pulang, host(p.ip_masuk), host(p.ip_pulang),
	p.dikoreksi_oleh, p.ts_koreksi, p.alasan_koreksi, p.created_at, p.updated_at,
	COALESCE(u.nama_lengkap, u.username), k.nama`

// presensiFrom joins unit_kerja LEFT because id_unit_kerja is nullable — a tap
// made by a caller holding a global grant carries no unit, and an inner join
// would drop exactly those rows while total_item still counted them.
const presensiFrom = `
	FROM presensi p
	JOIN users u ON u.id = p.id_user
	LEFT JOIN unit_kerja k ON k.id = p.id_unit_kerja`

// presensiFilter is shared by the COUNT and the row query, and because it
// reaches users (the name search) the COUNT has to run against presensiFrom too
// rather than a bare FROM presensi — the same correction isu #12 fase 6 had to
// make in four other modules.
//
// The unit clause is where isu #12 fase 6 lands for this module: it needs no
// join and no extra column, because id_unit_kerja is already on the row. A row
// whose unit is NULL is invisible to any caller bound to one unit — NULL = $6 is
// never true — and that is deliberate: such a row was made under a global
// grant, and showing it to a unit-bound caller would hand them attendance from
// any unit at all.
//
// Placeholder discipline: the filter owns $1..$6 and pagination follows after
// it.
const presensiFilter = `
	WHERE ($1::BIGINT IS NULL OR p.id_user = $1)
	  AND ($2::DATE IS NULL OR p.tanggal >= $2)
	  AND ($3::DATE IS NULL OR p.tanggal <= $3)
	  AND ($4::VARCHAR IS NULL OR p.shift = $4)
	  AND ($5::VARCHAR IS NULL OR p.status = $5)
	  AND ($6::BIGINT IS NULL OR p.id_unit_kerja = $6)`

// PresensiFilter carries the list filters. Nil means "no filter" for every
// field.
type PresensiFilter struct {
	IDUser           *int64
	TanggalDari      *time.Time
	TanggalSampai    *time.Time
	Shift            *string
	Status           *string
	AktifIDUnitKerja *int64
}

// PresensiPatch carries a correction. jam_pulang may only ever be filled or
// moved, never cleared back to NULL — reopening a closed shift would reactivate
// presensi_terbuka_uidx for a day already gone, and the usecase refuses that
// before a patch ever reaches here.
type PresensiPatch struct {
	JamMasuk  *time.Time
	JamPulang *time.Time
	Shift     *string

	DikoreksiOleh int64
	TsKoreksi     time.Time
	AlasanKoreksi string
}

func (r *PresensiRepository) Create(ctx context.Context, db DBTX, presensi *entity.Presensi) error {
	const query = `
		INSERT INTO presensi (id_user, tanggal, shift, jam_masuk, status, id_unit_kerja,
			sumber_masuk, ip_masuk, dikoreksi_oleh, ts_koreksi, alasan_koreksi)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8::INET, $9, $10, $11)
		RETURNING ` + presensiColumns

	err := db.QueryRowContext(
		ctx, query,
		presensi.IDUser, presensi.Tanggal, presensi.Shift, presensi.JamMasuk, presensi.Status,
		presensi.IDUnitKerja, presensi.SumberMasuk, presensi.IPMasuk,
		presensi.DikoreksiOleh, presensi.TsKoreksi, presensi.AlasanKoreksi,
	).Scan(scanPresensiWrite(presensi)...)
	if err != nil {
		return fmt.Errorf("insert presensi: %w", err)
	}

	return nil
}

// FindByID returns sql.ErrNoRows when the row is absent; the usecase maps that
// to a 404.
func (r *PresensiRepository) FindByID(ctx context.Context, db DBTX, id int64) (*entity.Presensi, error) {
	const query = `SELECT ` + presensiReadColumns + presensiFrom + ` WHERE p.id = $1`

	presensi := new(entity.Presensi)
	if err := db.QueryRowContext(ctx, query, id).Scan(scanPresensi(presensi)...); err != nil {
		return nil, err
	}

	return presensi, nil
}

// FindByUserTanggalShift looks for the row at exactly (id_user, tanggal, shift)
// — the slot presensi_user_tanggal_shift_uidx protects. It answers
// sql.ErrNoRows when that slot is empty, which Masuk reads as "nothing recorded
// yet" rather than a duplicate.
func (r *PresensiRepository) FindByUserTanggalShift(ctx context.Context, db DBTX, idUser int64, tanggal time.Time, shift string) (*entity.Presensi, error) {
	const query = `SELECT ` + presensiReadColumns + presensiFrom + `
		WHERE p.id_user = $1 AND p.tanggal = $2 AND p.shift = $3`

	presensi := new(entity.Presensi)
	if err := db.QueryRowContext(ctx, query, idUser, tanggal, shift).Scan(scanPresensi(presensi)...); err != nil {
		return nil, err
	}

	return presensi, nil
}

// FindByUserTanggal returns every shift this person has recorded on one date —
// at most two rows, and normally one. It backs "what should the button look
// like right now".
func (r *PresensiRepository) FindByUserTanggal(ctx context.Context, db DBTX, idUser int64, tanggal time.Time) ([]entity.Presensi, error) {
	const query = `SELECT ` + presensiReadColumns + presensiFrom + `
		WHERE p.id_user = $1 AND p.tanggal = $2
		ORDER BY p.shift, p.id`

	rows, err := db.QueryContext(ctx, query, idUser, tanggal)
	if err != nil {
		return nil, fmt.Errorf("select presensi harian: %w", err)
	}
	defer rows.Close()

	list := make([]entity.Presensi, 0, 2)

	for rows.Next() {
		var presensi entity.Presensi
		if err := rows.Scan(scanPresensi(&presensi)...); err != nil {
			return nil, fmt.Errorf("scan presensi harian: %w", err)
		}

		list = append(list, presensi)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate presensi harian: %w", err)
	}

	return list, nil
}

// FindTerbuka returns the one shift this user currently has open, guaranteed
// unique by presensi_terbuka_uidx. sql.ErrNoRows means nobody has an open
// shift — Pulang reads that as "belum presensi masuk" and Masuk reads it as
// "nothing to close".
func (r *PresensiRepository) FindTerbuka(ctx context.Context, db DBTX, idUser int64) (*entity.Presensi, error) {
	const query = `SELECT ` + presensiReadColumns + presensiFrom + `
		WHERE p.id_user = $1 AND p.status = 'BUKA'`

	presensi := new(entity.Presensi)
	if err := db.QueryRowContext(ctx, query, idUser).Scan(scanPresensi(presensi)...); err != nil {
		return nil, err
	}

	return presensi, nil
}

// TandaiLupaPulang closes a row nobody tapped Pulang for, without inventing an
// hour. status is repeated in the WHERE, the same shape every guarded
// transition in this project uses, so two callers racing to close the same row
// cannot both believe they did it.
func (r *PresensiRepository) TandaiLupaPulang(ctx context.Context, db DBTX, id int64) error {
	const query = `UPDATE presensi SET status = 'LUPA_PULANG' WHERE id = $1 AND status = 'BUKA'`

	if _, err := db.ExecContext(ctx, query, id); err != nil {
		return fmt.Errorf("tandai lupa pulang: %w", err)
	}

	return nil
}

// Pulang closes the one open row this user has, whatever date or shift it
// belongs to — presensi_terbuka_uidx is what guarantees there is at most one.
// status is repeated in the WHERE so two Pulang taps racing on a bad connection
// cannot both close the same row; the loser's RowsAffected is 0.
func (r *PresensiRepository) Pulang(ctx context.Context, db DBTX, idUser int64, jamPulang time.Time, ipPulang *string) (*entity.Presensi, error) {
	const query = `
		UPDATE presensi SET
			jam_pulang    = $2,
			status        = 'SELESAI',
			sumber_pulang = 'TOMBOL',
			ip_pulang     = $3::INET
		WHERE id_user = $1 AND status = 'BUKA'
		RETURNING ` + presensiColumns

	presensi := new(entity.Presensi)
	if err := db.QueryRowContext(ctx, query, idUser, jamPulang, ipPulang).Scan(scanPresensiWrite(presensi)...); err != nil {
		return nil, err
	}

	return presensi, nil
}

// Update applies a correction and returns the stored row. RETURNING supplies
// the response; sql.ErrNoRows means the id does not exist, so there is no
// separate existence check — that would be two queries and still racy.
//
// status is recomputed here, never accepted from the caller: a jam_pulang that
// ends up non-NULL after the patch means SELESAI, whatever it was before.
// Correcting jam_masuk or shift alone, with jam_pulang still NULL, leaves
// status exactly as it was (BUKA or LUPA_PULANG) — a correction to the
// clock-in is not a claim about whether the shift ever closed.
//
// sumber_masuk turns KOREKSI when jam_masuk or shift moved — shift belongs to
// the clock-in event, and re-inferring it is never done, but recording that it
// was corrected still is. sumber_pulang turns KOREKSI only when jam_pulang
// itself moved.
func (r *PresensiRepository) Update(ctx context.Context, db DBTX, id int64, patch PresensiPatch) (*entity.Presensi, error) {
	const query = `
		UPDATE presensi SET
			jam_masuk      = COALESCE($2, jam_masuk),
			jam_pulang     = COALESCE($3, jam_pulang),
			shift          = COALESCE($4, shift),
			status         = CASE WHEN COALESCE($3, jam_pulang) IS NOT NULL THEN 'SELESAI' ELSE status END,
			sumber_masuk   = CASE WHEN $2 IS NOT NULL OR $4 IS NOT NULL THEN 'KOREKSI' ELSE sumber_masuk END,
			sumber_pulang  = CASE WHEN $3 IS NOT NULL THEN 'KOREKSI' ELSE sumber_pulang END,
			dikoreksi_oleh = $5,
			ts_koreksi     = $6,
			alasan_koreksi = $7
		WHERE id = $1
		RETURNING ` + presensiColumns

	presensi := new(entity.Presensi)

	err := db.QueryRowContext(
		ctx, query, id,
		patch.JamMasuk, patch.JamPulang, patch.Shift,
		patch.DikoreksiOleh, patch.TsKoreksi, patch.AlasanKoreksi,
	).Scan(scanPresensiWrite(presensi)...)
	if err != nil {
		return nil, err
	}

	return presensi, nil
}

// Search returns one page of rows plus the total matching count.
func (r *PresensiRepository) Search(ctx context.Context, db DBTX, filter PresensiFilter, limit, offset int) ([]entity.Presensi, int64, error) {
	args := []any{
		filter.IDUser, filter.TanggalDari, filter.TanggalSampai,
		filter.Shift, filter.Status, filter.AktifIDUnitKerja,
	}

	var total int64
	if err := db.QueryRowContext(
		ctx, `SELECT COUNT(*)`+presensiFrom+presensiFilter, args...,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count presensi: %w", err)
	}

	if total == 0 {
		return []entity.Presensi{}, 0, nil
	}

	// ORDER BY ends in a unique column: tanggal alone leaves a day's rows in an
	// unspecified order, which lets one row appear on two pages while another
	// is never returned at all.
	query := `SELECT ` + presensiReadColumns + presensiFrom + presensiFilter + `
		ORDER BY p.tanggal DESC, p.id DESC
		LIMIT $7 OFFSET $8`

	rows, err := db.QueryContext(ctx, query, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("select presensi: %w", err)
	}
	defer rows.Close()

	list := make([]entity.Presensi, 0, limit)

	for rows.Next() {
		var presensi entity.Presensi
		if err := rows.Scan(scanPresensi(&presensi)...); err != nil {
			return nil, 0, fmt.Errorf("scan presensi: %w", err)
		}

		list = append(list, presensi)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate presensi: %w", err)
	}

	return list, total, nil
}

// presensiRekapHarian collapses a month to one row per (employee, calendar day),
// counting each shift's completion and its minutes. It is the inner half of the
// recap and is declared once so the COUNT and the row query cannot drift apart.
//
// Grouping by tanggal rather than by jam_masuk's own month is deliberate: the
// two agree today, and naming the column explicitly is what keeps them agreeing
// if a shift ever crosses midnight.
//
// $1 tahun, $2 bulan, $3 active unit_kerja, $4 id_user (nil = every employee).
const presensiRekapHarian = `
	WITH harian AS (
		SELECT p.id_user,
		       p.tanggal,
		       COUNT(*) FILTER (WHERE p.shift = 'PAGI' AND p.status = 'SELESAI')  AS pagi_selesai,
		       COUNT(*) FILTER (WHERE p.shift = 'MALAM' AND p.status = 'SELESAI') AS malam_selesai,
		       COUNT(*) FILTER (WHERE p.shift = 'PAGI' AND p.status = 'LUPA_PULANG')  AS pagi_lupa_pulang,
		       COUNT(*) FILTER (WHERE p.shift = 'MALAM' AND p.status = 'LUPA_PULANG') AS malam_lupa_pulang,
		       COALESCE(SUM(EXTRACT(EPOCH FROM (p.jam_pulang - p.jam_masuk)) / 60)
		                FILTER (WHERE p.shift = 'PAGI' AND p.status = 'SELESAI'), 0)  AS menit_pagi,
		       COALESCE(SUM(EXTRACT(EPOCH FROM (p.jam_pulang - p.jam_masuk)) / 60)
		                FILTER (WHERE p.shift = 'MALAM' AND p.status = 'SELESAI'), 0) AS menit_malam
		FROM presensi p
		WHERE p.tanggal >= make_date($1, $2, 1)
		  AND p.tanggal < (make_date($1, $2, 1) + INTERVAL '1 month')
		  AND ($3::BIGINT IS NULL OR p.id_unit_kerja = $3)
		  AND ($4::BIGINT IS NULL OR p.id_user = $4)
		GROUP BY p.id_user, p.tanggal
	)`

// RekapBulanan answers the month for a whole page of employees in ONE query,
// never one query per employee — the anti-N+1 shape SaldoPerRuangBatch
// established.
//
// hari_dua_shift is the nested aggregation: a COUNT over the per-day rows that
// carry both shifts SELESAI, which is why the CTE exists at all. Summing pagi
// and malam would count a two-shift day twice and could never say which days
// overlapped — and that day is the entire reason this recap is being built now
// rather than later.
//
// It reports and does not decide: nothing here weighs a LUPA_PULANG day as
// payable or not.
func (r *PresensiRepository) RekapBulanan(ctx context.Context, db DBTX, tahun, bulan int, idUser *int64, aktifIDUnitKerja *int64, limit, offset int) ([]entity.PresensiRekap, int64, error) {
	args := []any{tahun, bulan, aktifIDUnitKerja, idUser}

	var total int64
	if err := db.QueryRowContext(
		ctx,
		presensiRekapHarian+`SELECT COUNT(DISTINCT h.id_user) FROM harian h`,
		args...,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count rekap presensi: %w", err)
	}

	if total == 0 {
		return []entity.PresensiRekap{}, 0, nil
	}

	query := presensiRekapHarian + `
		SELECT h.id_user,
		       COALESCE(u.nama_lengkap, u.username),
		       SUM(h.pagi_selesai)::INT,
		       SUM(h.malam_selesai)::INT,
		       COUNT(*) FILTER (WHERE h.pagi_selesai > 0 AND h.malam_selesai > 0)::INT,
		       SUM(h.pagi_lupa_pulang)::INT,
		       SUM(h.malam_lupa_pulang)::INT,
		       ROUND(SUM(h.menit_pagi))::INT,
		       ROUND(SUM(h.menit_malam))::INT
		FROM harian h
		JOIN users u ON u.id = h.id_user
		GROUP BY h.id_user, u.nama_lengkap, u.username
		ORDER BY COALESCE(u.nama_lengkap, u.username), h.id_user
		LIMIT $5 OFFSET $6`

	rows, err := db.QueryContext(ctx, query, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("select rekap presensi: %w", err)
	}
	defer rows.Close()

	list := make([]entity.PresensiRekap, 0, limit)

	for rows.Next() {
		rekap := entity.PresensiRekap{Tahun: tahun, Bulan: bulan}

		if err := rows.Scan(
			&rekap.IDUser, &rekap.NamaUser, &rekap.HariPagiSelesai, &rekap.HariMalamSelesai,
			&rekap.HariDuaShift, &rekap.HariLupaPulangPagi, &rekap.HariLupaPulangMalam,
			&rekap.TotalMenitKerjaPagi, &rekap.TotalMenitKerjaMalam,
		); err != nil {
			return nil, 0, fmt.Errorf("scan rekap presensi: %w", err)
		}

		list = append(list, rekap)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate rekap presensi: %w", err)
	}

	return list, total, nil
}

// lockSapuanPresensi keys the daily sweep job's session-level advisory lock —
// isu #40 fase 5. Must differ from lockPembersihanDokumen (dokumen_repository.go)
// and lockRekonsiliasi (kartu_stok_repository.go): the three jobs share
// pg_advisory_lock's one namespace and picking the same number would make one
// job silently skip a run whenever another happened to be mid-sweep.
const lockSapuanPresensi int64 = 40_001_040

// TryLockSapuan takes the sweep job's advisory lock and reports whether it got
// it. A false is not a failure: another worker is already sweeping, and this one
// has nothing to do. db must be a single connection — a *sql.Conn — or the
// unlock can land on a different pooled connection and quietly do nothing.
func (r *PresensiRepository) TryLockSapuan(ctx context.Context, db DBTX) (bool, error) {
	var locked bool
	if err := db.QueryRowContext(
		ctx, `SELECT pg_try_advisory_lock($1)`, lockSapuanPresensi,
	).Scan(&locked); err != nil {
		return false, fmt.Errorf("lock sapuan presensi: %w", err)
	}

	return locked, nil
}

// UnlockSapuan releases the lock TryLockSapuan took. Must run on the same
// connection.
func (r *PresensiRepository) UnlockSapuan(ctx context.Context, db DBTX) error {
	if _, err := db.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, lockSapuanPresensi); err != nil {
		return fmt.Errorf("unlock sapuan presensi: %w", err)
	}

	return nil
}

// SapuanLupaPulang marks every row still BUKA whose tanggal has already passed
// as LUPA_PULANG, without ever inventing jam_pulang.
//
// Swept per date, never per shift: a PAGI row still open at 20:00 the same day
// is left alone, because the person may genuinely still be there and a MALAM
// tap already handles that case correctly. batas is the first date NOT yet
// swept — normally today, in the caller's own reckoning of WIB — so only
// strictly earlier dates are touched.
func (r *PresensiRepository) SapuanLupaPulang(ctx context.Context, db DBTX, batas time.Time) (int64, error) {
	const query = `UPDATE presensi SET status = 'LUPA_PULANG' WHERE status = 'BUKA' AND tanggal < $1`

	result, err := db.ExecContext(ctx, query, batas)
	if err != nil {
		return 0, fmt.Errorf("sapuan lupa pulang: %w", err)
	}

	return result.RowsAffected()
}

// scanPresensi keeps the scan targets in one place, in presensiReadColumns'
// order. Several call sites read the same column list, and a column added to
// that constant without a target added here is a runtime error in all of them
// at once — which is exactly why the two live next to each other.
func scanPresensi(presensi *entity.Presensi) []any {
	return []any{
		&presensi.ID, &presensi.IDUser, &presensi.Tanggal, &presensi.Shift,
		&presensi.JamMasuk, &presensi.JamPulang, &presensi.Status,
		&presensi.IDUnitKerja, &presensi.SumberMasuk, &presensi.SumberPulang,
		&presensi.IPMasuk, &presensi.IPPulang,
		&presensi.DikoreksiOleh, &presensi.TsKoreksi, &presensi.AlasanKoreksi,
		&presensi.CreatedAt, &presensi.UpdatedAt,
		&presensi.NamaUser, &presensi.NamaUnitKerja,
	}
}

// scanPresensiWrite is scanPresensi without the two joined names, for the
// write-side RETURNING clause (presensiColumns), which carries no join.
func scanPresensiWrite(presensi *entity.Presensi) []any {
	return []any{
		&presensi.ID, &presensi.IDUser, &presensi.Tanggal, &presensi.Shift,
		&presensi.JamMasuk, &presensi.JamPulang, &presensi.Status,
		&presensi.IDUnitKerja, &presensi.SumberMasuk, &presensi.SumberPulang,
		&presensi.IPMasuk, &presensi.IPPulang,
		&presensi.DikoreksiOleh, &presensi.TsKoreksi, &presensi.AlasanKoreksi,
		&presensi.CreatedAt, &presensi.UpdatedAt,
	}
}
