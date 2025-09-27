package graph

import (
	"fmt"

	"gonum.org/v1/gonum/graph"
	"gonum.org/v1/gonum/graph/path"
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

func (g *Graph) PlanOpenShiftPlatformUpgrade(fromNode *Node, desiredToNodes NodePredicate, fromPlatform MajorMinor, toPlatform MajorMinor) (UpdatePlan, error) {
	if desiredToNodes == nil {
		desiredToNodes = AllNodes()
	}

	var candidateNodes []*Node
	for toGraphNode := range NodeIterator(g.Nodes()) {
		if !desiredToNodes(g, toGraphNode) {
			continue
		}
		// TODO: Many operator support lifecycles will not support the final desired
		//  destination version on every platform between fromPlatform and toPlatform.
		//  ..
		//  This needs to be updated such that can have a plan like:
		//    1. Update operator
		//    2. Update platform
		//    3. Update operator
		//    4. Update platform
		//    5. (possibly) Update operator after upgrading to desired platform
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

	ocpSteps, err := ocpPlatformUpdatePath(fromPlatform, toPlatform)
	if err != nil {
		return nil, err
	}
	for _, ocpStep := range ocpSteps {
		up = append(up, ocpStep)
	}

	return up, nil
}

func ocpPlatformUpdatePath(from MajorMinor, to MajorMinor) ([]PlatformUpdateStep, error) {
	diff := from.Compare(to)
	if diff == 0 {
		return nil, nil
	}
	if diff > 0 {
		return nil, fmt.Errorf("invalid from and to: from is greater than to")
	}
	if from.Major != to.Major {
		return nil, fmt.Errorf("invalid from and to: cannot automatically update across major versions")
	}
	cur := from
	var steps []PlatformUpdateStep
	for cur != to {
		if cur.Minor%2 == 0 && cur.Minor+2 <= to.Minor {
			cur.Minor += 2
		} else {
			cur.Minor += 1
		}
		steps = append(steps, PlatformUpdateStep{
			PlatformName: "OpenShift",
			FromPlatform: from,
			ToPlatform:   cur,
		})
		from = cur
	}
	return steps, nil
}
