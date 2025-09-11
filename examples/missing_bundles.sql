SELECT 
    t1.name, t1.tag,
    t4.repo || '@' || t4.digest as ref
FROM catalogs AS t1
JOIN catalog_bundle_references AS t2
    ON t1.id = t2.catalog_id
LEFT JOIN bundle_reference_bundles AS t3
    ON t2.bundle_reference_id = t3.bundle_reference_id
JOIN bundle_references AS t4
    ON t2.bundle_reference_id = t4.id
WHERE t3.bundle_id IS NULL;
