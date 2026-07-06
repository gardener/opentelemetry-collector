// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package gardenerreceiver

import (
	"testing"

	corev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	securityv1alpha1 "github.com/gardener/gardener/pkg/apis/security/v1alpha1"
	seedmanagementv1alpha1 "github.com/gardener/gardener/pkg/apis/seedmanagement/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

func TestTransformShoot_RetainsUsedFields(t *testing.T) {
	shoot := &corev1beta1.Shoot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-shoot",
			Namespace: "garden-dev",
			UID:       "abc-123",
			Labels: map[string]string{
				"shoot.gardener.cloud/status": "healthy",
				"unused":                      "drop-me",
			},
			CreationTimestamp: metav1.Now(),
		},
		Spec: corev1beta1.ShootSpec{
			Region: "eu-west-1",
			Provider: corev1beta1.Provider{
				Type: "aws",
				Workers: []corev1beta1.Worker{
					{
						Name:    "worker-1",
						Minimum: 1,
						Maximum: 3,
						Machine: corev1beta1.Machine{
							Type: "m5.large",
							Image: &corev1beta1.ShootMachineImage{
								Name:    "gardenlinux",
								Version: ptr.To("1.0"),
							},
							Architecture: ptr.To("amd64"),
						},
						CRI: &corev1beta1.CRI{
							Name: "containerd",
						},
						Zones:  []string{"eu-west-1a"},
						Taints: []corev1.Taint{{Key: "key1"}},
						Labels: map[string]string{"l": "v"},
					},
				},
			},
			Kubernetes: corev1beta1.Kubernetes{
				Version: "1.28.0",
				KubeAPIServer: &corev1beta1.KubeAPIServerConfig{
					AuditConfig: &corev1beta1.AuditConfig{},
					StructuredAuthentication: &corev1beta1.StructuredAuthentication{
						ConfigMapName: "config-map",
					},
					AdmissionPlugins: []corev1beta1.AdmissionPlugin{
						{Name: "PodSecurity"},
					},
				},
				KubeControllerManager: &corev1beta1.KubeControllerManagerConfig{
					NodeCIDRMaskSize:              ptr.To[int32](24),
					HorizontalPodAutoscalerConfig: &corev1beta1.HorizontalPodAutoscalerConfig{},
				},
				KubeScheduler: &corev1beta1.KubeSchedulerConfig{},
				Kubelet:       &corev1beta1.KubeletConfig{PodPIDsLimit: ptr.To[int64](100)},
				KubeProxy:     &corev1beta1.KubeProxyConfig{},
			},
			SeedName: ptr.To("seed-1"),
			Purpose:  (*corev1beta1.ShootPurpose)(ptr.To("production")),
			ControlPlane: &corev1beta1.ControlPlane{
				HighAvailability: &corev1beta1.HighAvailability{
					FailureTolerance: corev1beta1.FailureTolerance{
						Type: corev1beta1.FailureToleranceTypeZone,
					},
				},
			},
			Hibernation: &corev1beta1.Hibernation{
				Enabled:   ptr.To(true),
				Schedules: []corev1beta1.HibernationSchedule{{Start: ptr.To("0 17 * * *")}},
			},
			Maintenance: &corev1beta1.Maintenance{
				TimeWindow: &corev1beta1.MaintenanceTimeWindow{Begin: "010000+0000", End: "020000+0000"},
				AutoUpdate: &corev1beta1.MaintenanceAutoUpdate{KubernetesVersion: true},
			},
			DNS:                    &corev1beta1.DNS{Domain: ptr.To("example.com")},
			Extensions:             []corev1beta1.Extension{{Type: "shoot-dns-service"}},
			SecretBindingName:      ptr.To("sb-1"), //nolint:staticcheck // SA1019
			CredentialsBindingName: ptr.To("cb-1"),
		},
		Status: corev1beta1.ShootStatus{
			TechnicalID:  "shoot--dev--my-shoot",
			IsHibernated: true,
			LastOperation: &corev1beta1.LastOperation{
				Type:     corev1beta1.LastOperationTypeReconcile,
				State:    corev1beta1.LastOperationStateSucceeded,
				Progress: 100,
			},
			Conditions:  []corev1beta1.Condition{{Type: "APIServerAvailable", Status: "True"}},
			Constraints: []corev1beta1.Condition{{Type: "MaintenancePreconditionsSatisfied", Status: "True"}},
			LastErrors:  []corev1beta1.LastError{{Description: "err"}},
			Gardener:    corev1beta1.Gardener{Version: "1.90.0"},
		},
	}

	result, err := transformShoot(shoot)
	require.NoError(t, err)
	s := result.(*corev1beta1.Shoot)

	assert.Equal(t, "my-shoot", s.Name)
	assert.Equal(t, "garden-dev", s.Namespace)
	assert.Equal(t, map[string]string{"shoot.gardener.cloud/status": "healthy"}, s.Labels)
	assert.NotEmpty(t, s.CreationTimestamp)
	assert.Equal(t, "aws", s.Spec.Provider.Type)
	assert.Equal(t, "eu-west-1", s.Spec.Region)
	assert.Len(t, s.Spec.Provider.Workers, 1)
	assert.Equal(t, "worker-1", s.Spec.Provider.Workers[0].Name)
	assert.Equal(t, int32(1), s.Spec.Provider.Workers[0].Minimum)
	assert.Equal(t, int32(3), s.Spec.Provider.Workers[0].Maximum)
	assert.Equal(t, "m5.large", s.Spec.Provider.Workers[0].Machine.Type)
	assert.NotNil(t, s.Spec.Provider.Workers[0].Machine.Image)
	assert.NotNil(t, s.Spec.Provider.Workers[0].CRI)
	assert.Equal(t, []string{"eu-west-1a"}, s.Spec.Provider.Workers[0].Zones)
	assert.Len(t, s.Spec.Provider.Workers[0].Taints, 1)
	assert.Equal(t, "1.28.0", s.Spec.Kubernetes.Version)
	assert.NotNil(t, s.Spec.Kubernetes.KubeAPIServer.AuditConfig)
	assert.NotEmpty(t, s.Spec.Kubernetes.KubeAPIServer.StructuredAuthentication.ConfigMapName)
	assert.Len(t, s.Spec.Kubernetes.KubeAPIServer.AdmissionPlugins, 1)
	assert.NotNil(t, s.Spec.Kubernetes.KubeControllerManager)
	assert.NotNil(t, s.Spec.Kubernetes.KubeScheduler)
	assert.NotNil(t, s.Spec.Kubernetes.Kubelet)
	assert.NotNil(t, s.Spec.Kubernetes.KubeProxy)
	assert.Equal(t, ptr.To("seed-1"), s.Spec.SeedName)
	assert.NotNil(t, s.Spec.Purpose)
	assert.NotNil(t, s.Spec.ControlPlane)
	assert.NotNil(t, s.Spec.Hibernation)
	assert.NotNil(t, s.Spec.Maintenance)
	assert.NotNil(t, s.Spec.DNS)
	assert.Len(t, s.Spec.Extensions, 1)
	assert.NotNil(t, s.Spec.SecretBindingName) //nolint:staticcheck // SA1019
	assert.NotNil(t, s.Spec.CredentialsBindingName)
	assert.Equal(t, "shoot--dev--my-shoot", s.Status.TechnicalID)
	assert.True(t, s.Status.IsHibernated)
	assert.NotNil(t, s.Status.LastOperation)
	assert.Len(t, s.Status.Conditions, 1)
	assert.Len(t, s.Status.Constraints, 1)
	assert.Len(t, s.Status.LastErrors, 1)
	assert.Equal(t, "1.90.0", s.Status.Gardener.Version)
}

