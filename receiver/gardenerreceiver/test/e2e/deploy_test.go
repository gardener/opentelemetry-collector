// SPDX-FileCopyrightText: Contributors to the Gardener project
//
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	otelv1beta1 "github.com/open-telemetry/opentelemetry-operator/apis/v1beta1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/ptr"

	gardenercorev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	gardenerclientset "github.com/gardener/gardener/pkg/client/core/clientset/versioned"
)

const (
	// collectorSecretName is the volume-mounted secret holding the Gardener API
	// kubeconfig both collectors read to talk to the virtual garden.
	collectorSecretName = "gardener-viewer-kubeconfig"

	// prometheusStatefulSet is the StatefulSet the prometheus-operator generates
	// for the "gardener" Prometheus resource (it prefixes "prometheus-").
	prometheusStatefulSet = "prometheus-gardener"

	// prometheusLBService is the type: LoadBalancer service this suite creates to
	// expose Prometheus to the host. Gardener's provider-local cloud-controller
	// (deployed by `make kind-up`) assigns it an external IP from 172.18.255.224/27,
	// which infra.sh's setup-loopback-devices makes routable from the host.
	prometheusLBService = "prometheus-lb"

	// prometheusCollectorName / otlpCollectorName are the OpenTelemetryCollector
	// resource names; the otel operator names the generated workload
	// "<name>-collector", giving the *Deployment constants below.
	prometheusCollectorName = "gardener-prometheus"
	otlpCollectorName       = "gardener-otlp"

	// The otel operator names the workload for a collector "<name>-collector".
	prometheusCollectorDeployment = prometheusCollectorName + "-collector"
	otlpCollectorDeployment       = otlpCollectorName + "-collector"

	// otelCollectorResource is the plural resource name (for the REST path) of
	// the OpenTelemetryCollector CRD; unlike the prometheus-operator types it
	// exports neither a resource-name nor a kind constant.
	otelCollectorResource = "opentelemetrycollectors"
	otelCollectorKind     = "OpenTelemetryCollector"

	// resourceReadyTimeout bounds how long we wait for the operators to
	// reconcile their generated workloads into a Ready state.
	resourceReadyTimeout = 5 * time.Minute
	resourceReadyPoll    = 5 * time.Second
)

