package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"Arthafreestyle/ERP/internal/entity"
)

// ProductRepository owns every SQL statement touching product, product_satuan, and
// product_harga_jual. The three live together because every write to a child is driven
// by its product.
type ProductRepository struct{}

func NewProductRepository() *ProductRepository {
	return &ProductRepository{}
}

// productColumns is the write-side list for INSERT/UPDATE ... RETURNING.
const productColumns = `id, kode_barang, nama, id_satuan_dasar, stok_minimum, is_aktif,
	created_at, created_by, updated_at, updated_by`

// productReadColumns adds the base unit's name, resolved by the join in productFrom.
// Fetching it per row would be an N+1.
const productReadColumns = `p.id, p.kode_barang, p.nama, p.id_satuan_dasar, p.stok_minimum,
	p.is_aktif, p.created_at, p.created_by, p.updated_at, p.updated_by, s.nama`

// productFrom joins satuan INNER, not LEFT: product.id_satuan_dasar is NOT NULL and
// carries a foreign key, so a product without a base unit cannot exist. An outer join
// here would only suggest otherwise.
const productFrom = `
	FROM product p
	JOIN satuan s ON s.id = p.id_satuan_dasar`

// productFilter is shared by the COUNT and the row query. Two copies of a filter
// eventually diverge and total_item starts lying about the data.
//
// Placeholder discipline: the filter owns $1..$2 and pagination follows after it.
const productFilter = `
	WHERE ($1 = '' OR p.nama ILIKE '%' || $1 || '%' OR p.kode_barang ILIKE '%' || $1 || '%')
	  AND ($2::BOOLEAN IS NULL OR p.is_aktif = $2)`

// ProductPatch carries a partial update. kode_barang and id_satuan_dasar are absent by
// design — see model.UpdateProductRequest for why. Every field here is NOT NULL, so
// COALESCE is correct and no presence flag is needed.
type ProductPatch struct {
	Nama        *string
	StokMinimum *int64
	IsAktif     *bool
	UpdatedBy   *int64
}

func (r *ProductRepository) Create(ctx context.Context, db DBTX, product *entity.Product) error {
	const query = `
		INSERT INTO product (kode_barang, nama, id_satuan_dasar, stok_minimum, is_aktif, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING ` + productColumns

	err := db.QueryRowContext(
		ctx, query,
		product.KodeBarang, product.Nama, product.IDSatuanDasar,
		product.StokMinimum, product.IsAktif, product.CreatedBy,
	).Scan(
		&product.ID, &product.KodeBarang, &product.Nama, &product.IDSatuanDasar,
		&product.StokMinimum, &product.IsAktif,
		&product.CreatedAt, &product.CreatedBy, &product.UpdatedAt, &product.UpdatedBy,
	)
	if err != nil {
		return fmt.Errorf("insert product: %w", err)
	}

	return nil
}

// FindByID returns sql.ErrNoRows when the row is absent; the usecase maps that to a 404.
// Children are not included — the caller adds them with FindSatuan and FindHargaJual.
func (r *ProductRepository) FindByID(ctx context.Context, db DBTX, id int64) (*entity.Product, error) {
	const query = `SELECT ` + productReadColumns + productFrom + ` WHERE p.id = $1`

	product := new(entity.Product)

	err := db.QueryRowContext(ctx, query, id).Scan(
		&product.ID, &product.KodeBarang, &product.Nama, &product.IDSatuanDasar,
		&product.StokMinimum, &product.IsAktif,
		&product.CreatedAt, &product.CreatedBy, &product.UpdatedAt, &product.UpdatedBy,
		&product.NamaSatuanDasar,
	)
	if err != nil {
		return nil, err
	}

	return product, nil
}

// ExistsByKodeBarang mirrors product_kode_barang_key. The column is NOT NULL, so unlike
// the nullable master codes this answer is always meaningful. Pass 0 when creating.
func (r *ProductRepository) ExistsByKodeBarang(ctx context.Context, db DBTX, kode string, exceptID int64) (bool, error) {
	const query = `
		SELECT EXISTS (
			SELECT 1 FROM product
			WHERE lower(kode_barang) = lower($1)
			  AND ($2 = 0 OR id <> $2)
		)`

	var exists bool
	if err := db.QueryRowContext(ctx, query, kode, exceptID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check product kode_barang: %w", err)
	}

	return exists, nil
}