func TestTransformShoot_StripsUnusedFields(t *testing.T) {
	shoot := &corev1beta1.Shoot{
		ObjectMeta: metav1.ObjectMeta{
			Name:          "my-shoot",
			Namespace:     "garden-dev",
			ManagedFields: []metav1.ManagedFieldsEntry{{Manager: "gardener"}},
			Labels: map[string]string{
				"shoot.gardener.cloud/status": "progressing",
				"foo":                         "bar",
			},
			Annotations:     map[string]string{"foo": "bar"},
			Finalizers:      []string{"gardener"},
			OwnerReferences: []metav1.OwnerReference{{Name: "owner"}},
		},
		Spec: corev1beta1.ShootSpec{
			Provider: corev1beta1.Provider{
				Type:                 "aws",
				InfrastructureConfig: &runtime.RawExtension{Raw: []byte(`{}`)},
				ControlPlaneConfig:   &runtime.RawExtension{Raw: []byte(`{}`)},
				WorkersSettings:      &corev1beta1.WorkersSettings{},
				Workers: []corev1beta1.Worker{
					{
						Name:    "worker-1",
						Minimum: 1,
						Maximum: 3,
						Machine: corev1beta1.Machine{Type: "m5.large"},
						// Fields that should be stripped:
						CABundle:                         ptr.To("cert"),
						Kubernetes:                       &corev1beta1.WorkerKubernetes{},
						MaxSurge:                         &intstr.IntOrString{IntVal: 1},
						MaxUnavailable:                   &intstr.IntOrString{IntVal: 0},
						ProviderConfig:                   &runtime.RawExtension{Raw: []byte(`{}`)},
						Volume:                           &corev1beta1.Volume{VolumeSize: "50Gi"},
						DataVolumes:                      []corev1beta1.DataVolume{{Name: "dv"}},
						KubeletDataVolumeName:            ptr.To("dv"),
						SystemComponents:                 &corev1beta1.WorkerSystemComponents{},
						MachineControllerManagerSettings: &corev1beta1.MachineControllerManagerSettings{},
						Sysctls:                          map[string]string{"vm.max_map_count": "262144"},
						ClusterAutoscaler:                &corev1beta1.ClusterAutoscalerOptions{},
						Priority:                         ptr.To[int32](1),
						UpdateStrategy:                   (*corev1beta1.MachineUpdateStrategy)(ptr.To("Rolling")),
						ControlPlane:                     &corev1beta1.WorkerControlPlane{},
					},
				},
			},
			Networking:        &corev1beta1.Networking{Type: ptr.To("calico")},
			Resources:         []corev1beta1.NamedResourceReference{{Name: "res"}},
			Tolerations:       []corev1beta1.Toleration{{Key: "t"}},
			SystemComponents:  &corev1beta1.SystemComponents{},
			CloudProfile:      &corev1beta1.CloudProfileReference{Name: "cp"},
			CloudProfileName:  ptr.To("cp"), //nolint:staticcheck // SA1019
			ExposureClassName: ptr.To("exposure"),
			SchedulerName:     ptr.To("sched"),
			Kubernetes: corev1beta1.Kubernetes{
				Version: "1.28.0",
				KubeAPIServer: &corev1beta1.KubeAPIServerConfig{
					ServiceAccountConfig:                &corev1beta1.ServiceAccountConfig{},
					Requests:                            &corev1beta1.APIServerRequests{},
					WatchCacheSizes:                     &corev1beta1.WatchCacheSizes{},
					Logging:                             &corev1beta1.APIServerLogging{},
					DefaultNotReadyTolerationSeconds:    ptr.To[int64](300),
					DefaultUnreachableTolerationSeconds: ptr.To[int64](300),
					EventTTL:                            &metav1.Duration{},
					EncryptionConfig:                    &corev1beta1.EncryptionConfig{},
					StructuredAuthentication:            &corev1beta1.StructuredAuthentication{},
					StructuredAuthorization:             &corev1beta1.StructuredAuthorization{},
					EnableAnonymousAuthentication:       ptr.To(false), //nolint:staticcheck // SA1019
					RuntimeConfig:                       map[string]bool{"v1": true},
					APIAudiences:                        []string{"kubernetes"},
				},
			},
		},
		Status: corev1beta1.ShootStatus{
			TechnicalID:                "shoot--dev--my-shoot",
			AdvertisedAddresses:        []corev1beta1.ShootAdvertisedAddress{{Name: "ext"}},
			ClusterIdentity:            ptr.To("id"),
			Credentials:                &corev1beta1.ShootCredentials{},
			ObservedGeneration:         5,
			RetryCycleStartTime:        &metav1.Time{},
			SeedName:                   ptr.To("seed-1"),
			MigrationStartTime:         &metav1.Time{},
			LastHibernationTriggerTime: &metav1.Time{},
			LastMaintenance:            &corev1beta1.LastMaintenance{},
			Networking:                 &corev1beta1.NetworkingStatus{},
			LastOperation: &corev1beta1.LastOperation{
				Type:           corev1beta1.LastOperationTypeReconcile,
				State:          corev1beta1.LastOperationStateProcessing,
				Progress:       42,
				Description:    "long progress description that should be stripped",
				LastUpdateTime: metav1.Now(),
			},
			LastErrors: []corev1beta1.LastError{
				{
					Description: "noisy error description that should be stripped",
					TaskID:      ptr.To("task-1"),
					Codes:       []corev1beta1.ErrorCode{corev1beta1.ErrorInfraUnauthorized},
				},
			},
		},
	}

	result, err := transformShoot(shoot)
	require.NoError(t, err)
	s := result.(*corev1beta1.Shoot)

	// ObjectMeta stripped
	assert.Nil(t, s.ManagedFields)
	assert.Equal(t, map[string]string{"shoot.gardener.cloud/status": "progressing"}, s.Labels)
	assert.Nil(t, s.Annotations)
	assert.Nil(t, s.Finalizers)
	assert.Nil(t, s.OwnerReferences)

	// Provider stripped
	assert.Nil(t, s.Spec.Provider.InfrastructureConfig)
	assert.Nil(t, s.Spec.Provider.ControlPlaneConfig)
	assert.Nil(t, s.Spec.Provider.WorkersSettings)

	// Worker sub-fields stripped
	w := s.Spec.Provider.Workers[0]
	assert.Nil(t, w.CABundle)
	assert.Nil(t, w.Kubernetes)
	assert.Nil(t, w.MaxSurge)
	assert.Nil(t, w.MaxUnavailable)
	assert.Nil(t, w.ProviderConfig)
	assert.Nil(t, w.Volume)
	assert.Nil(t, w.DataVolumes)
	assert.Nil(t, w.KubeletDataVolumeName)
	assert.Nil(t, w.SystemComponents)
	assert.Nil(t, w.MachineControllerManagerSettings)
	assert.Nil(t, w.Sysctls)
	assert.Nil(t, w.ClusterAutoscaler)
	assert.Nil(t, w.Priority)
	assert.Nil(t, w.UpdateStrategy)
	assert.Nil(t, w.ControlPlane)

	// Spec fields stripped
	assert.Nil(t, s.Spec.Networking)
	assert.Nil(t, s.Spec.Resources)
	assert.Nil(t, s.Spec.Tolerations)
	assert.Nil(t, s.Spec.SystemComponents)
	assert.Nil(t, s.Spec.CloudProfile)
	assert.Nil(t, s.Spec.CloudProfileName) //nolint:staticcheck // SA1019
	assert.Nil(t, s.Spec.ExposureClassName)
	assert.Nil(t, s.Spec.SchedulerName)

	// KubeAPIServer fields stripped
	kas := s.Spec.Kubernetes.KubeAPIServer
	assert.Nil(t, kas.ServiceAccountConfig)
	assert.Nil(t, kas.Requests)
	assert.Nil(t, kas.WatchCacheSizes)
	assert.Nil(t, kas.Logging)
	assert.Nil(t, kas.DefaultNotReadyTolerationSeconds)
	assert.Nil(t, kas.DefaultUnreachableTolerationSeconds)
	assert.Nil(t, kas.EventTTL)
	assert.Nil(t, kas.EncryptionConfig)
	assert.Nil(t, kas.StructuredAuthorization)
	assert.Nil(t, kas.EnableAnonymousAuthentication) //nolint:staticcheck // SA1019
	assert.Nil(t, kas.RuntimeConfig)
	assert.Nil(t, kas.APIAudiences)

	// Status fields stripped
	assert.Nil(t, s.Status.AdvertisedAddresses)
	assert.Nil(t, s.Status.ClusterIdentity)
	assert.Nil(t, s.Status.Credentials)
	assert.Equal(t, int64(0), s.Status.ObservedGeneration)
	assert.Nil(t, s.Status.RetryCycleStartTime)
	assert.Nil(t, s.Status.SeedName)
	assert.Nil(t, s.Status.MigrationStartTime)
	assert.Nil(t, s.Status.LastHibernationTriggerTime)
	assert.Nil(t, s.Status.LastMaintenance)
	assert.Nil(t, s.Status.Networking)

	// LastOperation: kept, with verbose fields cleared
	require.NotNil(t, s.Status.LastOperation)
	assert.Equal(t, corev1beta1.LastOperationTypeReconcile, s.Status.LastOperation.Type)
	assert.Equal(t, corev1beta1.LastOperationStateProcessing, s.Status.LastOperation.State)
	assert.Equal(t, int32(42), s.Status.LastOperation.Progress)
	assert.Empty(t, s.Status.LastOperation.Description)
	assert.True(t, s.Status.LastOperation.LastUpdateTime.IsZero())

	// LastErrors: kept (Codes is consumed by hasUserErrors), Description/TaskID cleared
	require.Len(t, s.Status.LastErrors, 1)
	assert.Empty(t, s.Status.LastErrors[0].Description)
	assert.Nil(t, s.Status.LastErrors[0].TaskID)
	assert.Equal(t, []corev1beta1.ErrorCode{corev1beta1.ErrorInfraUnauthorized}, s.Status.LastErrors[0].Codes)
}

