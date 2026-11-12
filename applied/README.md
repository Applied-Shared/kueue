<!--
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
-->

# Applied v0.19.10 Rollout Patches

The rollout keeps the existing mixed-resource reclamation and cohort-reclamation requeue timestamp fixes.
The timestamp adjustment is a FIFO tie-break, not a guarantee that a preemptor wins admission.
Qualify mixed CPU/GPU requests, unequal priorities, and staggered multi-victim eviction before enabling unified queues.
Evaluate the native `PrioritizePreemptorWorkloads` feature gate separately; this patch does not enable it.

## Temporary Base-Priority Eligibility

The Applied fork always compares `Workload.spec.priority` for same-queue `withinClusterQueue: LowerPriority` eligibility.
There is no per-ClusterQueue annotation or additional feature gate.
An age-demoted incumbent cannot be displaced by an equal-user-priority waiter.
The classical and Fair Sharing algorithms both apply the override.
Other preemption policies and cross-queue eligibility are unchanged.

This intentionally changes upstream boost-based time sharing for every queue using `withinClusterQueue: LowerPriority`, including legacy queues.
The behavior is tied to the Applied controller image, not the Lilypad unified-queue rollout flag.

Admission order and victim ranking still use effective priority, including `kueue.x-k8s.io/priority-boost` when its feature gate is enabled.
Lilypad should continue its external two-hour age adjustment, extend it to unified customer queues, and keep the boost range smaller than the gaps between user-priority classes.
There is no hardcoded two-hour, preemption-history, or oldest-runtime comparator in this patch.
The final native victim tie-break remains newest quota reservation first.

## Retirement

Replace this fork-wide compatibility override with upstream configurable base-priority eligibility once it supports the required native Fair Sharing integration.
Qualification must include mixed same-team/cross-team victim plans, not just isolated eligibility checks.
Remove the compatibility override after configuring the required policy in the upstream replacement and passing those cases.
The upstream direction is [KEP-13396](https://github.com/kubernetes-sigs/kueue/tree/main/keps/13396-configurable-preemptions).

Continue the existing reclamation discussion in [PR7392](https://github.com/kubernetes-sigs/kueue/pull/7392) rather than assuming the published fixes are already accepted upstream.
Remove either existing fix only after its regression is covered by a qualified upstream replacement.
