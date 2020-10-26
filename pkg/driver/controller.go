package driver

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/protobuf/ptypes"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog"

	"github.com/flowswiss/goclient/flow"
	"github.com/flowswiss/goclient/flow/sse"
)

const (
	gib int64 = 1 << 30
	tib int64 = 1 << 40
)

const (
	minVolumeSize = 1 * gib
	defVolumeSize = 5 * gib
	maxVolumeSize = 5 * tib
)

func (d *Driver) CreateVolume(ctx context.Context, request *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	if len(request.Name) == 0 {
		return nil, status.Error(codes.InvalidArgument, "name must be provided")
	}

	if request.VolumeCapabilities == nil {
		return nil, status.Error(codes.InvalidArgument, "volume capabilities must be provided")
	}

	if request.AccessibilityRequirements != nil {
		for _, requisite := range request.AccessibilityRequirements.Requisite {
			location, ok := requisite.Segments["location"]
			if !ok {
				continue
			}

			locationId := flow.ParseIdentifier(location)
			if !locationId.Valid() {
				return nil, status.Error(codes.InvalidArgument, "invalid location requisite")
			}

			if locationId != d.clusterLocationId {
				return nil, status.Errorf(codes.InvalidArgument, "volume can only be created at location %s, got %s", d.clusterLocationId, locationId)
			}
		}
	}

	if violations := validateVolumeCapabilities(request.VolumeCapabilities); len(violations) != 0 {
		return nil, status.Errorf(codes.InvalidArgument, "the following volume capabilities cannot be satisfied: %s", strings.Join(violations, ", "))
	}

	size, err := extractVolumeSize(request.CapacityRange)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	sizeInGib := int(math.Ceil(float64(size) / float64(gib)))

	klog.Info("Creating volume ", Fields{
		"name":         request.Name,
		"size":         sizeInGib,
		"capabilities": request.VolumeCapabilities,
	})

	volumes, _, err := d.flow.Volume.List(ctx, flow.PaginationOptions{NoFilter: 1})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	for _, volume := range volumes {
		if volume.Name == request.Name {
			if volume.Size != sizeInGib {
				return nil, status.Error(codes.AlreadyExists, "volume size mismatch")
			}

			klog.Info("Found volume with matching requirements ", logVolume(volume))

			return &csi.CreateVolumeResponse{
				Volume: &csi.Volume{
					VolumeId:      volume.Id.String(),
					CapacityBytes: int64(volume.Size) * gib,
					AccessibleTopology: []*csi.Topology{
						{
							Segments: map[string]string{
								"location": volume.Location.Id.String(),
							},
						},
					},
				},
			}, nil
		}
	}

	data := &flow.VolumeCreate{
		Name:       request.Name,
		Size:       sizeInGib,
		LocationId: d.clusterLocationId,
	}

	volume, _, err := d.flow.Volume.Create(ctx, data)
	if err != nil {
		return nil, err
	}

	klog.Info("Created volume ", logVolume(volume))

	if volume.Status.Id == flow.VolumeStatusWorking {
		err = waitForVolume(d.sse, ctx, volume)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	return &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:      volume.Id.String(),
			CapacityBytes: int64(volume.Size) * gib,
			AccessibleTopology: []*csi.Topology{
				{
					Segments: map[string]string{
						"location": volume.Location.Id.String(),
					},
				},
			},
		},
	}, nil
}

