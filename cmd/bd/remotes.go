package main

import (
	"context"
	"os"
	"path/filepath"
	"sync"

	"github.com/BurntSushi/toml"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// RemotesConfig holds all named remotes and tracks which one is active.
type RemotesConfig struct {
	Active  string            `toml:"active"`
	Remotes map[string]Remote `toml:"remotes"`
}

// Remote is a named server profile.
type Remote struct {
	URL     string `toml:"url"`
	Token   string `toml:"token,omitempty"`
	NATSURL string `toml:"nats_url,omitempty"`
}

func remoteConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".local", "state", "beads")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "remotes.toml"), nil
}

func loadRemotesConfig() (RemotesConfig, error) {
	path, err := remoteConfigPath()
	if err != nil {
		return RemotesConfig{}, err
	}
	var cfg RemotesConfig
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		if os.IsNotExist(err) {
			return RemotesConfig{Remotes: map[string]Remote{}}, nil
		}
		return RemotesConfig{}, err
	}
	if cfg.Remotes == nil {
		cfg.Remotes = map[string]Remote{}
	}
	return cfg, nil
}

func saveRemotesConfig(cfg RemotesConfig) error {
	path, err := remoteConfigPath()
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(cfg)
}

// Cached active remote values, loaded once per process.
var (
	remoteOnce      sync.Once
	cachedRemoteURL string
	cachedToken     string
	cachedNATSURL   string
)

func loadActiveRemoteOnce() {
	remoteOnce.Do(func() {
		cfg, err := loadRemotesConfig()
		if err != nil || cfg.Active == "" {
			return
		}
		r, ok := cfg.Remotes[cfg.Active]
		if !ok {
			return
		}
		cachedRemoteURL = r.URL
		cachedToken = r.Token
		cachedNATSURL = r.NATSURL
	})
}

func activeRemoteURL() string {
	loadActiveRemoteOnce()
	return cachedRemoteURL
}

func activeRemoteToken() string {
	loadActiveRemoteOnce()
	return cachedToken
}

func activeRemoteNATSURL() string {
	loadActiveRemoteOnce()
	return cachedNATSURL
}

// bearerTokenInterceptor returns a gRPC unary interceptor that attaches a
// Bearer token to every outgoing call.
func bearerTokenInterceptor(token string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}
