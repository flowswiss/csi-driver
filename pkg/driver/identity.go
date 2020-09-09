package driver

import (
	"context"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/protobuf/ptypes/wrappers"
	"k8s.io/klog"
)

func (d *Driver) GetPluginInfo(ctx context.Context, request *csi.GetPluginInfoRequest) (*csi.GetPluginInfoResponse, error) {
	return &csi.GetPluginInfoResponse{
		Name:          name,
		VendorVersion: version,
	}, nil
}

func (d *Driver) GetPluginCapabilities(ctx context.Context, request *csi.GetPluginCapabilitiesRequest) (*csi.GetPluginCapabilitiesResponse, error) {
	return &csi.GetPluginCapabilitiesResponse{
		Capabilities: []*csi.PluginCapability{
			{
				Type: &csi.PluginCapability_Service_{
					Service: &csi.PluginCapability_Service{
						Type: csi.PluginCapability_Service_CONTROLLER_SERVICE,
					},
				},
			},
		},
	}, nil
}

func (d *Driver) Probe(ctx context.Context, request *csi.ProbeRequest) (*csi.ProbeResponse, error) {
	err := d.healthChecker.Check(ctx)
	if err != nil {
		klog.Warning("Health check failed: ", err)
	}

	ready := err == nil

	return &csi.ProbeResponse{
		Ready: &wrappers.BoolValue{Value: ready},
	}, nil
}