// Update applies a patch and returns the stored row. sql.ErrNoRows means the id does
// not exist, so there is no separate existence check.
//
// NamaSatuanDasar is left empty here: RETURNING cannot reach the joined satuan table.
// The usecase re-reads through FindByID when it needs the name.
func (r *ProductRepository) Update(ctx context.Context, db DBTX, id int64, patch ProductPatch) (*entity.Product, error) {
	// updated_at is left to the product_set_updated_at trigger from migration 000002.
	const query = `
		UPDATE product SET
			nama         = COALESCE($2, nama),
			stok_minimum = COALESCE($3, stok_minimum),
			is_aktif     = COALESCE($4, is_aktif),
			updated_by   = $5
		WHERE id = $1
		RETURNING ` + productColumns

	product := new(entity.Product)

	err := db.QueryRowContext(
		ctx, query, id,
		patch.Nama, patch.StokMinimum, patch.IsAktif, patch.UpdatedBy,
	).Scan(
		&product.ID, &product.KodeBarang, &product.Nama, &product.IDSatuanDasar,
		&product.StokMinimum, &product.IsAktif,
		&product.CreatedAt, &product.CreatedBy, &product.UpdatedAt, &product.UpdatedBy,
	)
	if err != nil {
		return nil, err
	}

	return product, nil
}

// Search returns one page of products plus the total matching count. Children are not
// attached: a list of 20 products with their units and price history would be three
// queries deep for data the list view does not show.
func (r *ProductRepository) Search(ctx context.Context, db DBTX, search string, isAktif *bool, limit, offset int) ([]entity.Product, int64, error) {
	search = EscapeLike(search)

	// COUNT skips the join: nothing in the filter needs satuan.
	var total int64
	if err := db.QueryRowContext(
		ctx, `SELECT COUNT(*) FROM product p`+productFilter, search, isAktif,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count product: %w", err)
	}

	if total == 0 {
		return []entity.Product{}, 0, nil
	}

	// ORDER BY ends in a unique column: nama alone leaves ties in an unspecified
	// order, which lets one row appear on two pages while another is never returned.
	// product_nama_id_idx from migration 000011 supports exactly this.
	query := `SELECT ` + productReadColumns + productFrom + productFilter + `
		ORDER BY p.nama, p.id
		LIMIT $3 OFFSET $4`

	rows, err := db.QueryContext(ctx, query, search, isAktif, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("select product: %w", err)
	}
	defer rows.Close()

	list := make([]entity.Product, 0, limit)

	for rows.Next() {
		var product entity.Product

		if err := rows.Scan(
			&product.ID, &product.KodeBarang, &product.Nama, &product.IDSatuanDasar,
			&product.StokMinimum, &product.IsAktif,
			&product.CreatedAt, &product.CreatedBy, &product.UpdatedAt, &product.UpdatedBy,
			&product.NamaSatuanDasar,
		); err != nil {
			return nil, 0, fmt.Errorf("scan product: %w", err)
		}

		list = append(list, product)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate product: %w", err)
	}

	return list, total, nil
}

// InsertSatuan adds one conversion row.
//
// ON CONFLICT names bare columns because product_satuan_product_satuan_uidx indexes
// bare columns. DO UPDATE rather than DO NOTHING so re-sending a unit corrects its
// factor instead of silently keeping the old one — which would look like the request
// succeeded while the stored conversion disagreed with it.
func (r *ProductRepository) InsertSatuan(ctx context.Context, db DBTX, satuan *entity.ProductSatuan) error {
	const query = `
		INSERT INTO product_satuan (id_product, id_satuan, faktor, is_default_input)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (id_product, id_satuan) DO UPDATE
			SET faktor = EXCLUDED.faktor, is_default_input = EXCLUDED.is_default_input
		RETURNING id, id_product, id_satuan, faktor, is_default_input`

	err := db.QueryRowContext(
		ctx, query,
		satuan.IDProduct, satuan.IDSatuan, satuan.Faktor, satuan.IsDefaultInput,
	).Scan(
		&satuan.ID, &satuan.IDProduct, &satuan.IDSatuan, &satuan.Faktor, &satuan.IsDefaultInput,
	)
	if err != nil {
		return fmt.Errorf("insert product_satuan: %w", err)
	}

	return nil
}

