package docker

import "github.com/docker/docker/api/types/container"

// portFixture keeps the port tests readable without importing the SDK
// type into every table row.
type portFixture struct {
	Type    string
	Private uint16
	Public  uint16
}

func toPorts(in []portFixture) []container.Port {
	out := make([]container.Port, 0, len(in))
	for _, p := range in {
		out = append(out, container.Port{PrivatePort: p.Private, PublicPort: p.Public, Type: p.Type})
	}
	return out
}
