package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"Arthafreestyle/ERP/internal/entity"

	"google.golang.org/genai"
)

// GeminiFakturReader is the FakturReader Gemini backs.
//
// It holds a *genai.Client built once in config.Bootstrap and injected — never one
// created per request, the same discipline every other outbound client in this
// project follows. Model is a plain field rather than baked into the client because
// the model name is a config key of its own (gemini.model), changeable without a
// second client.
type GeminiFakturReader struct {
	Client *genai.Client
	Model  string
}

func NewGeminiFakturReader(client *genai.Client, model string) *GeminiFakturReader {
	return &GeminiFakturReader{Client: client, Model: model}
}

func (g *GeminiFakturReader) Baca(
	ctx context.Context, gambar []byte, mime string, jenis JenisOCRFaktur, katalog []entity.Product,
) (*FakturReaderHasil, error) {
	config := &genai.GenerateContentConfig{
		SystemInstruction: genai.NewContentFromParts(
			[]*genai.Part{genai.NewPartFromText(sistemPromptOCR(jenis, katalog))}, genai.RoleUser,
		),
		// Structured output, never free text parsed by hand — the schema is what
		// keeps every number a STRING (fase 1's decision) so a JSON float never gets
		// a chance to round money before it reaches big.Rat.
		ResponseMIMEType: "application/json",
		ResponseSchema:   skemaOCRFaktur,
	}

	contents := []*genai.Content{genai.NewContentFromParts(
		[]*genai.Part{genai.NewPartFromBytes(gambar, mime)}, genai.RoleUser,
	)}

	resp, err := g.Client.Models.GenerateContent(ctx, g.Model, contents, config)
	if err != nil {
		var apiErr genai.APIError
		if errors.As(err, &apiErr) && apiErr.Code == http.StatusNotFound {
			// A model Google has retired answers this way with no other warning —
			// observed against gemini-2.5-flash while this issue was written. Wrapped
			// so the usecase can log gemini.model's value rather than a bare 500.
			return nil, fmt.Errorf("%w: %s (%s)", ErrModelGeminiTidakDitemukan, g.Model, apiErr.Message)
		}

		return nil, fmt.Errorf("gemini: generate content: %w", err)
	}

	var keluaran keluaranOCRFaktur
	if err := json.Unmarshal([]byte(resp.Text()), &keluaran); err != nil {
		return nil, fmt.Errorf("gemini: respons bukan JSON sesuai skema: %w", err)
	}

	hasil := keluaran.keFakturReaderHasil()
	hasil.Model = g.Model

	if resp.UsageMetadata != nil {
		hasil.PromptTokenCount = resp.UsageMetadata.PromptTokenCount
		hasil.CandidatesTokenCount = resp.UsageMetadata.CandidatesTokenCount
	}

	return hasil, nil
}

// keluaranOCRFaktur mirrors skemaOCRFaktur field for field. Kept as its own type,
// distinct from FakturReaderHasil, so the JSON tags Gemini's schema is built from
// stay local to this file and never leak into the interface the rest of the
// application programs against.
type keluaranOCRFaktur struct {
	NoFaktur      *string            `json:"no_faktur"`
	TanggalFaktur *string            `json:"tanggal_faktur"`
	NamaSupplier  *string            `json:"nama_supplier"`
	DiskonNota    *string            `json:"diskon_nota"`
	PPN           *string            `json:"ppn"`
	Total         *string            `json:"total"`
	TandaLunas    *string            `json:"tanda_lunas"`
	Baris         []keluaranBarisOCR `json:"baris"`
}

type keluaranBarisOCR struct {
	Urutan           int64   `json:"urutan"`
	TeksAsli         string  `json:"teks_asli"`
	KodeVendor       *string `json:"kode_vendor"`
	IDProduct        *int64  `json:"id_product"`
	SatuanTerbaca    *string `json:"satuan_terbaca"`
	IDSatuan         *int64  `json:"id_satuan"`
	Qty              *string `json:"qty"`
	HargaSatuan      *string `json:"harga_satuan"`
	DiskonBaris      *string `json:"diskon_baris"`
	Dicentang        *bool   `json:"dicentang"`
	QtyTulisanTangan *string `json:"qty_tulisan_tangan"`
}

