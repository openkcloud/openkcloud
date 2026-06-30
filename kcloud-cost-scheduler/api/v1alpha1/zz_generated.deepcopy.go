package v1alpha1

import (
	"encoding/json"

	"k8s.io/apimachinery/pkg/runtime"
)

func (in *CostPolicy) DeepCopyInto(out *CostPolicy) {
	copyJSON(in, out)
}

func (in *CostPolicy) DeepCopy() *CostPolicy {
	if in == nil {
		return nil
	}
	out := new(CostPolicy)
	in.DeepCopyInto(out)
	return out
}

func (in *CostPolicy) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

func (in *CostPolicyList) DeepCopyInto(out *CostPolicyList) {
	copyJSON(in, out)
}

func (in *CostPolicyList) DeepCopy() *CostPolicyList {
	if in == nil {
		return nil
	}
	out := new(CostPolicyList)
	in.DeepCopyInto(out)
	return out
}

func (in *CostPolicyList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

func (in *PowerPolicy) DeepCopyInto(out *PowerPolicy) {
	copyJSON(in, out)
}

func (in *PowerPolicy) DeepCopy() *PowerPolicy {
	if in == nil {
		return nil
	}
	out := new(PowerPolicy)
	in.DeepCopyInto(out)
	return out
}

func (in *PowerPolicy) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

func (in *PowerPolicyList) DeepCopyInto(out *PowerPolicyList) {
	copyJSON(in, out)
}

func (in *PowerPolicyList) DeepCopy() *PowerPolicyList {
	if in == nil {
		return nil
	}
	out := new(PowerPolicyList)
	in.DeepCopyInto(out)
	return out
}

func (in *PowerPolicyList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

func (in *WorkloadOptimizer) DeepCopyInto(out *WorkloadOptimizer) {
	copyJSON(in, out)
}

func (in *WorkloadOptimizer) DeepCopy() *WorkloadOptimizer {
	if in == nil {
		return nil
	}
	out := new(WorkloadOptimizer)
	in.DeepCopyInto(out)
	return out
}

func (in *WorkloadOptimizer) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

func (in *WorkloadOptimizerList) DeepCopyInto(out *WorkloadOptimizerList) {
	copyJSON(in, out)
}

func (in *WorkloadOptimizerList) DeepCopy() *WorkloadOptimizerList {
	if in == nil {
		return nil
	}
	out := new(WorkloadOptimizerList)
	in.DeepCopyInto(out)
	return out
}

func (in *WorkloadOptimizerList) DeepCopyObject() runtime.Object {
	return in.DeepCopy()
}

func copyJSON(in, out interface{}) {
	data, err := json.Marshal(in)
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, out)
}
