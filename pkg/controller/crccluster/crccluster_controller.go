package crccluster

// TODO: This should be split out of one giant file, obviously...
import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"strings"
	"time"

	crcv1alpha1 "github.com/bbrowning/crc-operator/pkg/apis/crc/v1alpha1"
	libMachineLog "github.com/code-ready/machine/libmachine/log"
	sshClient "github.com/code-ready/machine/libmachine/ssh"
	"github.com/go-logr/logr"
	"github.com/google/uuid"
	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	routev1 "github.com/openshift/api/route/v1"
	configv1Client "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"
	operatorv1Client "github.com/openshift/client-go/operator/clientset/versioned/typed/operator/v1"
	routev1Client "github.com/openshift/client-go/route/clientset/versioned/typed/route/v1"
	"github.com/operator-framework/operator-sdk/pkg/status"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/ssh"
	appsv1 "k8s.io/api/apps/v1"
	certificatesv1beta1 "k8s.io/api/certificates/v1beta1"
	corev1 "k8s.io/api/core/v1"
	networkingv1beta1 "k8s.io/api/networking/v1beta1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	kubevirtv1 "kubevirt.io/client-go/api/v1"
	cdiv1 "kubevirt.io/containerized-data-importer/pkg/apis/core/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var log = logf.Log.WithName("controller_crccluster")

var defaultBundleName = os.Getenv("DEFAULT_BUNDLE_NAME")
var routesHelperImage = os.Getenv("ROUTES_HELPER_IMAGE")
var bundleNs = os.Getenv("POD_NAMESPACE")

const (
	sshPort int = 2022
)

// Add creates a new CrcCluster Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	if defaultBundleName == "" {
		log.Error(fmt.Errorf("DEFAULT_BUNDLE_NAME environment variable must be set"), "")
		os.Exit(1)
	}
	if routesHelperImage == "" {
		log.Error(fmt.Errorf("ROUTES_HELPER_IMAGE environment variable must be set"), "")
		os.Exit(1)
	}
	if bundleNs == "" {
		log.Error(fmt.Errorf("POD_NAMESPACE environment variable must be set"), "")
		os.Exit(1)
	}
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {

	return &ReconcileCrcCluster{
		client:         mgr.GetClient(),
		scheme:         mgr.GetScheme(),
		routeAPIExists: routeAPIExists(mgr),
	}
}

