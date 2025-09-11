# ExtensionDB

ExtensionDB is a PostgreSQL database for storing and analyzing Red Hat OpenShift operator catalog data. This project downloads operator index catalogs from various Red Hat registries, parses the catalog data, and stores it in a structured database for analysis and exploration.

## Overview

The database stores information about:
- **Catalogs**: Different operator index catalogs (certified, community, Red Hat, marketplace)
- **Packages**: Operator packages available in the catalogs
- **Bundles**: Specific versions/releases of operator packages
- **Bundle References**: Container image references for bundles
- **Relationships**: Associations between catalogs, packages, and bundles

## Prerequisites

Before setting up the project, ensure you have the following tools installed:

### Required Tools
- **Docker** and **Docker Compose** - for running PostgreSQL
- **Go 1.25.1+** - for building and running the application
- **curl** - for downloading product lifecycle data
- **opm** - OpenShift Package Manager CLI tool
- **skopeo** - for inspecting container images
- **sha256sum** - for generating checksums

### Installing Required Tools

#### Installing OPM (OpenShift Package Manager)
```bash
# Download the latest release from GitHub
curl -LO https://github.com/operator-framework/operator-registry/releases/latest/download/linux-amd64-opm
chmod +x linux-amd64-opm
sudo mv linux-amd64-opm /usr/local/bin/opm
```

#### Installing Skopeo
```bash
# On macOS with Homebrew
brew install skopeo

# On Ubuntu/Debian
sudo apt-get install skopeo

# On RHEL/CentOS/Fedora
sudo dnf install skopeo
```

## Quick Start

### 1. Clone the Repository
```bash
git clone https://github.com/joelanford/extensiondb.git
cd extensiondb
```

### 2. Start the PostgreSQL Database
```bash
# Start PostgreSQL using Makefile target
make db-up
```

The database will be available at `localhost:5432` with:
- **Database**: `extensiondb`
- **Username**: `postgres`
- **Password**: `postgres`

### 3. Prepare Catalog Data
```bash
# Download and prepare operator catalog data
./data/prepare.sh
```

This script will:
- Fetch operator catalog data from multiple Red Hat registries:
  - `registry.redhat.io/redhat/redhat-operator-index`
  - `registry.redhat.io/redhat/community-operator-index`
  - `registry.redhat.io/redhat/certified-operator-index`
  - `registry.redhat.io/redhat/redhat-marketplace-index`
- Download catalogs for OpenShift versions 4.10 through 4.20
- Store catalog data as JSON files in `data/catalogs/`

**Note**: The first run will take significant time (30+ minutes) as it downloads large catalog datasets. Subsequent runs may be faster due to caching based on image digests.

### 4. Build and Run the Application
```bash
# Run database migrations and load catalog data
CATALOGS_DIR=data/catalogs go run ./cmd/main.go
```

## Usage Examples

### Connecting to the Database
```bash
# Connect using psql
PGPASSWORD=postgres psql -h localhost -p 5432 -U postgres -d extensiondb

### Example Queries

#### List all catalogs
```sql
SELECT name, tag FROM catalogs ORDER BY name, tag;
```

#### Find packages in a specific catalog
```sql
SELECT DISTINCT p.name 
FROM packages p
JOIN bundles b ON p.id = b.package_id
JOIN bundle_reference_bundles brb ON b.id = brb.bundle_id
JOIN catalog_bundle_references cbr ON brb.bundle_reference_id = cbr.bundle_reference_id
JOIN catalogs c ON cbr.catalog_id = c.id
WHERE c.name = 'redhat-operator-index' AND c.tag = '4.20';
```

#### Find missing bundles (referenced but not stored)
```sql
PGPASSWORD=postgres psql -h localhost -p 5432 -U postgres -d extensiondb -f examples/missing_bundles.sql
```

#### Find oldest package builds
```sql
PGPASSWORD=postgres psql -h localhost -p 5432 -U postgres -d extensiondb -f examples/oldest_builds.sql
```
