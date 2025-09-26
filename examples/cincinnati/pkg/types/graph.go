package types

import (
	"errors"
	"fmt"
	"iter"
	"math"
	"slices"
	"time"

	"github.com/blang/semver/v4"
	"github.com/joelanford/extensiondb/examples/cincinnati/pkg/util"
	"gonum.org/v1/gonum/graph"
	"gonum.org/v1/gonum/graph/path"
	"gonum.org/v1/gonum/graph/simple"
	"k8s.io/apimachinery/pkg/util/sets"
)

type Graph struct {
	simple.WeightedDirectedGraph

	paths path.AllShortest
	heads sets.Set[*Node]
}

type GraphConfig struct {
	Streams      []VersionStream
	Nodes        []*Node
	AsOf         time.Time
	IncludePreGA bool
}

func NewGraph(cfg GraphConfig) (*Graph, error) {
	wg := simple.NewWeightedDirectedGraph(0, math.Inf(1))
	for _, node := range cfg.Nodes {
		wg.AddNode(node)
	}

	g := &Graph{WeightedDirectedGraph: *wg}
	if err := g.buildEdges(cfg); err != nil {
		return nil, err
	}
	g.paths = path.DijkstraAllPaths(wg)

	heads := sets.New[*Node]()
	for n := range g.NodesMatching(isHead) {
		heads.Insert(n)
	}
	g.heads = heads

	return g, nil
}

func (g *Graph) Paths() path.AllShortest {
	return g.paths
}

func (g *Graph) NodesMatching(match NodePredicate) iter.Seq[*Node] {
	it := NodeIterator(g.Nodes())
	return func(yield func(*Node) bool) {
		for n := range it {
			if match(g, n) {
				if !yield(n) {
					return
				}
			}
		}
	}
}

func (g *Graph) Heads() sets.Set[*Node] {
	return g.heads
}

func isHead(g *Graph, n *Node) bool {
	for range NodeIterator(g.From(n.ID())) {
		return false
	}
	return true
}

