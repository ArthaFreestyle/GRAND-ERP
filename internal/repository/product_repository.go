package repository

import (
	"context"
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