// deployComponents deploys the full test stack into the runtime cluster: it
// creates the test namespace, the Gardener API kubeconfig secret, both
// collectors, the Prometheus instance with its RBAC and ServiceMonitor, and the
// Shoot on the virtual garden, waits for the operator-generated workloads to
// become Ready, and creates the LoadBalancer service that exposes Prometheus. It
// returns the rest config for the runtime cluster so the caller can read the
// service's external IP.
func deployComponents(ctx context.Context) *restclient.Config {
	kubeconfig := os.Getenv("KUBECONFIG")
	Expect(kubeconfig).NotTo(BeEmpty(), "KUBECONFIG must point at the runtime cluster kubeconfig")

	gardenerDir := os.Getenv("GARDENER_DIR")
	Expect(gardenerDir).NotTo(BeEmpty(), "GARDENER_DIR must point at the gardener source tree")

	gardenerAPIKubeconfig := os.Getenv("GARDENER_API_KUBECONFIG")
	Expect(gardenerAPIKubeconfig).NotTo(BeEmpty(), "GARDENER_API_KUBECONFIG must point at the virtual-garden kubeconfig")

	collectorImage := os.Getenv("COLLECTOR_IMAGE")
	Expect(collectorImage).NotTo(BeEmpty(), "COLLECTOR_IMAGE must be the resolved collector image reference")

	namespace := testNamespace()

	restConfig, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	Expect(err).NotTo(HaveOccurred(), "load runtime kubeconfig")

	clientset, err := kubernetes.NewForConfig(restConfig)
	Expect(err).NotTo(HaveOccurred(), "build runtime clientset")

	By("creating the test namespace")
	create(ctx, clientset.CoreV1().Namespaces(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: namespace},
	})

	By("creating the Gardener receiver kubeconfig secret")
	kubeconfigBytes, err := os.ReadFile(gardenerAPIKubeconfig)
	Expect(err).NotTo(HaveOccurred(), "read Gardener API kubeconfig %s", gardenerAPIKubeconfig)
	create(ctx, clientset.CoreV1().Secrets(namespace), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: collectorSecretName, Namespace: namespace},
		Data:       map[string][]byte{"kubeconfig": kubeconfigBytes},
	})

	By("deploying the OpenTelemetry Collectors")
	otelClient := crdRESTClient(restConfig, otelv1beta1.GroupVersion, otelv1beta1.AddToScheme)
	createCRD(ctx, otelClient, otelCollectorResource, namespace, prometheusCollector(namespace, collectorImage))
	createCRD(ctx, otelClient, otelCollectorResource, namespace, otlpCollector(namespace, collectorImage))

	By("deploying the Prometheus RBAC")
	sa, clusterRole, binding := prometheusRBAC(namespace)
	create(ctx, clientset.CoreV1().ServiceAccounts(namespace), sa)
	create(ctx, clientset.RbacV1().ClusterRoles(), clusterRole)
	create(ctx, clientset.RbacV1().ClusterRoleBindings(), binding)

	By("deploying the Prometheus instance and the ServiceMonitor")
	monitoringClient := crdRESTClient(restConfig, monitoringv1.SchemeGroupVersion, monitoringv1.AddToScheme)
	createCRD(ctx, monitoringClient, monitoringv1.PrometheusName, namespace, prometheusInstance(namespace))
	createCRD(ctx, monitoringClient, monitoringv1.ServiceMonitorName, namespace, serviceMonitor(namespace))

	By("creating the Shoot on the virtual garden")
	createShoot(ctx, gardenerAPIKubeconfig, gardenerDir)

	By("waiting for the Prometheus collector deployment to become Ready")
	waitDeploymentReady(ctx, clientset, namespace, prometheusCollectorDeployment)

	By("waiting for the OTLP collector deployment to become Ready")
	waitDeploymentReady(ctx, clientset, namespace, otlpCollectorDeployment)

	By("waiting for the Prometheus instance to become Ready")
	waitStatefulSetReady(ctx, clientset, namespace, prometheusStatefulSet)

	By("exposing Prometheus via a LoadBalancer service")
	createPrometheusLoadBalancer(ctx, clientset, namespace)

	return restConfig
}

// createPrometheusLoadBalancer creates the type: LoadBalancer service that
// exposes Prometheus to the host. It copies its selector from the
// prometheus-operator-generated prometheus-operated service, which exists only
// after the Prometheus instance has reconciled — hence it runs after
// waitStatefulSetReady.
func createPrometheusLoadBalancer(ctx context.Context, clientset kubernetes.Interface, namespace string) {
	operated, err := clientset.CoreV1().Services(namespace).Get(ctx, prometheusService, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred(), "get %s service", prometheusService)

	port, err := strconv.Atoi(prometheusPort)
	Expect(err).NotTo(HaveOccurred(), "parse prometheus port")

	create(ctx, clientset.CoreV1().Services(namespace), &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: prometheusLBService, Namespace: namespace},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeLoadBalancer,
			Selector: operated.Spec.Selector,
			Ports: []corev1.ServicePort{{
				Name:       "web",
				Port:       int32(port),
				TargetPort: intstr.FromInt(port),
				Protocol:   corev1.ProtocolTCP,
			}},
		},
	})
}

// namedObject is the subset of client-go's typed create signatures the built-in
// objects share: Create(ctx, obj, opts) (T, error). It lets one create helper
// serve namespaces, secrets, and RBAC objects alike.
type namedObject[T any] interface {
	Create(ctx context.Context, obj T, opts metav1.CreateOptions) (T, error)
}

// create creates obj via a typed client-go interface, treating an AlreadyExists
// error as success so the suite is idempotent when re-run against a
// partially-provisioned cluster (SKIP_TEARDOWN).
func create[T any](ctx context.Context, client namedObject[T], obj T) {
	_, err := client.Create(ctx, obj, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return
	}
	Expect(err).NotTo(HaveOccurred(), "create %T", obj)
}

