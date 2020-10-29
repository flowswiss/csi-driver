package driver

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"google.golang.org/grpc"
	"k8s.io/klog"

	"github.com/flowswiss/goclient/flow"
)

type Fields map[string]interface{}

func (f Fields) String() string {
	var fields []string
	for key, val := range f {
		fields = append(fields, fmt.Sprintf("%s=%v", key, val))
	}
	return fmt.Sprintf("{%s}", strings.Join(fields, ", "))
}

func logVolume(volume *flow.Volume) Fields {
	return Fields{
		"id":         volume.Id,
		"name":       volume.Name,
		"serial":     volume.SerialNumber,
		"size":       volume.Size,
		"status":     volume.Status,
		"created_at": volume.CreatedAt,
	}
}

func logServer(server *flow.Server) Fields {
	return Fields{
		"id":     server.Id,
		"name":   server.Name,
		"status": server.Status,
	}
}

func logSnapshot(snapshot *flow.Snapshot) Fields {
	return Fields{
		"id":         snapshot.Id,
		"name":       snapshot.Name,
		"created_at": snapshot.CreatedAt,
	}
}

type httpLogTransport struct {
	base http.RoundTripper
}

func (t *httpLogTransport) RoundTrip(req *http.Request) (res *http.Response, err error) {
	res, err = t.base.RoundTrip(req)
	if !klog.V(3) {
		return
	}

	if err != nil {
		klog.Warning("Error in request ", req.Method, " ", req.URL, ": ", err)
		return
	}

	klog.Info("Request ", req.Method, " ", req.URL, " resulted in ", res.Status)
	return
}

func grpcLoggingInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (res interface{}, err error) {
	klog.V(3).Infof("Received request to %s with data %#v", info.FullMethod, req)

	res, err = handler(ctx, req)
	if err != nil {
		klog.Error(info.FullMethod, " failed with ", err)
	}
	return
}
