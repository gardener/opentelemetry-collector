// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package gardenerreceiver

import (
	"fmt"
	"sort"
	"strings"

	corev1beta1 "github.com/gardener/gardener/pkg/apis/core/v1beta1"
	corev1beta1constants "github.com/gardener/gardener/pkg/apis/core/v1beta1/constants"
	securityv1alpha1 "github.com/gardener/gardener/pkg/apis/security/v1alpha1"
	seedmanagementv1alpha1 "github.com/gardener/gardener/pkg/apis/seedmanagement/v1alpha1"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"k8s.io/utils/ptr"
)

// userErrorCodes is the set of ErrorCode values that indicate user-caused errors,
// mirroring the logic in gardener-metrics-exporter.
var userErrorCodes = map[corev1beta1.ErrorCode]struct{}{
	corev1beta1.ErrorInfraUnauthorized:             {},
	corev1beta1.ErrorInfraQuotaExceeded:            {},
	corev1beta1.ErrorConfigurationProblem:          {},
	corev1beta1.ErrorInfraUnauthenticated:          {},
	corev1beta1.ErrorInfraDependencies:             {},
	corev1beta1.ErrorRetryableConfigurationProblem: {},
	corev1beta1.ErrorProblematicWebhook:            {},
}

// shootLookups holds cross-informer lookup maps built once per collection cycle
// and shared across the shoot metric collectors that need them.
type shootLookups struct {
	// managedSeedShoots is the set of shoot names that are used as ManagedSeeds.
	managedSeedShoots map[string]struct{}
	// seedByName maps seed name to seed for iaas/region lookups.
	seedByName map[string]*corev1beta1.Seed
	// projectByNamespace maps shoot namespace to billing/owner info from the Project.
	projectByNamespace map[string]projectBillingInfo
	// secretBindingRefNS maps "namespace/name" to the SecretRef.Namespace of a SecretBinding.
	secretBindingRefNS map[string]string
	// credentialsBindingRefNS maps "namespace/name" to the CredentialsRef.Namespace of a CredentialsBinding.
	credentialsBindingRefNS map[string]string
}

type projectBillingInfo struct {
	costObject      string
	costObjectType  string
	costObjectOwner string
}

// buildShootLookups constructs the cross-informer lookup maps once per collection cycle.
func (r *gardenerReceiver) buildShootLookups() shootLookups {
	l := shootLookups{
		managedSeedShoots:       map[string]struct{}{},
		seedByName:              map[string]*corev1beta1.Seed{},
		projectByNamespace:      map[string]projectBillingInfo{},
		secretBindingRefNS:      map[string]string{},
		credentialsBindingRefNS: map[string]string{},
	}

	if r.managedSeedInformer != nil {
		for _, item := range r.managedSeedInformer.GetStore().List() {
			ms := item.(*seedmanagementv1alpha1.ManagedSeed)
			if ms.Spec.Shoot != nil && ms.Spec.Shoot.Name != "" {
				l.managedSeedShoots[ms.Spec.Shoot.Name] = struct{}{}
			}
		}
	}

	if r.seedInformer != nil {
		for _, item := range r.seedInformer.GetStore().List() {
			seed := item.(*corev1beta1.Seed)
			l.seedByName[seed.Name] = seed
		}
	}

	if r.projectInformer != nil {
		for _, item := range r.projectInformer.GetStore().List() {
			proj := item.(*corev1beta1.Project)
			if proj.Spec.Namespace == nil {
				continue
			}
			pi := projectBillingInfo{
				costObject:     proj.Annotations["billing.gardener.cloud/costObject"],
				costObjectType: proj.Annotations["billing.gardener.cloud/costObjectType"],
			}
			if proj.Spec.Owner != nil {
				pi.costObjectOwner = proj.Spec.Owner.Name
			}
			l.projectByNamespace[*proj.Spec.Namespace] = pi
		}
	}

	if r.secretBindingInformer != nil {
		for _, item := range r.secretBindingInformer.GetStore().List() {
			sb := item.(*corev1beta1.SecretBinding) //nolint:staticcheck // SA1019
			key := fmt.Sprintf("%s/%s", sb.Namespace, sb.Name)
			l.secretBindingRefNS[key] = sb.SecretRef.Namespace
		}
	}

	if r.credentialsBindingInformer != nil {
		for _, item := range r.credentialsBindingInformer.GetStore().List() {
			cb := item.(*securityv1alpha1.CredentialsBinding)
			key := fmt.Sprintf("%s/%s", cb.Namespace, cb.Name)
			l.credentialsBindingRefNS[key] = cb.CredentialsRef.Namespace
		}
	}

	return l
}

