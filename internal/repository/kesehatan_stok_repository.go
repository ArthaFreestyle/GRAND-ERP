package repository

import (
	"context"
	"fmt"
	"time"

	"Arthafreestyle/ERP/internal/entity"
)

// The two reads behind GET /laporan/kesehatan-stok — isu #37. They are methods on
// KartuStokRepository, not a repository of their own: every figure starts from the
// last row of a kartu_stok chain, and the one other table they reach
// (stok_opname) is reached only to find which kartu_stok adjustment rows to sum. This
// file exists only to keep the score's SQL readable as one piece.

// kesehatanSaldoCTE collapses kartu_stok to one row per (product, room) — the last
// one by id, never by date, because that is the order the trigger builds the chain
// in — for every room in scope. Filtering on the room inside the DISTINCT ON is safe
// because the room is part of the key: it removes whole chains, never picks a
// different winner within one.
//
// $1 narrows to one room, $2 scopes to the active unit_kerja; both nil means the
// whole company. Shared by both queries below, so the two can never disagree on which
// balances are in scope.
const kesehatanSaldoCTE = `
	WITH saldo AS (
		SELECT DISTINCT ON (ks.id_barang, ks.id_ruang)
			ks.id_barang, ks.id_ruang, ks.stok_akhir, ks.nilai_akhir
		FROM kartu_stok ks
		JOIN ruang r ON r.id = ks.id_ruang
		WHERE ($1::BIGINT IS NULL OR ks.id_ruang = $1)
		  AND ($2::BIGINT IS NULL OR r.id_unit_kerja = $2)
		ORDER BY ks.id_barang, ks.id_ruang, ks.id DESC
	)`

// KesehatanStokProduk counts the products behind KETERSEDIAAN and CAKUPAN_MINIMUM.
//
// The KETERSEDIAAN population is deliberately NOT the one GET /product/stok-minimum
// uses. That list LEFT JOINs every active product with a minimum and reads a product
// that never moved in scope as a total of 0 — right for a shopping list, where a
// product nobody has stocked yet still needs buying. For a health score it is wrong:
// stok_minimum is one global number per product, so an outlet that simply does not
// carry an item would be marked "habis" for it every day. Here the product must have
// at least one kartu_stok row in scope. Do not "align" the two.
//
// Within that population the thresholds are exactly the stok-minimum list's:
// total <= stok_minimum is not healthy (reaching the reorder point is the signal),
// so every product counted menipis or habis here also appears on that list for the
// same scope.
//
// Only is_aktif products: a retired product needs no reordering and no minimum.
func (r *KartuStokRepository) KesehatanStokProduk(
	ctx context.Context, db DBTX, idRuang, aktifIDUnitKerja *int64,
) (*entity.KesehatanStokProduk, error) {
	query := kesehatanSaldoCTE + `
		SELECT
			COUNT(*) FILTER (WHERE p.stok_minimum > 0),
			COUNT(*) FILTER (WHERE p.stok_minimum > 0 AND t.total_stok > p.stok_minimum),
			COUNT(*) FILTER (WHERE p.stok_minimum > 0 AND t.total_stok > 0 AND t.total_stok <= p.stok_minimum),
			COUNT(*) FILTER (WHERE p.stok_minimum > 0 AND t.total_stok = 0),
			COUNT(*) FILTER (WHERE t.total_stok > 0),
			COUNT(*) FILTER (WHERE t.total_stok > 0 AND p.stok_minimum > 0)
		FROM (
			SELECT id_barang, SUM(stok_akhir) AS total_stok
			FROM saldo
			GROUP BY id_barang
		) t
		JOIN product p ON p.id = t.id_barang
		WHERE p.is_aktif`

	var hasil entity.KesehatanStokProduk

	if err := db.QueryRowContext(ctx, query, idRuang, aktifIDUnitKerja).Scan(
		&hasil.ProdukDinilai, &hasil.Sehat, &hasil.Menipis, &hasil.Habis,
		&hasil.ProdukDipegang, &hasil.ProdukBerminimum,
	); err != nil {
		return nil, fmt.Errorf("select kesehatan stok produk: %w", err)
	}

	return &hasil, nil
}

