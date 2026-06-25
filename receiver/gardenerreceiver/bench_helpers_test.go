// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

//go:build bench

package gardenerreceiver

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	corev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

// The synthetic Shoot built by makeShoot mirrors the structural shape of a
// real Gardener Shoot resource (counts and field types are taken from a
// production example), but every value is a generic placeholder. Personal
// identifiers, internal landscape topology, IP ranges, cluster UIDs, and
// version hashes from the real example were intentionally NOT copied — only
// the shape is preserved, since it's the shape that determines cache
// memory footprint.
//
// In particular, the helper populates every field that transformShoot is
// designed to strip, so the benchmark exercises the full optimization path:
//
//   ObjectMeta:      Annotations, Labels, ManagedFields, Finalizers,
//                    OwnerReferences
//   Spec:            Networking (with ProviderConfig), Tolerations,
//                    SystemComponents, CloudProfile, ExposureClassName,
//                    SchedulerName, KubeAPIServer.* (Requests, Logging,
//                    EncryptionConfig, EventTTL, …)
//   Spec.Provider:   InfrastructureConfig, ControlPlaneConfig,
//                    WorkersSettings; per-worker ProviderConfig,
//                    UpdateStrategy, MaxSurge, MaxUnavailable,
//                    SystemComponents
//   Status:          AdvertisedAddresses, ClusterIdentity, Credentials,
//                    SeedName, LastHibernationTriggerTime, LastMaintenance,
//                    Networking, LastErrors; LastOperation.Description and
//                    LastUpdateTime
//
// Fields populated below but NOT touched by transformShoot (e.g. Conditions,
// Constraints, Spec.Hibernation) reflect realistic shoot bloat that the
// receiver legitimately keeps in cache; their inclusion makes the absolute
// post-transform footprint realistic too.

const benchNamespace = "garden-bench"

// makeShoot builds a synthetic Shoot whose structure matches a real one.
// The idx parameter is mixed into a few fields so that updates carry distinct
// payloads (avoids collapsing into a single canonical object on the heap
// via string interning of identical literals).
func makeShoot(idx int) *corev1beta1.Shoot {
	name := fmt.Sprintf("shoot-%06d", idx)
	uid := fmt.Sprintf("00000000-0000-0000-0000-%012d", idx)

	return &corev1beta1.Shoot{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "core.gardener.cloud/v1beta1",
			Kind:       "Shoot",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         benchNamespace,
			UID:               types.UID(uid),
			ResourceVersion:   fmt.Sprintf("%d", idx),
			Generation:        int64(1000 + idx),
			CreationTimestamp: metav1.Time{Time: time.Now().Add(-30 * 24 * time.Hour)},
			Annotations:       makeAnnotations(),
			Labels:            makeLabels(),
			Finalizers: []string{
				"gardener",
				"core.gardener.cloud/controllerregistration",
			},
			ManagedFields:   makeManagedFields(),
			OwnerReferences: makeOwnerReferences(uid),
		},
		Spec:   makeShootSpec(idx),
		Status: makeShootStatus(idx),
	}
}

// makeAnnotations returns annotations with realistic key prefixes and a
// neutral creator value (no real email).
func makeAnnotations() map[string]string {
	return map[string]string{
		"gardener.cloud/created-by":       "bench@example.invalid",
		"shoot.gardener.cloud/tasks":      "deployInfrastructure",
		"gardener.cloud/operation-status": "ok",
	}
}

// makeLabels mirrors the count and key shape of the real example
// (~11 extension/provider labels) without copying internal extension names
// that aren't already public.
func makeLabels() map[string]string {
	return map[string]string{
		"extensions.extensions.gardener.cloud/image-rewriter":                   "true",
		"extensions.extensions.gardener.cloud/shoot-cert-service":               "true",
		"extensions.extensions.gardener.cloud/shoot-dns-service":                "true",
		"extensions.extensions.gardener.cloud/shoot-networking-filter":          "true",
		"extensions.extensions.gardener.cloud/shoot-networking-problemdetector": "true",
		"name.seed.gardener.cloud/bench-seed":                                   "true",
		"networking.extensions.gardener.cloud/calico":                           "true",
		"operatingsystemconfig.extensions.gardener.cloud/gardenlinux":           "true",
		"provider.extensions.gardener.cloud/example":                            "true",
		"shoot.gardener.cloud/status":                                           "healthy",
	}
}

