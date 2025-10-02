package graph

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/joelanford/extensiondb/examples/cincinnati/pkg/util"
	"gonum.org/v1/gonum/graph"
	"k8s.io/apimachinery/pkg/util/sets"
)

type PlatformUpdate struct {
	Name        string
	From        MajorMinor
	To          MajorMinor
	NodeUpdates []PlatformNodeUpdate
}

func (pu *PlatformUpdate) PrettyReport() string {
	var sb strings.Builder

	slices.SortFunc(pu.NodeUpdates, func(a, b PlatformNodeUpdate) int {
		return cmp.Compare(a.From.Name, b.From.Name)
	})

	sb.WriteString(fmt.Sprintf("======== Plan for %s update from %s to %s ========\n\n", pu.Name, pu.From, pu.To))
	if len(pu.NodeUpdates) > 0 {
		sb.WriteString("Currently installed packages included in update plan:\n")
		for _, pnu := range pu.NodeUpdates {
			sb.WriteString(fmt.Sprintf("  - %s\n", pnu.From.NVR()))
		}
	}
	sb.WriteString("\n")

	var errUpdates []PlatformNodeUpdate
	for _, nu := range pu.NodeUpdates {
		if nu.Error != nil {
			errUpdates = append(errUpdates, nu)
		}
	}
	if len(errUpdates) > 0 {
		sb.WriteString("❌  Issues are currently blocking update:\n")
		for _, nu := range errUpdates {
			sb.WriteString(fmt.Sprintf("  - %s: %v\n", nu.From.NVR(), nu.Error))
		}
		return sb.String()
	}

	sb.WriteString(fmt.Sprintln("----- Phase 1: Suggested package updates before platform update: -----"))
	for _, nu := range pu.NodeUpdates {
		buildPrettyNodeUpdate(&sb, nu.From, nu.Before)
	}
	sb.WriteString("\n")

	sb.WriteString(fmt.Sprintln("----- Phase 2: Platform update: -----"))
	sb.WriteString(fmt.Sprintf("  ⬆  %s: %s -> %s\n\n", pu.Name, pu.From, pu.To))

	sb.WriteString(fmt.Sprintln("----- Phase 3: Suggested package updates after platform update: -----"))
	for _, nu := range pu.NodeUpdates {
		buildPrettyNodeUpdate(&sb, nu.From, nu.After)
	}
	sb.WriteString("\n")

	return sb.String()
}

func buildPrettyNodeUpdate(sb *strings.Builder, from *Node, path []*Node) {
	if len(path) > 1 {
		sb.WriteString(fmt.Sprintf("  ⬆  %s: %s\n", from.Name, strings.Join(util.MapSlice(path, func(n *Node) string { return n.VR() }), " -> ")))
	} else {
		sb.WriteString(fmt.Sprintf("  ✅️ %s: No updates necessary\n", from.NVR()))
	}
}

type PlatformNodeUpdate struct {
	From   *Node
	Before []*Node
	After  []*Node
	Error  error
}

func (g *Graph) PlanOpenShiftUpdate(froms []*Node, fromPlatform, toPlatform MajorMinor) (*PlatformUpdate, error) {
	if err := validateOpenShiftUpdate(fromPlatform, toPlatform); err != nil {
		return nil, err
	}

	var traversedPlatforms []MajorMinor
	for curPlatform := fromPlatform; curPlatform.Compare(toPlatform) <= 0; curPlatform.Minor++ {
		traversedPlatforms = append(traversedPlatforms, curPlatform)
	}

	var pnus []PlatformNodeUpdate
	for _, from := range froms {
		pnus = append(pnus, g.platformNodeUpdatePathFrom(from, traversedPlatforms))
	}
	return &PlatformUpdate{Name: "OpenShift", From: fromPlatform, To: toPlatform, NodeUpdates: pnus}, nil
}