func routeAPIExists(mgr manager.Manager) bool {
	// See if we have OpenShift Route APIs available
	routeAPIExists := true
	gvk := schema.GroupVersionKind{
		Group:   "route.openshift.io",
		Kind:    "Route",
		Version: "v1",
	}
	_, err := mgr.GetRESTMapper().RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		routeAPIExists = false
	}
	return routeAPIExists
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("crccluster-controller", mgr, controller.Options{
		Reconciler: r,
		// SSHing into the nodes can take quite a while sometimes, so
		// be pretty generous with concurrency here
		MaxConcurrentReconciles: 10,
	})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource CrcCluster
	err = c.Watch(&source.Kind{Type: &crcv1alpha1.CrcCluster{}}, &handler.EnqueueRequestForObject{}, predicate.GenerationChangedPredicate{})
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

	// Watch for changes to secondary resource Kubernetes Service and
	// requeue the owner CrcCluster
	err = c.Watch(&source.Kind{Type: &corev1.Service{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &crcv1alpha1.CrcCluster{},
	})
	if err != nil {
		return err
	}

	if routeAPIExists(mgr) {
		// Watch for changes to secondary resource OpenShift Route and
		// requeue the owner CrcCluster
		err = c.Watch(&source.Kind{Type: &routev1.Route{}}, &handler.EnqueueRequestForOwner{
			IsController: true,
			OwnerType:    &crcv1alpha1.CrcCluster{},
		})
		if err != nil {
			return err
		}
	} else {
		// Watch for changes to secondary resource Kubernetes Ingress
		// and requeue the owner CrcCluster
		err = c.Watch(&source.Kind{Type: &networkingv1beta1.Ingress{}}, &handler.EnqueueRequestForOwner{
			IsController: true,
			OwnerType:    &crcv1alpha1.CrcCluster{},
		})
		if err != nil {
			return err
		}
	}

	// Watch for changes to secondary resource Kubernetes Deployment
	// and requeue the owner CrcCluster
	err = c.Watch(&source.Kind{Type: &appsv1.Deployment{}}, &handler.EnqueueRequestForOwner{
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

	// Whether this cluster has OpenShift Routes
	routeAPIExists bool
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
		crc, err = r.initializeStatusConditions(reqLogger, crc)
	}

	bundle, err := r.bundleForCrc(crc)
	if err != nil {
		// TODO: This is a permanent error and thus should just fail
		// some condition
		reqLogger.Error(err, "Failed to get bundle for CrcCluster.")
		return reconcile.Result{}, err
	}
	reqLogger.Info("Located bundle for cluster", "Bundle.Name", bundle.Name, "Bundle.Spec.Image", bundle.Spec.Image)

	virtualMachine, err := r.ensureVirtualMachineExists(reqLogger, crc, bundle)
	if err != nil {
		return reconcile.Result{}, err
	}

	k8sService, err := r.ensureServiceExists(reqLogger, crc)
	if err != nil {
		return reconcile.Result{}, err
	}

	apiHost := ""
	if r.routeAPIExists {
		route, err := r.ensureAPIRouteExists(reqLogger, crc)
		if err != nil {
			return reconcile.Result{}, err
		}
		apiHost = route.Spec.Host
	} else {
		ingress, err := r.ensureAPIIngressExists(reqLogger, crc)
		if err != nil {
			return reconcile.Result{}, err
		}
		apiHost = ingress.Spec.Rules[0].Host
	}
	crc.Status.APIURL = fmt.Sprintf("https://%s", apiHost)
	crc.Status.BaseDomain = strings.Replace(apiHost, "api.", "", 1)
	crc.Status.ConsoleURL = fmt.Sprintf("https://%s", routeHostForDomain(crc.Status.BaseDomain, "console", "openshift-console"))

	r.updateVirtualMachineNotReadyCondition(virtualMachine, crc)
	if virtualMachine.Spec.Running != nil && !*virtualMachine.Spec.Running {
		crc.Status.Stopped = true
	} else {
		crc.Status.Stopped = false
	}

	r.updateNetworkingNotReadyCondition(k8sService, crc)

	if err := r.updateCredentials(crc); err != nil {
		return reconcile.Result{}, err
	}

	crc, err = r.updateCrcClusterStatus(crc)
	if err != nil {
		return reconcile.Result{}, err
	}

	// Don't attempt any further reconciling until the VM is ready
	if crc.Status.Conditions.IsTrueFor(crcv1alpha1.ConditionTypeVirtualMachineNotReady) {
		if crc.Status.Stopped {
			// The VM is not ready but the cluster is stopped, so we're good
			reqLogger.Info("Cluster is stopped and virtual machine is not ready - this is expected")
			return reconcile.Result{}, nil
		}
		reqLogger.Info("Waiting on the VirtualMachine to become Ready before continuing")
		return reconcile.Result{RequeueAfter: time.Second * 10}, nil
	}

	sshClient, err := createSSHClient(k8sService, bundle)
	if err != nil {
		reqLogger.Error(err, "Failed to create SSH Client.")
		return reconcile.Result{}, err
	}

	if crc.Status.Conditions.IsTrueFor(crcv1alpha1.ConditionTypeKubeletNotReady) {
		crc, err = r.ensureKubeletStarted(reqLogger, sshClient, crc, bundle)
		if err != nil {
			reqLogger.Error(err, "Failed to start Kubelet.")
			return reconcile.Result{}, err
		}
	}

	crcK8sConfig, err := restConfigFromCrcCluster(crc, bundle)
	if err != nil {
		reqLogger.Error(err, "Error generating Kubernetes REST config from kubeconfig.")
		return reconcile.Result{}, err
	}
	k8sClient, err := kubernetes.NewForConfig(crcK8sConfig)
	if err != nil {
		reqLogger.Error(err, "Error generating Kubernetes client from REST config.")
		return reconcile.Result{}, err
	}

	insecureCrcK8sConfig := rest.CopyConfig(crcK8sConfig)
	insecureCrcK8sConfig.Insecure = true
	insecureCrcK8sConfig.CAData = []byte{}
	insecureK8sClient, err := kubernetes.NewForConfig(insecureCrcK8sConfig)
	if err != nil {
		reqLogger.Error(err, "Error generating Kubernetes client from REST config.")
		return reconcile.Result{}, err
	}

	if crc.Status.Conditions.IsTrueFor(crcv1alpha1.ConditionTypeClusterNotConfigured) {
		reqLogger.Info("Updating cluster admin password.")
		if err := r.updateClusterAdminUser(crc, insecureK8sClient); err != nil {
			reqLogger.Error(err, "Error updating cluster admin password.")
			return reconcile.Result{}, err
		}

		reqLogger.Info("Updating pull secret.")
		if err := r.updatePullSecret(crc, sshClient, insecureK8sClient); err != nil {
			reqLogger.Error(err, "Error updating pull secret.")
			return reconcile.Result{}, err
		}

		reqLogger.Info("Updating cluster ID.")
		crc, err = r.updateClusterID(crc, insecureCrcK8sConfig)
		if err != nil {
			reqLogger.Error(err, "Error updating cluster ID.")
			return reconcile.Result{}, err
		}

		reqLogger.Info("Updating cluster admin client certificate.")
		crc, err = r.updateClusterAdminCert(crc, insecureK8sClient)
		if err != nil {
			reqLogger.Error(err, "Error updating cluster admin client certificate.")
			return reconcile.Result{}, err
		}

		reqLogger.Info("Removing shared kubeadmin secret.")
		if err := r.removeSharedKubeadminSecret(insecureK8sClient); err != nil {
			reqLogger.Error(err, "Error removing shared kubeadmin secret.")
			return reconcile.Result{}, err
		}

		reqLogger.Info("Approving CSRs.")
		if err := r.approveCSRs(insecureK8sClient); err != nil {
			reqLogger.Error(err, "Error approving CSRs.")
			return reconcile.Result{}, err
		}

		reqLogger.Info("Updating ingress domain.")
		if err := r.updateIngressDomain(crc, insecureCrcK8sConfig); err != nil {
			reqLogger.Error(err, "Error updating ingress domain.")
			return reconcile.Result{}, err
		}

		crc.SetConditionBool(crcv1alpha1.ConditionTypeClusterNotConfigured, false)
		crc, err = r.updateCrcClusterStatus(crc)
		if err != nil {
			return reconcile.Result{}, err
		}
	}

	reqLogger.Info("Ensuring ingress controllers updated.")
	if err := r.ensureIngressControllersUpdated(crc, insecureCrcK8sConfig); err != nil {
		reqLogger.Error(err, "Error updating ingress controllers.")
		return reconcile.Result{}, err
	}

	reqLogger.Info("Cleaning up terminating OpenShift router pods.")
	if err := r.cleanupTerminatingRouterPods(insecureK8sClient); err != nil {
		reqLogger.Error(err, "Error cleaning up terminating OpenShift router pods.")
		return reconcile.Result{}, err
	}

	reqLogger.Info("Checking for requestheader-client-ca.")
	hasRequestCA, err := r.hasRequestHeaderClientCA(insecureK8sClient)
	if err != nil {
		reqLogger.Error(err, "Error checking for requestheader-client-ca.")
		return reconcile.Result{}, err
	}
	if !hasRequestCA {
		reqLogger.Info("No requestheader-client-ca yet - trying again.")
		return reconcile.Result{RequeueAfter: time.Second * 10}, nil
	}

	reqLogger.Info("Waiting on OpenShift API Server to stabilize.")
	stable, err := r.waitForOpenShiftAPIServer(insecureK8sClient)
	if err != nil {
		reqLogger.Error(err, "Error waiting on OpenShift API Server to stabilize.")
		return reconcile.Result{}, err
	}
	if !stable {
		return reconcile.Result{RequeueAfter: time.Second * 10}, nil
	}

	reqLogger.Info("Updating infrastructure status.apiServerURL.")
	if err := r.updateAPIServerURL(crc, insecureCrcK8sConfig); err != nil {
		reqLogger.Error(err, "Error updating infrastructure status.apiServerURL.")
		return reconcile.Result{}, err
	}

	reqLogger.Info("Updating default routes.")
	routesUpdated, err := r.updateDefaultRoutes(crc, insecureCrcK8sConfig)
	if err != nil {
		reqLogger.Error(err, "Error updating default routes.")
		return reconcile.Result{}, err
	} else if routesUpdated {
		return reconcile.Result{RequeueAfter: time.Second * 20}, nil
	}

	reqLogger.Info("Waiting on cluster to stabilize.")
	notReadyPods, err := r.waitForClusterToStabilize(insecureK8sClient)
	if err != nil {
		reqLogger.Error(err, "Error waiting on cluster to stabilize.")
		return reconcile.Result{}, err
	}
	if len(notReadyPods) > 0 {
		notReadyPodNames := []string{}
		for _, pod := range notReadyPods {
			notReadyPodNames = append(notReadyPodNames, fmt.Sprintf("%s/%s", pod.Namespace, pod.Name))
		}
		reqLogger.Info("Still waiting on some pods to report as ready.", "NotReadyPodNames", notReadyPodNames)
		return reconcile.Result{RequeueAfter: time.Second * 10}, nil
	}

	reqLogger.Info("Deploying route helper pod.")
	if err := r.deployRouteHelperPod(crc); err != nil {
		reqLogger.Error(err, "Error deploying route helper pod.")
		return reconcile.Result{}, err
	}

	reqLogger.Info("Waiting on console URL to be available.")
	consoleUp, err := r.waitForConsoleURL(crc)
	if err != nil {
		reqLogger.Error(err, "Error waiting on console URL to be available.")
		return reconcile.Result{}, err
	}
	if consoleUp {
		reqLogger.Info("Marking CrcCluster as Ready")
		crc.SetConditionBool(crcv1alpha1.ConditionTypeReady, true)
		crc, err = r.updateCrcClusterStatus(crc)
		if err != nil {
			reqLogger.Error(err, "Error updating CrcCluster status")
			return reconcile.Result{}, err
		}
	} else {
		reqLogger.Info("Marking CrcCluster as NotReady")
		crc.SetConditionBool(crcv1alpha1.ConditionTypeReady, false)
		crc, err = r.updateCrcClusterStatus(crc)
		if err != nil {
			reqLogger.Error(err, "Error updating CrcCluster status")
			return reconcile.Result{}, err
		}
		return reconcile.Result{RequeueAfter: time.Second * 10}, nil
	}

	fmt.Printf("blah blah k8sClient %v\n", k8sClient)

	return reconcile.Result{}, nil
}

func (r *ReconcileCrcCluster) waitForConsoleURL(crc *crcv1alpha1.CrcCluster) (bool, error) {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{Transport: transport}
	resp, err := client.Get(crc.Status.ConsoleURL)
	if err != nil {
		return false, err
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return true, nil
	}
	return false, nil
}

func (r *ReconcileCrcCluster) deployRouteHelperPod(crc *crcv1alpha1.CrcCluster) error {
	if err := r.ensureRouteHelperServiceAccount(crc); err != nil {
		return err
	}
	if err := r.ensureRouteHelperRole(crc); err != nil {
		return err
	}
	if err := r.ensureRouteHelperRoleBinding(crc); err != nil {
		return err
	}
	if err := r.ensureRouteHelperDeployment(crc); err != nil {
		return err
	}
	return nil
}

func (r *ReconcileCrcCluster) ensureRouteHelperServiceAccount(crc *crcv1alpha1.CrcCluster) error {
	labels := map[string]string{
		"crcCluster": crc.Name,
	}

	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-route-helper", crc.Name),
			Namespace: crc.Namespace,
			Labels:    labels,
		},
	}

	if err := controllerutil.SetControllerReference(crc, sa, r.scheme); err != nil {
		return err
	}

	existingSa := &corev1.ServiceAccount{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: sa.Name, Namespace: sa.Namespace}, existingSa)
	if err != nil && errors.IsNotFound(err) {
		err = r.client.Create(context.TODO(), sa)
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return nil
}

