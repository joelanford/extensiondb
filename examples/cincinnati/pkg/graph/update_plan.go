package graph

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"

	"github.com/joelanford/extensiondb/examples/cincinnati/pkg/util"
	"gonum.org/v1/gonum/graph"
	"k8s.io/apimachinery/pkg/util/sets"
)

type UpdatePlan struct {
	BeforePlatform []NodeUpdateStep   `json:"beforePlatform,omitempty"`
	Platform       PlatformUpdateStep `json:"platform,omitempty"`
	AfterPlatform  []NodeUpdateStep   `json:"afterPlatform,omitempty"`
}

type PlatformUpdateStep struct {
	Name  string     `json:"name"`
	From  MajorMinor `json:"from"`
	To    MajorMinor `json:"to"`
	Error error      `json:"error,omitempty"`
}

type NodeUpdateStep struct {
	From  *Node
	Path  []*Node
	Error error `json:"error,omitempty"`
}

type NodeUpdateRequest struct {
	From *Node
	To   NodePredicate
}

// PlanOpenShiftPlatformUpgrade plans an update from fromPlatform to toPlatform with consideration for the nodeUpdates.
//
// If fromPlatform's minor version is even, toPlatform must be "minor+1" or "minor+2". If fromPlatform's minor version
// is odd, toPlatform must be "minor+1". Any other combination of fromPlatform and toPlatform results in an error.
//
// PlanOpenShiftPlatform interrogates the graph to find desired nodes that are supported on all platform
// versions that will be traversed during a platform update, and from that set chooses the shortest path for each
// node update.
//
// If the "from" node of a node update is supported on all traversed platform versions, the returned plan will not
// suggest any updates for that node.
func (g *Graph) PlanOpenShiftPlatformUpgrade(nodeUpdates []NodeUpdateRequest, fromPlatform MajorMinor, toPlatform MajorMinor) UpdatePlan {

	var up UpdatePlan
	up.Platform = PlatformUpdateStep{
		Name:  "OpenShift",
		From:  fromPlatform,
		To:    toPlatform,
		Error: validateOpenShiftUpdate(fromPlatform, toPlatform),
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
	type shortestPath struct {
		updatePath []*Node
		weight     float64
	}

	for _, nu := range nodeUpdates {
		// Get shortest path among all paths to desired nodes.
		var sp *shortestPath
		for toNode := range g.NodesMatching(AndNodes(PackageNodes(nu.From.Name), nu.To)) {
			if !toNode.SupportedPlatformVersions.IsSuperset(traversedPlatformVersions) {
				continue
			}
			updatePath, weight, _ := g.Paths().Between(nu.From.ID(), toNode.ID())
			if weight == math.Inf(1) {
				continue
			}
			if sp == nil || weight < sp.weight {
				sp = &shortestPath{util.MapSlice(updatePath, func(i graph.Node) *Node { return i.(*Node) }), weight}
			}
		}

		if sp == nil {
			opvs := strings.Join(
				util.MapSlice(
					slices.SortedFunc(maps.Keys(traversedPlatformVersions), util.Compare),
					func(mm MajorMinor) string { return mm.String() },
				),
				", ",
			)
			up.BeforePlatform = append(up.BeforePlatform, NodeUpdateStep{
				From:  nu.From,
				Error: fmt.Errorf("no update path destinations are supported on all traversed platform versions (%v)", opvs),
			})
			continue
		}
		nus := NodeUpdateStep{From: sp.updatePath[0], Path: sp.updatePath[1:]}
		up.BeforePlatform = append(up.BeforePlatform, nus)
	}
	return up
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

func (up *UpdatePlan) PrettyReport() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Update plan for %s %s to %s", up.Platform.Name, up.Platform.From, up.Platform.To))

	installedOperators := sets.New[string]()
	installedOperators.Insert(util.MapSlice(up.BeforePlatform, func(nus NodeUpdateStep) string { return nus.From.NVR() })...)
	installedOperators.Insert(util.MapSlice(up.AfterPlatform, func(nus NodeUpdateStep) string { return nus.From.NVR() })...)

	if installedOperators.Len() > 0 {
		sb.WriteString(fmt.Sprintf(", with installed operators %s\n", strings.Join(sets.List(installedOperators), ", ")))
	} else {
		sb.WriteString(fmt.Sprintln())
	}

	printNodeUpdateSteps := func(steps []NodeUpdateStep) {
		for _, s := range steps {
			if s.Error != nil {
				sb.WriteString(fmt.Sprintf("       ❌  Error: %s: %v\n", s.From.NVR(), s.Error))
			} else if len(s.Path) == 0 {
				sb.WriteString(fmt.Sprintf("       ✅️ No update necessary for %s\n", s.From.NVR()))
			} else {
				pathStr := strings.Join(util.MapSlice(s.Path, func(n *Node) string { return n.VR() }), " -> ")
				sb.WriteString(fmt.Sprintf("       ⬆  Update %s: %s -> %s\n", s.From.Name, s.From.VR(), pathStr))
			}
		}
	}

	// Pre-update steps
	if len(up.BeforePlatform) > 0 {
		sb.WriteString(fmt.Sprintf("  1. Pre-%s update steps:\n", up.Platform.Name))
		printNodeUpdateSteps(up.BeforePlatform)
	} else {
		sb.WriteString(fmt.Sprintf("  1. ✅️ No pre-%s update steps\n", up.Platform.Name))
	}

	// Platform update
	if up.Platform.Error != nil {
		sb.WriteString(fmt.Sprintf("  2. ❌  Error: %s\n", up.Platform.Error))
	} else if up.Platform.From == up.Platform.To {
		sb.WriteString(fmt.Sprintf("  2. ✅  No update necessary %s %s\n", up.Platform.Name, up.Platform.From))
	} else {
		sb.WriteString(fmt.Sprintf("  2. ⬆  Update %s: %s -> %s\n", up.Platform.Name, up.Platform.From, up.Platform.To))
	}

	// Post-update steps
	if len(up.AfterPlatform) > 0 {
		sb.WriteString(fmt.Sprintf("  3. Post-%s update steps:\n", up.Platform.Name))
		printNodeUpdateSteps(up.AfterPlatform)
	} else {
		sb.WriteString(fmt.Sprintf("  3. ✅️ No post-%s update steps\n", up.Platform.Name))
	}
	sb.WriteString(fmt.Sprintln())

	return sb.String()
}