// resolveBillingInfo resolves the billing project for a shoot by following the
// credentials binding indirection. A shoot's billing info comes from the project
// that owns the referenced credentials, not necessarily the shoot's own project.
func (l *shootLookups) resolveBillingInfo(shoot *corev1beta1.Shoot) projectBillingInfo {
	projectNS := shoot.Namespace

	if shoot.Spec.CredentialsBindingName != nil {
		key := fmt.Sprintf("%s/%s", shoot.Namespace, *shoot.Spec.CredentialsBindingName)
		if ns, ok := l.credentialsBindingRefNS[key]; ok {
			projectNS = ns
		}
	} else if shoot.Spec.SecretBindingName != nil { //nolint:staticcheck // SA1019
		key := fmt.Sprintf("%s/%s", shoot.Namespace, *shoot.Spec.SecretBindingName) //nolint:staticcheck // SA1019
		if ns, ok := l.secretBindingRefNS[key]; ok {
			projectNS = ns
		}
	}

	return l.projectByNamespace[projectNS]
}

func hasUserErrors(lastErrors []corev1beta1.LastError) bool {
	for _, le := range lastErrors {
		for _, code := range le.Codes {
			if _, ok := userErrorCodes[code]; ok {
				return true
			}
		}
	}
	return false
}

func getMaintenancePreconditionsStatus(shoot *corev1beta1.Shoot) string {
	for _, c := range shoot.Status.Constraints {
		if c.Type == corev1beta1.ShootMaintenancePreconditionsSatisfied {
			return string(c.Status)
		}
	}
	return "Unknown"
}

func (r *gardenerReceiver) collectShootInfoMetrics(sm *pmetric.ScopeMetrics, now pcommon.Timestamp, l shootLookups) {
	shootList := r.shootInformer.GetStore().List()
	if len(shootList) == 0 {
		r.logger.Debug("No shoots found")
		return
	}

	metric := sm.Metrics().AppendEmpty()
	metric.SetName("garden.shoot.info")
	metric.SetDescription("Information about Gardener shoots")
	metric.SetUnit("")
	gauge := metric.SetEmptyGauge()

	for _, item := range shootList {
		shoot := item.(*corev1beta1.Shoot)

		purpose := ""
		if shoot.Spec.Purpose != nil {
			purpose = string(*shoot.Spec.Purpose)
		}
		_, isSeed := l.managedSeedShoots[shoot.Name]
		seedName := ptr.Deref(shoot.Spec.SeedName, "")
		seedIaaS, seedRegion := "", ""
		if seed, ok := l.seedByName[seedName]; ok {
			seedIaaS = seed.Spec.Provider.Type
			seedRegion = seed.Spec.Provider.Region
		}
		failureTol := ""
		if shoot.Spec.ControlPlane != nil && shoot.Spec.ControlPlane.HighAvailability != nil {
			failureTol = string(shoot.Spec.ControlPlane.HighAvailability.FailureTolerance.Type)
		}
		pi := l.resolveBillingInfo(shoot)

		dp := gauge.DataPoints().AppendEmpty()
		dp.SetTimestamp(now)
		dp.SetIntValue(1)
		dp.Attributes().PutStr("gardener.shoot.name", shoot.Name)
		dp.Attributes().PutStr("gardener.project.name", getProject(shoot))
		dp.Attributes().PutStr("cloud.provider", shoot.Spec.Provider.Type)
		dp.Attributes().PutStr("cloud.region", shoot.Spec.Region)
		dp.Attributes().PutStr("gardener.kubernetes.version", shoot.Spec.Kubernetes.Version)
		dp.Attributes().PutStr("gardener.version", shoot.Status.Gardener.Version)
		dp.Attributes().PutStr("gardener.seed.name", seedName)
		dp.Attributes().PutStr("gardener.seed.iaas", seedIaaS)
		dp.Attributes().PutStr("gardener.seed.region", seedRegion)
		dp.Attributes().PutStr("gardener.shoot.uid", string(shoot.UID))
		dp.Attributes().PutStr("gardener.shoot.technical_id", shoot.Status.TechnicalID)
		dp.Attributes().PutStr("gardener.shoot.purpose", purpose)
		dp.Attributes().PutBool("gardener.shoot.workerless", shoot.Spec.Provider.Workers == nil)
		dp.Attributes().PutBool("gardener.shoot.is_seed", isSeed)
		dp.Attributes().PutStr("gardener.shoot.failure_tolerance", failureTol)
		dp.Attributes().PutStr("gardener.cost_object", pi.costObject)
		dp.Attributes().PutStr("gardener.cost_object_type", pi.costObjectType)
		dp.Attributes().PutStr("gardener.cost_object_owner", pi.costObjectOwner)
	}
}