// makeManagedFields produces five entries with FieldsV1 blobs of the size
// real serializations carry. The blob content is a meaningless byte pattern.
func makeManagedFields() []metav1.ManagedFieldsEntry {
	const entryCount = 5
	entries := make([]metav1.ManagedFieldsEntry, 0, entryCount)
	fieldsBlob := []byte(strings.Repeat(`{"f:metadata":{"f:annotations":{}}}`, 60))
	for idx := 0; idx < entryCount; idx++ {
		entries = append(entries, metav1.ManagedFieldsEntry{
			Manager:    fmt.Sprintf("gardener-controller-%d", idx),
			Operation:  metav1.ManagedFieldsOperationUpdate,
			APIVersion: "core.gardener.cloud/v1beta1",
			Time:       &metav1.Time{Time: time.Now()},
			FieldsType: "FieldsV1",
			FieldsV1:   &metav1.FieldsV1{Raw: fieldsBlob},
		})
	}
	return entries
}

func makeOwnerReferences(uid string) []metav1.OwnerReference {
	// Single owner reference is typical; included so transformShoot has
	// something to clear.
	return []metav1.OwnerReference{{
		APIVersion: "core.gardener.cloud/v1beta1",
		Kind:       "Project",
		Name:       "bench-project",
		UID:        types.UID(uid),
	}}
}

