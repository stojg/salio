package main

import (
	"bufio"
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/dgryski/go-fuzzstr"
	"math/rand"
	"os"
	"strconv"
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
	IsNat     bool
	Cluster   string
	Bastions  []*Instance
}

type JumpPath struct {
	Bastion  *Instance
	Instance *Instance
}

/**
 * usage: salio -p playpen -r ap-southeast-2 cluster stack env
 */
func main() {

	if len(os.Args) < 2 {
		os.Exit(1)
	}

	flags := make(map[string]string, 0)
	var searchTerms []string

	args := os.Args[1:]

	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-") {
			flags[args[i]] = args[i+1]
			i++
		} else {
			searchTerms = append(searchTerms, args[i])
		}
	}
	searchTerm := strings.Join(searchTerms, ".")

	if val, ok := flags["-p"]; ok {
		os.Setenv("AWS_PROFILE", val)
	}

	if val, ok := flags["-r"]; ok {
		os.Setenv("AWS_REGION", val)
	}

	config := &aws.Config{}
	if os.Getenv("AWS_REGION") == "" {
		config.Region = aws.String("ap-southeast-2")
	}

	instances := fetchInstances(config)

	target := findInstanceName(searchTerm, instances)

	candidates := findCandidates(target, instances)

	if len(candidates) == 0 {
		fmt.Println("No path found")
		os.Exit(0)
	}

	for idx := range candidates {
		fmt.Printf("%d. %s %s %s\n", idx+1, candidates[idx].Instance.ID, candidates[idx].Instance.Name, candidates[idx].Instance.PrivateIP)
	}

	serverIndex := "1"
	reader := bufio.NewReader(os.Stdin)
	if len(candidates) == 1 {
		fmt.Print("[enter] to continue")
		reader.ReadString('\n')
	} else {
		fmt.Print("pick server # and then [enter] to continue: ")
		serverIndex, _ = reader.ReadString('\n')
	}
	id, err := strconv.Atoi(strings.Replace(serverIndex, "\n", "", 1))
	if err != nil {
		fmt.Println(err)
	}
	if id < 1 || id > len(candidates) {
		fmt.Println("I cannot do that Dave.")
		os.Exit(1)
	}
	id -= 1

	fmt.Printf("jumping to %s (%s) via %s (%s)\n\n", candidates[id].Instance.Name, candidates[id].Instance.PrivateIP, candidates[id].Bastion.Name, candidates[id].Bastion.PublicIP)

	sshClient, err := NewTunnelledSSHClient("admin", candidates[id].Bastion.PublicIP, candidates[id].Instance.PrivateIP, true)
	if err != nil {
		fmt.Printf("%s", err)
		os.Exit(1)
	}

	err = Shell(sshClient)
	if err != nil {
		fmt.Printf("%s", err)
		os.Exit(1)
	}
}

func findCandidates(target string, instances []*Instance) []*JumpPath {
	s1 := rand.NewSource(time.Now().UnixNano())
	random := rand.New(s1)
	var candidates []*JumpPath
	for _, instance := range instances {
		if instance.Name != target {
			continue
		}
		if len(instance.Bastions) < 1 {
			fmt.Printf("No bastion servers found for %s\n", instance.Name)
			continue
		}
		index := random.Intn(len(instance.Bastions))
		candidates = append(candidates, &JumpPath{
			Bastion:  instance.Bastions[index],
			Instance: instance,
		})
	}
	return candidates
}

func findInstanceName(targetName string, instances []*Instance) string {

	for _, i := range instances {
		if i.Name == targetName {
			return targetName
		}
	}

	var instanceNames []string
	for _, i := range instances {
		instanceNames = append(instanceNames, i.Name)
	}

	fuzzIndex := fuzzstr.NewIndex(instanceNames)
	postings := fuzzIndex.Query(targetName)
	for i := 0; i < len(postings); i++ {
		return instanceNames[postings[i].Doc]
	}
	return ""
}

func fetchInstances(config *aws.Config) []*Instance {
	var instances []*Instance

	s, err := session.NewSession(config)
	if err != nil {
		panic(err)
	}

	svc := ec2.New(s, &aws.Config{})
	resp, err := svc.DescribeInstances(nil)
	if err != nil {
		panic(err)
	}
	for idx := range resp.Reservations {
		for _, inst := range resp.Reservations[idx].Instances {
			i := NewInstance(inst)
			instances = append(instances, i)
		}
	}
	// find the bastion for each instance that is in a private subnet
	for _, i := range instances {
		if i.IsNat {
			continue
		}
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
			i.Bastions = append(i.Bastions, j)
		}
	}

	return instances
}

// NewInstance creates a new Instance struct from an AWS describeInstances call
func NewInstance(inst *ec2.Instance) *Instance {
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
	return i
}
