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

package configuration

import (
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	appsv1alpha1 "github.com/apecloud/kubeblocks/apis/apps/v1alpha1"
	"github.com/apecloud/kubeblocks/pkg/controller/component"
	intctrlutil "github.com/apecloud/kubeblocks/pkg/controllerutil"
	testapps "github.com/apecloud/kubeblocks/pkg/testutil/apps"
)

const clusterDefName = "test-clusterdef"
const clusterName = "test-cluster"
const mysqlCompDefName = "replicasets"
const scriptConfigName = "test-script-config"
const configSpecName = "test-config-spec"
const mysqlCompName = "mysql"
const mysqlConfigName = "mysql-component-config"
const mysqlConfigConstraintName = "mysql8.0-config-constraints"
const mysqlScriptsConfigName = "apecloud-mysql-scripts"
const testConfigContent = "test-config-content"

func allFieldsClusterDefObj(needCreate bool) *appsv1alpha1.ClusterDefinition {
	clusterDefObj := testapps.NewClusterDefFactory(clusterDefName).
		AddComponentDef(testapps.StatefulMySQLComponent, mysqlCompDefName).
		AddScriptTemplate(scriptConfigName, mysqlScriptsConfigName, testCtx.DefaultNamespace, testapps.ScriptsVolumeName, nil).
		AddConfigTemplate(configSpecName, mysqlConfigName, mysqlConfigConstraintName, testCtx.DefaultNamespace, testapps.ConfVolumeName).
		GetObject()
	if needCreate {
		Expect(testCtx.CreateObj(testCtx.Ctx, clusterDefObj)).Should(Succeed())
	}
	return clusterDefObj
}

func newAllFieldsClusterObj(clusterDefObj *appsv1alpha1.ClusterDefinition, needCreate bool) (*appsv1alpha1.Cluster, *appsv1alpha1.ClusterDefinition, types.NamespacedName) {
	// setup Cluster obj requires default ClusterDefinition object
	if clusterDefObj == nil {
		clusterDefObj = allFieldsClusterDefObj(needCreate)
	}
	pvcSpec := testapps.NewPVCSpec("1Gi")
	clusterObj := testapps.NewClusterFactory(testCtx.DefaultNamespace, clusterName, clusterDefObj.Name).
		AddComponent(mysqlCompName, mysqlCompDefName).SetReplicas(1).
		AddVolumeClaimTemplate(testapps.DataVolumeName, pvcSpec).
		AddComponentService(testapps.ServiceVPCName, corev1.ServiceTypeLoadBalancer).
		AddComponentService(testapps.ServiceInternetName, corev1.ServiceTypeLoadBalancer).
		GetObject()
	key := client.ObjectKeyFromObject(clusterObj)
	if needCreate {
		Expect(testCtx.CreateObj(testCtx.Ctx, clusterObj)).Should(Succeed())
	}
	return clusterObj, clusterDefObj, key
}

func newAllFieldsSynthesizedComponent(clusterDef *appsv1alpha1.ClusterDefinition, cluster *appsv1alpha1.Cluster) *component.SynthesizedComponent {
	reqCtx := intctrlutil.RequestCtx{
		Ctx: testCtx.Ctx,
		Log: logger,
	}
	synthesizeComp, err := component.BuildSynthesizedComponentWrapper4Test(reqCtx, testCtx.Cli, clusterDef, cluster, &cluster.Spec.ComponentSpecs[0])
	Expect(err).Should(Succeed())
	Expect(synthesizeComp).ShouldNot(BeNil())
	addTestVolumeMount(synthesizeComp.PodSpec, mysqlCompName)
	if len(synthesizeComp.ConfigTemplates) > 0 {
		configSpec := &synthesizeComp.ConfigTemplates[0]
		configSpec.ReRenderResourceTypes = []appsv1alpha1.RerenderResourceType{appsv1alpha1.ComponentVScaleType, appsv1alpha1.ComponentHScaleType}
	}
	return synthesizeComp
}

func newAllFieldsComponent(cluster *appsv1alpha1.Cluster) *appsv1alpha1.Component {
	comp, _ := component.BuildComponent(cluster, &cluster.Spec.ComponentSpecs[0], nil, nil)
	return comp
}

func addTestVolumeMount(spec *corev1.PodSpec, containerName string) {
	for i := range spec.Containers {
		container := &spec.Containers[i]
		if container.Name != containerName {
			continue
		}
		container.VolumeMounts = append(container.VolumeMounts,
			corev1.VolumeMount{
				Name:      testapps.ScriptsVolumeName,
				MountPath: "/scripts",
			}, corev1.VolumeMount{
				Name:      testapps.ConfVolumeName,
				MountPath: "/etc/config",
			})
		return
	}
}
