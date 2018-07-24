package main

import (
	"bufio"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"time"

	"sort"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/dgryski/go-fuzzstr"
)

const sshUserName = "admin"

var (
	version string
)

type instance struct {
	ID         string
	Name       string
	Role       string
	Tags       map[string]string
	PublicIP   string
	PrivateIP  string
	IsNat      bool
	Cluster    string
	Bastions   []*instance
	LaunchTime *time.Time
}

type instancePair struct {
	Bastion  *instance
	Instance *instance
}

/**
 * usage: salio -p playpen -r ap-southeast-2 cluster stack env
 */
func main() {

	if len(os.Args) < 2 {
		printUsageAndQuit(1)
	}

	flags := make(map[string]string, 0)
	var searchTerms []string

	args := os.Args[1:]

	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-") {
			if len(args) <= i+1 {
				printUsageAndQuit(1)
			}
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

	instances, err := fetchInstances(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error fetching ec2 instances: %s\n", err.Error())
		os.Exit(1)
	}

	targets := findInstanceNames(searchTerm, instances)

	candidates := getCandidates(targets, instances)

	sort.Sort(candidateSort(candidates))

	if len(candidates) == 0 {
		fmt.Println("No instances found")
		os.Exit(0)
	}

	longestName := 0
	for _, c := range candidates {
		if len(c.Instance.Name) > longestName {
			longestName = len(c.Instance.Name)
		}
	}

	for idx, c := range candidates {
		fmt.Printf("%3d. %-19s %s %-15s %s\n", idx+1, c.Instance.ID, padToLen(c.Instance.Name, " ", longestName), c.Instance.PrivateIP, c.Instance.LaunchTime.Local().Format("2006-01-02 15:04"))
	}

	fmt.Print("pick server # and then [enter] to continue: ")

	reader := bufio.NewReader(os.Stdin)
	serverIndex, _ := reader.ReadString('\n')

	id, err := strconv.Atoi(strings.Replace(serverIndex, "\n", "", 1))
	if err != nil {
		fmt.Println("I cannot do that Dave.")
		os.Exit(1)
	}
	if id < 1 || id > len(candidates) {
		fmt.Println("I cannot do that Dave.")
		os.Exit(1)
	}
	id--

	fmt.Printf("jumping to %s (%s) via %s (%s)\n\n", candidates[id].Instance.Name, candidates[id].Instance.PrivateIP, candidates[id].Bastion.Name, candidates[id].Bastion.PublicIP)

	sshClient, err := newTunnelledSSHClient(sshUserName, candidates[id].Bastion.PublicIP, candidates[id].Instance.PrivateIP)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(1)
	}

	err = Shell(sshClient)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", err)
		os.Exit(1)
	}
}

// getCandidates will take a target (an instance name) and a list of instances and return a jump path chain
func getCandidates(targets []string, instances []*instance) []*instancePair {
	// randomise which bastion box to use
	randSource := rand.NewSource(time.Now().UnixNano())
	random := rand.New(randSource)
	var candidates []*instancePair
	for _, target := range targets {
		for _, instance := range instances {
			if instance.Name != target {
				continue
			}
			if len(instance.Bastions) < 1 {
				fmt.Printf("No bastion servers found for %s\n", instance.Name)
				continue
			}
			index := random.Intn(len(instance.Bastions))
			candidates = append(candidates, &instancePair{
				Bastion:  instance.Bastions[index],
				Instance: instance,
			})
		}
	}

	return candidates
}

// sorts candidate by name first and then with launchtime
type candidateSort []*instancePair

func (p candidateSort) Len() int { return len(p) }
func (p candidateSort) Less(i, j int) bool {
	if p[i].Instance.Name < p[j].Instance.Name {
		return true
	}
	if p[i].Instance.Name > p[j].Instance.Name {
		return false
	}
	return p[i].Instance.LaunchTime.After(*p[j].Instance.LaunchTime)
}
func (p candidateSort) Swap(i, j int) { p[i], p[j] = p[j], p[i] }

// findInstanceNames takes the users typed target name and finds real instance names from that
func findInstanceNames(targetName string, instances []*instance) []string {

	var instanceNames []string
	for _, i := range instances {
		instanceNames = append(instanceNames, i.Name)
	}

	fuzzIndex := fuzzstr.NewIndex(instanceNames)
	postings := fuzzIndex.Query(targetName)

	result := make(map[string]bool)
	for i := 0; i < len(postings); i++ {
		name := instanceNames[postings[i].Doc]
		result[name] = true
	}

	// convert back into a slice
	var names []string
	for name := range result {
		names = append(names, name)
	}

	return names
}

func padToLen(s string, padStr string, overallLen int) string {
	var padCountInt = 1 + ((overallLen - len(padStr)) / len(padStr))
	var retStr = s + strings.Repeat(padStr, padCountInt)
	return retStr[:overallLen]
}

func fetchInstances(config *aws.Config) ([]*instance, error) {
	var instances []*instance

	s := session.Must(session.NewSession(config))
	svc := ec2.New(s, &aws.Config{})

	filters := []*ec2.Filter{
		{
			Name:   aws.String("instance-state-name"),
			Values: []*string{aws.String("running"), aws.String("pending")},
		},
	}

	resp, err := svc.DescribeInstances(&ec2.DescribeInstancesInput{
		Filters: filters,
	})
	if err != nil {
		return instances, err
	}

	for idx := range resp.Reservations {
		for _, inst := range resp.Reservations[idx].Instances {
			i := newInstance(inst)
			instances = append(instances, i)
		}
	}

	// find the bastion for each instance that is in a private subnet
	for _, i := range instances {
		if i.IsNat {
			continue
		}
		// find bastion instance for instances
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

	return instances, nil
}

// newInstance creates a new instance struct from an AWS describeInstances call
func newInstance(inst *ec2.Instance) *instance {
	i := &instance{
		ID:   *inst.InstanceId,
		Tags: make(map[string]string, 0),
	}

	if inst.PrivateIpAddress != nil {
		i.PrivateIP = *inst.PrivateIpAddress
	}

	if inst.PublicIpAddress != nil {
		i.PublicIP = *inst.PublicIpAddress
	}

	i.LaunchTime = inst.LaunchTime

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

func printUsageAndQuit(exitCode int) {
	fmt.Printf("salio - ssh proxy (%s)\n", version)
	fmt.Println("usage: salio -p playpen -r ap-southeast-2 cluster stack env")
	os.Exit(exitCode)
}
