package main

import (
	"os"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/terminal"
)

type sshForwardingClient struct {
	agentForwarding bool
	*ssh.Client
	authAgentReqSent bool
}

func (s *sshForwardingClient) ForwardAgentAuthentication(session *ssh.Session) error {
	if s.agentForwarding && !s.authAgentReqSent {
		// We are allowed to send "auth-agent-req@openssh.com" request only once per channel
		// otherwise ssh daemon replies with the "SSH2_MSG_CHANNEL_FAILURE 100"
		s.authAgentReqSent = true
		return agent.RequestAgentForwarding(session)
	}
	return nil
}

// Shell launches an interactive shell on the given client. It returns any
// error encountered in setting up the SSH session.
func Shell(client *sshForwardingClient) error {
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

// makeSession initializes a ssh.Session connected to the invoking process's stdout/stderr/stdout.
// If the invoking session is a terminal, a TTY will be requested for the SSH session.
// It returns a ssh.Session, a finalizing function used to clean up after the session terminates,
// and any error encountered in setting up the session.
func makeSession(client *sshForwardingClient) (session *ssh.Session, finalize func(), err error) {
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
