package main

import (
	"context"
	"fmt"
	"log"
	"maps"
	"math"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/RyanCarrier/dijkstra/v2"
	"github.com/blang/semver/v4"
	"github.com/joelanford/extensiondb/internal/db"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/util/sets"
)

type SemverVersionFlag struct {
	semver.Version
}

func (s *SemverVersionFlag) String() string {
	return s.Version.String()
}

func (s *SemverVersionFlag) Set(str string) error {
	v, err := semver.Parse(str)
	if err != nil {
		return err
	}
	s.Version = v
	return nil
}

func (s *SemverVersionFlag) Type() string {
	return "semver.Version"
}

var _ pflag.Value = (*SemverVersionFlag)(nil)

func main() {
	var (
		pdb       *db.DB
		minV      = SemverVersionFlag{Version: semver.MustParse("0.0.0-0")}
		maxV      = SemverVersionFlag{Version: semver.Version{Major: math.MaxUint64, Minor: math.MaxUint64, Patch: math.MaxUint64}}
		bestEdges bool
	)
	cmd := cobra.Command{
		Use:  "upgrade-graph <packageName>",
		Args: cobra.ExactArgs(1),
		PreRun: func(cmd *cobra.Command, _ []string) {
			var err error
			pdb, err = db.NewDB(db.Config{
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
		},
		Run: func(cmd *cobra.Command, args []string) {
			packageName := args[0]
			if err := printGraphForPackage(cmd.Context(), pdb, packageName, minV.Version, maxV.Version, bestEdges); err != nil {
				log.Fatal(err)
			}
		},
	}
	cmd.Flags().Var(&minV, "min-version", "minimum version")
	cmd.Flags().Var(&maxV, "max-version", "minimum version")
	cmd.Flags().BoolVar(&bestEdges, "best-edges", true, "only show best edges in the graph")

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := cmd.ExecuteContext(ctx); err != nil {
		log.Fatal(err)
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

type edgeAttrs int

const (
	canGetToHighestVersion edgeAttrs = 1 << iota
	linksToNextMinorHighestZ
)

func printGraphForPackage(ctx context.Context, pdb *db.DB, packageName string, minVersion, maxVersion semver.Version, bestOnly bool) error {
	rows, err := pdb.QueryContext(ctx, `
SELECT p.name, b.version, (b.image ->> 'created')::timestamp AS created FROM bundles AS b JOIN packages AS p ON p.id = b.package_id WHERE p.name = $1 ORDER BY created`, packageName)
	if err != nil {
		return err
	}
	defer rows.Close()

	var (
		bundles        = []*bundle{}
		edges          = map[*bundle][]*bundle{}
		edgeAttributes = map[[2]*bundle]edgeAttrs{}
		heads          = sets.Set[*bundle]{}
		highestVersion *bundle
	)

	for rows.Next() {
		var b bundle
		if err := rows.Scan(&b.name, &b.version, &b.created); err != nil {
			return err
		}

		if b.version.LT(minVersion) || b.version.GT(maxVersion) {
			continue
		}

		if highestVersion == nil || b.version.GT(highestVersion.version) {
			highestVersion = &b
		}

		for _, oldB := range bundles {
			if oldB.version.LTE(b.version) && oldB.version.Major == b.version.Major && b.version.Minor-oldB.version.Minor <= 1 {
				if _, ok := edges[oldB]; !ok {
					edges[oldB] = []*bundle{}
				}
				edges[oldB] = append(edges[oldB], &b)
				edgeAttributes[[2]*bundle{oldB, &b}] = 0
				heads = heads.Delete(oldB)
			}
		}
		heads.Insert(&b)
		bundles = append(bundles, &b)
	}

	for from := range edges {
		slices.SortFunc(edges[from], func(a, b *bundle) int {
			return b.version.Compare(a.version)
		})
	}

	graph := dijkstra.NewMappedGraph[*bundle]()

	for _, b := range bundles {
		graph.AddEmptyVertex(b)
	}

	for from, tos := range edges {
		for w, to := range tos {
			graph.AddArc(from, to, uint64(w))
		}
	}

	pathToHighest := sets.New[*bundle]()

	for _, b := range bundles {
		if b == highestVersion {
			continue
		}
		best, err := graph.Shortest(b, highestVersion)
		if err != nil {
			continue
		}
		for i := 1; i < len(best.Path); i++ {
			from := best.Path[i-1]
			to := best.Path[i]
			pathToHighest.Insert(to)
			edgeAttributes[[2]*bundle{from, to}] |= canGetToHighestVersion
		}
	}
	for from, tos := range edges {
		if len(tos) == 0 {
			panic("adjacency list should only exist if edges exist")
		}
		to := tos[0]
		edgeAttributes[[2]*bundle{from, to}] |= linksToNextMinorHighestZ
	}

	fmt.Println("flowchart LR")
	for _, b := range bundles {
		fmt.Printf("  %%%% created: %s\n", b.created.Format(time.RFC3339))
		fmt.Printf("  %s%s\n\n", b.ID(), getClass(b, highestVersion, pathToHighest, heads))
	}

	edgeCounter := 0
	sortedKeys := slices.SortedFunc(maps.Keys(edgeAttributes), func(e1 [2]*bundle, e2 [2]*bundle) int {
		if v := e1[0].version.Compare(e2[0].version); v != 0 {
			return v
		}
		return e1[1].version.Compare(e2[1].version)
	})

	var (
		canGetToHighestVersionEdges   []string
		linksToNextMinorHighestZEdges []string
	)

	for _, edge := range sortedKeys {
		attrs := edgeAttributes[edge]
		if isBest := attrs != 0; !isBest && bestOnly {
			continue
		}
		switch attrs {
		case canGetToHighestVersion, canGetToHighestVersion | linksToNextMinorHighestZ:
			canGetToHighestVersionEdges = append(canGetToHighestVersionEdges, strconv.Itoa(edgeCounter))
		case linksToNextMinorHighestZ:
			linksToNextMinorHighestZEdges = append(linksToNextMinorHighestZEdges, strconv.Itoa(edgeCounter))
		}

		fmt.Printf("  %s --> %s\n", edge[0].ID(), edge[1].ID())
		edgeCounter++
	}

	fmt.Println("")
	fmt.Println("  classDef head fill:#fcc,stroke:#333,stroke-width:4px;")
	fmt.Println("  classDef pathToHighest fill:#aaf,stroke:#333,stroke-width:4px;")
	fmt.Println("  classDef highest fill:#8f8,stroke:#333,stroke-width:4px;")
	fmt.Println("")
	fmt.Printf("  linkStyle %s stroke:#f66,stroke-width:1px;\n", strings.Join(linksToNextMinorHighestZEdges, ","))
	fmt.Printf("  linkStyle %s stroke:#6f6,stroke-width:4px;\n", strings.Join(canGetToHighestVersionEdges, ","))
	fmt.Printf("  linkStyle default stroke:#bbb,stroke-width:1px;\n")

	return nil
}

func getClass(b *bundle, highestVersion *bundle, pathToHighest, heads sets.Set[*bundle]) string {
	if highestVersion == b {
		return ":::highest"
	} else if pathToHighest.Has(b) {
		return ":::pathToHighest"
	} else if heads.Has(b) {
		return ":::head"
	}
	return ""
}
