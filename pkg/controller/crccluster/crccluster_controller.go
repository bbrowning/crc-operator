package crccluster

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"net/http"
	"os/exec"
	"reflect"
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

const (
	sshPort int = 2022
)

// Add creates a new CrcCluster Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
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

	virtualMachine, err := r.ensureVirtualMachineExists(reqLogger, crc)
	if err != nil {
		return reconcile.Result{}, err
	}

	k8sService, err := r.ensureServiceExists(reqLogger, crc)
	if err != nil {
		return reconcile.Result{}, err
	}

	apiHost := ""
	if r.routeAPIExists {
		route, err := r.ensureRouteExists(reqLogger, crc)
		if err != nil {
			return reconcile.Result{}, err
		}
		apiHost = route.Spec.Host
	} else {
		ingress, err := r.ensureIngressExists(reqLogger, crc)
		if err != nil {
			return reconcile.Result{}, err
		}
		apiHost = ingress.Spec.Rules[0].Host
	}
	crc.Status.APIURL = fmt.Sprintf("https://%s", apiHost)
	crc.Status.BaseDomain = strings.Replace(apiHost, "api.", "", 1)
	crc.Status.ConsoleURL = fmt.Sprintf("https://%s", consoleHost(crc.Status.BaseDomain))

	r.updateVirtualMachineNotReadyCondition(virtualMachine, crc)
	r.updateNetworkingNotReadyCondition(k8sService, crc)
	r.updateCredentials(crc)

	crc, err = r.updateCrcClusterStatus(crc)
	if err != nil {
		return reconcile.Result{}, err
	}

	// Don't attempt any further reconciling until the VM is ready
	if crc.Status.Conditions.IsTrueFor(crcv1alpha1.ConditionTypeVirtualMachineNotReady) {
		reqLogger.Info("Waiting on the VirtualMachine to become Ready before continuing")
		return reconcile.Result{}, nil
	}

	sshClient, err := createSSHClient(k8sService)
	if err != nil {
		reqLogger.Error(err, "Failed to create SSH Client.")
		return reconcile.Result{}, err
	}

	if crc.Status.Conditions.IsTrueFor(crcv1alpha1.ConditionTypeKubeletNotReady) {
		crc, err = r.ensureKubeletStarted(reqLogger, sshClient, crc)
		if err != nil {
			reqLogger.Error(err, "Failed to start Kubelet.")
			return reconcile.Result{}, err
		}
	}

	crcK8sConfig, err := restConfigFromCrcCluster(crc)
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

	reqLogger.Info("Updating console route.")
	consoleRouteUpdated, err := r.updateConsoleRoute(crc, insecureCrcK8sConfig)
	if err != nil {
		reqLogger.Error(err, "Error updating console route.")
		return reconcile.Result{}, err
	} else if consoleRouteUpdated {
		return reconcile.Result{RequeueAfter: time.Second * 20}, nil
	}

	reqLogger.Info("Waiting on cluster to stabilize.")
	stable, err = r.waitForClusterToStabilize(insecureK8sClient)
	if err != nil {
		reqLogger.Error(err, "Error waiting on cluster to stabilize.")
		return reconcile.Result{}, err
	}
	if !stable {
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
							Image:           "quay.io/bbrowning/crc-operator-routes-helper:v0.0.1",
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

func restConfigFromCrcCluster(crc *crcv1alpha1.CrcCluster) (*rest.Config, error) {
	kubeconfigBytes, err := base64.StdEncoding.DecodeString(crc.Status.Kubeconfig)
	if err != nil {
		return nil, err
	}
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

func (r *ReconcileCrcCluster) waitForClusterToStabilize(k8sClient *kubernetes.Clientset) (bool, error) {
	pods, err := k8sClient.CoreV1().Pods("").List(metav1.ListOptions{FieldSelector: "status.phase!=Succeeded"})
	if err != nil {
		return false, err
	}
	for _, pod := range pods.Items {
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodReady {
				if condition.Status != corev1.ConditionTrue {
					return false, nil
				}
			}
		}
	}
	return true, nil
}

func (r *ReconcileCrcCluster) approveCSRs(k8sClient *kubernetes.Clientset) error {
	csrs, err := k8sClient.CertificatesV1beta1().CertificateSigningRequests().List(metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, csr := range csrs.Items {
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
			_, err := k8sClient.CertificatesV1beta1().CertificateSigningRequests().UpdateApproval(&csr)
			if err != nil {
				return err
			}
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

func (r *ReconcileCrcCluster) updateConsoleRoute(crc *crcv1alpha1.CrcCluster, restConfig *rest.Config) (bool, error) {
	routeClient, err := routev1Client.NewForConfig(restConfig)
	if err != nil {
		return false, err
	}
	consoleRouteNs := "openshift-console"
	consoleRouteName := "console"
	consoleRouteHost := consoleHost(crc.Status.BaseDomain)
	route, err := routeClient.Routes(consoleRouteNs).Get(consoleRouteName, metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	if route.Spec.Host != consoleRouteHost {
		route.Spec.Host = consoleRouteHost
		if _, err := routeClient.Routes(consoleRouteNs).Update(route); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
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

func (r *ReconcileCrcCluster) ensureKubeletStarted(logger logr.Logger, sshClient sshClient.Client, crc *crcv1alpha1.CrcCluster) (*crcv1alpha1.CrcCluster, error) {
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

		startKubeletScript := fmt.Sprintf(`
set -e
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

echo ">> Starting Kubelet."
sudo systemctl start kubelet
if [ $? == 0 ]; then
  echo "__kubelet_running: true"
fi
`, crc.Status.BaseDomain, nameserver)

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

func (r *ReconcileCrcCluster) ensureVirtualMachineExists(logger logr.Logger, crc *crcv1alpha1.CrcCluster) (*kubevirtv1.VirtualMachine, error) {
	virtualMachine, err := r.newVirtualMachineForCrcCluster(crc)
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

func (r *ReconcileCrcCluster) ensureRouteExists(logger logr.Logger, crc *crcv1alpha1.CrcCluster) (*routev1.Route, error) {
	route, err := r.newRouteForCrcCluster(crc)
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

func (r *ReconcileCrcCluster) ensureIngressExists(logger logr.Logger, crc *crcv1alpha1.CrcCluster) (*networkingv1beta1.Ingress, error) {
	ingress, err := r.newIngressForCrcCluster(crc)
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
	crc.SetConditionBool(crcv1alpha1.ConditionTypeVirtualMachineNotReady, !vm.Status.Ready)
	if !vm.Status.Ready {
		// If the VM is no longer ready then we need to reconfigure
		// everything when it comes back up
		//
		// TODO: If we pivot to VMs with persistent disk, this may
		// need to change
		crc.SetConditionBool(crcv1alpha1.ConditionTypeKubeletNotReady, true)
		crc.SetConditionBool(crcv1alpha1.ConditionTypeClusterNotConfigured, true)
	}
}

func (r *ReconcileCrcCluster) updateNetworkingNotReadyCondition(svc *corev1.Service, crc *crcv1alpha1.CrcCluster) {
	if svc.Spec.ClusterIP != "" && crc.Status.APIURL != "" {
		crc.SetConditionBool(crcv1alpha1.ConditionTypeNetworkingNotReady, false)
	} else {
		crc.SetConditionBool(crcv1alpha1.ConditionTypeNetworkingNotReady, true)
	}
}

func (r *ReconcileCrcCluster) updateCredentials(crc *crcv1alpha1.CrcCluster) {
	if crc.Status.Conditions.IsFalseFor(crcv1alpha1.ConditionTypeVirtualMachineNotReady) &&
		crc.Status.Conditions.IsFalseFor(crcv1alpha1.ConditionTypeNetworkingNotReady) {

		crc.Status.KubeAdminPassword = "DEP6h-PvR7K-7fYqe-IhLUP"

		// TODO: This certificate-authority-data doesn't match the
		// actual cert the user gets when they hit the api server
		crc.Status.Kubeconfig = base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf(`apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSUM3VENDQWRXZ0F3SUJBZ0lCQVRBTkJna3Foa2lHOXcwQkFRc0ZBREFtTVNRd0lnWURWUVFEREJ0cGJtZHkKWlhOekxXOXdaWEpoZEc5eVFERTFPVEV6TlRneE5UTXdIaGNOTWpBd05qQTFNVEUxTlRVeVdoY05Nakl3TmpBMQpNVEUxTlRVeldqQW1NU1F3SWdZRFZRUUREQnRwYm1keVpYTnpMVzl3WlhKaGRHOXlRREUxT1RFek5UZ3hOVE13CmdnRWlNQTBHQ1NxR1NJYjNEUUVCQVFVQUE0SUJEd0F3Z2dFS0FvSUJBUURTOUtJNFhUZHJRblMvTkdGS2thTGcKZStvdmEwSWxHYjNsbE5QVnJnZTBwdlNGNTRUakFUQlpOc2hOekRQN1huVkRYUFZ0VlU4OXNMTHZjZDJDSHFLaApSR1pHdnFCMGJlTmowZ2dnTlNWU3RBc1NCUSt2Smp2TTN2bS91R25nR3FxZGdXcUdPbGV1YUoxUlNTZUZwa2VLCmIvMGttbFZWRStoUHVZbXFjL1ErditiU0w0Um5Fb2pSRGU2QzdtZ2U4M2pGd0xmTjJjR3dpVjFjUG9kZFgrVEYKb2F5Y0xVaEh0SjZnTVN6SkZ1c1Z4Z3RPOFpRdkR1UXRPQ0ZLVUhWS2NDM3JpR096VUE3WkxxMWF3ZzRVRmJJTgoxODl4QkhPRnNlZWE5RjRXckZJWXBEZVF6a3BUeHJ2VnBuZ2wyRkZ3eGNTU1hLL0Y2WFZtY3g1SFNnZEsrY3pCCkFnTUJBQUdqSmpBa01BNEdBMVVkRHdFQi93UUVBd0lDcERBU0JnTlZIUk1CQWY4RUNEQUdBUUgvQWdFQU1BMEcKQ1NxR1NJYjNEUUVCQ3dVQUE0SUJBUUJlMjZmUnFsZFhFdE5mWEdYaVhtYStuaVhnMmRtQ2g2azdXYUNrMkdGNgpHbHhZMDNkcmNYeXpwUzRTT2Rac2VqaVBwVU9ubTgwdnZBai9LaWZmakxDUDIvUDBUT2w3cCtlNTBFbGFaZVIvCjMxRjRDMzdZYW5VbFV3YVVUblFtUXRSd002Szl2QWRiRUZ5SWVHV1AraU04TFFFUnRYRXA4M0tJS1BQbjVPd2YKNjBrUXBLSWRKL2ttR3pwRUllS0FVTmpITTgyM01JU3FZd21yVDN3elBmankxZEpyeUtXNGdLazZTVmJqVUZXTwp6UFpyMVk0Tmd0aG5HSFRvbnhNYWkxRDhZa2cvM3k0TWt3Q3FKWHk0ZlJEdnRpMklMaG5xNWx4RExzOThwaU1BCmRhMVdveWNHSlNWdHYySHkwKzg2amNFelo3T01mRGllSnRRdVpaUzgrdjVKCi0tLS0tRU5EIENFUlRJRklDQVRFLS0tLS0KLS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSUM3VENDQWRXZ0F3SUJBZ0lCQVRBTkJna3Foa2lHOXcwQkFRc0ZBREFtTVNRd0lnWURWUVFEREJ0cGJtZHkKWlhOekxXOXdaWEpoZEc5eVFERTFPVEV6TlRneE5UTXdIaGNOTWpBd05qQTFNVEUxTlRVeVdoY05Nakl3TmpBMQpNVEUxTlRVeldqQW1NU1F3SWdZRFZRUUREQnRwYm1keVpYTnpMVzl3WlhKaGRHOXlRREUxT1RFek5UZ3hOVE13CmdnRWlNQTBHQ1NxR1NJYjNEUUVCQVFVQUE0SUJEd0F3Z2dFS0FvSUJBUURTOUtJNFhUZHJRblMvTkdGS2thTGcKZStvdmEwSWxHYjNsbE5QVnJnZTBwdlNGNTRUakFUQlpOc2hOekRQN1huVkRYUFZ0VlU4OXNMTHZjZDJDSHFLaApSR1pHdnFCMGJlTmowZ2dnTlNWU3RBc1NCUSt2Smp2TTN2bS91R25nR3FxZGdXcUdPbGV1YUoxUlNTZUZwa2VLCmIvMGttbFZWRStoUHVZbXFjL1ErditiU0w0Um5Fb2pSRGU2QzdtZ2U4M2pGd0xmTjJjR3dpVjFjUG9kZFgrVEYKb2F5Y0xVaEh0SjZnTVN6SkZ1c1Z4Z3RPOFpRdkR1UXRPQ0ZLVUhWS2NDM3JpR096VUE3WkxxMWF3ZzRVRmJJTgoxODl4QkhPRnNlZWE5RjRXckZJWXBEZVF6a3BUeHJ2VnBuZ2wyRkZ3eGNTU1hLL0Y2WFZtY3g1SFNnZEsrY3pCCkFnTUJBQUdqSmpBa01BNEdBMVVkRHdFQi93UUVBd0lDcERBU0JnTlZIUk1CQWY4RUNEQUdBUUgvQWdFQU1BMEcKQ1NxR1NJYjNEUUVCQ3dVQUE0SUJBUUJlMjZmUnFsZFhFdE5mWEdYaVhtYStuaVhnMmRtQ2g2azdXYUNrMkdGNgpHbHhZMDNkcmNYeXpwUzRTT2Rac2VqaVBwVU9ubTgwdnZBai9LaWZmakxDUDIvUDBUT2w3cCtlNTBFbGFaZVIvCjMxRjRDMzdZYW5VbFV3YVVUblFtUXRSd002Szl2QWRiRUZ5SWVHV1AraU04TFFFUnRYRXA4M0tJS1BQbjVPd2YKNjBrUXBLSWRKL2ttR3pwRUllS0FVTmpITTgyM01JU3FZd21yVDN3elBmankxZEpyeUtXNGdLazZTVmJqVUZXTwp6UFpyMVk0Tmd0aG5HSFRvbnhNYWkxRDhZa2cvM3k0TWt3Q3FKWHk0ZlJEdnRpMklMaG5xNWx4RExzOThwaU1BCmRhMVdveWNHSlNWdHYySHkwKzg2amNFelo3T01mRGllSnRRdVpaUzgrdjVKCi0tLS0tRU5EIENFUlRJRklDQVRFLS0tLS0KLS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSURRRENDQWlpZ0F3SUJBZ0lJTGlKYklDbE9RaEl3RFFZSktvWklodmNOQVFFTEJRQXdQakVTTUJBR0ExVUUKQ3hNSmIzQmxibk5vYVdaME1TZ3dKZ1lEVlFRREV4OXJkV0psTFdGd2FYTmxjblpsY2kxc2IyTmhiR2h2YzNRdApjMmxuYm1WeU1CNFhEVEl3TURZd05URXhNVGcxTlZvWERUTXdNRFl3TXpFeE1UZzFOVm93UGpFU01CQUdBMVVFCkN4TUpiM0JsYm5Ob2FXWjBNU2d3SmdZRFZRUURFeDlyZFdKbExXRndhWE5sY25abGNpMXNiMk5oYkdodmMzUXQKYzJsbmJtVnlNSUlCSWpBTkJna3Foa2lHOXcwQkFRRUZBQU9DQVE4QU1JSUJDZ0tDQVFFQTdMQXlHY3NiaUc1OApDSzRUUUZQQ3cwVUc3ems0SVhTTDlKV0U1SjlMUHQycW12azdjR2hLTnlGL0NIZElodk0zY2tZM2dLcnhkSlZMCjhLWnJYbUxnRVlXM1hUYzFMWjc5TG5UQmt0RWFVMTFvVU1kMkVFaUh2WFVrSFJKaUNNNzFhOTZQOEFkZUZFQloKNEhnQkR5V3ZGcUFFWWlpSnc4M1hxYUVNdXBGUDJFM0ZTTjEra291Sk9BbE1OaDZHcEdvdVlNMGlMek5SKzVtSQpvRFdKUW92bk9OWlorb0l0MDBEQ1kwZHA5V3FqWEhGSzZuNmg5QXNldG4yS0dmZkVKS2ZoQzdOWDBnRW9yK1dICk5wa3Z6SG4wRjlaSkRjUGtoYm0vcHM0TGdDcWdMalBhTkR3RTFsMmZBNjkwTkJJZVZtQ0gxaGZ2SlhYM0ozbzEKVkV4UC80T3gzd0lEQVFBQm8wSXdRREFPQmdOVkhROEJBZjhFQkFNQ0FxUXdEd1lEVlIwVEFRSC9CQVV3QXdFQgovekFkQmdOVkhRNEVGZ1FVM2NCcEsrb2wweTFmZ211UUhnTE1xeDRFNW9Bd0RRWUpLb1pJaHZjTkFRRUxCUUFECmdnRUJBRFF5QkI0eUNjYjBvZmtpODZCU2piODJydVc2ckFoWVQ0cTljZnJWY2ZhdEs0ZURxSFAzMWROQTdRUmEKeFdmbCtsMFd6dkVmT2dVOGMxUDhSRE1NampubitteDdobnZOaUgwQ0xnL3R1RUFmRlZzZFZKYlNqMk5rZTAxRwpTN09RUkVmOGJkQklucmNkM0xiYThMU084MDhic0V0WmdnZG13RndBUWsvdWRYN1d2SUlQVkppeTZCeGpWZ0FWClJYaDZFcUxQaUlWTDJ3b0YwVGNHSXpGNE5UMlUzcWNLM2NKdUVjM1lzdkkrck1tWEJDT25FY0N2MzB2a1d0NmsKNnV4cEdOWGxidlRxekZTY3NZL09zZEVUeGpBVHhxTUEzZ2FLR000OTFnM3REUXhaUVNNRlJTMjRMYlFhSjJDQgpFbWUzMFhlempvdTNUNytyVWx1S1dCd3luUzg9Ci0tLS0tRU5EIENFUlRJRklDQVRFLS0tLS0KLS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSURURENDQWpTZ0F3SUJBZ0lJTUwzZTJ4ZG9Xd0F3RFFZSktvWklodmNOQVFFTEJRQXdSREVTTUJBR0ExVUUKQ3hNSmIzQmxibk5vYVdaME1TNHdMQVlEVlFRREV5VnJkV0psTFdGd2FYTmxjblpsY2kxelpYSjJhV05sTFc1bApkSGR2Y21zdGMybG5ibVZ5TUI0WERUSXdNRFl3TlRFeE1UZzFOVm9YRFRNd01EWXdNekV4TVRnMU5Wb3dSREVTCk1CQUdBMVVFQ3hNSmIzQmxibk5vYVdaME1TNHdMQVlEVlFRREV5VnJkV0psTFdGd2FYTmxjblpsY2kxelpYSjIKYVdObExXNWxkSGR2Y21zdGMybG5ibVZ5TUlJQklqQU5CZ2txaGtpRzl3MEJBUUVGQUFPQ0FROEFNSUlCQ2dLQwpBUUVBeDNGL3pVd0tZanMvSDZIaG9NWWxqamlRVDg0eklMOGVTVmZGVEZJMVUwZVcxbDV1akp2Wlk3bDRnZyt3CmlybEtFRUtoWmtXNUpWbEZwaVpxbW8yR0lPc3JJWGoxL0Fpc1VVcWdXUUtEWHVDUnhWNitCeE5xM1Z6d1VJUDYKNDA4L2o4Z0tPZGp6NVRTTFUwa0VIYmVLc0FYYmNZSUVYQXorTDBEdFo4WWx0NnpJc2hjaCs0RGxMUDR3R0NVMAowNWx3d2dEcUZUOE1lUEhzb0pISFRTcDFxT0V4Z2E5bjBzTGMxTGFXUkJabzBGMmNleWVqdy95bUlwUkNpVHc4CmZvR0cwYm4ydWZFOG9TWkRpMUZSa0JJQ2puNWlLdlkxNFR0VVpCN2RiMXFlZmZ5MStmZThJTElRdEphcStVaDkKL3R2QnUyYVJITlQyWnV1MUNvRDNlaWhqdlFJREFRQUJvMEl3UURBT0JnTlZIUThCQWY4RUJBTUNBcVF3RHdZRApWUjBUQVFIL0JBVXdBd0VCL3pBZEJnTlZIUTRFRmdRVXJpbGxEUHhFVHFQbVVuUHZrWTArSVBvYXErTXdEUVlKCktvWklodmNOQVFFTEJRQURnZ0VCQU1TTExxR0NObmhEZUhScWtaUWEvNktUR21KZVpEc1F3MURHRUpYc1VMVloKcXpRODFjZUtreEVDQVByK2hzcmRORVB6bEhDbUpGQXVYczBXQ0lRN1NvaUNJcmYzQnV6cVNIK2QxME9sZjlkdApXS1lSTmg1UXVaODgxWWhDNDZsZ3hZVjk5RjU3WW5ROG8rellaN2ZDUTMreGRGbytudXNRYys4K2tQM1VGcUJtCjd4Q2V2MmtJUm1RT005c05WUTcrdnBkb2I2dTJwN1VLSFplQmd6Q2h2ODhXclF4M2lIOUlob2J5bnkyREk1Y1UKS1BQbWNVQlY1Y2pualZkVVp2SW1wNDVqcEwvWUNLam5OQjNWNmVOQ2ROSnozZWh4Y3B2bFFMdVgycUhGdzdGTQozRWU1UlRMbnl6SEFTVFQramRJQUVvUUpTSnNHNFJiZ0g3WDVZd2laVDI0PQotLS0tLUVORCBDRVJUSUZJQ0FURS0tLS0tCi0tLS0tQkVHSU4gQ0VSVElGSUNBVEUtLS0tLQpNSUlETWpDQ0FocWdBd0lCQWdJSUczR3l1WWpVaU9nd0RRWUpLb1pJaHZjTkFRRUxCUUF3TnpFU01CQUdBMVVFCkN4TUpiM0JsYm5Ob2FXWjBNU0V3SHdZRFZRUURFeGhyZFdKbExXRndhWE5sY25abGNpMXNZaTF6YVdkdVpYSXcKSGhjTk1qQXdOakExTVRFeE9EVTJXaGNOTXpBd05qQXpNVEV4T0RVMldqQTNNUkl3RUFZRFZRUUxFd2x2Y0dWdQpjMmhwWm5ReElUQWZCZ05WQkFNVEdHdDFZbVV0WVhCcGMyVnlkbVZ5TFd4aUxYTnBaMjVsY2pDQ0FTSXdEUVlKCktvWklodmNOQVFFQkJRQURnZ0VQQURDQ0FRb0NnZ0VCQU03K1J1Y3pEWUpXVjAvK1FHaHNuTUUrV2dlcUJERS8KNDkrSUkyUEttL01rV0NDTlRYNU1yM05mZ0RKSDlMRDBRRzZMZlc3Q0lVWFZjeWZ4ZDd1WVNETERwVmJJSVRyQgpoa1g0WmdtQTd6d09RcUQ4SElhZUp0QmJ5ZnhaWFpBbkNoSlVMU2JscDFYa2NnTEVlVm5hTHZwOVF6QkpCOHRDCmRPczRqbFpkSmRXcm9GYlZJUUVJRHBYT0k4L0diY0dZTXd6cXRiNFRzUVFzNWZ6MG5FVlI0eXoveE9wN0xRQ1EKbVlwWER4cUhFbzVXb0w1Vk5YSGZmWUVRRE9WeVlTeDNEL21FYkd1QzNrUWdXd3F1LzFZeU1sbkFVN2djbDNMWQovSFg1bVRmSVRKVG1VKzJlOHFoNHZMTEVMckVVU2E4alVzR1cyUHIvSmx1NklPei9kR3d0OTdVQ0F3RUFBYU5DCk1FQXdEZ1lEVlIwUEFRSC9CQVFEQWdLa01BOEdBMVVkRXdFQi93UUZNQU1CQWY4d0hRWURWUjBPQkJZRUZDR0UKcmpMWk1wNk41a202NWZZUWVQalNnaDVqTUEwR0NTcUdTSWIzRFFFQkN3VUFBNElCQVFCMm1LVVRqNi9WUG5VRQoyeUI3NEtPQ3VBaldkU1JrVHpGU3JCNTFmZHBkQmZJSU1mRDRYai9ZRUkxSDZQQ2dZS2lzbWpaRUJ1QVNHQzNBCnNUNjBCVDFJQmFERG9PeWlOelhUUFdTRlVEUkFYeUYzWkJrVGlYM2JjTU9WRzI3VDBXRVRLU1Y3Q2N3N0pPcWEKSXJpQkJpc25RVUlhV0R3SnhEdm5ZUXNBendOWWVBNjRzdDhHNkRWaVF1ZkV1SFpBT0VUYXo5ek55NnNhd2ErUApjUUxVZTFsaVFSNk1EWjhGY2tPL1UxNURyeE16MVBJeUNiRW43LzVGVzFsL0lENkZBM05MQ3Yxa1hvTVdXd0dVCmpIYzRQMmZzVHBGbStGODNsdDlDcCtQWU92N1Jkdm1QQytUTSs1NW45djVES2p1SkVBZWtyNUdsN0NZcVJxaU8KTjdXdzNMc0YKLS0tLS1FTkQgQ0VSVElGSUNBVEUtLS0tLQo=
    server: %s
  name: crc
contexts:
- context:
    cluster: crc
    user: admin
  name: admin
current-context: admin
kind: Config
preferences: {}
users:
- name: admin
  user:
    client-certificate-data: LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSURaekNDQWsrZ0F3SUJBZ0lJTW9yOCtncGEwWGd3RFFZSktvWklodmNOQVFFTEJRQXdOakVTTUJBR0ExVUUKQ3hNSmIzQmxibk5vYVdaME1TQXdIZ1lEVlFRREV4ZGhaRzFwYmkxcmRXSmxZMjl1Wm1sbkxYTnBaMjVsY2pBZQpGdzB5TURBMk1EVXhNVEU0TlRWYUZ3MHpNREEyTURNeE1URTROVFZhTURBeEZ6QVZCZ05WQkFvVERuTjVjM1JsCmJUcHRZWE4wWlhKek1SVXdFd1lEVlFRREV3eHplWE4wWlcwNllXUnRhVzR3Z2dFaU1BMEdDU3FHU0liM0RRRUIKQVFVQUE0SUJEd0F3Z2dFS0FvSUJBUURzUHE3VDZWNS9JeWwzSlR6ais2REg4aFZqR0tGUWZGS3dya3l0NTNLNwprbHVKbXF1WXpIUDUwSHg5RDc2V2FVM0V5cmZJNWl1MElFOFhiQXcvUittT2M3QWErOHJqTWliVFc0UHFsSVZ3CkFNQTlLOExybG5HVnJvdmlaQ0Z3QmMwM0dZSUVKUENJZno4K25aQzhzSkswbEZteVY1SkY3NDdMY0RyTENTdVkKQnJEemdibWJOcTVjWndQVCsvUHMrZ283T3Q3dXlod25obndmeisyUmxBWFpsMk0zN25SY0ZJOGdBanM1Zjg1UgpNRTJNZk5jVHZLLzFXWThZREZSQ2ZNREtiUXNPR0NWUzFyRFd6MGIxaVJRS3JIVFdSWkNXczBXQWs0SmROODhuClRFdFZCcWtaZEp2dGxRY2dCR3pkMWg2WTVFWVZmOUM3ajlwdHdDc2YwaGozQWdNQkFBR2pmekI5TUE0R0ExVWQKRHdFQi93UUVBd0lGb0RBZEJnTlZIU1VFRmpBVUJnZ3JCZ0VGQlFjREFRWUlLd1lCQlFVSEF3SXdEQVlEVlIwVApBUUgvQkFJd0FEQWRCZ05WSFE0RUZnUVVZRFJXeVRpWUJqUlo2bHppQkxuUEFPM05ZZE13SHdZRFZSMGpCQmd3CkZvQVVHcUJLNmR2Wno1MUhlcjgvUEhPOC95cElydlV3RFFZSktvWklodmNOQVFFTEJRQURnZ0VCQUR6WUdjc3MKaUpOYm1wbHdSUk15cHF2UWMvdCtTcXk4cUhrU2xWSnpwMFN5d3RLVnFKTGh4VXRhZlBpVmlkQlFJZjdFVkZRMApQRG1FdXJidkJWSDNPWUtRZTlmdks1cVdjYmdsenFRS1hwcUxLaElvQ3V5VHZ2azNmT0xDMmdyYjNJTGx1WDlwCnBMVE9YbjV0akR6NlNsSTJYNnB6SjdpZGIvdHJtaVdDYWlNdmNkQ0Qrc0VMUGZzS0h5QWZZZ3RONk9zQ2hxTFYKcHYwRnQwRVZ4dnlFMzc5TkdnWnhyM3doWktGYjJRUFBWRWRVcGZPOFRpRnpWRWFueCtIdWxCZjVkWm1ZMUtmago3TU0xYmtoWUhqcFFyWEhWK2YyVHZLS0FRZHh4SlErODlCajlFK0YrSXl6djlyMFdQZ3JITXJUbTlzYjJpVGllCm9hcnVaZU9GYVJVUS8vTT0KLS0tLS1FTkQgQ0VSVElGSUNBVEUtLS0tLQo=
    client-key-data: LS0tLS1CRUdJTiBSU0EgUFJJVkFURSBLRVktLS0tLQpNSUlFcEFJQkFBS0NBUUVBN0Q2dTArbGVmeU1wZHlVODQvdWd4L0lWWXhpaFVIeFNzSzVNcmVkeXU1SmJpWnFyCm1NeHorZEI4ZlErK2xtbE54TXEzeU9ZcnRDQlBGMndNUDBmcGpuT3dHdnZLNHpJbTAxdUQ2cFNGY0FEQVBTdkMKNjVaeGxhNkw0bVFoY0FYTk54bUNCQ1R3aUg4L1BwMlF2TENTdEpSWnNsZVNSZStPeTNBNnl3a3JtQWF3ODRHNQptemF1WEdjRDAvdno3UG9LT3pyZTdzb2NKNFo4SDgvdGtaUUYyWmRqTis1MFhCU1BJQUk3T1gvT1VUQk5qSHpYCkU3eXY5Vm1QR0F4VVFuekF5bTBMRGhnbFV0YXcxczlHOVlrVUNxeDAxa1dRbHJORmdKT0NYVGZQSjB4TFZRYXAKR1hTYjdaVUhJQVJzM2RZZW1PUkdGWC9RdTQvYWJjQXJIOUlZOXdJREFRQUJBb0lCQUNlSWljc09kM0RCR3BSRQpsLzd5d2NJVDRiNVdoZEFwTGRGQktiWEVVRy9SR3g1WTByUmNLbUE0b2t4dlVRNXNpc1lPd2xpTkkrMGRwdjZkClp5TkR6bkszSzFZb29wZ0ljWFRYRUtrMXQycTV4WEczSEFRK2hiMXRteDBFY3BBRGVJYnE3dFh3dEl1eTk0dHIKNUtlZXlMNE5RVUZWNURWdDFEQjVGRzJibUQ3MU5XRW5KNFhncTUxNUxkY2VUS1dBbm92NURmbmVNcXJqU21oUgpHeHV4RnorbVZyeUowUVIyL3JZY1l5cWRsZFJKQ2REcFdGRndSdDRVbmlLaHVHdEhGZlpTU2ZYU2QvYmhRbnUwCmtmbWh5OFlMMFpURUkvSE9pVWdBUzhFUHVWbWhsbmRESkwxY2ZoRUJNaFQvYXd1TDYvQklZS3gyWWJNTmpjZUUKbjdwVzlRa0NnWUVBK045enRRU296Z3JBYWwyV1Z1ei9rWENsMzlmd0VjZFpaUXV2b2MxaU5ZU1NtUzVLanFtbQpjSmRGNFd6bG0yUUJnUllFT044RE1vL3RQNmF3MmFiYjQ4SW1FYlRjKzFodzJBUThHSGpYNXlqTFkvM0VrT1FxCi9QNWE3QXFhbU9udS83TnplU1FYZERwQzhiWnZsNm5wdkVzallEZi95dWVnVmR6VmRMSktDK01DZ1lFQTh3S20KNWE1NTNDa2I5YUxXbVFOUGFqd25ZVXRHaG5EQUhvaThEV0E4ZTVxYzg1MHRSNTBBVDVNazhYVUF0YjRJbFkvUgpVS3FYY3U0blU5Tk9rYmVndmJoZWZ6ZDNyR0xPQkdlQUhqWmptUTV0T1hMT1Z4SVFJdElvRVBVTGtSclJrRDB2CjF5eVdZYXdhQXlka213Nk5haEowbldQRUxnbU1TcXlqZlBWMnN0MENnWUVBamJ0Yi91d3ZZbUFYSXJ3M29UdUoKZEgrdHg2UUhrV2h4VGExeEVYbVJBNStEaVg4bWNNYkhCZm53anlmZ1B6V2Q4YkRqS0t4QSt1dWlsb3hNelRkTQpwUkh0Y2tvSlM0OGJmTG8wcTA4dXpmT2FtVkJ0UUlMZ3hJSHFyK0IrR0xXcEtiQStBL0I4OXZFekxNclVGSkJzCmo1SlBERDM0QzhzTHNicDVTZU03YmpjQ2dZRUEzSmVFcnl4QnZHT28yTUszc1BCN1Q0RkpjaDFsNkxaQy83UzUKbUI3SzZKMENhblk4V3l5ZTBwMU14TTZrRlZacTduRTkzYzd0YWN2YjhWRDRtbmdwTnU4OUFKaDJUd3JsM3NPaApYa3VhLzU1RDhnbFFXMk92T0J5emVDa3BGZEJWZVd6Qmw3OEd4NlQxZS9WdmN2Mnp5eHp6dE1lU2x3UGQwUStECjNQUHBpeFVDZ1lBRVE5VFI4Z0s0V0NuTUxWSUxzWGZUUlVEdjRram9Ob3prT0JwVG5nejN4T0dRNW9vajNMVm0KTEtJU1VTeU15SkJNRCtNU2dnMVd1SkEzYUdGTWdMcndnRS9HaXQvTEtjaW8rMjFUVjdUQUtsZWJJMGd5SFdGbgpRSEtmOWVrTTl3Ym5xa0VPUmJ1cUJ0UUxsTWJjeXI0cHF0V3FjU3lkd1M5czllL2hTaGVCMWc9PQotLS0tLUVORCBSU0EgUFJJVkFURSBLRVktLS0tLQo=
`, crc.Status.APIURL)))
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

	vmCPU := kubevirtv1.CPU{
		Sockets: uint32(crc.Spec.CPU),
		Cores:   1,
		Threads: 1,
	}
	vm.Spec.Template.Spec.Domain.CPU = &vmCPU

	guestMemory := resource.MustParse(crc.Spec.Memory)
	vmMemory := kubevirtv1.Memory{
		Guest: &guestMemory,
	}
	vm.Spec.Template.Spec.Domain.Memory = &vmMemory

	vmRequestCPU := crc.Spec.CPU / 2
	if vmRequestCPU < 2 {
		vmRequestCPU = 2
	}
	// TODO: don't hardcode this, but calculate it as some percent of
	// the requested memory with a floor of 9Gi
	vmRequestMemory := resource.MustParse("9Gi")
	vmResources := corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse(fmt.Sprintf("%d", vmRequestCPU)),
		corev1.ResourceMemory: vmRequestMemory,
	}
	vm.Spec.Template.Spec.Domain.Resources.Requests = vmResources

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

func (r *ReconcileCrcCluster) newRouteForCrcCluster(crc *crcv1alpha1.CrcCluster) (*routev1.Route, error) {
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

func (r *ReconcileCrcCluster) newIngressForCrcCluster(crc *crcv1alpha1.CrcCluster) (*networkingv1beta1.Ingress, error) {
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

// TODO: Obviously none of this should be hardcoded...
func createSSHClient(k8sService *corev1.Service) (sshClient.Client, error) {
	privateKey, err := ssh.ParsePrivateKey([]byte(`-----BEGIN OPENSSH PRIVATE KEY-----
b3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAAAAAABAAABlwAAAAdzc2gtcn
NhAAAAAwEAAQAAAYEAoC7Hrs5iaMisHjZn5lUAWlgGG2sHn3/LXINHUO0uR9QPWV4a+jO9
l+1C2WCp0RoJMqGnUq7RP9jRzen2TlRN21LzPH8w9TbJsnwGYdc8dHVSWjZ8PcahiqnMke
YXmrQQnY7ZL8/0Nbr97L0HSQ41GkZfiZm9aoX1RYXlEDhMNP7/4r4WkA6rQY1XkNsMGs4m
6WIGk0E1a1R8jWVi+7JV9zRjBy5vzMuiVTru+TMA6w64dWKgi29eVANQeg+OMOnrNtMNVl
sk1yAP7vm0cICIbGba3cALhFPhNX1tRoFcVqWMOVcTyi0yIxDRMP/ID0BikhbmyrrB6hUF
ivnGjUmG/xG2PfchSgDJYjXVYsPWKz7/TYUb/6l3253taPzvG4WoOloA8AAgWOQzo5z9v0
iXHk+tTpm5puas1y288o86P91tMLlCv3NaSrtQXTYSvGTsYHf5aT3pIGAq3TEUnv16VZTl
wnRBBf8UwBVNTsZLsW5UKA3nmnigVXQOuDsq3grlAAAFgA6PKBAOjygQAAAAB3NzaC1yc2
EAAAGBAKAux67OYmjIrB42Z+ZVAFpYBhtrB59/y1yDR1DtLkfUD1leGvozvZftQtlgqdEa
CTKhp1Ku0T/Y0c3p9k5UTdtS8zx/MPU2ybJ8BmHXPHR1Ulo2fD3GoYqpzJHmF5q0EJ2O2S
/P9DW6/ey9B0kONRpGX4mZvWqF9UWF5RA4TDT+/+K+FpAOq0GNV5DbDBrOJuliBpNBNWtU
fI1lYvuyVfc0Ywcub8zLolU67vkzAOsOuHVioItvXlQDUHoPjjDp6zbTDVZbJNcgD+75tH
CAiGxm2t3AC4RT4TV9bUaBXFaljDlXE8otMiMQ0TD/yA9AYpIW5sq6weoVBYr5xo1Jhv8R
tj33IUoAyWI11WLD1is+/02FG/+pd9ud7Wj87xuFqDpaAPAAIFjkM6Oc/b9Ilx5PrU6Zua
bmrNctvPKPOj/dbTC5Qr9zWkq7UF02Erxk7GB3+Wk96SBgKt0xFJ79elWU5cJ0QQX/FMAV
TU7GS7FuVCgN55p4oFV0Drg7Kt4K5QAAAAMBAAEAAAGAfSkQTb5llop2MoVAWfFA/VaaLw
JKSo6IUBkjuFAbQXSpKaMmYSncksGI4mFtTz2QwkcdfrWqOsEn7kVJd5rX2u/Nrw+TKYdN
wnC2a+zKCBVD68l2+q4huz9B4R5wgyj/cp0ThxBuOS2LC1gIQUUgqQ8jx1ihcIKLS297tF
jI8v/s4Ta2WombtvTB3yXJJ4i9Ts6RZK4nF15ElBcMaK7IDQiZ+BqIsPTMOtx5ra30obY2
20HdQBYdFngggb910zJyo0IDs7xZy/0XHhHT6M81nebulfBZPvktzQpyEH8TD8cZJKoQiH
oH9qpvEQTc8ZnWvqNgogzHwvExBBfLuEhK+wnI2wPCqSOy417LBj8np5jznrM3F6uN9BOa
slzHaGYlWqEDESse00FfaCjrXAOdwSYmE8BjkqT3nS3WyA8hqPRGoQWU12jEtFWTLspOi/
eMd4/CuTm5Ji2QGTBbDawp0xWwylAm3bqonRPLdrqz37CDXvOCap6hYF9H4Ef+bRwBAAAA
wF6akh/FDcYW6RddwB0aTeGmk6uDRaJxeI76GFvUloAef9Hq0J3oGiyr3qqQATo3BYfnyX
Ix6jd4Pue0fA8g8ki8wBp2ZxvfacYF5S8SRAeadAo7sx9njODJ/BDp35E+/zRkLA68BMmS
g8am3lTNbHPUGRoNUvybpJXcoMTmUf6oGZAuWXYRn7RkDaP+ixbpjSrSb7lwDUKiSX9wZK
L0beHRULSlOH55eqxOIr3QX+FBLlLmR2vuj2cZWxD8uTHV/QAAAMEAzLWx6LiRN/BwOMwy
++f5twza/jD0to3UiFLalOQYAIHKHwQGMQI3n9FBh1JLzOzG1tHMqTRiu3Wb9WGi5HBh2U
SX/iuORqD6nT/ClvojGDcF5TVOBCy91GBYIngRpy9iaCfxv5vNDTceQLfekIn6TSFod0Hd
MNh8vBiO9RXIm6vbzPo3zi1TmeoZkgXtTS9cKkK20EwStwSlnEhz7T6t8yZj84RWYqdpk3
i8IQ8XhJDJic8vAXFtUaRjHBNk5IThAAAAwQDIURMCxYDnLisnb3ILb8/K7OoDKKEQyoFE
YaYdtjSLcMgVROjKllwN0IzEGAn28cgphafXeCo7VgEN5DVWHv909w1ZDFX1Tf2G16+qlQ
nJese8qhTgems+EG+xBmVeCGBLBluQ8iSrx7TA9WvyKL9ElUvzWLRVDtEHqJOqLYb1JrtR
DFJEMUnvRq2X433USHAuY1yMZ4b8BWHx/67SbJLgkwq/NwUBKQEVCIHtp6IbKo3cPaymJA
4GkUdjSO9DQoUAAAAEY29yZQECAwQFBgc=
-----END OPENSSH PRIVATE KEY-----`))
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

func consoleHost(baseDomain string) string {
	return fmt.Sprintf("console-openshift-console.%s", baseDomain)
}
