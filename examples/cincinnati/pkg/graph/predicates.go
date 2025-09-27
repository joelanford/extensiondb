package graph

import "github.com/blang/semver/v4"

type NodePredicate func(*Graph, *Node) bool

func AllNodes() NodePredicate {
	return func(*Graph, *Node) bool { return true }
}

func NodeInRange(rng semver.Range) NodePredicate {
	return func(_ *Graph, actual *Node) bool {
		return rng(actual.Version)
	}
}

type EdgePredicate func(*Graph, *Node, *Node, float64) bool

func AllEdges() EdgePredicate {
	return func(*Graph, *Node, *Node, float64) bool { return true }
}