func (r *gardenerReceiver) collectShootHibernatedMetric(sm *pmetric.ScopeMetrics, now pcommon.Timestamp) {
	shootList := r.shootInformer.GetStore().List()
	if len(shootList) == 0 {
		return
	}

	metric := sm.Metrics().AppendEmpty()
	metric.SetName("garden.shoot.hibernated")
	metric.SetDescription("Hibernation status of a shoot. Value is 1 if the shoot is currently hibernated, 0 otherwise.")
	metric.SetUnit("")
	gauge := metric.SetEmptyGauge()

	for _, item := range shootList {
		shoot := item.(*corev1beta1.Shoot)
		dp := gauge.DataPoints().AppendEmpty()
		dp.SetTimestamp(now)
		if shoot.Status.IsHibernated {
			dp.SetIntValue(1)
		} else {
			dp.SetIntValue(0)
		}
		dp.Attributes().PutStr("gardener.shoot.name", shoot.Name)
		dp.Attributes().PutStr("gardener.project.name", getProject(shoot))
		dp.Attributes().PutStr("gardener.shoot.uid", string(shoot.UID))
		dp.Attributes().PutStr("gardener.shoot.technical_id", shoot.Status.TechnicalID)
	}
}

func (r *gardenerReceiver) collectShootCreationTimestamp(sm *pmetric.ScopeMetrics, now pcommon.Timestamp) {
	shootList := r.shootInformer.GetStore().List()
	if len(shootList) == 0 {
		return
	}

	metric := sm.Metrics().AppendEmpty()
	metric.SetName("garden.shoot.creation_timestamp")
	metric.SetDescription("Timestamp of the shoot creation.")
	metric.SetUnit("s")
	gauge := metric.SetEmptyGauge()

	for _, item := range shootList {
		shoot := item.(*corev1beta1.Shoot)
		dp := gauge.DataPoints().AppendEmpty()
		dp.SetTimestamp(now)
		dp.SetIntValue(shoot.CreationTimestamp.Unix())
		dp.Attributes().PutStr("gardener.shoot.name", shoot.Name)
		dp.Attributes().PutStr("gardener.project.name", getProject(shoot))
		dp.Attributes().PutStr("gardener.shoot.uid", string(shoot.UID))
		dp.Attributes().PutStr("gardener.shoot.technical_id", shoot.Status.TechnicalID)
	}
}

func (r *gardenerReceiver) collectShootOperationStates(sm *pmetric.ScopeMetrics, now pcommon.Timestamp) {
	shootList := r.shootInformer.GetStore().List()
	if len(shootList) == 0 {
		r.logger.Debug("No shoots found")
		return
	}

	statesMetric := sm.Metrics().AppendEmpty()
	statesMetric.SetName("garden.shoot.operation_states")
	statesMetric.SetDescription("Operation state of a Shoot. Available operations: 'Create'|'Reconcile'|'Delete'|'Migrate'|'Restore'.")
	statesMetric.SetUnit("")
	statesGauge := statesMetric.SetEmptyGauge()

	progressMetric := sm.Metrics().AppendEmpty()
	progressMetric.SetName("garden.shoot.operation_progress_percent")
	progressMetric.SetDescription("Operation progress of a Shoot in percent.")
	progressMetric.SetUnit("%")
	progressGauge := progressMetric.SetEmptyGauge()

	allOperationTypes := []corev1beta1.LastOperationType{
		corev1beta1.LastOperationTypeCreate,
		corev1beta1.LastOperationTypeReconcile,
		corev1beta1.LastOperationTypeDelete,
		corev1beta1.LastOperationTypeMigrate,
		corev1beta1.LastOperationTypeRestore,
	}

	for _, item := range shootList {
		shoot := item.(*corev1beta1.Shoot)
		project := getProject(shoot)

		for _, opType := range allOperationTypes {
			statesDp := statesGauge.DataPoints().AppendEmpty()
			statesDp.SetTimestamp(now)
			statesDp.Attributes().PutStr("gardener.shoot.name", shoot.Name)
			statesDp.Attributes().PutStr("gardener.project.name", project)
			statesDp.Attributes().PutStr("gardener.operation.type", string(opType))
			statesDp.Attributes().PutStr("gardener.shoot.technical_id", shoot.Status.TechnicalID)

			progressDp := progressGauge.DataPoints().AppendEmpty()
			progressDp.SetTimestamp(now)
			progressDp.Attributes().PutStr("gardener.shoot.name", shoot.Name)
			progressDp.Attributes().PutStr("gardener.project.name", project)
			progressDp.Attributes().PutStr("gardener.operation.type", string(opType))
			progressDp.Attributes().PutStr("gardener.shoot.technical_id", shoot.Status.TechnicalID)

			if shoot.Status.LastOperation != nil && shoot.Status.LastOperation.Type == opType {
				statesDp.Attributes().PutStr("gardener.operation.state", string(shoot.Status.LastOperation.State))
				statesDp.SetIntValue(1)
				progressDp.SetIntValue(int64(shoot.Status.LastOperation.Progress))
			} else {
				statesDp.Attributes().PutStr("gardener.operation.state", "")
				statesDp.SetIntValue(0)
				progressDp.SetIntValue(0)
			}
		}
	}
}

