-- Create extension for UUID generation
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TABLE catalogs (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    "name" TEXT NOT NULL,
    tag TEXT NOT NULL,
    digest TEXT NOT NULL,

    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),

    UNIQUE ("name", tag),

    CONSTRAINT valid_digest CHECK (
        digest ~ '^sha256:[a-f0-9]{64}$'
    )
);

CREATE TABLE packages (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    "name" TEXT NOT NULL UNIQUE,

    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),

    CONSTRAINT packages_name_dns_1123_subdomain CHECK (
        "name" ~ '^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$'
        AND LENGTH("name") >= 1
        AND LENGTH("name") <= 253
    )
);

CREATE TABLE bundles (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    package_id UUID NOT NULL REFERENCES packages(id) ON DELETE CASCADE,

    descriptor JSONB UNIQUE NOT NULL,
    index JSONB,
    manifest JSONB NOT NULL,
    image JSONB NOT NULL,

    version TEXT NOT NULL,
    release TEXT,

    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),

    CONSTRAINT bundles_version_semver CHECK (
        version ~ '^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?(?:\+([0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$'
        AND LENGTH(version) >= 5
        AND LENGTH(version) <= 255
    )
);
CREATE INDEX idx_bundles_package_id ON bundles (package_id);
CREATE INDEX idx_bundles_descriptor_digest ON bundles (((descriptor ->> 'digest')));

CREATE TABLE bundle_references (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),

    repo TEXT,
    tag TEXT,
    digest TEXT,

    CONSTRAINT bundle_reference_unique UNIQUE NULLS NOT DISTINCT (repo, tag, digest),

    CONSTRAINT bundle_reference_tag_or_digest CHECK (
        (tag IS NULL AND digest IS NOT NULL) OR
        (tag IS NOT NULL AND digest IS NULL)
    ),

    CONSTRAINT bundle_reference_digest CHECK (
        digest IS NULL OR
        digest ~ '^sha256:[a-f0-9]{64}$'
    )
);

CREATE TABLE catalog_digests (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    catalog_id UUID REFERENCES catalogs(id),
    digest TEXT NOT NULL,

    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),

    UNIQUE (catalog_id, digest),

    CONSTRAINT valid_digest CHECK (
        digest ~ '^sha256:[a-f0-9]{64}$'
    )
);

CREATE TABLE catalog_digest_bundle_references (
    catalog_digest_id UUID REFERENCES catalog_digests(id) ON DELETE CASCADE,
    bundle_reference_id UUID REFERENCES bundle_references(id) ON DELETE CASCADE,

    CONSTRAINT catalog_digest_bundle_references_pkey PRIMARY KEY (catalog_digest_id, bundle_reference_id)  -- explicit pk
);
CREATE INDEX idx_catalog_digest_bundle_references_bundle_reference_id ON catalog_digest_bundle_references (bundle_reference_id);

CREATE TABLE bundle_reference_bundles (
    bundle_id UUID REFERENCES bundles(id) ON DELETE CASCADE,
    bundle_reference_id UUID REFERENCES bundle_references(id) ON DELETE CASCADE,

    CONSTRAINT bundle_reference_bundles_pkey PRIMARY KEY (bundle_id, bundle_reference_id)  -- explicit pk
);
CREATE INDEX idx_bundle_reference_bundles_bundle_reference_id ON bundle_reference_bundles (bundle_reference_id);
