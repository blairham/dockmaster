// SPDX-FileCopyrightText: 2026 Blair Hamilton
// SPDX-License-Identifier: Apache-2.0

package views

// Usage sums the CPU/MEM poll's last samples over the running containers:
// docker stats' CPU % (per core, so up to CPUs × 100) and memory in use.
// running counts the running containers and sampled those with a sample,
// so a caller can tell "nothing running" from "no sample yet". It reads
// only what the poll already has — no daemon call.
func (v *ContainersView) Usage() (cpu float64, mem int64, running, sampled int) {
	for _, c := range v.all {
		if !c.Running() {
			continue
		}
		running++
		if s, ok := v.stats[c.ID]; ok && s.OK {
			sampled++
			cpu += s.CPUPerc
			mem += s.MemUsage
		}
	}
	return cpu, mem, running, sampled
}

// ThresholdText renders text in orange past warn and red past critical,
// as the CPU% and MEM columns are.
func ThresholdText(text string, pct, warn, critical float64) string {
	return thresholdText(text, pct, warn, critical)
}
