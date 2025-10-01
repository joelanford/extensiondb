package graph

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
	wg simple.WeightedDirectedGraph

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

	g := &Graph{wg: *wg}
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

func (g *Graph) To(to *Node) iter.Seq[*Node] {
	return NodeIterator(g.wg.To(to.ID()))
}

func (g *Graph) From(from *Node) iter.Seq[*Node] {
	return NodeIterator(g.wg.From(from.ID()))
}

type WeightedEdge struct {
	From, To *Node
	Weight   float64
}

func (g *Graph) EdgeWeight(from, to *Node) float64 {
	w := g.wg.WeightedEdge(from.ID(), to.ID())
	if w == nil {
		return math.Inf(1)
	}
	return w.Weight()
}

func (g *Graph) FirstNodeMatching(match NodePredicate) *Node {
	for n := range NodeIterator(g.wg.Nodes()) {
		if match(g, n) {
			return n
		}
	}
	return nil
}

func (g *Graph) NodesMatching(match NodePredicate) iter.Seq[*Node] {
	it := NodeIterator(g.wg.Nodes())
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
	for range NodeIterator(g.wg.From(n.ID())) {
		return false
	}
	return true
}

func (g *Graph) buildEdges(cfg GraphConfig) error {
	var (
		streamsByMajorMinor = util.KeySlice(cfg.Streams, func(s VersionStream) MajorMinor { return s.Version })
		nodesByReleaseDate  = slices.SortedFunc(NodeIterator(g.wg.Nodes()), func(a, b *Node) int { return a.ReleaseDate.Compare(b.ReleaseDate) })
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

		// We don't know from's full set of successors yet. For now, set weight to 1.
		// Once all edges have been set, we can make a second pass to set better weights.
		edge := simple.WeightedEdge{F: from, T: to, W: 1}
		g.wg.SetWeightedEdge(edge)
	}
}

// assignEdgeWeights assigns edge weights to prioritize updating through supported nodes and to higher versions
// (in that order). It assigns a rank to each node (higher nodes have better support phase and higher versions), and
// then assigns all incoming edge weights as that node's rank.
//
// In order to guarantee that all paths with worse support are worse than all paths with better support,
// assignEdgeWeights create gaps between ranks when support tiers are crossed. For example, if there are 3 nodes with
// "full" support with ranks 1, 2, and 3, then traversing upgrades 3 -> 2 -> 1 would have a total sum of 6. Therefore,
// the best "maintenance" support node needs rank 7 to ensure that all paths through a single "maintenance" support
// node are worse than the worst path through all "full" supports nodes.
func (g *Graph) assignEdgeWeights() {
	bestNodes := slices.SortedFunc(NodeIterator(g.wg.Nodes()), func(a *Node, b *Node) int {
		if v := b.LifecyclePhase.Compare(a.LifecyclePhase); v != 0 {
			return v
		}
		return b.Compare(a)
	})

	const delta = 0.01
	var (
		rank                   = float64(0)
		nextLifecyclePhaseRank = float64(0)
		curLifecyclePhase      = LifeCyclePhaseUnknown
	)
	for _, to := range bestNodes {
		if curLifecyclePhase != to.LifecyclePhase {
			curLifecyclePhase = to.LifecyclePhase

			rank = nextLifecyclePhaseRank
			nextLifecyclePhaseRank = 0
		}
		rank += delta
		nextLifecyclePhaseRank += rank
		for from := range NodeIterator(g.wg.To(to.ID())) {
			g.wg.RemoveEdge(from.ID(), to.ID())
			g.wg.SetWeightedEdge(simple.WeightedEdge{F: from, T: to, W: rank})
		}
	}
}
