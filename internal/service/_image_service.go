package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/blang/semver/v4"
	"github.com/joelanford/extensiondb/internal/models"
	"github.com/joelanford/extensiondb/internal/registry"
	"github.com/joelanford/extensiondb/internal/repository"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	v1alpha1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	"go.podman.io/image/v5/docker/reference"
)

// ImageService provides high-level operations for processing container images
type ImageService struct {
	repo *repository.Repository
}

// NewImageService creates a new image service
func NewImageService(repo *repository.Repository) *ImageService {
	return &ImageService{
		repo: repo,
	}
}

func (s *ImageService) processImageReference(ctx context.Context, canonicalRef reference.Canonical) (*models.ImageConfig, error) {
	// Check if we already have this canonical image
	if existing, err := s.repo.GetImageConfigByDigest(canonicalRef.Digest()); err != nil {
		return nil, fmt.Errorf("failed to check existing image config: %w", err)
	} else if existing != nil {
		return existing, nil // Already processed
	}

	// Fetch image info from registry using canonical reference
	imageInfo, err := registry.FetchRegistryV1ImageInfo(ctx, canonicalRef)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch image info for %s: %w", canonicalRef, err)
	}

	// Convert image config to JSONB
	configBlob, err := s.imageConfigToJSONB(imageInfo.ImageConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to convert image config to JSONB: %w", err)
	}

	// Extract bundle version from CSV
	bundleVersion := s.extractBundleVersionFromCSV(imageInfo.CSV)

	// Store in database using canonical digest-based reference
	imageConfig, err := s.repo.InsertImageConfig(canonicalRef.Digest(), configBlob, bundleVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to store image config: %w", err)
	}

	return imageConfig, nil
}

func (s *ImageService) processImageWithCatalogReference(ctx context.Context, canonicalRef reference.Canonical, ocpVersion, catalogName string) (*models.ImageConfig, error) {
	// Process the image first
	imageConfig, err := s.processImageReference(ctx, canonicalRef)
	if err != nil {
		return nil, err
	}

	// Add the catalog reference
	if err := s.repo.UpsertOCPCatalogReference(imageConfig.ID, ocpVersion, catalogName); err != nil {
		return nil, fmt.Errorf("failed to add catalog reference: %w", err)
	}

	return imageConfig, nil
}

// BatchProcessImages processes multiple image references
func (s *ImageService) BatchProcessImages(ctx context.Context, canonicalRefs []reference.Canonical, ocpVersion, catalogName string) ([]*models.ImageConfig, error) {
	var results []*models.ImageConfig
	var errs []error

	for _, canonicalRef := range canonicalRefs {
		imageConfig, err := s.processImageReference(ctx, canonicalRef)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to process %s: %w", canonicalRef, err))
			continue
		}
		results = append(results, imageConfig)
	}

	if len(errs) > 0 {
		// Return partial results with error summary
		errorMsg := fmt.Sprintf("encountered %d errors during batch processing", len(errs))
		for i, err := range errs {
			if i < 3 { // Show first 3 errors
				errorMsg += fmt.Sprintf("\n  - %v", err)
			} else if i == 3 {
				errorMsg += fmt.Sprintf("\n  - ... and %d more", len(errs)-3)
				break
			}
		}
		return results, errors.New(errorMsg)
	}

	return results, nil
}

// GetBundleInfo retrieves bundle information including CSV details
func (s *ImageService) Get(ctx context.Context, canonicalRef reference.Canonical) (*BundleInfo, error) {
	// Get from database first
	imageConfig, err := s.repo.GetImageConfigByDigest(canonicalRef.Digest())
	if err != nil {
		return nil, fmt.Errorf("failed to get image config: %w", err)
	}

	if imageConfig == nil {
		// Not in database, fetch from registry
		imageConfig, err = s.processImageReference(ctx, canonicalRef)
		if err != nil {
			return nil, err
		}
	}

	// Get catalog references
	catalogRefs, err := s.repo.GetOCPCatalogReferences(imageConfig.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get catalog references: %w", err)
	}

	// Extract bundle information from config blob
	bundleInfo := &BundleInfo{
		ImageConfig:    imageConfig,
		CatalogRefs:    catalogRefs,
		PackageName:    s.extractLabelFromBlob(imageConfig.Blob, "operators.operatorframework.io.bundle.package.v1"),
		DefaultChannel: s.extractLabelFromBlob(imageConfig.Blob, "operators.operatorframework.io.bundle.channel.default.v1"),
		Channels:       s.extractLabelFromBlob(imageConfig.Blob, "operators.operatorframework.io.bundle.channels.v1"),
	}

	return bundleInfo, nil
}

// BundleInfo contains comprehensive bundle information
type BundleInfo struct {
	ImageConfig    *models.ImageConfig          `json:"image_config"`
	CatalogRefs    []models.OCPCatalogReference `json:"catalog_refs"`
	PackageName    *string                      `json:"package_name"`
	DefaultChannel *string                      `json:"default_channel"`
	Channels       *string                      `json:"channels"`
}

// imageConfigToJSONB converts OCI image config to JSONB format
func (s *ImageService) imageConfigToJSONB(config ocispec.Image) (models.JSONB, error) {
	// Convert to JSON bytes first
	jsonBytes, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal image config: %w", err)
	}

	// Convert to map for JSONB storage
	var jsonbMap models.JSONB
	if err := json.Unmarshal(jsonBytes, &jsonbMap); err != nil {
		return nil, fmt.Errorf("failed to unmarshal to JSONB: %w", err)
	}

	return jsonbMap, nil
}

// extractBundleVersionFromCSV extracts version from CSV spec
func (s *ImageService) extractBundleVersionFromCSV(csv v1alpha1.ClusterServiceVersion) semver.Version {
	return csv.Spec.Version.Version
}

// extractLabelFromBlob extracts a label value from the image config blob
func (s *ImageService) extractLabelFromBlob(blob models.JSONB, labelKey string) *string {
	if config, ok := blob["config"].(map[string]interface{}); ok {
		if labels, ok := config["Labels"].(map[string]interface{}); ok {
			if value, ok := labels[labelKey].(string); ok && value != "" {
				return &value
			}
		}
	}
	return nil
}
