package main

import (
	"errors"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/terminal"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// Shell launches an interactive shell on the given client. It returns any
// error encountered in setting up the SSH session.
func Shell(client *SSHForwardingClient) error {
	session, finalize, err := makeSession(client)
	if err != nil {
		return err
	}

	defer finalize()

	if err = session.Shell(); err != nil {
		return err
	}

	session.Wait()
	return nil
}

func NewTunnelledSSHClient(user, tunaddr, tgtaddr string, agentForwarding bool) (*SSHForwardingClient, error) {
	tunaddr = maybeAddDefaultPort(tunaddr)
	tgtaddr = maybeAddDefaultPort(tgtaddr)

	clientConfig, err := sshClientConfig(user)
	if err != nil {
		return nil, err
	}

	var tunnelClient *ssh.Client
	dialFunc := func(echan chan error) {
		var err error
		tunnelClient, err = ssh.Dial("tcp", tunaddr, clientConfig)
		echan <- err
	}
	err = timeoutSSHDial(dialFunc)
	if err != nil {
		return nil, err
	}

	var targetConn net.Conn
	dialFunc = func(echan chan error) {
		tgtTCPAddr, err := net.ResolveTCPAddr("tcp", tgtaddr)
		if err != nil {
			echan <- err
			return
		}
		targetConn, err = tunnelClient.DialTCP("tcp", nil, tgtTCPAddr)
		echan <- err
	}
	err = timeoutSSHDial(dialFunc)
	if err != nil {
		return nil, err
	}

	c, chans, reqs, err := ssh.NewClientConn(targetConn, tgtaddr, clientConfig)
	if err != nil {
		return nil, err
	}
	return newSSHForwardingClient(ssh.NewClient(c, chans, reqs), agentForwarding)
}

func SSHAgentClient() (agent.Agent, error) {
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

type SSHForwardingClient struct {
	agentForwarding bool
	*ssh.Client
	authAgentReqSent bool
}

func (s *SSHForwardingClient) ForwardAgentAuthentication(session *ssh.Session) error {
	if s.agentForwarding && !s.authAgentReqSent {
		// We are allowed to send "auth-agent-req@openssh.com" request only once per channel
		// otherwise ssh daemon replies with the "SSH2_MSG_CHANNEL_FAILURE 100"
		s.authAgentReqSent = true
		return agent.RequestAgentForwarding(session)
	}
	return nil
}

// makeSession initializes a ssh.Session connected to the invoking process's stdout/stderr/stdout.
// If the invoking session is a terminal, a TTY will be requested for the SSH session.
// It returns a ssh.Session, a finalizing function used to clean up after the session terminates,
// and any error encountered in setting up the session.
func makeSession(client *SSHForwardingClient) (session *ssh.Session, finalize func(), err error) {
	session, err = client.NewSession()
	if err != nil {
		return
	}
	if err = client.ForwardAgentAuthentication(session); err != nil {
		return
	}
	session.Stdout = os.Stdout
	session.Stderr = os.Stderr
	session.Stdin = os.Stdin

	modes := ssh.TerminalModes{
		ssh.ECHO:          1,     // enable echoing
		ssh.TTY_OP_ISPEED: 14400, // input speed = 14.4kbaud
		ssh.TTY_OP_OSPEED: 14400, // output speed = 14.4kbaud
	}

	fd := int(os.Stdin.Fd())
	if terminal.IsTerminal(fd) {

		var termWidth, termHeight int
		var oldState *terminal.State

		oldState, err = terminal.MakeRaw(fd)
		if err != nil {
			return
		}

		finalize = func() {
			session.Close()
			terminal.Restore(fd, oldState)
		}

		termWidth, termHeight, err = terminal.GetSize(fd)

		if err != nil {
			return
		}
		err = session.RequestPty("xterm-256color", termHeight, termWidth, modes)
	} else {
		finalize = func() {
			session.Close()
		}
	}

	return
}

func maybeAddDefaultPort(addr string) string {
	if strings.Contains(addr, ":") {
		return addr
	}
	return net.JoinHostPort(addr, strconv.Itoa(22))
}

func sshClientConfig(user string) (*ssh.ClientConfig, error) {
	agentClient, err := SSHAgentClient()
	if err != nil {
		return nil, err
	}

	signers, err := agentClient.Signers()
	if err != nil {
		return nil, err
	}

	cfg := ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signers...),
		},
	}

	return &cfg, nil
}

func newSSHForwardingClient(client *ssh.Client, agentForwarding bool) (*SSHForwardingClient, error) {
	a, err := SSHAgentClient()
	if err != nil {
		return nil, err
	}

	err = agent.ForwardToAgent(client, a)
	if err != nil {
		return nil, err
	}

	return &SSHForwardingClient{agentForwarding, client, false}, nil
}

func timeoutSSHDial(dial func(chan error)) error {
	var err error

	echan := make(chan error)
	go dial(echan)

	select {
	case <-time.After(time.Second):
		return errors.New("timed out while initiating SSH connection")
	case err = <-echan:
		return err
	}
}
