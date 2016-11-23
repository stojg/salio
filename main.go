package main

import (
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/dgryski/go-fuzzstr"
	"math/rand"
	"os"
	"strings"
	"time"
)

type Instance struct {
	ID        string
	Name      string
	Role      string
	Tags      map[string]string
	PublicIP  string
	PrivateIP string
	SubnetID  string
	HaveNAT   bool
	IsNat     bool
	Cluster   string
	NATs      []*Instance
}

type Endpoint struct {
	Jump     *Instance
	Instance *Instance
}

var instances []*Instance

/**
 * usage: AWS_PROFILE=playpen AWS_REGION=ap-southeast-2 aws-ssh-login
 */
func main() {

	if len(os.Args) < 2 {
		os.Exit(1)
	}

	config := &aws.Config{}
	if os.Getenv("AWS_REGION") == "" {
		config.Region = aws.String("ap-southeast-2")
	}

	searchInstance := strings.Join(os.Args[1:], ".")
	s1 := rand.NewSource(time.Now().UnixNano())
	random := rand.New(s1)

	sess, err := session.NewSession(config)
	if err != nil {
		panic(err)
	}
	//pubSubnets := fetchRouteTables(sess)
	instances = append(instances, fetchInstances(sess)...)
	var instanceNames []string

	for _, i := range instances {
		if i.IsNat {
			continue
		}
		instanceNames = append(instanceNames, i.Name)

		for _, j := range instances {
			if j.ID == i.ID {
				continue
			}
			if !j.IsNat {
				continue
			}
			if i.Cluster != j.Cluster {
				continue
			}
			i.NATs = append(i.NATs, j)
		}
	}

	var candidates []*Endpoint

	var targetInstance string
	for _, i := range instances {
		if i.Name == searchInstance {
			targetInstance = searchInstance
			break
		}
	}

	idx := fuzzstr.NewIndex(instanceNames)
	if targetInstance == "" {
		postings := idx.Query(searchInstance)
		for i := 0; i < len(postings); i++ {
			targetInstance = instanceNames[postings[i].Doc]
			break
		}
	}

	for _, i := range instances {
		if i.Name != targetInstance {
			continue
		}
		index := random.Intn(len(i.NATs))
		natInstance := i.NATs[index]
		candidates = append(candidates, &Endpoint{
			Jump:     natInstance,
			Instance: i,
		})
	}

	if len(candidates) == 0 {
		fmt.Println("No path found")
	}

	for idx := range candidates {
		fmt.Printf("%s %s %s %s\n", candidates[idx].Instance.ID, candidates[idx].Instance.Name, candidates[idx].Jump.PublicIP, candidates[idx].Instance.PrivateIP)
	}
}

func fetchRouteTables(sess *session.Session) []string {
	svc := ec2.New(sess, &aws.Config{})
	resp, err := svc.DescribeRouteTables(nil)
	if err != nil {
		panic(err)
	}

	var publicSubnets []string

	for _, routeTable := range resp.RouteTables {
		if len(routeTable.Associations) < 1 {
			continue
		}

		var public bool

		for _, route := range routeTable.Routes {
			if route.GatewayId != nil && *route.GatewayId != "local" {
				public = true
			}
		}
		if !public {
			continue
		}

		var assocSubnets []string
		for _, assoc := range routeTable.Associations {
			if assoc.SubnetId == nil {
				continue
			}
			assocSubnets = append(assocSubnets, *assoc.SubnetId)
		}

		if len(assocSubnets) < 1 {
			continue
		}
		publicSubnets = append(publicSubnets, assocSubnets...)
	}
	return publicSubnets
}

func fetchInstances(sess *session.Session) []*Instance {
	var instances []*Instance
	svc := ec2.New(sess, &aws.Config{})
	resp, err := svc.DescribeInstances(nil)
	if err != nil {
		panic(err)
	}
	for idx := range resp.Reservations {
		for _, inst := range resp.Reservations[idx].Instances {
			i := &Instance{
				ID:   *inst.InstanceId,
				Tags: make(map[string]string, 0),
			}

			if inst.PrivateIpAddress != nil {
				i.PrivateIP = *inst.PrivateIpAddress
			}

			if inst.PublicIpAddress != nil {
				i.PublicIP = *inst.PublicIpAddress
			}
			if inst.SubnetId != nil {
				i.SubnetID = *inst.SubnetId
			}

			for k := range inst.Tags {
				i.Tags[*inst.Tags[k].Key] = *inst.Tags[k].Value
			}
			if name, ok := i.Tags["Name"]; ok {
				i.Name = name
				names := strings.Split(name, ".")
				if names[0] != "" {
					i.Cluster = names[0]
				}
			}
			if role, ok := i.Tags["role"]; ok {
				i.Role = role
				if role == "nat" {
					i.IsNat = true
				}
			}

			//fmt.Printf("%s %s - %s - %s - %s\n", i.Name, i.PrivateIP, i.PublicIP, i.Role, i.SubnetID)
			instances = append(instances, i)
		}
	}
	return instances
}
