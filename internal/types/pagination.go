package types

// Pagination is the normalised page request. Page is 1-based.
type Pagination struct {
	Page     int
	PageSize int
}

const (
	DefaultPageSize = 20
	MaxPageSize     = 100
)

// Normalize clamps the request into the allowed range.
func (p Pagination) Normalize() Pagination {
	if p.Page < 1 {
		p.Page = 1
	}
	if p.PageSize < 1 {
		p.PageSize = DefaultPageSize
	}
	if p.PageSize > MaxPageSize {
		p.PageSize = MaxPageSize
	}
	return p
}

// Offset is the SQL offset for this page.
func (p Pagination) Offset() int { return (p.Page - 1) * p.PageSize }

// Page is a paginated response envelope.
type Page[T any] struct {
	Data     []T   `json:"data"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
	Total    int64 `json:"total"`
}

func NewPage[T any](data []T, p Pagination, total int64) Page[T] {
	if data == nil {
		data = []T{}
	}
	return Page[T]{Data: data, Page: p.Page, PageSize: p.PageSize, Total: total}
}
