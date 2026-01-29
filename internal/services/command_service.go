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

// SendStartJob sends a start job command to a node (Phase 4)
func (s *CommandService) SendStartJob(nodeID string, payload map[string]interface{}) error {
	cmd := mtls.Command{
		ID:      uuid.New().String(),
		Type:    "start_job",
		Payload: payload,
	}
	return s.mtlsServer.SendCommand(nodeID, cmd)
}

// SendStopJob sends a stop job command to a node (Phase 4)
func (s *CommandService) SendStopJob(nodeID string, payload map[string]interface{}) error {
	cmd := mtls.Command{
		ID:      uuid.New().String(),
		Type:    "stop_job",
		Payload: payload,
	}
	return s.mtlsServer.SendCommand(nodeID, cmd)
}
