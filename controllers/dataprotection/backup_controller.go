/*
Copyright (C) 2022-2025 ApeCloud Co., Ltd

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

package dataprotection

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	vsv1beta1 "github.com/kubernetes-csi/external-snapshotter/client/v3/apis/volumesnapshot/v1beta1"
	vsv1 "github.com/kubernetes-csi/external-snapshotter/client/v6/apis/volumesnapshot/v1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/clock"
	"k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	kbappsv1 "github.com/apecloud/kubeblocks/apis/apps/v1"
	dpv1alpha1 "github.com/apecloud/kubeblocks/apis/dataprotection/v1alpha1"
	"github.com/apecloud/kubeblocks/pkg/constant"
	intctrlutil "github.com/apecloud/kubeblocks/pkg/controllerutil"
	"github.com/apecloud/kubeblocks/pkg/dataprotection/action"
	dpbackup "github.com/apecloud/kubeblocks/pkg/dataprotection/backup"
	dptypes "github.com/apecloud/kubeblocks/pkg/dataprotection/types"
	dputils "github.com/apecloud/kubeblocks/pkg/dataprotection/utils"
	"github.com/apecloud/kubeblocks/pkg/dataprotection/utils/boolptr"
	viper "github.com/apecloud/kubeblocks/pkg/viperx"
)

// BackupReconciler reconciles a Backup object
type BackupReconciler struct {
	client.Client
	Scheme     *k8sruntime.Scheme
	Recorder   record.EventRecorder
	RestConfig *rest.Config
	clock      clock.RealClock
}

// +kubebuilder:rbac:groups=dataprotection.kubeblocks.io,resources=backups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=dataprotection.kubeblocks.io,resources=backups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=dataprotection.kubeblocks.io,resources=backups/finalizers,verbs=update

// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=snapshot.storage.k8s.io,resources=volumesnapshots,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=snapshot.storage.k8s.io,resources=volumesnapshots/finalizers,verbs=update;patch
// +kubebuilder:rbac:groups=snapshot.storage.k8s.io,resources=volumesnapshotclasses,verbs=get;list;watch

// +kubebuilder:rbac:groups=core,resources=serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=get;list;watch;create;update;patch;delete

// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete;deletecollection
// +kubebuilder:rbac:groups=apps,resources=statefulsets/status,verbs=get
// +kubebuilder:rbac:groups=apps,resources=statefulsets/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the backup closer to the desired state.
func (r *BackupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// setup common request context
	reqCtx := intctrlutil.RequestCtx{
		Ctx:      ctx,
		Req:      req,
		Log:      log.FromContext(ctx).WithValues("backup", req.NamespacedName),
		Recorder: r.Recorder,
	}

	// get backup object, and return if not found
	backup := &dpv1alpha1.Backup{}
	if err := r.Client.Get(reqCtx.Ctx, reqCtx.Req.NamespacedName, backup); err != nil {
		return intctrlutil.CheckedRequeueWithError(err, reqCtx.Log, "")
	}

	// check whether to skip reconciliation
	if val, ok := backup.Annotations[dptypes.SkipReconciliationAnnotationKey]; ok && strings.EqualFold(val, "true") {
		reqCtx.Log.V(1).Info("skip reconciliation", "backup", req.NamespacedName)
		return intctrlutil.Reconciled()
	}

	reqCtx.Log.V(1).Info("reconcile", "backup", req.NamespacedName, "phase", backup.Status.Phase)

	// if backup is being deleted, set backup phase to Deleting. The backup
	// reference workloads, data and volume snapshots will be deleted by controller
	// later when the backup status.phase is deleting.
	if !backup.GetDeletionTimestamp().IsZero() && backup.Status.Phase != dpv1alpha1.BackupPhaseDeleting {
		patch := client.MergeFrom(backup.DeepCopy())
		backup.Status.Phase = dpv1alpha1.BackupPhaseDeleting
		if err := r.Client.Status().Patch(reqCtx.Ctx, backup, patch); err != nil {
			return intctrlutil.RequeueWithError(err, reqCtx.Log, "")
		}
	}

	switch backup.Status.Phase {
	case "", dpv1alpha1.BackupPhaseNew:
		return r.handleNewPhase(reqCtx, backup)
	case dpv1alpha1.BackupPhaseRunning:
		return r.handleRunningPhase(reqCtx, backup)
	case dpv1alpha1.BackupPhaseCompleted:
		return r.handleCompletedPhase(reqCtx, backup)
	case dpv1alpha1.BackupPhaseDeleting:
		return r.handleDeletingPhase(reqCtx, backup)
	case dpv1alpha1.BackupPhaseFailed:
		if backup.Labels[dptypes.BackupTypeLabelKey] == string(dpv1alpha1.BackupTypeContinuous) {
			if backup.Status.StartTimestamp.IsZero() {
				// if the backup fails in the 'New' phase, reconcile it from 'New' phase handler.
				return r.handleNewPhase(reqCtx, backup)
			}
			return r.handleRunningPhase(reqCtx, backup)
		}
		return intctrlutil.Reconciled()
	default:
		return intctrlutil.Reconciled()
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *BackupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	b := intctrlutil.NewControllerManagedBy(mgr).
		For(&dpv1alpha1.Backup{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: viper.GetInt(dptypes.CfgDataProtectionReconcileWorkers),
		}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&batchv1.Job{}).
		Watches(&corev1.Pod{}, handler.EnqueueRequestsFromMapFunc(r.filterBackupPods)).
		Watches(&batchv1.Job{}, handler.EnqueueRequestsFromMapFunc(r.parseBackupJob))

	if dputils.SupportsVolumeSnapshotV1() {
		b.Owns(&vsv1.VolumeSnapshot{}, builder.Predicates{})
	} else {
		b.Owns(&vsv1beta1.VolumeSnapshot{}, builder.Predicates{})
	}
	return b.Complete(r)
}

func (r *BackupReconciler) filterBackupPods(ctx context.Context, obj client.Object) []reconcile.Request {
	var requests []reconcile.Request
	labels := obj.GetLabels()
	if v, ok := labels[constant.AppManagedByLabelKey]; !ok || v != dptypes.AppName {
		return requests
	}
	backupName, ok := labels[dptypes.BackupNameLabelKey]
	if !ok {
		return requests
	}
	for _, v := range obj.GetOwnerReferences() {
		if (v.Kind == constant.StatefulSetKind && v.Name == backupName) || v.Kind == constant.JobKind {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Namespace: obj.GetNamespace(),
					Name:      backupName,
				},
			})
			break
		}
	}
	return requests
}

func (r *BackupReconciler) parseBackupJob(_ context.Context, object client.Object) []reconcile.Request {
	job := object.(*batchv1.Job)
	var requests []reconcile.Request
	backupName := job.Labels[dptypes.BackupNameLabelKey]
	backupNamespace := job.Labels[dptypes.BackupNamespaceLabelKey]
	if backupName != "" && backupNamespace != "" {
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Namespace: backupNamespace,
				Name:      backupName,
			},
		})
	}
	return requests
}

// deleteBackupFiles deletes the backup files stored in backup repository.
func (r *BackupReconciler) deleteBackupFiles(reqCtx intctrlutil.RequestCtx, backup *dpv1alpha1.Backup) error {
	deleteBackup := func() error {
		// remove backup finalizers to delete it
		patch := client.MergeFrom(backup.DeepCopy())
		controllerutil.RemoveFinalizer(backup, dptypes.DataProtectionFinalizerName)
		return r.Patch(reqCtx.Ctx, backup, patch)
	}

	deleter := &dpbackup.Deleter{
		RequestCtx: reqCtx,
		Client:     r.Client,
		Scheme:     r.Scheme,
	}

	// TODO: update the mcMgr param
	saName, err := EnsureWorkerServiceAccount(reqCtx, r.Client, backup.Namespace, nil)
	if err != nil {
		return fmt.Errorf("failed to get worker service account: %w", err)
	}
	deleter.WorkerServiceAccount = saName

	status, err := deleter.DeleteBackupFiles(backup)
	switch status {
	case dpbackup.DeletionStatusSucceeded:
		return deleteBackup()
	case dpbackup.DeletionStatusFailed:
		failureReason := err.Error()
		if backup.Status.FailureReason == failureReason {
			return nil
		}
		backupPatch := client.MergeFrom(backup.DeepCopy())
		backup.Status.FailureReason = failureReason
		r.Recorder.Event(backup, corev1.EventTypeWarning, "DeleteBackupFilesFailed", failureReason)
		return r.Status().Patch(reqCtx.Ctx, backup, backupPatch)
	case dpbackup.DeletionStatusDeleting,
		dpbackup.DeletionStatusUnknown:
		// wait for the deletion job completed
		return err
	}
	return err
}

// handleDeletingPhase handles the deletion of backup. It will delete the backup CR
// and the backup workload(job).
func (r *BackupReconciler) handleDeletingPhase(reqCtx intctrlutil.RequestCtx, backup *dpv1alpha1.Backup) (ctrl.Result, error) {
	// delete related backups
	if err := r.deleteRelatedBackups(reqCtx, backup); err != nil {
		return intctrlutil.RequeueWithError(err, reqCtx.Log, "")
	}

	// if backup phase is Deleting, delete the backup reference workloads,
	// backup data stored in backup repository and volume snapshots.
	// TODO(ldm): if backup is being used by restore, do not delete it.
	if err := r.deleteExternalResources(reqCtx, backup); err != nil {
		return intctrlutil.RequeueWithError(err, reqCtx.Log, "")
	}

	if backup.Spec.DeletionPolicy == dpv1alpha1.BackupDeletionPolicyRetain {
		r.Recorder.Event(backup, corev1.EventTypeWarning, "Retain", "can not delete the backup if deletionPolicy is Retain")
		return intctrlutil.Reconciled()
	}

	if cleaned, err := r.waitForBackupPodsDeleted(reqCtx, backup); err != nil {
		return intctrlutil.RequeueWithError(err, reqCtx.Log, "")
	} else if !cleaned {
		return intctrlutil.Reconciled()
	}

	if err := r.deleteVolumeSnapshots(reqCtx, backup); err != nil {
		return intctrlutil.RequeueWithError(err, reqCtx.Log, "")
	}

	if err := r.deleteBackupFiles(reqCtx, backup); err != nil {
		return intctrlutil.RequeueWithError(err, reqCtx.Log, "")
	}
	return intctrlutil.Reconciled()
}

func (r *BackupReconciler) handleNewPhase(
	reqCtx intctrlutil.RequestCtx,
	backup *dpv1alpha1.Backup) (ctrl.Result, error) {
	request, err := r.prepareBackupRequest(reqCtx, backup)
	if err != nil {
		return r.updateStatusIfFailed(reqCtx, backup.DeepCopy(), backup, err)
	}
	// record the status.target/status.targets infos for continuous backup.
	if err = r.recordBackupStatusTargets(reqCtx, request); err != nil {
		return r.updateStatusIfFailed(reqCtx, backup, request.Backup, err)
	}
	backupStatusCopy := request.Backup.Status.DeepCopy()
	// set and patch backup object meta, including labels, annotations and finalizers
	// if the backup object meta is changed, the backup object will be patched.
	if wait, err := PatchBackupObjectMeta(backup, request); err != nil {
		return r.updateStatusIfFailed(reqCtx, backup, request.Backup, err)
	} else if wait {
		return intctrlutil.Reconciled()
	}
	request.Backup.Status = *backupStatusCopy
	// set and patch backup status
	if err = r.patchBackupStatus(backup, request); err != nil {
		return r.updateStatusIfFailed(reqCtx, backup, request.Backup, err)
	}
	return intctrlutil.Reconciled()
}

// recordBackupStatusTargets records the backup status target or targets for next reconcile.
func (r *BackupReconciler) recordBackupStatusTargets(
	reqCtx intctrlutil.RequestCtx,
	request *dpbackup.Request) error {
	if request.Backup.Status.Target != nil || len(request.Backup.Status.Targets) > 0 {
		return nil
	}
	buildStatusTarget := func(target *dpv1alpha1.BackupTarget) (*dpv1alpha1.BackupStatusTarget, error) {
		if err := r.prepareRequestTargetInfo(reqCtx, request, target); err != nil {
			return nil, err
		}
		var selectedTargetPods []string
		for i := range request.TargetPods {
			selectedTargetPods = append(selectedTargetPods, request.TargetPods[i].Name)
		}
		return &dpv1alpha1.BackupStatusTarget{
			BackupTarget:       *target,
			SelectedTargetPods: selectedTargetPods,
		}, nil
	}
	setStatusTarget := func(target *dpv1alpha1.BackupTarget) error {
		if statusTarget, err := buildStatusTarget(target); err != nil {
			return err
		} else {
			request.Status.Target = statusTarget
		}
		return nil
	}
	setStatusTargets := func(targets []dpv1alpha1.BackupTarget) error {
		for i := range targets {
			if statusTarget, err := buildStatusTarget(&targets[i]); err != nil {
				return err
			} else {
				request.Status.Targets = append(request.Status.Targets, *statusTarget)
			}
		}
		return nil
	}
	var err error
	switch {
	case request.BackupMethod.Target != nil:
		err = setStatusTarget(request.BackupMethod.Target)
	case len(request.BackupMethod.Targets) > 0:
		err = setStatusTargets(request.BackupMethod.Targets)
	case request.BackupPolicy.Spec.Target != nil:
		err = setStatusTarget(request.BackupPolicy.Spec.Target)
	case len(request.BackupPolicy.Spec.Targets) > 0:
		err = setStatusTargets(request.BackupPolicy.Spec.Targets)
	default:
		return intctrlutil.NewFatalError(fmt.Sprintf(`backup target/targets can not be empty in backupPolicy "%s"`, request.BackupPolicy.Name))
	}
	return err
}

// prepareBackupRequest prepares a request for a backup, with all references to
// other kubernetes objects, and validate them.
func (r *BackupReconciler) prepareBackupRequest(
	reqCtx intctrlutil.RequestCtx,
	backup *dpv1alpha1.Backup) (*dpbackup.Request, error) {
	request := &dpbackup.Request{
		Backup:     backup.DeepCopy(),
		RequestCtx: reqCtx,
		Client:     r.Client,
	}
	if request.Annotations == nil {
		request.Annotations = make(map[string]string)
	}

	if request.Labels == nil {
		request.Labels = make(map[string]string)
	}

	backupPolicy, err := dputils.GetBackupPolicyByName(reqCtx, r.Client, backup.Spec.BackupPolicyName)
	if err != nil {
		return nil, err
	}
	if backupPolicy.Status.Phase == dpv1alpha1.UnavailablePhase {
		return nil, intctrlutil.NewFatalError(fmt.Sprintf(`phase of backupPolicy "%s" is Unavailable`, backupPolicy.Name))
	}

	backupMethod := dputils.GetBackupMethodByName(backup.Spec.BackupMethod, backupPolicy)
	if backupMethod == nil {
		return nil, intctrlutil.NewNotFound("backupMethod: %s not found",
			backup.Spec.BackupMethod)
	}

	// backupMethod should specify snapshotVolumes or actionSetName, if we take
	// snapshots to back up volumes, the snapshotVolumes should be set to true
	// and the actionSetName is not required, if we do not take snapshots to back
	// up volumes, the actionSetName is required.
	snapshotVolumes := boolptr.IsSetToTrue(backupMethod.SnapshotVolumes)
	if !snapshotVolumes && backupMethod.ActionSetName == "" {
		return nil, fmt.Errorf("backup method %s should specify snapshotVolumes or actionSetName", backupMethod.Name)
	}
	request.SnapshotVolumes = snapshotVolumes

	if backupMethod.ActionSetName != "" {
		actionSet, err := dputils.GetActionSetByName(reqCtx, r.Client, backupMethod.ActionSetName)
		if err != nil {
			return nil, err
		}
		// validate parameters
		if err := dputils.ValidateParameters(actionSet, backup.Spec.Parameters, true); err != nil {
			return nil, fmt.Errorf("fails to validate parameters with actionset %s: %v", actionSet.Name, err)
		}
		request.ActionSet = actionSet
	}

	// check encryption config
	if backupPolicy.Spec.EncryptionConfig != nil {
		if err := checkEncryptionConfig(reqCtx.Ctx, backupPolicy.Spec.EncryptionConfig, r.Client, backupPolicy.Namespace); err != nil {
			return nil, fmt.Errorf("failed to validate backupPolicy's encryption config: %w", err)
		}
	}

	request.BackupPolicy = backupPolicy
	request.BackupMethod = backupMethod

	if !snapshotVolumes {
		// if use volume snapshot, ignore backup repo
		if err = HandleBackupRepo(request); err != nil {
			return nil, err
		}
	}

	switch dpv1alpha1.BackupType(request.GetBackupType()) {
	case dpv1alpha1.BackupTypeIncremental:
		// requires backup repo info to validate parent backup
		request, err = prepare4Incremental(request)
	case dpv1alpha1.BackupTypeContinuous:
		err = validateContinuousBackup(backup, reqCtx, request.Client)
	}
	if err != nil {
		return nil, err
	}

	return request, nil
}

// prepareRequestTargetInfo prepares the backup target info for request object.
func (r *BackupReconciler) prepareRequestTargetInfo(reqCtx intctrlutil.RequestCtx,
	request *dpbackup.Request,
	target *dpv1alpha1.BackupTarget) error {
	var selectedPods []string
	backupStatusTarget := dputils.GetBackupStatusTarget(request.Backup, target.Name)
	if backupStatusTarget != nil {
		selectedPods = backupStatusTarget.SelectedTargetPods
	}
	if request.ParentBackup != nil && target.PodSelector.UseParentSelectedPods && len(selectedPods) == 0 {
		parentBackupStatusTarget := dputils.GetBackupStatusTarget(request.ParentBackup, target.Name)
		if parentBackupStatusTarget != nil && len(parentBackupStatusTarget.SelectedTargetPods) > 0 {
			selectedPods = parentBackupStatusTarget.SelectedTargetPods
		}
	}
	request.Target = target
	backupType := dpv1alpha1.BackupTypeFull
	if request.ActionSet != nil {
		backupType = request.ActionSet.Spec.BackupType
	}
	targetPods, err := GetTargetPods(reqCtx, r.Client,
		selectedPods, request.BackupPolicy, target, backupType)
	if err != nil {
		return err
	}
	if len(targetPods) == 0 {
		if backupType == dpv1alpha1.BackupTypeContinuous {
			// stop the sts to un-bound the pvcs when the continuous backup is failed.
			if err = dpbackup.StopStatefulSetsWhenFailed(reqCtx.Ctx, r.Client, request.Backup, target.Name); err != nil {
				return err
			}
		}
		return fmt.Errorf("failed to get target pods by backup policy %s/%s",
			request.BackupPolicy.Namespace, request.BackupPolicy.Name)
	}

	request.TargetPods = targetPods
	saName := target.ServiceAccountName
	if saName == "" {
		// TODO: update the mcMgr param
		saName, err = EnsureWorkerServiceAccount(reqCtx, r.Client, request.Backup.Namespace, nil)
		if err != nil {
			return fmt.Errorf("failed to get worker service account: %w", err)
		}
	}
	request.WorkerServiceAccount = saName
	return nil
}

func (r *BackupReconciler) patchBackupStatus(
	original *dpv1alpha1.Backup,
	request *dpbackup.Request) error {
	request.Status.FormatVersion = dpbackup.FormatVersion
	if !request.SnapshotVolumes {
		request.Status.Path = dpbackup.BuildBaseBackupPath(
			request.Backup, request.BackupRepo.Spec.PathPrefix, request.BackupPolicy.Spec.PathPrefix)
	}
	request.Status.BackupMethod = request.BackupMethod
	if request.BackupRepo != nil {
		request.Status.BackupRepoName = request.BackupRepo.Name
	}
	if request.BackupRepoPVC != nil {
		request.Status.PersistentVolumeClaimName = request.BackupRepoPVC.Name
	}
	if !request.SnapshotVolumes && request.BackupPolicy.Spec.UseKopia {
		request.Status.KopiaRepoPath = dpbackup.BuildKopiaRepoPath(
			request.Backup, request.BackupRepo.Spec.PathPrefix, request.BackupPolicy.Spec.PathPrefix)
	}
	if request.ParentBackup != nil {
		// inherit encryption config from parent backup
		request.Status.EncryptionConfig = request.ParentBackup.Status.EncryptionConfig
	} else if request.BackupPolicy.Spec.EncryptionConfig != nil {
		request.Status.EncryptionConfig = request.BackupPolicy.Spec.EncryptionConfig
	}
	// init action status
	actions, err := request.BuildActions()
	if err != nil {
		return err
	}
	for targetPodName, acts := range actions {
		for _, act := range acts {
			request.Status.Actions = append(request.Status.Actions, dpv1alpha1.ActionStatus{
				Name:          act.GetName(),
				TargetPodName: targetPodName,
				Phase:         dpv1alpha1.ActionPhaseNew,
				ActionType:    act.Type(),
			})
		}
	}

	// update phase to running
	request.Status.Phase = dpv1alpha1.BackupPhaseRunning
	request.Status.StartTimestamp = &metav1.Time{Time: r.clock.Now().UTC()}

	// set status parent backup and base backup name
	if request.ParentBackup != nil {
		request.Status.ParentBackupName = request.ParentBackup.Name
	}
	if request.BaseBackup != nil {
		request.Status.BaseBackupName = request.BaseBackup.Name
	}

	if err = dpbackup.SetExpirationTime(request.Backup); err != nil {
		return err
	}
	return r.Client.Status().Patch(request.Ctx, request.Backup, client.MergeFrom(original))
}

func (r *BackupReconciler) handleRunningPhase(
	reqCtx intctrlutil.RequestCtx,
	backup *dpv1alpha1.Backup) (ctrl.Result, error) {
	restoreInProgress, err := r.checkRestoreInProgress(reqCtx, backup)
	if err != nil {
		return RecorderEventAndRequeue(reqCtx, r.Recorder, backup, err)
	}
	if restoreInProgress {
		msg := "backup job is delayed because restore is in progress"
		r.Recorder.Event(backup, corev1.EventTypeWarning, "RestoreInProgress", msg)
		return intctrlutil.Requeue(reqCtx.Log, msg)
	}

	if backup.Labels[dptypes.BackupTypeLabelKey] == string(dpv1alpha1.BackupTypeContinuous) {
		// check if the continuous backup is completed.
		if completed, err := r.checkIsCompletedDuringRunning(reqCtx, backup); err != nil {
			return RecorderEventAndRequeue(reqCtx, r.Recorder, backup, err)
		} else if completed {
			return intctrlutil.Reconciled()
		}
	}
	request, err := r.prepareBackupRequest(reqCtx, backup)
	if err != nil {
		return r.updateStatusIfFailed(reqCtx, backup.DeepCopy(), backup, err)
	}
	if err = r.syncContinuousBackupEncryptionConfig(reqCtx, backup, request.BackupPolicy); err != nil {
		return intctrlutil.CheckedRequeueWithError(err, reqCtx.Log, "sync continuous backup encryption config failed")
	}
	var (
		existFailedAction bool
		waiting           bool
		actionCtx         = action.ActionContext{
			Ctx:              reqCtx.Ctx,
			Client:           r.Client,
			Recorder:         r.Recorder,
			Scheme:           r.Scheme,
			RestClientConfig: r.RestConfig,
		}
		targets = dputils.GetBackupTargets(request.BackupPolicy, request.BackupMethod)
	)
	for i := range targets {
		if err = r.prepareRequestTargetInfo(reqCtx, request, &targets[i]); err != nil {
			return r.updateStatusIfFailed(reqCtx, backup, request.Backup, err)
		}
		// there are actions not completed, continue to handle following actions
		actions, err := request.BuildActions()
		if err != nil {
			return r.updateStatusIfFailed(reqCtx, backup, request.Backup, err)
		}
		// check all actions status, if any action failed, update backup status to failed
		// if all actions completed, update backup status to completed, otherwise,
		// continue to handle following actions.
		for targetPodName, acts := range actions {
			// the backup actions for selected pod
		targetPodBackupActions:
			for _, act := range acts {
				status, err := act.Execute(actionCtx)
				if err != nil {
					return r.updateStatusIfFailed(reqCtx, backup, request.Backup, err)
				}
				status.TargetPodName = targetPodName
				mergeActionStatus(request, status)
				switch status.Phase {
				case dpv1alpha1.ActionPhaseCompleted:
					updateBackupStatusByActionStatus(&request.Status)
					continue
				case dpv1alpha1.ActionPhaseFailed:
					existFailedAction = true
					break targetPodBackupActions
				case dpv1alpha1.ActionPhaseRunning:
					waiting = true
					break targetPodBackupActions
				}
			}
		}
	}
	if waiting {
		// reset time related fields for continuous backup
		request.Status.CompletionTimestamp = nil
		request.Status.Duration = nil
		err = dpbackup.SetExpirationTime(request.Backup)
		if err != nil {
			return r.updateStatusIfFailed(reqCtx, backup, request.Backup, fmt.Errorf("failed to set expiration time, %v", err))
		}
		// update status
		if err = r.Client.Status().Patch(reqCtx.Ctx, request.Backup, client.MergeFrom(backup)); err != nil {
			return intctrlutil.CheckedRequeueWithError(err, reqCtx.Log, "")
		}
		return intctrlutil.Reconciled()
	}
	if existFailedAction {
		return r.updateStatusIfFailed(reqCtx, backup, request.Backup,
			fmt.Errorf("there are failed actions, you can obtain the more informations in the status.actions"))
	}
	// all actions completed, update backup status to completed
	request.Status.Phase = dpv1alpha1.BackupPhaseCompleted
	request.Status.CompletionTimestamp = &metav1.Time{Time: r.clock.Now().UTC()}
	if !request.Status.StartTimestamp.IsZero() {
		// round the duration to a multiple of seconds.
		duration := request.Status.CompletionTimestamp.Sub(request.Status.StartTimestamp.Time).Round(time.Second)
		request.Status.Duration = &metav1.Duration{Duration: duration}
	}
	err = dpbackup.SetExpirationTime(request.Backup)
	if err != nil {
		return r.updateStatusIfFailed(reqCtx, backup, request.Backup, fmt.Errorf("failed to set expiration time, %v", err))
	}
	r.Recorder.Event(backup, corev1.EventTypeNormal, "CreatedBackup", "Completed backup")
	if err = r.Client.Status().Patch(reqCtx.Ctx, request.Backup, client.MergeFrom(backup)); err != nil {
		return intctrlutil.CheckedRequeueWithError(err, reqCtx.Log, "")
	}
	return intctrlutil.Reconciled()
}

func (r *BackupReconciler) syncContinuousBackupEncryptionConfig(reqCtx intctrlutil.RequestCtx, backup *dpv1alpha1.Backup, backupPolicy *dpv1alpha1.BackupPolicy) error {
	if backup.Labels[dptypes.BackupTypeLabelKey] != string(dpv1alpha1.BackupTypeContinuous) {
		return nil
	}
	if !reflect.DeepEqual(backup.Status.EncryptionConfig, backupPolicy.Spec.EncryptionConfig) {
		backup.Status.EncryptionConfig = backupPolicy.Spec.EncryptionConfig
		return r.Client.Status().Update(reqCtx.Ctx, backup)
	}
	return nil
}

func (r *BackupReconciler) checkRestoreInProgress(reqCtx intctrlutil.RequestCtx, backup *dpv1alpha1.Backup) (restoreInProgress bool, err error) {
	clusterName, ok := backup.Labels[constant.AppInstanceLabelKey]
	if !ok {
		reqCtx.Log.V(2).Info("AppInstanceLabel not found")
		return false, nil
	}
	cluster := &kbappsv1.Cluster{}
	backupTargetExists, err := intctrlutil.CheckResourceExists(reqCtx.Ctx, r.Client,
		client.ObjectKey{Name: clusterName, Namespace: backup.Namespace}, cluster)
	if err != nil || !backupTargetExists {
		return false, err
	}
	if cluster.Annotations == nil {
		return false, nil
	}
	_, ok = cluster.Annotations[constant.RestoreFromBackupAnnotationKey]
	return ok, nil
}

// checkIsCompletedDuringRunning when continuous schedule is disabled or cluster has been deleted,
// backup phase should be Completed.
func (r *BackupReconciler) checkIsCompletedDuringRunning(reqCtx intctrlutil.RequestCtx,
	backup *dpv1alpha1.Backup) (bool, error) {
	var (
		backupTargetExists              = true
		backupTargetIsStoppedOrDeleting bool
		err                             error
	)
	// check if target cluster exits
	clusterName := backup.Labels[constant.AppInstanceLabelKey]
	if clusterName != "" {
		cluster := &kbappsv1.Cluster{}
		backupTargetExists, err = intctrlutil.CheckResourceExists(reqCtx.Ctx, r.Client,
			client.ObjectKey{Name: clusterName, Namespace: backup.Namespace}, cluster)
		if err != nil {
			return false, err
		}
		backupTargetIsStoppedOrDeleting = cluster.IsDeleting() || cluster.Status.Phase == kbappsv1.StoppedClusterPhase
	}
	// if backup target exists, and it is not deleting or stopped, check if the schedule is enabled.
	if backupTargetExists && !backupTargetIsStoppedOrDeleting {
		backupSchedule := &dpv1alpha1.BackupSchedule{}
		if err = r.Client.Get(reqCtx.Ctx, client.ObjectKey{Name: backup.Labels[dptypes.BackupScheduleLabelKey],
			Namespace: backup.Namespace}, backupSchedule); err != nil {
			return false, err
		}
		for _, method := range backupSchedule.Spec.Schedules {
			// if Continuous backupMethod is enabled, return
			if method.BackupMethod == backup.Spec.BackupMethod && boolptr.IsSetToTrue(method.Enabled) {
				return false, nil
			}
		}
	}
	patch := client.MergeFrom(backup.DeepCopy())
	backup.Status.Phase = dpv1alpha1.BackupPhaseCompleted
	backup.Status.CompletionTimestamp = &metav1.Time{Time: r.clock.Now().UTC()}
	// set expiration time
	_ = dpbackup.SetExpirationTime(backup)
	if !backup.Status.StartTimestamp.IsZero() {
		// round the duration to a multiple of seconds.
		duration := backup.Status.CompletionTimestamp.Sub(backup.Status.StartTimestamp.Time).Round(time.Second)
		backup.Status.Duration = &metav1.Duration{Duration: duration}
	}
	for i := range backup.Status.Actions {
		act := &backup.Status.Actions[i]
		act.Phase = dpv1alpha1.ActionPhaseCompleted
		act.AvailableReplicas = pointer.Int32(int32(0))
		act.CompletionTimestamp = backup.Status.CompletionTimestamp
	}

	return true, r.Client.Status().Patch(reqCtx.Ctx, backup, patch)
}

// handleCompletedPhase handles the backup object in completed phase.
// It will delete the reference workloads.
func (r *BackupReconciler) handleCompletedPhase(
	reqCtx intctrlutil.RequestCtx,
	backup *dpv1alpha1.Backup) (ctrl.Result, error) {
	if err := r.deleteExternalResources(reqCtx, backup); err != nil {
		return intctrlutil.CheckedRequeueWithError(err, reqCtx.Log, "")
	}

	return intctrlutil.Reconciled()
}

func (r *BackupReconciler) updateStatusIfFailed(
	reqCtx intctrlutil.RequestCtx,
	original *dpv1alpha1.Backup,
	backup *dpv1alpha1.Backup,
	err error) (ctrl.Result, error) {
	if intctrlutil.IsTargetError(err, intctrlutil.ErrorTypeRequeue) {
		return intctrlutil.CheckedRequeueWithError(err, reqCtx.Log, "")
	}
	sendWarningEventForError(r.Recorder, backup, err)
	backup.Status.Phase = dpv1alpha1.BackupPhaseFailed
	backup.Status.FailureReason = err.Error()

	// set expiration time for failed backup, make sure the failed backup will be
	// deleted after the expiration time.
	_ = dpbackup.SetExpirationTime(backup)

	if errUpdate := r.Client.Status().Patch(reqCtx.Ctx, backup, client.MergeFrom(original)); errUpdate != nil {
		return intctrlutil.CheckedRequeueWithError(errUpdate, reqCtx.Log, "")
	}
	return intctrlutil.CheckedRequeueWithError(err, reqCtx.Log, "")
}

func (r *BackupReconciler) deleteVolumeSnapshots(reqCtx intctrlutil.RequestCtx,
	backup *dpv1alpha1.Backup) error {
	deleter := &dpbackup.Deleter{
		RequestCtx: reqCtx,
		Client:     r.Client,
	}
	return deleter.DeleteVolumeSnapshots(backup)
}

func (r *BackupReconciler) waitForBackupPodsDeleted(reqCtx intctrlutil.RequestCtx, backup *dpv1alpha1.Backup) (bool, error) {
	podList := &corev1.PodList{}
	if err := r.Client.List(reqCtx.Ctx, podList, client.InNamespace(backup.Namespace),
		client.MatchingLabels(map[string]string{
			dptypes.BackupNameLabelKey: backup.Name,
		})); err != nil {
		return false, err
	}
	if len(podList.Items) == 0 {
		return true, nil
	}
	return false, nil
}

// deleteExternalResources deletes the external workloads that execute backup.
// Currently, it only supports two types of workloads: job, statefulSet
func (r *BackupReconciler) deleteExternalResources(
	reqCtx intctrlutil.RequestCtx, backup *dpv1alpha1.Backup) error {
	labels := map[string]string{
		dptypes.BackupNameLabelKey:    backup.Name,
		constant.AppManagedByLabelKey: dptypes.AppName,
	}

	if clusterUID, ok := backup.Labels[dptypes.ClusterUIDLabelKey]; ok {
		labels[dptypes.ClusterUIDLabelKey] = clusterUID
	}

	// use map to avoid duplicate deletion of the same namespace.
	namespaces := map[string]sets.Empty{
		backup.Namespace: {},
		viper.GetString(constant.CfgKeyCtrlrMgrNS): {},
	}

	// delete the external jobs.
	if err := deleteRelatedObjectList(reqCtx, r.Client, &batchv1.JobList{}, namespaces, labels); err != nil {
		return err
	}

	// delete the external statefulSets.
	return deleteRelatedObjectList(reqCtx, r.Client, &appsv1.StatefulSetList{}, namespaces, labels)
}

// deleteRelatedBackups deletes the related backups.
func (r *BackupReconciler) deleteRelatedBackups(
	reqCtx intctrlutil.RequestCtx,
	backup *dpv1alpha1.Backup) error {
	backupList := &dpv1alpha1.BackupList{}
	labels := map[string]string{
		dptypes.BackupPolicyLabelKey: backup.Spec.BackupPolicyName,
	}
	if err := r.Client.List(reqCtx.Ctx, backupList,
		client.InNamespace(backup.Namespace), client.MatchingLabels(labels)); client.IgnoreNotFound(err) != nil {
		return err
	}
	for i := range backupList.Items {
		bp := &backupList.Items[i]
		// delete backups related to the current backup
		// files in the related backup's status.path will be deleted by its own associated deleter
		if bp.Status.ParentBackupName != backup.Name && bp.Status.BaseBackupName != backup.Name {
			continue
		}
		if err := intctrlutil.BackgroundDeleteObject(r.Client, reqCtx.Ctx, bp); err != nil {
			return err
		}
		reqCtx.Log.Info("delete the related backup", "backup", fmt.Sprintf("%s/%s", bp.Namespace, bp.Name))
	}
	return nil
}

// PatchBackupObjectMeta patches backup object metaObject include cluster snapshot.
func PatchBackupObjectMeta(
	original *dpv1alpha1.Backup,
	request *dpbackup.Request) (bool, error) {
	targetPod := request.TargetPods[0]

	// get KubeBlocks cluster and set labels and annotations for backup
	// TODO(ldm): we should remove this dependency of cluster in the future
	cluster := getCluster(request.Ctx, request.Client, targetPod)
	if cluster != nil {
		if err := setClusterSnapshotAnnotation(request, cluster); err != nil {
			return false, err
		}
		if err := setEncryptedSystemAccountsAnnotation(request, cluster); err != nil {
			return false, err
		}
		request.Labels[dptypes.ClusterUIDLabelKey] = string(cluster.UID)
	}

	for _, v := range getClusterLabelKeys() {
		if labelValue, ok := targetPod.Labels[v]; ok {
			request.Labels[v] = labelValue
		}
	}

	if _, ok := request.Labels[constant.AppManagedByLabelKey]; !ok {
		request.Labels[constant.AppManagedByLabelKey] = dptypes.AppName
	}
	request.Labels[dptypes.BackupTypeLabelKey] = request.GetBackupType()
	request.Labels[dptypes.BackupPolicyLabelKey] = request.Spec.BackupPolicyName
	// wait for the backup repo controller to prepare the essential resource.
	wait := false
	if request.BackupRepo != nil {
		request.Labels[dataProtectionBackupRepoKey] = request.BackupRepo.Name
		if (request.BackupRepo.AccessByMount() && request.BackupRepoPVC == nil) ||
			(request.BackupRepo.AccessByTool() && request.ToolConfigSecret == nil) {
			request.Labels[dataProtectionWaitRepoPreparationKey] = trueVal
			wait = true
		}
	}

	// set finalizer
	controllerutil.AddFinalizer(request.Backup, dptypes.DataProtectionFinalizerName)

	if reflect.DeepEqual(original.ObjectMeta, request.ObjectMeta) {
		return wait, nil
	}

	return wait, request.Client.Patch(request.Ctx, request.Backup, client.MergeFrom(original))
}

func mergeActionStatus(request *dpbackup.Request, status *dpv1alpha1.ActionStatus) {
	var exist bool
	for i := range request.Status.Actions {
		action := request.Status.Actions[i]
		if action.Name == status.Name {
			as := status.DeepCopy()
			if strings.HasPrefix(action.FailureReason, dptypes.LogCollectorOutput) {
				as.FailureReason = action.FailureReason
			}
			if request.Status.Actions[i].StartTimestamp != nil {
				as.StartTimestamp = request.Status.Actions[i].StartTimestamp
			}
			request.Status.Actions[i] = *as
			exist = true
			break
		}
	}
	if !exist {
		status.StartTimestamp = &metav1.Time{Time: time.Now()}
		request.Status.Actions = append(request.Status.Actions, *status)
	}
}

func updateBackupStatusByActionStatus(backupStatus *dpv1alpha1.BackupStatus) {
	for _, act := range backupStatus.Actions {
		if act.TotalSize != "" && backupStatus.TotalSize == "" {
			backupStatus.TotalSize = act.TotalSize
		}
		if act.TimeRange != nil && backupStatus.TimeRange == nil {
			backupStatus.TimeRange = act.TimeRange
		}
	}
}

func setEncryptedSystemAccountsAnnotation(request *dpbackup.Request, cluster *kbappsv1.Cluster) error {
	usernameKey := constant.AccountNameForSecret
	passwordKey := constant.AccountPasswdForSecret
	isSystemAccountSecret := func(secret *corev1.Secret) bool {
		username := secret.Data[usernameKey]
		password := secret.Data[passwordKey]
		return username != nil && password != nil
	}
	// fetch secret objects
	objectList, err := listObjectsOfCluster(request.Ctx, request.Client, cluster, &corev1.SecretList{})
	if err != nil {
		return err
	}
	secretList := objectList.(*corev1.SecretList)
	// store the data of secrets in a map data structure, which contains the name of component, the username, and the encrypted password.
	secretMap := map[string]map[string]string{}
	for i := range secretList.Items {
		if !isSystemAccountSecret(&secretList.Items[i]) {
			continue
		}
		componentName := secretList.Items[i].Labels[constant.KBAppComponentLabelKey]
		if componentName == "" {
			componentName = secretList.Items[i].Labels[constant.KBAppShardingNameLabelKey]
		}
		userName := string(secretList.Items[i].Data[usernameKey])
		e := intctrlutil.NewEncryptor(viper.GetString(constant.CfgKeyDPEncryptionKey))
		encryptedPwd, err := e.Encrypt(secretList.Items[i].Data[passwordKey])
		if err != nil {
			return err
		}
		if secretMap[componentName] == nil {
			secretMap[componentName] = make(map[string]string)
		}
		secretMap[componentName][userName] = encryptedPwd
	}
	// convert to string
	secretMapString, err := getObjectString(secretMap)
	if err != nil {
		return err
	}
	if secretMapString != nil && *secretMapString != "" {
		request.Backup.Annotations[constant.EncryptedSystemAccountsAnnotationKey] = *secretMapString
	}
	return nil
}

// getClusterObjectString gets the cluster object and convert it to string.
func getClusterObjectString(cluster *kbappsv1.Cluster) (*string, error) {
	// maintain only the cluster's spec and name/namespace.
	newCluster := &kbappsv1.Cluster{
		Spec: cluster.Spec,
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   cluster.Namespace,
			Name:        cluster.Name,
			Annotations: map[string]string{},
		},
		TypeMeta: cluster.TypeMeta,
	}
	removedAnnotations := map[string]sets.Empty{
		constant.OpsRequestAnnotationKey:   {},
		corev1.LastAppliedConfigAnnotation: {},
	}
	for k, v := range cluster.Annotations {
		if _, ok := removedAnnotations[k]; !ok {
			newCluster.Annotations[k] = v
		}
	}
	clusterString, err := getObjectString(newCluster)
	return clusterString, err
}

// setClusterSnapshotAnnotation sets the snapshot of cluster to the backup's annotations.
func setClusterSnapshotAnnotation(request *dpbackup.Request, cluster *kbappsv1.Cluster) error {
	if request.Backup.Annotations == nil {
		request.Backup.Annotations = map[string]string{}
	}
	clusterString, err := getClusterObjectString(cluster)
	if err != nil {
		return err
	}
	if clusterString == nil {
		return nil
	}
	request.Backup.Annotations[constant.ClusterSnapshotAnnotationKey] = *clusterString
	return nil
}

// validateContinuousBackup validates the continuous backup.
func validateContinuousBackup(backup *dpv1alpha1.Backup, reqCtx intctrlutil.RequestCtx, cli client.Client) error {
	// validate if the continuous backup is created by a backupSchedule.
	if _, ok := backup.Labels[dptypes.BackupScheduleLabelKey]; !ok {
		return fmt.Errorf("continuous backup is only allowed to be created by backupSchedule")
	}
	backupSchedule := &dpv1alpha1.BackupSchedule{}
	if err := cli.Get(reqCtx.Ctx, client.ObjectKey{Name: backup.Labels[dptypes.BackupScheduleLabelKey],
		Namespace: backup.Namespace}, backupSchedule); err != nil {
		return err
	}
	if backupSchedule.Status.Phase != dpv1alpha1.BackupSchedulePhaseAvailable {
		return fmt.Errorf("create continuous backup by failed backupschedule %s/%s",
			backupSchedule.Namespace, backupSchedule.Name)
	}
	return nil
}

// prepare4Incremental prepares for incremental backup
func prepare4Incremental(request *dpbackup.Request) (*dpbackup.Request, error) {
	if request.BackupRepo == nil {
		return nil, fmt.Errorf("backupRepo for incremental backup can't be empty")
	}
	// get and validate parent backup
	parentBackup, err := GetParentBackup(request.Ctx, request.Client, request.Backup, request.BackupMethod, request.BackupRepo.Name)
	if err != nil {
		return nil, err
	}
	parentBackupType, err := dputils.GetBackupTypeByMethodName(request.RequestCtx,
		request.Client, parentBackup.Spec.BackupMethod, request.BackupPolicy)
	if err != nil {
		return nil, err
	}
	request.ParentBackup = parentBackup
	// get and validate base backup
	switch parentBackupType {
	case dpv1alpha1.BackupTypeFull:
		request.BaseBackup = request.ParentBackup
	case dpv1alpha1.BackupTypeIncremental:
		baseBackup := &dpv1alpha1.Backup{}
		baseBackupName := request.ParentBackup.Status.BaseBackupName
		if len(baseBackupName) == 0 {
			return nil, fmt.Errorf("backup %s/%s base backup name is empty",
				request.ParentBackup.Namespace, request.ParentBackup.Name)
		}
		if err := request.Client.Get(request.Ctx, client.ObjectKey{Name: baseBackupName,
			Namespace: request.ParentBackup.Namespace}, baseBackup); err != nil {
			return nil, fmt.Errorf("failed to get base backup %s/%s: %w", request.ParentBackup.Namespace, baseBackupName, err)
		}
		request.BaseBackup = baseBackup
	default:
		return nil, fmt.Errorf("parent backup type is %s, but only full and incremental backup are supported", parentBackupType)
	}
	return request, nil
}
