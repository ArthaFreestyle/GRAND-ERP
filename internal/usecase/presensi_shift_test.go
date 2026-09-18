package usecase

// Internal (package usecase), and no database anywhere: the shift boundary is the
// one decision in this module that moves money — 17:29 is one shift and 17:31 is
// another — so it has to be provable without a fixture, the same shape
// hitungPosting and TestTanggalHargaJualMidnightBoundaryWIB already take.

import (
	"testing"
	"time"

	"Arthafreestyle/ERP/internal/entity"
)

// wib builds a wall-clock moment in WIB, which is the only calendar this module
// reasons in.
func wib(tahun int, bulan time.Month, hari, jam, menit int) time.Time {
	return time.Date(tahun, bulan, hari, jam, menit, 0, 0, zonaWIB)
}

func TestSimpulkanShiftDiSekitarBatas(t *testing.T) {
	tests := []struct {
		nama string
		jam  time.Time
		want string
	}{
		{"satu menit sebelum batas", wib(2026, time.March, 17, 17, 29), entity.ShiftPagi},
		{"tepat di batas sudah malam", wib(2026, time.March, 17, 17, 30), entity.ShiftMalam},
		{"satu menit sesudah batas", wib(2026, time.March, 17, 17, 31), entity.ShiftMalam},
		{"awal shift pagi", wib(2026, time.March, 17, 7, 0), entity.ShiftPagi},
		{"awal shift malam", wib(2026, time.March, 17, 18, 0), entity.ShiftMalam},

		// One boundary and no windows, so the hours nobody is scheduled for still
		// map somewhere instead of being refused. A tap at three in the morning is a
		// very early PAGI and a tap at eleven at night a very late MALAM — both
		// recorded, both correctable if the shift is genuinely wrong.
		{"dini hari tetap tercatat", wib(2026, time.March, 17, 3, 0), entity.ShiftPagi},
		{"larut malam tetap tercatat", wib(2026, time.March, 17, 23, 0), entity.ShiftMalam},
	}

	for _, tt := range tests {
		t.Run(tt.nama, func(t *testing.T) {
			if got := simpulkanShift(tt.jam); got != tt.want {
				t.Errorf("simpulkanShift(%s) = %s, want %s", tt.jam.Format(time.RFC3339), got, tt.want)
			}
		})
	}
}

// The same moment expressed in UTC has to answer identically. A server running in
// UTC — every container here does — would otherwise put an evening tap in the
// morning shift, and nothing about the response would look wrong.
func TestSimpulkanShiftSamaDalamZonaLain(t *testing.T) {
	// 17:30 WIB is 10:30 UTC: morning by the clock on the wall of a UTC server,
	// evening by the clock on the wall of the office.
	utc := wib(2026, time.March, 17, 17, 30).UTC()

	if got := simpulkanShift(utc); got != entity.ShiftMalam {
		t.Errorf("simpulkanShift(%s) = %s, want %s", utc.Format(time.RFC3339), got, entity.ShiftMalam)
	}
}

// The midnight trap, the same one TestTanggalHargaJualMidnightBoundaryWIB pins for
// prices: 00:30 WIB on the 15th is 17:30 UTC on the 14th. Truncating in UTC files
// that attendance under the previous day — on precisely the day somebody came in
// just after midnight, where the recap is hardest to argue with afterwards.
func TestTanggalPresensiMidnightBoundaryWIB(t *testing.T) {
	jam := wib(2026, time.March, 15, 0, 30)

	tanggal := tanggalPresensi(jam)

	want := time.Date(2026, time.March, 15, 0, 0, 0, 0, time.UTC)
	if !tanggal.Equal(want) {
		t.Errorf("tanggalPresensi(%s) = %s, want %s",
			jam.Format(time.RFC3339), tanggal.Format(time.RFC3339), want.Format(time.RFC3339))
	}

	// And that tap belongs to the morning shift of the 15th, not to the night shift
	// of the 14th: date and shift are two decisions and both are taken in WIB.
	if got := simpulkanShift(jam); got != entity.ShiftPagi {
		t.Errorf("shift = %s, want %s", got, entity.ShiftPagi)
	}
}
