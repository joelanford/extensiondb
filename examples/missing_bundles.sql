WITH ranked_digests_cte AS (
    SELECT
        id,
        catalog_id,
        digest,
        created_at,
        ROW_NUMBER() OVER(PARTITION BY catalog_id ORDER BY created_at DESC) AS rn
    FROM
        catalog_digests
)
SELECT 
    c.name, c.tag,
    br.repo || '@' || br.digest as ref
FROM catalogs AS c
JOIN ranked_digests_cte AS cd
    ON c.id = cd.catalog_id
JOIN catalog_digest_bundle_references AS cdbr
    ON cd.id = cdbr.catalog_digest_id
LEFT JOIN bundle_reference_bundles AS brb
    ON cdbr.bundle_reference_id = brb.bundle_reference_id
JOIN bundle_references AS br
    ON cdbr.bundle_reference_id = br.id
WHERE rn=1 AND brb.bundle_id IS NULL
ORDER BY ref;
