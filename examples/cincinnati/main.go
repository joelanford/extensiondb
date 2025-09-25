package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"maps"
	"math"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	_ "crypto/sha256"

	"github.com/blang/semver/v4"
	"github.com/dominikbraun/graph"
	"github.com/joelanford/extensiondb/internal/db"
	"go.podman.io/image/v5/docker/reference"
	"gonum.org/v1/gonum/graph/path"
	"gonum.org/v1/gonum/graph/simple"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/yaml"
)

func main() {
	fileData, err := os.ReadFile("./examples/cincinnati/template.yaml")
	if err != nil {
		log.Fatal(err)
	}
	var tmpl template
	if err := yaml.Unmarshal(fileData, &tmpl); err != nil {
		log.Fatal(err)
	}
	if err := tmpl.validate(); err != nil {
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

	bs, err := queryBundles(context.TODO(), pdb, tmpl.Images)
	if err != nil {
		log.Fatal(err)
	}

	g, err := newGraph(tmpl, bs)
	if err != nil {
		log.Fatal(err)
	}

	m, err := g.mermaid(true)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(m)

	sp, weight, err := g.preferredPathToOCPVersion(bs[0], majorMinor{4, 19}, preferenceHighestVersion())
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf(sp[0].String())
	for i := range sp[1:] {
		e, _ := g.global.Edge(sp[i], sp[i+1])
		fmt.Printf(" -(%d)-> %s", e.Properties.Weight, sp[i+1].String())
	}
	fmt.Println()
	fmt.Println(weight)
}

type upgradeGraph struct {
	global          graph.Graph[*bundle, *bundle]
	lifecycleStates map[majorMinor]string
	highestVersion  *bundle
	adjacency       map[*bundle]map[*bundle]graph.Edge[*bundle]
	predecessors    map[*bundle]map[*bundle]graph.Edge[*bundle]
}

func (g *upgradeGraph) getSupportedOpenshiftVersions(b *bundle) (sets.Set[majorMinor], error) {
	_, props, err := g.global.VertexWithProperties(b)
	if err != nil {
		return nil, err
	}
	propStr := props.Attributes["supportedOpenshiftVersions"]
	supportedOpenshiftVersionsStrs := strings.Split(propStr, "|")
	supportedOpenshiftVersions := sets.New[majorMinor]()
	for _, str := range supportedOpenshiftVersionsStrs {
		mm, err := newMajorMinorFromString(str)
		if err != nil {
			return nil, err
		}
		supportedOpenshiftVersions.Insert(mm)
	}
	return supportedOpenshiftVersions, nil
}

func preferenceHighestVersion() func(*bundle, *bundle) int {
	return func(a *bundle, b *bundle) int {
		return b.compare(a)
	}
}

func (g *upgradeGraph) preferredPathToOCPVersion(from *bundle, toOCPVersion majorMinor, preference func(*bundle, *bundle) int) ([]*bundle, float64, error) {
	// get all bundles available in desired OCP version
	var availableInTarget []*bundle
	am := g.adjacency
	for b := range am {
		if !b.supportedOpenshiftVersions.Has(toOCPVersion) {
			continue
		}
		availableInTarget = append(availableInTarget, b)
	}

	// sort by preference
	slices.SortFunc(availableInTarget, preference)

	// as soon as we find a path, return it.
	for _, to := range availableInTarget {
		sp, weight, err := g.shortestPath(from, to)
		if err == nil {
			return sp, weight, nil
		}
	}
	return nil, 0, graph.ErrTargetNotReachable
}

func (g *upgradeGraph) mermaid(onlyShortestPaths bool) (string, error) {
	var sb strings.Builder

	sb.WriteString("flowchart\n")

	sb.WriteString("  classDef green fill:#cfc,stroke:#333,stroke-width:4px;\n")
	sb.WriteString("  classDef yellow fill:#ffc,stroke:#333,stroke-width:4px;\n")
	sb.WriteString("  classDef orange fill:#fdb,stroke:#333,stroke-width:4px;\n")
	sb.WriteString("  classDef red fill:#fcc,stroke:#333,stroke-width:4px;\n\n")

	sb.WriteString("  classDef darkgreen fill:#070,color:#fff,stroke:#333,stroke-width:4px;\n")
	sb.WriteString("  classDef darkyellow fill:#770,color:#fff,stroke:#333,stroke-width:4px;\n")
	sb.WriteString("  classDef darkorange fill:#b50,color:#fff,stroke:#333,stroke-width:4px;\n")
	sb.WriteString("  classDef darkred fill:#b00,color:#fff,stroke:#333,stroke-width:4px;\n\n")

	sb.WriteString("  classDef head fill:#cfc,color:#000,stroke:#333,stroke-width:4px;\n\n")

	linkCount := 0
	linkColors := map[string][]string{}

	am := g.adjacency
	pm := g.predecessors

	vGroups := map[majorMinor][]*bundle{}
	sortedTos := slices.SortedFunc(maps.Keys(pm), func(a *bundle, b *bundle) int {
		return a.compare(b)
	})
	for _, b := range sortedTos {
		mm := majorMinor{Major: b.Version.Major, Minor: b.Version.Minor}
		vGroups[mm] = append(vGroups[mm], b)
	}

	sortedVGroups := slices.SortedFunc(maps.Keys(vGroups), func(a, b majorMinor) int {
		return a.compare(b)
	})
	for _, mm := range sortedVGroups {
		vGroup := vGroups[mm]
		subgraphString := fmt.Sprintf("%s", mm)
		sb.WriteString(fmt.Sprintf("\n  subgraph %s[%s]\n", subgraphString, mm))
		for _, to := range vGroup {
			sb.WriteString(fmt.Sprintf("    %s%s\n", to.String(), g.nodeClass(to, len(am[to]) == 0)))

			for from, e := range pm[to] {
				sp, style, color := g.edgeInfo(e)
				if !sp && onlyShortestPaths {
					continue
				}
				linkColors[color] = append(linkColors[color], strconv.Itoa(linkCount))
				sb.WriteString(fmt.Sprintf("    %s %s %s\n", from.String(), style, to.String()))
				linkCount++
			}
		}
		sb.WriteString("  end\n")
		sb.WriteString(fmt.Sprintf("  style %s %s\n", subgraphString, g.subgraphStyle(mm)))
	}

	for color, links := range linkColors {
		sb.WriteString(fmt.Sprintf("  linkStyle %s stroke:%s;\n", strings.Join(links, ","), color))
	}

	return sb.String(), nil
}

func (g *upgradeGraph) subgraphStyle(mm majorMinor) string {
	lf := g.lifecycleStates[mm]
	switch lf {
	case "pre-ga":
		return "fill:#fff"
	case "full-support":
		return "fill:#cfc"
	case "maintenance":
		return "fill:#ffc"
	case "eus-1":
		return "fill:#fdb"
	case "eus-2":
		return "fill:#fdb"
	case "end-of-life":
		return "fill:#fcc"
	}
	panic("unknown lifecycleState: " + lf)
}

func (g *upgradeGraph) edgeInfo(edge graph.Edge[*bundle]) (bool, string, string) {
	for to, path := range edge.Source.shortestPathTo {
		if path[0] != edge.Target {
			continue
		}
		if to == g.highestVersion {
			return true, "===>", "green"
		}
		return true, "--->", "black"
	}
	return false, "-.->", "gray"
	//
	//if _, ok := edge.Properties.Attributes["shortestPathToHighestVersion"]; ok {
	//	return true, "===>", "green"
	//} else if _, ok := edge.Properties.Attributes["shortestPathToNonHighestHead"]; ok {
	//	return true, "--->", "black"
	//}
	//return false, "-.->", "gray"
}

func (g *upgradeGraph) nodeClass(node *bundle, isHead bool) string {
	if isHead {
		return ":::head"
	}
	return ""
}

func (g *upgradeGraph) initialize(tmpl template, bundles []*bundle) error {
	gr := graph.New[*bundle, *bundle](func(a *bundle) *bundle { return a }, graph.Directed(), graph.PreventCycles())
	versionsByMajorMinor := map[majorMinor]majorMinorVersion{}
	for _, v := range tmpl.Versions {
		slices.SortFunc(v.Targets, func(a majorMinor, b majorMinor) int {
			return a.compare(b)
		})
		versionsByMajorMinor[v.Version] = v
	}

	var (
		seen []*bundle
		errs []error
	)
	for _, b := range bundles {
		mm := majorMinor{Major: b.Version.Major, Minor: b.Version.Minor}
		v, ok := versionsByMajorMinor[mm]
		if !ok {
			errs = append(errs, fmt.Errorf("invalid template: bundle image %s is in version %d.%d, but that version is not listed in the template versions", b.Reference.String(), b.Version.Major, b.Version.Minor))
			continue
		}
		b.supportedOpenshiftVersions = sets.New[majorMinor](v.Targets...)
		b.maxOpenShiftVersion = v.Targets[len(v.Targets)-1]
		b.lifecycleDates = v.LifecycleDates

		if err := gr.AddVertex(b); err != nil {
			return fmt.Errorf("error adding bundle %q to graph: %v", b.String(), err)
		}

		for _, a := range seen {
			if a.Version.LT(b.Version) && a.Version.GTE(v.MinUpgradeVersion) {
				if err := gr.AddEdge(a, b); err != nil {
					return fmt.Errorf("error adding edge %s -> %s to graph: %v", a.String(), b.String(), err)
				}
			}
		}

		seen = append(seen, b)
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	var (
		highestVersion *bundle
		heads          = sets.New[*bundle]()
	)
	am, err := gr.AdjacencyMap()
	if err != nil {
		return err
	}
	for b, ancestors := range am {
		if highestVersion == nil || b.Version.GT(highestVersion.Version) {
			highestVersion = b
		}
		if len(ancestors) == 0 {
			heads.Insert(b)
		}
	}

	sortedFroms := slices.SortedFunc(maps.Keys(am), func(a *bundle, b *bundle) int {
		return a.compare(b)
	})

	for _, from := range sortedFroms {
		sortedTos := slices.SortedFunc(maps.Keys(am[from]), func(a *bundle, b *bundle) int {
			return b.compare(a)
		})
		for i, to := range sortedTos {
			if err := gr.UpdateEdge(from, to, graph.EdgeWeight(i+1)); err != nil {
				return err
			}
		}
	}

	am, err = gr.AdjacencyMap()
	if err != nil {
		return err
	}
	for _, from := range sortedFroms {
		shortestPathsTo := map[*bundle][]*bundle{}
		for _, to := range heads.UnsortedList() {
			if heads.Has(from) {
				continue
			}
			sp, _, err := shortestPath(am, from, to)
			if err != nil {
				continue
			}
			shortestPathsTo[to] = sp[1:]
		}
		from.shortestPathTo = shortestPathsTo
	}

	pm, err := gr.PredecessorMap()
	if err != nil {
		return err
	}

	g.global = gr
	g.highestVersion = highestVersion
	g.adjacency = am
	g.predecessors = pm
	return nil
}

func newGraph(tmpl template, bundles []*bundle) (*upgradeGraph, error) {
	ug := &upgradeGraph{
		global:          graph.New[*bundle, *bundle](func(a *bundle) *bundle { return a }, graph.Directed(), graph.PreventCycles()),
		lifecycleStates: map[majorMinor]string{},
	}

	if err := ug.initialize(tmpl, bundles); err != nil {
		return nil, fmt.Errorf("error initializing graph: %v", err)
	}

	now := time.Now()
	for _, mmv := range tmpl.Versions {
		ug.lifecycleStates[mmv.Version] = mmv.lifecycleState(now)
	}

	return ug, nil
}

type template struct {
	Schema   string              `json:"schema"`
	Versions []majorMinorVersion `json:"versions"`
	Images   []imageRef          `json:"images"`
}

func (t *template) validate() error {
	var errs []error
	for _, version := range t.Versions {
		if err := version.LifecycleDates.validate(); err != nil {
			errs = append(errs, fmt.Errorf("version %q invalid: %v", version.Version, err))
		}
	}
	return errors.Join(errs...)
}

type majorMinorVersion struct {
	Version           majorMinor     `json:"version"`
	MinUpgradeVersion semver.Version `json:"minUpgradeVersion"`
	LifecycleDates    lifecycleDates `json:"lifecycleDates"`
	Targets           []majorMinor   `json:"targets"`
}

func (mmv *majorMinorVersion) lifecycleState(asOf time.Time) string {
	if asOf.Before(mmv.LifecycleDates.GA.Time) {
		return "pre-ga"
	}
	if asOf.Before(mmv.LifecycleDates.Maintenance.Time) {
		return "full-support"
	}
	if asOf.After(mmv.LifecycleDates.EndOfLife.Time) {
		return "end-of-life"
	}

	if len(mmv.LifecycleDates.Extensions) == 0 || asOf.Before(mmv.LifecycleDates.Extensions[0].Time) {
		return "maintenance"
	}
	for i, ext := range mmv.LifecycleDates.Extensions[1:] {
		if asOf.Before(ext.Time) {
			return fmt.Sprintf("eus-%d", i+1)
		}
	}
	return fmt.Sprintf("eus-%d", len(mmv.LifecycleDates.Extensions))
}

type date struct {
	time.Time
}

func (d *date) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return err
	}
	d.Time = t
	return nil
}