func TestTransformShoot_Idempotent(t *testing.T) {
	shoot := &corev1beta1.Shoot{
		ObjectMeta: metav1.ObjectMeta{
			Name:          "my-shoot",
			Namespace:     "garden-dev",
			ManagedFields: []metav1.ManagedFieldsEntry{{Manager: "gardener"}},
		},
		Spec: corev1beta1.ShootSpec{
			Provider:   corev1beta1.Provider{Type: "aws", Workers: []corev1beta1.Worker{{Name: "w"}}},
			Kubernetes: corev1beta1.Kubernetes{Version: "1.28.0"},
		},
	}

	result1, err := transformShoot(shoot)
	require.NoError(t, err)
	result2, err := transformShoot(result1)
	require.NoError(t, err)

	s1 := result1.(*corev1beta1.Shoot)
	s2 := result2.(*corev1beta1.Shoot)
	assert.Equal(t, s1.Name, s2.Name)
	assert.Nil(t, s2.ManagedFields)
}

func TestTransformShoot_NilSafety(t *testing.T) {
	shoot := &corev1beta1.Shoot{
		ObjectMeta: metav1.ObjectMeta{Name: "minimal"},
		Spec: corev1beta1.ShootSpec{
			Provider:   corev1beta1.Provider{Type: "aws"},
			Kubernetes: corev1beta1.Kubernetes{Version: "1.28.0"},
		},
	}

	_, err := transformShoot(shoot)
	require.NoError(t, err)
}

