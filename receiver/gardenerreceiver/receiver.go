// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package gardenerreceiver

import (
	"context"
	"fmt"
	"time"

	gardenerversioned "github.com/gardener/gardener/pkg/client/core/clientset/versioned"
	gardenerinformers "github.com/gardener/gardener/pkg/client/core/informers/externalversions"
	seedmanagementclientset "github.com/gardener/gardener/pkg/client/seedmanagement/clientset/versioned"
	seedmanagementinformers "github.com/gardener/gardener/pkg/client/seedmanagement/informers/externalversions"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/receiver"
	"go.opentelemetry.io/collector/receiver/receiverhelper"
	conventions "go.opentelemetry.io/otel/semconv/v1.18.0"
	"go.uber.org/zap"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
)

var _ receiver.Metrics = (*gardenerReceiver)(nil)

type gardenerReceiver struct {
	settings       receiver.Settings
	config         *Config
	consumer       consumer.Metrics
	cancel         context.CancelFunc
	logger         *zap.Logger
	gardenerClient gardenerversioned.Interface
	seedMgmtClient seedmanagementclientset.Interface
	obsrecv        *receiverhelper.ObsReport

	shootInformer       cache.SharedIndexInformer
	seedInformer        cache.SharedIndexInformer
	projectInformer     cache.SharedIndexInformer
	managedSeedInformer cache.SharedIndexInformer
	gardenletInformer   cache.SharedIndexInformer
}

func newReceiver(
	_ context.Context,
	settings receiver.Settings,
	cfg component.Config,
	consumer consumer.Metrics,
) (receiver.Metrics, error) {
	rCfg := cfg.(*Config)

	// Build Kubernetes client config.
	// If Kubeconfig is empty, BuildConfigFromFlags will automatically try:
	// 1. The $KUBECONFIG environment variable
	// 2. The default ~/.kube/config path
	// 3. In-cluster configuration
	config, err := clientcmd.BuildConfigFromFlags("", rCfg.Kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to build kubeconfig: %w", err)
	}

	// Create Gardener clientset
	gardenerClient, err := gardenerversioned.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create gardener client: %w", err)
	}

	// Create SeedManagement clientset
	seedMgmtClient, err := seedmanagementclientset.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create seedmanagement client: %w", err)
	}

	obsrecv, err := receiverhelper.NewObsReport(receiverhelper.ObsReportSettings{
		ReceiverID:             settings.ID,
		Transport:              "",
		ReceiverCreateSettings: settings,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create obsreport: %w", err)
	}

	receiver := &gardenerReceiver{
		settings:       settings,
		config:         rCfg,
		consumer:       consumer,
		logger:         settings.Logger,
		gardenerClient: gardenerClient,
		seedMgmtClient: seedMgmtClient,
		obsrecv:        obsrecv,
	}

	return receiver, nil
}

func (r *gardenerReceiver) Start(_ context.Context, _ component.Host) error {
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel

	r.logger.Info("Starting Gardener receiver")

	if r.config.HasShootResource() {
		// Set up informer factory
		shootFactory := gardenerinformers.NewSharedInformerFactoryWithOptions(
			r.gardenerClient,
			r.config.SyncPeriod,
			gardenerinformers.WithNamespace(r.config.Namespace),
		)

		// Set up Shoot informer
		r.shootInformer = shootFactory.Core().V1beta1().Shoots().Informer()

		shootFactory.Start(ctx.Done())

		r.logger.Info("Waiting for shoot informer cache to sync")
		if !cache.WaitForCacheSync(ctx.Done(), r.shootInformer.HasSynced) {
			return fmt.Errorf("failed to sync shoot informer cache")
		}

		r.logger.Info("Shoot informer caches synced successfully")
	}

	if r.config.HasSeedResource() || r.config.HasProjectResource() {
		// Seeds and projects are both cluster-scoped resources in the same API group;
		// share a single factory to avoid duplicate list/watch connections.
		clusterScopedFactory := gardenerinformers.NewSharedInformerFactory(r.gardenerClient, r.config.SyncPeriod)

		var toSync []cache.InformerSynced

		if r.config.HasSeedResource() {
			r.seedInformer = clusterScopedFactory.Core().V1beta1().Seeds().Informer()
			toSync = append(toSync, r.seedInformer.HasSynced)
		}

		if r.config.HasProjectResource() {
			r.projectInformer = clusterScopedFactory.Core().V1beta1().Projects().Informer()
			toSync = append(toSync, r.projectInformer.HasSynced)
		}

		clusterScopedFactory.Start(ctx.Done())

		r.logger.Info("Waiting for cluster-scoped informer caches to sync")
		if !cache.WaitForCacheSync(ctx.Done(), toSync...) {
			return fmt.Errorf("failed to sync cluster-scoped informer caches")
		}

		r.logger.Info("Cluster-scoped informer caches synced successfully")
	}

	if r.config.HasManagedSeedResource() || r.config.HasGardenletResource() {
		seedMgmtFactory := seedmanagementinformers.NewSharedInformerFactory(r.seedMgmtClient, r.config.SyncPeriod)

		if r.config.HasManagedSeedResource() {
			r.managedSeedInformer = seedMgmtFactory.Seedmanagement().V1alpha1().ManagedSeeds().Informer()
		}
		if r.config.HasGardenletResource() {
			r.gardenletInformer = seedMgmtFactory.Seedmanagement().V1alpha1().Gardenlets().Informer()
		}

		seedMgmtFactory.Start(ctx.Done())

		if r.config.HasManagedSeedResource() {
			r.logger.Info("Waiting for managed seed informer cache to sync")
			if !cache.WaitForCacheSync(ctx.Done(), r.managedSeedInformer.HasSynced) {
				return fmt.Errorf("failed to sync managed seed informer cache")
			}
			r.logger.Info("ManagedSeed informer caches synced successfully")
		}
		if r.config.HasGardenletResource() {
			r.logger.Info("Waiting for gardenlet informer cache to sync")
			if !cache.WaitForCacheSync(ctx.Done(), r.gardenletInformer.HasSynced) {
				return fmt.Errorf("failed to sync gardenlet informer cache")
			}
			r.logger.Info("Gardenlet informer caches synced successfully")
		}
	}

	go r.emitMetrics(ctx)

	return nil
}

