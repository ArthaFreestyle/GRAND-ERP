package usecase

import (
	"time"

	"Arthafreestyle/ERP/internal/entity"
)

// batasShiftWIB is the single minute that separates the two shifts: a tap before
// 17:30 WIB is PAGI, from 17:30 onwards it is MALAM. Expressed as minutes since
// midnight so the comparison is one integer against another.
//
// One boundary, not two windows. The real hours (PAGI 07:00–17:00, MALAM
// 18:00–21:00) leave three gaps — before seven, the 17:00–18:00 break, and after
// nine at night — and every gap is a tap that would have to be refused even
// though the person is genuinely standing there. A single boundary has no gaps:
// every hour maps to exactly one shift, and arriving far too early or far too
// late becomes a question of lateness (a later phase, needing a per-unit schedule
// master) rather than a question of whether attendance may be recorded at all.
//
// Accepted with open eyes: a tap at 03:00 records a very early PAGI, one at 23:00
// a very late MALAM. Both are still recorded, and the correction endpoint exists
// for the ones that are genuinely in the wrong shift.
//
// This boundary moves money — 17:29 is one shift, 17:31 another — which is why it
// is one named constant in one place, decided by the server's clock alone. A
// client inferring the shift from a phone's own clock would put someone in the
// wrong shift every time that clock drifts a few minutes, and a few minutes is
// all it takes right here.
const batasShiftWIB = 17*60 + 30

// simpulkanShift decides which shift a tap belongs to, from the wall clock in
// WIB. Pure and database-free on purpose: this is where a mistake is least
// visible, so it has to be testable without a fixture — the same shape
// hitungPosting and the kesehatan-stok scoring already take.
func simpulkanShift(jam time.Time) string {
	wib := jam.In(zonaWIB)

	if wib.Hour()*60+wib.Minute() < batasShiftWIB {
		return entity.ShiftPagi
	}

	return entity.ShiftMalam
}

// tanggalPresensi truncates a tap to the calendar date it belongs to, in WIB
// rather than in UTC or the server's own zone.
//
// Same trap tanggalHargaJual names: 00:30 WIB on the 15th is 17:30 UTC on the
// 14th, and truncating in UTC would file that attendance under the previous day —
// on precisely the day someone came in just after midnight, where the recap is
// hardest to argue with afterwards.
//
// The result is midnight UTC on that date, which is how every DATE column in this
// codebase is represented in Go.
func tanggalPresensi(jam time.Time) time.Time {
	tahun, bulan, hari := jam.In(zonaWIB).Date()

	return time.Date(tahun, bulan, hari, 0, 0, 0, 0, time.UTC)
}