func TestTransformShoot_UnknownType(t *testing.T) {
	obj := "not a shoot"
	result, err := transformShoot(obj)
	require.NoError(t, err)
	assert.Equal(t, "not a shoot", result)
}

func TestTransformSeed_RetainsUsedFields(t *testing.T) {
	seed := &corev1beta1.Seed{
		ObjectMeta: metav1.ObjectMeta{Name: "seed-1", Labels: map[string]string{"unused": "drop-me"}},
		Spec: corev1beta1.SeedSpec{
			Provider: corev1beta1.SeedProvider{
				Type:   "aws",
				Region: "eu-west-1",
			},
			Taints: []corev1beta1.SeedTaint{{Key: "seed.gardener.cloud/protected"}},
			Settings: &corev1beta1.SeedSettings{
				Scheduling: &corev1beta1.SeedSettingScheduling{Visible: true},
			},
		},
		Status: corev1beta1.SeedStatus{
			KubernetesVersion: ptr.To("1.28.0"),
			Capacity:          corev1.ResourceList{"shoots": resource.MustParse("100")},
			Allocatable:       corev1.ResourceList{"shoots": resource.MustParse("50")},
			Conditions:        []corev1beta1.Condition{{Type: "GardenletReady", Status: "True"}},
			LastOperation: &corev1beta1.LastOperation{
				Type:  corev1beta1.LastOperationTypeReconcile,
				State: corev1beta1.LastOperationStateSucceeded,
			},
		},
	}

	result, err := transformSeed(seed)
	require.NoError(t, err)
	s := result.(*corev1beta1.Seed)

	assert.Equal(t, "seed-1", s.Name)
	assert.Nil(t, s.Labels)
	assert.Equal(t, "aws", s.Spec.Provider.Type)
	assert.Equal(t, "eu-west-1", s.Spec.Provider.Region)
	assert.Len(t, s.Spec.Taints, 1)
	assert.NotNil(t, s.Spec.Settings)
	assert.True(t, s.Spec.Settings.Scheduling.Visible)
	assert.NotNil(t, s.Status.KubernetesVersion)
	assert.NotEmpty(t, s.Status.Capacity)
	assert.NotEmpty(t, s.Status.Allocatable)
	assert.Len(t, s.Status.Conditions, 1)
	assert.NotNil(t, s.Status.LastOperation)
}

