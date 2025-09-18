DROP VIEW IF EXISTS bundle_builds;
DROP VIEW IF EXISTS newest_catalog_digests;

CREATE VIEW newest_catalog_digests AS
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
    id,
    catalog_id,
    digest,
    created_at
FROM
    ranked_digests_cte
WHERE
    rn = 1;

CREATE VIEW bundle_builds AS
SELECT
    c.name AS catalog_name,
    c.tag AS catalog_tag,
    p.name AS package_name,
    b.version,
    (b.descriptor ->> 'mediaType') AS media_type
FROM bundles AS b
JOIN bundle_reference_bundles AS brb
    ON b.id = brb.bundle_id
JOIN catalog_digest_bundle_references AS cdrb
    ON brb.bundle_reference_id = cdrb.bundle_reference_id
JOIN newest_catalog_digests AS cd
    ON cdrb.catalog_digest_id = cd.id
JOIN catalogs AS c
    ON cd.catalog_id = c.id
JOIN packages AS p
    ON p.id = b.package_id;

SELECT
    package_name,
    version,
    media_type,
    catalog_name,
    STRING_AGG(catalog_tag, ', ' ORDER BY catalog_tag) as catalog_tags
FROM bundle_builds WHERE media_type != 'application/vnd.docker.distribution.manifest.v2+json' GROUP BY package_name, version, media_type, catalog_name ORDER BY package_name, version;

DROP VIEW bundle_builds;
DROP VIEW newest_catalog_digests;
