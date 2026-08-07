// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

// Package e2e validates that the Gardener receiver's metrics land in the
// Prometheus instance deployed by hack/gardener-receiver-e2e-test.sh. The shell
// script provisions the cluster and all components; this suite is invoked at
// the end to perform the actual assertions.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

const (
	defaultNamespace = "gardener-monitoring-test"
	// prometheusService is the headless service the prometheus-operator
	// creates for a Prometheus resource; it fronts the Prometheus pods on 9090.
	prometheusService = "prometheus-operated"
	prometheusPort    = "9090"

	metricTimeout = 5 * time.Minute
	metricPoll    = 10 * time.Second
)

// pf holds the suite-wide port-forward to Prometheus and the local URL callers
// query against.
var pf struct {
	baseURL  string
	stopChan chan struct{}
}

var _ = BeforeSuite(func() {
	kubeconfig := os.Getenv("KUBECONFIG")
	Expect(kubeconfig).NotTo(BeEmpty(), "KUBECONFIG must point at the runtime cluster kubeconfig")

	namespace := os.Getenv("TEST_NAMESPACE")
	if namespace == "" {
		namespace = defaultNamespace
	}

	restConfig, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	Expect(err).NotTo(HaveOccurred(), "load kubeconfig")

	clientset, err := kubernetes.NewForConfig(restConfig)
	Expect(err).NotTo(HaveOccurred(), "build clientset")

	ctx := context.Background()
	pod := readyPrometheusPod(ctx, clientset, namespace)

	localPort, stopChan := startPortForward(restConfig, clientset, namespace, pod)
	pf.baseURL = fmt.Sprintf("http://localhost:%d", localPort)
	pf.stopChan = stopChan

	GinkgoWriter.Printf("Port-forward to %s/%s established at %s\n", namespace, pod, pf.baseURL)
})

var _ = AfterSuite(func() {
	if pf.stopChan != nil {
		close(pf.stopChan)
	}
})

var _ = Describe("Gardener receiver metrics in Prometheus", func() {
	// Scraped via the Prometheus exporter + ServiceMonitor: Prometheus-style
	// name (dots become underscores).
	It("exposes garden_shoot_info scraped via the Prometheus exporter", func() {
		Eventually(func() (float64, error) {
			return queryPrometheusCount(pf.baseURL, "count(garden_shoot_info)")
		}).WithTimeout(metricTimeout).WithPolling(metricPoll).
			Should(BeNumerically(">", 0), "expected garden_shoot_info (scraped) to be present")
	})

	// Pushed via the second collector's otlphttp exporter into Prometheus' OTLP
	// receiver with NoTranslation: raw OpenTelemetry name (dots preserved).
	It("exposes garden.shoot.info pushed via OTLP (NoTranslation)", func() {
		Eventually(func() (float64, error) {
			return queryPrometheusCount(pf.baseURL, `count({__name__="garden.shoot.info"})`)
		}).WithTimeout(metricTimeout).WithPolling(metricPoll).
			Should(BeNumerically(">", 0), "expected garden.shoot.info (OTLP) to be present")
	})
})

// readyPrometheusPod returns the name of a running, ready pod backing the
// prometheus-operated service in the given namespace.
func readyPrometheusPod(ctx context.Context, clientset kubernetes.Interface, namespace string) string {
	svc, err := clientset.CoreV1().Services(namespace).Get(ctx, prometheusService, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred(), "get %s service", prometheusService)

	selector := metav1.FormatLabelSelector(&metav1.LabelSelector{MatchLabels: svc.Spec.Selector})

	var podName string
	Eventually(func() (string, error) {
		pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return "", err
		}
		for _, p := range pods.Items {
			if isPodReady(&p) {
				return p.Name, nil
			}
		}
		return "", nil
	}).WithTimeout(metricTimeout).WithPolling(metricPoll).
		ShouldNot(BeEmpty(), "expected a ready Prometheus pod behind %s", prometheusService)

	// Re-read once the Eventually has settled so we return the resolved name.
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	Expect(err).NotTo(HaveOccurred(), "list Prometheus pods")
	for _, p := range pods.Items {
		if isPodReady(&p) {
			podName = p.Name
			break
		}
	}
	Expect(podName).NotTo(BeEmpty())
	return podName
}

func isPodReady(pod *corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// startPortForward establishes a port-forward to the given pod's Prometheus
// port on a local ephemeral port and returns the resolved local port plus a
// stop channel that tears the forward down when closed.
func startPortForward(restConfig *restclient.Config, clientset kubernetes.Interface, namespace, pod string) (uint16, chan struct{}) {
	roundTripper, upgrader, err := spdy.RoundTripperFor(restConfig)
	Expect(err).NotTo(HaveOccurred(), "build spdy round tripper")

	reqURL := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(namespace).
		Name(pod).
		SubResource("portforward").
		URL()

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: roundTripper}, http.MethodPost, reqURL)

	stopChan := make(chan struct{})
	readyChan := make(chan struct{})

	// Local port "0" lets the kernel assign a free ephemeral port, which we
	// resolve via GetPorts once forwarding is ready.
	fw, err := portforward.New(dialer, []string{"0:" + prometheusPort}, stopChan, readyChan, GinkgoWriter, GinkgoWriter)
	Expect(err).NotTo(HaveOccurred(), "create port forwarder")

	go func() {
		defer GinkgoRecover()
		if err := fw.ForwardPorts(); err != nil {
			// ForwardPorts blocks until stopChan closes; an error here means the
			// forward failed to run.
			Fail(fmt.Sprintf("port-forward failed: %v", err))
		}
	}()

	select {
	case <-readyChan:
	case <-time.After(metricTimeout):
		close(stopChan)
		Fail("timed out waiting for port-forward to become ready")
	}

	ports, err := fw.GetPorts()
	Expect(err).NotTo(HaveOccurred(), "resolve forwarded ports")
	Expect(ports).NotTo(BeEmpty())
	return ports[0].Local, stopChan
}

// prometheusQueryResponse is the subset of the Prometheus /api/v1/query
// response we need to extract a scalar count.
type prometheusQueryResponse struct {
	Data struct {
		Result []struct {
			Value [2]json.RawMessage `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

// queryPrometheusCount runs an instant query and returns the numeric value of
// the first result sample, or 0 when there are no results yet.
func queryPrometheusCount(baseURL, query string) (float64, error) {
	u := baseURL + "/api/v1/query?" + url.Values{"query": {query}}.Encode()

	resp, err := http.Get(u)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("prometheus query %q returned status %d", query, resp.StatusCode)
	}

	var parsed prometheusQueryResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return 0, fmt.Errorf("decode prometheus response: %w", err)
	}
	if len(parsed.Data.Result) == 0 {
		return 0, nil
	}

	// value is [<unix timestamp float>, "<sample value string>"].
	var raw string
	if err := json.Unmarshal(parsed.Data.Result[0].Value[1], &raw); err != nil {
		return 0, fmt.Errorf("parse sample value: %w", err)
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("parse sample value %q: %w", raw, err)
	}
	return value, nil
}
