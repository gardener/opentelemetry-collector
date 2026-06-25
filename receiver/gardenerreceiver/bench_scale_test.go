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

	gardenerfake "github.com/gardener/gardener/pkg/client/core/clientset/versioned/fake"
	gardenerinformers "github.com/gardener/gardener/pkg/client/core/informers/externalversions"
	apiruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
)

// BenchmarkScale_ShootCache populates N shoots, lets the informer cache fill,
// and reports the steady-state heap with and without transformShoot.
//
// Run as:
//
//	go test -tags=bench -bench=BenchmarkScale -benchmem -run=^$ \
//	    -benchtime=1x -count=5
//
// The 1x + count=5 pattern is intentional: we want independent samples of the
// steady-state footprint, not N iterations of allocate-and-free. The diff
// between with_transform and no_transform is the value of the optimization.
func BenchmarkScale_ShootCache(b *testing.B) {
	for _, shootCount := range []int{100, 1_000, 10_000, 50_000} {
		b.Run(fmt.Sprintf("shoots=%d/with_transform", shootCount), func(b *testing.B) {
			runScale(b, shootCount, true)
		})
		b.Run(fmt.Sprintf("shoots=%d/no_transform", shootCount), func(b *testing.B) {
			runScale(b, shootCount, false)
		})
	}
}

func runScale(b *testing.B, shootCount int, withTransform bool) {
	b.ReportAllocs()

	initialShoots := make([]apiruntime.Object, 0, shootCount)
	for idx := 0; idx < shootCount; idx++ {
		initialShoots = append(initialShoots, makeShoot(idx))
	}
	gardenClient := gardenerfake.NewSimpleClientset(initialShoots...)

	var opts []gardenerinformers.SharedInformerOption
	if withTransform {
		opts = append(opts, gardenerinformers.WithTransform(transformShoot))
	}
	shootFactory := gardenerinformers.NewSharedInformerFactoryWithOptions(gardenClient, 0, opts...)
	shootInformer := shootFactory.Core().V1beta1().Shoots().Informer()

	beforeFill := memSnapshot()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	shootFactory.Start(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), shootInformer.HasSynced) {
		b.Fatal("informer cache failed to sync")
	}

	// Drop the source slice so it doesn't dominate the post-sync heap.
	initialShoots = nil

	afterFill := memSnapshot()
	reportMem(b, "steady", beforeFill, afterFill)

	// Keep the cache alive across the measurement so GC doesn't free it.
	runtime.KeepAlive(shootInformer)
}