func (d *Driver) DeleteVolume(ctx context.Context, request *csi.DeleteVolumeRequest) (*csi.DeleteVolumeResponse, error) {
	if len(request.VolumeId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volume id must be provided")
	}

	id := flow.ParseIdentifier(request.VolumeId)
	if !id.Valid() {
		// assume volume is deleted
		klog.Warning("Assuming volume is already deleted because it does not have a valid volume identifier: ", request.VolumeId)
		return &csi.DeleteVolumeResponse{}, nil
	}

	klog.Info("Deleting volume ", Fields{"id": id})

	volume, _, err := d.flow.Volume.Get(ctx, id)
	if err != nil {
		if resp, ok := err.(*flow.ErrorResponse); ok && resp.Response.StatusCode == http.StatusNotFound {
			// assume volume is already deleted
			klog.Warning("Assuming volume is already deleted because it was not found in the api")
			return &csi.DeleteVolumeResponse{}, nil
		}

		return nil, status.Error(codes.Internal, err.Error())
	}

	klog.V(2).Info("Volume identified as ", logVolume(volume))

	if volume.Snapshots > 0 {
		return nil, status.Error(codes.FailedPrecondition, "volume still has snapshots")
	}

	if volume.AttachedTo != nil {
		klog.Warning("Volume is still attached to instance ", logServer(volume.AttachedTo))

		_, err := d.flow.VolumeAction.Detach(ctx, volume.Id, volume.AttachedTo.Id)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	_, err = d.flow.Volume.Delete(ctx, id)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	klog.Info("Volume has been deleted")
	return &csi.DeleteVolumeResponse{}, nil
}

func (d *Driver) ControllerPublishVolume(ctx context.Context, request *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	if len(request.VolumeId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volume id must be provided")
	}

	if len(request.NodeId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "node id must be provided")
	}

	if request.Readonly {
		return nil, status.Error(codes.InvalidArgument, "read-only volumes are not supported")
	}

	if request.VolumeCapability == nil {
		return nil, status.Error(codes.InvalidArgument, "volume capability must be provided")
	}

	volumeId := flow.ParseIdentifier(request.VolumeId)
	if !volumeId.Valid() {
		return nil, status.Error(codes.NotFound, "provided volume id is invalid")
	}

	nodeId := flow.ParseIdentifier(request.NodeId)
	if !nodeId.Valid() {
		return nil, status.Error(codes.NotFound, "provided node id is invalid")
	}

	klog.Info("Attaching volume ", Fields{"id": volumeId}, " to server ", Fields{"id": nodeId})

	volume, _, err := d.flow.Volume.Get(ctx, volumeId)
	if err != nil {
		if resp, ok := err.(*flow.ErrorResponse); ok && resp.Response.StatusCode == http.StatusNotFound {
			return nil, status.Error(codes.NotFound, "volume does not exist")
		}

		return nil, status.Error(codes.Internal, err.Error())
	}

	klog.V(2).Info("Volume identified as ", logVolume(volume))

	if volume.AttachedTo != nil {
		if volume.AttachedTo.Id == nodeId {
			klog.Warning("Volume is already attached to selected node")
			return &csi.ControllerPublishVolumeResponse{}, nil
		}

		return nil, status.Error(codes.FailedPrecondition, "volume is already attach to another server")
	}

	server, _, err := d.flow.Server.Get(ctx, nodeId)
	if err != nil {
		if resp, ok := err.(*flow.ErrorResponse); ok && resp.Response.StatusCode == http.StatusNotFound {
			return nil, status.Error(codes.NotFound, "node does not exist")
		}

		return nil, status.Error(codes.Internal, err.Error())
	}

	klog.V(2).Info("Server identified as ", logServer(server))

	data := &flow.VolumeAttach{
		ServerId: nodeId,
	}

	_, _, err = d.flow.VolumeAction.Attach(ctx, volumeId, data)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	klog.Info("Volume has been attached to node")
	return &csi.ControllerPublishVolumeResponse{}, nil
}

func (d *Driver) ControllerUnpublishVolume(ctx context.Context, request *csi.ControllerUnpublishVolumeRequest) (*csi.ControllerUnpublishVolumeResponse, error) {
	if len(request.VolumeId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volume id must be provided")
	}

	if len(request.NodeId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "node id must be provided")
	}

	volumeId := flow.ParseIdentifier(request.VolumeId)
	if !volumeId.Valid() {
		return nil, status.Error(codes.InvalidArgument, "provided volume id is invalid")
	}

	nodeId := flow.ParseIdentifier(request.NodeId)
	if !nodeId.Valid() {
		return nil, status.Error(codes.InvalidArgument, "provided node id is invalid")
	}

	klog.Info("Detaching volume ", Fields{"id": volumeId}, " from server ", Fields{"id": nodeId})

	volume, _, err := d.flow.Volume.Get(ctx, volumeId)
	if err != nil {
		if resp, ok := err.(*flow.ErrorResponse); ok && resp.Response.StatusCode == http.StatusNotFound {
			klog.Warning("Assuming volume is already detached because it was not found in the api")
			return &csi.ControllerUnpublishVolumeResponse{}, nil
		}

		return nil, status.Error(codes.Internal, err.Error())
	}

	klog.V(2).Info("Volume identified as ", logVolume(volume))

	if volume.AttachedTo == nil {
		klog.Warning("Assuming volume has already been detached because it is not attached to any server")
		return &csi.ControllerUnpublishVolumeResponse{}, nil
	}

	if volume.AttachedTo.Id != nodeId {
		klog.Warning("Assuming volume has already been detached because it is attached to another server")
		return &csi.ControllerUnpublishVolumeResponse{}, nil
	}

	server, _, err := d.flow.Server.Get(ctx, nodeId)
	if err != nil {
		if resp, ok := err.(*flow.ErrorResponse); ok && resp.Response.StatusCode == http.StatusNotFound {
			klog.Warning("Assuming volume has already been detached because node was not found in the api")
			return &csi.ControllerUnpublishVolumeResponse{}, nil
		}

		return nil, status.Error(codes.Internal, err.Error())
	}

	klog.V(2).Info("Server identified as ", logServer(server))

	_, err = d.flow.VolumeAction.Detach(ctx, volumeId, nodeId)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	klog.Info("Volume has been detached")
	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