func (d date) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.Time.Format("2006-01-02"))
}

type lifecycleDates struct {
	GA          date   `json:"ga"`
	Maintenance date   `json:"maintenance"`
	Extensions  []date `json:"extensions"`
	EndOfLife   date   `json:"eol"`
}

func (l lifecycleDates) validate() error {
	expectedOrder := []date{l.GA, l.Maintenance}
	expectedOrder = append(expectedOrder, l.Extensions...)
	expectedOrder = append(expectedOrder, l.EndOfLife)

	var v time.Time
	for _, d := range expectedOrder {
		if d.Time.Before(v) {
			return fmt.Errorf("invalid: lifecycle dates out of order")
		}
		v = d.Time
	}
	return nil
}

type majorMinor struct {
	Major uint64 `json:"major"`
	Minor uint64 `json:"minor"`
}

func (v majorMinor) String() string {
	return fmt.Sprintf("%d.%d", v.Major, v.Minor)
}

func (v majorMinor) MarshalJSON() ([]byte, error) {
	return json.Marshal(v.String())
}

func (mm majorMinor) includes(v semver.Version) bool {
	return v.Major == mm.Major && v.Minor == mm.Minor
}

func (mm majorMinor) compare(other majorMinor) int {
	if v := cmp.Compare(mm.Major, other.Major); v != 0 {
		return v
	}
	return cmp.Compare(mm.Minor, other.Minor)
}

