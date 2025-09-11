package registry

import (
	"archive/tar"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/containerd/containerd/archive"
	"github.com/containers/image/v5/manifest"
	"github.com/joelanford/imageutil/remote"
	v1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"github.com/operator-framework/operator-registry/pkg/lib/bundle"
	"go.podman.io/image/v5/docker/reference"
	"go.podman.io/image/v5/pkg/compression"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"sigs.k8s.io/yaml"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// RegistryV1ImageInfo contains the resolved image information
type RegistryV1ImageInfo struct {
	Reference           reference.Canonical            // Canonical digest-based reference
	ReferenceDescriptor ocispec.Descriptor             // Descriptor resolved from the reference
	Index               *ocispec.Index                 // The index (if the reference pointed to an index instead of a manifest)
	Manifest            ocispec.Manifest               // Image manifest
	ImageConfig         ocispec.Image                  // Image config blob as JSON
	PackageName         string                         // Package name
	CSV                 v1alpha1.ClusterServiceVersion // CSV
}

// FetchRegistryV1Bundle fetches manifest and config for a canonical image reference
func FetchRegistryV1Bundle(ctx context.Context, canonicalRef reference.Canonical) (*RegistryV1ImageInfo, error) {
	// Create repository from canonical reference
	repo, err := remote.NewRepository(ctx, nil, canonicalRef.String())
	if err != nil {
		return nil, fmt.Errorf("failed to create repository for %s: %w", canonicalRef, err)
	}

	refDesc, err := repo.Resolve(ctx, canonicalRef.Digest().String())
	if err != nil {
		return nil, fmt.Errorf("failed to get descriptor for canonical reference %s: %w", canonicalRef, err)
	}

	// Fetch the ref blob
	refDesc, refBytes, err := oras.FetchBytes(ctx, repo, refDesc.Digest.String(), oras.FetchBytesOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch manifest for %s: %w", canonicalRef, err)
	}

	var (
		imageIndex    *ocispec.Index
		imageManifest ocispec.Manifest
	)
	switch refDesc.MediaType {
	case ocispec.MediaTypeImageManifest, manifest.DockerV2Schema2MediaType:
		if err := json.Unmarshal(refBytes, &imageManifest); err != nil {
			return nil, fmt.Errorf("failed to unmarshal manifest for %s: %w", canonicalRef, err)
		}
	case ocispec.MediaTypeImageIndex, manifest.DockerV2ListMediaType:
		imageIndex = &ocispec.Index{}
		if err := json.Unmarshal(refBytes, &imageIndex); err != nil {
			return nil, fmt.Errorf("failed to unmarshal index for %s: %w", canonicalRef, err)
		}

		_, manifestBytes, err := oras.FetchBytes(ctx, repo, imageIndex.Manifests[0].Digest.String(), oras.FetchBytesOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to fetch manifest for %s: %w", canonicalRef, err)
		}
		if err := json.Unmarshal(manifestBytes, &imageManifest); err != nil {
			return nil, fmt.Errorf("failed to unmarshal manifest for %s: %w", canonicalRef, err)
		}
	}

	// Fetch the config blob
	configReader, err := repo.Fetch(ctx, imageManifest.Config)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch config for %s: %w", canonicalRef, err)
	}
	defer configReader.Close()
	configVerifyReader := content.NewVerifyReader(configReader, imageManifest.Config)
	configBytes, err := io.ReadAll(configVerifyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to read config for %s: %w", canonicalRef, err)
	}

	var config ocispec.Image
	if err := json.Unmarshal(configBytes, &config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config blob for %s: %w", canonicalRef, err)
	}

	// Extract CSV from layers
	csv, err := extractClusterServiceVersion(ctx, repo, imageManifest)
	if err != nil {
		return nil, fmt.Errorf("failed to extract cluster service version for %s: %w", canonicalRef, err)
	}

	return &RegistryV1ImageInfo{
		Reference:           canonicalRef,
		ReferenceDescriptor: refDesc,
		Index:               imageIndex,
		Manifest:            imageManifest,
		ImageConfig:         config,
		PackageName:         config.Config.Labels[bundle.PackageLabel],
		CSV:                 *csv,
	}, nil
}

// extractBundleVersion attempts to extract the bundle version from various label sources
func extractClusterServiceVersion(ctx context.Context, repo *remote.Repository, manifest ocispec.Manifest) (*v1alpha1.ClusterServiceVersion, error) {
	tmpDir, err := os.MkdirTemp("", "extensiondb-bundle-extract-")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	for _, layer := range manifest.Layers {
		if err := func() error {
			layerReader, err := repo.Fetch(ctx, layer)
			if err != nil {
				return fmt.Errorf("failed to fetch layer for %s: %w", layer.Digest.String(), err)
			}
			defer layerReader.Close()

			decompressedReader, _, err := compression.AutoDecompress(layerReader)
			if err != nil {
				return fmt.Errorf("failed to decompress layer: %w", err)
			}
			defer decompressedReader.Close()

			matchesCSVFileName := func(name string) (bool, error) {
				for _, pattern := range []string{
					"manifests/*clusterserviceversion*",
					"manifests/*csv*",
				} {
					matched, err := filepath.Match(pattern, name)
					if err != nil {
						return false, err
					}
					if matched {
						return true, nil
					}
				}
				return false, nil
			}

			_, err = archive.Apply(ctx, tmpDir, decompressedReader, archive.WithFilter(func(h *tar.Header) (bool, error) {
				name := filepath.Clean(h.Name)
				isCSV, err := matchesCSVFileName(name)
				if err != nil {
					return false, err
				}
				if isCSV {
					h.Name = "./csv.yaml"
				} else {
					return false, nil
				}

				h.Uid = os.Getuid()
				h.Gid = os.Getgid()
				h.Mode = 0600
				if h.Typeflag == tar.TypeDir {
					h.Mode = 0700
				}
				h.PAXRecords = nil
				h.Xattrs = nil
				return true, nil
			}))
			return err
		}(); err != nil {
			return nil, err
		}
	}

	csvPath := filepath.Join(tmpDir, "csv.yaml")
	csvBytes, err := os.ReadFile(csvPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read CSV file: %w", err)
	}

	var csv v1alpha1.ClusterServiceVersion
	if err := yaml.Unmarshal(csvBytes, &csv); err != nil {
		return nil, fmt.Errorf("failed to unmarshal CSV file: %w", err)
	}

	return &csv, nil
}