func (d *Driver) ValidateVolumeCapabilities(ctx context.Context, request *csi.ValidateVolumeCapabilitiesRequest) (*csi.ValidateVolumeCapabilitiesResponse, error) {
	if len(request.VolumeId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volume id must be provided")
	}

	if request.VolumeCapabilities == nil {
		return nil, status.Error(codes.InvalidArgument, "volume capabilities must be provided")
	}

	id := flow.ParseIdentifier(request.VolumeId)
	if !id.Valid() {
		return nil, status.Error(codes.NotFound, "provided volume id is invalid")
	}

	klog.Info("Validating volume capabilities for ", Fields{
		"id":           id,
		"capabilities": request.VolumeCapabilities,
	})

	_, _, err := d.flow.Volume.Get(ctx, id)
	if err != nil {
		if resp, ok := err.(*flow.ErrorResponse); ok && resp.Response.StatusCode == http.StatusNotFound {
			return nil, status.Error(codes.NotFound, "volume does not exist")
		}

		return nil, status.Error(codes.Internal, err.Error())
	}

	return &csi.ValidateVolumeCapabilitiesResponse{
		Confirmed: &csi.ValidateVolumeCapabilitiesResponse_Confirmed{
			VolumeCapabilities: []*csi.VolumeCapability{
				{
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
					},
				},
			},
		},
	}, nil
}

func (d *Driver) ListVolumes(ctx context.Context, request *csi.ListVolumesRequest) (*csi.ListVolumesResponse, error) {
	return nil, unsupportedControllerCapability(csi.ControllerServiceCapability_RPC_LIST_VOLUMES)
}

func (d *Driver) GetCapacity(ctx context.Context, request *csi.GetCapacityRequest) (*csi.GetCapacityResponse, error) {
	return nil, unsupportedControllerCapability(csi.ControllerServiceCapability_RPC_GET_CAPACITY)
}

func (d *Driver) ControllerGetVolume(ctx context.Context, request *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error) {
	return nil, unsupportedControllerCapability(csi.ControllerServiceCapability_RPC_GET_VOLUME)
}

func (d *Driver) ControllerExpandVolume(ctx context.Context, request *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	if len(request.VolumeId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volume id must be provided")
	}

	volumeId := flow.ParseIdentifier(request.VolumeId)
	if !volumeId.Valid() {
		return nil, status.Error(codes.InvalidArgument, "provided volume id is invalid")
	}

	size, err := extractVolumeSize(request.CapacityRange)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	sizeInGib := int(math.Ceil(float64(size) / float64(gib)))

	volume, _, err := d.flow.Volume.Get(ctx, volumeId)
	if err != nil {
		if resp, ok := err.(*flow.ErrorResponse); ok && resp.Response.StatusCode == http.StatusNotFound {
			return nil, status.Error(codes.NotFound, "volume does not exist")
		}

		return nil, status.Error(codes.Internal, err.Error())
	}

	if volume.AttachedTo != nil {
		return nil, status.Error(codes.FailedPrecondition, "volume is still attached to server")
	}

	klog.Info("Expanding volume ", Fields{
		"id":   volume.Id,
		"size": sizeInGib,
	})

	if sizeInGib <= volume.Size {
		klog.Warning("Skipping volume resize because volume size exceeds requested size")

		return &csi.ControllerExpandVolumeResponse{
			CapacityBytes:         int64(volume.Size) * gib,
			NodeExpansionRequired: request.VolumeCapability.GetBlock() == nil,
		}, nil
	}

	data := &flow.VolumeExpand{
		Size: sizeInGib,
	}

	volume, _, err = d.flow.Volume.Expand(ctx, volume.Id, data)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &csi.ControllerExpandVolumeResponse{
		CapacityBytes:         int64(volume.Size) * gib,
		NodeExpansionRequired: request.VolumeCapability.GetBlock() == nil,
	}, nil
}

