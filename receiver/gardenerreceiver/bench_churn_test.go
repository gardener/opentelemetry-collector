// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

//go:build bench

package gardenerreceiver

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"

	gardenerfake "github.com/gardener/gardener/pkg/client/core/clientset/versioned/fake"
	gardenerinformers "github.com/gardener/gardener/pkg/client/core/informers/externalversions"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
)

// BenchmarkChurn_ShootCache fixes N shoots in the cache, applies M updates
// across them, then reports the resulting cache memory footprint.
//
// The question this benchmark answers: does churn inflate the cache, or does
// it settle back to the same footprint as a fresh cache of size N?
//
// A healthy informer plateaus — heap after M updates should equal the steady
// state from BenchmarkScale_ShootCache at the same N. A growing footprint
// indicates retained references, growing indices, or a leak in the transform
// path.
//
// Run as:
//
//	go test -tags=bench -bench=BenchmarkChurn -run=^$ \
//	    -benchtime=1x -count=5 -timeout=30m
func BenchmarkChurn_ShootCache(b *testing.B) {
	const shootCount = 5_000
	for _, updateCount := range []int{shootCount, 10 * shootCount, 100 * shootCount} {
		b.Run(fmt.Sprintf("shoots=%d/updates=%d", shootCount, updateCount), func(b *testing.B) {
			runChurn(b, shootCount, updateCount)
		})
	}
}

func runChurn(b *testing.B, shootCount, updateCount int) {
	b.ReportAllocs()

	initialShoots := make([]apiruntime.Object, shootCount)
	for idx := 0; idx < shootCount; idx++ {
		initialShoots[idx] = makeShoot(idx)
	}
	gardenClient := gardenerfake.NewSimpleClientset(initialShoots...)
	shootFactory := gardenerinformers.NewSharedInformerFactoryWithOptions(
		gardenClient, 0,
		gardenerinformers.WithTransform(transformShoot),
	)
	shootInformer := shootFactory.Core().V1beta1().Shoots().Informer()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	shootFactory.Start(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), shootInformer.HasSynced) {
		b.Fatal("informer cache failed to sync")
	}
	initialShoots = nil

	// Baseline: cache filled, no churn yet. This is the reference point.
	baseline := memSnapshot()

	tracker := gardenClient.Tracker()
	// The fake clientset's watch channel has a small fixed buffer and panics
	// on overflow rather than blocking. Yield frequently and pause briefly
	// in larger batches so the informer can drain — this is plumbing, not
	// throughput throttling.
	for updateIdx := 0; updateIdx < updateCount; updateIdx++ {
		updated := makeShoot(updateIdx % shootCount)
		updated.ResourceVersion = fmt.Sprintf("%d", updateIdx+shootCount)
		updated.Generation = int64(updateIdx)
		updated.Status.LastOperation.LastUpdateTime = metav1.Time{Time: time.Now()}
		if err := tracker.Update(shootGVR, updated, benchNamespace); err != nil {
			b.Fatalf("tracker update failed: %v", err)
		}
		if updateIdx%50 == 0 {
			time.Sleep(time.Millisecond)
		}
	}

	// Give the informer's processing loop a moment to drain queued events
	// before sampling. Without this we'd measure mid-flight allocations
	// rather than the settled cache.
	drainInformer(shootInformer, shootCount)

	afterChurn := memSnapshot()
	b.ReportMetric(diffMiB(afterChurn.HeapAlloc, baseline.HeapAlloc), "churn_heap_growth_MiB")
	b.ReportMetric(diffMiB(afterChurn.HeapInuse, baseline.HeapInuse), "churn_inuse_growth_MiB")
	b.ReportMetric(float64(afterChurn.HeapAlloc)/float64(1<<20), "post_churn_heap_MiB")
	b.ReportMetric(float64(afterChurn.NumGC-baseline.NumGC), "churn_gc_cycles")
	b.ReportMetric(float64(len(shootInformer.GetStore().ListKeys())), "cached_objects")

	runtime.KeepAlive(shootInformer)
}

// drainInformer waits until the informer has processed pending watch events.
// Heuristic: poll the store size — once it's stable across a few ticks and
// equal to the expected count, the queue has drained.
func drainInformer(informer cache.SharedIndexInformer, expectedSize int) {
	const requiredStableTicks = 5
	stableTickCount := 0
	lastSize := -1
	for stableTickCount < requiredStableTicks {
		time.Sleep(20 * time.Millisecond)
		currentSize := len(informer.GetStore().ListKeys())
		if currentSize == lastSize && currentSize == expectedSize {
			stableTickCount++
		} else {
			stableTickCount = 0
			lastSize = currentSize
		}
	}
}
