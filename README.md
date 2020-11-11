# Container Storage Interface
The official [Container Storage Interface](https://github.com/container-storage-interface/spec)
(CSI) for [Flow Swiss](https://flow.swiss/) and [Cloudbit](https://www.cloudbit.ch/)
block storage volumes. This plugin enables you to use our block storage devices
within Kubernetes or any other Container Orchestrator. 

## Deploying on Kubernetes
If you use a Managed Kubernetes Cluster, the CSI driver will be deployed
automatically, and you do not have to setup anything. If you however choose to
host your own cluster on our Compute Platform, you need to apply the following
steps to deploy this driver.

#### 1. Create an API token
Go into our portal and navigate to the Application Tokens section. Here you can
create a new one by giving it a name. Copy the generated token and store it
somewhere safe.

* [Create an application token on Flow Swiss](https://my.flow.swiss/#/organization/applications)
* [Create an application token on Cloudbit](https://my.cloudbit.ch/#/organization/applications)

#### 2. Create secret
Create a secret containing the token in a namespace of your choice on your
cluster.

```
kubectl create secret generic flow \
	--from-literal=base=[environment-url] \
	--from-literal=token=[application-token]
```

Replace the `application-token` placeholder with the previously generated API
token. For the placeholder `environment-url`, enter the URL of the system you
are using (`https://api.flow.swiss/` or `https://api.cloudbit.ch/`)

The same configuration can be achieved through the following yaml configuration:
```
apiVersion: v1
kind: Secret
metadata:
  name: flow
data:
  base: [environment-url-base64]
  token: [application-token-base64]
```

#### 3. Deploy the CSI plugin
All needed kubernetes configurations can be found in this repository in the
[deployments](deployments) directory. Choose the desired version and apply it
using the kubectl command.
```
kubectl apply -f deployments/kubernetes/VERSION
```

#### 4. Verify installation
To verify your installation you can create a simple volume claim by applying the
following configuration.
```
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: csi-test
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 1Gi
  storageClassName: flow-block-storage
```

If everything was successful you can check that the volume has been created by
running the following command.
```
$ kubectl get pv
NAME                                       CAPACITY   ACCESS MODES   RECLAIM POLICY   STATUS   CLAIM              STORAGECLASS         REASON   AGE
pvc-6d28a5c7-43f6-4be8-afb7-ab0dca6ed48b   1Gi        RWO            Delete           Bound    default/csi-test   flow-block-storage            7s
```
