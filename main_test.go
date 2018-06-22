package main

import (
	"testing"
)

func TestFindCandidates(t *testing.T) {

	bastion := &instance{
		ID:        "bastion1",
		Name:      "bastion1",
		Role:      "",
		PublicIP:  "",
		PrivateIP: "",
		IsNat:     false,
		Cluster:   "cluster1",
	}

	server1 := &instance{
		ID:        "server1",
		Name:      "server1",
		Role:      "",
		PublicIP:  "",
		PrivateIP: "",
		IsNat:     false,
		Cluster:   "cluster1",
		Bastions:  []*instance{bastion},
	}

	servers := []*instance{server1}

	paths := getCandidates([]string{"server1"}, servers)

	if len(paths) != 1 {
		t.Errorf("Expected 1 jump path, got %d", len(paths))
	}

	if paths[0].Instance.Name != server1.Name {
		t.Errorf("Expected candidate instance name to be %s, got %s", server1.Name, paths[0].Instance.Name)
	}

	if paths[0].Bastion.Name != bastion.Name {
		t.Errorf("Expected candidate instance name to be %s, got %s", bastion.Name, paths[0].Bastion.Name)
	}
}