func newMajorMinorFromString(s string) (majorMinor, error) {
	matches := majorMinorRegexp.FindStringSubmatch(s)
	if len(matches) == 0 {
		return majorMinor{}, fmt.Errorf("invalid version %q; expected <major>.<minor>", s)
	}
	if len(matches) != 3 {
		panic("programmer error: expected 2 submatches")
	}

	major, _ := strconv.ParseUint(matches[1], 10, 64)
	minor, _ := strconv.ParseUint(matches[2], 10, 64)
	return majorMinor{
		Major: major,
		Minor: minor,
	}, nil
}

const majorMinorPattern = `^(0|[1-9]\d*)\.(0|[1-9]\d*)$`

var majorMinorRegexp = regexp.MustCompile(majorMinorPattern)

func (v *majorMinor) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	mm, err := newMajorMinorFromString(s)
	if err != nil {
		return err
	}
	*v = mm
	return nil
}

type imageRef struct {
	reference.Canonical
}

func (i imageRef) MarshalJSON() ([]byte, error) {
	return json.Marshal(i.String())
}

func (i *imageRef) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	ref, err := reference.ParseNamed(s)
	if err != nil {
		return fmt.Errorf("invalid image reference %q: %w", s, err)
	}
	canonical, ok := ref.(reference.Canonical)
	if !ok {
		return fmt.Errorf("invalid image reference %q: not canonical", ref)
	}
	i.Canonical = canonical
	return nil
}