// ClearDefaultSatuan drops the default flag from every unit of a product except one.
//
// Called before setting a new default, because product_satuan_default_uidx allows only
// one flagged row per product: without this, marking a second unit as default fails
// with a unique violation instead of moving the flag.
func (r *ProductRepository) ClearDefaultSatuan(ctx context.Context, db DBTX, productID, exceptSatuanID int64) error {
	const query = `
		UPDATE product_satuan
		SET is_default_input = FALSE
		WHERE id_product = $1 AND is_default_input AND id_satuan <> $2`

	if _, err := db.ExecContext(ctx, query, productID, exceptSatuanID); err != nil {
		return fmt.Errorf("clear default product_satuan: %w", err)
	}

	return nil
}

// FindSatuan returns a product's conversion units, base unit included.
func (r *ProductRepository) FindSatuan(ctx context.Context, db DBTX, productID int64) ([]entity.ProductSatuan, error) {
	// Ordered by faktor so the base unit (faktor = 1) comes first and larger packings
	// follow, then by id to break ties on equal factors.
	const query = `
		SELECT ps.id, ps.id_product, ps.id_satuan, ps.faktor, ps.is_default_input, s.nama
		FROM product_satuan ps
		JOIN satuan s ON s.id = ps.id_satuan
		WHERE ps.id_product = $1
		ORDER BY ps.faktor, ps.id`

	rows, err := db.QueryContext(ctx, query, productID)
	if err != nil {
		return nil, fmt.Errorf("select product_satuan: %w", err)
	}
	defer rows.Close()

	list := make([]entity.ProductSatuan, 0, 4)

	for rows.Next() {
		var satuan entity.ProductSatuan

		if err := rows.Scan(
			&satuan.ID, &satuan.IDProduct, &satuan.IDSatuan,
			&satuan.Faktor, &satuan.IsDefaultInput, &satuan.NamaSatuan,
		); err != nil {
			return nil, fmt.Errorf("scan product_satuan: %w", err)
		}

		list = append(list, satuan)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate product_satuan: %w", err)
	}

	return list, nil
}

// HasSatuan reports whether a product already carries the given unit. Used to reject a
// price for a unit the product does not sell in — the schema has no foreign key tying
// product_harga_jual.id_satuan to product_satuan, so nothing else catches it.
func (r *ProductRepository) HasSatuan(ctx context.Context, db DBTX, productID, satuanID int64) (bool, error) {
	const query = `
		SELECT EXISTS (
			SELECT 1 FROM product_satuan WHERE id_product = $1 AND id_satuan = $2
		)`

	var exists bool
	if err := db.QueryRowContext(ctx, query, productID, satuanID).Scan(&exists); err != nil {
		return false, fmt.Errorf("check product_satuan: %w", err)
	}

	return exists, nil
}

// FaktorKey identifies one (product, unit) pair in the batch factor lookup.
type FaktorKey struct {
	IDProduct int64
	IDSatuan  int64
}

// FindFaktorBatch resolves the conversion factor for many (product, unit) pairs at
// once, returning only the pairs that are actually registered.
//
// One query, not one per line. A purchase document can carry hundreds of lines and
// every one of them needs its factor before it can be converted to base units;
// asking per line would make posting cost a round trip per row inside a transaction
// that is already holding row locks.
//
// A pair that is missing from the result is a unit the product is not sold in — the
// caller reports which one rather than letting a zero factor through, since
// qty_dasar would then be zero and the CHECK would reject it with a constraint name
// instead of a field name.
//
// pgx/stdlib implements CheckNamedValue, so a Go []int64 passes through
// database/sql untouched and needs no array wrapper.
func (r *ProductRepository) FindFaktorBatch(ctx context.Context, db DBTX, productIDs, satuanIDs []int64) (map[FaktorKey]int64, error) {
	const query = `
		SELECT ps.id_product, ps.id_satuan, ps.faktor
		FROM product_satuan ps
		JOIN unnest($1::BIGINT[], $2::BIGINT[]) AS pasangan(id_product, id_satuan)
			ON ps.id_product = pasangan.id_product AND ps.id_satuan = pasangan.id_satuan`

	rows, err := db.QueryContext(ctx, query, productIDs, satuanIDs)
	if err != nil {
		return nil, fmt.Errorf("select product_satuan faktor: %w", err)
	}
	defer rows.Close()

	faktor := make(map[FaktorKey]int64, len(productIDs))

	for rows.Next() {
		var key FaktorKey
		var nilai int64

		if err := rows.Scan(&key.IDProduct, &key.IDSatuan, &nilai); err != nil {
			return nil, fmt.Errorf("scan product_satuan faktor: %w", err)
		}

		faktor[key] = nilai
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate product_satuan faktor: %w", err)
	}

	return faktor, nil
}

