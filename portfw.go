package portfw

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"
)

// Mapping defines a remote-to-local port forwarding mapping.
type Mapping struct {
	RemoteAddr string // Remote address to listen on (e.g., "0.0.0.0:10888")
	LocalAddr  string // Local service address to forward to (e.g., "127.0.0.1:8080")
}

// Forwarder manages multiple remote port forwardings over an SSH connection.
type Forwarder struct {
	sshClient *ssh.Client
	mappings  []Mapping
}

// New creates a new Forwarder given an established SSH client.
func New(sshClient *ssh.Client) *Forwarder {
	return &Forwarder{
		sshClient: sshClient,
		mappings:  make([]Mapping, 0),
	}
}

// AddMapping adds a new remote-to-local mapping.
// For example, AddMapping("0.0.0.0:10888", "127.0.0.1:8080")
func (f *Forwarder) AddMapping(remoteAddr, localAddr string) {
	f.mappings = append(f.mappings, Mapping{
		RemoteAddr: remoteAddr,
		LocalAddr:  localAddr,
	})
}

// Start begins all configured port forwardings. It blocks until the provided context is canceled.
func (f *Forwarder) Start(ctx context.Context) error {
	for _, mapping := range f.mappings {
		// Request the SSH server to listen on the remote address.
		listener, err := f.sshClient.Listen("tcp", mapping.RemoteAddr)
		if err != nil {
			return fmt.Errorf("failed to listen on %s: %w", mapping.RemoteAddr, err)
		}
		log.Printf("Forwarding remote %s to local %s", mapping.RemoteAddr, mapping.LocalAddr)

		// Ensure the listener is closed when the context is canceled.
		go func(l net.Listener) {
			<-ctx.Done()
			l.Close()
		}(listener)

		// Accept connections on the remote listener.
		go func(mapping Mapping, listener net.Listener) {
			for {
				remoteConn, err := listener.Accept()
				if err != nil {
					// Check if we should exit on context cancellation.
					select {
					case <-ctx.Done():
						return
					default:
						log.Printf("Error accepting connection on %s: %v", mapping.RemoteAddr, err)
						continue
					}
				}
				go handleConnection(remoteConn, mapping.LocalAddr)
			}
		}(mapping, listener)
	}

	// Block until the context is canceled.
	<-ctx.Done()
	return nil
}

// handleConnection forwards data between the remote connection and the local service.
func handleConnection(remoteConn net.Conn, localAddr string) {
	defer remoteConn.Close()

	// Connect to the local service.
	localConn, err := net.Dial("tcp", localAddr)
	if err != nil {
		log.Printf("Failed to connect to local address %s: %v", localAddr, err)
		return
	}
	defer localConn.Close()

	// Start bidirectional copying of data.
	done := make(chan struct{}, 2)
	go func() {
		io.Copy(localConn, remoteConn)
		done <- struct{}{}
	}()
	go func() {
		io.Copy(remoteConn, localConn)
		done <- struct{}{}
	}()
	<-done // wait until one direction is finished
}

// DialSSH establishes an SSH connection using password authentication.
// Modify this function to support other authentication methods (e.g., public key) if needed.
func DialSSH(user, password, host string, port int) (*ssh.Client, error) {
	sshConfig := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.Password(password),
		},
		// WARNING: For production, do not use InsecureIgnoreHostKey.
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	return ssh.Dial("tcp", addr, sshConfig)
}
