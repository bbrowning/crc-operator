package crccluster

import (
	"context"
	"reflect"

	crcv1alpha1 "github.com/bbrowning/crc-operator/pkg/apis/crc/v1alpha1"
	"github.com/operator-framework/operator-sdk/pkg/status"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	kubevirtv1 "kubevirt.io/client-go/api/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("controller_crccluster")

// Add creates a new CrcCluster Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileCrcCluster{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("crccluster-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource CrcCluster
	err = c.Watch(&source.Kind{Type: &crcv1alpha1.CrcCluster{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// Watch for changes to secondary resource VirtualMachines and requeue the owner CrcCluster
	err = c.Watch(&source.Kind{Type: &kubevirtv1.VirtualMachine{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &crcv1alpha1.CrcCluster{},
	})
	if err != nil {
		return err
	}

	return nil
}

// blank assignment to verify that ReconcileCrcCluster implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileCrcCluster{}

// ReconcileCrcCluster reconciles a CrcCluster object
type ReconcileCrcCluster struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme
}

// Reconcile reads that state of the cluster for a CrcCluster object and makes changes based on the state read
// and what is in the CrcCluster.Spec
//
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcileCrcCluster) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling CrcCluster")

	// Fetch the CrcCluster instance
	existingCrc := &crcv1alpha1.CrcCluster{}
	err := r.client.Get(context.TODO(), request.NamespacedName, existingCrc)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			reqLogger.Info("CrcCluster resource not found. Ignoring since object must be deleted.")
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		reqLogger.Error(err, "Failed to get CrcCluster.")
		return reconcile.Result{}, err
	}
	crc := existingCrc.DeepCopy()

	// Initialize status conditions
	if len(crc.Status.Conditions) == 0 {
		return r.initializeStatusConditions(crc)
	}

	// Check if the VirtualMachine already exists. If it doesn't,
	// create a new one.
	existingVirtualMachine := &kubevirtv1.VirtualMachine{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: crc.Name, Namespace: crc.Namespace}, existingVirtualMachine)
	if err != nil && errors.IsNotFound(err) {
		return r.createVirtualMachineForCrcCluster(crc)
	} else if err != nil {
		reqLogger.Error(err, "Failed to get VirtualMachine.")
		return reconcile.Result{}, err
	}
	virtualMachine := existingVirtualMachine.DeepCopy()

	// Check i the Kubernetes Service already exists. If it doesn't,
	// create a new one.
	existingK8sService := &corev1.Service{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: crc.Name, Namespace: crc.Namespace}, existingK8sService)
	if err != nil && errors.IsNotFound(err) {
		return r.createServiceForCrcCluster(crc)
	} else if err != nil {
		reqLogger.Error(err, "Failed to get Kubernetes Service.")
		return reconcile.Result{}, err
	}
	k8sService := existingK8sService.DeepCopy()

	crcStatus := crcv1alpha1.CrcClusterStatus{}
	crc.Status.DeepCopyInto(&crcStatus)

	r.updateVirtualMachineNotReadyCondition(virtualMachine, &crcStatus)
	r.updateNetworkingNotReadyCondition(k8sService, &crcStatus)
	r.updateCredentials(&crcStatus)

	// Update status if needed
	if !reflect.DeepEqual(crc.Status, crcStatus) {
		crc.Status = crcStatus
		err := r.client.Status().Update(context.TODO(), crc)
		if err != nil {
			reqLogger.Error(err, "Failed to update CrcCluster status.")
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

func (r *ReconcileCrcCluster) initializeStatusConditions(crc *crcv1alpha1.CrcCluster) (reconcile.Result, error) {
	reqLogger := log.WithValues("CrcCluster.Namespace", crc.Namespace, "CrcCluster.Name", crc.Name)

	crc.Status.Conditions = status.NewConditions(
		status.Condition{
			Type:   crcv1alpha1.ConditionTypeVirtualMachineNotReady,
			Status: corev1.ConditionTrue,
		},
		status.Condition{
			Type:   crcv1alpha1.ConditionTypeNetworkingNotReady,
			Status: corev1.ConditionTrue,
		},
		status.Condition{
			Type:   crcv1alpha1.ConditionTypeClusterNotReady,
			Status: corev1.ConditionTrue,
		},
	)

	err := r.client.Status().Update(context.TODO(), crc)
	if err != nil {
		reqLogger.Error(err, "Failed to initialize CrcCluster status.")
		return reconcile.Result{}, err
	}

	// Status conditions initialized successfully - requeue for
	// further processing
	return reconcile.Result{Requeue: true}, nil
}

func (r *ReconcileCrcCluster) createVirtualMachineForCrcCluster(crc *crcv1alpha1.CrcCluster) (reconcile.Result, error) {
	reqLogger := log.WithValues("CrcCluster.Namespace", crc.Namespace, "CrcCluster.Name", crc.Name)

	vm, err := r.newVirtualMachineForCrcCluster(crc)
	if err != nil {
		reqLogger.Error(err, "Failed to create VirtualMachine.", "VirtualMachine.Namespace", vm.Namespace, "VirtualMachine.Name", vm.Name)
		return reconcile.Result{}, err
	}

	reqLogger.Info("Creating a new VirtualMachine", "VirtualMachine.Namespace", vm.Namespace, "VirtualMachine.Name", vm.Name)
	err = r.client.Create(context.TODO(), vm)
	if err != nil {
		reqLogger.Error(err, "Failed to create VirtualMachine.", "VirtualMachine.Namespace", vm.Namespace, "VirtualMachine.Name", vm.Name)
		return reconcile.Result{}, err
	}

	// VirtualMachine created successfully - requeue for further
	// processing
	return reconcile.Result{Requeue: true}, nil
}

func (r *ReconcileCrcCluster) createServiceForCrcCluster(crc *crcv1alpha1.CrcCluster) (reconcile.Result, error) {
	reqLogger := log.WithValues("CrcCluster.Namespace", crc.Namespace, "CrcCluster.Name", crc.Name)

	svc, err := r.newServiceForCrcCluster(crc)
	if err != nil {
		reqLogger.Error(err, "Failed to create Kubernetes Service.", "Service.Namespace", svc.Namespace, "Service.Name", svc.Name)
		return reconcile.Result{}, err
	}

	reqLogger.Info("Creating a new Kubernetes Service", "Service.Namespace", svc.Namespace, "Service.Name", svc.Name)
	err = r.client.Create(context.TODO(), svc)
	if err != nil {
		reqLogger.Error(err, "Failed to create Kubernetes Service.", "Service.Namespace", svc.Namespace, "Service.Name", svc.Name)
		return reconcile.Result{}, err
	}

	// Service created successfully- requeue for further processing
	return reconcile.Result{Requeue: true}, nil
}

func (r *ReconcileCrcCluster) updateVirtualMachineNotReadyCondition(vm *kubevirtv1.VirtualMachine, crcStatus *crcv1alpha1.CrcClusterStatus) {
	conditionValue := corev1.ConditionTrue
	if vm.Status.Ready {
		conditionValue = corev1.ConditionFalse
	}

	condition := status.Condition{
		Type:   crcv1alpha1.ConditionTypeVirtualMachineNotReady,
		Status: conditionValue,
	}
	crcStatus.Conditions.SetCondition(condition)
}

func (r *ReconcileCrcCluster) updateNetworkingNotReadyCondition(svc *corev1.Service, crcStatus *crcv1alpha1.CrcClusterStatus) {
	// TODO: Assuming networking is ready just because the K8s Service
	// has a ClusterIP is not really enough to signify the networking
	// is actually ready and the VM is reachable
	conditionValue := corev1.ConditionTrue
	if svc.Spec.ClusterIP != "" {
		conditionValue = corev1.ConditionFalse
	}

	condition := status.Condition{
		Type:   crcv1alpha1.ConditionTypeNetworkingNotReady,
		Status: conditionValue,
	}
	crcStatus.Conditions.SetCondition(condition)
}

func (r *ReconcileCrcCluster) updateCredentials(crcStatus *crcv1alpha1.CrcClusterStatus) {
	if crcStatus.Conditions.IsFalseFor(crcv1alpha1.ConditionTypeVirtualMachineNotReady) &&
		crcStatus.Conditions.IsFalseFor(crcv1alpha1.ConditionTypeNetworkingNotReady) {
		crcStatus.KubeAdminPassword = "DEP6h-PvR7K-7fYqe-IhLUP"
	}
}

// TODO: Obviously none of the hardcoded image/cpu/memory values below
// should be hardcoded
func (r *ReconcileCrcCluster) newVirtualMachineForCrcCluster(crc *crcv1alpha1.CrcCluster) (*kubevirtv1.VirtualMachine, error) {
	labels := map[string]string{
		"crcCluster":          crc.Name,
		"kubevirt.io/domain":  crc.Name,
		"vm.kubevirt.io/name": crc.Name,
	}

	podNetwork := kubevirtv1.PodNetwork{}

	containerDisk := kubevirtv1.ContainerDiskSource{
		Image: "quay.io/bbrowning/crc_bundle_4.4.5",
	}

	vmRunning := true
	vmCPU := kubevirtv1.CPU{
		Cores:   2,
		Sockets: 4,
		Threads: 1,
	}
	guestMemory := resource.MustParse("16Gi")
	vmMemory := kubevirtv1.Memory{
		Guest: &guestMemory,
	}
	diskBootOrder := uint(1)
	diskTarget := kubevirtv1.DiskTarget{
		Bus: "virtio",
	}
	ifMasquerade := kubevirtv1.InterfaceMasquerade{}
	ifMultiqueue := true
	vmTemplate := kubevirtv1.VirtualMachineInstanceTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels: labels,
		},
		Spec: kubevirtv1.VirtualMachineInstanceSpec{
			Domain: kubevirtv1.DomainSpec{
				CPU:    &vmCPU,
				Memory: &vmMemory,
				Resources: kubevirtv1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("2"),
						corev1.ResourceMemory: resource.MustParse("9Gi"),
					},
					OvercommitGuestOverhead: true,
				},
				Devices: kubevirtv1.Devices{
					Disks: []kubevirtv1.Disk{
						{
							Name:      "rootdisk",
							BootOrder: &diskBootOrder,
							DiskDevice: kubevirtv1.DiskDevice{
								Disk: &diskTarget,
							},
						},
					},
					Interfaces: []kubevirtv1.Interface{
						{
							Name:  "nic0",
							Model: "virtio",
							InterfaceBindingMethod: kubevirtv1.InterfaceBindingMethod{
								Masquerade: &ifMasquerade,
							},
						},
					},
					NetworkInterfaceMultiQueue: &ifMultiqueue,
				},
				Machine: kubevirtv1.Machine{
					Type: "q35",
				},
			},
			Hostname: "crc",
			Networks: []kubevirtv1.Network{
				{
					Name: "nic0",
					NetworkSource: kubevirtv1.NetworkSource{
						Pod: &podNetwork,
					},
				},
			},
			Volumes: []kubevirtv1.Volume{
				{
					Name: "rootdisk",
					VolumeSource: kubevirtv1.VolumeSource{
						ContainerDisk: &containerDisk,
					},
				},
			},
		},
	}

	vm := &kubevirtv1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      crc.Name,
			Namespace: crc.Namespace,
			Labels:    labels,
		},
		Spec: kubevirtv1.VirtualMachineSpec{
			Running:  &vmRunning,
			Template: &vmTemplate,
		},
	}

	if err := controllerutil.SetControllerReference(crc, vm, r.scheme); err != nil {
		return vm, err
	}

	return vm, nil
}

func (r *ReconcileCrcCluster) newServiceForCrcCluster(crc *crcv1alpha1.CrcCluster) (*corev1.Service, error) {
	labels := map[string]string{
		"crcCluster": crc.Name,
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      crc.Name,
			Namespace: crc.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:       "ssh",
					Protocol:   corev1.ProtocolTCP,
					Port:       2022,
					TargetPort: intstr.FromInt(22),
				},
				{
					Name:       "api",
					Protocol:   corev1.ProtocolTCP,
					Port:       6443,
					TargetPort: intstr.FromInt(6443),
				},
				{
					Name:       "http",
					Protocol:   corev1.ProtocolTCP,
					Port:       80,
					TargetPort: intstr.FromInt(80),
				},
				{
					Name:       "https",
					Protocol:   corev1.ProtocolTCP,
					Port:       443,
					TargetPort: intstr.FromInt(443),
				},
			},
			Selector: map[string]string{"crcCluster": crc.Name},
			Type:     corev1.ServiceTypeClusterIP,
		},
	}

	if err := controllerutil.SetControllerReference(crc, svc, r.scheme); err != nil {
		return svc, err
	}

	return svc, nil
}