func (g *Graph) buildEdges(cfg GraphConfig) error {
	var (
		streamsByMajorMinor = util.KeySlice(cfg.Streams, func(s VersionStream) MajorMinor { return s.Version })
		nodesByReleaseDate  = slices.SortedFunc(NodeIterator(g.Nodes()), func(a, b *Node) int { return a.ReleaseDate.Compare(b.ReleaseDate) })
		froms               = make([]*Node, 0, len(nodesByReleaseDate))
		errs                []error
	)
	for _, to := range nodesByReleaseDate {
		toMM := NewMajorMinorFromVersion(to.Version)
		stream, ok := streamsByMajorMinor[toMM]
		if !ok {
			errs = append(errs, fmt.Errorf("node with reference %s has major.minor version %s, but that version is not in an available stream", to.ImageReference.String(), toMM))
			continue
		}

		to.SupportedPlatformVersions = sets.New[MajorMinor](stream.SupportedPlatformVersions...)
		to.LifecyclePhase = stream.LifecycleDates.Phase(cfg.AsOf)
		
		if !cfg.IncludePreGA && to.LifecyclePhase == LifecyclePhasePreGA {
			continue
		}

		g.initializeEdgesTo(froms, to, stream.MinimumUpdateVersion)
		froms = append(froms, to)
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	g.assignEdgeWeights()
	return nil
}

func NodeIterator(it graph.Nodes) iter.Seq[*Node] {
	return func(yield func(*Node) bool) {
		for it.Next() {
			n := it.Node().(*Node)
			if !yield(n) {
				return
			}
		}
	}
}

func (cfg *GraphConfig) Validate() error {
	// TODO: collect errors
	if len(cfg.Streams) == 0 {
		return fmt.Errorf("no streams specified")
	}
	for _, stream := range cfg.Streams {
		if err := stream.LifecycleDates.ValidateOrder(); err != nil {
			return err
		}
	}
	if len(cfg.Nodes) == 0 {
		return fmt.Errorf("no nodes specified")
	}
	if err := validateNodeNames(cfg.Nodes); err != nil {
		return err
	}
	if cfg.AsOf.IsZero() {
		return fmt.Errorf("no as-of timestamp specified")
	}
	return nil
}

func validateNodeNames(nodes []*Node) error {
	names := sets.New[string]()
	for _, n := range nodes {
		if n.Name != "" {
			names.Insert(n.Name)
		}
	}
	if len(names) == 0 {
		return errors.New("invalid nodes: no nodes are have a name")
	}
	if len(names) != 1 {
		return fmt.Errorf("invalid nodes: found more than one name in the set of node names, expected exactly one: %v", sets.List(names))
	}
	return nil
}

func (g *Graph) initializeEdgesTo(froms []*Node, to *Node, minimumUpdateVersion semver.Version) {
	for _, from := range froms {
		// Don't update to a lower version
		if from.Compare(to) > 0 {
			continue
		}

		// Don't update from a version below the minimum update version
		if from.Version.LT(minimumUpdateVersion) {
			continue
		}

		// Don't update to a different major version
		if from.Version.Major != to.Version.Major {
			continue
		}

		// Don't update into a "worse" lifecycle state
		if from.LifecyclePhase < to.LifecyclePhase {
			continue
		}
		// Don't update across minor versions into an end-of-life version (even if from is also an end-of-life version)
		if from.Version.Minor != to.Version.Minor && to.LifecyclePhase == LifecyclePhaseEndOfLife {
			continue
		}

		// We don't know from's full set of successors yet. For now, set weight to 1.
		// Once all edges have been set, we can make a second pass to set better weights.
		edge := simple.WeightedEdge{F: from, T: to, W: 1}
		g.SetWeightedEdge(edge)
	}
}

func (g *Graph) assignEdgeWeights() {
	for from := range NodeIterator(g.Nodes()) {
		// We sort the successors in reverse version order. We set up the weights so that graph traversals
		// prefer updating to higher versions, which reduces the overall number of updates necessary to get
		// to a desired version.
		tos := slices.SortedFunc(NodeIterator(g.From(from.ID())), func(a *Node, b *Node) int {
			return b.Compare(a)
		})
		for i, to := range tos {
			g.RemoveEdge(from.ID(), to.ID())
			g.SetWeightedEdge(simple.WeightedEdge{F: from, T: to, W: float64(i + 1)})
		}
	}
}

func (g *Graph) PlanPlatformUpgrade(fromNode *Node, desiredToNodes NodePredicate, fromPlatform MajorMinor, toPlatform MajorMinor) (UpdatePlan, error) {
	if desiredToNodes == nil {
		desiredToNodes = AllNodes()
	}

	var candidateNodes []*Node
	for toGraphNode := range NodeIterator(g.Nodes()) {
		if !desiredToNodes(g, toGraphNode) {
			continue
		}
		if !toGraphNode.SupportedPlatformVersions.HasAll(fromPlatform, toPlatform) {
			continue
		}
		candidateNodes = append(candidateNodes, toGraphNode)
	}
	if len(candidateNodes) == 0 {
		return nil, fmt.Errorf("no desired nodes are supported on both platforms %s and %s", fromPlatform, toPlatform)
	}

	type shortestPath struct {
		updatePath []graph.Node
		weight     float64
	}
	var sp *shortestPath
	for _, toNode := range candidateNodes {
		// No node update required. fromNode is supported on toPlatform.
		if fromNode == toNode {
			sp = &shortestPath{
				updatePath: []graph.Node{toNode},
				weight:     0,
			}
			break
		}
		updatePath, weight := path.DijkstraFromTo(fromNode, toNode, g)
		if len(updatePath) == 0 {
			continue
		}
		if sp == nil || weight < sp.weight {
			sp = &shortestPath{updatePath: updatePath, weight: weight}
		}
	}
	if sp == nil {
		return nil, fmt.Errorf("no paths found from node %s to any other desired node", fromNode.NVR())
	}
	var (
		up          UpdatePlan
		curNode     = fromNode
		curPlatform = fromPlatform
	)
	for i := 1; i < len(sp.updatePath); i++ {
		to := sp.updatePath[i].(*Node)
		up = append(up, NodeUpdateStep{
			FromNode: curNode,
			ToNode:   to,
		})
		curNode = to
	}

	for curPlatform.Compare(toPlatform) != 0 {
		nextPlatform := MajorMinor{Major: curPlatform.Major, Minor: curPlatform.Minor + 1}
		up = append(up, PlatformUpdateStep{
			PlatformName: "OpenShift",
			FromPlatform: curPlatform,
			ToPlatform:   nextPlatform,
		})
		curPlatform = nextPlatform
	}

	return up, nil
}