func (r *ReconcileCrcCluster) ensureRouteHelperRole(crc *crcv1alpha1.CrcCluster) error {
	labels := map[string]string{
		"crcCluster": crc.Name,
	}

	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-route-helper", crc.Name),
			Namespace: crc.Namespace,
			Labels:    labels,
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"crc.developer.openshift.io"},
				Resources: []string{"*"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{"apps"},
				Resources: []string{"deployments"},
				Verbs:     []string{"get", "list", "watch"},
			},
			{
				APIGroups: []string{"networking.k8s.io"},
				Resources: []string{"ingresses"},
				Verbs:     []string{"create", "delete", "get", "list", "patch", "update", "watch"},
			},
			{
				APIGroups: []string{"route.openshift.io"},
				Resources: []string{"routes", "routes/custom-host"},
				Verbs:     []string{"create", "delete", "get", "list", "patch", "update", "watch"},
			},
		},
	}

	if err := controllerutil.SetControllerReference(crc, role, r.scheme); err != nil {
		return err
	}

	existingRole := &rbacv1.Role{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: role.Name, Namespace: role.Namespace}, existingRole)
	if err != nil && errors.IsNotFound(err) {
		err = r.client.Create(context.TODO(), role)
		if err != nil {
			return err
		}
		// Get the Role again
		existingRole = &rbacv1.Role{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: role.Name, Namespace: role.Namespace}, existingRole)
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	if !reflect.DeepEqual(role.Rules, existingRole.Rules) {
		err := r.client.Update(context.TODO(), role)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *ReconcileCrcCluster) ensureRouteHelperRoleBinding(crc *crcv1alpha1.CrcCluster) error {
	labels := map[string]string{
		"crcCluster": crc.Name,
	}

	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-route-helper", crc.Name),
			Namespace: crc.Namespace,
			Labels:    labels,
		},
	}

	rb.Subjects = []rbacv1.Subject{
		{
			Kind:      "ServiceAccount",
			Name:      rb.Name,
			Namespace: rb.Namespace,
		},
	}

	rb.RoleRef = rbacv1.RoleRef{
		Kind:     "Role",
		Name:     rb.Name,
		APIGroup: "rbac.authorization.k8s.io",
	}

	if err := controllerutil.SetControllerReference(crc, rb, r.scheme); err != nil {
		return err
	}

	existingRb := &rbacv1.RoleBinding{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: rb.Name, Namespace: rb.Namespace}, existingRb)
	if err != nil && errors.IsNotFound(err) {
		err = r.client.Create(context.TODO(), rb)
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return nil
}

func (r *ReconcileCrcCluster) ensureRouteHelperDeployment(crc *crcv1alpha1.CrcCluster) error {
	labels := map[string]string{
		"crcCluster": crc.Name,
	}

	labelSelector := &metav1.LabelSelector{
		MatchLabels: labels,
	}
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-route-helper", crc.Name),
			Namespace: crc.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: labelSelector,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            "route-helper",
							Image:           routesHelperImage,
							ImagePullPolicy: corev1.PullAlways,
							Env: []corev1.EnvVar{
								{
									Name:  "CRC_NAME",
									Value: crc.Name,
								},
								{
									Name:  "CRC_NAMESPACE",
									Value: crc.Namespace,
								},
							},
						},
					},
				},
			},
		},
	}

	deployment.Spec.Template.Spec.ServiceAccountName = deployment.Name

	if err := controllerutil.SetControllerReference(crc, deployment, r.scheme); err != nil {
		return err
	}

	existingDeployment := &appsv1.Deployment{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: deployment.Name, Namespace: deployment.Namespace}, existingDeployment)
	if err != nil && errors.IsNotFound(err) {
		err = r.client.Create(context.TODO(), deployment)
		if err != nil {
			return err
		}
		// Get the Deployment again
		existingDeployment = &appsv1.Deployment{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: deployment.Name, Namespace: deployment.Namespace}, existingDeployment)
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	if !reflect.DeepEqual(deployment.Spec, existingDeployment.Spec) {
		fmt.Println("Deployment specs differ, but not updating because it causes an infinite loop until doing a smarter diff.")
		// err := r.client.Update(context.TODO(), deployment)
		// if err != nil {
		// 	return err
		// }
	}
	return nil
}

func (r *ReconcileCrcCluster) updateCrcClusterStatus(crc *crcv1alpha1.CrcCluster) (*crcv1alpha1.CrcCluster, error) {
	existingCrc := &crcv1alpha1.CrcCluster{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: crc.Name, Namespace: crc.Namespace}, existingCrc)
	if err != nil {
		return crc, err
	}
	if !reflect.DeepEqual(crc.Status, existingCrc.Status) {
		err := r.client.Status().Update(context.TODO(), crc)
		if err != nil {
			return crc, err
		}
		updatedCrc := &crcv1alpha1.CrcCluster{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: crc.Name, Namespace: crc.Namespace}, updatedCrc)
		if err != nil {
			return crc, err
		}
		crc = updatedCrc.DeepCopy()
	}
	return crc, nil
}

func restConfigFromCrcCluster(crc *crcv1alpha1.CrcCluster, bundle *crcv1alpha1.CrcBundle) (*rest.Config, error) {
	bundleKubeconfig, err := base64.StdEncoding.DecodeString(bundle.Spec.Kubeconfig)
	if err != nil {
		return nil, err
	}
	kubeconfigBytes := []byte(strings.ReplaceAll(string(bundleKubeconfig), "https://api.crc.testing:6443", crc.Status.APIURL))
	config, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigBytes)
	if err != nil {
		return nil, err
	}

	return config, nil
}

func (r *ReconcileCrcCluster) updatePullSecret(crc *crcv1alpha1.CrcCluster, sshClient sshClient.Client, k8sClient *kubernetes.Clientset) error {
	// Copy pull secret to node
	pullSecretScript := fmt.Sprintf(`
set -e
echo "%s" | base64 -d | sudo tee /var/lib/kubelet/config.json
sudo chmod 0600 /var/lib/kubelet/config.json
`, crc.Spec.PullSecret)
	output, err := sshClient.Output(pullSecretScript)
	if err != nil {
		fmt.Println(output)
		return err
	}

	// Update pull-secret secret in the cluster
	openshiftConfigNs := "openshift-config"
	secretName := "pull-secret"
	secret, err := k8sClient.CoreV1().Secrets(openshiftConfigNs).Get(secretName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	crcPullSecretBytes, err := base64.StdEncoding.DecodeString(crc.Spec.PullSecret)
	if err != nil {
		return err
	}
	existingPullSecretBytes, found := secret.Data[".dockerconfigjson"]
	if !found || !bytes.Equal(existingPullSecretBytes, crcPullSecretBytes) {
		secret.Data[".dockerconfigjson"] = crcPullSecretBytes
		if _, err := k8sClient.CoreV1().Secrets(openshiftConfigNs).Update(secret); err != nil {
			return err
		}
	}
	return nil
}

func (r *ReconcileCrcCluster) updateClusterAdminUser(crc *crcv1alpha1.CrcCluster, k8sClient *kubernetes.Clientset) error {
	secretName := "htpass-secret"
	openshiftConfigNs := "openshift-config"
	secret, err := k8sClient.CoreV1().Secrets(openshiftConfigNs).Get(secretName, metav1.GetOptions{})
	if err != nil {
		return err
	}
	passwordHash, err := hashPassword(crc.Status.KubeAdminPassword)
	if err != nil {
		return err
	}
	// TODO: Need to generate a new kubeconfig here and a cert and/or
	// token to go along with it
	htpasswdBytes := []byte("kubeadmin:")
	htpasswdBytes = append(htpasswdBytes, passwordHash...)
	htpasswdBytes = append(htpasswdBytes, []byte("\n")...)
	existingHtpasswdBytes, found := secret.Data["htpasswd"]
	if !found || !bytes.Equal(existingHtpasswdBytes, htpasswdBytes) {
		secret.Data["htpasswd"] = htpasswdBytes
		if _, err := k8sClient.CoreV1().Secrets(openshiftConfigNs).Update(secret); err != nil {
			return err
		}
	}

	crbName := "crc-cluster-admin"
	_, err = k8sClient.RbacV1().ClusterRoleBindings().Get(crbName, metav1.GetOptions{})
	if err != nil && errors.IsNotFound(err) {
		crb := &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name: crbName,
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "ClusterRole",
				Name:     "cluster-admin",
			},
			Subjects: []rbacv1.Subject{
				{
					APIGroup: "rbac.authorization.k8s.io",
					Kind:     "User",
					Name:     "kubeadmin",
				},
			},
		}
		_, err = k8sClient.RbacV1().ClusterRoleBindings().Create(crb)
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	return nil
}

