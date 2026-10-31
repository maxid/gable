package nacos

import (
	"context"
	"errors"
	"testing"

	"github.com/nacos-group/nacos-sdk-go/v2/model"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"

	"github.com/maxid/gable/rpc"
)

type fakeClient struct {
	instance *model.Instance
	err      error
	closed   bool
	gotGroup string
}

func (f *fakeClient) SelectOneHealthyInstance(param vo.SelectOneHealthInstanceParam) (*model.Instance, error) {
	f.gotGroup = param.GroupName
	return f.instance, f.err
}

func (f *fakeClient) CloseClient() { f.closed = true }

func TestResolveBuildsTheBaseURL(t *testing.T) {
	client := &fakeClient{instance: &model.Instance{Ip: "10.0.0.7", Port: 8081}}
	resolver := newWithClient(client, "", "http")

	base, err := resolver.Resolve(context.Background(), "iam")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if base != "http://10.0.0.7:8081" {
		t.Fatalf("base = %q", base)
	}
	if client.gotGroup != "DEFAULT_GROUP" {
		t.Fatalf("group = %q, want DEFAULT_GROUP", client.gotGroup)
	}
}

func TestResolveHonoursTheScheme(t *testing.T) {
	resolver := newWithClient(&fakeClient{instance: &model.Instance{Ip: "10.0.0.7", Port: 8443}}, "", "https")

	base, err := resolver.Resolve(context.Background(), "iam")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if base != "https://10.0.0.7:8443" {
		t.Fatalf("base = %q", base)
	}
}

// Every failure must be rpc.ErrNoEndpoint: the transport treats "no instance"
// differently from "the instance answered with an error".
func TestResolveMapsFailuresToNoEndpoint(t *testing.T) {
	cases := map[string]*fakeClient{
		"client error":  {err: errors.New("nacos down")},
		"nil instance":  {instance: nil},
		"empty address": {instance: &model.Instance{Ip: "", Port: 0}},
	}
	for name, client := range cases {
		resolver := newWithClient(client, "", "http")
		if _, err := resolver.Resolve(context.Background(), "iam"); !errors.Is(err, rpc.ErrNoEndpoint) {
			t.Errorf("%s: err = %v, want ErrNoEndpoint", name, err)
		}
	}

	resolver := newWithClient(&fakeClient{}, "", "http")
	if _, err := resolver.Resolve(context.Background(), ""); !errors.Is(err, rpc.ErrNoEndpoint) {
		t.Errorf("empty service err = %v", err)
	}
}

func TestCloseReleasesTheClient(t *testing.T) {
	client := &fakeClient{}
	newWithClient(client, "", "http").Close()
	if !client.closed {
		t.Fatal("close did not release the naming client")
	}
}

func TestNewRequiresServers(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("empty server list must be rejected")
	}
	if _, err := New(Config{Servers: []string{"bad-addr"}}); err == nil {
		t.Fatal("malformed server address must be rejected")
	}
}

func TestSplitHostPort(t *testing.T) {
	host, port, err := splitHostPort("127.0.0.1:8848")
	if err != nil || host != "127.0.0.1" || port != 8848 {
		t.Fatalf("split = %q, %d, %v", host, port, err)
	}
	for _, bad := range []string{"127.0.0.1", ":8848", "host:"} {
		if _, _, err := splitHostPort(bad); err == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
}