// CloseOpenHargaJual ends any still-open price version for a product and unit at the
// given date.
//
// The range is '[)', so closing at the new version's start date leaves neither gap nor
// overlap: the old price covers up to that day, the new one from it.
//
// The `berlaku_dari < $3` guard matters. Without it, a version starting on or after the
// new date would be closed to a date at or before its own start, violating
// product_harga_jual_periode_check — and an existing future price is a real
// possibility, not a hypothetical.
func (r *ProductRepository) CloseOpenHargaJual(ctx context.Context, db DBTX, productID, satuanID int64, until time.Time) error {
	const query = `
		UPDATE product_harga_jual
		SET berlaku_sampai = $3
		WHERE id_product = $1
		  AND id_satuan = $2
		  AND berlaku_sampai IS NULL
		  AND berlaku_dari < $3`

	if _, err := db.ExecContext(ctx, query, productID, satuanID, until); err != nil {
		return fmt.Errorf("close product_harga_jual: %w", err)
	}

	return nil
}

// InsertHargaJual opens a new price version.
//
// An overlap raises SQLSTATE 23P01 from product_harga_jual_no_overlap, which the
// usecase turns into a 409. That constraint is the only real guard: the check spans
// rows, so no pre-check in Go can replace it.
func (r *ProductRepository) InsertHargaJual(ctx context.Context, db DBTX, harga *entity.ProductHargaJual) error {
	const query = `
		INSERT INTO product_harga_jual (id_product, id_satuan, harga, berlaku_dari, created_by)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, id_product, id_satuan, harga::TEXT, berlaku_dari, berlaku_sampai,
		          created_at, created_by`

	err := db.QueryRowContext(
		ctx, query,
		harga.IDProduct, harga.IDSatuan, harga.Harga, harga.BerlakuDari, harga.CreatedBy,
	).Scan(
		&harga.ID, &harga.IDProduct, &harga.IDSatuan, &harga.Harga,
		&harga.BerlakuDari, &harga.BerlakuSampai, &harga.CreatedAt, &harga.CreatedBy,
	)
	if err != nil {
		return fmt.Errorf("insert product_harga_jual: %w", err)
	}

	return nil
}

// FindHargaJual returns a product's price versions, newest first.
//
// harga is cast to TEXT so NUMERIC(20,2) arrives as the exact decimal PostgreSQL
// stored. Scanning it into a float64 would round money on the way out.
// FindHargaBerlaku answers which price version is in force, per satuan, for one
// product on one date — isu #8 fase 1. The database's own decision, not something
// resolved in Go: because product_harga_jual_no_overlap forbids two versions from
// ever being valid at once, "which one applies" is a plain WHERE rather than a
// DISTINCT ON "latest wins" — there is never more than one candidate to pick from.
func (r *ProductRepository) FindHargaBerlaku(ctx context.Context, db DBTX, productID int64, tanggal time.Time) ([]entity.HargaJualBerlaku, error) {
	const query = `
		SELECT h.id, h.id_satuan, s.nama, h.harga::TEXT, h.berlaku_dari, h.berlaku_sampai
		FROM product_harga_jual h
		JOIN satuan s ON s.id = h.id_satuan
		WHERE h.id_product = $1
		  AND h.berlaku_dari <= $2
		  AND (h.berlaku_sampai IS NULL OR h.berlaku_sampai > $2)
		ORDER BY s.nama, h.id_satuan`

	rows, err := db.QueryContext(ctx, query, productID, tanggal)
	if err != nil {
		return nil, fmt.Errorf("select harga berlaku: %w", err)
	}
	defer rows.Close()

	list := make([]entity.HargaJualBerlaku, 0, 4)

	for rows.Next() {
		var harga entity.HargaJualBerlaku

		if err := rows.Scan(
			&harga.IDHargaJual, &harga.IDSatuan, &harga.NamaSatuan,
			&harga.Harga, &harga.BerlakuDari, &harga.BerlakuSampai,
		); err != nil {
			return nil, fmt.Errorf("scan harga berlaku: %w", err)
		}

		list = append(list, harga)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate harga berlaku: %w", err)
	}

	return list, nil
}

