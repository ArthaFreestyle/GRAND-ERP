package usecase

import (
	"context"
	"database/sql"

	"Arthafreestyle/ERP/internal/repository"

	"github.com/sirupsen/logrus"
)

// RekonsiliasiUseCase owns the one worker job isu #25 asks for: a daily check that
// kartu_stok's balance chain is what the trigger should have produced. It has no
// HTTP surface at all — cmd/worker's scheduler is its only caller — so unlike every
// other usecase in this package it borrows no Validate and answers to no request
// DTO, the same reason DokumenUseCase.BersihkanYatim needs neither.
type RekonsiliasiUseCase struct {
	DB                  *sql.DB
	Log                 *logrus.Logger
	KartuStokRepository *repository.KartuStokRepository
}

func NewRekonsiliasiUseCase(
	db *sql.DB,
	log *logrus.Logger,
	kartuStokRepository *repository.KartuStokRepository,
) *RekonsiliasiUseCase {
	return &RekonsiliasiUseCase{
		DB:                  db,
		Log:                 log,
		KartuStokRepository: kartuStokRepository,
	}
}

// PeriksaRantaiSaldo runs KartuStokRepository.PeriksaRantai and logs what it finds.
// It matches worker.Job's Jalankan signature and returns the count of broken chains
// found — zero is the expected, silent answer every day this job runs correctly.
//
// This never repairs anything it finds. A discrepancy in an append-only chain whose
// every balance is trigger-computed means a bug somewhere upstream — the trigger,
// a migration, or something that wrote to kartu_stok directly — and the only
// dishonest response is to make it quiet again from here. If a product genuinely
// needs a correction, the tool for that is stok_opname, not this job.
func (c *RekonsiliasiUseCase) PeriksaRantaiSaldo(ctx context.Context) (int, error) {
	// One connection for the whole job, exactly as BersihkanYatim's comment explains:
	// a session-level advisory lock belongs to the connection that took it, and
	// releasing it from a different pooled connection would silently do nothing.
	conn, err := c.DB.Conn(ctx)
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = conn.Close()
	}()

	locked, err := c.KartuStokRepository.TryLockRekonsiliasi(ctx, conn)
	if err != nil {
		return 0, err
	}

	if !locked {
		// Another worker is already reconciling. Nothing to do and nothing wrong.
		c.Log.Debug("rekonsiliasi kartu_stok: worker lain sedang berjalan")

		return 0, nil
	}
	defer func() {
		if err := c.KartuStokRepository.UnlockRekonsiliasi(ctx, conn); err != nil {
			c.Log.WithError(err).Error("rekonsiliasi kartu_stok: gagal melepas advisory lock")
		}
	}()

	selisih, err := c.KartuStokRepository.PeriksaRantai(ctx, conn)
	if err != nil {
		return 0, err
	}

	if len(selisih) == 0 {
		c.Log.Info("rekonsiliasi kartu_stok: rantai saldo konsisten")

		return 0, nil
	}

	// Every broken chain gets its own log line, not just the total — a count alone
	// would tell an operator something is wrong without telling them what to open.
	for _, s := range selisih {
		c.Log.WithFields(logrus.Fields{
			"id_barang": s.IDBarang,
			"id_ruang":  s.IDRuang,
			"id":        s.ID,
		}).Error("rekonsiliasi kartu_stok: rantai saldo menyimpang dari perhitungan trigger")
	}

	return len(selisih), nil
}
