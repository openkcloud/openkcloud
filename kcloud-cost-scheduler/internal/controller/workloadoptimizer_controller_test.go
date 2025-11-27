/*
Copyright 2025.

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

package controller

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	kcloudv1alpha1 "github.com/KETI-Cloud-Platform/k8s-workload-operator/api/v1alpha1"
	"github.com/KETI-Cloud-Platform/k8s-workload-operator/pkg/metrics"
	"github.com/KETI-Cloud-Platform/k8s-workload-operator/pkg/optimizer"
	"github.com/KETI-Cloud-Platform/k8s-workload-operator/pkg/scheduler"
)

func TestWorkloadOptimizerController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "WorkloadOptimizer Controller Suite")
}

var _ = Describe("WorkloadOptimizer Controller", func() {
	var (
		ctx               context.Context
		cancel            context.CancelFunc
		reconciler        *WorkloadOptimizerReconciler
		fakeClient        client.Client
		scheme            *runtime.Scheme
		optimizerEngine   *optimizer.Engine
		schedulerInstance *scheduler.Scheduler
		metricsCollector  *metrics.MetricsCollector
		testNamespace     string
		testWorkloadName  string
	)

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())

		// Setup scheme
		scheme = runtime.NewScheme()
		Expect(kcloudv1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(corev1.AddToScheme(scheme)).To(Succeed())

		// Setup fake client
		fakeClient = fake.NewClientBuilder().WithScheme(scheme).Build()

		// Initialize components
		optimizerEngine = optimizer.NewEngine()
		schedulerInstance = scheduler.NewScheduler()
		metricsCollector = metrics.NewMetricsCollector()

		// Setup reconciler
		reconciler = &WorkloadOptimizerReconciler{
			Client:    fakeClient,
			Scheme:    scheme,
			Optimizer: optimizerEngine,
			Scheduler: schedulerInstance,
			Metrics:   metricsCollector,
		}

		// Setup test data
		testNamespace = "test-namespace"
		testWorkloadName = "test-workload"

		// Setup logger
		ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	})

	AfterEach(func() {
		cancel()
	})

	Context("When reconciling a WorkloadOptimizer", func() {
		var workload *kcloudv1alpha1.WorkloadOptimizer

		BeforeEach(func() {
			workload = &kcloudv1alpha1.WorkloadOptimizer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testWorkloadName,
					Namespace: testNamespace,
				},
				Spec: kcloudv1alpha1.WorkloadOptimizerSpec{
					WorkloadType: "training",
					Priority:     5,
					Resources: kcloudv1alpha1.ResourceRequirements{
						CPU:    "2",
						Memory: "4Gi",
						GPU:    1,
						NPU:    0,
					},
					CostConstraints: &kcloudv1alpha1.CostConstraints{
						MaxCostPerHour: 10.0,
						BudgetLimit:    float64Ptr(1000.0),
					},
					PowerConstraints: &kcloudv1alpha1.PowerConstraints{
						MaxPowerUsage: 500.0,
					},
				},
			}
		})

		It("should successfully reconcile a new WorkloadOptimizer", func() {
			// Create the WorkloadOptimizer
			Expect(fakeClient.Create(ctx, workload)).To(Succeed())

			// Reconcile
			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      testWorkloadName,
					Namespace: testNamespace,
				},
			}
			result, err := reconciler.Reconcile(ctx, req)

			// Verify result
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))

			// Verify WorkloadOptimizer was updated
			var updatedWorkload kcloudv1alpha1.WorkloadOptimizer
			Expect(fakeClient.Get(ctx, req.NamespacedName, &updatedWorkload)).To(Succeed())
			Expect(updatedWorkload.Status.Phase).NotTo(BeEmpty())
		})

		It("should handle WorkloadOptimizer deletion", func() {
			// Create WorkloadOptimizer with finalizer
			workload.Finalizers = []string{"workloadoptimizer.kcloud.io/finalizer"}
			Expect(fakeClient.Create(ctx, workload)).To(Succeed())

			// Set deletion timestamp
			now := metav1.Now()
			workload.DeletionTimestamp = &now
			Expect(fakeClient.Update(ctx, workload)).To(Succeed())

			// Reconcile
			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      testWorkloadName,
					Namespace: testNamespace,
				},
			}
			result, err := reconciler.Reconcile(ctx, req)

			// Verify result
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Requeue).To(BeFalse())
		})

		It("should handle WorkloadOptimizer not found", func() {
			// Reconcile non-existent WorkloadOptimizer
			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      "non-existent",
					Namespace: testNamespace,
				},
			}
			result, err := reconciler.Reconcile(ctx, req)

			// Verify result
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Requeue).To(BeFalse())
		})

		It("should add finalizer to WorkloadOptimizer", func() {
			// Create WorkloadOptimizer without finalizer
			Expect(fakeClient.Create(ctx, workload)).To(Succeed())

			// Reconcile
			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      testWorkloadName,
					Namespace: testNamespace,
				},
			}
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Verify finalizer was added
			var updatedWorkload kcloudv1alpha1.WorkloadOptimizer
			Expect(fakeClient.Get(ctx, req.NamespacedName, &updatedWorkload)).To(Succeed())
			Expect(updatedWorkload.Finalizers).To(ContainElement("workloadoptimizer.kcloud.io/finalizer"))
		})

		It("should update WorkloadOptimizer status", func() {
			// Create WorkloadOptimizer
			Expect(fakeClient.Create(ctx, workload)).To(Succeed())

			// Reconcile
			req := ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      testWorkloadName,
					Namespace: testNamespace,
				},
			}
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Verify status was updated
			var updatedWorkload kcloudv1alpha1.WorkloadOptimizer
			Expect(fakeClient.Get(ctx, req.NamespacedName, &updatedWorkload)).To(Succeed())
			Expect(updatedWorkload.Status.Phase).NotTo(BeEmpty())
			Expect(updatedWorkload.Status.LastOptimizationTime).NotTo(BeNil())
		})
	})

	Context("When analyzing current state", func() {
		var workload *kcloudv1alpha1.WorkloadOptimizer

		BeforeEach(func() {
			workload = &kcloudv1alpha1.WorkloadOptimizer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testWorkloadName,
					Namespace: testNamespace,
				},
				Spec: kcloudv1alpha1.WorkloadOptimizerSpec{
					WorkloadType: "training",
					Priority:     5,
					Resources: kcloudv1alpha1.ResourceRequirements{
						CPU:    "2",
						Memory: "4Gi",
						GPU:    1,
						NPU:    0,
					},
				},
			}
		})

		It("should analyze current state successfully", func() {
			// Create test node
			node := &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-node",
				},
				Status: corev1.NodeStatus{
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("4"),
						corev1.ResourceMemory: resource.MustParse("8Gi"),
						"nvidia.com/gpu":      resource.MustParse("2"),
					},
				},
			}
			Expect(fakeClient.Create(ctx, node)).To(Succeed())

			// Create test pod
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: testNamespace,
					Labels: map[string]string{
						"workloadoptimizer.kcloud.io/name": testWorkloadName,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "test-container",
							Image: "test-image",
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("1"),
									corev1.ResourceMemory: resource.MustParse("2Gi"),
								},
							},
						},
					},
				},
			}
			Expect(fakeClient.Create(ctx, pod)).To(Succeed())

			// Analyze current state
			state, err := reconciler.analyzeCurrentState(ctx, workload)

			// Verify result
			Expect(err).NotTo(HaveOccurred())
			Expect(state).NotTo(BeNil())
			Expect(state.Pods).To(HaveLen(1))
			Expect(state.AvailableNodes).To(HaveLen(1))
		})

		It("should handle empty current state", func() {
			// Analyze current state with no resources
			state, err := reconciler.analyzeCurrentState(ctx, workload)

			// Verify result
			Expect(err).NotTo(HaveOccurred())
			Expect(state).NotTo(BeNil())
			Expect(state.Pods).To(BeEmpty())
			Expect(state.AvailableNodes).To(BeEmpty())
		})
	})

	Context("When performing optimization", func() {
		var workload *kcloudv1alpha1.WorkloadOptimizer

		BeforeEach(func() {
			workload = &kcloudv1alpha1.WorkloadOptimizer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testWorkloadName,
					Namespace: testNamespace,
				},
				Spec: kcloudv1alpha1.WorkloadOptimizerSpec{
					WorkloadType: "training",
					Priority:     5,
					Resources: kcloudv1alpha1.ResourceRequirements{
						CPU:    "2",
						Memory: "4Gi",
						GPU:    1,
						NPU:    0,
					},
					CostConstraints: &kcloudv1alpha1.CostConstraints{
						MaxCostPerHour: 10.0,
						BudgetLimit:    float64Ptr(1000.0),
					},
					PowerConstraints: &kcloudv1alpha1.PowerConstraints{
						MaxPowerUsage: 500.0,
					},
				},
			}
		})

		It("should perform optimization successfully", func() {
			// Create test current state
			currentState := &optimizer.WorkloadState{
				Pods:           []corev1.Pod{},
				AvailableNodes: []corev1.Node{},
			}

			// Perform optimization
			result, err := reconciler.performOptimization(ctx, workload, currentState)

			// Verify result
			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(result.Score).To(BeNumerically(">=", 0))
			Expect(result.Score).To(BeNumerically("<=", 1))
		})

		It("should handle optimization with constraints", func() {
			// Create test current state
			currentState := &optimizer.WorkloadState{
				Pods:           []corev1.Pod{},
				AvailableNodes: []corev1.Node{},
			}

			// Perform optimization
			result, err := reconciler.performOptimization(ctx, workload, currentState)

			// Verify result respects constraints
			Expect(err).NotTo(HaveOccurred())
			Expect(result).NotTo(BeNil())
			Expect(result.EstimatedCost).To(BeNumerically("<=", 10.0))
			Expect(result.EstimatedPower).To(BeNumerically("<=", 500.0))
		})
	})

	Context("When updating status", func() {
		var workload *kcloudv1alpha1.WorkloadOptimizer

		BeforeEach(func() {
			workload = &kcloudv1alpha1.WorkloadOptimizer{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testWorkloadName,
					Namespace: testNamespace,
				},
				Spec: kcloudv1alpha1.WorkloadOptimizerSpec{
					WorkloadType: "training",
					Priority:     5,
					Resources: kcloudv1alpha1.ResourceRequirements{
						CPU:    "2",
						Memory: "4Gi",
						GPU:    1,
						NPU:    0,
					},
				},
			}
			Expect(fakeClient.Create(ctx, workload)).To(Succeed())
		})

		It("should update status successfully", func() {
			// Create optimization result
			result := &optimizer.OptimizationResult{
				Score:                0.8,
				EstimatedCost:        5.0,
				EstimatedPower:       250.0,
				RequiresRescheduling: false,
				RecommendedReplicas:  1,
			}

			// Update status
			err := reconciler.updateStatus(ctx, workload, result)

			// Verify result
			Expect(err).NotTo(HaveOccurred())

			// Verify status was updated
			var updatedWorkload kcloudv1alpha1.WorkloadOptimizer
			Expect(fakeClient.Get(ctx, types.NamespacedName{
				Name:      testWorkloadName,
				Namespace: testNamespace,
			}, &updatedWorkload)).To(Succeed())

			Expect(updatedWorkload.Status.OptimizationScore).NotTo(BeNil())
			Expect(*updatedWorkload.Status.OptimizationScore).To(Equal(0.8))
			Expect(updatedWorkload.Status.CurrentCost).NotTo(BeNil())
			Expect(*updatedWorkload.Status.CurrentCost).To(Equal(5.0))
			Expect(updatedWorkload.Status.CurrentPower).NotTo(BeNil())
			Expect(*updatedWorkload.Status.CurrentPower).To(Equal(250.0))
		})

		It("should handle status update with nil values", func() {
			// Create optimization result with nil values
			result := &optimizer.OptimizationResult{
				Score:                0.0,
				EstimatedCost:        0.0,
				EstimatedPower:       0.0,
				RequiresRescheduling: false,
				RecommendedReplicas:  0,
			}

			// Update status
			err := reconciler.updateStatus(ctx, workload, result)

			// Verify result
			Expect(err).NotTo(HaveOccurred())
		})
	})
})

// Helper functions
func float64Ptr(f float64) *float64 {
	return &f
}
