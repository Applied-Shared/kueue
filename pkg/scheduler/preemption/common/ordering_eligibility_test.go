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

package common

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/component-base/featuregate"

	kueue "sigs.k8s.io/kueue/apis/kueue/v1beta2"
	controllerconstants "sigs.k8s.io/kueue/pkg/controller/constants"
	"sigs.k8s.io/kueue/pkg/features"
	utiltesting "sigs.k8s.io/kueue/pkg/util/testing"
	utiltestingapi "sigs.k8s.io/kueue/pkg/util/testing/v1beta2"
	"sigs.k8s.io/kueue/pkg/workload"
)

func TestBoostRanksVictimsWithoutCreatingEligibility(t *testing.T) {
	features.SetFeatureGatesDuringTest(t, map[featuregate.Feature]bool{features.PriorityBoost: true})
	_, log := utiltesting.ContextWithLog(t)
	aged := utiltestingapi.MakeWorkload("aged", metav1.NamespaceDefault).Priority(0).Annotation(controllerconstants.PriorityBoostAnnotationKey, "-25").Obj()
	young := utiltestingapi.MakeWorkload("young", metav1.NamespaceDefault).Priority(0).Obj()
	incoming := utiltestingapi.MakeWorkload("incoming", metav1.NamespaceDefault).Priority(50).Obj()
	for _, victim := range []*kueue.Workload{aged, young} {
		if !SatisfiesPreemptionPolicy(log, incoming, victim, workload.Ordering{}, kueue.PreemptionPolicyLowerPriority) {
			t.Errorf("higher-base-priority incoming job must be eligible to preempt %s", victim.Name)
		}
	}
	if CandidatesOrdering(log, false, &workload.Info{Obj: aged, ClusterQueue: "team"}, &workload.Info{Obj: young, ClusterQueue: "team"}, "team", time.Unix(1700000000, 0)) >= 0 {
		t.Error("applied age boost must still prefer the aged eligible victim")
	}
	if SatisfiesPreemptionPolicy(log, young, aged, workload.Ordering{}, kueue.PreemptionPolicyLowerPriority) {
		t.Error("age boost must not permit equal-base-priority same-queue preemption")
	}
}
