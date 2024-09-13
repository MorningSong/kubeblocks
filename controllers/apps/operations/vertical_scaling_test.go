/*
Copyright (C) 2022-2024 ApeCloud Co., Ltd

This file is part of KubeBlocks project

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program.  If not, see <http://www.gnu.org/licenses/>.
*/

package operations

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	appsv1 "github.com/apecloud/kubeblocks/apis/apps/v1"
	appsv1alpha1 "github.com/apecloud/kubeblocks/apis/apps/v1alpha1"
	opsutil "github.com/apecloud/kubeblocks/controllers/apps/operations/util"
	"github.com/apecloud/kubeblocks/pkg/constant"
	intctrlutil "github.com/apecloud/kubeblocks/pkg/controllerutil"
	"github.com/apecloud/kubeblocks/pkg/generics"
	testapps "github.com/apecloud/kubeblocks/pkg/testutil/apps"
	testk8s "github.com/apecloud/kubeblocks/pkg/testutil/k8s"
)

var _ = Describe("VerticalScaling OpsRequest", func() {

	var (
		randomStr   = testCtx.GetRandomStr()
		compDefName = "test-compdef-" + randomStr
		clusterName = "test-cluster-" + randomStr
		reqCtx      intctrlutil.RequestCtx
	)

	cleanEnv := func() {
		reqCtx = intctrlutil.RequestCtx{Ctx: ctx}
		// must wait till resources deleted and no longer existed before the testcases start,
		// otherwise if later it needs to create some new resource objects with the same name,
		// in race conditions, it will find the existence of old objects, resulting failure to
		// create the new objects.
		By("clean resources")
		// delete cluster(and all dependent sub-resources), cluster definition
		testapps.ClearClusterResourcesWithRemoveFinalizerOption(&testCtx)

		// delete rest resources
		inNS := client.InNamespace(testCtx.DefaultNamespace)
		ml := client.HasLabels{testCtx.TestObjLabelKey}
		// namespaced
		testapps.ClearResources(&testCtx, generics.OpsRequestSignature, inNS, ml)
	}

	BeforeEach(cleanEnv)

	AfterEach(cleanEnv)

	Context("Test OpsRequest", func() {

		testVerticalScaling := func(verticalScaling []appsv1alpha1.VerticalScaling) *OpsResource {
			By("init operations resources ")
			opsRes, _, _ := initOperationsResources(compDefName, clusterName)

			By("create VerticalScaling ops")
			ops := testapps.NewOpsRequestObj("vertical-scaling-ops-"+randomStr, testCtx.DefaultNamespace,
				clusterName, appsv1alpha1.VerticalScalingType)

			ops.Spec.VerticalScalingList = verticalScaling
			opsRes.OpsRequest = testapps.CreateOpsRequest(ctx, testCtx, ops)
			// set ops phase to Pending
			opsRes.OpsRequest.Status.Phase = appsv1alpha1.OpsPendingPhase

			By("test save last configuration and OpsRequest phase is Running")
			_, err := GetOpsManager().Do(reqCtx, k8sClient, opsRes)
			Expect(err).ShouldNot(HaveOccurred())
			Eventually(testapps.GetOpsRequestPhase(&testCtx, client.ObjectKeyFromObject(ops))).Should(Equal(appsv1alpha1.OpsCreatingPhase))

			By("test vertical scale action function")
			vsHandler := verticalScalingHandler{}
			Expect(vsHandler.Action(reqCtx, k8sClient, opsRes)).Should(Succeed())
			_, _, err = vsHandler.ReconcileAction(reqCtx, k8sClient, opsRes)
			Expect(err).ShouldNot(HaveOccurred())
			return opsRes
		}

		It("vertical scaling by resource", func() {
			verticalScaling := []appsv1alpha1.VerticalScaling{
				{
					ComponentOps: appsv1alpha1.ComponentOps{ComponentName: defaultCompName},
					ResourceRequirements: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("400m"),
							corev1.ResourceMemory: resource.MustParse("300Mi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("400m"),
							corev1.ResourceMemory: resource.MustParse("300Mi"),
						},
					},
				},
			}
			testVerticalScaling(verticalScaling)
		})

		It("cancel vertical scaling opsRequest", func() {
			By("init operations resources with CLusterDefinition/Hybrid components Cluster/consensus Pods")
			reqCtx := intctrlutil.RequestCtx{Ctx: ctx}
			opsRes, _, _ := initOperationsResources(compDefName, clusterName)
			podList := initInstanceSetPods(ctx, k8sClient, opsRes)

			By("create VerticalScaling ops")
			ops := testapps.NewOpsRequestObj("vertical-scaling-ops-"+randomStr, testCtx.DefaultNamespace,
				clusterName, appsv1alpha1.VerticalScalingType)
			ops.Spec.VerticalScalingList = []appsv1alpha1.VerticalScaling{
				{
					ComponentOps: appsv1alpha1.ComponentOps{ComponentName: defaultCompName},
					ResourceRequirements: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("400m"),
							corev1.ResourceMemory: resource.MustParse("300Mi"),
						},
					},
				},
			}
			opsRes.OpsRequest = testapps.CreateOpsRequest(ctx, testCtx, ops)

			By("mock opsRequest is Running")
			mockComponentIsOperating(opsRes.Cluster, appsv1.UpdatingClusterCompPhase, defaultCompName)
			Expect(testapps.ChangeObjStatus(&testCtx, opsRes.OpsRequest, func() {
				opsRes.OpsRequest.Status.Phase = appsv1alpha1.OpsRunningPhase
				opsRes.OpsRequest.Status.StartTimestamp = metav1.Time{Time: time.Now()}
			})).ShouldNot(HaveOccurred())
			// wait 1 second for checking progress
			time.Sleep(time.Second)
			reCreatePod := func(pod *corev1.Pod) {
				pod.Kind = constant.PodKind
				testk8s.MockPodIsTerminating(ctx, testCtx, pod)
				testk8s.RemovePodFinalizer(ctx, testCtx, pod)
				testapps.MockInstanceSetPod(&testCtx, nil, clusterName, defaultCompName,
					pod.Name, "leader", "ReadWrite", ops.Spec.VerticalScalingList[0].ResourceRequirements)
			}

			By("mock podList[0] rolling update successfully by re-creating it")
			reCreatePod(podList[0])

			By("reconcile opsRequest status")
			_, err := GetOpsManager().Reconcile(reqCtx, k8sClient, opsRes)
			Expect(err).ShouldNot(HaveOccurred())

			By("the progress status of pod[0] should be Succeed ")
			progressDetails := opsRes.OpsRequest.Status.Components[defaultCompName].ProgressDetails
			progressDetail := findStatusProgressDetail(progressDetails, getProgressObjectKey(constant.PodKind, podList[0].Name))
			Expect(progressDetail.Status).Should(Equal(appsv1alpha1.SucceedProgressStatus))

			By("cancel verticalScaling opsRequest")
			cancelOpsRequest(reqCtx, opsRes, opsRes.OpsRequest.Status.StartTimestamp.Time)

			By("mock podList[0] rolled back successfully by re-creating it")
			reCreatePod(podList[0])

			By("reconcile opsRequest status after canceling opsRequest and component is Running after rolling update")
			mockConsensusCompToRunning(opsRes)
			_, err = GetOpsManager().Reconcile(reqCtx, k8sClient, opsRes)
			Expect(err).ShouldNot(HaveOccurred())

			By("expect for cancelling opsRequest successfully")
			opsRequest := opsRes.OpsRequest
			Expect(opsRequest.Status.Phase).Should(Equal(appsv1alpha1.OpsCancelledPhase))
			Expect(opsRequest.Status.Progress).Should(Equal("1/1"))
			progressDetails = opsRequest.Status.Components[defaultCompName].ProgressDetails
			Expect(len(progressDetails)).Should(Equal(1))
			progressDetail = findStatusProgressDetail(progressDetails, getProgressObjectKey(constant.PodKind, podList[0].Name))
			Expect(progressDetail.Status).Should(Equal(appsv1alpha1.SucceedProgressStatus))
			Expect(progressDetail.Message).Should(ContainSubstring("with rollback"))
		})

		It("force run vertical scaling opsRequests", func() {
			By("create the first vertical scaling")
			verticalScaling1 := []appsv1alpha1.VerticalScaling{
				{
					ComponentOps: appsv1alpha1.ComponentOps{ComponentName: defaultCompName},
					ResourceRequirements: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("400m"),
							corev1.ResourceMemory: resource.MustParse("300Mi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("400m"),
							corev1.ResourceMemory: resource.MustParse("300Mi"),
						},
					},
				},
			}
			opsRes := testVerticalScaling(verticalScaling1)
			firstOpsRequest := opsRes.OpsRequest.DeepCopy()
			Expect(testapps.ChangeObjStatus(&testCtx, firstOpsRequest, func() {
				firstOpsRequest.Status.Phase = appsv1alpha1.OpsRunningPhase
			})).Should(Succeed())

			By("create the second opsRequest with force flag")
			ops := testapps.NewOpsRequestObj("vertical-scaling-ops-1-"+randomStr, testCtx.DefaultNamespace,
				clusterName, appsv1alpha1.VerticalScalingType)
			ops.Spec.Force = true
			ops.Spec.VerticalScalingList = []appsv1alpha1.VerticalScaling{
				{
					ComponentOps: appsv1alpha1.ComponentOps{ComponentName: defaultCompName},
					ResourceRequirements: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("400m"),
							corev1.ResourceMemory: resource.MustParse("300Mi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("500m"),
							corev1.ResourceMemory: resource.MustParse("600Mi"),
						},
					},
				},
			}
			opsRes.OpsRequest = testapps.CreateOpsRequest(ctx, testCtx, ops)
			// set ops phase to Pending
			opsRes.OpsRequest.Status.Phase = appsv1alpha1.OpsPendingPhase

			By("mock the first reconcile and expect the opsPhase to Creating")
			_, err := GetOpsManager().Do(reqCtx, k8sClient, opsRes)
			Expect(err).ShouldNot(HaveOccurred())
			Eventually(testapps.GetOpsRequestPhase(&testCtx, client.ObjectKeyFromObject(ops))).Should(Equal(appsv1alpha1.OpsCreatingPhase))

			By("mock the next reconcile")
			_, err = GetOpsManager().Do(reqCtx, k8sClient, opsRes)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(testapps.ChangeObjStatus(&testCtx, ops, func() {
				ops.Status.Phase = appsv1alpha1.OpsRunningPhase
			})).Should(Succeed())

			By("the first operations request is expected to be aborted.")
			Eventually(testapps.GetOpsRequestPhase(&testCtx, client.ObjectKeyFromObject(firstOpsRequest))).Should(Equal(appsv1alpha1.OpsAbortedPhase))
			opsRequestSlice, _ := opsutil.GetOpsRequestSliceFromCluster(opsRes.Cluster)
			Expect(len(opsRequestSlice)).Should(Equal(1))
		})
	})
})