// crdRESTClient builds a REST client scoped to a single CRD GroupVersion, using
// the API package's own scheme as the (de)serializer so typed objects marshal
// correctly. It is the client-go equivalent of a generated typed clientset for
// groups (otel operator, prometheus-operator) that ship only API types.
func crdRESTClient(config *restclient.Config, gv schema.GroupVersion, addToScheme func(*runtime.Scheme) error) restclient.Interface {
	scheme := runtime.NewScheme()
	Expect(addToScheme(scheme)).To(Succeed())

	cfg := *config
	cfg.GroupVersion = &gv
	cfg.APIPath = "/apis"
	cfg.NegotiatedSerializer = serializer.NewCodecFactory(scheme).WithoutConversion()

	client, err := restclient.RESTClientFor(&cfg)
	Expect(err).NotTo(HaveOccurred(), "build REST client for %s", gv)
	return client
}

// createCRD POSTs a namespaced custom resource via a group-scoped REST client,
// treating AlreadyExists as success (see create).
func createCRD(ctx context.Context, client restclient.Interface, resource, namespace string, obj runtime.Object) {
	err := client.Post().
		Namespace(namespace).
		Resource(resource).
		Body(obj).
		Do(ctx).
		Error()
	if apierrors.IsAlreadyExists(err) {
		return
	}
	Expect(err).NotTo(HaveOccurred(), "create %s in %s", resource, namespace)
}

// prometheusCollector builds the collector whose gardener metrics are scraped
// by Prometheus via the ServiceMonitor (Prometheus-style names).
func prometheusCollector(namespace, image string) *otelv1beta1.OpenTelemetryCollector {
	return &otelv1beta1.OpenTelemetryCollector{
		TypeMeta:   metav1.TypeMeta{APIVersion: otelv1beta1.GroupVersion.String(), Kind: otelCollectorKind},
		ObjectMeta: metav1.ObjectMeta{Name: prometheusCollectorName, Namespace: namespace},
		Spec: otelv1beta1.OpenTelemetryCollectorSpec{
			OpenTelemetryCommonFields: otelv1beta1.OpenTelemetryCommonFields{
				Image:        image,
				Replicas:     ptr.To(int32(1)),
				Volumes:      []corev1.Volume{kubeconfigVolume()},
				VolumeMounts: []corev1.VolumeMount{kubeconfigVolumeMount()},
				Ports: []otelv1beta1.PortsSpec{{
					ServicePort: corev1.ServicePort{Name: "prometheus", Port: 8889, Protocol: corev1.ProtocolTCP},
				}},
			},
			Mode: otelv1beta1.ModeDeployment,
			// UpgradeStrategy has no omitempty; the CRD rejects the "" zero value,
			// so set it explicitly.
			UpgradeStrategy: otelv1beta1.UpgradeStrategyAutomatic,
			Config: otelv1beta1.Config{
				Receivers: gardenerReceiverConfig(),
				Exporters: otelv1beta1.AnyConfig{Object: map[string]any{
					"prometheus": map[string]any{"endpoint": "0.0.0.0:8889"},
				}},
				Service: otelv1beta1.Service{
					Pipelines: map[string]*otelv1beta1.Pipeline{
						"metrics": {Receivers: []string{"gardener"}, Exporters: []string{"prometheus"}},
					},
				},
			},
		},
	}
}

// otlpCollector builds the collector that pushes gardener metrics into
// Prometheus' OTLP receiver with NoTranslation (raw OpenTelemetry names).
func otlpCollector(namespace, image string) *otelv1beta1.OpenTelemetryCollector {
	// The Prometheus OTLP receiver lives at /api/v1/otlp; the otlphttp exporter
	// appends /v1/metrics to reach /api/v1/otlp/v1/metrics.
	metricsEndpoint := fmt.Sprintf("http://%s.%s.svc:%s/api/v1/otlp/v1/metrics", prometheusService, namespace, prometheusPort)
	return &otelv1beta1.OpenTelemetryCollector{
		TypeMeta:   metav1.TypeMeta{APIVersion: otelv1beta1.GroupVersion.String(), Kind: otelCollectorKind},
		ObjectMeta: metav1.ObjectMeta{Name: otlpCollectorName, Namespace: namespace},
		Spec: otelv1beta1.OpenTelemetryCollectorSpec{
			OpenTelemetryCommonFields: otelv1beta1.OpenTelemetryCommonFields{
				Image:        image,
				Replicas:     ptr.To(int32(1)),
				Volumes:      []corev1.Volume{kubeconfigVolume()},
				VolumeMounts: []corev1.VolumeMount{kubeconfigVolumeMount()},
			},
			Mode: otelv1beta1.ModeDeployment,
			// UpgradeStrategy has no omitempty; the CRD rejects the "" zero value,
			// so set it explicitly.
			UpgradeStrategy: otelv1beta1.UpgradeStrategyAutomatic,
			Config: otelv1beta1.Config{
				Receivers: gardenerReceiverConfig(),
				Exporters: otelv1beta1.AnyConfig{Object: map[string]any{
					"otlphttp": map[string]any{"metrics_endpoint": metricsEndpoint},
				}},
				Service: otelv1beta1.Service{
					Pipelines: map[string]*otelv1beta1.Pipeline{
						"metrics": {Receivers: []string{"gardener"}, Exporters: []string{"otlphttp"}},
					},
				},
			},
		},
	}
}

