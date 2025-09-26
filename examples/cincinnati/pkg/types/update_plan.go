package types

import "fmt"

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
