package repository

import (
	"database/sql"
	"fmt"

	"github.com/joelanford/extensiondb/internal/models"
)

// Repository provides database operations
type Repository struct {
	db *sql.DB
}

// New creates a new repository
func New(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// InsertImageConfig inserts a new image config (idempotent)
func (r *Repository) InsertImageConfig(imageRef string, blob models.JSONB, bundleVersion *string) (*models.ImageConfig, error) {
	query := `
		INSERT INTO image_configs (image_reference, blob, bundle_version)
		VALUES ($1, $2, $3)
		ON CONFLICT (image_reference) DO NOTHING
		RETURNING id, image_reference, blob, bundle_version, created_at`

	var ic models.ImageConfig
	err := r.db.QueryRow(query, imageRef, blob, bundleVersion).Scan(
		&ic.ID, &ic.ImageReference, &ic.Blob, &ic.BundleVersion, &ic.CreatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			// Conflict occurred, fetch the existing record
			return r.GetImageConfigByReference(imageRef)
		}
		return nil, fmt.Errorf("failed to insert image config: %w", err)
	}

	return &ic, nil
}

// UpsertOCPCatalogReference inserts an OCP catalog reference (idempotent)
func (r *Repository) UpsertOCPCatalogReference(imageConfigID, ocpVersion, catalogName string) error {
	query := `
		INSERT INTO ocp_catalog_references (image_config_id, ocp_version, catalog_name, first_seen)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (image_config_id, ocp_version, catalog_name)
		DO NOTHING`

	_, err := r.db.Exec(query, imageConfigID, ocpVersion, catalogName)
	if err != nil {
		return fmt.Errorf("failed to upsert OCP catalog reference: %w", err)
	}

	return nil
}

// GetImageConfigByReference retrieves an image config by its reference
func (r *Repository) GetImageConfigByReference(imageRef string) (*models.ImageConfig, error) {
	query := `
		SELECT id, image_reference, blob, bundle_version, created_at
		FROM image_configs
		WHERE image_reference = $1`

	var ic models.ImageConfig
	err := r.db.QueryRow(query, imageRef).Scan(
		&ic.ID, &ic.ImageReference, &ic.Blob, &ic.BundleVersion, &ic.CreatedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get image config: %w", err)
	}

	return &ic, nil
}

// GetImageConfigs retrieves image configs with optional filtering
func (r *Repository) GetImageConfigs(limit, offset int) ([]models.ImageConfig, error) {
	query := `
		SELECT id, image_reference, blob, bundle_version, created_at
		FROM image_configs
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2`

	rows, err := r.db.Query(query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query image configs: %w", err)
	}
	defer rows.Close()

	var results []models.ImageConfig
	for rows.Next() {
		var ic models.ImageConfig
		err := rows.Scan(
			&ic.ID, &ic.ImageReference, &ic.Blob, &ic.BundleVersion, &ic.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan image config: %w", err)
		}
		results = append(results, ic)
	}

	return results, nil
}

// GetOCPCatalogReferences retrieves OCP catalog references for an image config
func (r *Repository) GetOCPCatalogReferences(imageConfigID string) ([]models.OCPCatalogReference, error) {
	query := `
		SELECT id, image_config_id, ocp_version, catalog_name, first_seen
		FROM ocp_catalog_references
		WHERE image_config_id = $1
		ORDER BY ocp_version, catalog_name`

	rows, err := r.db.Query(query, imageConfigID)
	if err != nil {
		return nil, fmt.Errorf("failed to query OCP catalog references: %w", err)
	}
	defer rows.Close()

	var results []models.OCPCatalogReference
	for rows.Next() {
		var ocr models.OCPCatalogReference
		err := rows.Scan(
			&ocr.ID, &ocr.ImageConfigID, &ocr.OCPVersion, &ocr.CatalogName, &ocr.FirstSeen,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan OCP catalog reference: %w", err)
		}
		results = append(results, ocr)
	}

	return results, nil
}

// GetBundleImages retrieves only bundle images (those with bundle labels)
func (r *Repository) GetBundleImages(limit, offset int) ([]models.ImageConfig, error) {
	query := `
		SELECT id, image_reference, blob, bundle_version, created_at
		FROM image_configs
		WHERE blob->'config'->'Labels' ? 'operators.operatorframework.io.bundle.package.v1'
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2`

	rows, err := r.db.Query(query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query bundle images: %w", err)
	}
	defer rows.Close()

	var results []models.ImageConfig
	for rows.Next() {
		var ic models.ImageConfig
		err := rows.Scan(
			&ic.ID, &ic.ImageReference, &ic.Blob, &ic.BundleVersion, &ic.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan bundle image: %w", err)
		}
		results = append(results, ic)
	}

	return results, nil
}

// GetImageConfigsByBundleVersion retrieves image configs by bundle version
func (r *Repository) GetImageConfigsByBundleVersion(bundleVersion string, limit, offset int) ([]models.ImageConfig, error) {
	query := `
		SELECT id, image_reference, blob, bundle_version, created_at
		FROM image_configs
		WHERE bundle_version = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`

	rows, err := r.db.Query(query, bundleVersion, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query image configs by bundle version: %w", err)
	}
	defer rows.Close()

	var results []models.ImageConfig
	for rows.Next() {
		var ic models.ImageConfig
		err := rows.Scan(
			&ic.ID, &ic.ImageReference, &ic.Blob, &ic.BundleVersion, &ic.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan image config: %w", err)
		}
		results = append(results, ic)
	}

	return results, nil
}

// GetPackageStats returns statistics about packages
func (r *Repository) GetPackageStats() (map[string]interface{}, error) {
	queries := map[string]string{
		"total_images":  `SELECT COUNT(*) FROM image_configs`,
		"bundle_images": `SELECT COUNT(*) FROM image_configs WHERE blob->'config'->'Labels' ? 'operators.operatorframework.io.bundle.package.v1'`,
		"unique_packages": `SELECT COUNT(DISTINCT blob->'config'->'Labels'->>'operators.operatorframework.io.bundle.package.v1') 
							FROM image_configs 
							WHERE blob->'config'->'Labels' ? 'operators.operatorframework.io.bundle.package.v1'`,
		"unique_ocp_versions": `SELECT COUNT(DISTINCT ocp_version) FROM ocp_catalog_references`,
		"unique_catalogs":     `SELECT COUNT(DISTINCT catalog_name) FROM ocp_catalog_references`,
	}

	stats := make(map[string]interface{})

	for key, query := range queries {
		var count int
		err := r.db.QueryRow(query).Scan(&count)
		if err != nil {
			return nil, fmt.Errorf("failed to get %s: %w", key, err)
		}
		stats[key] = count
	}

	return stats, nil
}