func (r *ReconcileCrcCluster) updateClusterID(crc *crcv1alpha1.CrcCluster, restConfig *rest.Config) (*crcv1alpha1.CrcCluster, error) {
	if crc.Status.ClusterID == "" {
		clusterID, err := uuid.NewRandom()
		if err != nil {
			return crc, err
		}
		crc.Status.ClusterID = clusterID.String()
		crc, err = r.updateCrcClusterStatus(crc)
		if err != nil {
			return crc, err
		}
	}

	configClient, err := configv1Client.NewForConfig(restConfig)
	if err != nil {
		return crc, err
	}
	clusterVersion, err := configClient.ClusterVersions().Get("version", metav1.GetOptions{})
	if err != nil {
		return crc, err
	}
	crcClusterID := configv1.ClusterID(crc.Status.ClusterID)
	if clusterVersion.Spec.ClusterID != crcClusterID {
		clusterVersion.Spec.ClusterID = crcClusterID
		if _, err := configClient.ClusterVersions().Update(clusterVersion); err != nil {
			return crc, err
		}
	}

	return crc, nil
}

func (r *ReconcileCrcCluster) hasRequestHeaderClientCA(k8sClient *kubernetes.Clientset) (bool, error) {
	configMap, err := k8sClient.CoreV1().ConfigMaps("kube-system").Get("extension-apiserver-authentication", metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	if configMap.Data["requestheader-client-ca-file"] != "" {
		return true, nil
	}
	return false, nil
}

func (r *ReconcileCrcCluster) cleanupTerminatingRouterPods(k8sClient *kubernetes.Clientset) error {
	openshiftIngressNs := "openshift-ingress"
	pods, err := k8sClient.CoreV1().Pods(openshiftIngressNs).List(metav1.ListOptions{FieldSelector: "status.phase=Running"})
	if err != nil {
		return err
	}
	for _, pod := range pods.Items {
		if pod.DeletionTimestamp != nil {
			// This pod is terminating, so let's help it
			// along. Otherwise it can take 3+ minutes before our new
			// router pods start
			gracePeriodSeconds := int64(0)
			deleteOptions := &metav1.DeleteOptions{
				GracePeriodSeconds: &gracePeriodSeconds,
			}
			err := k8sClient.CoreV1().Pods(pod.Namespace).Delete(pod.Name, deleteOptions)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *ReconcileCrcCluster) waitForOpenShiftAPIServer(k8sClient *kubernetes.Clientset) (bool, error) {
	apiServerNs := "openshift-apiserver"
	pods, err := k8sClient.CoreV1().Pods(apiServerNs).List(metav1.ListOptions{})
	if err != nil {
		return false, err
	}
	if len(pods.Items) < 1 {
		return false, fmt.Errorf("Expected at least one OpenShift API server pod, found %d", pods.Items)
	}
	for _, pod := range pods.Items {
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodReady {
				if condition.Status == corev1.ConditionTrue {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

func (r *ReconcileCrcCluster) updateAPIServerURL(crc *crcv1alpha1.CrcCluster, restConfig *rest.Config) error {
	configClient, err := configv1Client.NewForConfig(restConfig)
	if err != nil {
		return err
	}
	infra, err := configClient.Infrastructures().Get("cluster", metav1.GetOptions{})
	if err != nil {
		return err
	}
	// APIServerURL always needs a port until
	// https://github.com/openshift/cluster-kube-apiserver-operator/pull/855
	// get merged
	desiredAPIServerURL := fmt.Sprintf("%s:443", crc.Status.APIURL)
	if infra.Status.APIServerURL != desiredAPIServerURL {
		infra.Status.APIServerURL = desiredAPIServerURL
		_, err := configClient.Infrastructures().UpdateStatus(infra)
		if err != nil && errors.IsNotFound(err) {
			// OCP versions older than 4.5 didn't use the status
			// subresource for Infrastructure
			_, err = configClient.Infrastructures().Update(infra)
			if err != nil {
				return err
			}
		} else {
			return err
		}
	}
	return nil
}

func (r *ReconcileCrcCluster) waitForClusterToStabilize(k8sClient *kubernetes.Clientset) ([]corev1.Pod, error) {
	pods, err := k8sClient.CoreV1().Pods("").List(metav1.ListOptions{FieldSelector: "status.phase!=Succeeded"})
	notReadyPods := []corev1.Pod{}
	if err != nil {
		return notReadyPods, err
	}
	for _, pod := range pods.Items {
		openshiftNamespacesRegex := regexp.MustCompile(`^openshift-.*|kube-.*$`)
		ignoredMarketplacePodsRegex := regexp.MustCompile(`^community-operators-.*|certified-operators-.*$`)
		// Ignore all non-OpenShift pods
		if !openshiftNamespacesRegex.MatchString(pod.Namespace) {
			continue
		}
		// Ignore some of the marketplace operator pods
		if pod.Namespace == "openshift-marketplace" && ignoredMarketplacePodsRegex.MatchString(pod.Name) {
			continue
		}
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodReady {
				if condition.Status != corev1.ConditionTrue {
					notReadyPods = append(notReadyPods, pod)
				}
			}
		}
	}
	return notReadyPods, nil
}

func (r *ReconcileCrcCluster) updateClusterAdminCert(crc *crcv1alpha1.CrcCluster, k8sClient *kubernetes.Clientset) (*crcv1alpha1.CrcCluster, error) {
	if crc.Status.Kubeconfig != "" {
		return crc, nil
	}

	csr := &certificatesv1beta1.CertificateSigningRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name: "crc-cluster-admin",
		},
		Spec: certificatesv1beta1.CertificateSigningRequestSpec{
			Groups: []string{"cluster-admin"},
			Usages: []certificatesv1beta1.KeyUsage{certificatesv1beta1.UsageClientAuth},
		},
	}
	existingCsr, err := k8sClient.CertificatesV1beta1().CertificateSigningRequests().Get(csr.Name, metav1.GetOptions{})
	if err != nil && errors.IsNotFound(err) {
		cmd := exec.Command("openssl", "req", "-subj", "/CN=kubeadmin", "-new", "-key", "-", "-nodes")
		clientKey, err := base64.StdEncoding.DecodeString(crc.Status.KubeAdminClientKey)
		if err != nil {
			return crc, err
		}
		cmd.Stdin = bytes.NewReader(clientKey)
		csrBytes, err := cmd.Output()
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				return crc, fmt.Errorf("Error from openssl: %s", ee.Stderr)
			}
			return crc, err
		}
		csr.Spec.Request = csrBytes
		existingCsr, err = k8sClient.CertificatesV1beta1().CertificateSigningRequests().Create(csr)
		if err != nil {
			return crc, err
		}
	} else if err != nil {
		return crc, err
	}

	if err := r.approveCSR(existingCsr, k8sClient); err != nil {
		return crc, err
	}

	approvedCsr, err := k8sClient.CertificatesV1beta1().CertificateSigningRequests().Get(csr.Name, metav1.GetOptions{})
	if err != nil {
		return crc, err
	}
	certBytes := approvedCsr.Status.Certificate
	if len(certBytes) == 0 {
		return crc, fmt.Errorf("Expected the approved CSR to have a status.certificate value")
	}
	clientCert := base64.StdEncoding.EncodeToString(certBytes)

	// TODO: Disable insecure-skip-tls-verify and get the proper
	// certificate-authority-data from the cluster
	crc.Status.Kubeconfig = base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf(`apiVersion: v1
clusters:
- cluster:
    insecure-skip-tls-verify: true
    server: %s
  name: crc
contexts:
- context:
    cluster: crc
    user: kubeadmin
  name: kubeadmin
current-context: kubeadmin
kind: Config
preferences: {}
users:
- name: kubeadmin
  user:
    client-certificate-data: %s
    client-key-data: %s
`, crc.Status.APIURL, clientCert, crc.Status.KubeAdminClientKey)))

	crc, err = r.updateCrcClusterStatus(crc)
	if err != nil {
		return crc, err
	}

	return crc, nil
}

