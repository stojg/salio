package main

import (
	"errors"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

func newTunnelledSSHClient(user, tunnelAddress, targetAddress string) (*sshForwardingClient, error) {
	tunnelAddress = maybeAddDefaultPort(tunnelAddress)
	targetAddress = maybeAddDefaultPort(targetAddress)

	agentClient, err := sshAgentClient()
	if err != nil {
		return nil, err
	}

	signers, err := agentClient.Signers()
	if err != nil {
		return nil, err
	}

	clientConfig := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signers...),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	var tunnelClient *ssh.Client
	dialFunc := func(echan chan error) {
		var err error
		tunnelClient, err = ssh.Dial("tcp", tunnelAddress, clientConfig)
		echan <- err
	}
	if err = timeoutSSHDial(dialFunc); err != nil {
		return nil, err
	}

	var targetConn net.Conn
	dialFunc = func(echan chan error) {
		tgtTCPAddr, err := net.ResolveTCPAddr("tcp", targetAddress)
		if err != nil {
			echan <- err
			return
		}
		targetConn, err = tunnelClient.DialTCP("tcp", nil, tgtTCPAddr)
		echan <- err
	}
	if err = timeoutSSHDial(dialFunc); err != nil {
		return nil, err
	}

	conn, chans, reqs, err := ssh.NewClientConn(targetConn, targetAddress, clientConfig)
	if err != nil {
		return nil, err
	}
	return newSSHForwardingClient(ssh.NewClient(conn, chans, reqs))
}

func sshAgentClient() (agent.Agent, error) {
	sock := os.Getenv("SSH_AUTH_SOCK")
	if sock == "" {
		return nil, errors.New("SSH_AUTH_SOCK environment variable is not set, verify that ssh-agent is running")
	}

	agt, err := net.Dial("unix", sock)
	if err != nil {
		return nil, err
	}

	return agent.NewClient(agt), nil
}

func maybeAddDefaultPort(addr string) string {
	if strings.Contains(addr, ":") {
		return addr
	}
	return net.JoinHostPort(addr, strconv.Itoa(22))
}

func newSSHForwardingClient(client *ssh.Client) (*sshForwardingClient, error) {
	a, err := sshAgentClient()
	if err != nil {
		return nil, err
	}

	err = agent.ForwardToAgent(client, a)
	if err != nil {
		return nil, err
	}

	return &sshForwardingClient{true, client, false}, nil
}

func timeoutSSHDial(dial func(chan error)) error {
	var err error

	echan := make(chan error)
	go dial(echan)

	select {
	case <-time.After(time.Second * 10):
		return errors.New("timed out while initiating SSH connection")
	case err = <-echan:
		return err
	}
}