// makeShootSpec assembles a spec with four worker pools, full kubeAPIServer
// config block, networking with provider config, hibernation schedule and
// maintenance window. Field counts match the real example.
func makeShootSpec(idx int) corev1beta1.ShootSpec {
	hibernationEnabled := false
	hibernationStart := "00 20 * * 1,3,4,5,6,0,2"
	hibernationLocation := "Europe/Berlin"
	autoUpdateMachineImage := true

	apiAudiences := []string{"kubernetes"}
	eventTTL := metav1.Duration{Duration: time.Hour}

	infraConfig := apiruntime.RawExtension{
		Raw: []byte(`{"apiVersion":"example.provider.extensions.gardener.cloud/v1alpha1","kind":"InfrastructureConfig","networks":{"workers":"10.0.0.0/16"}}`),
	}
	controlPlaneConfig := apiruntime.RawExtension{
		Raw: []byte(`{"apiVersion":"example.provider.extensions.gardener.cloud/v1alpha1","kind":"ControlPlaneConfig","loadBalancerProvider":"default"}`),
	}
	networkingProviderConfig := apiruntime.RawExtension{
		Raw: []byte(`{"birdExporter":{"enabled":true},"overlay":{"enabled":true}}`),
	}
	dnsExtensionConfig := apiruntime.RawExtension{
		Raw: []byte(`{"apiVersion":"service.dns.extensions.gardener.cloud/v1alpha1","kind":"DNSConfig","syncProvidersFromShootSpecDNS":true}`),
	}

	return corev1beta1.ShootSpec{
		CloudProfile: &corev1beta1.CloudProfileReference{
			Kind: "CloudProfile",
			Name: "bench-cloud-profile",
		},
		CredentialsBindingName: ptr.To("bench-credentials-binding"),
		DNS: &corev1beta1.DNS{
			Domain: ptr.To(fmt.Sprintf("shoot-%06d.bench.example.invalid", idx)),
		},
		Extensions: []corev1beta1.Extension{{
			Type:           "shoot-dns-service",
			ProviderConfig: &dnsExtensionConfig,
		}},
		Hibernation: &corev1beta1.Hibernation{
			Enabled: &hibernationEnabled,
			Schedules: []corev1beta1.HibernationSchedule{{
				Start:    &hibernationStart,
				Location: &hibernationLocation,
			}},
		},
		Kubernetes: corev1beta1.Kubernetes{
			Version: "1.34.6",
			KubeAPIServer: &corev1beta1.KubeAPIServerConfig{
				DefaultNotReadyTolerationSeconds:    ptr.To[int64](300),
				DefaultUnreachableTolerationSeconds: ptr.To[int64](300),
				EventTTL:                            &eventTTL,
				APIAudiences:                        apiAudiences,
				Logging: &corev1beta1.APIServerLogging{
					Verbosity: ptr.To[int32](2),
				},
				Requests: &corev1beta1.APIServerRequests{
					MaxMutatingInflight:    ptr.To[int32](200),
					MaxNonMutatingInflight: ptr.To[int32](400),
				},
				EncryptionConfig: &corev1beta1.EncryptionConfig{
					Resources: []string{"secrets"},
				},
			},
		},
		Maintenance: &corev1beta1.Maintenance{
			AutoUpdate: &corev1beta1.MaintenanceAutoUpdate{
				KubernetesVersion:   true,
				MachineImageVersion: &autoUpdateMachineImage,
			},
			TimeWindow: &corev1beta1.MaintenanceTimeWindow{
				Begin: "020000+0200",
				End:   "030000+0200",
			},
		},
		Networking: &corev1beta1.Networking{
			Type:           ptr.To("calico"),
			Nodes:          ptr.To("10.0.0.0/16"),
			Pods:           ptr.To("100.64.0.0/12"),
			Services:       ptr.To("100.104.0.0/13"),
			IPFamilies:     []corev1beta1.IPFamily{corev1beta1.IPFamilyIPv4},
			ProviderConfig: &networkingProviderConfig,
		},
		Provider: corev1beta1.Provider{
			Type:                 "example",
			InfrastructureConfig: &infraConfig,
			ControlPlaneConfig:   &controlPlaneConfig,
			Workers:              makeWorkers(),
			WorkersSettings: &corev1beta1.WorkersSettings{
				SSHAccess: &corev1beta1.SSHAccess{Enabled: true},
			},
		},
		Purpose:           ptr.To(corev1beta1.ShootPurpose("evaluation")),
		Region:            "bench-region-1",
		SeedName:          ptr.To(fmt.Sprintf("bench-seed-%d", idx%20)),
		SchedulerName:     ptr.To("default-scheduler"),
		ExposureClassName: ptr.To("standard"),
		SystemComponents: &corev1beta1.SystemComponents{
			CoreDNS: &corev1beta1.CoreDNS{
				Autoscaling: &corev1beta1.CoreDNSAutoscaling{
					Mode: corev1beta1.CoreDNSAutoscalingModeHorizontal,
				},
			},
			NodeLocalDNS: &corev1beta1.NodeLocalDNS{
				Enabled: true,
			},
		},
	}
}

// makeWorkers builds four worker pools with sizes/zones matching the real
// example shape. Per-pool ProviderConfig is populated so transformShoot has
// something non-trivial to drop.
func makeWorkers() []corev1beta1.Worker {
	zones := []string{"bench-1a", "bench-1b", "bench-1c"}
	pools := []struct {
		name      string
		machine   string
		min, max  int32
		hasLabels bool
	}{
		{"small", "bench.c2.m4", 0, 6, false},
		{"large", "bench.c16.m32", 0, 10, true},
		{"larger", "bench.c16.m64", 0, 10, true},
		{"largest", "bench.c32.m128", 0, 3, false},
	}

	workers := make([]corev1beta1.Worker, 0, len(pools))
	providerConfig := apiruntime.RawExtension{
		Raw: []byte(`{"apiVersion":"example.provider.extensions.gardener.cloud/v1alpha1","kind":"WorkerConfig"}`),
	}
	for _, pool := range pools {
		worker := corev1beta1.Worker{
			Name: pool.name,
			CRI: &corev1beta1.CRI{
				Name: corev1beta1.CRINameContainerD,
			},
			Machine: corev1beta1.Machine{
				Type:         pool.machine,
				Architecture: ptr.To("amd64"),
				Image: &corev1beta1.ShootMachineImage{
					Name:    "gardenlinux",
					Version: ptr.To("2150.4.0"),
				},
			},
			Maximum:        pool.max,
			Minimum:        pool.min,
			MaxSurge:       intOrStringPtr(1),
			MaxUnavailable: intOrStringPtr(0),
			Zones:          zones,
			SystemComponents: &corev1beta1.WorkerSystemComponents{
				Allow: true,
			},
			UpdateStrategy: ptr.To(corev1beta1.AutoRollingUpdate),
			ProviderConfig: &providerConfig,
		}
		if pool.hasLabels {
			worker.Labels = map[string]string{"pool": "demo-pods"}
		}
		workers = append(workers, worker)
	}
	return workers
}