func TestTransformSeed_StripsUnusedFields(t *testing.T) {
	seed := &corev1beta1.Seed{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "seed-1",
			ManagedFields:   []metav1.ManagedFieldsEntry{{Manager: "g"}},
			Annotations:     map[string]string{"a": "b"},
			Labels:          map[string]string{"l": "v"},
			Finalizers:      []string{"f"},
			OwnerReferences: []metav1.OwnerReference{{Name: "o"}},
		},
		Spec: corev1beta1.SeedSpec{
			Provider: corev1beta1.SeedProvider{
				Type:           "aws",
				Region:         "eu-west-1",
				ProviderConfig: &runtime.RawExtension{Raw: []byte(`{}`)},
				Zones:          []string{"a", "b"},
			},
			Backup:             &corev1beta1.Backup{Provider: "aws"},
			DNS:                corev1beta1.SeedDNS{Provider: &corev1beta1.SeedDNSProvider{Type: "aws-route53"}},
			Networks:           corev1beta1.SeedNetworks{Pods: "10.0.0.0/16"},
			Ingress:            &corev1beta1.Ingress{},
			Volume:             &corev1beta1.SeedVolume{},
			AccessRestrictions: []corev1beta1.AccessRestriction{{Name: "ar"}},
			Extensions:         []corev1beta1.Extension{{Type: "ext"}},
			Resources:          []corev1beta1.NamedResourceReference{{Name: "r"}},
		},
		Status: corev1beta1.SeedStatus{
			Gardener:                             &corev1beta1.Gardener{Version: "1.90.0"},
			ObservedGeneration:                   3,
			ClusterIdentity:                      ptr.To("cid"),
			ClientCertificateExpirationTimestamp: &metav1.Time{},
		},
	}

	result, err := transformSeed(seed)
	require.NoError(t, err)
	s := result.(*corev1beta1.Seed)

	assert.Nil(t, s.ManagedFields)
	assert.Nil(t, s.Annotations)
	assert.Nil(t, s.Labels)
	assert.Nil(t, s.Finalizers)
	assert.Nil(t, s.OwnerReferences)
	assert.Nil(t, s.Spec.Backup)
	assert.Equal(t, corev1beta1.SeedDNS{}, s.Spec.DNS)
	assert.Equal(t, corev1beta1.SeedNetworks{}, s.Spec.Networks)
	assert.Nil(t, s.Spec.Provider.ProviderConfig)
	assert.Nil(t, s.Spec.Provider.Zones)
	assert.Nil(t, s.Spec.Ingress)
	assert.Nil(t, s.Spec.Volume)
	assert.Nil(t, s.Spec.AccessRestrictions)
	assert.Nil(t, s.Spec.Extensions)
	assert.Nil(t, s.Spec.Resources)
	assert.Nil(t, s.Status.Gardener)
	assert.Equal(t, int64(0), s.Status.ObservedGeneration)
	assert.Nil(t, s.Status.ClusterIdentity)
	assert.Nil(t, s.Status.ClientCertificateExpirationTimestamp)
}

