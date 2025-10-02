package main

import (
	"cmp"
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
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
	g, err := newGraphFromFile("./examples/cincinnati/product-templates")
	if err != nil {
		log.Fatal(err)
	}

	quay := g.FirstNodeMatching(graph.AndNodes(
		graph.PackageNodes("quay-operator"),
		graph.NodeInRange(semver.MustParseRange("3.9.8")),
	))

	clusterLogging := g.FirstNodeMatching(graph.AndNodes(
		graph.PackageNodes("cluster-logging"),
		graph.NodeInRange(semver.MustParseRange("6.0.8")),
	))

	acm := g.FirstNodeMatching(graph.AndNodes(
		graph.PackageNodes("advanced-cluster-management"),
		graph.NodeInRange(semver.MustParseRange("2.11.1")),
	))

	kubevirt := g.FirstNodeMatching(graph.AndNodes(
		graph.PackageNodes("kubevirt-hyperconverged"),
		graph.NodeInRange(semver.MustParseRange("4.12.10")),
	))

	printDirectPathsFrom(g, quay)
	printDirectPathsFrom(g, clusterLogging)
	printDirectPathsFrom(g, acm)
	printDirectPathsFrom(g, kubevirt)
	printShortestPathsFrom(g, quay)
	printShortestPathsFrom(g, clusterLogging)
	printShortestPathsFrom(g, acm)
	printShortestPathsFrom(g, kubevirt)
	printUpgradePlans(g)

	if err := writeMermaidFile(g, "./examples/cincinnati", "quay-operator"); err != nil {
		log.Fatal(err)
	}
	if err := writeMermaidFile(g, "./examples/cincinnati", "cluster-logging"); err != nil {
		log.Fatal(err)
	}
	if err := writeMermaidFile(g, "./examples/cincinnati", "advanced-cluster-management"); err != nil {
		log.Fatal(err)
	}
	if err := writeMermaidFile(g, "./examples/cincinnati", "kubevirt-hyperconverged"); err != nil {
		log.Fatal(err)
	}
}

func newGraphFromFile(path string) (*graph.Graph, error) {
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

	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	packages := make([]graph.Package, len(entries))
	for _, entry := range entries {
		filename := filepath.Join(path, entry.Name())
		fileData, err := os.ReadFile(filename)
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

		nodes, err := queryNodes(context.TODO(), pdb, tmpl.Images)
		if err != nil {
			return nil, err
		}
		packages = append(packages, graph.Package{
			Name:    tmpl.Name,
			Nodes:   nodes,
			Streams: tmpl.VersionStreams,
		})
	}

	return graph.NewGraph(graph.GraphConfig{
		Packages:     packages,
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
	fmt.Printf("Direct successors of %s:\n", from.NVR())
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
	fmt.Printf("Shortest path from %s to every descendent:\n", fromNode.NVR())
	for _, sp := range shortestPaths {
		if sp.weight == math.Inf(1) {
			continue
		}
		fmt.Printf("  %s (%s) --> %s (%s): %.2f\n", sp.from.VR(), sp.from.LifecyclePhase, sp.to.VR(), sp.to.LifecyclePhase, sp.weight)
	}
	fmt.Println()
}

func printUpgradePlans(ng *graph.Graph) {
	type planCfg struct {
		nodes        graph.NodePredicate
		fromPlatform graph.MajorMinor
		toPlatform   graph.MajorMinor
	}
	for _, cfg := range []planCfg{
		{
			nodes: graph.OrNodes(
				graph.AndNodes(graph.PackageNodes("advanced-cluster-management"), graph.NodeInRange(semver.MustParseRange("2.7.1"))),
				graph.AndNodes(graph.PackageNodes("cluster-logging"), graph.NodeInRange(semver.MustParseRange("5.6.1"))),
				graph.AndNodes(graph.PackageNodes("kubevirt-hyperconverged"), graph.NodeInRange(semver.MustParseRange("4.12.0"))),
				graph.AndNodes(graph.PackageNodes("quay-operator"), graph.NodeInRange(semver.MustParseRange("3.9.8"))),
			),
			fromPlatform: graph.MajorMinor{Major: 4, Minor: 12},
			toPlatform:   graph.MajorMinor{Major: 4, Minor: 14},
		},
		{
			nodes: graph.OrNodes(
				graph.AndNodes(graph.PackageNodes("advanced-cluster-management"), graph.NodeInRange(semver.MustParseRange("2.8.8"))),
				graph.AndNodes(graph.PackageNodes("cluster-logging"), graph.NodeInRange(semver.MustParseRange("5.8.21"))),
				graph.AndNodes(graph.PackageNodes("kubevirt-hyperconverged"), graph.NodeInRange(semver.MustParseRange("4.14.14"))),
				graph.AndNodes(graph.PackageNodes("quay-operator"), graph.NodeInRange(semver.MustParseRange("3.10.15"))),
			),
			fromPlatform: graph.MajorMinor{Major: 4, Minor: 14},
			toPlatform:   graph.MajorMinor{Major: 4, Minor: 16},
		},
	} {
		up, err := ng.PlanOpenShiftUpdate(
			slices.Collect(ng.NodesMatching(cfg.nodes)),
			cfg.fromPlatform,
			cfg.toPlatform,
		)
		if err != nil {
			fmt.Println(err)
		}
		fmt.Println(up.PrettyReport())
	}
}

func writeMermaidFile(ng *graph.Graph, dir string, pkg string) error {
	nm := viz.Mermaid(ng, pkg, viz.MermaidConfig{KeepEdge: onShortestPathToAnyHead})
	return os.WriteFile(filepath.Join(dir, fmt.Sprintf("mermaid/%s.mmd", pkg)), []byte(nm), 0644)
}

func onShortestPathToAnyHead(g *graph.Graph, from *graph.Node, to *graph.Node, w float64) bool {
	shortestPathTo := map[*graph.Node][]ggraph.Node{}
	for head := range g.Heads() {
		sp, spWeight, _ := g.Paths().Between(from.ID(), head.ID())
		if spWeight == math.Inf(1) {
			continue
		}
		shortestPathTo[head] = sp
	}

	for _, sp := range shortestPathTo {
		if len(sp) > 1 && sp[1] == to {
			return true
		}
	}
	return false
}
