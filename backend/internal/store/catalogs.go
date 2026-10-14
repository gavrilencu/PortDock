package store

// TemplateCatalog is a catalog of service templates, downloaded from a URL
// or uploaded (catalogs package).
type TemplateCatalog struct {
	ID   int64  `db:"id" json:"id"`
	Name string `db:"name" json:"name"` // first part of its templates' keys
	URL  string `db:"url" json:"url"`
	// Format is auto, portdock or coolify.
	Format  string `db:"format" json:"format"`
	Enabled bool   `db:"enabled" json:"enabled"`
	// Content is the uploaded document of a catalog without a URL.
	Content string `db:"content" json:"-"`
	// Templates is the JSON of the parsed catalog (catalogs.Parsed).
	Templates  string `db:"templates" json:"-"`
	Count      int    `db:"count" json:"count"`
	FetchedAt  string `db:"fetched_at" json:"fetched_at"`
	FetchError string `db:"fetch_error" json:"fetch_error"`
	CreatedAt  string `db:"created_at" json:"created_at"`
}
