package usecase

import (
	"context"
	"database/sql"

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
