package entity

// Ruang maps the ruang table — lokasi penyimpanan yang jadi partisi saldo di
// kartu_stok. Entities carry no JSON tags and no framework imports; they never
// leave the usecase layer.
type Ruang struct {
	ID        int64
	Kode      *string
	NamaRuang string
	IsAktif   bool
}
