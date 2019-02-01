package spiffecontext

import (
	"context"

	"google.golang.org/grpc/metadata"
)

const (
	_headerSpiffeWorkloadKey   = "workload.spiffe.io"
	_headerSpiffeWorkloadValue = "true"
)

// NewOutgoing returns a new SPIFFE context with metadata for speaking to the Workload API.
func NewOutgoing(ctx context.Context, headers map[string]string) context.Context {
	md := metadata.New(headers)
	md.Set(_headerSpiffeWorkloadKey, _headerSpiffeWorkloadValue)
	return metadata.NewOutgoingContext(ctx, md)
}

// IsValidIncomingContext verifies that this is a valid SPIFFE request context.
func IsValidIncomingContext(ctx context.Context) bool {
	md, ok := metadata.FromIncomingContext(ctx)
	return ok && len(md[_headerSpiffeWorkloadKey]) == 1 && md[_headerSpiffeWorkloadKey][0] == _headerSpiffeWorkloadValue
}
