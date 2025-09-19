package main

import (
	"context"
	"fmt"
	"log"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/blang/semver/v4"
	"github.com/joelanford/extensiondb/internal/db"
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

	if err := printGraphForPackage(ctx, pdb, "quay-operator", semver.MustParse("3.4.0"), semver.MustParse("3.99999.99999"), true); err != nil {
		log.Fatalf("Failed to list bundles for package: %v", err)
	}
}

type bundle struct {
	name    string
	version semver.Version
	created time.Time
}

func (b bundle) ID() string {
	return fmt.Sprintf("%s.v%s", b.name, b.version)
}

func printGraphForPackage(ctx context.Context, pdb *db.DB, packageName string, minVersion, maxVersion semver.Version, bestOnly bool) error {
	rows, err := pdb.QueryContext(ctx, `
SELECT p.name, b.version, (b.image ->> 'created')::timestamp AS created FROM bundles AS b JOIN packages AS p ON p.id = b.package_id WHERE p.name = $1 ORDER BY created`, packageName)
	if err != nil {
		return err
	}
	defer rows.Close()

	bundles := []*bundle{}
	edges := map[*bundle][]*bundle{}
	for rows.Next() {
		var b bundle
		if err := rows.Scan(&b.name, &b.version, &b.created); err != nil {
			return err
		}

		if b.version.LT(minVersion) || b.version.GT(maxVersion) {
			continue
		}

		for _, oldB := range bundles {
			if oldB.version.LTE(b.version) && oldB.version.Major == b.version.Major && b.version.Minor-oldB.version.Minor <= 1 {
				if _, ok := edges[oldB]; !ok {
					edges[oldB] = []*bundle{}
				}
				edges[oldB] = append(edges[oldB], &b)
			}
		}
		bundles = append(bundles, &b)
	}

	for from := range edges {
		slices.SortFunc(edges[from], func(a, b *bundle) int {
			return b.version.Compare(a.version)
		})
	}

	fmt.Println("flowchart LR")
	for _, b := range bundles {
		fmt.Printf("  %%%% created: %s\n", b.created.Format(time.RFC3339))
		fmt.Printf("  %s\n\n", b.ID())
	}

	linkCounter := 0
	bestLinks := make([]string, 0, len(edges))

	for from, tos := range edges {
		for i, to := range tos {
			if i == 0 {
				bestLinks = append(bestLinks, strconv.Itoa(linkCounter))
			} else if bestOnly {
				continue
			}
			fmt.Printf("  %s --> %s\n", from.ID(), to.ID())
			linkCounter++
		}
	}

	if !bestOnly {
		fmt.Printf("  linkStyle %s stroke:#3f3,stroke-width:10px\n", strings.Join(bestLinks, ","))
		fmt.Println("  linkStyle default stroke:#bbb,stroke-width:1px;")
	}

	return nil
}