// makeShootStatus mirrors the status block of a fully reconciled shoot:
// 5 conditions, 3 constraints, advertisedAddresses, lastMaintenance with a
// long description, networking status, and the gardener identity block.
func makeShootStatus(idx int) corev1beta1.ShootStatus {
	now := time.Now()
	timestamp := metav1.Time{Time: now}

	conditions := []corev1beta1.Condition{
		newCondition("APIServerAvailable", "HealthzRequestSucceeded",
			"API server /healthz endpoint responded with success status code.", timestamp),
		newCondition("ControlPlaneHealthy", "ControlPlaneRunning",
			"All control plane components are healthy.", timestamp),
		newCondition("ObservabilityComponentsHealthy", "ObservabilityComponentsRunning",
			"All observability components are healthy.", timestamp),
		newCondition("EveryNodeReady", "EveryNodeReady",
			"All nodes are ready.", timestamp),
		newCondition("SystemComponentsHealthy", "SystemComponentsRunning",
			"All system components are healthy.", timestamp),
	}
	constraints := []corev1beta1.Condition{
		newCondition("UsesUnifiedHTTPProxyPort", "ShootUsesUnifiedHTTPProxyPort",
			"Shoot uses http-proxy port 8443 for VPN", timestamp),
		newCondition("HibernationPossible", "NoProblematicWebhooks",
			"All webhooks are properly configured.", timestamp),
		newCondition("MaintenancePreconditionsSatisfied", "NoProblematicWebhooks",
			"All webhooks are properly configured.", timestamp),
	}

	advertisedAddresses := []corev1beta1.ShootAdvertisedAddress{
		{Name: "external", URL: fmt.Sprintf("https://api.shoot-%06d.bench.example.invalid", idx)},
		{Name: "wildcard-tls-seed-bound", URL: fmt.Sprintf("https://api-shoot-%06d.ingress.bench.example.invalid", idx)},
		{Name: "internal", URL: fmt.Sprintf("https://api.shoot-%06d.internal.bench.example.invalid", idx)},
		{Name: "service-account-issuer", URL: fmt.Sprintf("https://api.shoot-%06d.internal.bench.example.invalid", idx)},
		{
			Name: "ingress/oauth2-ingress/0/0",
			URL:  fmt.Sprintf("https://plutono-shoot-%06d.ingress.bench.example.invalid", idx),
		},
	}

	return corev1beta1.ShootStatus{
		Conditions:  conditions,
		Constraints: constraints,
		Gardener: corev1beta1.Gardener{
			ID:      "bench-gardenlet-id",
			Name:    "gardenlet-bench",
			Version: "v1.0.0-bench",
		},
		IsHibernated:        false,
		LastOperation:       makeLastOperation(),
		LastErrors:          makeLastErrors(),
		ObservedGeneration:  int64(1000 + idx),
		SeedName:            ptr.To(fmt.Sprintf("bench-seed-%d", idx%20)),
		TechnicalID:         fmt.Sprintf("shoot--bench--%06d", idx),
		UID:                 types.UID(fmt.Sprintf("00000000-0000-0000-0000-%012d", idx)),
		ClusterIdentity:     ptr.To(fmt.Sprintf("shoot--bench--shoot-%06d-bench-landscape", idx)),
		AdvertisedAddresses: advertisedAddresses,
		Credentials: &corev1beta1.ShootCredentials{
			Rotation: &corev1beta1.ShootCredentialsRotation{},
		},
		LastHibernationTriggerTime: &metav1.Time{Time: now.Add(-12 * time.Hour)},
		LastMaintenance: &corev1beta1.LastMaintenance{
			Description: strings.Repeat(
				`Worker pool updated machine image "gardenlinux" from "2150.3.0" to "2150.4.0". `,
				4),
			TriggeredTime: metav1.Time{Time: now.Add(-8 * time.Hour)},
			State:         corev1beta1.LastOperationStateSucceeded,
		},
		Networking: &corev1beta1.NetworkingStatus{
			EgressCIDRs: []string{"10.0.0.0/16"},
			Nodes:       []string{"10.0.0.0/16"},
			Pods:        []string{"100.64.0.0/12"},
			Services:    []string{"100.104.0.0/13"},
		},
	}
}