func (r *ReconcileCrcCluster) removeSharedKubeadminSecret(k8sClient *kubernetes.Clientset) error {
	err := k8sClient.CoreV1().Secrets("kube-system").Delete("kubeadmin", &metav1.DeleteOptions{})
	if err != nil && errors.IsNotFound(err) {
		return nil
	}
	return err
}

func (r *ReconcileCrcCluster) approveCSRs(k8sClient *kubernetes.Clientset) error {
	csrs, err := k8sClient.CertificatesV1beta1().CertificateSigningRequests().List(metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, csr := range csrs.Items {
		if err := r.approveCSR(&csr, k8sClient); err != nil {
			return err
		}
	}
	return nil
}

func (r *ReconcileCrcCluster) approveCSR(csr *certificatesv1beta1.CertificateSigningRequest, k8sClient *kubernetes.Clientset) error {
	var alreadyApproved bool
	for _, condition := range csr.Status.Conditions {
		if condition.Type == certificatesv1beta1.CertificateApproved {
			alreadyApproved = true
		}
	}
	if !alreadyApproved {
		csr.Status.Conditions = append(csr.Status.Conditions, certificatesv1beta1.CertificateSigningRequestCondition{
			Type:           certificatesv1beta1.CertificateApproved,
			Reason:         "CRCApprove",
			Message:        "This CSR was approved by CodeReady Containers operator.",
			LastUpdateTime: metav1.Now(),
		})
		_, err := k8sClient.CertificatesV1beta1().CertificateSigningRequests().UpdateApproval(csr)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *ReconcileCrcCluster) updateIngressDomain(crc *crcv1alpha1.CrcCluster, restConfig *rest.Config) error {
	configClient, err := configv1Client.NewForConfig(restConfig)
	if err != nil {
		return err
	}
	ingress, err := configClient.Ingresses().Get("cluster", metav1.GetOptions{})
	if err != nil {
		return err
	}
	if ingress.Spec.Domain != crc.Status.BaseDomain {
		ingress.Spec.Domain = crc.Status.BaseDomain
		if _, err := configClient.Ingresses().Update(ingress); err != nil {
			return err
		}
	}
	return nil
}

func (r *ReconcileCrcCluster) updateDefaultRoutes(crc *crcv1alpha1.CrcCluster, restConfig *rest.Config) (bool, error) {
	updatedRoutes := false
	routeClient, err := routev1Client.NewForConfig(restConfig)
	if err != nil {
		return updatedRoutes, err
	}
	defaultRouteNamespaces := []string{
		"openshift-console",
		"openshift-image-registry",
		"openshift-monitoring",
	}
	for _, routeNs := range defaultRouteNamespaces {
		routes, err := routeClient.Routes(routeNs).List(metav1.ListOptions{})
		if err != nil {
			return updatedRoutes, err
		}
		for _, route := range routes.Items {
			expectedRouteHost := routeHostForDomain(crc.Status.BaseDomain, route.Name, route.Namespace)
			if route.Spec.Host != expectedRouteHost {
				route.Spec.Host = expectedRouteHost
				if _, err := routeClient.Routes(route.Namespace).Update(&route); err != nil {
					return updatedRoutes, err
				}
				updatedRoutes = true
			}
		}
	}
	return updatedRoutes, nil
}

func (r *ReconcileCrcCluster) ensureIngressControllersUpdated(crc *crcv1alpha1.CrcCluster, restConfig *rest.Config) error {
	operatorClient, err := operatorv1Client.NewForConfig(restConfig)
	if err != nil {
		return err
	}
	needsNewIngress := false
	ingressControllerNs := "openshift-ingress-operator"
	ingressControllerName := "default"
	numReplicas := int32(1)
	ingressController, err := operatorClient.IngressControllers(ingressControllerNs).Get(ingressControllerName, metav1.GetOptions{})
	if err != nil && errors.IsNotFound(err) {
		needsNewIngress = true
	} else if err != nil {
		return err
	} else {
		if ingressController.Status.Domain != crc.Status.BaseDomain {
			if err := operatorClient.IngressControllers(ingressControllerNs).Delete(ingressControllerName, &metav1.DeleteOptions{}); err != nil {
				return err
			}
			needsNewIngress = true
		} else if ingressController.Spec.Replicas == nil || *ingressController.Spec.Replicas != numReplicas {
			ingressController.Spec.Replicas = &numReplicas
			if _, err := operatorClient.IngressControllers(ingressControllerNs).Update(ingressController); err != nil {
				return err
			}
		}
	}
	if needsNewIngress {
		newIngress := &operatorv1.IngressController{
			ObjectMeta: metav1.ObjectMeta{
				Name: ingressControllerName,
			},
			Spec: operatorv1.IngressControllerSpec{
				Replicas: &numReplicas,
				Domain:   crc.Status.BaseDomain,
			},
		}
		if _, err := operatorClient.IngressControllers(ingressControllerNs).Create(newIngress); err != nil {
			// If we get an already exists here then it means the
			// ingress operator already recreated this for us
			// immediately after the delete above so just ignore
			// it. We only create here to speed things up.
			if !errors.IsAlreadyExists(err) {
				return err
			}
		}
	}
	return nil
}

func (r *ReconcileCrcCluster) ensureKubeletStarted(logger logr.Logger, sshClient sshClient.Client, crc *crcv1alpha1.CrcCluster, bundle *crcv1alpha1.CrcBundle) (*crcv1alpha1.CrcCluster, error) {
	output, err := sshClient.Output(`sudo systemctl status kubelet; if [ $? == 0 ]; then echo "__kubelet_running: true"; else echo "__kubelet_running: false"; fi`)
	if err != nil {
		logger.Error(err, "Error checking kubelet status in VirtualMachine.")
		return crc, err
	}
	fmt.Printf("Kubelet status: %s\n", output)

	kubeletRunning := strings.Contains(output, "__kubelet_running: true")

	if !kubeletRunning {
		logger.Info("Starting kubelet in the VirtualMachine.")
		nameserverScript := []byte(`cat /etc/resolv.conf | grep nameserver`)
		err := ioutil.WriteFile("/tmp/nameserver", nameserverScript, 0644)
		if err != nil {
			logger.Error(err, "Error writing nameserver script.")
			return crc, err
		}
		nameserver, err := exec.Command("sh", "/tmp/nameserver").Output()
		if err != nil {
			logger.Error(err, "Error finding nameserver of operator pod.")
			return crc, err
		}
		fmt.Printf("Nameserver is '%s'\n", nameserver)

		insecureReadyzHack := ""
		if bundle.Name == "ocp450rc1" {
			insecureReadyzHack = `
echo "SHOULD_LOOP=true
cleanup() {
  echo -e 'Stopping monitoring kube-apiserver pod starts'
  SHOULD_LOOP=false
}
trap 'cleanup' EXIT

echo 'Monitoring kube-apiserver pod starts'
while \$SHOULD_LOOP; do
  sleep 60

  APISERVER_POD=\$(crictl pods --namespace openshift-kube-apiserver --name 'kube-apiserver-crc-*' --latest --quiet)
  if [ -z \"\$APISERVER_POD\" ]; then
    continue
  fi

  APISERVER_CONTAINER=\$(crictl ps -p \$APISERVER_POD --name 'kube-apiserver$' --all --latest --quiet)
  crictl logs --tail 5 \$APISERVER_CONTAINER 2>&1 | grep '\.\.\.\.\.'
  if [ \$? -gt 0 ]; then
    continue
  fi

  # Kill the readyz container so the kube-apiserver can start
  START=\$(date +%s)
  until [ \$(expr \$(date +%s) - \$START) -gt 10 ]; do
    PID=\$(ps x | grep 'cluster-kube-apiserver-operator insecure-readyz' | grep -v grep | awk '{print \$1}')
    if [ \"\$PID\" != \"\" ]; then
      echo \"KILLING PID \$PID\"
      kill \$PID
    fi
  done
done
" | sudo tee /tmp/monitorKubeApiServer.sh
sudo chmod 0755 /tmp/monitorKubeApiServer.sh
echo 'Starting monitorKubeApiServer.sh'
setsid sudo /tmp/monitorKubeApiServer.sh 1>/dev/null 2>/dev/null &
echo 'Started monitorKubeApiServer.sh'
`
		}

		startKubeletScript := fmt.Sprintf(`
set -e
echo "> Growing root filesystem."
sudo xfs_growfs /

echo "> Setting up DNS and starting kubelet."
echo ">> Setting up dnsmasq.conf"
echo "user=root
port= 53
bind-interfaces
expand-hosts
log-queries
srv-host=_etcd-server-ssl._tcp.crc.testing,etcd-0.crc.testing,2380,10
local=/crc.testing/
domain=crc.testing
address=/apps-crc.testing/10.0.2.2
address=/%[1]s/10.0.2.2
address=/etcd-0.crc.testing/10.0.2.2
address=/api.crc.testing/10.0.2.2
address=/api-int.crc.testing/10.0.2.2
address=/$(hostname).crc.testing/192.168.126.11" | sudo tee /var/srv/dnsmasq.conf

sudo cat /var/srv/dnsmasq.conf

echo ">> Starting dnsmasq container."
sudo podman rm -f dnsmasq 2>/dev/null || true
sudo rm -f /var/lib/cni/networks/podman/10.88.0.8
sudo podman run  --ip 10.88.0.8 --name dnsmasq -v /var/srv/dnsmasq.conf:/etc/dnsmasq.conf -p 53:53/udp --privileged -d quay.io/crcont/dnsmasq:latest

echo ">> Updating resolv.conf."
echo "# Generated by CRC
search crc.testing
nameserver 10.88.0.8
%[2]s" | sudo tee /etc/resolv.conf

echo ">> Verifying DNS setup."

LOOPS=0
until [ $LOOPS -eq 5 ] || host -R 3 foo.apps-crc.testing; do
  sleep 1
  LOOPS=$((LOOPS + 1))
done
[ $LOOPS -lt 5 ]

LOOPS=0
until [ $LOOPS -eq 5 ] || host -R 3 quay.io; do
  sleep 1
  LOOPS=$((LOOPS + 1))
done
[ $LOOPS -lt 5 ]

%[3]s


echo ">> Starting Kubelet."
sudo systemctl start kubelet
if [ $? == 0 ]; then
  echo "__kubelet_running: true"
fi
`, crc.Status.BaseDomain, nameserver, insecureReadyzHack)

		output, err := sshClient.Output(startKubeletScript)
		if err != nil {
			logger.Error(err, "Error checking kubelet status in VirtualMachine.")
			fmt.Println(output)
			return crc, err
		}
		kubeletRunning = strings.Contains(output, "__kubelet_running: true")
	}

	if !kubeletRunning {
		return crc, fmt.Errorf("Kubelet not yet running")
	}

	crc.SetConditionBool(crcv1alpha1.ConditionTypeKubeletNotReady, false)
	crc, err = r.updateCrcClusterStatus(crc)
	if err != nil {
		return crc, err
	}

	return crc, nil
}

func (r *ReconcileCrcCluster) initializeStatusConditions(logger logr.Logger, crc *crcv1alpha1.CrcCluster) (*crcv1alpha1.CrcCluster, error) {
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
			Type:   crcv1alpha1.ConditionTypeKubeletNotReady,
			Status: corev1.ConditionTrue,
		},
		status.Condition{
			Type:   crcv1alpha1.ConditionTypeClusterNotConfigured,
			Status: corev1.ConditionTrue,
		},
		status.Condition{
			Type:   crcv1alpha1.ConditionTypeReady,
			Status: corev1.ConditionFalse,
		},
	)

	crc, err := r.updateCrcClusterStatus(crc)
	if err != nil {
		logger.Error(err, "Failed to initialize CrcCluster status.")
		return crc, err
	}

	return crc, nil
}