func (r *gardenerReceiver) collectShootConditions(sm *pmetric.ScopeMetrics, now pcommon.Timestamp) {
	shootList := r.shootInformer.GetStore().List()
	if len(shootList) == 0 {
		r.logger.Debug("No shoots found")
		return
	}

	metric := sm.Metrics().AppendEmpty()
	metric.SetName("garden.shoot.condition")
	metric.SetDescription("Condition state of a Gardener shoot")
	metric.SetUnit("")
	gauge := metric.SetEmptyGauge()

	for _, item := range shootList {
		shoot := item.(*corev1beta1.Shoot)

		operationType := ""
		if shoot.Status.LastOperation != nil {
			operationType = string(shoot.Status.LastOperation.Type)
		}

		shootHasUserErrors := hasUserErrors(shoot.Status.LastErrors)
		isCompliant := getMaintenancePreconditionsStatus(shoot)

		for _, condition := range shoot.Status.Conditions {
			dp := gauge.DataPoints().AppendEmpty()
			dp.SetTimestamp(now)
			dp.SetIntValue(1)
			dp.Attributes().PutStr("gardener.shoot.name", shoot.Name)
			dp.Attributes().PutStr("gardener.project.name", getProject(shoot))
			dp.Attributes().PutStr("gardener.shoot.uid", string(shoot.UID))
			dp.Attributes().PutStr("gardener.shoot.technical_id", shoot.Status.TechnicalID)
			dp.Attributes().PutStr("gardener.condition.type", string(condition.Type))
			dp.Attributes().PutStr("gardener.condition.status", string(condition.Status))
			dp.Attributes().PutStr("gardener.condition.reason", condition.Reason)
			dp.Attributes().PutStr("gardener.operation.type", operationType)
			dp.Attributes().PutBool("gardener.shoot.has_user_errors", shootHasUserErrors)
			dp.Attributes().PutStr("gardener.shoot.is_compliant", isCompliant)
		}
	}
}

func (r *gardenerReceiver) collectShootStatusMetric(sm *pmetric.ScopeMetrics, now pcommon.Timestamp) {
	shootList := r.shootInformer.GetStore().List()
	if len(shootList) == 0 {
		r.logger.Debug("No shoots found")
		return
	}

	metric := sm.Metrics().AppendEmpty()
	metric.SetName("garden.shoot.status")
	metric.SetDescription("Status of a Gardener shoot")
	metric.SetUnit("")
	gauge := metric.SetEmptyGauge()

	statusValues := []string{"healthy", "progressing", "unhealthy", "unknown"}

	for _, item := range shootList {
		shoot := item.(*corev1beta1.Shoot)

		actualStatus := shoot.Labels[corev1beta1constants.ShootStatus]

		for _, status := range statusValues {
			dp := gauge.DataPoints().AppendEmpty()
			dp.SetTimestamp(now)
			if status == actualStatus {
				dp.SetIntValue(1)
			} else {
				dp.SetIntValue(0)
			}
			dp.Attributes().PutStr("gardener.shoot.name", shoot.Name)
			dp.Attributes().PutStr("gardener.project.name", getProject(shoot))
			dp.Attributes().PutStr("gardener.shoot.technical_id", shoot.Status.TechnicalID)
			dp.Attributes().PutStr("gardener.shoot.status", status)
		}
	}
}