func (r *gardenerReceiver) Shutdown(context.Context) error {
	r.logger.Info("Shutting down Gardener receiver")
	if r.cancel != nil {
		r.cancel()
	}

	return nil
}

func (r *gardenerReceiver) emitMetrics(ctx context.Context) {
	ticker := time.NewTicker(r.config.CollectionInterval)
	defer ticker.Stop()

	// Emit an initial metric immediately
	if err := r.sendMetrics(ctx); err != nil {
		r.logger.Error("Failed to send initial shoot metrics", zap.Error(err))
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.sendMetrics(ctx); err != nil {
				r.logger.Error("Failed to send shoot metrics", zap.Error(err))
			}
		}
	}
}

func (r *gardenerReceiver) sendMetrics(ctx context.Context) error {
	md := pmetric.NewMetrics()
	sm := r.initScopeMetrics(&md)

	now := nowTimestamp()

	if r.config.HasShootResource() {
		l := r.buildShootLookups()
		r.collectShootInfoMetrics(&sm, now, l)
		r.collectShootHibernatedMetric(&sm, now)
		r.collectShootCreationTimestamp(&sm, now)
		r.collectShootConditions(&sm, now, l)
		r.collectShootOperationStates(&sm, now)
		r.collectShootNodeMetrics(&sm, now)
		r.collectShootOperationsTotal(&sm, now)
		r.collectShootCustomizationMetrics(&sm, now)
	}

	if r.config.HasSeedResource() {
		r.collectSeedInfoMetrics(&sm, now)
		r.emitSeedCapacityMetrics(&sm, now)
		r.collectSeedConditions(&sm, now)
		r.collectSeedAllocatableMetrics(&sm, now)
		r.collectSeedOperationStates(&sm, now)
	}

	if r.config.HasProjectResource() {
		r.collectProjectMetrics(&sm, now)
		r.collectUserMetrics(&sm, now)
	}

	if r.config.HasManagedSeedResource() {
		r.collectManagedSeedMetrics(&sm, now)
	}

	if r.config.HasGardenletResource() {
		r.collectGardenletMetrics(&sm, now)
	}

	ctx = r.obsrecv.StartMetricsOp(ctx)
	dataPointCount := md.DataPointCount()

	err := r.consumer.ConsumeMetrics(ctx, md)

	r.obsrecv.EndMetricsOp(ctx, "gardener", dataPointCount, err)

	return err
}

func (r *gardenerReceiver) initScopeMetrics(md *pmetric.Metrics) pmetric.ScopeMetrics {
	rm := md.ResourceMetrics().AppendEmpty()
	rm.SetSchemaUrl(conventions.SchemaURL)

	// Create scope metrics
	sm := rm.ScopeMetrics().AppendEmpty()
	sm.SetSchemaUrl(conventions.SchemaURL)
	sm.Scope().SetName("github.com/gardener/opentelemetry-collector/receiver/gardenerreceiver")

	return sm
}

func nowTimestamp() pcommon.Timestamp {
	return pcommon.NewTimestampFromTime(time.Now())
}
