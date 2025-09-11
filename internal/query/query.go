package query

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/joelanford/extensiondb/internal/models"
	"github.com/opencontainers/go-digest"
	"go.podman.io/image/v5/docker/reference"
)

// Query provides database operations
type Query struct {
	db *sql.DB
}

// New creates a new repository
func New(db *sql.DB) *Query {
	return &Query{db: db}
}

func (q Query) GetOrCreateCatalog(ctx context.Context, name, tag string) (*models.Catalog, error) {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("error starting transaction: %w", err)
	}

	row := tx.QueryRowContext(ctx, `INSERT INTO catalogs ("name", "tag") VALUES ($1, $2) ON CONFLICT ("name", "tag") DO NOTHING RETURNING *;`, name, tag)

	catalog, err := catalogFromRow(row)
	if err == nil {
		return catalog, tx.Commit()
	}

	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("error inserting catalog: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("error committing query: %w", err)
	}

	return catalogFromRow(q.db.QueryRowContext(ctx, `SELECT * FROM catalogs WHERE name = $1 AND tag = $2`, name, tag))
}

func catalogFromRow(row *sql.Row) (*models.Catalog, error) {
	var catalog models.Catalog
	if err := row.Scan(&catalog.ID, &catalog.Name, &catalog.Tag, &catalog.CreatedAt); err != nil {
		return nil, err
	}
	return &catalog, nil
}

func (q Query) GetOrCreatePackage(ctx context.Context, name string) (*models.Package, error) {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("error starting transaction: %w", err)
	}

	row := tx.QueryRowContext(ctx, `INSERT INTO packages ("name") VALUES ($1) ON CONFLICT ("name") DO NOTHING RETURNING *;`, name)

	pkg, err := packageFromRow(row)
	if err == nil {
		return pkg, tx.Commit()
	}

	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("error inserting package: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("error committing query: %w", err)
	}

	return packageFromRow(q.db.QueryRowContext(ctx, `SELECT * FROM packages WHERE name = $1`, name))
}

func packageFromRow(row *sql.Row) (*models.Package, error) {
	var pkg models.Package
	if err := row.Scan(&pkg.ID, &pkg.Name, &pkg.CreatedAt); err != nil {
		return nil, err
	}
	return &pkg, nil
}

func (q Query) GetOrCreateBundleReference(ctx context.Context, ref reference.Named) (*models.BundleReference, error) {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("error starting transaction: %w", err)
	}

	var (
		insertRow func() *sql.Row
		selectRow func() *sql.Row
	)
	switch r := ref.(type) {
	case reference.NamedTagged:
		insertRow = func() *sql.Row {
			return tx.QueryRowContext(ctx, `INSERT INTO bundle_references (repo, tag) VALUES ($1, $2) ON CONFLICT (repo, tag) DO NOTHING RETURNING *;`, ref.Name(), r.Tag())
		}
		selectRow = func() *sql.Row {
			return q.db.QueryRowContext(ctx, `SELECT * FROM bundle_references WHERE repo = $1 and tag = $2`, ref.Name(), r.Tag())
		}
	case reference.Canonical:
		insertRow = func() *sql.Row {
			fmt.Println("insert canonical")
			return tx.QueryRowContext(ctx, `INSERT INTO bundle_references (repo, digest) VALUES ($1, $2) ON CONFLICT (repo, digest) DO NOTHING RETURNING *;`, ref.Name(), r.Digest().String())
		}
		selectRow = func() *sql.Row {
			fmt.Println("select canonical")
			return q.db.QueryRowContext(ctx, `SELECT * FROM bundle_references WHERE repo = $1 and digest = $2`, ref.Name(), r.Digest().String())
		}
	}

	br, err := bundleReferenceFromRow(insertRow())
	if err == nil {
		return br, tx.Commit()
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("error committing query: %w", err)
	}

	return bundleReferenceFromRow(selectRow())
}

func (q Query) GetOrCreateCanonicalBundleReference(ctx context.Context, ref reference.Canonical) (*models.BundleReference, error) {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("error starting transaction: %w", err)
	}

	row := tx.QueryRowContext(ctx, `INSERT INTO bundle_references (repo, tag, digest) VALUES ($1, NULL, $2) ON CONFLICT (repo, tag, digest) DO NOTHING RETURNING *;`, ref.Name(), ref.Digest().String())
	br, err := bundleReferenceFromRow(row)
	if err == nil {
		return br, tx.Commit()
	}

	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("error inserting bundle reference: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("error committing query: %w", err)
	}

	return bundleReferenceFromRow(q.db.QueryRowContext(ctx, `SELECT * FROM bundle_references WHERE repo = $1 and digest = $2`, ref.Name(), ref.Digest().String()))
}