func (r *ReconcileCrcCluster) ensureVirtualMachineExists(logger logr.Logger, crc *crcv1alpha1.CrcCluster, bundle *crcv1alpha1.CrcBundle) (*kubevirtv1.VirtualMachine, error) {
	virtualMachine, err := r.newVirtualMachineForCrcCluster(crc, bundle)
	if err != nil {
		logger.Error(err, "Failed to create VirtualMachine.", "VirtualMachine.Namespace", virtualMachine.Namespace, "VirtualMachine.Name", virtualMachine.Name)
		return nil, err
	}

	// Check if the VirtualMachine already exists. If it doesn't,
	// create a new one.
	existingVirtualMachine := &kubevirtv1.VirtualMachine{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: virtualMachine.Name, Namespace: virtualMachine.Namespace}, existingVirtualMachine)
	if err != nil && errors.IsNotFound(err) {
		logger.Info("Creating a new VirtualMachine.", "VirtualMachine.Namespace", virtualMachine.Namespace, "VirtualMachine.Name", virtualMachine.Name)
		err = r.client.Create(context.TODO(), virtualMachine)
		if err != nil {
			logger.Error(err, "Failed to create VirtualMachine.", "VirtualMachine.Namespace", virtualMachine.Namespace, "VirtualMachine.Name", virtualMachine.Name)
			return nil, err
		}

		// Get the VirtualMachine again
		existingVirtualMachine = &kubevirtv1.VirtualMachine{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: virtualMachine.Name, Namespace: virtualMachine.Namespace}, existingVirtualMachine)
		if err != nil {
			logger.Error(err, "Failed to get VirtualMachine.")
			return nil, err
		}
	} else if err != nil {
		logger.Error(err, "Failed to get VirtualMachine.")
		return nil, err
	}
	if !reflect.DeepEqual(virtualMachine.Spec, existingVirtualMachine.Spec) {
		existingVirtualMachine.Spec = virtualMachine.Spec
		err := r.client.Update(context.TODO(), existingVirtualMachine)
		if err != nil {
			return nil, err
		}
	}
	virtualMachine = existingVirtualMachine.DeepCopy()
	return virtualMachine, nil
}

func (r *ReconcileCrcCluster) ensureServiceExists(logger logr.Logger, crc *crcv1alpha1.CrcCluster) (*corev1.Service, error) {
	k8sSvc, err := r.newServiceForCrcCluster(crc)
	if err != nil {
		logger.Error(err, "Failed to create Kubernetes Service.", "Service.Namespace", k8sSvc.Namespace, "Service.Name", k8sSvc.Name)
		return nil, err
	}

	// Check if the Kubernetes Service already exists. If it doesn't,
	// create a new one.
	existingK8sSvc := &corev1.Service{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: k8sSvc.Name, Namespace: k8sSvc.Namespace}, existingK8sSvc)
	if err != nil && errors.IsNotFound(err) {
		logger.Info("Creating a new Kubernetes Service.", "Service.Namespace", k8sSvc.Namespace, "Service.Name", k8sSvc.Name)
		err = r.client.Create(context.TODO(), k8sSvc)
		if err != nil {
			logger.Error(err, "Failed to create Kubernetes Service.", "Service.Namespace", k8sSvc.Namespace, "Service.Name", k8sSvc.Name)
			return nil, err
		}

		// Get the Kubernetes Service again
		existingK8sSvc := &corev1.Service{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: k8sSvc.Name, Namespace: k8sSvc.Namespace}, existingK8sSvc)
		if err != nil {
			logger.Error(err, "Failed to get Kubernetes Service.")
			return nil, err
		}
	} else if err != nil {
		logger.Error(err, "Failed to get Kubernetes Service.")
		return nil, err
	}
	k8sSvc = existingK8sSvc.DeepCopy()
	return k8sSvc, nil
}

func (r *ReconcileCrcCluster) ensureAPIRouteExists(logger logr.Logger, crc *crcv1alpha1.CrcCluster) (*routev1.Route, error) {
	route, err := r.newAPIRouteForCrcCluster(crc)
	if err != nil {
		logger.Error(err, "Failed to create OpenShift Route.", "Route.Namespace", route.Namespace, "Route.Name", route.Name)
		return nil, err
	}

	// Check if the API Server Route already exists. If it doesn't,
	// create a new one.
	existingRoute := &routev1.Route{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: route.Name, Namespace: route.Namespace}, existingRoute)
	if err != nil && errors.IsNotFound(err) {
		logger.Info("Creating a new OpenShift Route", "Route.Namespace", route.Namespace, "Route.Name", route.Name)
		err = r.client.Create(context.TODO(), route)
		if err != nil {
			logger.Error(err, "Failed to create OpenShift Route.", "Route.Namespace", route.Namespace, "Route.Name", route.Name)
			return nil, err
		}

		// Get the OpenShift Route again
		existingRoute = &routev1.Route{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: route.Name, Namespace: route.Namespace}, existingRoute)
		if err != nil {
			logger.Error(err, "Failed to get OpenShift Route.")
			return nil, err
		}
	} else if err != nil {
		logger.Error(err, "Failed to get OpenShift Route.")
		return nil, err
	}
	route = existingRoute.DeepCopy()
	return route, nil
}

