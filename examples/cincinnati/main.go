package main

import (
	"cmp"
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"slices"
	"strings"
	"time"

	_ "crypto/sha256"

	"github.com/blang/semver/v4"
	"github.com/joelanford/extensiondb/examples/cincinnati/pkg/graph"
	"github.com/joelanford/extensiondb/examples/cincinnati/pkg/util"
	"github.com/joelanford/extensiondb/examples/cincinnati/pkg/viz"
	"github.com/joelanford/extensiondb/internal/db"
	"go.podman.io/image/v5/docker/reference"
	ggraph "gonum.org/v1/gonum/graph"
	"sigs.k8s.io/yaml"
)

func main() {
	g, err := newGraphFromFile("./examples/cincinnati/product-templates/quay-operator.cincinnati.yaml")
	if err != nil {
		log.Fatal(err)
	}

	printDirectPathsFrom(g, g.FirstNodeMatching(graph.NodeInRange(semver.MustParseRange("3.9.0"))))
	printShortestPathsFrom(g, g.FirstNodeMatching(graph.NodeInRange(semver.MustParseRange("3.9.0"))))
	printUpgradePlans(g)

	if err := writeMermaidFile(g, "./examples/cincinnati/graph.mmd"); err != nil {
		log.Fatal(err)
	}
}

