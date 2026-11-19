package sync

import (
	"context"
	"errors"
	"fmt"

	"github.com/ansonnchan/tokenholic/internal/modelcatalog"
	"github.com/ansonnchan/tokenholic/internal/normalize"
	protocol "github.com/ansonnchan/tokenholic/internal/sync"
	serverauth "github.com/ansonnchan/tokenholic/server/auth"
)

var (
	// ErrInvalidBatch means client metadata disagrees with the server catalog.
	ErrInvalidBatch = errors.New("usage batch is invalid")
	// ErrRevisionConflict means equal device/key/revision identity was presented
	// with a different allowlisted payload.
	ErrRevisionConflict = errors.New("usage bucket revision conflicts with stored payload")
)

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
	models     *modelcatalog.Resolver
	pricing    *modelcatalog.PricingRegistry
}

// NewService constructs the synchronization service.
func NewService(auth Authenticator, repository Repository, models *modelcatalog.Resolver, pricing *modelcatalog.PricingRegistry) (*Service, error) {
	if auth == nil || repository == nil || models == nil || pricing == nil {
		return nil, errors.New("initialize synchronization: authenticator, repository, model catalog, and pricing registry are required")
	}
	return &Service{auth: auth, repository: repository, models: models, pricing: pricing}, nil
}

// Apply authenticates the device and applies one validated bounded batch.
func (s *Service) Apply(ctx context.Context, credential string, batch protocol.Batch) (protocol.BatchResponse, error) {
	if err := batch.Validate(); err != nil {
		return protocol.BatchResponse{}, fmt.Errorf("%w: %v", ErrInvalidBatch, err)
	}
	principal, err := s.auth.Authenticate(ctx, credential)
	if err != nil {
		return protocol.BatchResponse{}, err
	}
	buckets := make([]protocol.Envelope, len(batch.Buckets))
	for index, envelope := range batch.Buckets {
		buckets[index], err = s.authoritativeEnvelope(envelope)
		if err != nil {
			return protocol.BatchResponse{}, fmt.Errorf("%w: bucket %d: %v", ErrInvalidBatch, index, err)
		}
	}
	acknowledgements, err := s.repository.ApplyUsageBuckets(ctx, principal, buckets)
	if err != nil {
		return protocol.BatchResponse{}, err
	}
	return protocol.BatchResponse{Acknowledgements: acknowledgements}, nil
}

func (s *Service) authoritativeEnvelope(envelope protocol.Envelope) (protocol.Envelope, error) {
	bucket := envelope.Bucket
	resolution := s.models.Resolve(bucket.ObservedModelID, normalize.ModelResolution(bucket.ModelResolution))
	if bucket.ModelResolution != string(resolution.Quality) ||
		!sameText(bucket.CanonicalModelID, resolution.CanonicalID) ||
		!sameText(bucket.ModelProvider, resolution.Provider) ||
		!sameText(bucket.PricingKey, resolution.PricingKey) {
		return protocol.Envelope{}, errors.New("model metadata does not match the server catalog")
	}

	bucket.APIEstimate = protocol.Value{State: "unavailable"}
	bucket.PricingRevision = nil
	if resolution.PricingKey != nil {
		tokens, available := billableTokens(bucket.Tokens)
		if available {
			estimate, priced, err := s.pricing.EstimateTokens(*resolution.PricingKey, tokens)
			if err != nil {
				return protocol.Envelope{}, fmt.Errorf("calculate API-equivalent estimate: %w", err)
			}
			if priced {
				state := "complete"
				if hasPartialBillableTokens(bucket.Tokens) {
					state = "partial"
				}
				bucket.APIEstimate = protocol.Value{Value: uint64Pointer(estimate.MicroUSD), State: state}
				bucket.PricingRevision = stringPointer(estimate.PricingRevision)
			}
		}
	}
	envelope.Bucket = bucket
	if err := envelope.Validate(); err != nil {
		return protocol.Envelope{}, fmt.Errorf("validate authoritative bucket: %w", err)
	}
	return envelope, nil
}

func billableTokens(tokens protocol.Tokens) (normalize.TokenCounts, bool) {
	values := []protocol.Value{tokens.InputUncached, tokens.InputCacheRead, tokens.InputCacheWrite, tokens.Output}
	for _, value := range values {
		if value.Value == nil {
			return normalize.TokenCounts{}, false
		}
	}
	return normalize.TokenCounts{
		InputUncached: tokens.InputUncached.Value, InputCacheRead: tokens.InputCacheRead.Value,
		InputCacheWrite: tokens.InputCacheWrite.Value, Output: tokens.Output.Value,
	}, true
}

func hasPartialBillableTokens(tokens protocol.Tokens) bool {
	return tokens.InputUncached.State == "partial" || tokens.InputCacheRead.State == "partial" ||
		tokens.InputCacheWrite.State == "partial" || tokens.Output.State == "partial"
}

func sameText(left, right *string) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func stringPointer(value string) *string {
	copy := value
	return &copy
}

func uint64Pointer(value uint64) *uint64 {
	copy := value
	return &copy
}