func (k keluaranOCRFaktur) keFakturReaderHasil() *FakturReaderHasil {
	baris := make([]FakturReaderBaris, len(k.Baris))
	for i, b := range k.Baris {
		baris[i] = FakturReaderBaris{
			Urutan:           b.Urutan,
			TeksAsli:         b.TeksAsli,
			KodeVendor:       b.KodeVendor,
			IDProduct:        b.IDProduct,
			SatuanTerbaca:    b.SatuanTerbaca,
			IDSatuan:         b.IDSatuan,
			Qty:              b.Qty,
			HargaSatuan:      b.HargaSatuan,
			DiskonBaris:      b.DiskonBaris,
			Dicentang:        b.Dicentang,
			QtyTulisanTangan: b.QtyTulisanTangan,
		}
	}

	return &FakturReaderHasil{
		NoFaktur:      k.NoFaktur,
		TanggalFaktur: k.TanggalFaktur,
		NamaSupplier:  k.NamaSupplier,
		DiskonNota:    k.DiskonNota,
		PPN:           k.PPN,
		Total:         k.Total,
		TandaLunas:    k.TandaLunas,
		Baris:         baris,
	}
}

// skemaOCRFaktur is the ResponseSchema handed to Gemini, built once at package init
// rather than per call — it never depends on anything but the request's fixed shape.
//
// Every number is TypeString, never TypeNumber: encoding/json would decode a JSON
// number through float64, rounding money before it ever reaches parseNumeric's
// big.Rat. Indonesian-formatted figures ("1.254.000,00") are normalized in Go by
// parseAngkaIndonesia, not trusted to the model.
var skemaOCRFaktur = &genai.Schema{
	Type: genai.TypeObject,
	Properties: map[string]*genai.Schema{
		"no_faktur":      {Type: genai.TypeString, Nullable: ptrBool(true)},
		"tanggal_faktur": {Type: genai.TypeString, Nullable: ptrBool(true), Description: "Tanggal pada dokumen, format bebas apa adanya yang tertulis."},
		"nama_supplier":  {Type: genai.TypeString, Nullable: ptrBool(true)},
		"diskon_nota":    {Type: genai.TypeString, Nullable: ptrBool(true), Description: "Rupiah bilangan bulat, hanya digit. Titik di kertas adalah pemisah ribuan: 5.000 ditulis 5000."},
		"ppn":            {Type: genai.TypeString, Nullable: ptrBool(true), Description: "Rupiah bilangan bulat, hanya digit. Titik di kertas adalah pemisah ribuan: 5.000 ditulis 5000."},
		"total":          {Type: genai.TypeString, Nullable: ptrBool(true), Description: "Rupiah bilangan bulat, hanya digit. Titik di kertas adalah pemisah ribuan: 5.000 ditulis 5000."},
		"tanda_lunas":    {Type: genai.TypeString, Nullable: ptrBool(true), Description: "Cap atau tulisan seperti LUNAS/TUNAI/KREDIT jika ada, apa adanya."},
		"baris": {
			Type: genai.TypeArray,
			Items: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"urutan":             {Type: genai.TypeInteger, Description: "Nomor urut baris pada dokumen, mulai dari 1."},
					"teks_asli":          {Type: genai.TypeString, Description: "Teks nama barang persis seperti tertulis di dokumen."},
					"kode_vendor":        {Type: genai.TypeString, Nullable: ptrBool(true)},
					"id_product":         {Type: genai.TypeInteger, Nullable: ptrBool(true), Description: "id dari KATALOG PRODUK, atau null bila tidak yakin."},
					"satuan_terbaca":     {Type: genai.TypeString, Nullable: ptrBool(true)},
					"id_satuan":          {Type: genai.TypeInteger, Nullable: ptrBool(true), Description: "id_satuan dari KATALOG PRODUK untuk id_product ini, atau null bila tidak yakin."},
					"qty":                {Type: genai.TypeString, Description: "Angka apa adanya seperti tertulis, boleh format Indonesia."},
					"harga_satuan":       {Type: genai.TypeString, Description: "Rupiah bilangan bulat, hanya digit. Titik di kertas adalah pemisah ribuan: 5.000 ditulis 5000."},
					"diskon_baris":       {Type: genai.TypeString, Nullable: ptrBool(true), Description: "Rupiah bilangan bulat, hanya digit. Titik di kertas adalah pemisah ribuan: 5.000 ditulis 5000."},
					"dicentang":          {Type: genai.TypeBoolean, Nullable: ptrBool(true), Description: "Hanya untuk faktur kedatangan: true jika ada tanda centang, false jika tidak ada, null jika tidak jelas terbaca."},
					"qty_tulisan_tangan": {Type: genai.TypeString, Nullable: ptrBool(true), Description: "Angka tulisan tangan di baris itu, bila ada."},
				},
				Required: []string{"urutan", "teks_asli", "qty", "harga_satuan"},
				PropertyOrdering: []string{
					"urutan", "teks_asli", "kode_vendor", "id_product", "satuan_terbaca", "id_satuan",
					"qty", "harga_satuan", "diskon_baris", "dicentang", "qty_tulisan_tangan",
				},
			},
		},
	},
	Required: []string{"baris"},
	PropertyOrdering: []string{
		"no_faktur", "tanggal_faktur", "nama_supplier", "diskon_nota", "ppn", "total", "tanda_lunas", "baris",
	},
}

