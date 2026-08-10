package model

// WebResponse is the single envelope every HTTP response uses. Exactly one of
// Data or Errors is populated.
//
// Data deliberately has no omitempty: an empty list is "empty" to encoding/json,
// so omitempty would drop the key altogether and a client reading
// response.data.length would break on exactly the page that has no rows. It is
// always present, which costs a `"data": null` on error responses.
type WebResponse[T any] struct {
	Data             T                 `json:"data"`
	Paging           *PageMetadata     `json:"paging,omitempty"`
	Errors           string            `json:"errors,omitempty"`
	ValidationErrors map[string]string `json:"validation_errors,omitempty"`
}

// PageMetadata accompanies list endpoints.
type PageMetadata struct {
	Page      int   `json:"page"`
	Size      int   `json:"size"`
	TotalItem int64 `json:"total_item"`
	TotalPage int64 `json:"total_page"`
}

// PageRequest holds the pagination parameters shared by list endpoints.
type PageRequest struct {
	Page int `json:"page" query:"page" validate:"min=1"`
	Size int `json:"size" query:"size" validate:"min=1,max=100"`
}

// Normalize fills in defaults so a caller may omit both parameters.
func (p *PageRequest) Normalize() {
	if p.Page < 1 {
		p.Page = 1
	}
	if p.Size < 1 {
		p.Size = 20
	}
	if p.Size > 100 {
		p.Size = 100
	}
}

// Offset converts the page/size pair into a SQL OFFSET.
func (p *PageRequest) Offset() int {
	return (p.Page - 1) * p.Size
}
