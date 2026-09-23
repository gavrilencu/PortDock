package templates

import (
	_ "embed"
	"encoding/json"
)

//go:embed images.json
var imagesJSON []byte

// CatalogEnv is an environment variable suggested for a catalog image.
// Values may contain ${PASSWORD}, ${SECRET}, ${URL} and ${FQDN} placeholders
// that the UI fills in when the application is created.
type CatalogEnv struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	Secret   bool   `json:"secret,omitempty"`
	Required bool   `json:"required,omitempty"`
	Hint     string `json:"hint,omitempty"`
}

// CatalogImage is a ready-to-deploy Docker image preset.
type CatalogImage struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Image       string       `json:"image"`
	Category    string       `json:"category"`
	Description string       `json:"description"`
	Port        int          `json:"port"`
	Command     string       `json:"command,omitempty"`
	Options     string       `json:"options,omitempty"`
	Env         []CatalogEnv `json:"env,omitempty"`
	Volumes     []string     `json:"volumes,omitempty"`
	Website     string       `json:"website,omitempty"`
	Managed     string       `json:"managed,omitempty"` // engine id when Shipyard can run it as a managed database
}

type Catalog struct {
	Categories []string       `json:"categories"`
	Images     []CatalogImage `json:"images"`
}

var catalog = func() Catalog {
	var c Catalog
	if err := json.Unmarshal(imagesJSON, &c); err != nil {
		panic("templates: invalid images.json: " + err.Error())
	}
	return c
}()

func ImageCatalog() Catalog { return catalog }