// sistemPromptOCR builds the instruction Gemini receives as SystemInstruction: the
// task, the mapping rule, the centang convention (or its absence), and the active
// product catalog as a plain table.
//
// The catalog is the whole point of isu #39's design: every vendor spells the same
// product differently ("AQUA 600ML KRT" vs "AMDK-0600-24"), so Gemini is told, in
// plain words, to answer with an id_product from this list or null — never a guess.
func sistemPromptOCR(jenis JenisOCRFaktur, katalog []entity.Product) string {
	var sb strings.Builder

	sb.WriteString("Anda adalah asisten OCR untuk faktur/nota pembelian toko retail di Indonesia.\n")
	sb.WriteString("Baca foto yang diberikan dan jawab HANYA dengan data terstruktur sesuai skema JSON yang diminta, tanpa teks lain di luar skema.\n\n")

	sb.WriteString("ATURAN PEMETAAN PRODUK (WAJIB):\n")
	sb.WriteString("- Setiap baris HARUS dipetakan ke id_product dari KATALOG PRODUK di bawah, bukan ditebak dari nama atau kode vendor pada foto.\n")
	sb.WriteString("- Vendor sering memakai nama/kode barangnya sendiri yang berbeda dari katalog kami; cocokkan berdasarkan arti barangnya, bukan kemiripan teks.\n")
	sb.WriteString("- Jika tidak yakin baris itu produk yang mana di katalog, isi id_product dengan null. null lebih baik daripada tebakan yang salah.\n")
	sb.WriteString("- id_satuan juga harus salah satu satuan yang terdaftar untuk id_product itu di katalog. Jika tidak yakin, isi null.\n\n")

	sb.WriteString("ATURAN ANGKA UANG (WAJIB):\n")
	sb.WriteString("- Harga, total, diskon, dan PPN adalah rupiah bilangan bulat. Titik pada angka uang adalah pemisah ribuan, BUKAN desimal: \"5.000\" berarti lima ribu, tulis 5000 (bukan 5.0 atau 5).\n")
	sb.WriteString("- Tulis angka uang hanya sebagai digit tanpa titik, koma, spasi, atau simbol Rp. Contoh: \"Rp 1.254.000\" ditulis 1254000; \"12.500\" ditulis 12500.\n")
	sb.WriteString("- Jangan menambahkan desimal (seperti \",00\" atau \".00\") pada angka uang. Ini tidak berlaku untuk qty, yang boleh berdesimal bila memang tertulis begitu.\n\n")

	switch jenis {
	case JenisOCRFakturKedatangan:
		sb.WriteString("KONTEKS DOKUMEN: FAKTUR KEDATANGAN BARANG, yang sudah dicentang oleh petugas gudang saat mencocokkan fisik barang dengan fakturnya.\n")
		sb.WriteString("- Baris yang ada tanda centang/checklist jelas di sebelahnya: dicentang = true.\n")
		sb.WriteString("- Baris tanpa tanda centang sama sekali: dicentang = false. Jika ada angka tulisan tangan di baris itu yang menunjukkan jumlah yang benar-benar datang, isi qty_tulisan_tangan dengan angka itu; kalau tidak ada, biarkan null.\n")
		sb.WriteString("- Baris yang tanda centangnya ada tapi meragukan atau tidak jelas terbaca: dicentang = null.\n\n")
	case JenisOCRNota:
		sb.WriteString("KONTEKS DOKUMEN: NOTA PEMBELIAN dari supplier (misalnya pembelian langsung di toko, barang dibawa pulang saat itu juga). Dokumen ini tidak punya konvensi centang. Abaikan tanda apa pun yang terlihat dan biarkan field dicentang selalu null untuk setiap baris.\n")
		sb.WriteString("- tanda_lunas: laporkan cap atau tulisan seperti LUNAS atau TUNAI persis apa adanya jika ada; jika tidak ada tanda sama sekali, biarkan null. Jangan menebak.\n\n")
	}

	sb.WriteString("KATALOG PRODUK AKTIF (id | kode_barang | nama | satuan tersedia sebagai id:nama:faktor):\n")

	for _, p := range katalog {
		satuan := make([]string, 0, len(p.Satuan))
		for _, s := range p.Satuan {
			satuan = append(satuan, fmt.Sprintf("%d:%s:%d", s.IDSatuan, s.NamaSatuan, s.Faktor))
		}

		fmt.Fprintf(&sb, "%d | %s | %s | %s\n", p.ID, p.KodeBarang, p.Nama, strings.Join(satuan, ", "))
	}

	return sb.String()
}

func ptrBool(b bool) *bool {
	return &b
}

var _ FakturReader = (*GeminiFakturReader)(nil)