type bundle struct {
	PackageName string
	Version     semver.Version
	Release     *string
	Reference   reference.Canonical
	BuiltAt     time.Time

	maxOpenShiftVersion        majorMinor
	supportedOpenshiftVersions sets.Set[majorMinor]
	lifecycleDates             lifecycleDates
	shortestPathTo             map[*bundle][]*bundle
}

func (b bundle) String() string {
	id := fmt.Sprintf("v%s", b.Version)
	if b.Release != nil {
		id += fmt.Sprintf("_%s", *b.Release)
	}
	return id
}

func (b bundle) ID() int64 {
	h := fnv.New64a()
	h.Write([]byte(b.String()))
	return int64(h.Sum64())
}

func (b *bundle) compare(other *bundle) int {
	if v := b.Version.Compare(other.Version); v != 0 {
		return v
	}
	if b.Release == nil && other.Release != nil {
		return -1
	}
	if b.Release == nil && other.Release == nil {
		return 0
	}
	if b.Release != nil && other.Release == nil {
		return 1
	}
	return cmp.Compare(*b.Release, *other.Release)
}

func queryBundles(ctx context.Context, pdb *db.DB, refs []imageRef) ([]*bundle, error) {
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

	var bs []*bundle
	for rows.Next() {
		var (
			b   bundle
			ref string
		)
		if err := rows.Scan(&b.PackageName, &b.Version, &b.Release, &ref, &b.BuiltAt); err != nil {
			return nil, err
		}
		b.Reference = refLookup[ref]
		bs = append(bs, &b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := validateSamePackage(bs); err != nil {
		return nil, err
	}

	return bs, nil
}

func validateSamePackage(bundles []*bundle) error {
	pkgs := sets.New[string]()
	for _, b := range bundles {
		pkgs.Insert(b.PackageName)
	}
	if len(pkgs) != 1 {
		return fmt.Errorf("policy[singlepackage]: bundles contain more than one package: %v", sets.List(pkgs))
	}
	return nil
}

func shortestPath(adjacencyMap map[*bundle]map[*bundle]graph.Edge[*bundle], from *bundle, to *bundle) ([]*bundle, float64, error) {
	// NOTE: the graph.ShortestPath function seems broken, at least for graphs that we might build.
	// So for now, we use gonum/graph and gonum/graph/path to compute shortest paths.
	wg := simple.NewWeightedDirectedGraph(0, math.Inf(1))
	for a := range adjacencyMap {
		for b, e := range adjacencyMap[a] {
			wg.SetWeightedEdge(simple.WeightedEdge{
				F: a,
				T: b,
				W: float64(e.Properties.Weight),
			})
		}
	}
	sp, weight := path.DijkstraFromTo(from, to, wg)
	if len(sp) == 0 {
		return nil, math.Inf(1), graph.ErrTargetNotReachable
	}

	var out []*bundle
	for _, b := range sp {
		out = append(out, b.(*bundle))
	}
	return out, weight, nil
}

func (ug *upgradeGraph) shortestPath(from *bundle, to *bundle) ([]*bundle, float64, error) {
	return shortestPath(ug.adjacency, from, to)
}
