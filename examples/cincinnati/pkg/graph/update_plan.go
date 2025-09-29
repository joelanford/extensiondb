package graph

import (
	"fmt"
	"slices"
	"strings"

	"github.com/joelanford/extensiondb/examples/cincinnati/pkg/util"
	"gonum.org/v1/gonum/graph"
	"k8s.io/apimachinery/pkg/util/sets"
)

type UpdatePlan []UpdateStep

type UpdateStep interface {
	Name() string
	From() string
	To() string
}

type PlatformUpdateStep struct {
	PlatformName string
	FromPlatform MajorMinor
	ToPlatform   MajorMinor
}

func (us PlatformUpdateStep) Name() string {
	return us.PlatformName
}

func (us PlatformUpdateStep) From() string {
	return us.FromPlatform.String()
}

func (us PlatformUpdateStep) To() string {
	return us.ToPlatform.String()
}

type NodeUpdateStep struct {
	FromNode *Node
	ToNode   *Node
}

func (nu NodeUpdateStep) Name() string {
	if nu.FromNode.Name != nu.ToNode.Name {
		panic(fmt.Sprintf("invalid node update step: conflicting names: %s to %s", nu.FromNode.Name, nu.ToNode.Name))
	}
	return nu.FromNode.Name
}

func (nu NodeUpdateStep) From() string {
	return nu.FromNode.VR()
}

func (nu NodeUpdateStep) To() string {
	return nu.ToNode.VR()
}

// PlanOpenShiftPlatformUpgrade plans an update from fromPlatform to toPlatform with consideration for the node
// updates from fromNode.
//
// If fromPlatform's minor version is even, toPlatform must be "minor+1" or "minor+2". If fromPlatform's minor version
// is odd, toPlatform must be "minor+1". Any other combination of fromPlatform and toPlatform results in an error.
//
// PlanOpenShiftPlatform interrogates the graph to find desired nodes that are supported on all platform
// versions that will be traversed during a platform update, and from that set chooses the shortest path from
// fromNode.
//
// If fromNode is supported on all traversed platform versions, the returned plan will not suggest any node
// updates.
func (g *Graph) PlanOpenShiftPlatformUpgrade(fromNode *Node, desiredToNodes NodePredicate, fromPlatform MajorMinor, toPlatform MajorMinor) (UpdatePlan, error) {
	if desiredToNodes == nil {
		desiredToNodes = AllNodes()
	}

	if err := validateOpenShiftUpdate(fromPlatform, toPlatform); err != nil {
		return nil, fmt.Errorf("invalid openshift platform update: %v", err)
	}

	traversedPlatformVersions := sets.New[MajorMinor]()
	for curPlatform := fromPlatform; curPlatform.Compare(toPlatform) <= 0; curPlatform.Minor++ {
		traversedPlatformVersions.Insert(curPlatform)
	}

	// TODO: This planning algorithm assumes that extension upgrades will happen before a platform upgrade and never
	//   after a platform upgrade. But platform-aligned extensions may want their users to:
	//     - traverse a platform update to X.Y with an extension aligned to X.Y-1
	//     - after the platform update to X.Y succeeds, update the extension to a version aligned with X.Y
	//   To do this, we need to have more extension metadata to distinguish between "fully supported on X.Y" and
	//   "functionally works on X.Y but only supported as an intermediate state on the way to the X.Y-aligned version"

	var candidateNodes []*Node
	for toNode := range NodeIterator(g.Nodes()) {
		if !desiredToNodes(g, toNode) {
			continue
		}

		if !toNode.SupportedPlatformVersions.IsSuperset(traversedPlatformVersions) {
			continue
		}
		candidateNodes = append(candidateNodes, toNode)
	}
	if len(candidateNodes) == 0 {
		orderedPlatformVersions := traversedPlatformVersions.UnsortedList()
		slices.SortFunc(orderedPlatformVersions, util.Compare)
		orderedPlatformVersionStrs := util.MapSlice(orderedPlatformVersions, func(mm MajorMinor) string { return mm.String() })
		return nil, fmt.Errorf("no desired extensions are supported on all traversed platform versions (%v)", strings.Join(orderedPlatformVersionStrs, ", "))
	}

	type shortestPath struct {
		updatePath []graph.Node
		weight     float64
	}
	var sp *shortestPath
	for _, toNode := range candidateNodes {
		updatePath, weight, _ := g.Paths().Between(fromNode.ID(), toNode.ID())
		if sp == nil || weight < sp.weight {
			sp = &shortestPath{updatePath: updatePath, weight: weight}
		}
	}
	if sp == nil {
		return nil, fmt.Errorf("no paths found from node %s to any other desired node", fromNode.NVR())
	}
	var (
		up      UpdatePlan
		curNode = fromNode
	)
	for i := 1; i < len(sp.updatePath); i++ {
		to := sp.updatePath[i].(*Node)
		up = append(up, NodeUpdateStep{
			FromNode: curNode,
			ToNode:   to,
		})
		curNode = to
	}
	if fromPlatform != toPlatform {
		up = append(up, PlatformUpdateStep{
			PlatformName: "OpenShift",
			FromPlatform: fromPlatform,
			ToPlatform:   toPlatform,
		})
	}

	return up, nil
}

func validateOpenShiftUpdate(from MajorMinor, to MajorMinor) error {
	diff := from.Compare(to)
	if diff == 0 {
		return nil
	}
	if diff > 0 {
		return fmt.Errorf("downgrading from %s to %s is not supported", from.String(), to.String())
	}
	if from.Major != to.Major {
		return fmt.Errorf("updating from major version %d to major version %d is not supported", from.Major, to.Major)
	}

	evenMinor := from.Minor%2 == 0
	maxTo := MajorMinor{Major: from.Major, Minor: from.Minor + 1}
	if evenMinor {
		maxTo.Minor = from.Minor + 2
	}
	if to.Compare(maxTo) > 0 {
		return fmt.Errorf("furthest supported update from %s is to %s", from, maxTo)
	}

	return nil
}
