// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package gardenerreceiver

import (
	corev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	corev1beta1constants "github.com/gardener/gardener/pkg/apis/core/v1beta1/constants"
	securityv1alpha1 "github.com/gardener/gardener/pkg/apis/security/v1alpha1"
	seedmanagementv1alpha1 "github.com/gardener/gardener/pkg/apis/seedmanagement/v1alpha1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	projectAnnotationCostObject     = "billing.gardener.cloud/costObject"
	projectAnnotationCostObjectType = "billing.gardener.cloud/costObjectType"
)

// The transform functions below are registered via SharedInformerFactory's
// WithTransform option and are invoked exactly once per object, before it is
// inserted into the informer cache. That contract makes in-place mutation safe:
// no consumer can observe the object until the transform returns.

func retainStringMapKeys(m map[string]string, keys ...string) map[string]string {
	if len(m) == 0 {
		return nil
	}

	retained := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := m[key]; ok {
			retained[key] = value
		}
	}
	if len(retained) == 0 {
		return nil
	}
	return retained
}

func transformShoot(obj any) (any, error) {
	shoot, ok := obj.(*corev1beta1.Shoot)
	if !ok {
		return obj, nil
	}

	shoot.ManagedFields = nil
	shoot.Labels = retainStringMapKeys(shoot.Labels, corev1beta1constants.ShootStatus)
	shoot.Annotations = nil
	shoot.Finalizers = nil
	shoot.OwnerReferences = nil

	// Spec.Provider: keep Type, Region, Workers (partially)
	shoot.Spec.Provider.InfrastructureConfig = nil
	shoot.Spec.Provider.ControlPlaneConfig = nil
	shoot.Spec.Provider.WorkersSettings = nil

	for i := range shoot.Spec.Provider.Workers {
		w := &shoot.Spec.Provider.Workers[i]
		w.CABundle = nil
		w.Kubernetes = nil
		w.MaxSurge = nil
		w.MaxUnavailable = nil
		w.ProviderConfig = nil
		w.Volume = nil
		w.DataVolumes = nil
		w.KubeletDataVolumeName = nil
		w.SystemComponents = nil
		w.MachineControllerManagerSettings = nil
		w.Sysctls = nil
		w.ClusterAutoscaler = nil
		w.Priority = nil
		w.UpdateStrategy = nil
		w.ControlPlane = nil
	}

	shoot.Spec.Networking = nil
	shoot.Spec.Resources = nil
	shoot.Spec.Tolerations = nil
	shoot.Spec.SystemComponents = nil
	shoot.Spec.CloudProfile = nil
	shoot.Spec.CloudProfileName = nil //nolint:staticcheck // SA1019
	shoot.Spec.ExposureClassName = nil
	shoot.Spec.SchedulerName = nil

	if shoot.Spec.Kubernetes.KubeAPIServer != nil {
		kas := shoot.Spec.Kubernetes.KubeAPIServer
		kas.ServiceAccountConfig = nil
		kas.Requests = nil
		kas.WatchCacheSizes = nil
		kas.Logging = nil
		kas.DefaultNotReadyTolerationSeconds = nil
		kas.DefaultUnreachableTolerationSeconds = nil
		kas.EventTTL = nil
		kas.EncryptionConfig = nil
		kas.StructuredAuthentication = nil
		kas.StructuredAuthorization = nil
		kas.Autoscaling = nil
		kas.EnableAnonymousAuthentication = nil //nolint:staticcheck // SA1019
		kas.RuntimeConfig = nil
		kas.APIAudiences = nil
	}

	shoot.Status.AdvertisedAddresses = nil
	shoot.Status.ClusterIdentity = nil
	shoot.Status.Credentials = nil
	shoot.Status.EncryptedResources = nil //nolint:staticcheck // SA1019
	shoot.Status.ObservedGeneration = 0
	shoot.Status.RetryCycleStartTime = nil
	shoot.Status.SeedName = nil
	shoot.Status.MigrationStartTime = nil
	shoot.Status.LastHibernationTriggerTime = nil
	shoot.Status.LastMaintenance = nil
	shoot.Status.Networking = nil

	if shoot.Status.LastOperation != nil {
		shoot.Status.LastOperation.Description = ""
		shoot.Status.LastOperation.LastUpdateTime = metav1.Time{}
	}
	for i := range shoot.Status.LastErrors {
		shoot.Status.LastErrors[i].Description = ""
		shoot.Status.LastErrors[i].TaskID = nil
	}

	return shoot, nil
}