func (r *ReconcileCrcCluster) ensureAPIIngressExists(logger logr.Logger, crc *crcv1alpha1.CrcCluster) (*networkingv1beta1.Ingress, error) {
	ingress, err := r.newAPIIngressForCrcCluster(crc)
	if err != nil {
		logger.Error(err, "Failed to create Kubernetes Ingress.", "Ingress.Namespace", ingress.Namespace, "Ingress.Name", ingress.Name)
		return nil, err
	}

	// Check if the Kubernetes Ingress already exists. If it doesn't,
	// create a new one.
	existingIngress := &networkingv1beta1.Ingress{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: ingress.Name, Namespace: ingress.Namespace}, existingIngress)
	if err != nil && errors.IsNotFound(err) {
		logger.Info("Creating a new Kubernetes Ingress.", "Ingress.Namespace", ingress.Namespace, "Ingress.Name", ingress.Name)
		err = r.client.Create(context.TODO(), ingress)
		if err != nil {
			logger.Error(err, "Failed to create Kubernetes Ingress.", "Ingress.Namespace", ingress.Namespace, "Ingress.Name", ingress.Name)
			return nil, err
		}

		// Get the Kubernetes Ingress again
		existingIngress = &networkingv1beta1.Ingress{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: ingress.Name, Namespace: ingress.Namespace}, existingIngress)
		if err != nil {
			logger.Error(err, "Failed to get Kubernetes Ingress.")
			return nil, err
		}
	} else if err != nil {
		logger.Error(err, "Failed to get Kubernetes Ingress.")
		return nil, err
	}
	ingress = existingIngress.DeepCopy()
	return ingress, nil
}

func (r *ReconcileCrcCluster) updateVirtualMachineNotReadyCondition(vm *kubevirtv1.VirtualMachine, crc *crcv1alpha1.CrcCluster) {
	vmReady := vm.Spec.Running != nil && *vm.Spec.Running && vm.Status.Ready
	crc.SetConditionBool(crcv1alpha1.ConditionTypeVirtualMachineNotReady, !vmReady)
	if !vmReady {
		// If the VM is no longer ready then we need to reconfigure
		// everything when it comes back up
		//
		// TODO: If we pivot to VMs with persistent disk, this may
		// need to change
		crc.SetConditionBool(crcv1alpha1.ConditionTypeKubeletNotReady, true)
		crc.SetConditionBool(crcv1alpha1.ConditionTypeClusterNotConfigured, true)
		crc.SetConditionBool(crcv1alpha1.ConditionTypeReady, false)
	}
}

func (r *ReconcileCrcCluster) updateNetworkingNotReadyCondition(svc *corev1.Service, crc *crcv1alpha1.CrcCluster) {
	if svc.Spec.ClusterIP != "" && crc.Status.APIURL != "" {
		crc.SetConditionBool(crcv1alpha1.ConditionTypeNetworkingNotReady, false)
	} else {
		crc.SetConditionBool(crcv1alpha1.ConditionTypeNetworkingNotReady, true)
	}
}

func (r *ReconcileCrcCluster) updateCredentials(crc *crcv1alpha1.CrcCluster) error {
	if crc.Status.KubeAdminPassword == "" {
		kubeAdminPassword, err := generateKubeUserPassword()
		if err != nil {
			return err
		}
		crc.Status.KubeAdminPassword = kubeAdminPassword
	}

	if crc.Status.KubeAdminClientKey == "" {
		key, err := exec.Command("openssl", "genrsa", "4096").Output()
		if err != nil {
			return err
		}
		crc.Status.KubeAdminClientKey = base64.StdEncoding.EncodeToString(key)
	}

	return nil
}

func (r *ReconcileCrcCluster) bundleForCrc(crc *crcv1alpha1.CrcCluster) (*crcv1alpha1.CrcBundle, error) {
	bundleName := crc.Spec.BundleName
	if bundleName == "" {
		bundleName = defaultBundleName
	}
	bundleImage := crc.Spec.BundleImage

	// First, see if a BundleImage was given and exactly matches one
	// of the predefined bundle images
	if bundleImage != "" {
		bundle, err := r.bundleFromImage(bundleImage)
		if err == nil {
			return bundle, nil
		}
	}
	// Now, attempt to find the bundle by name
	bundle, err := r.bundleFromName(bundleName)
	if err != nil {
		return nil, err
	}
	if bundleImage != "" {
		bundle.Spec.Image = bundleImage
	}
	return bundle, nil
}

func (r *ReconcileCrcCluster) bundleFromImage(image string) (*crcv1alpha1.CrcBundle, error) {
	bundleList := &crcv1alpha1.CrcBundleList{}
	err := r.client.List(context.TODO(), bundleList, &client.ListOptions{Namespace: bundleNs})
	if err != nil {
		return nil, err
	}
	for _, bundle := range bundleList.Items {
		if image == bundle.Spec.Image {
			copiedBundle := bundle
			return &copiedBundle, nil
		}
	}
	return nil, fmt.Errorf("No known bundle matches image %s", image)
}

func (r *ReconcileCrcCluster) bundleFromName(name string) (*crcv1alpha1.CrcBundle, error) {
	bundleList := &crcv1alpha1.CrcBundleList{}
	err := r.client.List(context.TODO(), bundleList, &client.ListOptions{Namespace: bundleNs})
	if err != nil {
		return nil, err
	}
	for _, bundle := range bundleList.Items {
		if name == bundle.Name {
			copiedBundle := bundle
			return &copiedBundle, nil
		}
	}
	return nil, fmt.Errorf("No known bundle matches name %s", name)
}

func (r *ReconcileCrcCluster) newVirtualMachineForCrcCluster(crc *crcv1alpha1.CrcCluster, bundle *crcv1alpha1.CrcBundle) (*kubevirtv1.VirtualMachine, error) {
	labels := map[string]string{
		"crcCluster":          crc.Name,
		"kubevirt.io/domain":  crc.Name,
		"vm.kubevirt.io/name": crc.Name,
	}

	vm := &kubevirtv1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      crc.Name,
			Namespace: crc.Namespace,
			Labels:    labels,
		},
	}

	podNetwork := kubevirtv1.PodNetwork{}
	vmRunning := !crc.Spec.Stopped
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
				Resources: kubevirtv1.ResourceRequirements{
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
				},
			},
		},
	}

	vm.Spec = kubevirtv1.VirtualMachineSpec{
		Running:  &vmRunning,
		Template: &vmTemplate,
	}

	vmCPU := kubevirtv1.CPU{
		Sockets: uint32(crc.Spec.CPU),
		Cores:   1,
		Threads: 1,
	}
	vm.Spec.Template.Spec.Domain.CPU = &vmCPU

	guestMemory, err := resource.ParseQuantity(crc.Spec.Memory)
	if err != nil {
		return vm, err
	}
	vmMemory := kubevirtv1.Memory{
		Guest: &guestMemory,
	}
	vm.Spec.Template.Spec.Domain.Memory = &vmMemory

	vmRequestCPU := crc.Spec.CPU / 2
	if vmRequestCPU < 2 {
		vmRequestCPU = 2
	}
	vmResources := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse(fmt.Sprintf("%d", vmRequestCPU)),
		corev1.ResourceMemory: guestMemory,
	}
	vm.Spec.Template.Spec.Domain.Resources.Requests = vmResources

	storageSpec := crc.Spec.Storage
	if storageSpec.Persistent {
		// Persistent, so use a DataVolume to import the container
		// image into a new PVC.
		dataVolumeName := fmt.Sprintf("%s-datavolume", crc.Name)

		bundleQuantity, err := resource.ParseQuantity(bundle.Spec.DiskSize)
		if err != nil {
			return vm, err
		}
		var storageQuantity resource.Quantity
		if crc.Spec.Storage.Size != "" {
			storageQuantity, err = resource.ParseQuantity(crc.Spec.Storage.Size)
			if err != nil {
				return vm, err
			}
			if storageQuantity.Cmp(bundleQuantity) < 0 {
				return vm, fmt.Errorf("Requested storage size %s is less than the minimum disk size of %s needed by bundle %s", crc.Spec.Storage.Size, bundle.Spec.DiskSize, bundle.Name)
			}
		} else {
			storageQuantity = bundleQuantity
		}

		dataVolumeTemplate := cdiv1.DataVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name: dataVolumeName,
			},
			Spec: cdiv1.DataVolumeSpec{
				Source: cdiv1.DataVolumeSource{
					Registry: &cdiv1.DataVolumeSourceRegistry{
						URL: fmt.Sprintf("docker://%s", bundle.Spec.Image),
					},
				},
				PVC: &corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: storageQuantity,
						},
					},
				},
			},
		}
		vm.Spec.DataVolumeTemplates = []cdiv1.DataVolume{dataVolumeTemplate}

		vm.Spec.Template.Spec.Volumes[0].VolumeSource = kubevirtv1.VolumeSource{
			DataVolume: &kubevirtv1.DataVolumeSource{
				Name: dataVolumeName,
			},
		}
	} else {
		// Not persisent, so use the bundle's container image directly
		vm.Spec.Template.Spec.Volumes[0].VolumeSource = kubevirtv1.VolumeSource{
			ContainerDisk: &kubevirtv1.ContainerDiskSource{
				Image: bundle.Spec.Image,
			},
		}
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
					Port:       int32(sshPort),
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
			Selector: map[string]string{"vm.kubevirt.io/name": crc.Name},
			Type:     corev1.ServiceTypeClusterIP,
		},
	}

	if err := controllerutil.SetControllerReference(crc, svc, r.scheme); err != nil {
		return svc, err
	}

	return svc, nil
}