// HargaBerlakuKey identifies one (product, unit) pair in the batch price lookup,
// mirroring FaktorKey.
type HargaBerlakuKey struct {
	IDProduct int64
	IDSatuan  int64
}

// FindHargaBerlakuBatch resolves the price in force for many (product, unit) pairs at
// once, on one date — isu #8 fase 1, built for penjualan to fill a whole basket of
// lines in one query rather than one round trip per line, exactly like
// FindFaktorBatch and KartuStokRepository.SaldoBatch.
//
// A pair missing from the result has no price in force on that date — including a
// product that has never had one — and the caller decides what that means: this
// codebase already promises a price is a proposal, not a requirement, so an absent
// entry is not itself an error.
func (r *ProductRepository) FindHargaBerlakuBatch(ctx context.Context, db DBTX, productIDs, satuanIDs []int64, tanggal time.Time) (map[HargaBerlakuKey]entity.HargaBerlaku, error) {
	const query = `
		SELECT h.id_product, h.id_satuan, h.id, h.harga::TEXT
		FROM product_harga_jual h
		JOIN unnest($1::BIGINT[], $2::BIGINT[]) AS pasangan(id_product, id_satuan)
			ON h.id_product = pasangan.id_product AND h.id_satuan = pasangan.id_satuan
		WHERE h.berlaku_dari <= $3
		  AND (h.berlaku_sampai IS NULL OR h.berlaku_sampai > $3)`

	rows, err := db.QueryContext(ctx, query, productIDs, satuanIDs, tanggal)
	if err != nil {
		return nil, fmt.Errorf("select harga berlaku batch: %w", err)
	}
	defer rows.Close()

	hasil := make(map[HargaBerlakuKey]entity.HargaBerlaku, len(productIDs))

	for rows.Next() {
		var key HargaBerlakuKey
		var harga entity.HargaBerlaku

		if err := rows.Scan(&key.IDProduct, &key.IDSatuan, &harga.IDHargaJual, &harga.Harga); err != nil {
			return nil, fmt.Errorf("scan harga berlaku batch: %w", err)
		}

		hasil[key] = harga
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate harga berlaku batch: %w", err)
	}

	return hasil, nil
}

// HargaJualDipakaiDokumen reports whether a price version has already been used by a
// penjualan_detail line — isu #8 fase 2, the one guard both UpdateHargaJual and
// DeleteHargaJual are refused by. penjualan_detail already snapshots
// harga_satuan_input, so touching the master row here would never change what a past
// document billed — but it would leave id_harga_jual pointing at a row that no longer
// says which price-list version that document came from, and that trace is the only
// one this system keeps.
func (r *ProductRepository) HargaJualDipakaiDokumen(ctx context.Context, db DBTX, idHargaJual int64) (bool, error) {
	const query = `SELECT EXISTS (SELECT 1 FROM penjualan_detail WHERE id_harga_jual = $1)`

	var dipakai bool
	if err := db.QueryRowContext(ctx, query, idHargaJual).Scan(&dipakai); err != nil {
		return false, fmt.Errorf("check penjualan_detail id_harga_jual: %w", err)
	}

	return dipakai, nil
}

// FindHargaJualByID locks and returns one price version, scoped to its own product so
// a caller cannot reach across products with a bare id. Used by DeleteHargaJual to
// read the row's id_satuan and berlaku_dari/berlaku_sampai before removing it — the
// facts ReopenPreviousHargaJual needs to know what to hand back to the version before
// it.
func (r *ProductRepository) FindHargaJualByID(ctx context.Context, db DBTX, id, productID int64) (*entity.ProductHargaJual, error) {
	const query = `
		SELECT id, id_product, id_satuan, harga::TEXT, berlaku_dari, berlaku_sampai,
		       created_at, created_by
		FROM product_harga_jual
		WHERE id = $1 AND id_product = $2
		FOR UPDATE`

	harga := new(entity.ProductHargaJual)

	err := db.QueryRowContext(ctx, query, id, productID).Scan(
		&harga.ID, &harga.IDProduct, &harga.IDSatuan, &harga.Harga,
		&harga.BerlakuDari, &harga.BerlakuSampai, &harga.CreatedAt, &harga.CreatedBy,
	)
	if err != nil {
		return nil, err
	}

	return harga, nil
}

