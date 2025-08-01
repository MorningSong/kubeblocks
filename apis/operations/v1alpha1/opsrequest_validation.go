/*
Copyright (C) 2022-2025 ApeCloud Co., Ltd

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

package v1alpha1

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"

	appsv1 "github.com/apecloud/kubeblocks/apis/apps/v1"
	"github.com/apecloud/kubeblocks/pkg/constant"
)

const (
	KBSwitchoverCandidateInstanceForAnyPod = "*"
)

// log is for logging in this package.
var (
	opsRequestAnnotationKey = "kubeblocks.io/ops-request"
	// OpsRequestBehaviourMapper records the opsRequest behaviour according to the OpsType.
	OpsRequestBehaviourMapper = map[OpsType]OpsRequestBehaviour{}
)

// IsComplete checks if opsRequest has been completed.
func (r *OpsRequest) IsComplete(phases ...OpsPhase) bool {
	completedPhase := func(phase OpsPhase) bool {
		return slices.Contains([]OpsPhase{OpsCancelledPhase, OpsSucceedPhase, OpsAbortedPhase, OpsFailedPhase}, phase)
	}
	if len(phases) == 0 {
		return completedPhase(r.Status.Phase)
	}
	for i := range phases {
		if !completedPhase(phases[i]) {
			return false
		}
	}
	return true
}

// Force checks if the current opsRequest can be forcibly executed
func (r *OpsRequest) Force() bool {
	// ops of type 'Start' do not support force execution.
	return r.Spec.Force && r.Spec.Type != StartType
}

// Validate validates OpsRequest
func (r *OpsRequest) Validate(ctx context.Context,
	k8sClient client.Client,
	cluster *appsv1.Cluster,
	needCheckClusterPhase bool) error {
	if needCheckClusterPhase {
		if err := r.ValidateClusterPhase(cluster); err != nil {
			return err
		}
	}
	return r.ValidateOps(ctx, k8sClient, cluster)
}

// ValidateClusterPhase validates whether the current cluster state supports the OpsRequest
func (r *OpsRequest) ValidateClusterPhase(cluster *appsv1.Cluster) error {
	opsBehaviour := OpsRequestBehaviourMapper[r.Spec.Type]
	// if the OpsType has no cluster phases, ignore it
	if len(opsBehaviour.FromClusterPhases) == 0 {
		return nil
	}
	if r.Force() {
		return nil
	}
	// validate whether existing the same type OpsRequest
	var (
		opsRequestValue string
		opsRecorders    []OpsRecorder
		ok              bool
	)
	if opsRequestValue, ok = cluster.Annotations[opsRequestAnnotationKey]; ok {
		// opsRequest annotation value in cluster to map
		if err := json.Unmarshal([]byte(opsRequestValue), &opsRecorders); err != nil {
			return err
		}
	}
	// check if the opsRequest can be executed in the current cluster.
	if slices.Contains(opsBehaviour.FromClusterPhases, cluster.Status.Phase) {
		return nil
	}
	var opsRecord *OpsRecorder
	for _, v := range opsRecorders {
		if v.Name == r.Name {
			opsRecord = &v
			break
		}
	}
	// check if this opsRequest needs to verify cluster phase before opsRequest starts running.
	needCheck := len(opsRecorders) == 0 || (opsRecord != nil && !opsRecord.InQueue)
	if needCheck {
		return fmt.Errorf("OpsRequest.spec.type=%s is forbidden when Cluster.status.phase=%s", r.Spec.Type, cluster.Status.Phase)
	}
	return nil
}

// ValidateOps validates ops attributes
func (r *OpsRequest) ValidateOps(ctx context.Context,
	k8sClient client.Client,
	cluster *appsv1.Cluster) error {
	// Check whether the corresponding attribute is legal according to the operation type
	switch r.Spec.Type {
	case UpgradeType:
		return r.validateUpgrade(ctx, k8sClient, cluster)
	case VerticalScalingType:
		return r.validateVerticalScaling(cluster)
	case HorizontalScalingType:
		return r.validateHorizontalScaling(ctx, k8sClient, cluster)
	case VolumeExpansionType:
		return r.validateVolumeExpansion(ctx, k8sClient, cluster)
	case RestartType:
		return r.validateRestart(cluster)
	case SwitchoverType:
		return r.validateSwitchover(cluster)
	case ExposeType:
		return r.validateExpose(ctx, cluster)
	case RebuildInstanceType:
		return r.validateRebuildInstance(cluster)
	}
	return nil
}

// validateExpose validates expose api when spec.type is Expose
func (r *OpsRequest) validateExpose(_ context.Context, cluster *appsv1.Cluster) error {
	exposeList := r.Spec.ExposeList
	if exposeList == nil {
		return notEmptyError("spec.expose")
	}

	var compOpsList []ComponentOps
	counter := 0
	for _, v := range exposeList {
		if len(v.ComponentName) > 0 {
			compOpsList = append(compOpsList, ComponentOps{ComponentName: v.ComponentName})
			continue
		} else {
			counter++
		}
		if counter > 1 {
			return fmt.Errorf("at most one spec.expose.componentName can be empty")
		}
		if v.Switch == EnableExposeSwitch {
			for _, opssvc := range v.Services {
				if len(opssvc.Ports) == 0 {
					return fmt.Errorf("spec.expose.services.ports must be specified when componentName is empty")
				}
			}
		}
	}
	return r.checkComponentExistence(cluster, compOpsList)
}

func (r *OpsRequest) validateRebuildInstance(cluster *appsv1.Cluster) error {
	rebuildFrom := r.Spec.RebuildFrom
	if len(rebuildFrom) == 0 {
		return notEmptyError("spec.rebuildFrom")
	}
	var compOpsList []ComponentOps
	for _, v := range rebuildFrom {
		compOpsList = append(compOpsList, v.ComponentOps)
	}
	return r.checkComponentExistence(cluster, compOpsList)
}

// validateUpgrade validates spec.restart
func (r *OpsRequest) validateRestart(cluster *appsv1.Cluster) error {
	restartList := r.Spec.RestartList
	if len(restartList) == 0 {
		return notEmptyError("spec.restart")
	}
	return r.checkComponentExistence(cluster, restartList)
}

// validateUpgrade validates spec.clusterOps.upgrade
func (r *OpsRequest) validateUpgrade(ctx context.Context, k8sClient client.Client, cluster *appsv1.Cluster) error {
	upgrade := r.Spec.Upgrade
	if upgrade == nil {
		return notEmptyError("spec.upgrade")
	}
	if len(r.Spec.Upgrade.Components) == 0 {
		return notEmptyError("spec.upgrade.components")
	}
	return nil
}

// validateVerticalScaling validates api when spec.type is VerticalScaling
func (r *OpsRequest) validateVerticalScaling(cluster *appsv1.Cluster) error {
	verticalScalingList := r.Spec.VerticalScalingList
	if len(verticalScalingList) == 0 {
		return notEmptyError("spec.verticalScaling")
	}

	// validate resources is legal and get component name slice
	compOpsList := make([]ComponentOps, len(verticalScalingList))
	for i, v := range verticalScalingList {
		compOpsList[i] = v.ComponentOps
		var instanceNames []string
		for j := range v.Instances {
			instanceNames = append(instanceNames, v.Instances[j].Name)
		}
		if err := r.checkInstanceTemplate(cluster, v.ComponentOps, instanceNames); err != nil {
			return err
		}
		if invalidValue, err := validateVerticalResourceList(v.Requests); err != nil {
			return invalidValueError(invalidValue, err.Error())
		}
		if invalidValue, err := validateVerticalResourceList(v.Limits); err != nil {
			return invalidValueError(invalidValue, err.Error())
		}
		if invalidValue, err := compareRequestsAndLimits(v.ResourceRequirements); err != nil {
			return invalidValueError(invalidValue, err.Error())
		}
	}
	return r.checkComponentExistence(cluster, compOpsList)
}

// compareRequestsAndLimits compares the resource requests and limits
func compareRequestsAndLimits(resources corev1.ResourceRequirements) (string, error) {
	requests := resources.Requests
	limits := resources.Limits
	if requests == nil || limits == nil {
		return "", nil
	}
	for k, v := range requests {
		if limitQuantity, ok := limits[k]; !ok {
			continue
		} else if compareQuantity(&v, &limitQuantity) {
			return v.String(), errors.New(fmt.Sprintf(`must be less than or equal to %s limit`, k))
		}
	}
	return "", nil
}

// compareQuantity compares requests quantity and limits quantity
func compareQuantity(requestQuantity, limitQuantity *resource.Quantity) bool {
	return requestQuantity != nil && limitQuantity != nil && requestQuantity.Cmp(*limitQuantity) > 0
}

// validateHorizontalScaling validates api when spec.type is HorizontalScaling
func (r *OpsRequest) validateHorizontalScaling(ctx context.Context, cli client.Client, cluster *appsv1.Cluster) error {
	horizontalScalingList := r.Spec.HorizontalScalingList
	if len(horizontalScalingList) == 0 {
		return notEmptyError("spec.horizontalScaling")
	}
	compOpsList := make([]ComponentOps, len(horizontalScalingList))
	hScaleMap := map[string]HorizontalScaling{}
	for i, v := range horizontalScalingList {
		compOpsList[i] = v.ComponentOps
		hScaleMap[v.ComponentName] = horizontalScalingList[i]
	}
	if err := r.checkComponentExistence(cluster, compOpsList); err != nil {
		return err
	}
	for _, comSpec := range cluster.Spec.ComponentSpecs {
		if hScale, ok := hScaleMap[comSpec.Name]; ok {
			// Default values if no limit is found
			minNum, maxNum := 1, 16384
			if comSpec.ComponentDef != "" {
				compDef := &appsv1.ComponentDefinition{}
				if err := cli.Get(ctx, client.ObjectKey{Name: comSpec.ComponentDef, Namespace: r.Namespace}, compDef); err != nil {
					return err
				}
				if compDef.Spec.ReplicasLimit != nil {
					minNum = int(compDef.Spec.ReplicasLimit.MinReplicas)
					maxNum = int(compDef.Spec.ReplicasLimit.MaxReplicas)
				}
			}
			if err := r.validateHorizontalScalingSpec(hScale, comSpec, cluster.Name, false, maxNum, minNum); err != nil {
				return err
			}
		}

	}
	for _, spec := range cluster.Spec.Shardings {
		if hScale, ok := hScaleMap[spec.Name]; ok {
			// Default values if no limit is found
			minNum, maxNum := 1, 2048
			if spec.ShardingDef != "" {
				shardingDef := &appsv1.ShardingDefinition{}
				if err := cli.Get(ctx, types.NamespacedName{Name: spec.ShardingDef, Namespace: r.Namespace}, shardingDef); err != nil {
					return err
				}
				if shardingDef.Spec.ShardsLimit != nil {
					minNum = int(shardingDef.Spec.ShardsLimit.MinShards)
					maxNum = int(shardingDef.Spec.ShardsLimit.MaxShards)
				}
			}
			if err := r.validateHorizontalScalingSpec(hScale, spec.Template, cluster.Name, true, maxNum, minNum); err != nil {
				return err
			}
		}
	}
	return nil
}

// CountOfflineOrOnlineInstances calculate the number of instances that need to be brought online and offline corresponding to the instance template name.
func (r *OpsRequest) CountOfflineOrOnlineInstances(clusterName, componentName string, hScaleInstanceNames []string) map[string]int32 {
	offlineOrOnlineInsCountMap := map[string]int32{}
	for _, insName := range hScaleInstanceNames {
		insTplName := appsv1.GetInstanceTemplateName(clusterName, componentName, insName)
		offlineOrOnlineInsCountMap[insTplName]++
	}
	return offlineOrOnlineInsCountMap
}

func (r *OpsRequest) validateHorizontalScalingSpec(hScale HorizontalScaling, compSpec appsv1.ClusterComponentSpec, clusterName string, isSharding bool, maxReplicasOrShards int, minReplicasOrShards int) error {
	scaleIn := hScale.ScaleIn
	scaleOut := hScale.ScaleOut
	// Validate Shards if present
	if hScale.Shards != nil {
		return r.validateShards(hScale, isSharding, minReplicasOrShards, maxReplicasOrShards)
	}
	// Use last configuration if available
	if err := r.applyLastConfiguration(hScale.ComponentName, &compSpec); err != nil {
		return err
	}

	compInstanceTplReplicas := map[string]int32{}
	for _, instance := range compSpec.Instances {
		compInstanceTplReplicas[instance.Name] = instance.GetReplicas()
	}

	// Define the scaling operation validation function
	validateHScaleOperation := func(replicaChanger ReplicaChanger, newInstances []appsv1.InstanceTemplate, instanceNames []string, isScaleIn bool) error {
		var operationPrefix, instanceField string
		if isScaleIn {
			operationPrefix = "ScaleIn:"
			instanceField = "onlineInstancesToOffline"
		} else {
			operationPrefix = "ScaleOut:"
			instanceField = "offlineInstancesToOnline"
		}

		if isSharding && len(instanceNames) > 0 {
			return fmt.Errorf(`cannot specify %s for a sharding component "%s"`, instanceField, hScale.ComponentName)
		}

		// Rule 1: Check if the length of the instance names is greater than the configured replicaChanges
		if replicaChanger.ReplicaChanges != nil && len(instanceNames) > int(*replicaChanger.ReplicaChanges) {
			return fmt.Errorf(`the length of %s can't be greater than the "replicaChanges" for the component`, instanceField)
		}

		// Track the count of offline/online instances
		offlineOrOnlineInsCountMap := r.CountOfflineOrOnlineInstances(clusterName, hScale.ComponentName, instanceNames)
		insTplChangeMap := make(map[string]int32)
		totalReplicaChanges := int32(0)

		// Rule 2: Validate each instance template and ensure replicaChanges are valid
		for _, instance := range replicaChanger.Instances {
			instanceReplicas, exists := compInstanceTplReplicas[instance.Name]
			if !exists {
				return fmt.Errorf(`%s cannot find the instance template "%s" in component "%s"`, operationPrefix, instance.Name, hScale.ComponentName)
			}
			if isScaleIn && instance.ReplicaChanges > instanceReplicas {
				return fmt.Errorf(`%s "replicaChanges" of instanceTemplate "%s" can't be greater than %d`, operationPrefix, instance.Name, instanceReplicas)
			}
			totalReplicaChanges += instance.ReplicaChanges
			insTplChangeMap[instance.Name] = instance.ReplicaChanges
		}

		// Rule 3: Ensure replicaChanges are not less than the replicaCount for each instance template
		for insTplName, replicaCount := range offlineOrOnlineInsCountMap {
			replicaChangesForOneInsTpl, exists := insTplChangeMap[insTplName]
			if !exists {
				totalReplicaChanges += replicaCount
				continue
			}
			if replicaChangesForOneInsTpl < replicaCount {
				return fmt.Errorf(`"replicaChanges" can't be less than %d when %d instances of the instance template "%s" are configured in %s`,
					replicaCount, replicaCount, insTplName, instanceField)
			}
		}

		// Validate new instance templates
		for _, insTpl := range newInstances {
			if _, exists := compInstanceTplReplicas[insTpl.Name]; exists {
				return fmt.Errorf(`new instance template "%s" already exists in component "%s"`, insTpl.Name, hScale.ComponentName)
			}
			totalReplicaChanges += insTpl.GetReplicas()
		}

		// Validate if replicaChanges exceed the allowed replicaChanges limit
		if replicaChanger.ReplicaChanges != nil && totalReplicaChanges > *replicaChanger.ReplicaChanges {
			return fmt.Errorf(`%s "replicaChanges" can't be less than the sum of "replicaChanges" for specified instance templates`, operationPrefix)
		}

		// Check if the final replica count is within the limits
		actualReplicaChange := totalReplicaChanges
		if replicaChanger.ReplicaChanges != nil {
			actualReplicaChange = *replicaChanger.ReplicaChanges
		}
		if isScaleIn && int(compSpec.Replicas)-int(actualReplicaChange) < minReplicasOrShards {
			return fmt.Errorf(`the number of replicas after scaling down violates the replica limit for component "%s"`, hScale.ComponentName)
		}
		if !isScaleIn && int(compSpec.Replicas)+int(actualReplicaChange) > maxReplicasOrShards {
			return fmt.Errorf(`the number of replicas after scaling up violates the replica limit for component "%s"`, hScale.ComponentName)
		}
		return nil
	}

	// Validate scaleIn and scaleOut separately
	if scaleIn != nil {
		if err := validateHScaleOperation(scaleIn.ReplicaChanger, nil, scaleIn.OnlineInstancesToOffline, true); err != nil {
			return err
		}
		if scaleIn.ReplicaChanges != nil && *scaleIn.ReplicaChanges > compSpec.Replicas {
			return fmt.Errorf(`"scaleIn.replicaChanges" can't be greater than %d for component "%s"`, compSpec.Replicas, hScale.ComponentName)
		}
	}
	if scaleOut != nil {
		if err := validateHScaleOperation(scaleOut.ReplicaChanger, scaleOut.NewInstances, scaleOut.OfflineInstancesToOnline, false); err != nil {
			return err
		}
	}

	// Check for conflicting scaling operations
	if err := r.checkConflictingScalingOperations(hScale); err != nil {
		return err
	}
	return nil
}

// validateShards validates the shards field if it is present
func (r *OpsRequest) validateShards(hScale HorizontalScaling, isSharding bool, minReplicasOrShards int, maxReplicasOrShards int) error {
	if hScale.Shards != nil {
		if !isSharding {
			return fmt.Errorf(`shards field cannot be used for the component "%s"`, hScale.ComponentName)
		}
		if hScale.ScaleOut != nil || hScale.ScaleIn != nil {
			return fmt.Errorf(`shards field cannot be used together with scaleOut or scaleIn for the component "%s"`, hScale.ComponentName)
		}
		shards := int(*hScale.Shards)
		if shards < minReplicasOrShards || shards > maxReplicasOrShards {
			return fmt.Errorf(`the number of shards after horizontal scale violates the shards limit "%s"`, hScale.ComponentName)
		}
	}
	return nil
}

// applyLastConfiguration applies the last known configuration to the component spec
func (r *OpsRequest) applyLastConfiguration(componentName string, compSpec *appsv1.ClusterComponentSpec) error {
	if lastCompConfiguration, ok := r.Status.LastConfiguration.Components[componentName]; ok {
		compSpec.Instances = lastCompConfiguration.Instances
		if lastCompConfiguration.Replicas != nil {
			compSpec.Replicas = *lastCompConfiguration.Replicas
		}
		compSpec.OfflineInstances = lastCompConfiguration.OfflineInstances
	}
	return nil
}

// checkConflictingScalingOperations checks for any conflicting scaling operations
func (r *OpsRequest) checkConflictingScalingOperations(hScale HorizontalScaling) error {
	if hScale.ScaleIn != nil && hScale.ScaleOut != nil {
		offlineToOnlineSet := make(map[string]struct{})
		for _, instance := range hScale.ScaleIn.OnlineInstancesToOffline {
			offlineToOnlineSet[instance] = struct{}{}
		}
		for _, instance := range hScale.ScaleOut.OfflineInstancesToOnline {
			if _, exists := offlineToOnlineSet[instance]; exists {
				return fmt.Errorf(`instance "%s" cannot be both in "OfflineInstancesToOnline" and "OnlineInstancesToOffline"`, instance)
			}
		}
	}
	return nil
}

// validateVolumeExpansion validates volumeExpansion api when spec.type is VolumeExpansion
func (r *OpsRequest) validateVolumeExpansion(ctx context.Context, cli client.Client, cluster *appsv1.Cluster) error {
	volumeExpansionList := r.Spec.VolumeExpansionList
	if len(volumeExpansionList) == 0 {
		return notEmptyError("spec.volumeExpansion")
	}

	compOpsList := make([]ComponentOps, len(volumeExpansionList))
	for i, v := range volumeExpansionList {
		compOpsList[i] = v.ComponentOps
	}
	if err := r.checkComponentExistence(cluster, compOpsList); err != nil {
		return err
	}
	return r.checkVolumesAllowExpansion(ctx, cli, cluster)
}

// validateSwitchover validates switchover api when spec.type is Switchover.
// more time consuming checks will be done in handler's Action() function.
func (r *OpsRequest) validateSwitchover(cluster *appsv1.Cluster) error {
	switchoverList := r.Spec.SwitchoverList
	if len(switchoverList) == 0 {
		return notEmptyError("spec.switchover")
	}
	compOpsList := make([]ComponentOps, 0)
	for _, v := range switchoverList {
		if len(v.ComponentName) == 0 {
			continue
		}
		compOpsList = append(compOpsList, ComponentOps{
			ComponentName: v.ComponentName,
		})

	}
	if err := r.checkComponentExistence(cluster, compOpsList); err != nil {
		return err
	}

	for _, switchover := range switchoverList {
		if switchover.InstanceName == "" {
			return notEmptyError("switchover.instanceName")
		}
	}

	return nil
}

func (r *OpsRequest) checkInstanceTemplate(cluster *appsv1.Cluster, componentOps ComponentOps, inputInstances []string) error {
	instanceNameMap := make(map[string]sets.Empty)
	setInstanceMap := func(instances []appsv1.InstanceTemplate) {
		for i := range instances {
			instanceNameMap[instances[i].Name] = sets.Empty{}
		}
	}
	for _, spec := range cluster.Spec.Shardings {
		if spec.Name != componentOps.ComponentName {
			continue
		}
		setInstanceMap(spec.Template.Instances)
	}
	for _, compSpec := range cluster.Spec.ComponentSpecs {
		if compSpec.Name != componentOps.ComponentName {
			continue
		}
		setInstanceMap(compSpec.Instances)
	}
	var notFoundInstanceNames []string
	for _, insName := range inputInstances {
		if _, ok := instanceNameMap[insName]; !ok {
			notFoundInstanceNames = append(notFoundInstanceNames, insName)
		}
	}
	if len(notFoundInstanceNames) > 0 {
		return fmt.Errorf("instance: %v not found in cluster: %s", notFoundInstanceNames, r.Spec.GetClusterName())
	}
	return nil
}

// checkComponentExistence checks whether components to be operated exist in cluster spec.
func (r *OpsRequest) checkComponentExistence(cluster *appsv1.Cluster, compOpsList []ComponentOps) error {
	compNameMap := make(map[string]sets.Empty)
	for _, compSpec := range cluster.Spec.ComponentSpecs {
		compNameMap[compSpec.Name] = sets.Empty{}
	}
	for _, spec := range cluster.Spec.Shardings {
		compNameMap[spec.Name] = sets.Empty{}
	}
	var (
		notFoundCompNames []string
	)
	for _, compOps := range compOpsList {
		if _, ok := compNameMap[compOps.ComponentName]; !ok {
			notFoundCompNames = append(notFoundCompNames, compOps.ComponentName)
		}
		continue
	}

	if len(notFoundCompNames) > 0 {
		return fmt.Errorf("components: %v not found, in cluster.spec.componentSpecs or cluster.spec.shardingSpecs", notFoundCompNames)
	}
	return nil
}

func (r *OpsRequest) checkVolumesAllowExpansion(ctx context.Context, cli client.Client, cluster *appsv1.Cluster) error {
	type Entity struct {
		existInSpec         bool
		storageClassName    *string
		allowExpansion      bool
		requestStorage      resource.Quantity
		isShardingComponent bool
	}

	// component name/ sharding name -> vct name -> entity
	vols := make(map[string]map[string]Entity)
	setVols := func(vcts []OpsRequestVolumeClaimTemplate, componentName string) {
		for _, vct := range vcts {
			if _, ok := vols[componentName]; !ok {
				vols[componentName] = make(map[string]Entity)
			}
			vols[componentName][vct.Name] = Entity{false, nil, false, vct.Storage, false}
		}
	}

	for _, comp := range r.Spec.VolumeExpansionList {
		setVols(comp.VolumeClaimTemplates, comp.ComponentOps.ComponentName)
	}
	fillVol := func(vct appsv1.PersistentVolumeClaimTemplate, key string, isShardingComp bool) {
		e, ok := vols[key][vct.Name]
		if !ok {
			return
		}
		e.existInSpec = true
		e.storageClassName = vct.Spec.StorageClassName
		e.isShardingComponent = isShardingComp
		vols[key][vct.Name] = e
	}
	fillCompVols := func(compSpec appsv1.ClusterComponentSpec, componentName string, isShardingComp bool) {
		if _, ok := vols[componentName]; !ok {
			return // ignore not-exist component
		}
		for _, vct := range compSpec.VolumeClaimTemplates {
			fillVol(vct, componentName, isShardingComp)
		}
	}
	// traverse the spec to update volumes
	for _, comp := range cluster.Spec.ComponentSpecs {
		fillCompVols(comp, comp.Name, false)
	}
	for _, sharding := range cluster.Spec.Shardings {
		fillCompVols(sharding.Template, sharding.Name, true)
	}

	// check all used storage classes
	var err error
	for key, compVols := range vols {
		for vname := range compVols {
			e := vols[key][vname]
			if !e.existInSpec {
				continue
			}
			e.storageClassName, err = r.getSCNameByPvcAndCheckStorageSize(ctx, cli, key, vname, e.isShardingComponent, e.requestStorage)
			if err != nil {
				return err
			}
			allowExpansion, err := r.checkStorageClassAllowExpansion(ctx, cli, e.storageClassName)
			if err != nil {
				continue // ignore the error and take it as not-supported
			}
			e.allowExpansion = allowExpansion
			vols[key][vname] = e
		}
	}

	for key, compVols := range vols {
		var (
			notFound     []string
			notSupport   []string
			notSupportSc []string
		)
		for vct, e := range compVols {
			if !e.existInSpec {
				notFound = append(notFound, vct)
			}
			if !e.allowExpansion {
				notSupport = append(notSupport, vct)
				if e.storageClassName != nil {
					notSupportSc = append(notSupportSc, *e.storageClassName)
				}
			}
		}
		if len(notFound) > 0 {
			return fmt.Errorf("volumeClaimTemplates: %v not found in component: %s, you can view infos by command: "+
				"kubectl get cluster %s -n %s", notFound, key, cluster.Name, r.Namespace)
		}
		if len(notSupport) > 0 {
			var notSupportScString string
			if len(notSupportSc) > 0 {
				notSupportScString = fmt.Sprintf("storageClass: %v of ", notSupportSc)
			}
			return fmt.Errorf(notSupportScString+"volumeClaimTemplate: %v not support volume expansion in component: %s, you can view infos by command: "+
				"kubectl get sc", notSupport, key)
		}
	}
	return nil
}

// checkStorageClassAllowExpansion checks whether the specified storage class supports volume expansion.
func (r *OpsRequest) checkStorageClassAllowExpansion(ctx context.Context,
	cli client.Client,
	storageClassName *string) (bool, error) {
	if storageClassName == nil {
		return false, nil
	}
	storageClass := &storagev1.StorageClass{}
	// take not found error as unsupported
	if err := cli.Get(ctx, types.NamespacedName{Name: *storageClassName}, storageClass); err != nil && !apierrors.IsNotFound(err) {
		return false, err
	}
	if storageClass.AllowVolumeExpansion == nil {
		return false, nil
	}
	return *storageClass.AllowVolumeExpansion, nil
}

// getSCNameByPvcAndCheckStorageSize gets the storageClassName by pvc and checks if the storage size is valid.
func (r *OpsRequest) getSCNameByPvcAndCheckStorageSize(ctx context.Context,
	cli client.Client,
	key,
	vctName string,
	isShardingComponent bool,
	requestStorage resource.Quantity) (*string, error) {
	componentName := key
	targetInsTPLName := ""
	if strings.Contains(key, ".") {
		keyStrs := strings.Split(key, ".")
		componentName = keyStrs[0]
		targetInsTPLName = keyStrs[1]
	}
	matchingLabels := client.MatchingLabels{
		constant.AppInstanceLabelKey:             r.Spec.GetClusterName(),
		constant.VolumeClaimTemplateNameLabelKey: vctName,
	}
	if isShardingComponent {
		matchingLabels[constant.KBAppShardingNameLabelKey] = componentName
	} else {
		matchingLabels[constant.KBAppComponentLabelKey] = componentName
	}
	pvcList := &corev1.PersistentVolumeClaimList{}
	if err := cli.List(ctx, pvcList, client.InNamespace(r.Namespace), matchingLabels); err != nil {
		return nil, err
	}
	var pvc *corev1.PersistentVolumeClaim
	for _, pvcItem := range pvcList.Items {
		if targetInsTPLName == pvcItem.Labels[constant.KBAppInstanceTemplateLabelKey] {
			pvc = &pvcItem
			break
		}
	}
	if pvc == nil {
		return nil, nil
	}
	previousValue := *pvc.Status.Capacity.Storage()
	if requestStorage.Cmp(previousValue) < 0 {
		return nil, fmt.Errorf(`requested storage size of volumeClaimTemplate "%s" can not less than status.capacity.storage "%s" `,
			vctName, previousValue.String())
	}
	return pvc.Spec.StorageClassName, nil
}

// validateVerticalResourceList checks if k8s resourceList is legal
func validateVerticalResourceList(resourceList map[corev1.ResourceName]resource.Quantity) (string, error) {
	for k := range resourceList {
		if k != corev1.ResourceCPU && k != corev1.ResourceMemory && !strings.HasPrefix(k.String(), corev1.ResourceHugePagesPrefix) {
			return string(k), fmt.Errorf("resource key is not cpu or memory or hugepages- ")
		}
	}

	return "", nil
}

func notEmptyError(target string) error {
	return fmt.Errorf(`"%s" can not be empty`, target)
}

func invalidValueError(target string, value string) error {
	return fmt.Errorf(`invalid value for "%s": %s`, target, value)
}

// GetRunningOpsByOpsType gets the running opsRequests by type.
func GetRunningOpsByOpsType(ctx context.Context, cli client.Client,
	clusterName, namespace, opsType string) ([]OpsRequest, error) {
	opsRequestList := &OpsRequestList{}
	if err := cli.List(ctx, opsRequestList, client.MatchingLabels{
		constant.AppInstanceLabelKey:    clusterName,
		constant.OpsRequestTypeLabelKey: opsType,
	}, client.InNamespace(namespace)); err != nil {
		return nil, err
	}
	if len(opsRequestList.Items) == 0 {
		return nil, nil
	}
	var runningOpsList []OpsRequest
	for _, v := range opsRequestList.Items {
		if v.Status.Phase == OpsRunningPhase {
			runningOpsList = append(runningOpsList, v)
			break
		}
	}
	return runningOpsList, nil
}
