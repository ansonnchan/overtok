package sync

import (
	"context"
	"errors"

	protocol "github.com/ansonnchan/tokenholic/internal/sync"
	serverauth "github.com/ansonnchan/tokenholic/server/auth"
)

// ErrRevisionConflict means equal device/key/revision identity was presented
// with a different allowlisted payload.
var ErrRevisionConflict = errors.New("usage bucket revision conflicts with stored payload")

// Authenticator resolves the device represented by a bearer credential.
type Authenticator interface {
	Authenticate(context.Context, string) (serverauth.Principal, error)
}

// Repository applies absolute revisions for one authenticated device.
type Repository interface {
	ApplyUsageBuckets(context.Context, serverauth.Principal, []protocol.Envelope) ([]protocol.Acknowledgement, error)
}

// Service owns authentication and server-side batch application.
type Service struct {
	auth       Authenticator
	repository Repository
}

// NewService constructs the synchronization service.
func NewService(auth Authenticator, repository Repository) (*Service, error) {
	if auth == nil || repository == nil {
		return nil, errors.New("initialize synchronization: authenticator and repository are required")
	}
	return &Service{auth: auth, repository: repository}, nil
}

// Apply authenticates the device and applies one validated bounded batch.
func (s *Service) Apply(ctx context.Context, credential string, batch protocol.Batch) (protocol.BatchResponse, error) {
	if err := batch.Validate(); err != nil {
		return protocol.BatchResponse{}, err
	}
	principal, err := s.auth.Authenticate(ctx, credential)
	if err != nil {
		return protocol.BatchResponse{}, err
	}
	acknowledgements, err := s.repository.ApplyUsageBuckets(ctx, principal, batch.Buckets)
	if err != nil {
		return protocol.BatchResponse{}, err
	}
	return protocol.BatchResponse{Acknowledgements: acknowledgements}, nil
}
