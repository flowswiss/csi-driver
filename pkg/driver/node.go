package driver

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/flowswiss/goclient/flow"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog"

	"github.com/flowswiss/csi-driver/pkg/fs"
)

func (d *Driver) NodeStageVolume(ctx context.Context, request *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	if len(request.VolumeId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volume id must be provided")
	}

	if len(request.StagingTargetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "staging target path must be provided")
	}

	if request.VolumeCapability == nil {
		return nil, status.Error(codes.InvalidArgument, "volume capability must be provided")
	}

	if request.VolumeCapability.AccessType == nil {
		return nil, status.Error(codes.InvalidArgument, "volume access type must be provided")
	}

	volumeId := flow.ParseIdentifier(request.VolumeId)
	if !volumeId.Valid() {
		return nil, status.Error(codes.InvalidArgument, "provided volume id is invalid")
	}

	volume, _, err := d.flow.Volume.Get(ctx, volumeId)
	if err != nil {
		if resp, ok := err.(*flow.ErrorResponse); ok && resp.Response.StatusCode == http.StatusNotFound {
			return nil, status.Error(codes.InvalidArgument, "volume does not exist")
		}

		return nil, status.Error(codes.Internal, err.Error())
	}

	klog.Info("Staging volume ", logVolume(volume), " to ", request.StagingTargetPath)

	mounted, err := d.mounter.IsMounted(request.StagingTargetPath)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	if mounted {
		klog.Warning("Assuming volume is already staged because staging path is already mounted")
		return &csi.NodeStageVolumeResponse{}, nil
	}

	err = os.RemoveAll(request.StagingTargetPath)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	device, err := findDeviceBySerial(volume.SerialNumber)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	for len(device) == 0 {
		klog.Info("Waiting for device with serial ", volume.SerialNumber, " to become available on host")
		device, err = findDeviceBySerial(volume.SerialNumber)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}

		time.Sleep(time.Second)
	}

	klog.Info("Found matching device ", device)

	switch accessType := request.VolumeCapability.AccessType.(type) {
	case *csi.VolumeCapability_Block:
		err = d.mounter.Mount(device, request.StagingTargetPath, "", fs.MountOptionsBind|fs.MountOptionsBlock)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	case *csi.VolumeCapability_Mount:
		err = d.mounter.FormatAndMount(device, request.StagingTargetPath, accessType.Mount.FsType, fs.MountOptionsDefault)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	return &csi.NodeStageVolumeResponse{}, nil
}

func (d *Driver) NodeUnstageVolume(ctx context.Context, request *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	if len(request.VolumeId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volume id must be provided")
	}

	if len(request.StagingTargetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "staging target path must be provided")
	}

	klog.Info("Unstaging volume from ", request.StagingTargetPath)

	mounted, err := d.mounter.IsMounted(request.StagingTargetPath)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	if !mounted {
		klog.Warning("Assuming volume is already unstaged because staging path is not mounted")
		return &csi.NodeUnstageVolumeResponse{}, nil
	}

	err = d.mounter.Unmount(request.StagingTargetPath)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	_ = os.RemoveAll(request.StagingTargetPath)

	return &csi.NodeUnstageVolumeResponse{}, nil
}

func (d *Driver) NodePublishVolume(ctx context.Context, request *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	if len(request.VolumeId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volume id must be provided")
	}

	if len(request.StagingTargetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "staging target path must be provided")
	}

	if len(request.TargetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "target path must be provided")
	}

	if request.VolumeCapability == nil {
		return nil, status.Error(codes.InvalidArgument, "volume capability must be provided")
	}

	klog.Info("Publishing volume ", request.VolumeId, " from ", request.StagingTargetPath, " to ", request.TargetPath)

	mounted, err := d.mounter.IsMounted(request.TargetPath)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	if mounted {
		klog.Warning("Assuming volume is already mounted because target path is mounted")
		return &csi.NodePublishVolumeResponse{}, nil
	}

	err = os.RemoveAll(request.TargetPath)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	switch request.VolumeCapability.AccessType.(type) {
	case *csi.VolumeCapability_Block:
		err := d.mounter.Mount(request.StagingTargetPath, request.TargetPath, "", fs.MountOptionsBind|fs.MountOptionsBlock)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	case *csi.VolumeCapability_Mount:
		err := d.mounter.Mount(request.StagingTargetPath, request.TargetPath, "", fs.MountOptionsBind)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	return &csi.NodePublishVolumeResponse{}, nil
}

func (d *Driver) NodeUnpublishVolume(ctx context.Context, request *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	if len(request.VolumeId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volume id must be provided")
	}

	if len(request.TargetPath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "target path must be provided")
	}

	klog.Info("Unpublishing volume ", request.VolumeId, " from ", request.TargetPath)

	mounted, err := d.mounter.IsMounted(request.TargetPath)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	if !mounted {
		klog.Warning("Assuming volume is already unpublished because target path is not mounted")
		return &csi.NodeUnpublishVolumeResponse{}, nil
	}

	err = d.mounter.Unmount(request.TargetPath)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	_ = os.RemoveAll(request.TargetPath)

	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func (d *Driver) NodeGetVolumeStats(ctx context.Context, request *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	return nil, unsupportedNodeCapability(csi.NodeServiceCapability_RPC_GET_VOLUME_STATS)
}

func (d *Driver) NodeExpandVolume(ctx context.Context, request *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	if len(request.VolumeId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volume id must be provided")
	}

	if len(request.VolumePath) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volume path must be provided")
	}

	mounted, err := d.mounter.IsMounted(request.VolumePath)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	if !mounted {
		return nil, status.Error(codes.NotFound, "volume is not mounted")
	}

	klog.Info("Resizing volume", request.VolumeId)

	if request.VolumeCapability.GetBlock() != nil {
		klog.Info("Skipping file system expansion for block volume")
		return &csi.NodeExpandVolumeResponse{}, nil
	}

	err = d.mounter.Resize(request.VolumePath)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &csi.NodeExpandVolumeResponse{}, nil
}

func (d *Driver) NodeGetCapabilities(ctx context.Context, request *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	capabilityTypes := []csi.NodeServiceCapability_RPC_Type{
		csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
		csi.NodeServiceCapability_RPC_EXPAND_VOLUME,
	}

	var capabilities []*csi.NodeServiceCapability
	for _, capabilityType := range capabilityTypes {
		capabilities = append(capabilities, &csi.NodeServiceCapability{
			Type: &csi.NodeServiceCapability_Rpc{
				Rpc: &csi.NodeServiceCapability_RPC{
					Type: capabilityType,
				},
			},
		})
	}

	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: capabilities,
	}, nil
}

func (d *Driver) NodeGetInfo(ctx context.Context, request *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	return &csi.NodeGetInfoResponse{
		NodeId: d.currentNodeId.String(),
	}, nil
}

func findDeviceBySerial(serial string) (string, error) {
	var bestMatch string
	regex := regexp.MustCompile("QEMU_HARDDISK_([0-9a-f\\-]+)")

	err := filepath.Walk("/dev/disk/by-id", func(path string, info os.FileInfo, err error) error {
		matches := regex.FindStringSubmatch(path)
		if len(matches) == 0 {
			return nil
		}

		uuid := matches[1]
		if strings.Index(serial, uuid) == 0 {
			bestMatch = path
		}

		return nil
	})

	if err != nil {
		return "", err
	}

	return bestMatch, nil
}

func unsupportedNodeCapability(capability csi.NodeServiceCapability_RPC_Type) error {
	return status.Errorf(codes.InvalidArgument, "unsupported node capability: %v", capability)
}
