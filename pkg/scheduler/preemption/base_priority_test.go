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

func TestBasePriorityWithinClusterQueue(t *testing.T) {
	cases := map[string]struct {
		otherQueue        bool
		preemptorPriority int32
		candidatePriority int32
		preemptorBoost    string
		candidateBoost    string
		policy            kueue.PreemptionPolicy
		boostDisabled     bool
		wantTarget        bool
	}{
		"same queue rejects age-demoted equal priority": {
			candidateBoost: "-25",
		},
		"same queue rejects boosted equal-priority preemptor": {
			preemptorBoost: "5",
		},
		"same queue allows higher base priority despite victim boost": {
			preemptorPriority: 100, candidateBoost: "150", wantTarget: true,
		},
		"same queue rejects lower base priority despite preemptor boost": {
			candidatePriority: 100, preemptorBoost: "150",
		},
		"LowerOrNewerEqualPriority keeps effective-priority eligibility": {
			candidatePriority: 100, preemptorBoost: "150",
			policy: kueue.PreemptionPolicyLowerOrNewerEqualPriority, wantTarget: true,
		},
		"Never remains unchanged": {
			preemptorPriority: 100, policy: kueue.PreemptionPolicyNever,
		},
		"Any remains unchanged": {
			candidatePriority: 100, policy: kueue.PreemptionPolicyAny, wantTarget: true,
		},
		"cross-queue boosted eligibility remains unchanged": {
			otherQueue: true, candidatePriority: 100, preemptorBoost: "150", wantTarget: true,
		},
		"cross-queue boosted protection remains unchanged": {
			otherQueue: true, preemptorPriority: 100, candidateBoost: "150",
		},
		"cross-queue Any can reclaim from higher base priority": {
			otherQueue: true, candidatePriority: 100, policy: kueue.PreemptionPolicyAny, wantTarget: true,
		},
		"disabled boost ignores demotion": {
			candidateBoost: "-25", boostDisabled: true,
		},
		"disabled boost still permits higher base priority": {
			preemptorPriority: 100, candidateBoost: "150", boostDisabled: true, wantTarget: true,
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
			for name, tc := range cases {
				t.Run(name, func(t *testing.T) {
					features.SetFeatureGateDuringTest(t, features.PriorityBoost, !tc.boostDisabled)
					ctx, log := utiltesting.ContextWithLog(t)
					now := time.Date(2026, time.November, 10, 12, 0, 0, 0, time.UTC)
					candidateCQ := kueue.ClusterQueueReference("team")
					if tc.otherQueue {
						candidateCQ = "other"
					}
					candidate := utiltestingapi.MakeWorkload("candidate", "ns").
						Priority(tc.candidatePriority).
						Annotation(controllerconstants.PriorityBoostAnnotationKey, tc.candidateBoost).
						Request(corev1.ResourceCPU, "1").
						SimpleReserveQuota(candidateCQ, "default", now).
						Obj()
					incoming := utiltestingapi.MakeWorkload("incoming", "ns").
						Priority(tc.preemptorPriority).
						Annotation(controllerconstants.PriorityBoostAnnotationKey, tc.preemptorBoost).
						Request(corev1.ResourceCPU, "1").Obj()
					policy := tc.policy
					if policy == "" {
						policy = kueue.PreemptionPolicyLowerPriority
					}
					team := utiltestingapi.MakeClusterQueue("team").Cohort("root").
						ResourceGroup(*utiltestingapi.MakeFlavorQuotas("default").Resource(corev1.ResourceCPU, "1").Obj()).
						Preemption(kueue.ClusterQueuePreemption{WithinClusterQueue: policy, ReclaimWithinCohort: policy})
					cl := utiltesting.NewClientBuilder().WithLists(&kueue.WorkloadList{Items: []kueue.Workload{*candidate}}).Build()
					cache := schdcache.New(cl)
					cache.AddOrUpdateResourceFlavor(log, utiltestingapi.MakeResourceFlavor("default").Obj())
					for _, cq := range []*kueue.ClusterQueue{
						team.Obj(),
						utiltestingapi.MakeClusterQueue("other").Cohort("root").
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
						fs = &config.FairSharing{PreemptionStrategies: []config.PreemptionStrategy{config.LessThanOrEqualToFinalShare}}
					}
					preemptor := New(cl, workload.Ordering{}, nil, fs, false, clocktesting.NewFakeClock(now), nil, nil, nil)
					info := workload.NewInfo(log, incoming)
					info.ClusterQueue = "team"
					assignment := singlePodSetAssignment(flavorassigner.ResourceAssignment{
						corev1.ResourceCPU: {Name: "default", Mode: flavorassigner.Preempt},
					})
					targets := preemptor.GetTargets(ctx, *info, assignment, snapshot)
					if got := len(targets) == 1; got != tc.wantTarget || len(targets) > 1 {
						t.Fatalf("got %d targets, want target=%t", len(targets), tc.wantTarget)
					}
					if len(targets) == 1 && targets[0].WorkloadInfo.Obj.Name != candidate.Name {
						t.Fatalf("unexpected target: %s", targets[0].WorkloadInfo.Obj.Name)
					}
				})
			}
		})
	}
}