func (d *Driver) CreateSnapshot(ctx context.Context, request *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	if len(request.Name) == 0 {
		return nil, status.Error(codes.InvalidArgument, "name must be provided")
	}

	if len(request.SourceVolumeId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "volume id must be provided")
	}

	volumeId := flow.ParseIdentifier(request.SourceVolumeId)
	if !volumeId.Valid() {
		return nil, status.Error(codes.InvalidArgument, "provided volume id is invalid")
	}

	snapshots, _, err := d.flow.Snapshot.List(ctx, flow.PaginationOptions{NoFilter: 1})
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	for _, snapshot := range snapshots {
		if snapshot.Name == request.Name {
			klog.Info("Found snapshot with matching requirements ", logSnapshot(snapshot))

			timestamp, err := ptypes.TimestampProto(snapshot.CreatedAt.Time())
			if err != nil {
				return nil, status.Error(codes.Internal, err.Error())
			}

			return &csi.CreateSnapshotResponse{
				Snapshot: &csi.Snapshot{
					SizeBytes:      int64(snapshot.Size) * gib,
					SnapshotId:     snapshot.Id.String(),
					SourceVolumeId: snapshot.Volume.Id.String(),
					CreationTime:   timestamp,
					ReadyToUse:     true,
				},
			}, nil
		}
	}

	data := &flow.SnapshotCreate{
		Name:     request.Name,
		VolumeId: volumeId,
	}

	snapshot, _, err := d.flow.Snapshot.Create(ctx, data)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	timestamp, err := ptypes.TimestampProto(snapshot.CreatedAt.Time())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &csi.CreateSnapshotResponse{
		Snapshot: &csi.Snapshot{
			SizeBytes:      int64(snapshot.Size) * gib,
			SnapshotId:     snapshot.Id.String(),
			SourceVolumeId: snapshot.Volume.Id.String(),
			CreationTime:   timestamp,
			ReadyToUse:     true,
		},
	}, nil
}

func (d *Driver) DeleteSnapshot(ctx context.Context, request *csi.DeleteSnapshotRequest) (*csi.DeleteSnapshotResponse, error) {
	if len(request.SnapshotId) == 0 {
		return nil, status.Error(codes.InvalidArgument, "snapshot id must be provided")
	}

	snapshotId := flow.ParseIdentifier(request.SnapshotId)
	if !snapshotId.Valid() {
		return nil, status.Error(codes.InvalidArgument, "provided snapshot id is invalid")
	}

	_, err := d.flow.Snapshot.Delete(ctx, snapshotId)
	if err != nil {
		if resp, ok := err.(*flow.ErrorResponse); ok && resp.Response.StatusCode == http.StatusNotFound {
			// assume snapshot is already deleted
			klog.Warning("Assuming snapshot is already deleted because it was not found in the api")
			return &csi.DeleteSnapshotResponse{}, nil
		}

		return nil, status.Error(codes.Internal, err.Error())
	}

	return &csi.DeleteSnapshotResponse{}, nil
}

func (d *Driver) ListSnapshots(ctx context.Context, request *csi.ListSnapshotsRequest) (*csi.ListSnapshotsResponse, error) {
	return nil, unsupportedControllerCapability(csi.ControllerServiceCapability_RPC_LIST_SNAPSHOTS)
}