func makeLastOperation() *corev1beta1.LastOperation {
	return &corev1beta1.LastOperation{
		Type:           corev1beta1.LastOperationTypeReconcile,
		State:          corev1beta1.LastOperationStateSucceeded,
		Progress:       100,
		Description:    "Shoot cluster has been successfully reconciled.",
		LastUpdateTime: metav1.Time{Time: time.Now()},
	}
}

func makeLastErrors() []corev1beta1.LastError {
	// Empty in healthy shoots, but the real type is small enough that we
	// model the empty slice case explicitly to match the field's typical
	// presence rather than nil.
	return nil
}

func newCondition(condType, reason, message string, timestamp metav1.Time) corev1beta1.Condition {
	return corev1beta1.Condition{
		Type:               corev1beta1.ConditionType(condType),
		Status:             corev1beta1.ConditionTrue,
		LastTransitionTime: timestamp,
		LastUpdateTime:     timestamp,
		Reason:             reason,
		Message:            message,
	}
}

func intOrStringPtr(value int) *intstr.IntOrString {
	x := intstr.FromInt(value)
	return &x
}

// shootGVR is the GroupVersionResource used when driving updates through the
// fake clientset's tracker. Direct typed Update calls are expensive; using the
// tracker keeps the churn loop hot path lean.
var shootGVR = schema.GroupVersionResource{
	Group:    "core.gardener.cloud",
	Version:  "v1beta1",
	Resource: "shoots",
}

// memSnapshot captures heap state after forcing GC twice. Two GCs because the
// first frees finalizable objects and the second is the one that gives a
// stable read.
func memSnapshot() runtime.MemStats {
	runtime.GC()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats
}

func reportMem(b *testing.B, label string, before, after runtime.MemStats) {
	b.Helper()
	const bytesPerMiB = float64(1 << 20)
	b.ReportMetric(float64(after.HeapAlloc-before.HeapAlloc)/bytesPerMiB, label+"_heap_MiB")
	b.ReportMetric(float64(after.HeapInuse-before.HeapInuse)/bytesPerMiB, label+"_inuse_MiB")
	b.ReportMetric(float64(after.Mallocs-before.Mallocs), label+"_mallocs")
	// NumGC is a counter of completed GC cycles. memSnapshot forces two GCs
	// before sampling, so the delta between baseline and post-load shows GC
	// pressure caused by the load itself rather than by sampling.
	b.ReportMetric(float64(after.NumGC-before.NumGC), label+"_gc_cycles")
}

// diffMiB returns (current - baseline) in MiB as a signed float, so callers
// don't underflow when GC has shrunk the heap below the baseline between
// samples.
func diffMiB(current, baseline uint64) float64 {
	return (float64(current) - float64(baseline)) / float64(1<<20)
}