func (r *gardenerReceiver) collectShootNodeMetrics(sm *pmetric.ScopeMetrics, now pcommon.Timestamp) {
	shootList := r.shootInformer.GetStore().List()

	if len(shootList) == 0 {
		r.logger.Debug("No shoots found")
		return
	}

	minWorkerMetric := sm.Metrics().AppendEmpty()
	minWorkerMetric.SetName("garden.shoot.worker.min")
	minWorkerMetric.SetDescription("Minimum number of nodes in worker pool")
	minWorkerMetric.SetUnit("{node}")
	minWorkerGauge := minWorkerMetric.SetEmptyGauge()

	maxWorkerMetric := sm.Metrics().AppendEmpty()
	maxWorkerMetric.SetName("garden.shoot.worker.max")
	maxWorkerMetric.SetDescription("Maximum number of nodes in worker pool")
	maxWorkerMetric.SetUnit("{node}")
	maxWorkerGauge := maxWorkerMetric.SetEmptyGauge()

	shootNodeInfoMetric := sm.Metrics().AppendEmpty()
	shootNodeInfoMetric.SetName("garden.shoot.node")
	shootNodeInfoMetric.SetDescription("Information about worker pools in Gardener shoots")
	shootNodeInfoMetric.SetUnit("{node}")
	shootNodeInfoGauge := shootNodeInfoMetric.SetEmptyGauge()

	minNodesMetric := sm.Metrics().AppendEmpty()
	minNodesMetric.SetName("garden.shoot.nodes.min")
	minNodesMetric.SetDescription("Minimum number of nodes in Gardener shoot")
	minNodesMetric.SetUnit("{node}")
	minNodesGauge := minNodesMetric.SetEmptyGauge()

	maxNodesMetric := sm.Metrics().AppendEmpty()
	maxNodesMetric.SetName("garden.shoot.nodes.max")
	maxNodesMetric.SetDescription("Maximum number of nodes in Gardener shoot")
	maxNodesMetric.SetUnit("{node}")
	maxNodesGauge := maxNodesMetric.SetEmptyGauge()

	for _, shootListItem := range shootList {
		minNodes := 0
		maxNodes := 0

		shoot := shootListItem.(*corev1beta1.Shoot)
		project := getProject(shoot)

		for _, worker := range shoot.Spec.Provider.Workers {
			minWorkerDp := minWorkerGauge.DataPoints().AppendEmpty()
			minWorkerDp.SetTimestamp(now)
			minWorkerDp.SetIntValue(int64(worker.Minimum))
			minWorkerDp.Attributes().PutStr("gardener.shoot.name", shoot.Name)
			minWorkerDp.Attributes().PutStr("gardener.project.name", project)
			minWorkerDp.Attributes().PutStr("gardener.worker.name", worker.Name)
			minWorkerDp.Attributes().PutStr("gardener.worker.machine.type", worker.Machine.Type)
			minWorkerDp.Attributes().PutStr("gardener.shoot.technical_id", shoot.Status.TechnicalID)
			minNodes += int(worker.Minimum)

			maxWorkerDp := maxWorkerGauge.DataPoints().AppendEmpty()
			maxWorkerDp.SetTimestamp(now)
			maxWorkerDp.SetIntValue(int64(worker.Maximum))
			maxWorkerDp.Attributes().PutStr("gardener.shoot.name", shoot.Name)
			maxWorkerDp.Attributes().PutStr("gardener.project.name", project)
			maxWorkerDp.Attributes().PutStr("gardener.worker.name", worker.Name)
			maxWorkerDp.Attributes().PutStr("gardener.worker.machine.type", worker.Machine.Type)
			maxWorkerDp.Attributes().PutStr("gardener.shoot.technical_id", shoot.Status.TechnicalID)
			maxNodes += int(worker.Maximum)

			var criName string
			var containerRuntimes []string

			if worker.CRI == nil {
				criName = "docker (default)"
			} else {
				criName = string(worker.CRI.Name)
				for _, runtime := range worker.CRI.ContainerRuntimes {
					containerRuntimes = append(containerRuntimes, runtime.Type)
				}
				sort.Strings(containerRuntimes)
			}

			var imageVersion, imageArch string
			if worker.Machine.Image != nil {
				imageVersion = ptr.Deref(worker.Machine.Image.Version, "")
				if worker.Machine.Image.Name != "" {
					shootNodeInfoGaugeDp := shootNodeInfoGauge.DataPoints().AppendEmpty()
					shootNodeInfoGaugeDp.SetTimestamp(now)
					shootNodeInfoGaugeDp.SetIntValue(1)
					shootNodeInfoGaugeDp.Attributes().PutStr("gardener.shoot.name", shoot.Name)
					shootNodeInfoGaugeDp.Attributes().PutStr("gardener.project.name", project)
					shootNodeInfoGaugeDp.Attributes().PutStr("gardener.worker.name", worker.Name)
					shootNodeInfoGaugeDp.Attributes().PutStr("gardener.worker.machine.image.name", worker.Machine.Image.Name)
					shootNodeInfoGaugeDp.Attributes().PutStr("gardener.worker.machine.image.version", imageVersion)
					imageArch = ptr.Deref(worker.Machine.Architecture, "")
					shootNodeInfoGaugeDp.Attributes().PutStr("gardener.worker.machine.architecture", imageArch)
					shootNodeInfoGaugeDp.Attributes().PutStr("gardener.worker.cri", criName)
					shootNodeInfoGaugeDp.Attributes().PutStr("gardener.worker.container_runtimes", strings.Join(containerRuntimes, ", "))
					shootNodeInfoGaugeDp.Attributes().PutStr("gardener.shoot.technical_id", shoot.Status.TechnicalID)
				}
			}
		}

		minNodesDp := minNodesGauge.DataPoints().AppendEmpty()
		minNodesDp.SetTimestamp(now)
		minNodesDp.SetIntValue(int64(minNodes))
		minNodesDp.Attributes().PutStr("gardener.shoot.name", shoot.Name)
		minNodesDp.Attributes().PutStr("gardener.project.name", project)
		minNodesDp.Attributes().PutStr("gardener.shoot.technical_id", shoot.Status.TechnicalID)

		maxNodesDp := maxNodesGauge.DataPoints().AppendEmpty()
		maxNodesDp.SetTimestamp(now)
		maxNodesDp.SetIntValue(int64(maxNodes))
		maxNodesDp.Attributes().PutStr("gardener.shoot.name", shoot.Name)
		maxNodesDp.Attributes().PutStr("gardener.project.name", project)
		maxNodesDp.Attributes().PutStr("gardener.shoot.technical_id", shoot.Status.TechnicalID)
	}
}

