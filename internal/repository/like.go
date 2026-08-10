package repository

import "strings"

// likeEscaper menetralkan wildcard LIKE/ILIKE. Backslash harus diganti lebih
// dulu, kalau tidak escape yang kita tambahkan sendiri ikut ter-escape.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// EscapeLike menyiapkan input pengguna untuk dipakai di dalam pola ILIKE.
// Selalu lewatkan input pencarian ke sini sebelum dijadikan argumen query.
//
// Tanpa ini, `%` dan `_` dari pengguna diperlakukan sebagai wildcard: pencarian
// `%` mencocokkan seluruh baris, dan produk bernama `100%` tidak pernah
// ditemukan. Ini soal kebenaran hasil, bukan SQL injection — placeholder $N
// tetap aman dengan atau tanpa helper ini.
func EscapeLike(s string) string {
	return likeEscaper.Replace(s)
}
