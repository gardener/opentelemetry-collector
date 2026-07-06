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
// inserted into the informer cache.
//
// Each transform constructs a fresh object and copies only the retained
// fields. The original is dropped on return — its sub-objects become
// unreachable and are reclaimed by the next GC cycle. This whitelist style
// makes the contract explicit: a new field upstream is excluded by default
// rather than silently retained.

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

// retainObjectMeta produces a new ObjectMeta carrying only the fields that
// downstream metric collectors read. ManagedFields, Annotations, Finalizers,
// OwnerReferences and Labels are intentionally dropped; callers that need
// (filtered) Labels or Annotations populate them themselves.
func retainObjectMeta(meta metav1.ObjectMeta) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name:              meta.Name,
		Namespace:         meta.Namespace,
		UID:               meta.UID,
		ResourceVersion:   meta.ResourceVersion,
		Generation:        meta.Generation,
		CreationTimestamp: meta.CreationTimestamp,
	}
}

func transformShoot(obj any) (any, error) {
	src, ok := obj.(*corev1beta1.Shoot)
	if !ok {
		return obj, nil
	}

	dst := &corev1beta1.Shoot{
		TypeMeta:   src.TypeMeta,
		ObjectMeta: retainObjectMeta(src.ObjectMeta),
	}
	dst.Labels = retainStringMapKeys(src.Labels, corev1beta1constants.ShootStatus)

	dst.Spec = corev1beta1.ShootSpec{
		ControlPlane:           src.Spec.ControlPlane,
		CredentialsBindingName: src.Spec.CredentialsBindingName,
		DNS:                    src.Spec.DNS,
		Extensions:             src.Spec.Extensions,
		Hibernation:            src.Spec.Hibernation,
		Kubernetes:             retainShootKubernetes(src.Spec.Kubernetes),
		Maintenance:            src.Spec.Maintenance,
		Provider:               retainShootProvider(src.Spec.Provider),
		Purpose:                src.Spec.Purpose,
		Region:                 src.Spec.Region,
		SecretBindingName:      src.Spec.SecretBindingName, //nolint:staticcheck // SA1019
		SeedName:               src.Spec.SeedName,
	}

	dst.Status = corev1beta1.ShootStatus{
		Conditions:    src.Status.Conditions,
		Constraints:   src.Status.Constraints,
		Gardener:      src.Status.Gardener,
		IsHibernated:  src.Status.IsHibernated,
		LastErrors:    retainLastErrors(src.Status.LastErrors),
		LastOperation: retainLastOperation(src.Status.LastOperation),
		TechnicalID:   src.Status.TechnicalID,
		UID:           src.Status.UID,
	}

	return dst, nil
}

// retainShootProvider keeps Type, Workers (selectively), and drops the heavy
// InfrastructureConfig / ControlPlaneConfig / WorkersSettings raw extensions.
func retainShootProvider(src corev1beta1.Provider) corev1beta1.Provider {
	dst := corev1beta1.Provider{
		Type: src.Type,
	}
	if src.Workers == nil {
		return dst
	}

	dst.Workers = make([]corev1beta1.Worker, len(src.Workers))
	for i := range src.Workers {
		s := &src.Workers[i]
		dst.Workers[i] = corev1beta1.Worker{
			Annotations: s.Annotations,
			CRI:         s.CRI,
			Labels:      s.Labels,
			Machine:     s.Machine,
			Maximum:     s.Maximum,
			Minimum:     s.Minimum,
			Name:        s.Name,
			Taints:      s.Taints,
			Zones:       s.Zones,
		}
	}
	return dst
}

// retainShootKubernetes preserves the version and small per-component config
// blocks, but trims KubeAPIServer to the fields that drive metrics
// (admission plugins, audit policy presence, OIDC presence, feature gates
// inside KubernetesConfig).
func retainShootKubernetes(src corev1beta1.Kubernetes) corev1beta1.Kubernetes {
	dst := corev1beta1.Kubernetes{
		Version:               src.Version,
		KubeControllerManager: src.KubeControllerManager,
		KubeScheduler:         src.KubeScheduler,
		Kubelet:               src.Kubelet,
		KubeProxy:             src.KubeProxy,
	}

	if src.KubeAPIServer != nil {
		s := src.KubeAPIServer
		dst.KubeAPIServer = &corev1beta1.KubeAPIServerConfig{
			KubernetesConfig:         s.KubernetesConfig,
			AdmissionPlugins:         s.AdmissionPlugins,
			AuditConfig:              s.AuditConfig,
			StructuredAuthentication: s.StructuredAuthentication,
		}
	}

	return dst
}