func newGraphFromFile(path string) (*graph.Graph, error) {
	fileData, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var tmpl graph.Template
	if err := yaml.Unmarshal(fileData, &tmpl); err != nil {
		return nil, err
	}
	if err := tmpl.Validate(); err != nil {
		return nil, err
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
		return nil, err
	}

	nodes, err := queryNodes(context.TODO(), pdb, tmpl.Images)
	if err != nil {
		return nil, err
	}

	return graph.NewGraph(graph.GraphConfig{
		Streams:      tmpl.VersionStreams,
		Nodes:        nodes,
		AsOf:         time.Now(),
		IncludePreGA: false,
	})
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

func printDirectPathsFrom(ng *graph.Graph, from *graph.Node) {
	type edge struct {
		from   *graph.Node
		to     *graph.Node
		weight float64
	}
	var edges []edge
	for to := range ng.From(from) {
		weight := ng.EdgeWeight(from, to)
		edges = append(edges, edge{from: from, to: to, weight: weight})
	}
	slices.SortFunc(edges, func(a, b edge) int {
		return cmp.Compare(a.weight, b.weight)
	})
	fmt.Printf("Direct successors of %s:\n", from.VR())
	for _, e := range edges {
		fmt.Printf("  %s (%s) --> %s (%s): %.2f\n", e.from.VR(), e.from.LifecyclePhase, e.to.VR(), e.to.LifecyclePhase, e.weight)
	}
	fmt.Println()
}

func printShortestPathsFrom(ng *graph.Graph, fromNode *graph.Node) {
	type shortestPath struct {
		path   []*graph.Node
		from   *graph.Node
		to     *graph.Node
		weight float64
	}

	var shortestPaths []shortestPath
	for to := range ng.NodesMatching(graph.AllNodes()) {
		p, w, _ := ng.Paths().Between(fromNode.ID(), to.ID())
		shortestPaths = append(shortestPaths, shortestPath{
			path:   util.MapSlice(p, func(i ggraph.Node) *graph.Node { return i.(*graph.Node) }),
			from:   fromNode,
			to:     to,
			weight: w,
		})
	}
	slices.SortFunc(shortestPaths, func(a, b shortestPath) int {
		if v := cmp.Compare(a.weight, b.weight); v != 0 {
			return v
		}
		return util.Compare(a.to, b.to)
	})
	fmt.Printf("Shortest path from %s to every higher versioned node:\n", fromNode.VR())
	for _, sp := range shortestPaths {
		if sp.weight == math.Inf(1) && sp.from.Compare(sp.to) > 0 {
			continue
		}

		fmt.Printf("  %s (%s) --> %s (%s):", sp.from.VR(), sp.from.LifecyclePhase, sp.to.VR(), sp.to.LifecyclePhase)
		if sp.weight == math.Inf(1) {
			fmt.Printf(" ∞\n")
		} else {
			fmt.Printf(" %.2f\n", sp.weight)
		}

	}
	fmt.Println()
}

func printUpgradePlans(ng *graph.Graph) {
	type planCfg struct {
		fromVersion     semver.Version
		desiredNodeDesc string
		desiredNodes    graph.NodePredicate
		fromPlatform    graph.MajorMinor
		toPlatform      graph.MajorMinor
	}

	for _, cfg := range []planCfg{
		{
			desiredNodeDesc: "3.14.x",
			fromVersion:     semver.MustParse("3.9.0"),
			desiredNodes:    graph.NodeInRange(semver.MustParseRange(">=3.14.0 <3.15.0-0")),
			fromPlatform:    graph.MajorMinor{Major: 4, Minor: 12},
			toPlatform:      graph.MajorMinor{Major: 4, Minor: 14},
		},
		{
			desiredNodeDesc: "any version",
			fromVersion:     semver.MustParse("3.9.0"),
			desiredNodes:    graph.AllNodes(),
			fromPlatform:    graph.MajorMinor{Major: 4, Minor: 12},
			toPlatform:      graph.MajorMinor{Major: 4, Minor: 14},
		},
		{
			desiredNodeDesc: "3.14.z",
			fromVersion:     semver.MustParse("3.9.0"),
			desiredNodes:    graph.NodeInRange(semver.MustParseRange(">=3.14.0 <3.15.0-0")),
			fromPlatform:    graph.MajorMinor{Major: 4, Minor: 14},
			toPlatform:      graph.MajorMinor{Major: 4, Minor: 16},
		},
		{
			desiredNodeDesc: "3.10.z",
			fromVersion:     semver.MustParse("3.9.0"),
			desiredNodes:    graph.NodeInRange(semver.MustParseRange(">=3.10.0 <3.11.0-0")),
			fromPlatform:    graph.MajorMinor{Major: 4, Minor: 14},
			toPlatform:      graph.MajorMinor{Major: 4, Minor: 15},
		},
		{
			desiredNodeDesc: "any version",
			fromVersion:     semver.MustParse("3.9.0"),
			desiredNodes:    graph.NodeInRange(semver.MustParseRange(">=3.14.0 <3.15.0-0")),
			fromPlatform:    graph.MajorMinor{Major: 4, Minor: 15},
			toPlatform:      graph.MajorMinor{Major: 4, Minor: 17},
		},
		{
			desiredNodeDesc: "any version",
			fromVersion:     semver.MustParse("3.9.0"),
			desiredNodes:    graph.NodeInRange(semver.MustParseRange(">=3.14.0 <3.15.0-0")),
			fromPlatform:    graph.MajorMinor{Major: 4, Minor: 16},
			toPlatform:      graph.MajorMinor{Major: 4, Minor: 19},
		},
		{
			desiredNodeDesc: "any version",
			fromVersion:     semver.MustParse("3.7.10"),
			desiredNodes:    graph.NodeInRange(semver.MustParseRange(">=3.15.0 <3.16.0-0")),
			fromPlatform:    graph.MajorMinor{Major: 4, Minor: 14},
			toPlatform:      graph.MajorMinor{Major: 4, Minor: 15},
		},
		{
			desiredNodeDesc: "any version",
			fromVersion:     semver.MustParse("3.14.3"),
			desiredNodes:    graph.AllNodes(),
			fromPlatform:    graph.MajorMinor{Major: 4, Minor: 17},
			toPlatform:      graph.MajorMinor{Major: 4, Minor: 18},
		},
	} {
		fmt.Printf("Update plan for OCP %s to %s, and quay-operator from %s to %s:\n", cfg.fromPlatform, cfg.toPlatform, cfg.fromVersion.String(), cfg.desiredNodeDesc)
		node := ng.FirstNodeMatching(graph.NodeInRange(semver.MustParseRange(cfg.fromVersion.String())))
		if node == nil {
			fmt.Printf("  ERROR: could not find node with version %s\n\n", cfg.fromVersion.String())
			continue
		}
		up, err := ng.PlanOpenShiftPlatformUpgrade(
			node,
			cfg.desiredNodes,
			cfg.fromPlatform,
			cfg.toPlatform,
		)
		if err != nil {
			fmt.Printf("  ERROR: %v\n\n", err)
			continue
		}
		for i, us := range up {
			fmt.Printf("  %d. Update %s from %s to %s\n", i+1, us.Name(), us.From(), us.To())
		}
		fmt.Println()
	}
}

func writeMermaidFile(ng *graph.Graph, path string) error {
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
	return os.WriteFile(path, []byte(nm), 0644)
}
