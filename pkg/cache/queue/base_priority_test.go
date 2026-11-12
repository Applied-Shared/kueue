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

package queue

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"

	kueue "sigs.k8s.io/kueue/apis/kueue/v1beta2"
	controllerconstants "sigs.k8s.io/kueue/pkg/controller/constants"
	"sigs.k8s.io/kueue/pkg/features"
	utiltesting "sigs.k8s.io/kueue/pkg/util/testing"
	utiltestingapi "sigs.k8s.io/kueue/pkg/util/testing/v1beta2"
	"sigs.k8s.io/kueue/pkg/workload"
)

func TestRequeuePreservesDifferentBasePriority(t *testing.T) {
	features.SetFeatureGateDuringTest(t, features.PriorityBoost, true)
	features.SetFeatureGateDuringTest(t, features.SchedulingEquivalenceHashing, true)
	cases := map[string]struct {
		enqueueAfterFailure bool
	}{
		"higher base priority is already pending":      {},
		"higher base priority arrives after rejection": {enqueueAfterFailure: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ctx, log := utiltesting.ContextWithLog(t)
			cq, err := newClusterQueue(ctx, utiltestingapi.MakeClusterQueue("cq").
				QueueingStrategy(kueue.BestEffortFIFO).Obj(), defaultOrdering, nil, nil)
			if err != nil {
				t.Fatalf("create ClusterQueue: %v", err)
			}
			now := time.Now()
			first := workload.NewInfo(log, utiltestingapi.MakeWorkload("first", defaultNamespace).
				Creation(now).Priority(0).
				Annotation(controllerconstants.PriorityBoostAnnotationKey, "100").
				Request(corev1.ResourceCPU, "1").Obj())
			second := workload.NewInfo(log, utiltestingapi.MakeWorkload("second", defaultNamespace).
				Creation(now.Add(time.Second)).Priority(100).
				Request(corev1.ResourceCPU, "1").Obj())
			cq.PushOrUpdate(first)
			if !tc.enqueueAfterFailure {
				cq.PushOrUpdate(second)
			}
			if got := cq.Pop(); got == nil || got.Obj.Name != first.Obj.Name {
				t.Fatal("expected the older workload with equal effective priority first")
			}

			// Against a running workload with base priority 50, only second is
			// eligible for same-queue LowerPriority preemption, despite equal
			// effective priorities. Rejecting first must not also reject second.
			cq.RequeueIfNotPresent(ctx, first, RequeueReasonPreemptionNoCandidates, "")
			if tc.enqueueAfterFailure {
				cq.PushOrUpdate(second)
			}
			if got := cq.Pop(); got == nil || got.Obj.Name != second.Obj.Name {
				t.Fatal("expected higher-base-priority workload to remain eligible for scheduling")
			}
		})
	}
}