func (r *gardenerReceiver) collectShootOperationsTotal(sm *pmetric.ScopeMetrics, now pcommon.Timestamp) {
	shootList := r.shootInformer.GetStore().List()

	if len(shootList) == 0 {
		r.logger.Debug("No shoots found")
		return
	}

	type key struct {
		operation, state, iaas, seed, version, region string
	}
	counts := map[key]int64{}

	for _, shootListItem := range shootList {
		shoot := shootListItem.(*corev1beta1.Shoot)
		if shoot.Status.LastOperation == nil {
			continue
		}
		k := key{
			operation: string(shoot.Status.LastOperation.Type),
			state:     string(shoot.Status.LastOperation.State),
			iaas:      shoot.Spec.Provider.Type,
			seed:      ptr.Deref(shoot.Spec.SeedName, ""),
			version:   shoot.Spec.Kubernetes.Version,
			region:    shoot.Spec.Region,
		}
		counts[k]++
	}

	if len(counts) == 0 {
		return
	}

	metric := sm.Metrics().AppendEmpty()
	metric.SetName("garden.shoot.operations_total")
	metric.SetDescription("Count of ongoing shoot operations")
	metric.SetUnit("{operation}")

	gauge := metric.SetEmptyGauge()

	for k, count := range counts {
		dp := gauge.DataPoints().AppendEmpty()
		dp.SetTimestamp(now)
		dp.SetIntValue(count)
		dp.Attributes().PutStr("gardener.operation.type", k.operation)
		dp.Attributes().PutStr("gardener.operation.state", k.state)
		dp.Attributes().PutStr("cloud.provider", k.iaas)
		dp.Attributes().PutStr("gardener.seed.name", k.seed)
		dp.Attributes().PutStr("gardener.kubernetes.version", k.version)
		dp.Attributes().PutStr("cloud.region", k.region)
	}
}

