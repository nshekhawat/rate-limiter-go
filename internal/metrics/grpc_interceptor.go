// Package metrics provides observability instrumentation for the rate limiter service.
package metrics

import (
	"context"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// UnaryServerInterceptor returns a gRPC unary server interceptor that records metrics.
func UnaryServerInterceptor(m *Metrics) grpc.UnaryServerInterceptor {
	if m == nil {
		m = DefaultMetrics
	}

	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		start := time.Now()

		resp, err := handler(ctx, req)

		duration := time.Since(start).Seconds()
		code := status.Code(err).String()

		m.RecordGRPCRequest(info.FullMethod, code, duration)

		return resp, err
	}
}