// gardenerReceiverConfig is the receivers block shared by both collectors: the
// gardener receiver reading the mounted virtual-garden kubeconfig.
func gardenerReceiverConfig() otelv1beta1.AnyConfig {
	return otelv1beta1.AnyConfig{Object: map[string]any{
		"gardener": map[string]any{"kubeconfig": "/var/run/secrets/gardener/kubeconfig"},
	}}
}

func kubeconfigVolume() corev1.Volume {
	return corev1.Volume{
		Name:         collectorSecretName,
		VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: collectorSecretName}},
	}
}

func kubeconfigVolumeMount() corev1.VolumeMount {
	return corev1.VolumeMount{Name: collectorSecretName, MountPath: "/var/run/secrets/gardener", ReadOnly: true}
}

// prometheusRBAC returns the ServiceAccount, ClusterRole, and ClusterRoleBinding
// that grant the Prometheus instance the discovery/scrape permissions it needs.
func prometheusRBAC(namespace string) (*corev1.ServiceAccount, *rbacv1.ClusterRole, *rbacv1.ClusterRoleBinding) {
	clusterScopedName := namespace + "-prometheus"
	return &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{Name: "prometheus", Namespace: namespace},
		},
		&rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{Name: clusterScopedName},
			Rules: []rbacv1.PolicyRule{
				{
					APIGroups: []string{""},
					Resources: []string{"nodes", "nodes/metrics", "services", "endpoints", "pods"},
					Verbs:     []string{"get", "list", "watch"},
				},
				{APIGroups: []string{""}, Resources: []string{"configmaps"}, Verbs: []string{"get"}},
				{
					APIGroups: []string{"discovery.k8s.io"},
					Resources: []string{"endpointslices"},
					Verbs:     []string{"get", "list", "watch"},
				},
				{
					APIGroups: []string{"networking.k8s.io"},
					Resources: []string{"ingresses"},
					Verbs:     []string{"get", "list", "watch"},
				},
				{NonResourceURLs: []string{"/metrics"}, Verbs: []string{"get"}},
			},
		},
		&rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: clusterScopedName},
			RoleRef: rbacv1.RoleRef{
				APIGroup: rbacv1.GroupName,
				Kind:     "ClusterRole",
				Name:     clusterScopedName,
			},
			Subjects: []rbacv1.Subject{{
				Kind:      rbacv1.ServiceAccountKind,
				Name:      "prometheus",
				Namespace: namespace,
			}},
		}
}

// serviceMonitorLabel is the label the Prometheus instance selects
// ServiceMonitors by, and that the ServiceMonitor carries.
var serviceMonitorLabel = map[string]string{"app": prometheusCollectorDeployment}

// prometheusInstance builds the Prometheus resource that scrapes via the
// ServiceMonitor and accepts OTLP writes with NoTranslation.
func prometheusInstance(namespace string) *monitoringv1.Prometheus {
	return &monitoringv1.Prometheus{
		TypeMeta:   metav1.TypeMeta{APIVersion: monitoringv1.SchemeGroupVersion.String(), Kind: monitoringv1.PrometheusesKind},
		ObjectMeta: metav1.ObjectMeta{Name: "gardener", Namespace: namespace},
		Spec: monitoringv1.PrometheusSpec{
			CommonPrometheusFields: monitoringv1.CommonPrometheusFields{
				ServiceAccountName: "prometheus",
				EnableFeatures:     []monitoringv1.EnableFeature{"otlp-write-receiver"},
				OTLP: &monitoringv1.OTLPConfig{
					TranslationStrategy: ptr.To(monitoringv1.NoTranslation),
				},
				ServiceMonitorSelector:          &metav1.LabelSelector{MatchLabels: serviceMonitorLabel},
				ServiceMonitorNamespaceSelector: &metav1.LabelSelector{},
			},
		},
	}
}