func (r *ReconcileCrcCluster) newAPIRouteForCrcCluster(crc *crcv1alpha1.CrcCluster) (*routev1.Route, error) {
	labels := map[string]string{
		"crcCluster": crc.Name,
	}

	port := routev1.RoutePort{
		TargetPort: intstr.FromInt(6443),
	}
	tls := routev1.TLSConfig{
		Termination: routev1.TLSTerminationPassthrough,
	}
	route := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-api", crc.Name),
			Namespace: crc.Namespace,
			Labels:    labels,
		},
		Spec: routev1.RouteSpec{
			Port: &port,
			To: routev1.RouteTargetReference{
				Kind: "Service",
				Name: crc.Name,
			},
			TLS: &tls,
		},
	}

	clusterIngress := &configv1.Ingress{}
	if err := r.client.Get(context.TODO(), types.NamespacedName{Name: "cluster", Namespace: ""}, clusterIngress); err != nil {
		return route, err
	}

	routeDomain := clusterIngress.Spec.Domain
	route.Spec.Host = fmt.Sprintf("api.%s-%s.%s", crc.Name, crc.Namespace, routeDomain)

	if err := controllerutil.SetControllerReference(crc, route, r.scheme); err != nil {
		return route, err
	}
	return route, nil
}

func (r *ReconcileCrcCluster) newAPIIngressForCrcCluster(crc *crcv1alpha1.CrcCluster) (*networkingv1beta1.Ingress, error) {
	labels := map[string]string{
		"crcCluster": crc.Name,
	}

	annotations := map[string]string{
		"kubernetes.io/ingress.allow-http":             "false",
		"nginx.ingress.kubernetes.io/ssl-passthrough":  "true",
		"nginx.ingress.kubernetes.io/backend-protocol": "HTTPS",
	}

	httpIngress := networkingv1beta1.HTTPIngressRuleValue{
		Paths: []networkingv1beta1.HTTPIngressPath{
			{
				Path: "/",
				Backend: networkingv1beta1.IngressBackend{
					ServiceName: crc.Name,
					ServicePort: intstr.FromInt(6443),
				},
			},
		},
	}

	ingress := &networkingv1beta1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        fmt.Sprintf("%s-api", crc.Name),
			Namespace:   crc.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: networkingv1beta1.IngressSpec{
			Rules: []networkingv1beta1.IngressRule{
				{
					IngressRuleValue: networkingv1beta1.IngressRuleValue{
						HTTP: &httpIngress,
					},
				},
			},
		},
	}

	// Get the ingress-nginx load balancer ip/host
	ingressNginxSvc := &corev1.Service{}
	if err := r.client.Get(context.TODO(), types.NamespacedName{Name: "nginx-ingress-ingress-nginx-controller", Namespace: "ingress-nginx"}, ingressNginxSvc); err != nil {
		return ingress, fmt.Errorf("Failed to get ingress-nginx service - is ingress-nginx installed? Error: %v", err)
	}

	noIngressIPHostError := fmt.Errorf("ingress-nginx load balancer does not have an ingress ip/host yet: %v", ingressNginxSvc.Status)
	if len(ingressNginxSvc.Status.LoadBalancer.Ingress) < 1 {
		return ingress, noIngressIPHostError
	}
	lbIngress := ingressNginxSvc.Status.LoadBalancer.Ingress[0]
	lbHost := lbIngress.Hostname
	if lbHost == "" {
		if lbIngress.IP == "" {
			return ingress, noIngressIPHostError
		}
		lbHost = fmt.Sprintf("%s.nip.io", lbIngress.IP)
	}

	ingress.Spec.Rules[0].Host = fmt.Sprintf("api.%s-%s.%s", crc.Name, crc.Namespace, lbHost)

	if err := controllerutil.SetControllerReference(crc, ingress, r.scheme); err != nil {
		return ingress, err
	}

	return ingress, nil
}

func createSSHClient(k8sService *corev1.Service, bundle *crcv1alpha1.CrcBundle) (sshClient.Client, error) {
	bundleSSHKey, err := base64.StdEncoding.DecodeString(bundle.Spec.SSHKey)
	if err != nil {
		return nil, err
	}
	privateKey, err := ssh.ParsePrivateKey(bundleSSHKey)
	if err != nil {
		return nil, fmt.Errorf("Failed to parse private key: %v", err)
	}
	sshConfig := ssh.ClientConfig{
		User:            "core",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(privateKey)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         1 * time.Second,
	}
	libMachineLog.SetDebug(true)
	sshClient := &sshClient.NativeClient{
		Config:   sshConfig,
		Hostname: fmt.Sprintf("%s.%s", k8sService.Name, k8sService.Namespace),
		Port:     sshPort,
	}
	return sshClient, nil
}

func routeHostForDomain(baseDomain, routeName, routeNamespace string) string {
	return fmt.Sprintf("%s-%s.%s", routeName, routeNamespace, baseDomain)
}

func generateKubeUserPassword() (string, error) {
	return generateRandomPasswordHash(23)
}

func hashPassword(password string) ([]byte, error) {
	return bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
}

//
// Password generation below totally ripped from:
// https://github.com/openshift/installer/blob/92725a407d518d75ce515131bb52ab94df852c3d/pkg/asset/password/password.go
//

// generateRandomPasswordHash generates a hash of a random ASCII password
// 5char-5char-5char-5char
func generateRandomPasswordHash(length int) (string, error) {
	const (
		lowerLetters = "abcdefghijkmnopqrstuvwxyz"
		upperLetters = "ABCDEFGHIJKLMNPQRSTUVWXYZ"
		digits       = "23456789"
		all          = lowerLetters + upperLetters + digits
	)
	var password string
	for i := 0; i < length; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(all))))
		if err != nil {
			return "", err
		}
		newchar := string(all[n.Int64()])
		if password == "" {
			password = newchar
		}
		if i < length-1 {
			n, err = rand.Int(rand.Reader, big.NewInt(int64(len(password)+1)))
			if err != nil {
				return "", err
			}
			j := n.Int64()
			password = password[0:j] + newchar + password[j:]
		}
	}
	pw := []rune(password)
	for _, replace := range []int{5, 11, 17} {
		pw[replace] = '-'
	}
	return string(pw), nil
}