func (r *gardenerReceiver) collectShootCustomizationMetrics(sm *pmetric.ScopeMetrics, now pcommon.Timestamp) {
	shootList := r.shootInformer.GetStore().List()

	if len(shootList) == 0 {
		r.logger.Debug("No shoots found")
		return
	}

	var (
		hibernationEnabled     int64
		hibernationScheduled   int64
		maintenanceWindow      int64
		autoUpdateK8s          int64
		autoUpdateMachineImage int64
		nginxIngressEnabled    int64
		kubeDashboardEnabled   int64
		multipleWorkerPools    int64
		multiZoneWorkers       int64
		customDomain           int64
		workerTaints           int64
		workerLabels           int64
		workerAnnotations      int64
		auditPolicyCount       int64
		oidcConfigCount        int64
		nodeCIDRMaskCount      int64
		hpaKCMCount            int64
		podPIDLimitCount       int64
	)

	extensionCounts := map[string]int64{}
	featureGatesAPIServer := map[string]int64{}
	featureGatesKCM := map[string]int64{}
	featureGatesScheduler := map[string]int64{}
	admissionPlugins := map[string]int64{}
	proxyModes := map[string]int64{}

	for _, shootListItem := range shootList {
		shoot := shootListItem.(*corev1beta1.Shoot)

		if shoot.Spec.Hibernation != nil {
			if ptr.Deref(shoot.Spec.Hibernation.Enabled, false) {
				hibernationEnabled++
			}
			if len(shoot.Spec.Hibernation.Schedules) > 0 {
				hibernationScheduled++
			}
		}

		if shoot.Spec.Maintenance != nil {
			if shoot.Spec.Maintenance.TimeWindow != nil {
				maintenanceWindow++
			}
			if shoot.Spec.Maintenance.AutoUpdate != nil {
				if shoot.Spec.Maintenance.AutoUpdate.KubernetesVersion {
					autoUpdateK8s++
				}
				if shoot.Spec.Maintenance.AutoUpdate.MachineImageVersion != nil && *shoot.Spec.Maintenance.AutoUpdate.MachineImageVersion {
					autoUpdateMachineImage++
				}
			}
		}

		if shoot.Spec.Addons != nil {
			if shoot.Spec.Addons.NginxIngress != nil && shoot.Spec.Addons.NginxIngress.Enabled {
				nginxIngressEnabled++
			}
			if shoot.Spec.Addons.KubernetesDashboard != nil && shoot.Spec.Addons.KubernetesDashboard.Enabled {
				kubeDashboardEnabled++
			}
		}

		if len(shoot.Spec.Provider.Workers) > 1 {
			multipleWorkerPools++
		}

		workerZones := map[string]struct{}{}
		for _, w := range shoot.Spec.Provider.Workers {
			for _, z := range w.Zones {
				workerZones[z] = struct{}{}
			}
		}
		if len(workerZones) > 1 {
			multiZoneWorkers++
		}

		// Extensions
		shootExtensions := map[string]struct{}{}
		for _, ext := range shoot.Spec.Extensions {
			shootExtensions[ext.Type] = struct{}{}
		}
		for extType := range shootExtensions {
			extensionCounts[extType]++
		}

		// Custom DNS domain
		if shoot.Spec.DNS != nil && shoot.Spec.DNS.Domain != nil && *shoot.Spec.DNS.Domain != "" {
			customDomain++
		}

		// Worker pool taints, labels, annotations (count per shoot, not per pool)
		for _, w := range shoot.Spec.Provider.Workers {
			if len(w.Taints) > 0 {
				workerTaints++
				break
			}
		}
		for _, w := range shoot.Spec.Provider.Workers {
			if len(w.Labels) > 0 {
				workerLabels++
				break
			}
		}
		for _, w := range shoot.Spec.Provider.Workers {
			if len(w.Annotations) > 0 {
				workerAnnotations++
				break
			}
		}

		// Kubernetes component customization
		if shoot.Spec.Kubernetes.KubeAPIServer != nil {
			apiServer := shoot.Spec.Kubernetes.KubeAPIServer
			if apiServer.AuditConfig != nil && apiServer.AuditConfig.AuditPolicy != nil {
				auditPolicyCount++
			}
			if apiServer.OIDCConfig != nil { //nolint:staticcheck
				oidcConfigCount++
			}
			for fg, enabled := range apiServer.FeatureGates {
				if enabled {
					featureGatesAPIServer[fg]++
				}
			}
			for _, ap := range apiServer.AdmissionPlugins {
				if ptr.Deref(ap.Disabled, false) {
					continue
				}
				admissionPlugins[ap.Name]++
			}
		}

		if shoot.Spec.Kubernetes.KubeControllerManager != nil {
			kcm := shoot.Spec.Kubernetes.KubeControllerManager
			if kcm.NodeCIDRMaskSize != nil {
				nodeCIDRMaskCount++
			}
			if kcm.HorizontalPodAutoscalerConfig != nil {
				hpaKCMCount++
			}
			for fg, enabled := range kcm.FeatureGates {
				if enabled {
					featureGatesKCM[fg]++
				}
			}
		}

		if shoot.Spec.Kubernetes.KubeScheduler != nil {
			for fg, enabled := range shoot.Spec.Kubernetes.KubeScheduler.FeatureGates {
				if enabled {
					featureGatesScheduler[fg]++
				}
			}
		}

		if shoot.Spec.Kubernetes.Kubelet != nil && shoot.Spec.Kubernetes.Kubelet.PodPIDsLimit != nil {
			podPIDLimitCount++
		}

		if shoot.Spec.Kubernetes.KubeProxy != nil && shoot.Spec.Kubernetes.KubeProxy.Mode != nil {
			proxyModes[string(*shoot.Spec.Kubernetes.KubeProxy.Mode)]++
		}
	}

	// Scalar metrics (no per-label breakdown)
	type customMetric struct {
		name  string
		desc  string
		value int64
	}
	scalarMetrics := []customMetric{
		{"garden.shoots.hibernation.enabled_total", "Count of shoots with hibernation enabled", hibernationEnabled},
		{"garden.shoots.hibernation.schedule_total", "Count of shoots with a hibernation schedule configured", hibernationScheduled},
		{"garden.shoots.maintenance.window_total", "Count of shoots with a maintenance window configured", maintenanceWindow},
		{"garden.shoots.maintenance.autoupdate.k8s_version_total", "Count of shoots with auto-update for kubernetes versions configured", autoUpdateK8s},
		{"garden.shoots.maintenance.autoupdate.image_version_total", "Count of shoots with auto-update for machine image versions configured", autoUpdateMachineImage},
		{"garden.shoots.custom.addon.nginx_ingress_total", "Count of shoots with nginx ingress controller addon enabled", nginxIngressEnabled},
		{"garden.shoots.custom.addon.kube_dashboard_total", "Count of shoots with kubernetes dashboard addon enabled", kubeDashboardEnabled},
		{"garden.shoots.custom.worker.multiple_pools_total", "Count of shoots with multiple worker pools", multipleWorkerPools},
		{"garden.shoots.custom.worker.multi_zones_total", "Count of shoots with multi zone worker pools", multiZoneWorkers},
		{"garden.shoots.custom.network.custom_domain_total", "Count of shoots which use a custom DNS domain", customDomain},
		{"garden.shoots.custom.worker.taints_total", "Count of shoots with worker pool taints", workerTaints},
		{"garden.shoots.custom.worker.labels_total", "Count of shoots with worker pool labels", workerLabels},
		{"garden.shoots.custom.worker.annotations_total", "Count of shoots with worker pool annotations", workerAnnotations},
		{"garden.shoots.custom.apiserver.audit_policy_total", "Count of shoots with an audit log policy configured for the kube apiserver", auditPolicyCount},
		{"garden.shoots.custom.apiserver.oidc_config_total", "Count of shoots with an OIDC configuration for the kube apiserver", oidcConfigCount},
		{"garden.shoots.custom.kcm.node_cidr_mask_size_total", "Count of shoots which have node CIDR mask size configured on the kube controller manager", nodeCIDRMaskCount},
		{"garden.shoots.custom.kcm.horizontal_pod_autoscale_total", "Count of shoots with horizontal pod autoscaling for the kube controller manager", hpaKCMCount},
		{"garden.shoots.custom.kubelet.pod_pid_limit_total", "Count of shoots which have a pod PID limit configured for the kubelet(s)", podPIDLimitCount},
	}

	for _, m := range scalarMetrics {
		metric := sm.Metrics().AppendEmpty()
		metric.SetName(m.name)
		metric.SetDescription(m.desc)
		metric.SetUnit("")
		gauge := metric.SetEmptyGauge()
		dp := gauge.DataPoints().AppendEmpty()
		dp.SetTimestamp(now)
		dp.SetIntValue(m.value)
	}

	// Labeled metrics — one data point per label value
	type labeledMetric struct {
		name   string
		desc   string
		label  string
		counts map[string]int64
	}
	labeledMetrics := []labeledMetric{
		{
			"garden.shoots.custom.extensions_total",
			"Count of shoots which have a specific extension configured",
			"gardener.extension.type",
			extensionCounts,
		},
		{
			"garden.shoots.custom.apiserver.feature_gates_total",
			"Count of shoots with enabled kube apiserver feature gates",
			"gardener.feature_gate",
			featureGatesAPIServer,
		},
		{
			"garden.shoots.custom.apiserver.admission_plugins_total",
			"Count of shoots with enabled kube apiserver admission plugins",
			"gardener.admission_plugin",
			admissionPlugins,
		},
		{
			"garden.shoots.custom.kcm.feature_gates_total",
			"Count of shoots with enabled kube controller manager feature gates",
			"gardener.feature_gate",
			featureGatesKCM,
		},
		{
			"garden.shoots.custom.scheduler.feature_gates_total",
			"Count of shoots with enabled kube scheduler feature gates",
			"gardener.feature_gate",
			featureGatesScheduler,
		},
		{
			"garden.shoots.custom.proxy.mode_total",
			"Count of shoots by proxy mode configuration for the kube proxy",
			"gardener.proxy.mode",
			proxyModes,
		},
	}

	for _, m := range labeledMetrics {
		if len(m.counts) == 0 {
			continue
		}
		metric := sm.Metrics().AppendEmpty()
		metric.SetName(m.name)
		metric.SetDescription(m.desc)
		metric.SetUnit("")
		gauge := metric.SetEmptyGauge()
		for labelVal, count := range m.counts {
			dp := gauge.DataPoints().AppendEmpty()
			dp.SetTimestamp(now)
			dp.SetIntValue(count)
			dp.Attributes().PutStr(m.label, labelVal)
		}
	}
}

func getProject(shoot *corev1beta1.Shoot) string {
	const prefix = "garden-"
	if shoot.Namespace == "garden" {
		return "garden"
	}
	if strings.HasPrefix(shoot.Namespace, prefix) {
		return shoot.Namespace[len(prefix):]
	}
	return shoot.Namespace
}