// serviceMonitor builds the ServiceMonitor that points Prometheus at the
// prometheus-exporter collector's metrics port.
func serviceMonitor(namespace string) *monitoringv1.ServiceMonitor {
	return &monitoringv1.ServiceMonitor{
		TypeMeta: metav1.TypeMeta{APIVersion: monitoringv1.SchemeGroupVersion.String(), Kind: monitoringv1.ServiceMonitorsKind},
		ObjectMeta: metav1.ObjectMeta{
			Name:      prometheusCollectorDeployment,
			Namespace: namespace,
			Labels:    serviceMonitorLabel,
		},
		Spec: monitoringv1.ServiceMonitorSpec{
			Selector: metav1.LabelSelector{MatchLabels: map[string]string{
				"app.kubernetes.io/name":                           prometheusCollectorDeployment,
				"operator.opentelemetry.io/collector-service-type": "base",
			}},
			Endpoints: []monitoringv1.Endpoint{{Port: "prometheus", Interval: monitoringv1.Duration("30s")}},
		},
	}
}

// createShoot decodes $GARDENER_DIR/example/provider-local/shoot.yaml into a
// typed Shoot and creates it via the Gardener core clientset against the
// virtual-garden kubeconfig — the equivalent of `kubectl --kubeconfig=... apply
// -f example/provider-local/shoot.yaml`.
func createShoot(ctx context.Context, gardenerAPIKubeconfig, gardenerDir string) {
	shootPath := filepath.Join(gardenerDir, "example", "provider-local", "shoot.yaml")
	shootBytes, err := os.ReadFile(shootPath)
	Expect(err).NotTo(HaveOccurred(), "read shoot manifest %s", shootPath)

	scheme := runtime.NewScheme()
	Expect(gardenercorev1beta1.AddToScheme(scheme)).To(Succeed())
	codecs := serializer.NewCodecFactory(scheme)

	obj, _, err := codecs.UniversalDeserializer().Decode(shootBytes, nil, &gardenercorev1beta1.Shoot{})
	Expect(err).NotTo(HaveOccurred(), "decode shoot manifest")
	shoot, ok := obj.(*gardenercorev1beta1.Shoot)
	Expect(ok).To(BeTrue(), "decoded object is not a Shoot")

	restConfig, err := clientcmd.BuildConfigFromFlags("", gardenerAPIKubeconfig)
	Expect(err).NotTo(HaveOccurred(), "load virtual-garden kubeconfig")

	gardenClient, err := gardenerclientset.NewForConfig(restConfig)
	Expect(err).NotTo(HaveOccurred(), "build gardener core clientset")

	_, err = gardenClient.CoreV1beta1().Shoots(shoot.Namespace).Create(ctx, shoot, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		return
	}
	Expect(err).NotTo(HaveOccurred(), "create shoot %s/%s", shoot.Namespace, shoot.Name)
}

// waitDeploymentReady polls until the named Deployment reports its full replica
// count available.
func waitDeploymentReady(ctx context.Context, clientset kubernetes.Interface, namespace, name string) {
	Eventually(func(g Gomega) {
		dep, err := clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(dep.Status.AvailableReplicas).To(Equal(ptr.Deref(dep.Spec.Replicas, 1)),
			"deployment %s/%s not fully available yet", namespace, name)
	}).WithTimeout(resourceReadyTimeout).WithPolling(resourceReadyPoll).Should(Succeed())
}

// waitStatefulSetReady polls until the named StatefulSet reports its full
// replica count ready.
func waitStatefulSetReady(ctx context.Context, clientset kubernetes.Interface, namespace, name string) {
	Eventually(func(g Gomega) {
		sts, err := clientset.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(sts.Status.ReadyReplicas).To(Equal(ptr.Deref(sts.Spec.Replicas, 1)),
			"statefulset %s/%s not fully ready yet", namespace, name)
	}).WithTimeout(resourceReadyTimeout).WithPolling(resourceReadyPoll).Should(Succeed())
}
