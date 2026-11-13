/*
Copyright The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package preemption

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	clocktesting "k8s.io/utils/clock/testing"

	config "sigs.k8s.io/kueue/apis/config/v1beta2"
	kueue "sigs.k8s.io/kueue/apis/kueue/v1beta2"
	schdcache "sigs.k8s.io/kueue/pkg/cache/scheduler"
	controllerconstants "sigs.k8s.io/kueue/pkg/controller/constants"
	"sigs.k8s.io/kueue/pkg/features"
	"sigs.k8s.io/kueue/pkg/scheduler/flavorassigner"
	utiltesting "sigs.k8s.io/kueue/pkg/util/testing"
	utiltestingapi "sigs.k8s.io/kueue/pkg/util/testing/v1beta2"
	"sigs.k8s.io/kueue/pkg/workload"
)

func TestGetTargetsQuotaReservationOrder(t *testing.T) {
	cases := map[string]struct {
		olderCQ    kueue.ClusterQueueReference
		newerCQ    kueue.ClusterQueueReference
		olderBoost string
		wantTarget string
	}{
		"same ClusterQueue": {
			olderCQ: "team", newerCQ: "team", wantTarget: "older",
		},
		"same borrowing ClusterQueue": {
			olderCQ: "other-a", newerCQ: "other-a", wantTarget: "older",
		},
		"different borrowing ClusterQueues with equal shares": {
			olderCQ: "other-b", newerCQ: "other-a", wantTarget: "older",
		},
		"effective priority takes precedence over reservation time": {
			olderCQ: "team", newerCQ: "team", olderBoost: "25", wantTarget: "newer",
		},
	}
	algorithms := map[string]struct {
		fairSharing bool
		orderCache  bool
	}{
		"classical":                            {},
		"classical with candidate-order cache": {orderCache: true},
		"fair sharing":                         {fairSharing: true},
	}
	for algorithm, opts := range algorithms {
		t.Run(algorithm, func(t *testing.T) {
			features.SetFeatureGateDuringTest(t, features.ClassicalPreemptionCandidateOrderCache, opts.orderCache)
			features.SetFeatureGateDuringTest(t, features.PriorityBoost, true)
			for name, tc := range cases {
				t.Run(name, func(t *testing.T) {
					ctx, log := utiltesting.ContextWithLog(t)
					now := time.Date(2026, time.November, 12, 12, 0, 0, 0, time.UTC)
					// Creation time and UID deliberately favor the more recently reserved workload.
					older := utiltestingapi.MakeWorkload("older", "ns").UID("z").Priority(0).
						Creation(now.Add(-3*time.Minute)).Request(corev1.ResourceCPU, "1").
						SimpleReserveQuota(tc.olderCQ, "default", now.Add(-2*time.Minute))
					if tc.olderBoost != "" {
						older.Annotation(controllerconstants.PriorityBoostAnnotationKey, tc.olderBoost)
					}
					newer := utiltestingapi.MakeWorkload("newer", "ns").UID("a").Priority(0).
						Creation(now.Add(-4*time.Minute)).Request(corev1.ResourceCPU, "1").
						SimpleReserveQuota(tc.newerCQ, "default", now.Add(-time.Minute))
					cl := utiltesting.NewClientBuilder().WithLists(&kueue.WorkloadList{Items: []kueue.Workload{*older.Obj(), *newer.Obj()}}).Build()
					cache := schdcache.New(cl)
					cache.AddOrUpdateResourceFlavor(log, utiltestingapi.MakeResourceFlavor("default").Obj())
					for _, cq := range []*kueue.ClusterQueue{
						utiltestingapi.MakeClusterQueue("team").Cohort("root").
							ResourceGroup(*utiltestingapi.MakeFlavorQuotas("default").Resource(corev1.ResourceCPU, "2").Obj()).
							Preemption(kueue.ClusterQueuePreemption{
								WithinClusterQueue: kueue.PreemptionPolicyLowerPriority, ReclaimWithinCohort: kueue.PreemptionPolicyLowerPriority,
							}).Obj(),
						utiltestingapi.MakeClusterQueue("other-a").Cohort("root").
							ResourceGroup(*utiltestingapi.MakeFlavorQuotas("default").Resource(corev1.ResourceCPU, "0").Obj()).Obj(),
						utiltestingapi.MakeClusterQueue("other-b").Cohort("root").
							ResourceGroup(*utiltestingapi.MakeFlavorQuotas("default").Resource(corev1.ResourceCPU, "0").Obj()).Obj(),
					} {
						if err := cache.AddClusterQueue(ctx, cq); err != nil {
							t.Fatalf("add ClusterQueue: %v", err)
						}
					}
					if err := cache.AddOrUpdateCohort(utiltestingapi.MakeCohort("root").Obj()); err != nil {
						t.Fatalf("add Cohort: %v", err)
					}
					snapshot, err := cache.Snapshot(ctx)
					if err != nil {
						t.Fatalf("snapshot: %v", err)
					}
					var fs *config.FairSharing
					if opts.fairSharing {
						fs = &config.FairSharing{}
					}
					preemptor := New(cl, workload.Ordering{}, nil, fs, false, clocktesting.NewFakeClock(now), nil, nil, nil)
					info := workload.NewInfo(log, utiltestingapi.MakeWorkload("incoming", "ns").Priority(100).Request(corev1.ResourceCPU, "1").Obj())
					info.ClusterQueue = "team"
					targets := preemptor.GetTargets(ctx, *info, singlePodSetAssignment(flavorassigner.ResourceAssignment{
						corev1.ResourceCPU: {Name: "default", Mode: flavorassigner.Preempt},
					}), snapshot)
					if len(targets) != 1 {
						t.Fatalf("got %d targets, want one", len(targets))
					}
					if got := targets[0].WorkloadInfo.Obj.Name; got != tc.wantTarget {
						t.Errorf("target=%s, want %s", got, tc.wantTarget)
					}
				})
			}
		})
	}
}
