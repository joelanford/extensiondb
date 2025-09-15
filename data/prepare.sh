#!/usr/bin/env bash

set -euo pipefail  # Exit on error, undefined vars, pipe failures

# Get the directory where this script is located
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Change to the script directory to ensure all operations happen there
cd "$SCRIPT_DIR"
echo "Working in directory: $SCRIPT_DIR"


# Check required tools
for tool in curl opm skopeo sha256sum; do
    if ! command -v "$tool" &> /dev/null; then
        echo "Error: $tool is not installed or not in PATH"
        exit 1
    fi
done

IMAGES=(registry.redhat.io/redhat/redhat-operator-index registry.redhat.io/redhat/community-operator-index registry.redhat.io/redhat/certified-operator-index registry.redhat.io/redhat/redhat-marketplace-index)
VERSIONS=(v4.19 v4.18 v4.17 v4.16 v4.15 v4.14 v4.13 v4.12)

get_image_digest() {
    local image_ref=$1
    skopeo inspect --raw docker://$image_ref | sha256sum | cut -d' ' -f1
}

is_catalog_current() {
    local catalog_dir="$1"
    local current_digest="$2"
    
    if [[ ! -f "$catalog_dir/.metadata/digest" ]]; then
        return 1  # No digest file, needs update
    fi
    
    local stored_digest
    stored_digest=$(cat "$catalog_dir/.metadata/digest" 2>/dev/null)
    
    if [[ "$stored_digest" == "$current_digest" ]]; then
        return 0  # Up to date
    else
        return 1  # Needs update
    fi    
}

create_digest() {
    local catalog_dir="$1"
    local digest="$2"
    local image_ref="$3"
    
    mkdir -p $catalog_dir/.metadata
    echo -n $digest > $catalog_dir/.metadata/digest
}

create_indexignore() {
    local catalog_dir="$1"
    
    cat > "$catalog_dir/.indexignore" << EOF
# This file is used by the File-Based Catalog (FBC) format
# to allow non-FBC files to be included in an FBC directory
.metadata/
EOF
}

for version in ${VERSIONS[@]}; do
    for image in ${IMAGES[@]}; do
        # Extract catalog name from image (e.g., "redhat-operator-index" from "registry.redhat.io/redhat/redhat-operator-index")
        catalog_name=$(echo $image | sed 's/.*\///g')
        
        # Extract OCP version (remove 'v' prefix)
        ocp_version=$(echo $version | sed 's/^v//')
        
        # Create directory structure: ./catalogs/<catalogName>/<ocpVersion>
        dir_name=catalogs/$catalog_name/$ocp_version
        image_ref=$image:$version

        echo "Ensuring latest $image_ref exists in $dir_name"

        current_digest=$(get_image_digest $image_ref)

        # Create the directory if it doesn't exist
        mkdir -p $dir_name
        create_indexignore $dir_name

        if is_catalog_current $dir_name $current_digest; then
            echo "$image_ref is up to date (digest: $current_digest)"
            continue
        fi

        if ! opm render $image_ref > $dir_name/catalog.json; then
            echo "Failed to download $image_ref"
            exit 1
        fi

        create_digest $dir_name $current_digest $image_ref
        echo "Successfully cached $image_ref (digest: $current_digest)"
    done
done

echo "All downloads completed successfully!"