func (d *Driver) ControllerGetCapabilities(ctx context.Context, request *csi.ControllerGetCapabilitiesRequest) (*csi.ControllerGetCapabilitiesResponse, error) {
	capabilityTypes := []csi.ControllerServiceCapability_RPC_Type{
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
		csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME,
		csi.ControllerServiceCapability_RPC_CREATE_DELETE_SNAPSHOT,
		csi.ControllerServiceCapability_RPC_EXPAND_VOLUME,
	}

	var capabilities []*csi.ControllerServiceCapability
	for _, capabilityType := range capabilityTypes {
		capabilities = append(capabilities, &csi.ControllerServiceCapability{
			Type: &csi.ControllerServiceCapability_Rpc{
				Rpc: &csi.ControllerServiceCapability_RPC{
					Type: capabilityType,
				},
			},
		})
	}

	return &csi.ControllerGetCapabilitiesResponse{
		Capabilities: capabilities,
	}, nil
}

func validateVolumeCapabilities(capabilities []*csi.VolumeCapability) []string {
	var violations []string
	for _, capability := range capabilities {
		if capability.AccessMode.Mode != csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER {
			violations = append(violations, fmt.Sprintf("unsupported access mode %s", capability.AccessMode.Mode.String()))
		}

		switch capability.AccessType.(type) {
		case *csi.VolumeCapability_Block:
		case *csi.VolumeCapability_Mount:
		default:
			violations = append(violations, "unsupported access type")
		}
	}
	return violations
}

func extractVolumeSize(capacityRange *csi.CapacityRange) (int64, error) {
	if capacityRange == nil {
		return defVolumeSize, nil
	}

	requiredSet := capacityRange.RequiredBytes > 0
	limitSet := capacityRange.LimitBytes > 0

	if !requiredSet && !limitSet {
		return defVolumeSize, nil
	}

	if requiredSet && limitSet && capacityRange.LimitBytes < capacityRange.RequiredBytes {
		return 0, status.Errorf(codes.InvalidArgument, "limit (%d bytes) can not be less than required (%d bytes)", capacityRange.LimitBytes, capacityRange.RequiredBytes)
	}

	if requiredSet && !limitSet && capacityRange.RequiredBytes < minVolumeSize {
		return 0, status.Errorf(codes.OutOfRange, "required (%d bytes) can not be less than minimum supported volume size (%d bytes)", capacityRange.RequiredBytes, minVolumeSize)
	}

	if limitSet && capacityRange.LimitBytes < minVolumeSize {
		return 0, status.Errorf(codes.OutOfRange, "limit (%d bytes) can not be less than minimum supported volume size (%d bytes)", capacityRange.LimitBytes, minVolumeSize)
	}

	if requiredSet && capacityRange.RequiredBytes > maxVolumeSize {
		return 0, status.Errorf(codes.OutOfRange, "required (%d bytes) can not be greater than maximum supported volume size (%d bytes)", capacityRange.RequiredBytes, maxVolumeSize)
	}

	if !requiredSet && limitSet && capacityRange.LimitBytes > maxVolumeSize {
		return 0, status.Errorf(codes.OutOfRange, "limit (%d bytes) can not be greater than maximum supported volume size (%d bytes)", capacityRange.LimitBytes, maxVolumeSize)
	}

	return determineSupportedSize(capacityRange.RequiredBytes, capacityRange.LimitBytes), nil
}

func determineSupportedSize(required int64, limit int64) int64 {
	if required == 0 && limit == 0 {
		return defVolumeSize
	}

	var size int64

	if required == 0 {
		size = limit
	} else {
		size = required
	}

	if size < minVolumeSize {
		size = minVolumeSize
	}

	if size > maxVolumeSize {
		size = maxVolumeSize
	}

	return size
}

func waitForVolume(client *sse.Client, ctx context.Context, volume *flow.Volume) error {
	klog.Info("Waiting for volume to become available")

	channel, err := client.Subscribe(ctx, sse.SpecificVolumeTopic(volume.Id))
	if err != nil {
		return err
	}

	timeoutContext, _ := context.WithTimeout(ctx, 2*time.Minute)

	for volume.Status.Id == flow.VolumeStatusWorking {
		event, err := channel.WaitForEvent(timeoutContext)
		if err != nil {
			return err
		}

		err = event.Data.ToEntity(volume)
		if err != nil {
			return err
		}

		klog.Info("Received volume update ", logVolume(volume))
	}

	return nil
}

func unsupportedControllerCapability(capability csi.ControllerServiceCapability_RPC_Type) error {
	return status.Errorf(codes.InvalidArgument, "unsupported controller capability: %v", capability)
}