func transformSeed(obj any) (any, error) {
	seed, ok := obj.(*corev1beta1.Seed)
	if !ok {
		return obj, nil
	}

	seed.ManagedFields = nil
	seed.Labels = nil
	seed.Annotations = nil
	seed.Finalizers = nil
	seed.OwnerReferences = nil

	seed.Spec.Backup = nil
	seed.Spec.DNS = corev1beta1.SeedDNS{}
	seed.Spec.Networks = corev1beta1.SeedNetworks{}
	seed.Spec.Provider.ProviderConfig = nil
	seed.Spec.Provider.Zones = nil
	seed.Spec.Ingress = nil
	seed.Spec.Volume = nil
	seed.Spec.AccessRestrictions = nil
	seed.Spec.Extensions = nil
	seed.Spec.Resources = nil

	seed.Status.Gardener = nil
	seed.Status.ObservedGeneration = 0
	seed.Status.ClusterIdentity = nil
	seed.Status.ClientCertificateExpirationTimestamp = nil

	return seed, nil
}

func transformProject(obj any) (any, error) {
	project, ok := obj.(*corev1beta1.Project)
	if !ok {
		return obj, nil
	}

	project.ManagedFields = nil
	project.Labels = nil
	project.Annotations = retainStringMapKeys(project.Annotations, projectAnnotationCostObject, projectAnnotationCostObjectType)
	project.Finalizers = nil
	project.OwnerReferences = nil

	project.Spec.Description = nil
	project.Spec.Purpose = nil
	project.Spec.CreatedBy = nil
	project.Spec.Tolerations = nil

	for i := range project.Spec.Members {
		project.Spec.Members[i] = corev1beta1.ProjectMember{
			Subject: rbacv1.Subject{Kind: project.Spec.Members[i].Kind},
		}
	}

	project.Status.ObservedGeneration = 0
	project.Status.StaleSinceTimestamp = nil
	project.Status.StaleAutoDeleteTimestamp = nil
	project.Status.LastActivityTimestamp = nil

	return project, nil
}

func transformSecretBinding(obj any) (any, error) {
	sb, ok := obj.(*corev1beta1.SecretBinding) //nolint:staticcheck // SA1019
	if !ok {
		return obj, nil
	}

	sb.ManagedFields = nil
	sb.Labels = nil
	sb.Annotations = nil
	sb.Finalizers = nil
	sb.OwnerReferences = nil
	sb.Provider = nil
	sb.Quotas = nil

	return sb, nil
}

func transformCredentialsBinding(obj any) (any, error) {
	cb, ok := obj.(*securityv1alpha1.CredentialsBinding)
	if !ok {
		return obj, nil
	}

	cb.ManagedFields = nil
	cb.Labels = nil
	cb.Annotations = nil
	cb.Finalizers = nil
	cb.OwnerReferences = nil
	cb.Provider = securityv1alpha1.CredentialsBindingProvider{}
	cb.Quotas = nil

	return cb, nil
}

func transformManagedSeed(obj any) (any, error) {
	ms, ok := obj.(*seedmanagementv1alpha1.ManagedSeed)
	if !ok {
		return obj, nil
	}

	ms.ManagedFields = nil
	ms.Labels = nil
	ms.Annotations = nil
	ms.Finalizers = nil
	ms.OwnerReferences = nil
	ms.Spec.Gardenlet = seedmanagementv1alpha1.GardenletConfig{}
	ms.Status = seedmanagementv1alpha1.ManagedSeedStatus{}

	return ms, nil
}

func transformGardenlet(obj any) (any, error) {
	gl, ok := obj.(*seedmanagementv1alpha1.Gardenlet)
	if !ok {
		return obj, nil
	}

	gl.ManagedFields = nil
	gl.Labels = nil
	gl.Annotations = nil
	gl.Finalizers = nil
	gl.OwnerReferences = nil
	gl.Spec = seedmanagementv1alpha1.GardenletSpec{}

	return gl, nil
}

func transformCoreClusterScoped(obj any) (any, error) {
	switch obj.(type) {
	case *corev1beta1.Seed:
		return transformSeed(obj)
	case *corev1beta1.Project:
		return transformProject(obj)
	default:
		return obj, nil
	}
}

func transformSeedManagement(obj any) (any, error) {
	switch obj.(type) {
	case *seedmanagementv1alpha1.ManagedSeed:
		return transformManagedSeed(obj)
	case *seedmanagementv1alpha1.Gardenlet:
		return transformGardenlet(obj)
	default:
		return obj, nil
	}
}
