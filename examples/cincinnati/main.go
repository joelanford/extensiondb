package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"iter"
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
	"github.com/lucasb-eyer/go-colorful"
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

	now := time.Now()
	m, err := g.mermaid(mermaidConfig{
		IgnoreNode: func(b *bundle) bool {
			return false
		},
		IgnoreEdge: func(from *bundle, to *bundle, w int) bool {
			for head, sp := range from.shortestPathTo {
				if head.lifecycleDates.lifecycleState(now) != lifecycleStateFullSupport {
					continue
				}
				if sp[0] == to {
					return false
				}
			}
			return w > 3
		},
		NodeTextFn: func(n *bundle) string {
			return n.String()
		},
		NodeStyle: defaultNodeStyle(now),
		EdgeStyle: defaultEdgeStyle(now),
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile("./examples/cincinnati/graph.mmd", []byte(m), 0644); err != nil {
		log.Fatal(err)
	}

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
	lifecycleStates map[majorMinor]lifecycleState
	asOf            time.Time
	highestVersion  *bundle
	adjacency       map[*bundle]map[*bundle]graph.Edge[*bundle]
	predecessors    map[*bundle]map[*bundle]graph.Edge[*bundle]
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

type mermaidConfig struct {
	IgnoreNode func(*bundle) bool
	IgnoreEdge func(*bundle, *bundle, int) bool

	NodeTextFn func(*bundle) string
	NodeStyle  func(*bundle) string

	EdgeStyle func(*bundle, *bundle, int) string
}

func defaultMermaidConfig(m *mermaidConfig, asOf time.Time) {
	if m == nil {
		m = &mermaidConfig{}
	}
	if m.IgnoreNode == nil {
		m.IgnoreNode = func(_ *bundle) bool { return false }
	}
	if m.IgnoreEdge == nil {
		m.IgnoreEdge = func(_ *bundle, _ *bundle, _ int) bool { return false }
	}
	if m.NodeTextFn == nil {
		m.NodeTextFn = func(b *bundle) string { return b.String() }
	}
	if m.NodeStyle == nil {
		m.NodeStyle = defaultNodeStyle(asOf)
	}
	if m.EdgeStyle == nil {
		m.EdgeStyle = defaultEdgeStyle(asOf)
	}
}

func defaultNodeStyle(asOf time.Time) func(node *bundle) string {
	return func(node *bundle) string {
		hasPathToFullSupport := false
		for head := range node.shortestPathTo {
			if head.lifecycleDates.lifecycleState(asOf) != lifecycleStateFullSupport {
				continue
			}
			hasPathToFullSupport = true
			break
		}

		warningStyle := ""
		if !hasPathToFullSupport {
			warningStyle = ",stroke:#ff0000,stroke-width:3px"
		}

		lfs := node.lifecycleDates.lifecycleState(asOf)
		fillColor := colorForLifecycleState(lfs)
		textColor := colorful.LinearRgb(0, 0, 0)
		fh, fs, fl := fillColor.Hsl()
		fl *= .9
		isHead := len(node.shortestPathTo) == 0
		if isHead {
			fl = 1 - fl
			textColor = colorful.LinearRgb(.95, .95, .95)
		}
		return fmt.Sprintf("fill:%s,color:%s%s", colorful.Hsl(fh, fs, fl).Hex(), textColor.Hex(), warningStyle)
	}
}

func defaultEdgeStyle(asOf time.Time) func(from *bundle, to *bundle, _ int) string {
	return func(from *bundle, to *bundle, _ int) string {
		var highestHead *bundle
		for head := range from.shortestPathTo {
			if highestHead == nil || head.compare(highestHead) > 0 {
				highestHead = head
			}
		}
		for head, sp := range orderedMap(from.shortestPathTo, func(a, b *bundle) int { return b.compare(a) }) {
			if sp[0] != to {
				continue
			}
			headLFS := head.lifecycleDates.lifecycleState(asOf)
			headColor := colorForLifecycleState(headLFS)
			h, _, _ := headColor.Hsl()
			headColor = colorful.Hsl(h, 1, .3)
			return fmt.Sprintf("stroke:%s,stroke-width:2px", headColor.Hex())
		}
		return "stroke:gray,fill:none,stroke-width:0.5px,stroke-dasharray:4;"
	}
}

func colorForLifecycleState(lfs lifecycleState) colorful.Color {
	switch lfs {
	case lifecycleStatePreGA:
		return colorful.LinearRgb(1, 1, 1)
	case lifecycleStateFullSupport:
		return colorful.Hsl(100, 1, .9)
	case lifecycleStateMaintenance:
		return colorful.Hsl(60, 1, .9)
	case lifecycleStateEndOfLife:
		return colorful.Hsl(360, 1, .9)
	case 2:
		return colorful.Hsl(170, 1, .9)
	case 3:
		return colorful.Hsl(240, 1, .9)
	}
	panic("unknown lifecycleState: " + lfs.String())
}

func (g *upgradeGraph) mermaid(conf mermaidConfig) (string, error) {
	defaultMermaidConfig(&conf, g.asOf)

	var sb strings.Builder

	sb.WriteString("graph LR\n")

	pm := g.predecessors

	bundleMinorVersions := map[majorMinor][]*bundle{}
	for b := range orderedMap(pm, compareBundles) {
		if conf.IgnoreNode(b) {
			continue
		}
		mm := majorMinor{Major: b.Version.Major, Minor: b.Version.Minor}
		bundleMinorVersions[mm] = append(bundleMinorVersions[mm], b)
	}

	nodeClasses := map[string]string{}
	edgeCount := 0
	edgeStyles := map[string][]string{}

	for mm, vGroup := range orderedMap(bundleMinorVersions, compareMajorMinors) {
		subgraphString := fmt.Sprintf("%s", mm)
		sb.WriteString(fmt.Sprintf("\n  subgraph %s[\"%s (%s)\"]\n", subgraphString, mm, g.lifecycleStates[mm]))
		for _, to := range vGroup {
			style := conf.NodeStyle(to)
			class := "default"
			if style != "" {
				class = fmt.Sprintf("c%x", hashString(style))
				nodeClasses[class] = style
			}
			sb.WriteString(fmt.Sprintf("    %s:::%s\n", to.String(), class))

			for from, e := range orderedMap(pm[to], compareBundles) {
				if conf.IgnoreNode(from) || conf.IgnoreEdge(from, to, e.Properties.Weight) {
					continue
				}

				edgeStyle := conf.EdgeStyle(from, to, e.Properties.Weight)
				edgeStyles[edgeStyle] = append(edgeStyles[edgeStyle], strconv.Itoa(edgeCount))
				sb.WriteString(fmt.Sprintf("    %s --> %s\n", conf.NodeTextFn(from), conf.NodeTextFn(to)))
				edgeCount++
			}
		}
		sb.WriteString("  end\n")
		sb.WriteString(fmt.Sprintf("  style %s %s\n", subgraphString, g.subgraphStyle(mm)))
	}

	for class, style := range orderedMap(nodeClasses, cmp.Compare) {
		sb.WriteString(fmt.Sprintf("  classDef %s %s;\n", class, style))
	}

	for style, edges := range orderedMap(edgeStyles, cmp.Compare) {
		sb.WriteString(fmt.Sprintf("  linkStyle %s %s;\n", strings.Join(edges, ","), style))
	}

	return sb.String(), nil
}

func (g *upgradeGraph) subgraphStyle(mm majorMinor) string {
	lfs := g.lifecycleStates[mm]
	return fmt.Sprintf("fill:%s", colorForLifecycleState(lfs).Hex())
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
			fromLifecycleState := a.lifecycleDates.lifecycleState(g.asOf)
			toLifecycleState := b.lifecycleDates.lifecycleState(g.asOf)

			// Don't upgrade into a "worse" lifecycle state
			if fromLifecycleState < toLifecycleState {
				continue
			}
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

	for from, tos := range orderedMap(am, compareBundles) {
		count := 1
		for to := range orderedMap(tos, func(a, b *bundle) int { return b.compare(a) }) {
			weight := count
			toLifecycleState := to.lifecycleDates.lifecycleState(g.asOf)
			if toLifecycleState == lifecycleStateEndOfLife {
				weight = count + 100000
			}
			if err := gr.UpdateEdge(from, to, graph.EdgeWeight(weight)); err != nil {
				return err
			}
			count++
		}
	}

	am, err = gr.AdjacencyMap()
	if err != nil {
		return err
	}
	for from := range orderedMap(am, compareBundles) {
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
		asOf:            time.Now(),
		lifecycleStates: map[majorMinor]lifecycleState{},
	}
	if err := ug.initialize(tmpl, bundles); err != nil {
		return nil, fmt.Errorf("error initializing graph: %v", err)
	}

	for _, mmv := range tmpl.Versions {
		ug.lifecycleStates[mmv.Version] = mmv.LifecycleDates.lifecycleState(ug.asOf)
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

func (ld lifecycleDates) lifecycleState(asOf time.Time) lifecycleState {
	if asOf.Before(ld.GA.Time) {
		return lifecycleStatePreGA
	}
	if asOf.Before(ld.Maintenance.Time) {
		return lifecycleStateFullSupport
	}
	if asOf.After(ld.EndOfLife.Time) {
		return lifecycleStateEndOfLife
	}

	if len(ld.Extensions) == 0 || asOf.Before(ld.Extensions[0].Time) {
		return lifecycleStateMaintenance
	}
	for i, ext := range ld.Extensions[1:] {
		if asOf.Before(ext.Time) {
			return lifecycleState(i + 2)
		}
	}
	return lifecycleState(len(ld.Extensions) + 1)
}

type lifecycleState int

const (
	lifecycleStatePreGA       lifecycleState = -1
	lifecycleStateFullSupport lifecycleState = 0
	lifecycleStateMaintenance lifecycleState = 1
	lifecycleStateEndOfLife                  = math.MaxInt
)

func (ls lifecycleState) String() string {
	if ls < -1 {
		panic("invalid lifecycle state")
	}
	switch ls {
	case lifecycleStatePreGA:
		return "Pre-GA"
	case lifecycleStateFullSupport:
		return "Full Support"
	case lifecycleStateMaintenance:
		return "Maintenance"
	case lifecycleStateEndOfLife:
		return "End-of-Life"
	default:
		return fmt.Sprintf("EUS-%d", ls-1)
	}
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

func hashString(s string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(s))
	return h.Sum64()
}

func (b bundle) ID() int64 {
	return int64(hashString(b.String()))
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

func orderedMap[K comparable, V any](m map[K]V, cmp func(a, b K) int) iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		orderedKeys := slices.SortedFunc(maps.Keys(m), cmp)
		for _, k := range orderedKeys {
			v := m[k]
			if !yield(k, v) {
				return
			}
		}
	}
}

func compareBundles(a, b *bundle) int {
	return a.compare(b)
}

func compareMajorMinors(a, b majorMinor) int {
	return a.compare(b)
}