func validateOpenShiftUpdate(from MajorMinor, to MajorMinor) error {
	diff := from.Compare(to)
	if diff == 0 {
		return fmt.Errorf("platform update requires different from version (%s) and to version (%s)", from.String(), to.String())
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

func (g *Graph) platformNodeUpdatePathFrom(from *Node, traversedPlatforms []MajorMinor) PlatformNodeUpdate {
	type updatePath struct {
		p []*Node
		w float64
	}

	// If the from node is not supported on the current platform version, that issue needs to somehow be resolved
	// before planning a platform update.
	if !from.SupportedPlatformVersions.Has(traversedPlatforms[0]) {
		return PlatformNodeUpdate{
			From:  from,
			Error: errors.New("This version is not supported on the current platform version"),
		}
	}

	traversedPlatformsSet := sets.New[MajorMinor](traversedPlatforms...)
	fromPlatform := traversedPlatforms[0]
	toPlatform := traversedPlatforms[len(traversedPlatforms)-1]
	fromPlatformSet := sets.New[MajorMinor](fromPlatform)
	toPlatformSet := sets.New[MajorMinor](toPlatform)

	// find all update paths into nodes supported on the toPlatform
	var updatePaths []updatePath
	for to := range g.NodesMatching(AndNodes(
		PackageNodes(from.Name),
		supportedOnPlatforms(toPlatformSet),
	)) {
		p, w, _ := g.Paths().Between(from.ID(), to.ID())
		if w == math.Inf(1) {
			continue
		}
		updatePaths = append(updatePaths, updatePath{
			p: util.MapSlice(p, func(n graph.Node) *Node { return n.(*Node) }),
			w: w,
		})
	}

	// Sort update paths by weight (then by number of updates)
	slices.SortFunc(updatePaths, func(a, b updatePath) int {
		if v := cmp.Compare(a.w, b.w); v != 0 {
			return v
		}
		return cmp.Compare(len(a.p), len(b.p))
	})

	// For each update path:
	//   1. Walk nodes from beginning to end until a node is not functional on the "from platform".
	//      These nodes form the pre-update path.
	//   2. The last node in the pre-update path is the node that will be installed when the platform
	//      is being updated. We call this the "spanNode". We need to check to make sure that this
	//      node is functional on all traversed platform versions. If it isn't, this update path is
	//      not viable, and we move on to the next candidate update path.
	//   3. Continue walking the remaining nodes, now checking that they all support the "to platform".
	//      These nodes form the post-update path. If the remaining nodes on the path do not all support
	//      the "to platform", then this update path is also non-viable, and we move on to the next
	//      candidate path.

	for _, p := range updatePaths {
		pnu := PlatformNodeUpdate{
			From: from,
		}

		// 1. Build pre-update path
		for _, n := range p.p {
			if !functionalOnPlatforms(fromPlatformSet)(g, n) {
				break
			}
			pnu.Before = append(pnu.Before, n)
		}

		// 2. Identify span node and check for compatibility across all platform versions.
		spanNode := pnu.Before[len(pnu.Before)-1]
		if !functionalOnPlatforms(traversedPlatformsSet)(g, spanNode) {
			// This path won't work, so move on to the next possible path.
			continue
		}

		// 3. Build the post-update path
		if len(pnu.Before) == len(p.p) {
			// If the entire update can happen prior to the platform update, we're done!
			return pnu
		}
		if len(pnu.Before) != 0 {
			// The span node shows up as the last node in the pre-update path.
			// This ensures that the span node shows up again as the first node
			// of the post-update path.
			pnu.After = append(pnu.After, spanNode)
		}
		for _, n := range p.p[len(pnu.Before):] {
			if !functionalOnPlatforms(toPlatformSet)(g, n) {
				break
			}
			pnu.After = append(pnu.After, n)
		}

		// NOTE: we know that the final node in the update path is supported on the "to platform" because that criteria
		// was used originally when constructing the candidate update paths. Therefore, there is no need to check the
		// last post-update node again for "to platform" support.

		if len(pnu.Before)+len(pnu.After)-1 != len(p.p) {
			// We know we have an invalid path if not all nodes from the original
			// path show up in the before/after path (with the span node
			// showing up twice). We subtract 1 to make sure the span node is not
			// double-counted.
			continue
		}
		return pnu
	}

	// At this point, not a single candidate update path was viable, so report this error in the node update.
	return PlatformNodeUpdate{
		From:  from,
		Error: errors.New("No update paths from this version coincide with the support along the platform update path"),
	}
}

func supportedOnPlatforms(platforms sets.Set[MajorMinor]) NodePredicate {
	return func(_ *Graph, n *Node) bool {
		return n.SupportedPlatformVersions.IsSuperset(platforms)
	}
}
func functionalOnPlatforms(platforms sets.Set[MajorMinor]) NodePredicate {
	return func(_ *Graph, n *Node) bool {
		nodeFunctionalPlatforms := n.SupportedPlatformVersions.Union(n.RequiresUpdatePlatformVersions)
		return nodeFunctionalPlatforms.IsSuperset(platforms)
	}
}
