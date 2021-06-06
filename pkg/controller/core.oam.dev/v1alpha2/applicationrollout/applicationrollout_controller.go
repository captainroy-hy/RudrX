/*
Copyright 2021 The KubeVela Authors.

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

package applicationrollout

import (
	"context"
	"time"

	"github.com/crossplane/crossplane-runtime/pkg/event"
	"github.com/crossplane/crossplane-runtime/pkg/meta"
	"github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/oam-dev/kubevela/apis/core.oam.dev/v1beta1"
	"github.com/oam-dev/kubevela/apis/standard.oam.dev/v1alpha1"
	"github.com/oam-dev/kubevela/pkg/controller/common/rollout"
	controller "github.com/oam-dev/kubevela/pkg/controller/core.oam.dev"
	"github.com/oam-dev/kubevela/pkg/controller/core.oam.dev/v1alpha2/application/dispatch"
	"github.com/oam-dev/kubevela/pkg/oam/discoverymapper"
	oamutil "github.com/oam-dev/kubevela/pkg/oam/util"
)

const (
	errUpdateAppRollout = "failed to update the app rollout"

	appRolloutFinalizer = "finalizers.approllout.oam.dev"

	reconcileTimeOut = 60 * time.Second
)

// Reconciler reconciles an AppRollout object
type Reconciler struct {
	client.Client
	dm     discoverymapper.DiscoveryMapper
	record event.Recorder
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=core.oam.dev,resources=approllouts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.oam.dev,resources=approllouts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=core.oam.dev,resources=applications,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core.oam.dev,resources=applications/status,verbs=get;update;patch

// Reconcile is the main logic of appRollout controller
// nolint:gocyclo
func (r *Reconciler) Reconcile(req ctrl.Request) (res reconcile.Result, retErr error) {
	var appRollout v1beta1.AppRollout
	ctx, cancel := context.WithTimeout(context.TODO(), reconcileTimeOut)
	defer cancel()
	ctx = oamutil.SetNamespaceInCtx(ctx, req.Namespace)

	startTime := time.Now()
	defer func() {
		if retErr == nil {
			if res.Requeue || res.RequeueAfter > 0 {
				klog.InfoS("Finished reconciling appRollout", "controller request", req, "time spent",
					time.Since(startTime), "result", res)
			} else {
				klog.InfoS("Finished reconcile appRollout", "controller  request", req, "time spent",
					time.Since(startTime))
			}
		} else {
			klog.Errorf("Failed to reconcile appRollout %s: %v", req, retErr)
		}
	}()
	if err := r.Get(ctx, req.NamespacedName, &appRollout); err != nil {
		if apierrors.IsNotFound(err) {
			klog.InfoS("appRollout does not exist", "appRollout", klog.KRef(req.Namespace, req.Name))
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	klog.InfoS("Start to reconcile ", "appRollout", klog.KObj(&appRollout))

	// handle app Finalizer
	doneReconcile, res, retErr := r.handleFinalizer(ctx, &appRollout)
	if doneReconcile {
		return res, retErr
	}

	reconRes, err := r.DoReconcile(ctx, &appRollout)
	if err != nil {
		return reconcile.Result{}, err
	}
	return reconRes, r.updateStatus(ctx, &appRollout)
}

// DoReconcile is real reconcile logic for appRollout.
// !!! Note the AppRollout object should not be updated in this function as it could be logically used in Application reconcile loop which does not have real AppRollout object.
func (r *Reconciler) DoReconcile(ctx context.Context, appRollout *v1beta1.AppRollout) (res reconcile.Result, retErr error) {
	if len(appRollout.Status.RollingState) == 0 {
		appRollout.Status.ResetStatus()
	}
	targetAppRevisionName := appRollout.Spec.TargetAppRevisionName
	sourceAppRevisionName := appRollout.Spec.SourceAppRevisionName

	// no need to proceed if rollout is already in a terminal state and there is no source/target change
	doneReconcile := r.handleRollingTerminated(*appRollout, targetAppRevisionName, sourceAppRevisionName)
	if doneReconcile {
		return reconcile.Result{}, nil
	}

	// handle rollout target/source change (only if it's not deleting already)
	if isRolloutModified(*appRollout) {
		klog.InfoS("rollout target changed, restart the rollout", "new source", sourceAppRevisionName,
			"new target", targetAppRevisionName)
		r.record.Event(appRollout, event.Normal("Rollout Restarted",
			"rollout target changed, restart the rollout", "new source", sourceAppRevisionName,
			"new target", targetAppRevisionName))
		// we are okay to move directly to restart the rollout since we are at the terminal state
		// however, we need to make sure we properly finalizing the existing rollout before restart if it's
		// still in the middle of rolling out
		if appRollout.Status.RollingState != v1alpha1.RolloutSucceedState &&
			appRollout.Status.RollingState != v1alpha1.RolloutFailedState {
			// continue to handle the previous resources until we are okay to move forward
			targetAppRevisionName = appRollout.Status.LastUpgradedTargetAppRevision
			sourceAppRevisionName = appRollout.Status.LastSourceAppRevision
		} else {
			// mark so that we don't think we are modified again
			appRollout.Status.LastUpgradedTargetAppRevision = targetAppRevisionName
			appRollout.Status.LastSourceAppRevision = sourceAppRevisionName
		}
		appRollout.Status.StateTransition(v1alpha1.RollingModifiedEvent)
	}

	var sourceAppRev, targetAppRev *v1beta1.ApplicationRevision
	var sourceWorkload, targetWorkload *unstructured.Unstructured
	var err error

	if appRollout.Status.RollingState == v1alpha1.RolloutDeletingState {
		if sourceAppRevisionName == "" {
			klog.InfoS("source app fields not filled, this is a scale operation", "appRollout", klog.KRef(appRollout.Namespace, appRollout.Name))
		} else {
			sourceAppRev, err = r.getAppRevision(ctx, sourceAppRevisionName)
			if err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
		}
		targetAppRev, err = r.getAppRevision(ctx, targetAppRevisionName)
		if err != nil {
			return ctrl.Result{}, err
		}

		// TODO check resource trackers are gone
		// if sourceApp == nil && targetApp == nil {
		// if true {
		//     klog.InfoS("Both the target and the source app are gone", "appRollout",
		//         klog.KRef(appRollout.Namespace, appRollout.Name), "rolling state", appRollout.Status.RollingState)
		//     appRollout.Status.StateTransition(v1alpha1.RollingFinalizedEvent)
		//     // update the appRollout status
		//     return ctrl.Result{}, nil
		// }
	} else {
		if sourceAppRevisionName == "" {
			klog.Info("source app fields not filled, this is a scale operation")
		} else {
			sourceAppRev, err = r.getAppRevision(ctx, sourceAppRevisionName)
			if err != nil {
				return ctrl.Result{}, err
			}
			if appRollout.Status.RollingState == v1alpha1.LocatingTargetAppState &&
				sourceAppRev != nil {
				if err := r.emitAppRevisionForRollout(ctx, sourceAppRev, nil); err != nil {
					return ctrl.Result{}, err
				}
			}
		}
		targetAppRev, err = r.getAppRevision(ctx, targetAppRevisionName)
		if err != nil {
			return ctrl.Result{}, err
		}
		if appRollout.Status.RollingState == v1alpha1.LocatingTargetAppState {
			if err := r.emitAppRevisionForRollout(ctx, targetAppRev, sourceAppRev); err != nil {
				return ctrl.Result{}, err
			}
			appRollout.Status.StateTransition(v1alpha1.AppLocatedEvent)
		}
	}

	if sourceAppRev != nil {
		sourceWorkload, _ = getWorkload(sourceAppRev)
		klog.InfoS("get the source workload we need to work on", "sourceWorkload", klog.KObj(sourceWorkload))
	}
	targetWorkload, _ = getWorkload(targetAppRev)
	klog.InfoS("get the target workload we need to work on", "targetWorkload", klog.KObj(targetWorkload))

	// reconcile the rollout part of the spec given the target and source workload
	rolloutPlanController := rollout.NewRolloutPlanController(r, appRollout, r.record,
		&appRollout.Spec.RolloutPlan, &appRollout.Status.RolloutStatus, targetWorkload, sourceWorkload)
	result, rolloutStatus := rolloutPlanController.Reconcile(ctx)
	// make sure that the new status is copied back
	appRollout.Status.RolloutStatus = *rolloutStatus
	// do not update the last with new revision if we are still trying to abandon the previous rollout
	if rolloutStatus.RollingState != v1alpha1.RolloutAbandoningState {
		appRollout.Status.LastUpgradedTargetAppRevision = appRollout.Spec.TargetAppRevisionName
		appRollout.Status.LastSourceAppRevision = appRollout.Spec.SourceAppRevisionName
	}
	if rolloutStatus.RollingState == v1alpha1.RolloutSucceedState {
		klog.InfoS("rollout succeeded, record the source and target app revision", "source", sourceAppRevisionName,
			"target", targetAppRevisionName)
		if err = r.finalizeRollingSucceeded(ctx, sourceAppRev, targetAppRev); err != nil {
			return ctrl.Result{}, err
		}
	} else if rolloutStatus.RollingState == v1alpha1.RolloutFailedState {
		klog.InfoS("rollout failed, record the source and target app revision", "source", sourceAppRevisionName,
			"target", targetAppRevisionName, "revert on deletion", appRollout.Spec.RevertOnDelete)

	}
	// update the appRollout status
	return result, nil
}

// check if either the source or the target of the appRollout has changed
func isRolloutModified(appRollout v1beta1.AppRollout) bool {
	return appRollout.Status.RollingState != v1alpha1.RolloutDeletingState &&
		((appRollout.Status.LastUpgradedTargetAppRevision != "" &&
			appRollout.Status.LastUpgradedTargetAppRevision != appRollout.Spec.TargetAppRevisionName) ||
			(appRollout.Status.LastSourceAppRevision != "" &&
				appRollout.Status.LastSourceAppRevision != appRollout.Spec.SourceAppRevisionName))
}

// handle adding and handle finalizer logic, it turns if we should continue to reconcile
func (r *Reconciler) handleFinalizer(ctx context.Context, appRollout *v1beta1.AppRollout) (bool, reconcile.Result, error) {
	if appRollout.DeletionTimestamp.IsZero() {
		if !meta.FinalizerExists(&appRollout.ObjectMeta, appRolloutFinalizer) {
			meta.AddFinalizer(&appRollout.ObjectMeta, appRolloutFinalizer)
			klog.InfoS("Register new app rollout finalizers", "rollout", appRollout.Name,
				"finalizers", appRollout.ObjectMeta.Finalizers)
			return true, reconcile.Result{}, errors.Wrap(r.Update(ctx, appRollout), errUpdateAppRollout)
		}
	} else if meta.FinalizerExists(&appRollout.ObjectMeta, appRolloutFinalizer) {
		if appRollout.Status.RollingState == v1alpha1.RolloutSucceedState {
			klog.InfoS("Safe to delete the succeeded rollout", "rollout", appRollout.Name)
			meta.RemoveFinalizer(&appRollout.ObjectMeta, appRolloutFinalizer)
			return true, reconcile.Result{}, errors.Wrap(r.Update(ctx, appRollout), errUpdateAppRollout)
		}
		if appRollout.Status.RollingState == v1alpha1.RolloutFailedState {
			klog.InfoS("delete the rollout in deleted state", "rollout", appRollout.Name)
			if appRollout.Spec.RevertOnDelete {
				klog.InfoS("need to revert the failed rollout", "rollout", appRollout.Name)
			}
			meta.RemoveFinalizer(&appRollout.ObjectMeta, appRolloutFinalizer)
			return true, reconcile.Result{}, errors.Wrap(r.Update(ctx, appRollout), errUpdateAppRollout)
		}
		// still need to finalize
		klog.Info("perform clean up", "app rollout", appRollout.Name)
		r.record.Event(appRollout, event.Normal("Rollout ", "rollout target deleted, release the resources"))
		appRollout.Status.StateTransition(v1alpha1.RollingDeletedEvent)
	}
	return false, reconcile.Result{}, nil
}

func (r *Reconciler) handleRollingTerminated(appRollout v1beta1.AppRollout, targetAppRevisionName string,
	sourceAppRevisionName string) bool {
	// handle rollout completed
	if appRollout.Status.RollingState == v1alpha1.RolloutSucceedState ||
		appRollout.Status.RollingState == v1alpha1.RolloutFailedState {
		if appRollout.Status.LastUpgradedTargetAppRevision == targetAppRevisionName &&
			appRollout.Status.LastSourceAppRevision == sourceAppRevisionName {
			klog.InfoS("rollout completed, no need to reconcile", "source", sourceAppRevisionName,
				"target", targetAppRevisionName)
			return true
		}
	}
	return false
}

func (r *Reconciler) finalizeRollingSucceeded(ctx context.Context, sourceAppRev, targetAppRev *v1beta1.ApplicationRevision) error {
	m, err := getAssembledManifests(targetAppRev, false)
	if err != nil {
		return err
	}
	d := dispatch.NewAppManifestsDispatcher(r.Client, targetAppRev)
	if sourceAppRev != nil {
		rt := &v1beta1.ResourceTracker{}
		rt.SetName(dispatch.ConstructResourceTrackerName(sourceAppRev.Name, sourceAppRev.Namespace))
		d = d.EnableGC(rt)
	}
	if _, err := d.Dispatch(ctx, m); err != nil {
		return errors.WithMessage(err, "cannot garbage collect source app revision")
	}
	return nil
}

// UpdateStatus updates v1alpha2.AppRollout's Status with retry.RetryOnConflict
func (r *Reconciler) updateStatus(ctx context.Context, appRollout *v1beta1.AppRollout) error {
	status := appRollout.DeepCopy().Status
	return retry.RetryOnConflict(retry.DefaultBackoff, func() (err error) {
		if err = r.Get(ctx, client.ObjectKey{Namespace: appRollout.Namespace, Name: appRollout.Name}, appRollout); err != nil {
			return
		}
		appRollout.Status = status
		return r.Status().Update(ctx, appRollout)
	})
}

// NewReconciler render a applicationRollout reconciler
func NewReconciler(c client.Client, dm discoverymapper.DiscoveryMapper, record event.Recorder, scheme *runtime.Scheme) *Reconciler {
	return &Reconciler{
		c,
		dm,
		record,
		scheme,
	}
}

// SetupWithManager setup the controller with manager
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.record = event.NewAPIRecorder(mgr.GetEventRecorderFor("AppRollout")).
		WithAnnotations("controller", "AppRollout")
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.AppRollout{}).
		Owns(&v1beta1.Application{}).
		Complete(r)
}

// Setup adds a controller that reconciles AppRollout.
func Setup(mgr ctrl.Manager, args controller.Args) error {
	reconciler := Reconciler{
		Client: mgr.GetClient(),
		dm:     args.DiscoveryMapper,
		Scheme: mgr.GetScheme(),
	}
	return reconciler.SetupWithManager(mgr)
}
