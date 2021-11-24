package util

import "github.com/flowswiss/goclient/flow"

// MapSnapshotReadyToUse maps the flow.Snapshot status (proprietary) to the csi.CreateSnapshotResponse ReadyToUse field
// to indicate if a snapshot can be used or needs to do post processing
func MapSnapshotReadyToUse(snapshot *flow.Snapshot) bool {
	var statusReadyToUseMap = map[flow.SnaphostStatusKey]bool{
		flow.SnapshotStatusKeyAvailable: true,
		flow.SnapshotStatusKeyCreating: false,
		flow.SnapshotStatusKeyError: false,
	}
	return statusReadyToUseMap[snapshot.Status.Key]
}
