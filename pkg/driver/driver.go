package driver

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc"
	"k8s.io/klog"

	"github.com/flowswiss/goclient/flow"
	"github.com/flowswiss/goclient/flow/auth"

	"github.com/flowswiss/csi-driver/pkg/fs"
)

const (
	name    = "csi.flow.swiss"
	version = "1.0.0"
)

type Driver struct {
	flow          *flow.Client
	healthChecker *HealthChecker

	currentNodeId     flow.Id
	clusterLocationId flow.Id

	mounter *fs.Mounter
}

func NewDriver(token, baseUrl, hostname string) (*Driver, error) {
	client := auth.NewClientWithTransport(
		&auth.ApplicationTokenAuthenticator{Token: token},
		&httpLogTransport{base: http.DefaultTransport},
	)

	flowClient := flow.NewClient(client)
	flowClient.UserAgent = fmt.Sprintf("flow-csi-driver/%s %s", version, flowClient.UserAgent)

	if len(baseUrl) != 0 {
		var err error
		flowClient.BaseURL, err = url.Parse(baseUrl)
		if err != nil {
			return nil, err
		}
	}

	healthChecks := []HealthCheck{
		&apiAccessHealthCheck{client: flowClient},
	}

	srv, err := findServerByName(flowClient, context.TODO(), hostname)
	if err != nil {
		return nil, err
	}

	driver := &Driver{
		flow: flowClient,
		healthChecker: &HealthChecker{
			checks: healthChecks,
		},

		currentNodeId:     srv.Id,
		clusterLocationId: srv.Location.Id,

		mounter: fs.NewMounter(),
	}

	return driver, nil
}

func (d *Driver) Listen(socket string) error {
	u, err := url.Parse(socket)
	if err != nil {
		return err
	}

	host := u.Host

	if u.Scheme == "unix" {
		host = u.Path
		if err := os.Remove(host); err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	listener, err := net.Listen(u.Scheme, host)
	if err != nil {
		return err
	}

	srv := grpc.NewServer(grpc.UnaryInterceptor(grpcLoggingInterceptor))
	csi.RegisterIdentityServer(srv, d)
	csi.RegisterNodeServer(srv, d)
	csi.RegisterControllerServer(srv, d)

	klog.Info("Listening for connections on address: ", listener.Addr())

	err = srv.Serve(listener)
	if err != nil {
		return err
	}

	return nil
}

func findServerByName(client *flow.Client, ctx context.Context, name string) (*flow.Server, error) {
	klog.V(2).Info("Searching for server with name: ", name)

	list, _, err := client.Server.List(ctx, flow.PaginationOptions{NoFilter: 1})
	if err != nil {
		return nil, err
	}

	for _, srv := range list {
		if srv.Name == string(name) {
			klog.Info("Found matching server by name: ", logServer(srv))
			return srv, nil
		}
	}

	return nil, fmt.Errorf("no matching server found")
}
