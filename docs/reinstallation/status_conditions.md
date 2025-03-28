# Cluster reinstallation status conditions

The SiteConfig operator defines several reinstallation status conditions to help track the progress of a cluster reinstallation. These conditions provide insights into various stages of the process.

## Condition types

The following condition types are available:

1. `ReinstallRequestProcessed` - Indicates the overall status of the reinstallation request.
2. `ReinstallRequestValidated` - Verifies the validity of the reinstallation request.
3. `ReinstallPreservationDataBackedup` - Tracks the backup status of preserved data.
4. `ReinstallClusterIdentityDataDetected` - Determines whether cluster identity data is available for preservation.
5. `ReinstallRenderedManifestsDeleted` - Monitors the deletion of rendered manifests associated with the `ClusterInstance` custom resource.
6. `ReinstallPreservationDataRestored` - Tracks the restoration status of preserved data.

## Condition details

### ReinstallRequestValidated
Indicates whether the reinstallation request is validated.

#### Condition reasons:
- `Completed`: The reinstallation request is valid.
- `Failed`: The reinstallation request is invalid.

### ReinstallPreservationDataBackedup
Tracks the backup process of `Secret` and `ConfigMap` objects required for preservation.

#### Condition reasons:
- `PreservationNotRequired`: No backup required as `spec.reinstall.preservationMode: None` is set.
- `DataUnavailable`: No `Secret` or `ConfigMap` objects with the preservation label are found in the `ClusterInstance` namespace.
- `Completed`: `Secret` and `ConfigMap` objects are successfully backed up.
- `Failed`: One or more `Secret` and `ConfigMap` objects were not be backed up.

### ReinstallClusterIdentityDataDetected
Determines if cluster identity data is available for preservation.

#### Condition reasons:
- `PreservationNotRequired`: Data preservation is not required. The `preservationMode` field is set to `None`.
- `DataAvailable`: Cluster identity `Secret` and `ConfigMap` objects are successfully located.
- `DataUnavailable`: No cluster identity `Secret` or `ConfigMap` objects were detected.
- `Failed`: The preservation mode is set to `ClusterIdentity`, but no cluster identity data was found.

### ReinstallRenderedManifestsDeleted
Tracks the deletion of rendered manifests associated with the ClusterInstance.

#### Condition reasons:
- `InProgress`: Deleting rendered manifests is in progress.
- `Completed`: Successfully deleted all rendered manifests.
- `Failed`: Failed to delete one or more rendered manifests.
- `TimedOut`: Timed out while waiting for rendered manifests to be deleted.

### ReinstallPreservationDataRestored
Tracks the restoration of previously backed-up `Secret` and `ConfigMap` objects.

#### Condition reasons:
- `PreservationNotRequired`: Preservation is not required (`preservationMode: None`).
- `DataUnavailable`: No preserved `Secret` or `ConfigMap` objects detected for restoration.
- `Completed`: `Secret` and `ConfigMap` objects are successfully restored.
- `Failed`: Failed to restore one or more `Secret` and `ConfigMap` objects.

### ReinstallRequestProcessed
Indicates the overall status of the reinstallation request processing.

#### Condition reasons:
- `InProgress`: The reinstallation process is ongoing.
- `Completed`: The reinstallation process successfully completed, and the cluster is ready for reprovisioning.
- `Failed`: The reinstallation process encountered an error and failed.
- `TimedOut`: The reinstallation process exceeded the expected time limit and did not complete successfully.

