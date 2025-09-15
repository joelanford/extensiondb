DROP VIEW IF EXISTS newest_bundle_builds_by_package;
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
    (b.image ->> 'created')::timestamp AS created
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

CREATE VIEW newest_bundle_builds_by_package AS
SELECT
    t1.catalog_name,
    t1.catalog_tag,
    t1.package_name,
    t1.version,
    t1.created
FROM bundle_builds AS t1
INNER JOIN (
    SELECT
        package_name,
        MAX(created) AS newest_created
    FROM bundle_builds
    GROUP BY package_name, catalog_name
) AS t2
ON
    t1.package_name = t2.package_name AND
    t1.created = t2.newest_created;

SELECT package_name, version, created, catalog_name, STRING_AGG(catalog_tag, ', ' ORDER BY catalog_tag) FROM newest_bundle_builds_by_package WHERE created < NOW() - INTERVAL '1 year' GROUP BY package_name, version, created, catalog_name HAVING STRING_AGG(catalog_tag, ', ') ~ '4.19' ORDER BY created;

DROP VIEW newest_bundle_builds_by_package;
DROP VIEW bundle_builds;
DROP VIEW newest_catalog_digests;
