package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
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
	if err := bs.validate(); err != nil {
		log.Fatal(err)
	}

	targets := []majorMinor{
		//{4, 12},
		//{4, 14},
		//{4, 15},
		//{4, 16},
		{4, 17},
		{4, 18},
		{4, 19},
	}
	g, err := newGraph(tmpl, targets, bs)
	if err != nil {
		log.Fatal(err)
	}

	m, err := g.mermaid(true)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(m)
}

type upgradeGraph struct {
	global          graph.Graph[*bundle, *bundle]
	targets         map[majorMinor]graph.Graph[*bundle, *bundle]
	lifecycleStates map[majorMinor]string
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

	sortedTargets := slices.SortedFunc(maps.Keys(g.targets), func(a, b majorMinor) int {
		return a.compare(b)
	})

	linkCount := 0
	linkColors := map[string][]string{}
	for _, tgt := range sortedTargets {
		subgraph := g.targets[tgt]
		am, err := subgraph.AdjacencyMap()
		if err != nil {
			return "", err
		}
		pm, err := subgraph.PredecessorMap()
		if err != nil {
			return "", err
		}

		vGroups := map[majorMinor][]*bundle{}
		sortedTos := slices.SortedFunc(maps.Keys(pm), func(a *bundle, b *bundle) int {
			return a.compare(b)
		})
		for _, b := range sortedTos {
			mm := majorMinor{Major: b.Version.Major, Minor: b.Version.Minor}
			vGroups[mm] = append(vGroups[mm], b)
		}

		sb.WriteString(fmt.Sprintf("  subgraph %s\n", tgt))
		for _, node := range sortedTos {
			sb.WriteString(fmt.Sprintf("    %s_%s[%s]%s\n", tgt, node.ID(), node.ID(), g.nodeClass(tgt, node, len(am[node]) == 0)))
		}

		sortedVGroups := slices.SortedFunc(maps.Keys(vGroups), func(a, b majorMinor) int {
			return a.compare(b)
		})
		for _, mm := range sortedVGroups {
			vGroup := vGroups[mm]
			subgraphID := fmt.Sprintf("%s_%s", tgt, mm)
			sb.WriteString(fmt.Sprintf("\n    subgraph %s[%s]\n", subgraphID, mm))
			for _, to := range vGroup {
				for from, e := range pm[to] {
					sp, style, color := g.edgeInfo(e)
					if !sp && onlyShortestPaths {
						continue
					}
					linkColors[color] = append(linkColors[color], strconv.Itoa(linkCount))
					sb.WriteString(fmt.Sprintf("      %s_%s %s %s_%s\n", tgt, from.ID(), style, tgt, to.ID()))
					linkCount++
				}
			}
			sb.WriteString("    end\n")
			sb.WriteString(fmt.Sprintf("    style %s %s\n", subgraphID, g.subgraphStyle(mm)))
		}
		sb.WriteString("  end\n\n")
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
	if _, ok := edge.Properties.Attributes["shortestPathToHighestVersion"]; ok {
		return true, "===>", "green"
	} else if _, ok := edge.Properties.Attributes["shortestPathToNonHighestHead"]; ok {
		return true, "--->", "black"
	}
	return false, "-.->", "gray"
}

func (g *upgradeGraph) nodeClass(tgt majorMinor, node *bundle, isHead bool) string {
	prefix := ""
	if isHead {
		prefix = "dark"
	}

	_, attrs, _ := g.targets[tgt].VertexWithProperties(node)
	maxOCPVersionStr := attrs.Attributes["olm.maxOpenshiftVersion"]
	maxOCPVersion, err := newMajorMinorFromString(maxOCPVersionStr)
	if err != nil {
		panic(fmt.Sprintf("Invalid node %q: invalid major/minor version %q in olm.maxOpenshiftVersion attribute: %v", node.ID(), maxOCPVersionStr, err))
	}
	v := maxOCPVersion.compare(tgt)
	switch v {
	case -1:
		return fmt.Sprintf(":::%sred", prefix)
	case 0:
		return fmt.Sprintf(":::%sorange", prefix)
	case 1:
		eusTgt := majorMinor{Major: tgt.Major, Minor: tgt.Minor + 2}
		if maxOCPVersion.compare(eusTgt) < 0 {
			return fmt.Sprintf(":::%syellow", prefix)
		}
		return fmt.Sprintf(":::%sgreen", prefix)
	}
	return ""
}

func (g *upgradeGraph) initializeForTarget(tmpl template, tgt majorMinor, bundles []bundle) error {
	versions := tmpl.versionsForTarget(tgt)
	tgtGraph := graph.New[*bundle, *bundle](func(a *bundle) *bundle { return a }, graph.Directed(), graph.PreventCycles())

	var seen []*bundle
	for _, b := range bundles {
		for _, v := range versions {
			if v.matchesVersion(b.Version) {
				slices.SortFunc(v.Targets, func(a majorMinor, b majorMinor) int {
					return b.compare(a)
				})
				if err := tgtGraph.AddVertex(&b, graph.VertexAttribute("olm.maxOpenshiftVersion", v.Targets[0].String())); err != nil {
					return fmt.Errorf("error adding bundle %q to graph: %v", b.ID(), err)
				}

				for _, a := range seen {
					if a.Version.LT(b.Version) && a.Version.GTE(v.MinUpgradeVersion) {
						if err := tgtGraph.AddEdge(a, &b); err != nil {
							return fmt.Errorf("error adding edge %s -> %s to graph: %v", a.ID(), b.ID(), err)
						}
					}
				}

				seen = append(seen, &b)
				break
			}
		}
	}

	gam, err := g.global.AdjacencyMap()
	if err != nil {
		return err
	}
	for n := range gam {
		fmt.Println(n.ID())
	}

	var (
		highestVersion *bundle
		heads          = sets.New[*bundle]()
	)
	am, err := tgtGraph.AdjacencyMap()
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

	for from := range am {
		for _, to := range heads.UnsortedList() {
			if heads.Has(from) {
				continue
			}
			sp, err := graph.ShortestPath(tgtGraph, from, to)
			if err != nil {
				continue
			}
			opts := []func(*graph.EdgeProperties){}
			if to == highestVersion {
				opts = append(opts, graph.EdgeAttribute("shortestPathToHighestVersion", "true"))
			} else {
				opts = append(opts, graph.EdgeAttribute("shortestPathToNonHighestHead", "true"))
			}
			if err := tgtGraph.UpdateEdge(sp[0], sp[1], opts...); err != nil {
				return err
			}
		}
	}

	g.targets[tgt] = tgtGraph
	return nil
}

func newGraph(tmpl template, tgts []majorMinor, bundles []bundle) (*upgradeGraph, error) {
	ug := &upgradeGraph{
		global:          graph.New[*bundle, *bundle](func(a *bundle) *bundle { return a }, graph.Directed(), graph.PreventCycles()),
		targets:         map[majorMinor]graph.Graph[*bundle, *bundle]{},
		lifecycleStates: map[majorMinor]string{},
	}

	for _, tgt := range tgts {
		if err := ug.initializeForTarget(tmpl, tgt, bundles); err != nil {
			return nil, fmt.Errorf("error initializing graph: %v", err)
		}
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

func (mmv *majorMinorVersion) matchesVersion(v semver.Version) bool {
	return mmv.Version.includes(v)
}

func (mmv *majorMinorVersion) updatesFromVersion(v semver.Version) bool {
	// versions with a different major version are never update sources
	if mmv.Version.Major != v.Major {
		return false
	}
	return v.GTE(mmv.MinUpgradeVersion) && v.Minor <= mmv.Version.Minor
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

type bundles []bundle

func (bs bundles) validate() error {
	for _, f := range []func() error{
		bs.validateSamePackage,
	} {
		if err := f(); err != nil {
			return err
		}
	}
	return nil
}

func (bs bundles) validateSamePackage() error {
	pkgs := sets.New[string]()
	for _, b := range bs {
		pkgs.Insert(b.PackageName)
	}
	if len(pkgs) != 1 {
		return fmt.Errorf("policy[singlepackage]: bundles contain more than one package: %v", sets.List(pkgs))
	}
	return nil
}

func (tmpl template) versionsForTarget(tgt majorMinor) []majorMinorVersion {
	versions := []majorMinorVersion{}
	for _, v := range tmpl.Versions {
		if slices.Contains(v.Targets, tgt) {
			versions = append(versions, v)
		}
	}
	return versions
}

type bundle struct {
	PackageName string
	Version     semver.Version
	Release     *string
	Reference   reference.Canonical
	BuiltAt     time.Time
}

func (b bundle) ID() string {
	id := fmt.Sprintf("v%s", b.Version)
	if b.Release != nil {
		id += fmt.Sprintf("_%s", *b.Release)
	}
	return id
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

func queryBundles(ctx context.Context, pdb *db.DB, refs []imageRef) (bundles, error) {
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

	var bs bundles
	for rows.Next() {
		var (
			b   bundle
			ref string
		)
		if err := rows.Scan(&b.PackageName, &b.Version, &b.Release, &ref, &b.BuiltAt); err != nil {
			return nil, err
		}
		b.Reference = refLookup[ref]
		bs = append(bs, b)
	}
	return bs, nil
}
