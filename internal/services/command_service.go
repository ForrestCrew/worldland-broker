package services

import (
	"github.com/google/uuid"
	"github.com/worldland/worldland-hub/internal/adapters/mtls"
)

// CommandService handles sending commands to nodes via mTLS
type CommandService struct {
	mtlsServer *mtls.Server
}

// NewCommandService creates a new command service
func NewCommandService(mtlsServer *mtls.Server) *CommandService {
	return &CommandService{
		mtlsServer: mtlsServer,
	}
}

// SendStartRental sends a start_rental command to a node
func (s *CommandService) SendStartRental(nodeID string, payload map[string]interface{}) error {
	cmd := mtls.Command{
		ID:      uuid.New().String(),
		Type:    "start_rental",
		Payload: payload,
	}
	return s.mtlsServer.SendCommand(nodeID, cmd)
}

// SendStopRental sends a stop_rental command to a node
func (s *CommandService) SendStopRental(nodeID string, payload map[string]interface{}) error {
	cmd := mtls.Command{
		ID:      uuid.New().String(),
		Type:    "stop_rental",
		Payload: payload,
	}
	return s.mtlsServer.SendCommand(nodeID, cmd)
}

// SendStartJob sends a start job command to a node (legacy alias)
func (s *CommandService) SendStartJob(nodeID string, payload map[string]interface{}) error {
	return s.SendStartRental(nodeID, payload)
}

// SendStopJob sends a stop job command to a node (legacy alias)
func (s *CommandService) SendStopJob(nodeID string, payload map[string]interface{}) error {
	return s.SendStopRental(nodeID, payload)
}
