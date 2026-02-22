package server

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// LoggingInterceptor logs the method name, duration, and error (if any) for every
// unary RPC call.
func LoggingInterceptor(
	ctx context.Context,
	req any,
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (any, error) {
	start := time.Now()
	resp, err := handler(ctx, req)
	duration := time.Since(start)

	if err != nil {
		slog.Error("rpc completed",
			"method", info.FullMethod,
			"duration", duration,
			"error", err,
		)
	} else {
		slog.Info("rpc completed",
			"method", info.FullMethod,
			"duration", duration,
		)
	}

	return resp, err
}

// RecoveryInterceptor catches panics in downstream handlers, logs the stack
// trace, and returns a codes.Internal error instead of crashing the server.
func RecoveryInterceptor(
	ctx context.Context,
	req any,
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (resp any, err error) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("panic recovered in gRPC handler",
				"method", info.FullMethod,
				"panic", fmt.Sprintf("%v", r),
				"stack", string(debug.Stack()),
			)
			err = status.Errorf(codes.Internal, "internal server error")
		}
	}()
	return handler(ctx, req)
}
