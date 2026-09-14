package usecase

import (
	"context"
	"database/sql"
	"time"

	"Arthafreestyle/ERP/internal/model"
	"Arthafreestyle/ERP/internal/model/converter"
	"Arthafreestyle/ERP/internal/repository"

	"github.com/go-playground/validator/v10"
	"github.com/sirupsen/logrus"
)

// LaporanUseCase holds the three cross-cutting reports isu #22 fase 3 asks for —
// nilai persediaan, laba kotor, and rekap pergerakan.
//
// None of the three belongs to an existing module's resource the way RiwayatBeli
// belongs to product or Utang belongs to supplier: a room's inventory value, a
// month's gross margin, and a movement recap are not any one module's data, they are
// a read across several. So this usecase owns no table and no entity of its own — it
// borrows KartuStokRepository and PenjualanRepository exactly the way ProductUseCase
// borrows PembelianRepository for riwayat-beli, and exists only to give these three
// reads a resource to hang their endpoints off.
type LaporanUseCase struct {
	DB                  *sql.DB
	Log                 *logrus.Logger
	Validate            *validator.Validate
	KartuStokRepository *repository.KartuStokRepository
	PenjualanRepository *repository.PenjualanRepository
}

func NewLaporanUseCase(
	db *sql.DB,
	log *logrus.Logger,
	validate *validator.Validate,
	kartuStokRepository *repository.KartuStokRepository,
	penjualanRepository *repository.PenjualanRepository,
) *LaporanUseCase {
	return &LaporanUseCase{
		DB:                  db,
		Log:                 log,
		Validate:            validate,
		KartuStokRepository: kartuStokRepository,
		PenjualanRepository: penjualanRepository,
	}
}

// NilaiPersediaan answers current inventory value, one row per room.
func (c *LaporanUseCase) NilaiPersediaan(ctx context.Context, request *model.ListNilaiPersediaanRequest) ([]model.NilaiPersediaanResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	list, err := c.KartuStokRepository.NilaiPersediaan(ctx, c.DB, request.IDRuang, request.AktifIDUnitKerja)
	if err != nil {
		return nil, err
	}

	return converter.NilaiPersediaanToResponses(list), nil
}

// LabaKotor answers gross margin grouped by month.
func (c *LaporanUseCase) LabaKotor(ctx context.Context, request *model.ListLabaKotorRequest) ([]model.LabaKotorResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	list, err := c.PenjualanRepository.LabaKotor(ctx, c.DB, request.Dari, request.Sampai, request.AktifIDUnitKerja)
	if err != nil {
		return nil, err
	}

	return converter.LabaKotorToResponses(list), nil
}

// Pergerakan answers a movement recap grouped by product, room, and jenis_transaksi.
func (c *LaporanUseCase) Pergerakan(ctx context.Context, request *model.ListPergerakanRequest) ([]model.PergerakanResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	list, err := c.KartuStokRepository.Pergerakan(
		ctx, c.DB, request.Dari, request.Sampai, request.IDRuang, request.IDProduct, request.AktifIDUnitKerja,
	)
	if err != nil {
		return nil, err
	}

	return converter.PergerakanToResponses(list), nil
}

// KesehatanStok answers the stock health score of the active unit_kerja — isu #37.
// Nothing is stored; see kesehatan_stok_skor.go for the arithmetic and
// kesehatan_stok_repository.go for what each component reads.
//
// The two reads run inside one read-only REPEATABLE READ transaction so they share a
// snapshot: a sale committing between them could otherwise count a product as
// "sehat" in one query and value its room's stock from after the sale in the other.
// It is a read transaction, so it takes no lock any posting could wait on.
func (c *LaporanUseCase) KesehatanStok(ctx context.Context, request *model.KesehatanStokRequest) (*model.KesehatanStokResponse, error) {
	if err := c.Validate.Struct(request); err != nil {
		return nil, err
	}

	sekarang := time.Now()

	tx, err := c.DB.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	produk, err := c.KartuStokRepository.KesehatanStokProduk(ctx, tx, request.IDRuang, request.AktifIDUnitKerja)
	if err != nil {
		return nil, err
	}

	ruang, err := c.KartuStokRepository.KesehatanStokRuang(
		ctx, tx, request.IDRuang, request.AktifIDUnitKerja,
		sekarang.AddDate(0, 0, -hariStokMati), sekarang.AddDate(0, 0, -hariAkurasiOpname),
	)
	if err != nil {
		return nil, err
	}

	return susunKesehatanStok(*produk, ruang, request.AktifIDUnitKerja, request.IDRuang, sekarang), nil
}