func TestBasePriorityFairSharingMixedTargetsWithReevaluation(t *testing.T) {
	features.SetFeatureGateDuringTest(t, features.PriorityBoost, true)
	features.SetFeatureGateDuringTest(t, features.FairSharingReevaluatePreemptionCandidates, true)
	features.SetFeatureGateDuringTest(t, features.FairSharingVerifyFinalTargets, true)
	ctx, log := utiltesting.ContextWithLog(t)
	now := time.Date(2026, time.November, 10, 12, 0, 0, 0, time.UTC)
	admitted := []kueue.Workload{
		*utiltestingapi.MakeWorkload("own", "ns").Priority(0).
			Annotation(controllerconstants.PriorityBoostAnnotationKey, "150").
			Request(corev1.ResourceCPU, "2").SimpleReserveQuota("team", "default", now).Obj(),
	}
	for _, name := range []string{"borrower-1", "borrower-2", "borrower-3"} {
		admitted = append(admitted, *utiltestingapi.MakeWorkload(name, "ns").Priority(200).
			Request(corev1.ResourceCPU, "2").SimpleReserveQuota("other", "default", now).Obj())
	}
	cl := utiltesting.NewClientBuilder().WithLists(&kueue.WorkloadList{Items: admitted}).Build()
	cache := schdcache.New(cl)
	cache.AddOrUpdateResourceFlavor(log, utiltestingapi.MakeResourceFlavor("default").Obj())
	for _, cq := range []*kueue.ClusterQueue{
		utiltestingapi.MakeClusterQueue("team").Cohort("root").
			ResourceGroup(*utiltestingapi.MakeFlavorQuotas("default").Resource(corev1.ResourceCPU, "2").Obj()).
			Preemption(kueue.ClusterQueuePreemption{
				WithinClusterQueue:  kueue.PreemptionPolicyLowerPriority,
				ReclaimWithinCohort: kueue.PreemptionPolicyAny,
			}).Obj(),
		utiltestingapi.MakeClusterQueue("other").Cohort("root").
			ResourceGroup(*utiltestingapi.MakeFlavorQuotas("default").Resource(corev1.ResourceCPU, "2").Obj()).Obj(),
		utiltestingapi.MakeClusterQueue("shared").Cohort("root").
			ResourceGroup(*utiltestingapi.MakeFlavorQuotas("default").Resource(corev1.ResourceCPU, "4").Obj()).Obj(),
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
	incoming := utiltestingapi.MakeWorkload("incoming", "ns").Priority(100).Request(corev1.ResourceCPU, "3").Obj()
	info := workload.NewInfo(log, incoming)
	info.ClusterQueue = "team"
	preemptor := New(cl, workload.Ordering{}, nil,
		&config.FairSharing{PreemptionStrategies: []config.PreemptionStrategy{config.LessThanOrEqualToFinalShare}},
		false, clocktesting.NewFakeClock(now), nil, nil, nil)
	targets := preemptor.GetTargets(ctx, *info, singlePodSetAssignment(flavorassigner.ResourceAssignment{
		corev1.ResourceCPU: {Name: "default", Mode: flavorassigner.Preempt},
	}), snapshot)
	counts := make(map[kueue.ClusterQueueReference]int)
	for _, target := range targets {
		counts[target.WorkloadInfo.ClusterQueue]++
	}
	if len(targets) != 2 || counts["team"] != 1 || counts["other"] != 1 {
		t.Fatalf("target counts=%v, want one own-team and one other-team victim", counts)
	}
}
