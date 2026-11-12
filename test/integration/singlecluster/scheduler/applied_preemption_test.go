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

package scheduler

import (
	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kueue "sigs.k8s.io/kueue/apis/kueue/v1beta2"
	controllerconstants "sigs.k8s.io/kueue/pkg/controller/constants"
	"sigs.k8s.io/kueue/pkg/features"
	utiltestingapi "sigs.k8s.io/kueue/pkg/util/testing/v1beta2"
	"sigs.k8s.io/kueue/test/util/behavioral"
)

var _ = ginkgo.Describe("Applied base-priority preemption", func() {
	var (
		ns     *corev1.Namespace
		flavor *kueue.ResourceFlavor
		cq     *kueue.ClusterQueue
		queue  *kueue.LocalQueue
	)

	ginkgo.BeforeEach(func() {
		features.SetFeatureGateDuringTest(ginkgo.GinkgoTB(), features.PriorityBoost, true)
		ns = behavioral.CreateNamespaceFromPrefixWithLog(ctx, k8sClient, "applied-preemption-")
		flavor = utiltestingapi.MakeResourceFlavor("applied-default").Obj()
		behavioral.MustCreate(ctx, k8sClient, flavor)
		cq = utiltestingapi.MakeClusterQueue("applied-team").
			ResourceGroup(*utiltestingapi.MakeFlavorQuotas(flavor.Name).Resource(corev1.ResourceCPU, "1").Obj()).
			Preemption(kueue.ClusterQueuePreemption{WithinClusterQueue: kueue.PreemptionPolicyLowerPriority}).Obj()
		behavioral.MustCreate(ctx, k8sClient, cq)
		queue = utiltestingapi.MakeLocalQueue("queue", ns.Name).ClusterQueue(cq.Name).Obj()
		behavioral.MustCreate(ctx, k8sClient, queue)
	})

	ginkgo.AfterEach(func() {
		gomega.Expect(behavioral.DeleteWorkloadsInNamespace(ctx, k8sClient, ns)).To(gomega.Succeed())
		behavioral.ExpectObjectToBeDeleted(ctx, k8sClient, queue, true)
		behavioral.ExpectObjectToBeDeleted(ctx, k8sClient, cq, true)
		behavioral.ExpectObjectToBeDeleted(ctx, k8sClient, flavor, true)
		gomega.Expect(behavioral.DeleteNamespace(ctx, k8sClient, ns)).To(gomega.Succeed())
	})

	ginkgo.It("keeps age-demoted equal-priority work running without a queue opt-in", func() {
		incumbent := utiltestingapi.MakeWorkload("incumbent", ns.Name).
			Queue(kueue.LocalQueueName(queue.Name)).Priority(0).
			Annotation(controllerconstants.PriorityBoostAnnotationKey, "-25").
			Request(corev1.ResourceCPU, "1").Obj()
		behavioral.MustCreate(ctx, k8sClient, incumbent)
		behavioral.ExpectWorkloadsToHaveQuotaReservation(ctx, k8sClient, cq.Name, incumbent)

		waiting := utiltestingapi.MakeWorkload("waiting", ns.Name).
			Queue(kueue.LocalQueueName(queue.Name)).Priority(0).
			Request(corev1.ResourceCPU, "1").Obj()
		behavioral.MustCreate(ctx, k8sClient, waiting)
		behavioral.ExpectWorkloadsToBePending(ctx, k8sClient, waiting)
		gomega.Consistently(func(g gomega.Gomega) {
			var current kueue.Workload
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(incumbent), &current)).To(gomega.Succeed())
			g.Expect(apimeta.IsStatusConditionTrue(current.Status.Conditions, kueue.WorkloadEvicted)).To(gomega.BeFalse())
		}, behavioral.ConsistentDuration, behavioral.Interval).Should(gomega.Succeed())
	})

	ginkgo.It("allows higher user priority to replace lower-priority work with an age-protection boost", func() {
		incumbent := utiltestingapi.MakeWorkload("incumbent", ns.Name).
			Queue(kueue.LocalQueueName(queue.Name)).Priority(0).
			Annotation(controllerconstants.PriorityBoostAnnotationKey, "25").
			Request(corev1.ResourceCPU, "1").Obj()
		behavioral.MustCreate(ctx, k8sClient, incumbent)
		behavioral.ExpectWorkloadsToHaveQuotaReservation(ctx, k8sClient, cq.Name, incumbent)

		higher := utiltestingapi.MakeWorkload("higher", ns.Name).
			Queue(kueue.LocalQueueName(queue.Name)).Priority(50).
			Request(corev1.ResourceCPU, "1").Obj()
		behavioral.MustCreate(ctx, k8sClient, higher)
		behavioral.ExpectWorkloadsToBePreempted(ctx, k8sClient, incumbent)
		behavioral.FinishEvictionForWorkloads(ctx, k8sClient, incumbent)
		behavioral.ExpectWorkloadsToHaveQuotaReservation(ctx, k8sClient, cq.Name, higher)
	})
})