func bundleReferenceFromRow(row *sql.Row) (*models.BundleReference, error) {
	var br models.BundleReference
	if err := row.Scan(&br.ID, &br.Repo, &br.Tag, &br.Digest); err != nil {
		return nil, err
	}
	return &br, nil
}

func (q Query) GetBundleByDigest(ctx context.Context, dig digest.Digest) (*models.Bundle, error) {
	row := q.db.QueryRowContext(ctx, `SELECT * FROM bundles WHERE descriptor ->> 'digest' = $1`, dig)
	return bundleFromRow(row)
}

func bundleFromRow(row *sql.Row) (*models.Bundle, error) {
	var b models.Bundle
	if err := row.Scan(
		&b.ID,
		&b.PackageID,
		&b.Descriptor,
		&b.Index,
		&b.Manifest,
		&b.Image,
		&b.Version,
		&b.Release,
		&b.CreatedAt); err != nil {
		return nil, err
	}
	return &b, nil
}

func (q Query) EnsureCatalogBundleReference(ctx context.Context, c *models.Catalog, br *models.BundleReference) error {
	if _, err := q.db.ExecContext(ctx, `INSERT INTO catalog_bundle_references (
		catalog_id, bundle_reference_id
	) VALUES ($1, $2) ON CONFLICT (catalog_id, bundle_reference_id) DO NOTHING;`, c.ID, br.ID); err != nil {
		return fmt.Errorf("error inserting catalog_bundle_reference: %w", err)
	}
	return nil
}

func (q Query) EnsureBundleReferenceBundle(ctx context.Context, b *models.Bundle, br *models.BundleReference) error {
	if _, err := q.db.ExecContext(ctx, `INSERT INTO bundle_reference_bundles (
        	bundle_id, bundle_reference_id
        ) VALUES ($1, $2) ON CONFLICT (bundle_id, bundle_reference_id) DO NOTHING;`, b.ID, br.ID); err != nil {
		return fmt.Errorf("error inserting bundle_reference_bundle: %w", err)
	}
	return nil
}

func (q Query) CreateBundleWithCatalogAndReference(ctx context.Context, b *models.Bundle, c *models.Catalog, br *models.BundleReference) error {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("error starting transaction: %w", err)
	}
	if err := func() error {
		row := tx.QueryRowContext(ctx, `INSERT INTO bundles (
			package_id,  
			descriptor, 
			index, 
			manifest, 
			image, 
			version, 
			release
		) VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING *;`,
			b.PackageID,
			b.Descriptor,
			b.Index,
			b.Manifest,
			b.Image,
			b.Version,
			b.Release)
		updatedBundle, err := rowToBundle(row)
		if err != nil {
			return fmt.Errorf("error inserting bundle: %w", err)
		}
		*b = *updatedBundle

		if _, err := tx.ExecContext(ctx, `INSERT INTO bundle_reference_bundles (
        	bundle_id, bundle_reference_id
        ) VALUES ($1, $2);`, b.ID, br.ID); err != nil {
			return fmt.Errorf("error inserting bundle reference association: %w", err)
		}

		return nil
	}(); err != nil {
		return errors.Join(err, tx.Rollback())
	}
	return tx.Commit()
}

func rowToBundle(row *sql.Row) (*models.Bundle, error) {
	var b models.Bundle
	if err := row.Scan(
		&b.ID,
		&b.PackageID,
		&b.Descriptor,
		&b.Index,
		&b.Manifest,
		&b.Image,
		&b.Version,
		&b.Release,
		&b.CreatedAt); err != nil {
		return nil, err
	}
	return &b, nil
}

func (q Query) GetMissingBundlesInCatalog(ctx context.Context, c *models.Catalog) ([]*models.BundleReference, error) {
	rows, err := q.db.QueryContext(ctx, `
    SELECT 
        t4.*
    FROM catalogs AS t1
    JOIN catalog_bundle_references AS t2
    	ON t1.id = t2.catalog_id
    LEFT JOIN bundle_reference_bundles AS t3
        ON t2.bundle_reference_id = t3.bundle_reference_id
    JOIN bundle_references AS t4
    	ON t2.bundle_reference_id = t4.id
    WHERE t1.id = $1 AND t3.bundle_reference_id IS NULL;`, c.ID)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*models.BundleReference
	for rows.Next() {
		var br models.BundleReference
		if err := rows.Scan(&br.ID, &br.Repo, &br.Tag, &br.Digest, &br.CreatedAt); err != nil {
			return nil, err
		}
		result = append(result, &br)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
