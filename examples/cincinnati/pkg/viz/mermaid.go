package viz

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/joelanford/extensiondb/examples/cincinnati/pkg/graph"
	"github.com/joelanford/extensiondb/examples/cincinnati/pkg/util"
	"github.com/lucasb-eyer/go-colorful"
	ggraph "gonum.org/v1/gonum/graph"
)

type MermaidConfig struct {
	KeepNode graph.NodePredicate
	KeepEdge graph.EdgePredicate

	NodeText  func(*graph.Graph, *graph.Node) string
	NodeStyle func(*graph.Graph, *graph.Node) string

	EdgeStyle func(*graph.Graph, *graph.Node, *graph.Node, float64) string
}

func defaultMermaidConfig(m *MermaidConfig) {
	if m == nil {
		m = &MermaidConfig{}
	}
	if m.KeepNode == nil {
		m.KeepNode = graph.AllNodes()
	}
	if m.KeepEdge == nil {
		m.KeepEdge = graph.AllEdges()
	}
	if m.NodeText == nil {
		m.NodeText = func(_ *graph.Graph, n *graph.Node) string {
			return n.VR()
		}
	}

	if m.NodeStyle == nil {
		m.NodeStyle = defaultNodeStyle()
	}
	if m.EdgeStyle == nil {
		m.EdgeStyle = defaultEdgeStyle()
	}
}

func defaultNodeStyle() func(*graph.Graph, *graph.Node) string {
	return func(g *graph.Graph, node *graph.Node) string {
		fullSupportNodes := g.NodesMatching(func(_ *graph.Graph, n *graph.Node) bool {
			return n.LifecyclePhase == graph.LifecyclePhaseFullSupport
		})

		hasPathToFullSupport := false
		for to := range fullSupportNodes {
			sp, _, _ := g.Paths().Between(node.ID(), to.ID())
			if len(sp) > 0 {
				hasPathToFullSupport = true
				break
			}
		}

		warningStyle := ""
		if !hasPathToFullSupport {
			warningStyle = ",stroke:#ff0000,stroke-width:3px"
		}

		lfp := node.LifecyclePhase
		fillColor := colorForLifecyclePhase(lfp)
		textColor := colorful.LinearRgb(0, 0, 0)
		fh, fs, fl := fillColor.Hsl()
		fl *= .9

		if g.Heads().Has(node) {
			fl = 1 - fl
			textColor = colorful.LinearRgb(.95, .95, .95)
		}
		return fmt.Sprintf("fill:%s,color:%s%s", colorful.Hsl(fh, fs, fl).Hex(), textColor.Hex(), warningStyle)
	}
}

func defaultEdgeStyle() func(*graph.Graph, *graph.Node, *graph.Node, float64) string {
	return func(g *graph.Graph, from *graph.Node, to *graph.Node, _ float64) string {
		shortestPathTo := map[*graph.Node][]*graph.Node{}
		for head := range g.Heads() {
			sp, _, _ := g.Paths().Between(from.ID(), head.ID())
			if len(sp) == 0 {
				continue
			}
			shortestPathTo[head] = util.MapSlice(sp[1:], func(i ggraph.Node) *graph.Node {
				n := i.(*graph.Node)
				return n
			})
		}

		for head, sp := range util.OrderedMap(shortestPathTo, func(a, b *graph.Node) int { return b.Compare(a) }) {
			nextHop := sp[0]
			if nextHop != to {
				continue
			}
			headColor := colorForLifecyclePhase(head.LifecyclePhase)
			h, _, _ := headColor.Hsl()
			headColor = colorful.Hsl(h, 1, .3)
			return fmt.Sprintf("stroke:%s,stroke-width:2px", headColor.Hex())
		}
		return "stroke:gray,fill:none,stroke-width:0.5px,stroke-dasharray:4;"
	}
}

func Mermaid(g *graph.Graph, cfg MermaidConfig) string {
	defaultMermaidConfig(&cfg)

	var sb strings.Builder
	sb.WriteString("graph LR\n")

	bundleMinorVersions := map[graph.MajorMinor][]*graph.Node{}

	for _, n := range slices.SortedFunc(g.NodesMatching(graph.AllNodes()), util.Compare) {
		if !cfg.KeepNode(g, n) {
			continue
		}
		mm := graph.NewMajorMinorFromVersion(n.Version)
		bundleMinorVersions[mm] = append(bundleMinorVersions[mm], n)
	}

	nodeClasses := map[string]string{}
	edgeCount := 0
	edgeStyles := map[string][]string{}

	for mm, vGroup := range util.OrderedMap(bundleMinorVersions, util.Compare) {
		subgraphString := fmt.Sprintf("%s", mm)
		sb.WriteString(fmt.Sprintf("\n  subgraph %s[\"%s (%s)\"]\n", subgraphString, mm, vGroup[0].LifecyclePhase.String()))
		for _, to := range vGroup {
			style := cfg.NodeStyle(g, to)
			class := "default"
			if style != "" {
				class = fmt.Sprintf("c%x", util.HashString(style))
				nodeClasses[class] = style
			}

			// TODO: Use cfg.NodeText
			sb.WriteString(fmt.Sprintf("    %s:::%s\n", to.VR(), class))

			for _, from := range slices.SortedFunc(g.To(to), util.Compare) {
				weight := g.EdgeWeight(from, to)
				if !cfg.KeepNode(g, from) {
					continue
				}
				if !cfg.KeepEdge(g, from, to, weight) {
					continue
				}

				edgeStyle := cfg.EdgeStyle(g, from, to, weight)
				edgeStyles[edgeStyle] = append(edgeStyles[edgeStyle], strconv.Itoa(edgeCount))
				sb.WriteString(fmt.Sprintf("    %s --> %s\n", from.VR(), to.VR()))
				edgeCount++
			}
		}
		sb.WriteString("  end\n")
		sb.WriteString(fmt.Sprintf("  style %s %s\n", subgraphString, fillStyle(vGroup[0].LifecyclePhase)))
	}

	for class, style := range util.OrderedMap(nodeClasses, cmp.Compare) {
		sb.WriteString(fmt.Sprintf("  classDef %s %s;\n", class, style))
	}

	for style, edges := range util.OrderedMap(edgeStyles, cmp.Compare) {
		sb.WriteString(fmt.Sprintf("  linkStyle %s %s;\n", strings.Join(edges, ","), style))
	}

	return sb.String()
}

func fillStyle(lfp graph.LifecyclePhase) string {
	return fmt.Sprintf("fill:%s", colorForLifecyclePhase(lfp).Hex())
}

func colorForLifecyclePhase(lfp graph.LifecyclePhase) colorful.Color {
	switch lfp {
	case graph.LifecyclePhasePreGA:
		return colorful.LinearRgb(1, 1, 1)
	case graph.LifecyclePhaseFullSupport:
		return colorful.Hsl(100, 1, .9)
	case graph.LifecyclePhaseMaintenance:
		return colorful.Hsl(60, 1, .9)
	case graph.LifecyclePhaseEndOfLife:
		return colorful.Hsl(360, 1, .9)
	case 2:
		return colorful.Hsl(170, 1, .9)
	case 3:
		return colorful.Hsl(240, 1, .9)
	}
	panic("unknown lifecycleState: " + lfp.String())
}