// retainLastOperation keeps Type/State/Progress for metric reporting and drops
// the verbose Description and LastUpdateTime that bloat the cache.
func retainLastOperation(src *corev1beta1.LastOperation) *corev1beta1.LastOperation {
	if src == nil {
		return nil
	}
	return &corev1beta1.LastOperation{
		Type:     src.Type,
		State:    src.State,
		Progress: src.Progress,
	}
}

// retainLastErrors keeps only Codes — the only field hasUserErrors consumes.
func retainLastErrors(src []corev1beta1.LastError) []corev1beta1.LastError {
	if src == nil {
		return nil
	}
	dst := make([]corev1beta1.LastError, len(src))
	for i := range src {
		dst[i] = corev1beta1.LastError{
			Codes: src[i].Codes,
		}
	}
	return dst
}

func transformSeed(obj any) (any, error) {
	src, ok := obj.(*corev1beta1.Seed)
	if !ok {
		return obj, nil
	}

	dst := &corev1beta1.Seed{
		TypeMeta:   src.TypeMeta,
		ObjectMeta: retainObjectMeta(src.ObjectMeta),
		Spec: corev1beta1.SeedSpec{
			Provider: corev1beta1.SeedProvider{
				Type:   src.Spec.Provider.Type,
				Region: src.Spec.Provider.Region,
			},
			Settings: src.Spec.Settings,
			Taints:   src.Spec.Taints,
		},
		Status: corev1beta1.SeedStatus{
			Allocatable:       src.Status.Allocatable,
			Capacity:          src.Status.Capacity,
			Conditions:        src.Status.Conditions,
			KubernetesVersion: src.Status.KubernetesVersion,
			LastOperation:     src.Status.LastOperation,
		},
	}
	return dst, nil
}

func transformProject(obj any) (any, error) {
	src, ok := obj.(*corev1beta1.Project)
	if !ok {
		return obj, nil
	}

	dst := &corev1beta1.Project{
		TypeMeta:   src.TypeMeta,
		ObjectMeta: retainObjectMeta(src.ObjectMeta),
		Spec: corev1beta1.ProjectSpec{
			Namespace: src.Spec.Namespace,
			Owner:     src.Spec.Owner,
		},
		Status: corev1beta1.ProjectStatus{
			Phase: src.Status.Phase,
		},
	}
	dst.Annotations = retainStringMapKeys(src.Annotations,
		projectAnnotationCostObject, projectAnnotationCostObjectType)

	if src.Spec.Members != nil {
		dst.Spec.Members = make([]corev1beta1.ProjectMember, len(src.Spec.Members))
		for i := range src.Spec.Members {
			dst.Spec.Members[i] = corev1beta1.ProjectMember{
				Subject: rbacv1.Subject{Kind: src.Spec.Members[i].Kind},
			}
		}
	}

	return dst, nil
}

func transformSecretBinding(obj any) (any, error) {
	src, ok := obj.(*corev1beta1.SecretBinding) //nolint:staticcheck // SA1019
	if !ok {
		return obj, nil
	}

	return &corev1beta1.SecretBinding{ //nolint:staticcheck // SA1019
		TypeMeta:   src.TypeMeta,
		ObjectMeta: retainObjectMeta(src.ObjectMeta),
		SecretRef:  src.SecretRef,
	}, nil
}

func transformCredentialsBinding(obj any) (any, error) {
	src, ok := obj.(*securityv1alpha1.CredentialsBinding)
	if !ok {
		return obj, nil
	}

	return &securityv1alpha1.CredentialsBinding{
		TypeMeta:       src.TypeMeta,
		ObjectMeta:     retainObjectMeta(src.ObjectMeta),
		CredentialsRef: src.CredentialsRef,
	}, nil
}

func transformManagedSeed(obj any) (any, error) {
	src, ok := obj.(*seedmanagementv1alpha1.ManagedSeed)
	if !ok {
		return obj, nil
	}

	return &seedmanagementv1alpha1.ManagedSeed{
		TypeMeta:   src.TypeMeta,
		ObjectMeta: retainObjectMeta(src.ObjectMeta),
		Spec: seedmanagementv1alpha1.ManagedSeedSpec{
			Shoot: src.Spec.Shoot,
		},
	}, nil
}

func transformGardenlet(obj any) (any, error) {
	src, ok := obj.(*seedmanagementv1alpha1.Gardenlet)
	if !ok {
		return obj, nil
	}

	return &seedmanagementv1alpha1.Gardenlet{
		TypeMeta:   src.TypeMeta,
		ObjectMeta: retainObjectMeta(src.ObjectMeta),
		Status: seedmanagementv1alpha1.GardenletStatus{
			Conditions:         src.Status.Conditions,
			ObservedGeneration: src.Status.ObservedGeneration,
		},
	}, nil
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
