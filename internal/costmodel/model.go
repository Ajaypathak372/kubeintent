// Package costmodel provides a crude, v0 cost projection for workloads.
//
// Assumptions and limitations (intentionally simplistic for v0):
//   - Cost is estimated per-replica using a "cheapest fitting node" model.
//     There is no multi-tenant bin packing or shared-node accounting.
//   - The workload's resource fraction of the node is
//     max(cpu_fraction, memory_fraction). A workload using 50% of CPU
//     but 10% of memory is charged for 50%.
//   - 730 hours/month (365.25 days / 12 * 24).
//   - Spot, savings plans, reserved instances, and EBS/network costs are
//     not modeled. Projection is on-demand compute cost only.
//   - Node pools are matched by fit (CPU and memory), not by affinity,
//     taints, or topology.
package costmodel

import (
	"fmt"
	"os"
	"sort"

	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/yaml"
)

const hoursPerMonth = 730

// NodePool describes one class of node available for scheduling.
type NodePool struct {
	Name      string  `json:"name"`
	HourlyUSD float64 `json:"hourlyUSD"`
	CPUCores  float64 `json:"cpuCores"`
	MemoryGB  float64 `json:"memoryGB"`
}

// CostModel holds node pool definitions and projects workload costs.
type CostModel struct {
	NodePools []NodePool `json:"nodePools"`
}

// LoadFromFile reads a YAML cost-model config and returns a CostModel.
// Returns the raw os error (checkable with errors.Is) if the file is
// missing or unreadable.
func LoadFromFile(path string) (*CostModel, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cm CostModel
	if err := yaml.Unmarshal(data, &cm); err != nil {
		return nil, fmt.Errorf("parsing cost model config %s: %w", path, err)
	}
	if len(cm.NodePools) == 0 {
		return nil, fmt.Errorf("cost model config %s has no node pools", path)
	}
	return &cm, nil
}

// ProjectMonthlyCost estimates the monthly USD cost of running the given
// workload. It picks the cheapest node pool that can fit one replica's
// CPU and memory requests, computes the node-fraction consumed, and
// multiplies by hours-per-month and replica count.
func (m *CostModel) ProjectMonthlyCost(cpuRequest, memoryRequest resource.Quantity, replicas int32) (float64, error) {
	cpuCores := float64(cpuRequest.MilliValue()) / 1000.0
	memGB := float64(memoryRequest.Value()) / (1024 * 1024 * 1024)

	pools := make([]NodePool, len(m.NodePools))
	copy(pools, m.NodePools)
	sort.Slice(pools, func(i, j int) bool {
		return pools[i].HourlyUSD < pools[j].HourlyUSD
	})

	for _, pool := range pools {
		if cpuCores <= pool.CPUCores && memGB <= pool.MemoryGB {
			cpuFrac := cpuCores / pool.CPUCores
			memFrac := memGB / pool.MemoryGB
			frac := cpuFrac
			if memFrac > frac {
				frac = memFrac
			}
			return frac * pool.HourlyUSD * hoursPerMonth * float64(replicas), nil
		}
	}

	return 0, fmt.Errorf("no node pool can fit workload requiring %.3f CPU cores and %.3f GB memory", cpuCores, memGB)
}
