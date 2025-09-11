DROP VIEW IF EXISTS newest_bundle_builds_by_package;
DROP VIEW IF EXISTS bundle_builds;

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
JOIN catalog_bundle_references AS crb
    ON brb.bundle_reference_id = crb.bundle_reference_id
JOIN catalogs AS c
    ON crb.catalog_id = c.id
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
