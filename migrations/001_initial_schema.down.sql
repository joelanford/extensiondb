-- Drop indexes
DROP INDEX IF EXISTS idx_bundles_package_id;
DROP INDEX IF EXISTS idx_bundles_descriptor_digest;
DROP INDEX IF EXISTS idx_catalog_bundle_references_bundle_reference_id;
DROP INDEX IF EXISTS idx_bundle_reference_bundles_bundle_reference_id;


-- Drop tables (order matters due to foreign keys)
DROP TABLE IF EXISTS catalog_bundle_references;
DROP TABLE IF EXISTS bundle_reference_bundles;
DROP TABLE IF EXISTS bundle_references;
DROP TABLE IF EXISTS bundles;
DROP TABLE IF EXISTS packages;
DROP TABLE IF EXISTS catalogs;

-- Drop extension
DROP EXTENSION IF EXISTS "uuid-ossp";