func TestTransformProject_RetainsUsedFields(t *testing.T) {
	project := &corev1beta1.Project{
		ObjectMeta: metav1.ObjectMeta{
			Name: "my-project",
			Annotations: map[string]string{
				"billing.gardener.cloud/costObject":     "CO123",
				"billing.gardener.cloud/costObjectType": "IO",
				"unused":                                "drop-me",
			},
			Labels: map[string]string{"unused": "drop-me"},
		},
		Spec: corev1beta1.ProjectSpec{
			Namespace: ptr.To("garden-my-project"),
			Owner:     &rbacv1.Subject{Kind: "User", Name: "owner@example.com"},
			Members: []corev1beta1.ProjectMember{
				{Subject: rbacv1.Subject{Kind: "User", Name: "user1", APIGroup: "rbac.authorization.k8s.io"}, Role: "admin", Roles: []string{"admin"}},
				{Subject: rbacv1.Subject{Kind: "ServiceAccount", Name: "sa1", Namespace: "ns1"}, Role: "viewer"},
			},
		},
		Status: corev1beta1.ProjectStatus{
			Phase: corev1beta1.ProjectReady,
		},
	}

	result, err := transformProject(project)
	require.NoError(t, err)
	p := result.(*corev1beta1.Project)

	assert.Equal(t, "my-project", p.Name)
	assert.Equal(t, map[string]string{
		"billing.gardener.cloud/costObject":     "CO123",
		"billing.gardener.cloud/costObjectType": "IO",
	}, p.Annotations)
	assert.Nil(t, p.Labels)
	assert.Equal(t, ptr.To("garden-my-project"), p.Spec.Namespace)
	assert.Equal(t, "owner@example.com", p.Spec.Owner.Name)
	assert.Len(t, p.Spec.Members, 2)
	assert.Equal(t, "User", p.Spec.Members[0].Kind)
	assert.Equal(t, "ServiceAccount", p.Spec.Members[1].Kind)
	assert.Equal(t, corev1beta1.ProjectReady, p.Status.Phase)
}

func TestTransformProject_StripsUnusedFields(t *testing.T) {
	project := &corev1beta1.Project{
		ObjectMeta: metav1.ObjectMeta{
			Name:          "my-project",
			ManagedFields: []metav1.ManagedFieldsEntry{{Manager: "g"}},
			Annotations: map[string]string{
				"billing.gardener.cloud/costObject":     "CO123",
				"billing.gardener.cloud/costObjectType": "IO",
				"unused":                                "drop-me",
			},
			Labels:          map[string]string{"l": "v"},
			Finalizers:      []string{"f"},
			OwnerReferences: []metav1.OwnerReference{{Name: "o"}},
		},
		Spec: corev1beta1.ProjectSpec{
			Namespace:   ptr.To("garden-my-project"),
			Description: ptr.To("desc"),
			Purpose:     ptr.To("purpose"),
			CreatedBy:   &rbacv1.Subject{Kind: "User", Name: "c"},
			Tolerations: &corev1beta1.ProjectTolerations{},
			Members: []corev1beta1.ProjectMember{
				{Subject: rbacv1.Subject{Kind: "User", Name: "user1", APIGroup: "rbac", Namespace: "ns"}, Role: "admin", Roles: []string{"admin"}},
			},
		},
		Status: corev1beta1.ProjectStatus{
			Phase:                    corev1beta1.ProjectReady,
			ObservedGeneration:       5,
			StaleSinceTimestamp:      &metav1.Time{},
			StaleAutoDeleteTimestamp: &metav1.Time{},
			LastActivityTimestamp:    &metav1.Time{},
		},
	}

	result, err := transformProject(project)
	require.NoError(t, err)
	p := result.(*corev1beta1.Project)

	assert.Nil(t, p.ManagedFields)
	assert.Equal(t, map[string]string{
		"billing.gardener.cloud/costObject":     "CO123",
		"billing.gardener.cloud/costObjectType": "IO",
	}, p.Annotations)
	assert.Nil(t, p.Labels)
	assert.Nil(t, p.Finalizers)
	assert.Nil(t, p.OwnerReferences)
	assert.Nil(t, p.Spec.Description)
	assert.Nil(t, p.Spec.Purpose)
	assert.Nil(t, p.Spec.CreatedBy)
	assert.Nil(t, p.Spec.Tolerations)

	// Members: only Subject.Kind retained; everything else zeroed.
	assert.Equal(t, "User", p.Spec.Members[0].Kind)
	assert.Empty(t, p.Spec.Members[0].Name)
	assert.Empty(t, p.Spec.Members[0].APIGroup)
	assert.Empty(t, p.Spec.Members[0].Namespace)
	assert.Empty(t, p.Spec.Members[0].Role)
	assert.Nil(t, p.Spec.Members[0].Roles)

	assert.Equal(t, int64(0), p.Status.ObservedGeneration)
	assert.Nil(t, p.Status.StaleSinceTimestamp)
	assert.Nil(t, p.Status.StaleAutoDeleteTimestamp)
	assert.Nil(t, p.Status.LastActivityTimestamp)
}

