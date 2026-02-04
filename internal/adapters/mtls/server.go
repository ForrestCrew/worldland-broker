package mtls

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"log"
	"net"
	"sync"
)

// Command represents a command sent to a node
type Command struct {
	ID      string                 `json:"id"`
	Type    string                 `json:"type"`
	Payload map[string]interface{} `json:"payload"`
}

// CommandAck represents node acknowledgment of a command
type CommandAck struct {
	CommandID string                 `json:"command_id"`
	Status    string                 `json:"status"` // "ok" or "error"
	Error     string                 `json:"error,omitempty"`
	Payload   map[string]interface{} `json:"payload,omitempty"` // Additional response data (e.g., hostname for K8s labeling)
}

// Server handles mTLS connections from nodes
type Server struct {
	serverCert tls.Certificate
	clientCAs  *x509.CertPool
	addr       string
	listener   net.Listener
	stopCh     chan struct{}
	clients    sync.Map // nodeID -> net.Conn

	// OnMessage is called when a message is received from a node
	OnMessage func(nodeID string, msg []byte)

	// OnNodeConnected is called when a node establishes mTLS connection
	// nodeID is extracted from the client certificate's Common Name
	OnNodeConnected func(nodeID string)

	// OnNodeDisconnected is called when a node disconnects
	OnNodeDisconnected func(nodeID string)
}

// NewServer creates a new mTLS server
func NewServer(serverCert tls.Certificate, clientCAs *x509.CertPool, addr string) *Server {
	return &Server{
		serverCert: serverCert,
		clientCAs:  clientCAs,
		addr:       addr,
		stopCh:     make(chan struct{}),
	}
}

// Start begins listening for mTLS connections
func (s *Server) Start() error {
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{s.serverCert},
		ClientCAs:    s.clientCAs,
		ClientAuth:   tls.RequireAndVerifyClientCert, // Mutual TLS
		MinVersion:   tls.VersionTLS13,                // TLS 1.3 only per research
		MaxVersion:   tls.VersionTLS13,
	}

	listener, err := tls.Listen("tcp", s.addr, tlsConfig)
	if err != nil {
		return err
	}
	s.listener = listener

	go s.acceptLoop()
	return nil
}

func (s *Server) acceptLoop() {
	for {
		select {
		case <-s.stopCh:
			return
		default:
			conn, err := s.listener.Accept()
			if err != nil {
				select {
				case <-s.stopCh:
					return
				default:
					log.Printf("Accept error: %v", err)
					continue
				}
			}

			go s.handleConnection(conn)
		}
	}
}

func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()

	// Extract node ID from client certificate
	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		log.Printf("Not a TLS connection")
		return
	}

	if err := tlsConn.Handshake(); err != nil {
		log.Printf("TLS handshake failed: %v", err)
		return
	}

	state := tlsConn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		log.Printf("No client certificate")
		return
	}

	nodeID := state.PeerCertificates[0].Subject.CommonName
	log.Printf("Node connected: %s", nodeID)

	s.clients.Store(nodeID, conn)
	defer func() {
		s.clients.Delete(nodeID)
		// Notify disconnection
		if s.OnNodeDisconnected != nil {
			s.OnNodeDisconnected(nodeID)
		}
	}()

	// Notify connection - trigger auto-registration
	if s.OnNodeConnected != nil {
		s.OnNodeConnected(nodeID)
	}

	// Read messages from node
	buf := make([]byte, 4096)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			log.Printf("Read error from node %s: %v", nodeID, err)
			return
		}

		if s.OnMessage != nil {
			s.OnMessage(nodeID, buf[:n])
		}
	}
}

// SendCommand sends a command to a specific node
// Accepts both Command struct and map[string]interface{} for flexibility
func (s *Server) SendCommand(nodeID string, command interface{}) error {
	conn, ok := s.clients.Load(nodeID)
	if !ok {
		return ErrNodeNotConnected
	}

	data, err := json.Marshal(command)
	if err != nil {
		return err
	}

	_, err = conn.(net.Conn).Write(data)
	return err
}

// GetConnectedNodeID extracts the node ID from a TLS connection
// Used primarily for testing to verify node ID extraction
func (s *Server) GetConnectedNodeID(conn net.Conn) string {
	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		return ""
	}

	state := tlsConn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return ""
	}

	return state.PeerCertificates[0].Subject.CommonName
}

// Stop shuts down the mTLS server
func (s *Server) Stop() {
	close(s.stopCh)
	if s.listener != nil {
		s.listener.Close()
	}
}

var ErrNodeNotConnected = &NodeNotConnectedError{}

type NodeNotConnectedError struct{}

func (e *NodeNotConnectedError) Error() string {
	return "node not connected"
}
