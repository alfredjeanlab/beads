package server

import (
	beadsv1 "github.com/alfredjeanlab/beads/gen/beads/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

// NewGRPCServer creates a gRPC server with standard interceptors,
// registers the BeadsService, reflection, and returns the server ready to serve.
func NewGRPCServer(beadsServer *BeadsServer) *grpc.Server {
	srv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			RecoveryInterceptor,
			LoggingInterceptor,
		),
	)

	beadsv1.RegisterBeadsServiceServer(srv, beadsServer)
	reflection.Register(srv)

	return srv
}
