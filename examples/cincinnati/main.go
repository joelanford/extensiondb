package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	_ "crypto/sha256"

	"github.com/blang/semver/v4"
	"github.com/joelanford/extensiondb/examples/cincinnati/pkg/graph"
	"github.com/joelanford/extensiondb/examples/cincinnati/pkg/viz"
	"github.com/joelanford/extensiondb/internal/db"
	"go.podman.io/image/v5/docker/reference"
	ggraph "gonum.org/v1/gonum/graph"
	"sigs.k8s.io/yaml"
)

func main() {
	fileData, err := os.ReadFile("./examples/cincinnati/cincinnati.yaml")
	if err != nil {
		log.Fatal(err)
	}
	var tmpl graph.Template
	if err := yaml.Unmarshal(fileData, &tmpl); err != nil {
		log.Fatal(err)
	}
	if err := tmpl.Validate(); err != nil {
		log.Fatal(err)
	}

	pdb, err := db.NewDB(db.Config{
		Host:     "localhost",
		Port:     5432,
		User:     "postgres",
		Password: "postgres",
		DBName:   "extensiondb",
		SSLMode:  "disable",
	})
	if err != nil {
		log.Fatal(err)
	}

	nodes, err := queryNodes(context.TODO(), pdb, tmpl.Images)
	if err != nil {
		log.Fatal(err)
	}

	ng, err := graph.NewGraph(graph.GraphConfig{
		Streams:      tmpl.VersionStreams,
		Nodes:        nodes,
		AsOf:         time.Now(),
		IncludePreGA: false,
	})
	if err != nil {
		log.Fatal(err)
	}

	for n := range ng.NodesMatching(graph.NodeInRange(semver.MustParseRange("3.8.0"))) {
		fmt.Printf("Planning update of %s %s to 3.15 version with a platform upgrade from 4.12 to 4.19...", n.Name, n.VR())
		up, err := ng.PlanOpenShiftPlatformUpgrade(n,
			graph.NodeInRange(semver.MustParseRange(">=3.15.0 <3.16.0-0")),
			graph.MajorMinor{4, 12},
			graph.MajorMinor{4, 19},
		)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Done!\nPlease execute the following steps in order:\n")
		for i, us := range up {
			fmt.Printf("  %d. Update %s from %s to %s\n", i+1, us.Name(), us.From(), us.To())
		}
	}
	nm := viz.Mermaid(ng, viz.MermaidConfig{KeepEdge: func(g *graph.Graph, from *graph.Node, to *graph.Node, w float64) bool {
		shortestPathTo := map[*graph.Node][]ggraph.Node{}
		for head := range g.Heads() {
			sp, _, _ := g.Paths().Between(from.ID(), to.ID())
			if len(sp) == 0 {
				continue
			}
			shortestPathTo[head] = sp
		}

		// Only keep edges that are on the way to full support
		for head, sp := range shortestPathTo {
			if head.LifecyclePhase == graph.LifecyclePhaseFullSupport && sp[0] == to {
				return true
			}
		}
		return w <= 3
	}})
	if err := os.WriteFile("./examples/cincinnati/graph.mmd", []byte(nm), 0644); err != nil {
		log.Fatal(err)
	}
}

func queryNodes(ctx context.Context, pdb *db.DB, refs []graph.CanonicalReference) ([]*graph.Node, error) {
	placeholders := make([]string, 0, len(refs))
	params := make([]any, 0, len(refs)*2)
	for i, ref := range refs {
		a := i*2 + 1
		b := a + 1
		placeholders = append(placeholders, fmt.Sprintf("($%d,$%d)", a, b))
		params = append(params, ref.Name(), ref.Digest().String())
	}
	refLookup := map[string]reference.Canonical{}
	for _, ref := range refs {
		refLookup[ref.String()] = ref
	}

	query := fmt.Sprintf(`SELECT p.name, b.version, b.release, (br.repo || '@' || br.digest) as reference, (b.image ->> 'created')::timestamp as built_at FROM bundles as b JOIN packages as p ON p.id = b.package_id JOIN bundle_reference_bundles as brb ON brb.bundle_id = b.id JOIN bundle_references as br ON br.id = brb.bundle_reference_id WHERE (br.repo, br.digest) IN (%s) ORDER BY built_at ASC`, strings.Join(placeholders, ","))
	rows, err := pdb.QueryContext(ctx, query, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var nodes []*graph.Node
	for rows.Next() {
		var (
			n   graph.Node
			ref string
		)
		if err := rows.Scan(&n.Name, &n.Version, &n.Release, &ref, &n.ReleaseDate); err != nil {
			return nil, err
		}
		n.ImageReference = refLookup[ref]
		nodes = append(nodes, &n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return nodes, nil
}