func TestTransformSecretBinding_RetainsAndStrips(t *testing.T) {
	sb := &corev1beta1.SecretBinding{ //nolint:staticcheck // SA1019
		ObjectMeta: metav1.ObjectMeta{
			Name:          "sb-1",
			Namespace:     "garden-dev",
			ManagedFields: []metav1.ManagedFieldsEntry{{Manager: "g"}},
			Labels:        map[string]string{"l": "v"},
			Annotations:   map[string]string{"a": "b"},
			Finalizers:    []string{"f"},
		},
		SecretRef: corev1.SecretReference{
			Name:      "secret-1",
			Namespace: "garden-billing",
		},
		Provider: &corev1beta1.SecretBindingProvider{Type: "aws"}, //nolint:staticcheck // SA1019
		Quotas:   []corev1.ObjectReference{{Name: "q"}},
	}

	result, err := transformSecretBinding(sb)
	require.NoError(t, err)
	s := result.(*corev1beta1.SecretBinding) //nolint:staticcheck // SA1019

	assert.Equal(t, "sb-1", s.Name)
	assert.Equal(t, "garden-dev", s.Namespace)
	assert.Equal(t, "garden-billing", s.SecretRef.Namespace)
	assert.Nil(t, s.ManagedFields)
	assert.Nil(t, s.Labels)
	assert.Nil(t, s.Annotations)
	assert.Nil(t, s.Finalizers)
	assert.Nil(t, s.Provider)
	assert.Nil(t, s.Quotas)
}

func TestTransformCredentialsBinding_RetainsAndStrips(t *testing.T) {
	cb := &securityv1alpha1.CredentialsBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:          "cb-1",
			Namespace:     "garden-dev",
			ManagedFields: []metav1.ManagedFieldsEntry{{Manager: "g"}},
			Labels:        map[string]string{"l": "v"},
			Annotations:   map[string]string{"a": "b"},
			Finalizers:    []string{"f"},
		},
		CredentialsRef: corev1.ObjectReference{
			Name:      "cred-1",
			Namespace: "garden-billing",
		},
		Provider: securityv1alpha1.CredentialsBindingProvider{Type: "aws"},
		Quotas:   []corev1.ObjectReference{{Name: "q"}},
	}

	result, err := transformCredentialsBinding(cb)
	require.NoError(t, err)
	c := result.(*securityv1alpha1.CredentialsBinding)

	assert.Equal(t, "cb-1", c.Name)
	assert.Equal(t, "garden-dev", c.Namespace)
	assert.Equal(t, "garden-billing", c.CredentialsRef.Namespace)
	assert.Nil(t, c.ManagedFields)
	assert.Nil(t, c.Labels)
	assert.Nil(t, c.Annotations)
	assert.Nil(t, c.Finalizers)
	assert.Equal(t, securityv1alpha1.CredentialsBindingProvider{}, c.Provider)
	assert.Nil(t, c.Quotas)
}

func TestTransformManagedSeed_RetainsAndStrips(t *testing.T) {
	ms := &seedmanagementv1alpha1.ManagedSeed{
		ObjectMeta: metav1.ObjectMeta{
			Name:          "ms-1",
			ManagedFields: []metav1.ManagedFieldsEntry{{Manager: "g"}},
			Labels:        map[string]string{"l": "v"},
			Annotations:   map[string]string{"a": "b"},
			Finalizers:    []string{"f"},
		},
		Spec: seedmanagementv1alpha1.ManagedSeedSpec{
			Shoot:     &seedmanagementv1alpha1.Shoot{Name: "my-shoot"},
			Gardenlet: seedmanagementv1alpha1.GardenletConfig{Config: runtime.RawExtension{Raw: []byte(`{}`)}},
		},
		Status: seedmanagementv1alpha1.ManagedSeedStatus{
			ObservedGeneration: 3,
		},
	}

	result, err := transformManagedSeed(ms)
	require.NoError(t, err)
	m := result.(*seedmanagementv1alpha1.ManagedSeed)

	assert.Equal(t, "ms-1", m.Name)
	assert.Equal(t, "my-shoot", m.Spec.Shoot.Name)
	assert.Nil(t, m.ManagedFields)
	assert.Nil(t, m.Labels)
	assert.Nil(t, m.Annotations)
	assert.Nil(t, m.Finalizers)
	assert.Equal(t, seedmanagementv1alpha1.GardenletConfig{}, m.Spec.Gardenlet)
	assert.Equal(t, seedmanagementv1alpha1.ManagedSeedStatus{}, m.Status)
}

