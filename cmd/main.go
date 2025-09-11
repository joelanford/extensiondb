package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/joelanford/extensiondb/internal/db"
	"github.com/joelanford/extensiondb/internal/models"
	"github.com/joelanford/extensiondb/internal/query"
	"github.com/joelanford/extensiondb/internal/registry"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/operator-framework/operator-registry/alpha/declcfg"
	"go.podman.io/image/v5/docker/reference"
	"golang.org/x/sync/errgroup"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	pdb, err := db.NewDB(db.Config{
		Host:     "localhost",
		Port:     5432,
		User:     "postgres",
		Password: "postgres",
		DBName:   "extensiondb",
		SSLMode:  "disable",
	})
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}

	// Run migrations
	if err := pdb.RunMigrations("migrations"); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	q := query.New(pdb.DB)

	catalogNames := []string{
		"redhat-operator-index",
		"certified-operator-index",
	}
	catalogVersions := []string{
		"v4.19",
		"v4.18",
		"v4.17",
		"v4.16",
		"v4.15",
		"v4.14",
		"v4.13",
		"v4.12",
	}

	if err := buildDB(ctx, os.Getenv("CATALOGS_DIR"), q, catalogNames, catalogVersions); err != nil {
		log.Fatal(err)
	}
}

func buildDB(ctx context.Context, catalogsDir string, q *query.Query, catalogNames []string, catalogTags []string) error {
	for _, catalogName := range catalogNames {
		for _, catalogTag := range catalogTags {
			fmt.Printf("Processing catalog %s:%s\n", catalogName, catalogTag)

			c, err := q.GetOrCreateCatalog(ctx, catalogName, catalogTag)
			if err != nil {
				return fmt.Errorf("error creating catalog %s:%s: %w", catalogName, catalogTag, err)
			}

			catalogDir := filepath.Join(catalogsDir, catalogName, strings.TrimPrefix(catalogTag, "v"))

			imageRefChan := make(chan reference.Named)
			imageRefs := make([]reference.Named, 0)
			imageRefWg := sync.WaitGroup{}
			imageRefWg.Go(func() {
				for ref := range imageRefChan {
					imageRefs = append(imageRefs, ref)
				}
			})

			if err := declcfg.WalkMetasFS(ctx, os.DirFS(catalogDir), func(path string, meta *declcfg.Meta, err error) error {
				if err != nil {
					return err
				}
				if meta.Schema != declcfg.SchemaBundle {
					return nil
				}
				var b struct {
					Image string `json:"image"`
				}
				if err := json.Unmarshal(meta.Blob, &b); err != nil {
					return err
				}

				namedRef, err := reference.ParseNamed(b.Image)
				if err != nil {
					return err
				}

				select {
				case <-ctx.Done():
					return ctx.Err()
				case imageRefChan <- namedRef:
				}
				return nil
			}, declcfg.WithConcurrency(16)); err != nil {
				return err
			}
			close(imageRefChan)
			imageRefWg.Wait()

			type logWithTotal struct {
				msg   string
				total int
			}
			messagesChan := make(chan logWithTotal)
			logWg := sync.WaitGroup{}
			logWg.Go(func() {
				i := 0
				for msg := range messagesChan {
					i++
					fmt.Printf("%s: (%d of %d)\n", msg.msg, i, msg.total)
				}
			})

			eg, egCtx := errgroup.WithContext(ctx)
			eg.SetLimit(32)
			for _, imageRef := range imageRefs {
				eg.Go(func() error {
					canonicalRef, ok := imageRef.(reference.Canonical)
					if !ok {
						return fmt.Errorf("image reference is not a canonical reference")
					}

					br, err := q.GetOrCreateCanonicalBundleReference(egCtx, canonicalRef)
					if err != nil {
						return fmt.Errorf("error creating bundle reference %s: %w", imageRef, err)
					}

					if err := q.EnsureCatalogBundleReference(ctx, c, br); err != nil {
						return fmt.Errorf("error ensuring catalog bundle reference %s: %w", imageRef, err)
					}

					if b, err := q.GetBundleByDigest(egCtx, canonicalRef.Digest()); err == nil {
						if err := q.EnsureBundleReferenceBundle(egCtx, b, br); err != nil {
							return fmt.Errorf("error ensuring bundle reference %s: %w", imageRef, err)
						}
						messagesChan <- logWithTotal{msg: fmt.Sprintf("Successfully updated bundle for %q", canonicalRef), total: len(imageRefs)}
						return nil
					} else if !errors.Is(err, sql.ErrNoRows) {
						return fmt.Errorf("error getting bundle: %w", err)
					}

					// Fetch image info from registry using canonical reference
					imageInfo, err := registry.FetchRegistryV1Bundle(egCtx, canonicalRef)
					if err != nil {
						messagesChan <- logWithTotal{msg: fmt.Sprintf("Failed to fetch image info for %v: %v", canonicalRef, err), total: len(imageRefs)}
						return nil
					}

					p, err := q.GetOrCreatePackage(egCtx, imageInfo.PackageName)
					if err != nil {
						return fmt.Errorf("error creating package %s: %w", imageInfo.PackageName, err)
					}

					b := &models.Bundle{
						PackageID:  sql.NullString{String: p.ID, Valid: true},
						Descriptor: models.JSONB[ocispec.Descriptor]{V: &imageInfo.ReferenceDescriptor},
						Index:      models.JSONB[ocispec.Index]{imageInfo.Index},
						Manifest:   models.JSONB[ocispec.Manifest]{&imageInfo.Manifest},
						Image:      models.JSONB[ocispec.Image]{&imageInfo.ImageConfig},
						Version:    imageInfo.CSV.Spec.Version.String(),
					}
					if err := q.CreateBundleWithCatalogAndReference(egCtx, b, c, br); err != nil {
						return fmt.Errorf("error creating bundle: %w", err)
					}
					messagesChan <- logWithTotal{msg: fmt.Sprintf("Successfully created bundle for %q", canonicalRef), total: len(imageRefs)}
					return nil
				})
			}
			if err := eg.Wait(); err != nil {
				return err
			}
			close(messagesChan)
			logWg.Wait()
		}
	}
	return nil
}