// UpdateHargaJual corrects the price on an existing version. id_satuan and
// berlaku_dari are absent on purpose — see model.UpdateProductHargaJualRequest:
// berlaku_dari shifts a range and can collide with a neighbour, so the correction for
// that is delete-and-retype, which is what should show up in the trail anyway.
func (r *ProductRepository) UpdateHargaJual(ctx context.Context, db DBTX, id, productID int64, harga string) (*entity.ProductHargaJual, error) {
	const query = `
		UPDATE product_harga_jual
		SET harga = $3
		WHERE id = $1 AND id_product = $2
		RETURNING id, id_product, id_satuan, harga::TEXT, berlaku_dari, berlaku_sampai,
		          created_at, created_by`

	updated := new(entity.ProductHargaJual)

	err := db.QueryRowContext(ctx, query, id, productID, harga).Scan(
		&updated.ID, &updated.IDProduct, &updated.IDSatuan, &updated.Harga,
		&updated.BerlakuDari, &updated.BerlakuSampai, &updated.CreatedAt, &updated.CreatedBy,
	)
	if err != nil {
		return nil, err
	}

	return updated, nil
}

// DeleteHargaJual removes one price version outright — the third exception to
// "master data has no DELETE", after user_role and dokumen. The row it is allowed to
// remove is one no document references (HargaJualDipakaiDokumen guards that before
// this is ever called), so there is no audit trail to lose: nothing ever depended on
// it. sql.ErrNoRows means the id does not exist under this product.
func (r *ProductRepository) DeleteHargaJual(ctx context.Context, db DBTX, id, productID int64) error {
	const query = `DELETE FROM product_harga_jual WHERE id = $1 AND id_product = $2`

	result, err := db.ExecContext(ctx, query, id, productID)
	if err != nil {
		return fmt.Errorf("delete product_harga_jual: %w", err)
	}

	n, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected delete product_harga_jual: %w", err)
	}
	if n == 0 {
		return sql.ErrNoRows
	}

	return nil
}

// ReopenPreviousHargaJual undoes what CloseOpenHargaJual did to the version
// immediately before the one being deleted: it hands berlaku_sampai back to whatever
// the deleted version's own berlaku_sampai was (NULL if the deleted version was the
// open-ended one). The version to reopen is identified by berlaku_sampai equalling
// the deleted row's berlaku_dari — that is exactly the date CloseOpenHargaJual closed
// it to when the now-deleted version was opened.
//
// Without this, deleting a version leaves a gap: a date range covered by no price at
// all, which FindHargaBerlaku would then read back as "no price" for a product that
// plainly has one. A product with no earlier version simply has nothing to reopen —
// zero rows affected is not an error, it means the deleted version was the first ever.
func (r *ProductRepository) ReopenPreviousHargaJual(ctx context.Context, db DBTX, productID, satuanID int64, berlakuDariDihapus time.Time, berlakuSampaiDihapus *time.Time) error {
	const query = `
		UPDATE product_harga_jual
		SET berlaku_sampai = $4
		WHERE id_product = $1 AND id_satuan = $2 AND berlaku_sampai = $3`

	if _, err := db.ExecContext(
		ctx, query, productID, satuanID, berlakuDariDihapus, berlakuSampaiDihapus,
	); err != nil {
		return fmt.Errorf("reopen previous product_harga_jual: %w", err)
	}

	return nil
}

// daftarHargaFrom is shared by SearchDaftarHargaJual's COUNT and row query — isu #8
// fase 3. The date filter lives in the LEFT JOIN's ON clause, not in a WHERE: a
// product with no version in force on the requested date must still produce one row
// with null price columns, which is the entire point of using LEFT JOIN over JOIN
// here — putting the date check in WHERE would silently turn it back into an inner
// join and drop exactly the products this list exists to surface.
const daftarHargaFrom = `
	FROM product p
	LEFT JOIN product_harga_jual h
		ON h.id_product = p.id
		AND h.berlaku_dari <= $3
		AND (h.berlaku_sampai IS NULL OR h.berlaku_sampai > $3)
	LEFT JOIN satuan s ON s.id = h.id_satuan`

// daftarHargaFilter is shared by the COUNT and the row query, same discipline as
// productFilter: two copies drift and total_item stops agreeing with the rows.
const daftarHargaFilter = `
	WHERE ($1 = '' OR p.nama ILIKE '%' || $1 || '%' OR p.kode_barang ILIKE '%' || $1 || '%')
	  AND ($2::BOOLEAN IS NULL OR p.is_aktif = $2)`

