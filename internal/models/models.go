package models

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

type Catalog struct {
	ID string

	Name string
	Tag  string

	//JiraFeatureProject   sql.NullString
	//JiraFeatureComponent sql.NullString
	//JiraBugProject       sql.NullString
	//JiraBugComponent     sql.NullString
	//LastAcknowledged     sql.NullTime
	//LastAcknowledgedBy   sql.NullString

	CreatedAt sql.NullTime
}

type CatalogDigest struct {
	ID        string
	CatalogID string

	Digest string

	CreatedAt sql.NullTime
}

type Package struct {
	ID string

	Name string

	//JiraFeatureProject   sql.NullString
	//JiraFeatureComponent sql.NullString
	//JiraBugProject       sql.NullString
	//JiraBugComponent     sql.NullString
	//LastAcknowledged     sql.NullTime
	//LastAcknowledgedBy   sql.NullString

	CreatedAt sql.NullTime
}

type Bundle struct {
	ID        string
	PackageID sql.NullString

	Descriptor JSONB[ocispec.Descriptor]
	Index      JSONB[ocispec.Index]
	Manifest   JSONB[ocispec.Manifest]
	Image      JSONB[ocispec.Image]

	Version string
	Release sql.NullString

	CreatedAt sql.NullTime
}

type BundleReference struct {
	ID string

	Repo   string
	Tag    sql.NullString
	Digest sql.NullString

	CreatedAt sql.NullTime
}

// JSONB represents a PostgreSQL JSONB field
type JSONB[T any] struct {
	V *T
}

// Value implements the driver.Valuer interface for JSONB
func (j JSONB[T]) Value() (driver.Value, error) {
	if j.V == nil {
		return nil, nil
	}
	return json.Marshal(j.V)
}

// Scan implements the sql.Scanner interface for JSONB
func (j *JSONB[T]) Scan(value interface{}) error {
	if value == nil {
		j.V = nil
		return nil
	}

	bytes, ok := value.([]uint8)
	if !ok {
		return fmt.Errorf("cannot scan %T into JSONB", value)
	}

	j.V = new(T)
	return json.Unmarshal(bytes, j.V)
}
