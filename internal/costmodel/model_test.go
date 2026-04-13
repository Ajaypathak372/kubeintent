package costmodel

import (
	"math"
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
)

var testPools = []NodePool{
	{Name: "small", HourlyUSD: 0.096, CPUCores: 2, MemoryGB: 8},
	{Name: "large", HourlyUSD: 0.192, CPUCores: 4, MemoryGB: 16},
}

func TestProjectMonthlyCost_FitsSmallestNode(t *testing.T) {
	cm := &CostModel{NodePools: testPools}
	cpu := resource.MustParse("500m")
	mem := resource.MustParse("1Gi")

	cost, err := cm.ProjectMonthlyCost(cpu, mem, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// fraction = max(0.5/2, 1/8) = 0.25
	// cost = 0.25 * 0.096 * 730 * 1 = 17.52
	if math.Abs(cost-17.52) > 0.01 {
		t.Errorf("expected 17.52, got %.2f", cost)
	}
}

func TestProjectMonthlyCost_RequiresLargerNode(t *testing.T) {
	cm := &CostModel{NodePools: testPools}
	cpu := resource.MustParse("3")
	mem := resource.MustParse("12Gi")

	cost, err := cm.ProjectMonthlyCost(cpu, mem, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 3 CPU > 2 (small), fits large. fraction = max(3/4, 12/16) = 0.75
	// cost = 0.75 * 0.192 * 730 * 1 = 105.12
	if math.Abs(cost-105.12) > 0.01 {
		t.Errorf("expected 105.12, got %.2f", cost)
	}
}

func TestProjectMonthlyCost_NoFittingPool(t *testing.T) {
	cm := &CostModel{NodePools: testPools}
	cpu := resource.MustParse("8")
	mem := resource.MustParse("32Gi")

	_, err := cm.ProjectMonthlyCost(cpu, mem, 1)
	if err == nil {
		t.Fatal("expected error for workload that doesn't fit any pool")
	}
}

func TestProjectMonthlyCost_MultiReplica(t *testing.T) {
	cm := &CostModel{NodePools: testPools}
	cpu := resource.MustParse("500m")
	mem := resource.MustParse("1Gi")

	cost, err := cm.ProjectMonthlyCost(cpu, mem, 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Same as FitsSmallestNode * 3 = 52.56
	if math.Abs(cost-52.56) > 0.01 {
		t.Errorf("expected 52.56, got %.2f", cost)
	}
}
