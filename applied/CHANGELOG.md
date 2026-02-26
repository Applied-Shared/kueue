# Applied Kueue Changelog

## v0.15.3-applied-2

- **Git commit:** `f9df3c572be7b49da2bd651a2ef64fc5fe16a878`
- Fix preemption deadlock when workload needs both reclaim (TAS flavor) and borrow (non-TAS flavor) in the same cohort ([Applied-Shared/kueue#1](https://github.com/Applied-Shared/kueue/pull/1))

## v0.15.3-applied-1

- **Git commit:** `b887c66b7` (upstream v0.15.3 release)
- Initial Applied build of upstream v0.15.3 (no patches)