const daftarHargaColumns = `p.id, p.kode_barang, p.nama, p.is_aktif,
	h.id_satuan, s.nama, h.id, h.harga::TEXT, h.berlaku_dari, h.berlaku_sampai`

// SearchDaftarHargaJual lists one row per product and satuan with a price in force on
// tanggal, plus every product with none at all — isu #8 fase 3. It is a read that is
// not its own module: no new table, no migration, one query in the repository that
// already owns product_harga_jual, following the shape riwayat_beli set for a
// projection over rows that already exist.
func (r *ProductRepository) SearchDaftarHargaJual(ctx context.Context, db DBTX, search string, isAktif *bool, tanggal time.Time, limit, offset int) ([]entity.DaftarHargaJual, int64, error) {
	search = EscapeLike(search)

	var total int64
	if err := db.QueryRowContext(
		ctx, `SELECT COUNT(*) `+daftarHargaFrom+daftarHargaFilter, search, isAktif, tanggal,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count daftar harga jual: %w", err)
	}

	if total == 0 {
		return []entity.DaftarHargaJual{}, 0, nil
	}

	// ORDER BY ends in (p.id, h.id_satuan): p.id alone is unique per product, and
	// h.id_satuan is what makes a multi-satuan product's own rows deterministic —
	// the no-overlap constraint keeps at most one price row per (product, satuan) on
	// any given date, so that pair is never ambiguous even with h.id_satuan NULL for
	// a priceless product, which can only ever produce that one row.
	query := `SELECT ` + daftarHargaColumns + daftarHargaFrom + daftarHargaFilter + `
		ORDER BY p.nama, p.id, h.id_satuan
		LIMIT $4 OFFSET $5`

	rows, err := db.QueryContext(ctx, query, search, isAktif, tanggal, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("select daftar harga jual: %w", err)
	}
	defer rows.Close()

	list := make([]entity.DaftarHargaJual, 0, limit)

	for rows.Next() {
		var row entity.DaftarHargaJual

		if err := rows.Scan(
			&row.IDProduct, &row.KodeBarang, &row.NamaProduct, &row.IsAktif,
			&row.IDSatuan, &row.NamaSatuan, &row.IDHargaJual, &row.Harga,
			&row.BerlakuDari, &row.BerlakuSampai,
		); err != nil {
			return nil, 0, fmt.Errorf("scan daftar harga jual: %w", err)
		}

		list = append(list, row)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate daftar harga jual: %w", err)
	}

	return list, total, nil
}

// posProductFrom and posProductFilter back SearchPOS — isu #11, GET /api/v1/pos/product.
// Only active products, always: no is_aktif parameter exists on this endpoint at all,
// because a retired product must never become sellable again just because a switch
// was left open on this one screen.
const posProductFrom = `FROM product p`

const posProductFilter = `
	WHERE p.is_aktif
	  AND ($1 = '' OR p.nama ILIKE '%' || $1 || '%' OR p.kode_barang ILIKE '%' || $1 || '%')`

const posProductColumns = `p.id, p.kode_barang, p.nama`

// SearchPOS is the first of the POS catalog's three fixed queries per page: products
// plus paging. COUNT and the row query share posProductFilter, the same discipline
// every other list in this codebase follows.
//
// The exact-kode_barang match that sorts first compares against the raw search text,
// not the ILIKE-escaped one: EscapeLike turns a literal "100%" into "100\%", and
// comparing that against a stored kode_barang would never equal it. A scanned or
// typed code has to win the sort on its own literal value.
func (r *ProductRepository) SearchPOS(ctx context.Context, db DBTX, search string, limit, offset int) ([]entity.ProductPOS, int64, error) {
	escaped := EscapeLike(search)

	var total int64
	if err := db.QueryRowContext(
		ctx, `SELECT COUNT(*) `+posProductFrom+posProductFilter, escaped,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count pos product: %w", err)
	}

	if total == 0 {
		return []entity.ProductPOS{}, 0, nil
	}

	// ORDER BY ends in p.id, a unique column, same discipline as every other paged
	// list — without it one row can appear on two pages while another never comes
	// back. The exact-match clause only ever reorders ties at the very top; it
	// changes nothing about that guarantee.
	query := `SELECT ` + posProductColumns + ` ` + posProductFrom + posProductFilter + `
		ORDER BY (lower(p.kode_barang) = lower($2)) DESC, p.nama, p.id
		LIMIT $3 OFFSET $4`

	rows, err := db.QueryContext(ctx, query, escaped, search, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("select pos product: %w", err)
	}
	defer rows.Close()

	list := make([]entity.ProductPOS, 0, limit)

	for rows.Next() {
		var product entity.ProductPOS

		if err := rows.Scan(&product.ID, &product.KodeBarang, &product.Nama); err != nil {
			return nil, 0, fmt.Errorf("scan pos product: %w", err)
		}

		list = append(list, product)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate pos product: %w", err)
	}

	return list, total, nil
}

