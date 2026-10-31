// Package nacos resolves RPC service names through Nacos.
//
// It is the only package that knows Nacos exists: the transport sees an rpc.Resolver,
// so a standalone deployment keeps using a static endpoint without pulling the SDK
// into its dependency graph.
package nacos

import (
	"context"
	"fmt"
	"strconv"

	"github.com/nacos-group/nacos-sdk-go/v2/clients"
	"github.com/nacos-group/nacos-sdk-go/v2/common/constant"
	"github.com/nacos-group/nacos-sdk-go/v2/model"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"

	"github.com/maxid/gable/rpc"
)

// Config mirrors the registry block of the deployment configuration.
type Config struct {
	Servers   []string `yaml:"servers"`
	Namespace string   `yaml:"namespace"`
	Group     string   `yaml:"group"`
	Username  string   `yaml:"username"`
	Password  string   `yaml:"password"`
	// Scheme is the URL scheme used for the resolved endpoint: "http" (default) or
	// "https" when the inner listener terminates TLS.
	Scheme string `yaml:"scheme"`
}

// Resolver picks one healthy instance per call. Nacos keeps the instance list cached
// locally, so a per-call lookup is cheap and picks up registrations without a
// restart.
type Resolver struct {
	client namingClient
	group  string
	scheme string
}

// namingClient is the subset used here, so the adapter is testable.
type namingClient interface {
	SelectOneHealthyInstance(param vo.SelectOneHealthInstanceParam) (*model.Instance, error)
	CloseClient()
}

// New connects to Nacos.
func New(cfg Config) (*Resolver, error) {
	servers := make([]constant.ServerConfig, 0, len(cfg.Servers))
	for _, addr := range cfg.Servers {
		host, port, err := splitHostPort(addr)
		if err != nil {
			return nil, err
		}
		servers = append(servers, constant.ServerConfig{IpAddr: host, Port: uint64(port)})
	}
	if len(servers) == 0 {
		return nil, fmt.Errorf("%w: nacos servers are required", rpc.ErrNoEndpoint)
	}

	clientConfig := constant.ClientConfig{
		NamespaceId: cfg.Namespace,
		Username:    cfg.Username,
		Password:    cfg.Password,
		TimeoutMs:   5000,
	}
	client, err := clients.NewNamingClient(vo.NacosClientParam{
		ClientConfig:  &clientConfig,
		ServerConfigs: servers,
	})
	if err != nil {
		return nil, fmt.Errorf("gable/rpc/nacos: client: %w", err)
	}

	scheme := cfg.Scheme
	if scheme == "" {
		scheme = "http"
	}
	return newWithClient(client, cfg.Group, scheme), nil
}

func newWithClient(client namingClient, group, scheme string) *Resolver {
	if group == "" {
		group = "DEFAULT_GROUP"
	}
	return &Resolver{client: client, group: group, scheme: scheme}
}

// Resolve implements rpc.Resolver.
//
// A failure is rpc.ErrNoEndpoint, never a bare client error: the transport treats "no
// instance" and "the instance failed" differently.
func (r *Resolver) Resolve(_ context.Context, service string) (string, error) {
	if service == "" {
		return "", fmt.Errorf("%w: empty service name", rpc.ErrNoEndpoint)
	}

	instance, err := r.client.SelectOneHealthyInstance(vo.SelectOneHealthInstanceParam{
		ServiceName: service,
		GroupName:   r.group,
	})
	if err != nil {
		return "", fmt.Errorf("%w: %s: %v", rpc.ErrNoEndpoint, service, err)
	}
	if instance == nil || instance.Ip == "" || instance.Port == 0 {
		return "", fmt.Errorf("%w: %s returned no healthy instance", rpc.ErrNoEndpoint, service)
	}

	return r.scheme + "://" + instance.Ip + ":" + strconv.Itoa(int(instance.Port)), nil
}

// Close releases the Nacos long-polling client.
func (r *Resolver) Close() { r.client.CloseClient() }

// splitHostPort parses "host:port" without net.SplitHostPort so a bare IPv6 address
// cannot silently become an empty host.
func splitHostPort(addr string) (string, int, error) {
	host, portText := addr, ""
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			host, portText = addr[:i], addr[i+1:]
			break
		}
	}
	port, err := strconv.Atoi(portText)
	if err != nil || host == "" {
		return "", 0, fmt.Errorf("gable/rpc/nacos: server %q: want host:port", addr)
	}
	return host, port, nil
}
