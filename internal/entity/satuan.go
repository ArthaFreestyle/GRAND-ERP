package entity

import "time"

// Satuan maps the satuan table — unit of measure. Referenced by product_satuan
// and every *_detail table, so rows are retired with is_aktif rather than
// deleted. Entities carry no JSON tags and no framework imports; they never
// leave the usecase layer.
type Satuan struct {
	ID        int64
	Nama      string
	IsAktif   bool
	CreatedAt time.Time
	CreatedBy *int64
	UpdatedAt time.Time
	UpdatedBy *int64
}