// FindSatuanHargaBatch is the second of the POS catalog's three fixed queries per
// page: every conversion unit for every product on the page, with whatever price
// version is in force for it on tanggal attached in the same query — one unnest-free
// batch keyed by = ANY($1), following the shape FindRolesByUserIDs, FindFaktorBatch,
// and SaldoBatch already set for "one query for a whole page, not one per row".
//
// LEFT JOIN product_harga_jual, not JOIN: a satuan with no price in force on tanggal
// — including one that has never had a price at all — is exactly the row this
// endpoint still has to surface, with id_harga_jual and harga both nil. Putting the
// date range in the JOIN's ON clause rather than a WHERE is what keeps that row
// rather than silently dropping it back to an inner join.
//
// A product with no rows in the result at all cannot happen: every product carries
// its base unit, guaranteed at Create.
func (r *ProductRepository) FindSatuanHargaBatch(ctx context.Context, db DBTX, productIDs []int64, tanggal time.Time) (map[int64][]entity.ProductPOSSatuan, error) {
	const query = `
		SELECT ps.id_product, ps.id_satuan, s.nama, ps.faktor, ps.is_default_input,
		       h.id, h.harga::TEXT
		FROM product_satuan ps
		JOIN satuan s ON s.id = ps.id_satuan
		LEFT JOIN product_harga_jual h
			ON h.id_product = ps.id_product
			AND h.id_satuan = ps.id_satuan
			AND h.berlaku_dari <= $2
			AND (h.berlaku_sampai IS NULL OR h.berlaku_sampai > $2)
		WHERE ps.id_product = ANY($1)
		ORDER BY ps.id_product, ps.faktor, ps.id`

	rows, err := db.QueryContext(ctx, query, productIDs, tanggal)
	if err != nil {
		return nil, fmt.Errorf("select pos satuan harga batch: %w", err)
	}
	defer rows.Close()

	hasil := make(map[int64][]entity.ProductPOSSatuan, len(productIDs))

	for rows.Next() {
		var idProduct int64
		var satuan entity.ProductPOSSatuan

		if err := rows.Scan(
			&idProduct, &satuan.IDSatuan, &satuan.NamaSatuan, &satuan.Faktor, &satuan.IsDefaultInput,
			&satuan.IDHargaJual, &satuan.Harga,
		); err != nil {
			return nil, fmt.Errorf("scan pos satuan harga batch: %w", err)
		}

		hasil[idProduct] = append(hasil[idProduct], satuan)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pos satuan harga batch: %w", err)
	}

	return hasil, nil
}

func (r *ProductRepository) FindHargaJual(ctx context.Context, db DBTX, productID int64) ([]entity.ProductHargaJual, error) {
	const query = `
		SELECT h.id, h.id_product, h.id_satuan, h.harga::TEXT, h.berlaku_dari,
		       h.berlaku_sampai, h.created_at, h.created_by, s.nama
		FROM product_harga_jual h
		JOIN satuan s ON s.id = h.id_satuan
		WHERE h.id_product = $1
		ORDER BY h.berlaku_dari DESC, h.id DESC`

	rows, err := db.QueryContext(ctx, query, productID)
	if err != nil {
		return nil, fmt.Errorf("select product_harga_jual: %w", err)
	}
	defer rows.Close()

	list := make([]entity.ProductHargaJual, 0, 4)

	for rows.Next() {
		var harga entity.ProductHargaJual

		if err := rows.Scan(
			&harga.ID, &harga.IDProduct, &harga.IDSatuan, &harga.Harga,
			&harga.BerlakuDari, &harga.BerlakuSampai, &harga.CreatedAt, &harga.CreatedBy,
			&harga.NamaSatuan,
		); err != nil {
			return nil, fmt.Errorf("scan product_harga_jual: %w", err)
		}

		list = append(list, harga)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate product_harga_jual: %w", err)
	}

	return list, nil
}
