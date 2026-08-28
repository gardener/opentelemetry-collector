// SPDX-FileCopyrightText: Contributors to the Gardener project
//
// SPDX-License-Identifier: Apache-2.0

//go:build e2e

// Package e2e deploys the Gardener receiver end to end and validates that its
// metrics land in Prometheus. The hack/gardener-receiver-e2e-test.sh script
// brings up the KinD cluster, deploys Gardener, and resolves the collector
// image; this suite deploys every component (collectors, Prometheus, Shoot) and
// performs the assertions.
package e2e

import (
	"context"
	"fmt"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	promapi "github.com/prometheus/client_golang/api"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
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

// promAPI is the suite-wide Prometheus query client, pointed at the
// LoadBalancer service's external IP.
var promAPI promv1.API

// testNamespace is the runtime-cluster namespace every component is deployed
// into, overridable via TEST_NAMESPACE (the shell exports the same default).
func testNamespace() string {
	if ns := os.Getenv("TEST_NAMESPACE"); ns != "" {
		return ns
	}
	return defaultNamespace
}

var _ = BeforeSuite(func() {
	ctx := context.Background()

	// Deploy every component (namespace, secret, collectors, Prometheus stack,
	// Shoot) and wait for the operator-generated workloads to become Ready. The
	// shell script only brings up the cluster, deploys Gardener, and resolves the
	// collector image; everything downstream lives here.
	restConfig := deployComponents(ctx)

	namespace := testNamespace()

	clientset, err := kubernetes.NewForConfig(restConfig)
	Expect(err).NotTo(HaveOccurred(), "build clientset")

	ip := prometheusLoadBalancerIP(ctx, clientset, namespace)
	baseURL := fmt.Sprintf("http://%s:%s", ip, prometheusPort)

	client, err := promapi.NewClient(promapi.Config{Address: baseURL})
	Expect(err).NotTo(HaveOccurred(), "build prometheus client")
	promAPI = promv1.NewAPI(client)

	GinkgoWriter.Printf("Prometheus exposed via LoadBalancer service %s/%s at %s\n", namespace, prometheusLBService, baseURL)
})

var _ = Describe("Gardener receiver metrics in Prometheus", func() {
	// Scraped via the Prometheus exporter + ServiceMonitor: Prometheus-style
	// name (dots become underscores).
	It("exposes garden_shoot_info scraped via the Prometheus exporter", func(ctx SpecContext) {
		Eventually(func() (float64, error) {
			return queryPrometheus(ctx, "count(garden_shoot_info)")
		}).WithTimeout(metricTimeout).WithPolling(metricPoll).
			Should(BeNumerically(">", 0), "expected garden_shoot_info (scraped) to be present")
	})

	// Pushed via the second collector's otlphttp exporter into Prometheus' OTLP
	// receiver with NoTranslation: raw OpenTelemetry name (dots preserved).
	It("exposes garden.shoot.info pushed via OTLP (NoTranslation)", func(ctx SpecContext) {
		Eventually(func() (float64, error) {
			return queryPrometheus(ctx, `count({__name__="garden.shoot.info"})`)
		}).WithTimeout(metricTimeout).WithPolling(metricPoll).
			Should(BeNumerically(">", 0), "expected garden.shoot.info (OTLP) to be present")
	})
})

// prometheusLoadBalancerIP waits for the prometheus-lb LoadBalancer service
// (created by deployComponents) to be assigned an external IP and returns it.
func prometheusLoadBalancerIP(ctx context.Context, clientset kubernetes.Interface, namespace string) string {
	var ip string
	Eventually(func() (string, error) {
		svc, err := clientset.CoreV1().Services(namespace).Get(ctx, prometheusLBService, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		for _, ingress := range svc.Status.LoadBalancer.Ingress {
			if ingress.IP != "" {
				return ingress.IP, nil
			}
		}
		return "", nil
	}).WithTimeout(metricTimeout).WithPolling(metricPoll).
		ShouldNot(BeEmpty(), "expected an external IP on %s", prometheusLBService)

	svc, err := clientset.CoreV1().Services(namespace).Get(ctx, prometheusLBService, metav1.GetOptions{})
	Expect(err).NotTo(HaveOccurred(), "re-read %s service", prometheusLBService)
	for _, ingress := range svc.Status.LoadBalancer.Ingress {
		if ingress.IP != "" {
			ip = ingress.IP
			break
		}
	}
	Expect(ip).NotTo(BeEmpty())
	return ip
}

// queryPrometheus runs an instant query via the Prometheus API client and
// returns the numeric value of the first result sample, or 0 when there are no
// results yet. The queries used here wrap their selector in count(...), so the
// result is a single-element vector.
func queryPrometheus(ctx context.Context, query string) (float64, error) {
	value, warnings, err := promAPI.Query(ctx, query, time.Time{})
	if err != nil {
		return 0, fmt.Errorf("prometheus query %q: %w", query, err)
	}
	if len(warnings) > 0 {
		GinkgoWriter.Printf("prometheus query %q returned warnings: %v\n", query, warnings)
	}

	vector, ok := value.(model.Vector)
	if !ok {
		return 0, fmt.Errorf("prometheus query %q returned %s, want a vector", query, value.Type())
	}
	if len(vector) == 0 {
		return 0, nil
	}
	return float64(vector[0].Value), nil
}