// KesehatanStokRuang reads, for every room in scope currently holding stock, what
// STOK_MATI and AKURASI_OPNAME need — one statement for the whole scope, never one
// per room.
//
// Stok mati is decided per PRODUCT across the active unit, not per (product, room)
// chain, and that is a deliberate refinement of the issue's wording:
//
//   - Demand ("permintaan") is a PENJUALAN or PEMAKAIAN row anywhere in the unit
//     within the window. Judged per chain, a warehouse that only ever feeds its shop
//     through mutasi would have all its reserve flagged dead while that very product
//     sells every day next door. $1 (one room) therefore narrows which stock is
//     valued, never where demand is looked for.
//   - MUTASI_KELUAR is never demand: moving a box is not using it, and counting it
//     would let dead stock be revived by shuttling it between rooms.
//   - Reversals (id_kartu_stok_asal IS NOT NULL) are never demand. A sale posted 100
//     days ago and cancelled yesterday leaves a PEMBATALAN_TRANSAKSI row dated
//     yesterday (isu #6) — that is not the goods moving, and must not hide them.
//   - When retur_penjualan exists, RETUR_PENJUALAN must stay out of the demand list:
//     goods coming back are the opposite of demand.
//   - SALDO_AWAL (isu #43) is an ARRIVAL and never demand. It is not in the demand list
//     below, and it satisfies the arrival predicate on its own (stok_masuk > 0, not a
//     reversal, not MUTASI_MASUK) — so migrated goods that have not sold in the window
//     are genuinely dead stock. Do not add it to the demand list.
//
// Arrival ("kedatangan") gives goods that only just came in a grace period: stock
// is dead only if the product's FIRST arrival into the unit is older than the
// window. A MUTASI_MASUK counts as an arrival only when its source room belongs to
// another unit — a move inside the unit is the same goods changing shelf, and
// counting it would reset their age with one internal transfer. Under a global
// context ($2 nil) no mutasi is an arrival at all, for the same reason. A product
// with no qualifying arrival row is treated as old, not new.
//
// Stock value is not filtered on product or room is_aktif: a retired room still
// holding goods is the clearest dead stock there is.
//
// The opname half picks the latest POSTED stok_opname on the room whose ts_cutoff is
// inside its own window ($4), and sums:
//
//   - nilai_selisih: nilai_masuk of the SO_SURPLUS rows plus nilai_keluar of the
//     SO_DEFISIT rows its lines point at. Both are non-negative columns, so surplus
//     and deficit add as absolute amounts and never cancel — ten million lost on one
//     shelf and ten million found on another are two errors, not an accurate count.
//   - nilai_dihitung: stok_awal of every line whose stok_so was filled, valued at the
//     harga_pokok_satuan of its own cutoff row. Uncounted lines (stok_so NULL) are out
//     of both sums, the same rule posting applies: not counted is not zero.
//
// DRAFT/DIAJUKAN opnames have posted nothing yet and BATAL ones were reversed, so
// only POSTED is read.
func (r *KartuStokRepository) KesehatanStokRuang(
	ctx context.Context, db DBTX, idRuang, aktifIDUnitKerja *int64, batasStokMati, batasOpname time.Time,
) ([]entity.KesehatanStokRuang, error) {
	query := kesehatanSaldoCTE + `,
		produk_lingkup AS (
			SELECT DISTINCT id_barang FROM saldo WHERE stok_akhir > 0
		),
		permintaan AS (
			SELECT DISTINCT ks.id_barang
			FROM kartu_stok ks
			JOIN ruang r ON r.id = ks.id_ruang
			WHERE ks.id_barang IN (SELECT id_barang FROM produk_lingkup)
			  AND ($2::BIGINT IS NULL OR r.id_unit_kerja = $2)
			  AND ks.jenis_transaksi IN ('PENJUALAN', 'PEMAKAIAN')
			  AND ks.id_kartu_stok_asal IS NULL
			  AND ks.tanggal_transaksi >= $3::TIMESTAMPTZ
		),
		kedatangan AS (
			SELECT ks.id_barang, MIN(ks.tanggal_transaksi) AS pertama
			FROM kartu_stok ks
			JOIN ruang r ON r.id = ks.id_ruang
			LEFT JOIN mutasi mt ON ks.ref_table = 'mutasi' AND mt.id = ks.ref_id_transaksi
			LEFT JOIN ruang asal ON asal.id = mt.id_ruang_asal
			WHERE ks.id_barang IN (SELECT id_barang FROM produk_lingkup)
			  AND ($2::BIGINT IS NULL OR r.id_unit_kerja = $2)
			  AND ks.stok_masuk > 0
			  AND ks.id_kartu_stok_asal IS NULL
			  AND (ks.jenis_transaksi <> 'MUTASI_MASUK'
			       OR ($2::BIGINT IS NOT NULL AND asal.id_unit_kerja <> $2))
			GROUP BY ks.id_barang
		),
		per_ruang AS (
			SELECT s.id_ruang,
				SUM(s.stok_akhir) AS total_stok,
				SUM(s.nilai_akhir) AS nilai_persediaan,
				COALESCE(SUM(s.nilai_akhir) FILTER (
					WHERE s.stok_akhir > 0
					  AND pm.id_barang IS NULL
					  AND (kd.pertama IS NULL OR kd.pertama < $3::TIMESTAMPTZ)
				), 0) AS nilai_stok_mati
			FROM saldo s
			LEFT JOIN permintaan pm ON pm.id_barang = s.id_barang
			LEFT JOIN kedatangan kd ON kd.id_barang = s.id_barang
			GROUP BY s.id_ruang
			HAVING SUM(s.stok_akhir) > 0
		)
		SELECT pr.id_ruang, r.nama_ruang, pr.total_stok,
			pr.nilai_persediaan::TEXT, pr.nilai_stok_mati::TEXT,
			so.idstok_opname, so.nomor, so.ts_cutoff,
			nilai.nilai_selisih::TEXT, nilai.nilai_dihitung::TEXT
		FROM per_ruang pr
		JOIN ruang r ON r.id = pr.id_ruang
		LEFT JOIN LATERAL (
			SELECT o.idstok_opname, o.nomor, o.ts_cutoff
			FROM stok_opname o
			WHERE o.id_ruang = pr.id_ruang
			  AND o.status = 'POSTED'
			  AND o.ts_cutoff >= $4::TIMESTAMPTZ
			ORDER BY o.ts_cutoff DESC, o.idstok_opname DESC
			LIMIT 1
		) so ON TRUE
		LEFT JOIN LATERAL (
			SELECT
				COALESCE(SUM(adj.nilai_masuk + adj.nilai_keluar), 0) AS nilai_selisih,
				COALESCE(SUM(d.stok_awal * cut.harga_pokok_satuan) FILTER (WHERE d.stok_so IS NOT NULL), 0) AS nilai_dihitung
			FROM stok_opname_detail d
			JOIN kartu_stok cut ON cut.id = d.id_kartu_stok_cutoff
			LEFT JOIN kartu_stok adj ON adj.id = d.id_kartu_stok_penyesuaian
			WHERE d.id_stok_opname = so.idstok_opname
		) nilai ON so.idstok_opname IS NOT NULL
		ORDER BY pr.id_ruang`

	rows, err := db.QueryContext(ctx, query, idRuang, aktifIDUnitKerja, batasStokMati, batasOpname)
	if err != nil {
		return nil, fmt.Errorf("select kesehatan stok ruang: %w", err)
	}
	defer rows.Close()

	list := make([]entity.KesehatanStokRuang, 0, 8)

	for rows.Next() {
		var baris entity.KesehatanStokRuang

		if err := rows.Scan(
			&baris.IDRuang, &baris.NamaRuang, &baris.TotalStok,
			&baris.NilaiPersediaan, &baris.NilaiStokMati,
			&baris.IDStokOpname, &baris.NomorOpname, &baris.TsCutoffOpname,
			&baris.NilaiSelisihOpname, &baris.NilaiDihitungOpname,
		); err != nil {
			return nil, fmt.Errorf("scan kesehatan stok ruang: %w", err)
		}

		list = append(list, baris)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate kesehatan stok ruang: %w", err)
	}

	return list, nil
}