func TestTransformGardenlet_RetainsAndStrips(t *testing.T) {
	gl := &seedmanagementv1alpha1.Gardenlet{
		ObjectMeta: metav1.ObjectMeta{
			Name:          "gl-1",
			Generation:    7,
			ManagedFields: []metav1.ManagedFieldsEntry{{Manager: "g"}},
			Labels:        map[string]string{"l": "v"},
			Annotations:   map[string]string{"a": "b"},
			Finalizers:    []string{"f"},
		},
		Spec: seedmanagementv1alpha1.GardenletSpec{
			Config: runtime.RawExtension{Raw: []byte(`{}`)},
		},
		Status: seedmanagementv1alpha1.GardenletStatus{
			ObservedGeneration: 6,
			Conditions:         []corev1beta1.Condition{{Type: "Reconciled", Status: "True"}},
		},
	}

	result, err := transformGardenlet(gl)
	require.NoError(t, err)
	g := result.(*seedmanagementv1alpha1.Gardenlet)

	assert.Equal(t, "gl-1", g.Name)
	assert.Equal(t, int64(7), g.Generation)
	assert.Equal(t, int64(6), g.Status.ObservedGeneration)
	assert.Len(t, g.Status.Conditions, 1)
	assert.Nil(t, g.ManagedFields)
	assert.Nil(t, g.Labels)
	assert.Nil(t, g.Annotations)
	assert.Nil(t, g.Finalizers)
	assert.Equal(t, seedmanagementv1alpha1.GardenletSpec{}, g.Spec)
}

func TestTransformCoreClusterScoped_DispatchesSeed(t *testing.T) {
	seed := &corev1beta1.Seed{
		ObjectMeta: metav1.ObjectMeta{
			Name:          "seed-1",
			ManagedFields: []metav1.ManagedFieldsEntry{{Manager: "g"}},
		},
		Spec: corev1beta1.SeedSpec{
			Provider: corev1beta1.SeedProvider{Type: "aws", Region: "eu-west-1"},
		},
	}

	result, err := transformCoreClusterScoped(seed)
	require.NoError(t, err)
	s := result.(*corev1beta1.Seed)
	assert.Equal(t, "seed-1", s.Name)
	assert.Equal(t, "aws", s.Spec.Provider.Type)
	assert.Nil(t, s.ManagedFields)
}

func TestTransformCoreClusterScoped_DispatchesProject(t *testing.T) {
	project := &corev1beta1.Project{
		ObjectMeta: metav1.ObjectMeta{
			Name:          "proj-1",
			ManagedFields: []metav1.ManagedFieldsEntry{{Manager: "g"}},
		},
	}

	result, err := transformCoreClusterScoped(project)
	require.NoError(t, err)
	p := result.(*corev1beta1.Project)
	assert.Equal(t, "proj-1", p.Name)
	assert.Nil(t, p.ManagedFields)
}

func TestTransformCoreClusterScoped_PassthroughUnknown(t *testing.T) {
	obj := "unknown"
	result, err := transformCoreClusterScoped(obj)
	require.NoError(t, err)
	assert.Equal(t, "unknown", result)
}

func TestTransformSeedManagement_DispatchesManagedSeed(t *testing.T) {
	ms := &seedmanagementv1alpha1.ManagedSeed{
		ObjectMeta: metav1.ObjectMeta{
			Name:          "ms-1",
			ManagedFields: []metav1.ManagedFieldsEntry{{Manager: "g"}},
		},
		Spec: seedmanagementv1alpha1.ManagedSeedSpec{
			Shoot: &seedmanagementv1alpha1.Shoot{Name: "s"},
		},
	}

	result, err := transformSeedManagement(ms)
	require.NoError(t, err)
	m := result.(*seedmanagementv1alpha1.ManagedSeed)
	assert.Equal(t, "ms-1", m.Name)
	assert.Nil(t, m.ManagedFields)
}

func TestTransformSeedManagement_DispatchesGardenlet(t *testing.T) {
	gl := &seedmanagementv1alpha1.Gardenlet{
		ObjectMeta: metav1.ObjectMeta{
			Name:          "gl-1",
			Generation:    5,
			ManagedFields: []metav1.ManagedFieldsEntry{{Manager: "g"}},
		},
		Status: seedmanagementv1alpha1.GardenletStatus{ObservedGeneration: 4},
	}

	result, err := transformSeedManagement(gl)
	require.NoError(t, err)
	g := result.(*seedmanagementv1alpha1.Gardenlet)
	assert.Equal(t, "gl-1", g.Name)
	assert.Equal(t, int64(5), g.Generation)
	assert.Nil(t, g.ManagedFields)
}

func TestTransformSeedManagement_PassthroughUnknown(t *testing.T) {
	obj := 42
	result, err := transformSeedManagement(obj)
	require.NoError(t, err)
	assert.Equal(t, 42, result)
}
